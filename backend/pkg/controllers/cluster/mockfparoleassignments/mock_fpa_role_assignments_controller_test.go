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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/roleassignment"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

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

func TestServiceProviderClusterWithReservedAzureResourceIDs(t *testing.T) {
	t.Parallel()

	scope, err := azcorearm.ParseResourceID(testManagedRGScope)
	require.NoError(t, err)

	desiredKey := coreapi.RoleAssignmentKey{PrincipalID: testMockFPAPrincipalID, RoleDefinitionResourceID: OwnerRoleDefinitionResourceID}
	wantReservedID := roleassignment.ManagedResourceGroupScopedRoleAssignmentResourceID(scope.String(), desiredKey.PrincipalID, desiredKey.RoleDefinitionResourceID)

	s := &mockFPARoleAssignmentsSyncer{}

	t.Run("assigns a deterministic reserved ID to a non-draining key that lacks one", func(t *testing.T) {
		t.Parallel()
		existing := &coreapi.ServiceProviderCluster{}
		existing.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup = map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus{
			desiredKey: {},
		}

		got, err := s.serviceProviderClusterWithReservedAzureResourceIDs(existing, scope)
		require.NoError(t, err)
		gotStatus := got.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup[desiredKey]
		require.NotNil(t, gotStatus.ReservedAzureResourceID, "non-draining key should get a reserved ID")
		assert.Equal(t, wantReservedID, gotStatus.ReservedAzureResourceID.String(), "reserved ID must be the deterministic name")

		assert.Nil(t, existing.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup[desiredKey].ReservedAzureResourceID, "input must not be mutated")
	})

	t.Run("does not reserve for a draining key", func(t *testing.T) {
		t.Parallel()
		stamp := metav1.NewTime(time.Now())
		existing := &coreapi.ServiceProviderCluster{}
		existing.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup = map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus{
			desiredKey: {DeconfigureTimestamp: &stamp},
		}

		got, err := s.serviceProviderClusterWithReservedAzureResourceIDs(existing, scope)
		require.NoError(t, err)
		assert.Nil(t, got.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup[desiredKey].ReservedAzureResourceID, "draining key must not get a reserved ID")
	})

	t.Run("preserves an existing reserved ID, never recomputes", func(t *testing.T) {
		t.Parallel()
		preset := mustReservedID(t, "11111111-1111-1111-1111-111111111111")
		existing := &coreapi.ServiceProviderCluster{}
		existing.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup = map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus{
			desiredKey: {ReservedAzureResourceID: preset},
		}

		got, err := s.serviceProviderClusterWithReservedAzureResourceIDs(existing, scope)
		require.NoError(t, err)
		assert.Equal(t, preset.String(), got.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup[desiredKey].ReservedAzureResourceID.String(), "existing reserved ID must be preserved")
	})
}

func TestRoleAssignmentTemplateResources(t *testing.T) {
	t.Parallel()

	desiredKey := coreapi.RoleAssignmentKey{PrincipalID: testMockFPAPrincipalID, RoleDefinitionResourceID: OwnerRoleDefinitionResourceID}
	drainingKey := coreapi.RoleAssignmentKey{PrincipalID: "old-principal", RoleDefinitionResourceID: OwnerRoleDefinitionResourceID}
	stamp := metav1.NewTime(time.Now())

	t.Run("renders the non-draining key and excludes the draining one", func(t *testing.T) {
		t.Parallel()
		assignments := map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus{
			desiredKey:  {ReservedAzureResourceID: mustReservedID(t, "11111111-1111-1111-1111-111111111111")},
			drainingKey: {DeconfigureTimestamp: &stamp, ReservedAzureResourceID: mustReservedID(t, "22222222-2222-2222-2222-222222222222")},
		}
		resources, err := roleAssignmentTemplateResources(assignments)
		require.NoError(t, err)
		require.Len(t, resources, 1)
		assert.Equal(t, "Microsoft.Authorization/roleAssignments", resources[0]["type"])
		assert.Equal(t, "11111111-1111-1111-1111-111111111111", resources[0]["name"])
		props := resources[0]["properties"].(map[string]any)
		assert.Equal(t, testMockFPAPrincipalID, props["principalId"])
		assert.Equal(t, OwnerRoleDefinitionResourceID, props["roleDefinitionId"])
		assert.Equal(t, "ServicePrincipal", props["principalType"])
	})

	t.Run("errors when a non-draining key has no reserved ID", func(t *testing.T) {
		t.Parallel()
		assignments := map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus{
			desiredKey: {},
		}
		_, err := roleAssignmentTemplateResources(assignments)
		require.Error(t, err)
	})

	t.Run("empty when all keys are draining", func(t *testing.T) {
		t.Parallel()
		assignments := map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus{
			drainingKey: {DeconfigureTimestamp: &stamp, ReservedAzureResourceID: mustReservedID(t, "22222222-2222-2222-2222-222222222222")},
		}
		resources, err := roleAssignmentTemplateResources(assignments)
		require.NoError(t, err)
		assert.Empty(t, resources)
	})
}
