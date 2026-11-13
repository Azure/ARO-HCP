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

package coreapihelpers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks/v2"

	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/azureresourcesapihelpers"
)

const (
	testRoleDefinitionResourceID  = "/providers/Microsoft.Authorization/roleDefinitions/00000000-0000-0000-0000-000000000000"
	testRoleDefinitionResourceID2 = "/providers/Microsoft.Authorization/roleDefinitions/11111111-1111-1111-1111-111111111111"
	testPrincipalID               = "test-principal"
	testPrincipalID2              = "test-principal-2"
	testManagedRG                 = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-managed-rg"
	roleAssignmentInfix           = "/providers/Microsoft.Authorization/roleAssignments/"
	testNameA                     = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	testNameB                     = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
)

type tmplResource struct {
	// resourceType overrides the ARM resource type; empty means a role assignment.
	resourceType string
	name         string
	principalID  string
	roleDefID    string
}

// desireWithTemplate builds a DeploymentStackDesire whose Spec template declares the
// given role assignment resources, mirroring what the mock FPA controller publishes.
func desireWithTemplate(resources ...tmplResource) *azureresourcesapi.DeploymentStackDesire {
	arm := make([]map[string]any, 0, len(resources))
	for _, r := range resources {
		resourceType := r.resourceType
		if resourceType == "" {
			resourceType = "Microsoft.Authorization/roleAssignments"
		}
		arm = append(arm, map[string]any{
			"type":       resourceType,
			"apiVersion": "2022-04-01",
			"name":       r.name,
			"properties": map[string]any{
				"principalId":      r.principalID,
				"roleDefinitionId": r.roleDefID,
				"principalType":    "ServicePrincipal",
			},
		})
	}
	return &azureresourcesapi.DeploymentStackDesire{
		Spec: azureresourcesapi.DeploymentStackDesireSpec{
			Stack: &armdeploymentstacks.DeploymentStack{
				Properties: &armdeploymentstacks.DeploymentStackProperties{
					Template: map[string]any{
						"$schema":        "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
						"contentVersion": "1.0.0.0",
						"resources":      arm,
					},
				},
			},
		},
	}
}

// observeApplied marks desire as observed against its current spec, with a successful
// Apply, reporting each of managedNames as Managed in the stack.
func observeApplied(t *testing.T, desire *azureresourcesapi.DeploymentStackDesire, managedNames ...string) {
	t.Helper()
	hash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(desire.Spec)
	require.NoError(t, err, "failed to hash spec")
	desire.Status.ObservedSpecHash = hash

	resources := make([]*armdeploymentstacks.ManagedResourceReference, 0, len(managedNames))
	for _, name := range managedNames {
		id := testManagedRG + roleAssignmentInfix + name
		resources = append(resources, &armdeploymentstacks.ManagedResourceReference{
			ID:     ptr.To(id),
			Status: ptr.To(armdeploymentstacks.ResourceStatusModeManaged),
		})
	}
	desire.Status.Stack = &armdeploymentstacks.DeploymentStack{
		Properties: &armdeploymentstacks.DeploymentStackProperties{Resources: resources},
	}
	desire.Status.Conditions = []metav1.Condition{{
		Type:               azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied,
		Status:             metav1.ConditionTrue,
		Reason:             "Applied",
		LastTransitionTime: metav1.Now(),
	}}
}

