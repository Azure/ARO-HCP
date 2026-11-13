// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mockfparoleassignments

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks/v2"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/roleassignment"
	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/azureresourcesapihelpers"
)

const (
	testManagedRGScope     = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-mrg"
	testMockFPAPrincipalID = "mock-fpa-principal"
	roleAssignmentInfix    = "/providers/Microsoft.Authorization/roleAssignments/"
)

func mustReservedID(t *testing.T, name string) *azcorearm.ResourceID {
	t.Helper()
	id, err := azcorearm.ParseResourceID(testManagedRGScope + roleAssignmentInfix + name)
	require.NoError(t, err, "failed to parse reserved role assignment resource ID")
	return id
}

func mustScope(t *testing.T) *azcorearm.ResourceID {
	t.Helper()
	scope, err := azcorearm.ParseResourceID(testManagedRGScope)
	require.NoError(t, err, "failed to parse managed resource group scope")
	return scope
}

// desireFor builds a mock FPA DeploymentStackDesire whose Spec renders rendered, and,
// when observed is true, whose Status reports the spec as observed (matching hash), the
// named assignments as Managed, and (when applied) a successful Apply.
func desireFor(t *testing.T, scope *azcorearm.ResourceID, rendered map[coreapi.RoleAssignmentKey]string, managedNames []string, observed, applied bool) *azureresourcesapi.DeploymentStackDesire {
	t.Helper()
	spec, err := desiredDeploymentStackDesireSpec(scope, rendered)
	require.NoError(t, err, "failed to build desired spec")

	desire := &azureresourcesapi.DeploymentStackDesire{Spec: spec}
	if !observed {
		return desire
	}

	hash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(spec)
	require.NoError(t, err, "failed to hash spec")
	desire.Status.ObservedSpecHash = hash

	resources := make([]*armdeploymentstacks.ManagedResourceReference, 0, len(managedNames))
	for _, name := range managedNames {
		id := scope.String() + roleAssignmentInfix + name
		resources = append(resources, &armdeploymentstacks.ManagedResourceReference{
			ID:     ptr.To(id),
			Status: ptr.To(armdeploymentstacks.ResourceStatusModeManaged),
		})
	}
	desire.Status.Stack = &armdeploymentstacks.DeploymentStack{
		Properties: &armdeploymentstacks.DeploymentStackProperties{Resources: resources},
	}
	if applied {
		desire.Status.Conditions = []metav1.Condition{{
			Type:               azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied,
			Status:             metav1.ConditionTrue,
			Reason:             "Applied",
			LastTransitionTime: metav1.Now(),
		}}
	}
	return desire
}

func TestManagedResourceGroupScope(t *testing.T) {
	t.Parallel()

	confirmed := mustReservedID(t, "confirmed")
	pending := mustReservedID(t, "pending")

	t.Run("prefers the confirmed AzureResource", func(t *testing.T) {
		t.Parallel()
		scope := managedResourceGroupScope(coreapi.AzureReference{AzureResource: confirmed, PendingAzureResource: pending})
		assert.Same(t, confirmed, scope, "confirmed AzureResource is preferred over pending")
	})

	t.Run("falls back to PendingAzureResource", func(t *testing.T) {
		t.Parallel()
		scope := managedResourceGroupScope(coreapi.AzureReference{PendingAzureResource: pending})
		assert.Same(t, pending, scope, "pending reference is used when no confirmed one exists")
	})

	t.Run("nil when neither reference is set", func(t *testing.T) {
		t.Parallel()
		scope := managedResourceGroupScope(coreapi.AzureReference{})
		assert.Nil(t, scope, "no scope when the MRG is absent")
	})
}

func TestRoleAssignmentTemplateResources(t *testing.T) {
	t.Parallel()

	keyA := coreapi.RoleAssignmentKey{PrincipalID: "aaaa", RoleDefinitionResourceID: OwnerRoleDefinitionResourceID}
	keyB := coreapi.RoleAssignmentKey{PrincipalID: "bbbb", RoleDefinitionResourceID: OwnerRoleDefinitionResourceID}

	t.Run("renders one resource per key, sorted by principal", func(t *testing.T) {
		t.Parallel()
		resources := roleAssignmentTemplateResources(map[coreapi.RoleAssignmentKey]string{
			keyB: "name-b",
			keyA: "name-a",
		})
		require.Len(t, resources, 2, "one resource per key")
		// Sorted by principal, so A comes before B, making the spec hash stable.
		assert.Equal(t, "name-a", resources[0]["name"], "first resource is the lexicographically smaller principal")
		assert.Equal(t, "name-b", resources[1]["name"], "second resource is the larger principal")
		assert.Equal(t, "Microsoft.Authorization/roleAssignments", resources[0]["type"])
		props := resources[0]["properties"].(map[string]any)
		assert.Equal(t, "aaaa", props["principalId"])
		assert.Equal(t, OwnerRoleDefinitionResourceID, props["roleDefinitionId"])
		assert.Equal(t, "ServicePrincipal", props["principalType"])
	})

	t.Run("empty input renders no resources", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, roleAssignmentTemplateResources(map[coreapi.RoleAssignmentKey]string{}), "no keys means no resources")
	})
}