func TestRenderedMockFPARoleAssignments(t *testing.T) {
	t.Run("nil desire renders nothing", func(t *testing.T) {
		got, err := MockFPARoleAssignmentsInSpecTemplate(nil)
		require.NoError(t, err)
		assert.Empty(t, got, "a nil desire has no rendered assignments")
	})

	t.Run("desire without a template renders nothing", func(t *testing.T) {
		got, err := MockFPARoleAssignmentsInSpecTemplate(&azureresourcesapi.DeploymentStackDesire{})
		require.NoError(t, err)
		assert.Empty(t, got, "a desire with no template has no rendered assignments")
	})

	t.Run("maps each resource to its key and reserved name", func(t *testing.T) {
		desire := desireWithTemplate(
			tmplResource{name: testNameA, principalID: testPrincipalID, roleDefID: testRoleDefinitionResourceID},
			tmplResource{name: testNameB, principalID: testPrincipalID2, roleDefID: testRoleDefinitionResourceID},
		)
		got, err := MockFPARoleAssignmentsInSpecTemplate(desire)
		require.NoError(t, err)
		assert.Equal(t, map[coreapi.RoleAssignmentKey]string{
			{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}:  testNameA,
			{PrincipalID: testPrincipalID2, RoleDefinitionResourceID: testRoleDefinitionResourceID}: testNameB,
		}, got, "each template resource is recovered as a key and its reserved name")
	})

	t.Run("errors on duplicate keys naming different assignments", func(t *testing.T) {
		desire := desireWithTemplate(
			tmplResource{name: testNameA, principalID: testPrincipalID, roleDefID: testRoleDefinitionResourceID},
			tmplResource{name: testNameB, principalID: testPrincipalID, roleDefID: testRoleDefinitionResourceID},
		)
		got, err := MockFPARoleAssignmentsInSpecTemplate(desire)
		require.Error(t, err, "a key naming two different assignments is ambiguous and must error, not drop one name")
		assert.Nil(t, got, "no partial ledger is returned on error")
	})

	t.Run("tolerates a duplicate key repeating the same name", func(t *testing.T) {
		desire := desireWithTemplate(
			tmplResource{name: testNameA, principalID: testPrincipalID, roleDefID: testRoleDefinitionResourceID},
			tmplResource{name: testNameA, principalID: testPrincipalID, roleDefID: testRoleDefinitionResourceID},
		)
		got, err := MockFPARoleAssignmentsInSpecTemplate(desire)
		require.NoError(t, err, "an identical repeated entry is a harmless no-op")
		assert.Equal(t, map[coreapi.RoleAssignmentKey]string{
			{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}: testNameA,
		}, got, "the single assignment is recovered once")
	})

	t.Run("errors on an unexpected resource type", func(t *testing.T) {
		desire := desireWithTemplate(
			tmplResource{name: testNameA, principalID: testPrincipalID, roleDefID: testRoleDefinitionResourceID},
			tmplResource{resourceType: "Microsoft.Resources/deploymentScripts", name: testNameB, principalID: testPrincipalID2, roleDefID: testRoleDefinitionResourceID},
		)
		got, err := MockFPARoleAssignmentsInSpecTemplate(desire)
		require.Error(t, err, "an unexpected resource type must surface as an error")
		assert.Nil(t, got, "no partial ledger is returned on error")
	})

	t.Run("matches the role assignment type case-insensitively", func(t *testing.T) {
		desire := desireWithTemplate(
			tmplResource{resourceType: "microsoft.authorization/roleassignments", name: testNameA, principalID: testPrincipalID, roleDefID: testRoleDefinitionResourceID},
		)
		got, err := MockFPARoleAssignmentsInSpecTemplate(desire)
		require.NoError(t, err, "ARM resource types are compared case-insensitively")
		assert.Equal(t, map[coreapi.RoleAssignmentKey]string{
			{PrincipalID: testPrincipalID, RoleDefinitionResourceID: testRoleDefinitionResourceID}: testNameA,
		}, got, "a differently-cased role assignment type is still recovered")
	})

	t.Run("errors on a role assignment missing a name, principal, or role definition", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			resource tmplResource
		}{
			{"missing name", tmplResource{name: "", principalID: testPrincipalID, roleDefID: testRoleDefinitionResourceID}},
			{"missing principal", tmplResource{name: testNameA, principalID: "", roleDefID: testRoleDefinitionResourceID}},
			{"missing role definition", tmplResource{name: testNameB, principalID: testPrincipalID, roleDefID: ""}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got, err := MockFPARoleAssignmentsInSpecTemplate(desireWithTemplate(tc.resource))
				require.Error(t, err, "a malformed role assignment must surface as an error, not be silently dropped")
				assert.Nil(t, got, "no partial ledger is returned on error")
			})
		}
	})
}

// TestMockFPARoleAssignmentsSpecHashStability locks down the invariant the mock FPA
// controller relies on to avoid republish churn: reusing an already-published name for a
// key leaves the spec hash unchanged, while changing a name changes it. This matters most
// once reserved names become fully random (UUIDv4): with deterministic names a lost name
// is re-derived identically, but with random names the controller must reuse the exact
// published name to keep the hash stable, otherwise every reconcile mints a new name and
// thrashes Azure.
//
// The hash IS sensitive to the order of the resources array: JSON canonicalization
// normalizes object key order, not array element order. Array-order stability instead
// comes from the builder sorting keys by (principal, roleDefinition) before emitting the
// template, which stays deterministic even once names are random.
func TestMockFPARoleAssignmentsSpecHashStability(t *testing.T) {
	hashOf := func(t *testing.T, desire *azureresourcesapi.DeploymentStackDesire) string {
		t.Helper()
		h, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(desire.Spec)
		require.NoError(t, err, "hashing the mock FPA DeploymentStackDesire spec")
		return h
	}

	t.Run("reusing the same name keeps the hash stable", func(t *testing.T) {
		base := hashOf(t, desireWithTemplate(
			tmplResource{name: testNameA, principalID: testPrincipalID, roleDefID: testRoleDefinitionResourceID},
		))
		same := hashOf(t, desireWithTemplate(
			tmplResource{name: testNameA, principalID: testPrincipalID, roleDefID: testRoleDefinitionResourceID},
		))
		assert.Equal(t, base, same, "an unchanged key/name set must hash identically so no republish happens")
	})

	t.Run("changing a reserved name changes the hash", func(t *testing.T) {
		base := hashOf(t, desireWithTemplate(
			tmplResource{name: testNameA, principalID: testPrincipalID, roleDefID: testRoleDefinitionResourceID},
		))
		changed := hashOf(t, desireWithTemplate(
			tmplResource{name: testNameB, principalID: testPrincipalID, roleDefID: testRoleDefinitionResourceID},
		))
		assert.NotEqual(t, base, changed, "a different reserved name must change the hash so a genuine update is detected")
	})

	t.Run("resource array order affects the hash, so the builder must sort", func(t *testing.T) {
		ab := hashOf(t, desireWithTemplate(
			tmplResource{name: testNameA, principalID: testPrincipalID, roleDefID: testRoleDefinitionResourceID},
			tmplResource{name: testNameB, principalID: testPrincipalID2, roleDefID: testRoleDefinitionResourceID},
		))
		ba := hashOf(t, desireWithTemplate(
			tmplResource{name: testNameB, principalID: testPrincipalID2, roleDefID: testRoleDefinitionResourceID},
			tmplResource{name: testNameA, principalID: testPrincipalID, roleDefID: testRoleDefinitionResourceID},
		))
		assert.NotEqual(t, ab, ba, "array element order is not canonicalized, which is why the builder sorts keys by (principal, roleDefinition) for a stable hash")
	})
}