func TestNextRenderedRoleAssignments(t *testing.T) {
	t.Parallel()

	scope := mustScope(t)
	syncer := &mockFPARoleAssignmentsSyncer{
		mockFPAPrincipalID:              testMockFPAPrincipalID,
		desiredRoleDefinitionResourceID: OwnerRoleDefinitionResourceID,
	}
	desiredKey := coreapi.RoleAssignmentKey{PrincipalID: testMockFPAPrincipalID, RoleDefinitionResourceID: OwnerRoleDefinitionResourceID}
	desiredName := reserveRoleAssignmentName(scope, desiredKey)

	oldKey := coreapi.RoleAssignmentKey{PrincipalID: "old-principal", RoleDefinitionResourceID: OwnerRoleDefinitionResourceID}
	oldName := reserveRoleAssignmentName(scope, oldKey)
	olderKey := coreapi.RoleAssignmentKey{PrincipalID: "older-principal", RoleDefinitionResourceID: OwnerRoleDefinitionResourceID}
	olderName := reserveRoleAssignmentName(scope, olderKey)

	t.Run("fresh cluster reserves the desired key", func(t *testing.T) {
		t.Parallel()
		next, err := syncer.calculateDesiredRoleAssignmentKeys(desiredKey, scope, nil)
		require.NoError(t, err)
		assert.Equal(t, map[coreapi.RoleAssignmentKey]string{desiredKey: desiredName}, next, "only the desired key is rendered, with its deterministic reserved name")
	})

	t.Run("reuses the reserved name already published for the desired key", func(t *testing.T) {
		t.Parallel()
		current := map[coreapi.RoleAssignmentKey]string{desiredKey: desiredName}
		desire := desireFor(t, scope, current, []string{desiredName}, true, true)
		next, err := syncer.calculateDesiredRoleAssignmentKeys(desiredKey, scope, desire)
		require.NoError(t, err)
		assert.Equal(t, map[coreapi.RoleAssignmentKey]string{desiredKey: desiredName}, next, "desired key keeps its published name; nothing to drain")
	})

	t.Run("keeps a superseded key live until the desired one is confirmed", func(t *testing.T) {
		t.Parallel()
		// The desired key is not in the published template yet, so it cannot be confirmed.
		current := map[coreapi.RoleAssignmentKey]string{oldKey: oldName}
		desire := desireFor(t, scope, current, []string{oldName}, true, true)
		next, err := syncer.calculateDesiredRoleAssignmentKeys(desiredKey, scope, desire)
		require.NoError(t, err)
		assert.Equal(t, map[coreapi.RoleAssignmentKey]string{
			desiredKey: desiredName,
			oldKey:     oldName,
		}, next, "the superseded key stays rendered (grant stays live) while the replacement is added")
	})

	t.Run("drops a superseded key once the desired one is confirmed live", func(t *testing.T) {
		t.Parallel()
		current := map[coreapi.RoleAssignmentKey]string{desiredKey: desiredName, oldKey: oldName}
		// Both are managed and the apply succeeded, so the desired key is confirmed.
		desire := desireFor(t, scope, current, []string{desiredName, oldName}, true, true)
		next, err := syncer.calculateDesiredRoleAssignmentKeys(desiredKey, scope, desire)
		require.NoError(t, err)
		assert.Equal(t, map[coreapi.RoleAssignmentKey]string{desiredKey: desiredName}, next, "the superseded key is excluded so the stack deletes it")
	})

	t.Run("does not drop a superseded key while the apply has not yet succeeded", func(t *testing.T) {
		t.Parallel()
		current := map[coreapi.RoleAssignmentKey]string{desiredKey: desiredName, oldKey: oldName}
		// Observed but not applied: not yet confirmed.
		desire := desireFor(t, scope, current, []string{desiredName, oldName}, true, false)
		next, err := syncer.calculateDesiredRoleAssignmentKeys(desiredKey, scope, desire)
		require.NoError(t, err)
		assert.Equal(t, map[coreapi.RoleAssignmentKey]string{
			desiredKey: desiredName,
			oldKey:     oldName,
		}, next, "without a successful apply the superseded key is kept")
	})

	t.Run("transitive replacement unions all superseded keys until the desired one is confirmed", func(t *testing.T) {
		t.Parallel()
		// A to B to C: two superseded keys are still live and the desired key is new.
		current := map[coreapi.RoleAssignmentKey]string{olderKey: olderName, oldKey: oldName}
		desire := desireFor(t, scope, current, []string{olderName, oldName}, true, true)
		next, err := syncer.calculateDesiredRoleAssignmentKeys(desiredKey, scope, desire)
		require.NoError(t, err)
		assert.Equal(t, map[coreapi.RoleAssignmentKey]string{
			desiredKey: desiredName,
			oldKey:     oldName,
			olderKey:   olderName,
		}, next, "every superseded key stays rendered until the replacement is confirmed; no grant is lost and none are orphaned")
	})
}

func TestReservedRoleAssignmentName(t *testing.T) {
	t.Parallel()
	scope := mustScope(t)
	key := coreapi.RoleAssignmentKey{PrincipalID: testMockFPAPrincipalID, RoleDefinitionResourceID: OwnerRoleDefinitionResourceID}
	got := reserveRoleAssignmentName(scope, key)
	want := roleassignment.GenerateManagedResourceGroupScopedRoleAssignmentName(scope.String(), key.PrincipalID, key.RoleDefinitionResourceID)
	assert.Equal(t, want, got, "reserved name must match the deterministic generator")
}