func TestMockFPARoleAssignmentConfigured(t *testing.T) {
	t.Run("empty name is not configured", func(t *testing.T) {
		got, err := MockFPARoleAssignmentConfigured(desireWithTemplate(), "")
		require.NoError(t, err)
		assert.False(t, got, "an empty reserved name was never published")
	})

	t.Run("nil desire is not configured", func(t *testing.T) {
		got, err := MockFPARoleAssignmentConfigured(nil, testNameA)
		require.NoError(t, err)
		assert.False(t, got, "a nil desire has not been published")
	})

	t.Run("managed, applied, and current is configured", func(t *testing.T) {
		desire := desireWithTemplate(tmplResource{name: testNameA, principalID: testPrincipalID, roleDefID: testRoleDefinitionResourceID})
		observeApplied(t, desire, testNameA)
		got, err := MockFPARoleAssignmentConfigured(desire, testNameA)
		require.NoError(t, err)
		assert.True(t, got, "the assignment is live per a current, successful apply")
	})

	t.Run("applied but name not managed is not configured", func(t *testing.T) {
		desire := desireWithTemplate(tmplResource{name: testNameA, principalID: testPrincipalID, roleDefID: testRoleDefinitionResourceID})
		observeApplied(t, desire) // nothing managed
		got, err := MockFPARoleAssignmentConfigured(desire, testNameA)
		require.NoError(t, err)
		assert.False(t, got, "the assignment is not among the managed resources")
	})

	t.Run("stale observation is not configured", func(t *testing.T) {
		desire := desireWithTemplate(tmplResource{name: testNameA, principalID: testPrincipalID, roleDefID: testRoleDefinitionResourceID})
		observeApplied(t, desire, testNameA)
		desire.Status.ObservedSpecHash = "does-not-match"
		got, err := MockFPARoleAssignmentConfigured(desire, testNameA)
		require.NoError(t, err)
		assert.False(t, got, "the observed outcome is for a different spec")
	})

	t.Run("observed but not applied is not configured", func(t *testing.T) {
		desire := desireWithTemplate(tmplResource{name: testNameA, principalID: testPrincipalID, roleDefID: testRoleDefinitionResourceID})
		observeApplied(t, desire, testNameA)
		desire.Status.Conditions = nil
		got, err := MockFPARoleAssignmentConfigured(desire, testNameA)
		require.NoError(t, err)
		assert.False(t, got, "the apply has not succeeded")
	})
}

func TestMockFPARoleAssignmentsConfigured(t *testing.T) {
	t.Run("nil desire is not configured", func(t *testing.T) {
		got, err := MockFPARoleAssignmentsConfigured(nil)
		require.NoError(t, err)
		assert.False(t, got, "a nil desire has not been published")
	})

	t.Run("empty template (intent not recorded) is not configured", func(t *testing.T) {
		desire := desireWithTemplate()
		observeApplied(t, desire)
		got, err := MockFPARoleAssignmentsConfigured(desire)
		require.NoError(t, err)
		assert.False(t, got, "no assignments declared yet means nothing is configured")
	})

	t.Run("every rendered assignment managed and applied is configured", func(t *testing.T) {
		desire := desireWithTemplate(
			tmplResource{name: testNameA, principalID: testPrincipalID, roleDefID: testRoleDefinitionResourceID},
			tmplResource{name: testNameB, principalID: testPrincipalID2, roleDefID: testRoleDefinitionResourceID},
		)
		observeApplied(t, desire, testNameA, testNameB)
		got, err := MockFPARoleAssignmentsConfigured(desire)
		require.NoError(t, err)
		assert.True(t, got, "all rendered assignments are live per a current, successful apply")
	})

	t.Run("one rendered assignment not managed is not configured", func(t *testing.T) {
		desire := desireWithTemplate(
			tmplResource{name: testNameA, principalID: testPrincipalID, roleDefID: testRoleDefinitionResourceID},
			tmplResource{name: testNameB, principalID: testPrincipalID2, roleDefID: testRoleDefinitionResourceID},
		)
		observeApplied(t, desire, testNameA) // B not managed
		got, err := MockFPARoleAssignmentsConfigured(desire)
		require.NoError(t, err)
		assert.False(t, got, "a single not-yet-managed assignment makes the set not configured")
	})

	t.Run("stale observation is not configured", func(t *testing.T) {
		desire := desireWithTemplate(tmplResource{name: testNameA, principalID: testPrincipalID, roleDefID: testRoleDefinitionResourceID})
		observeApplied(t, desire, testNameA)
		desire.Status.ObservedSpecHash = "does-not-match"
		got, err := MockFPARoleAssignmentsConfigured(desire)
		require.NoError(t, err)
		assert.False(t, got, "the observed outcome is for a different spec")
	})

	t.Run("observed but not applied is not configured", func(t *testing.T) {
		desire := desireWithTemplate(tmplResource{name: testNameA, principalID: testPrincipalID, roleDefID: testRoleDefinitionResourceID})
		observeApplied(t, desire, testNameA)
		desire.Status.Conditions = nil
		got, err := MockFPARoleAssignmentsConfigured(desire)
		require.NoError(t, err)
		assert.False(t, got, "the apply has not succeeded")
	})
}
