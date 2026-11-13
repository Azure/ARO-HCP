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
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks/v2"

	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/azureresourcesapihelpers"
)

const (
	testMockFPAPrincipalID = "mock-fpa-principal"
	testManagedRGScope     = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-managed-rg"
)

func testIntentSyncer(clock *clocktesting.FakePassiveClock) *mockFPARoleAssignmentsIntentSyncer {
	return &mockFPARoleAssignmentsIntentSyncer{
		clock:                           clock,
		mockFPAPrincipalID:              testMockFPAPrincipalID,
		desiredRoleDefinitionResourceID: OwnerRoleDefinitionResourceID,
	}
}

func mustReservedID(t *testing.T, name string) *azcorearm.ResourceID {
	t.Helper()
	id, err := azcorearm.ParseResourceID(testManagedRGScope + "/providers/Microsoft.Authorization/roleAssignments/" + name)
	require.NoError(t, err)
	return id
}

func TestMergeRoleAssignments(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	wantKey := coreapi.RoleAssignmentKey{
		PrincipalID:              testMockFPAPrincipalID,
		RoleDefinitionResourceID: OwnerRoleDefinitionResourceID,
	}

	t.Run("empty existing adds the desired key", func(t *testing.T) {
		t.Parallel()
		s := testIntentSyncer(clocktesting.NewFakePassiveClock(now))
		got, err := s.mergeRoleAssignments(nil, nil)
		require.NoError(t, err)
		require.Contains(t, got, wantKey)
		require.NotNil(t, got[wantKey])
		assert.Nil(t, got[wantKey].DeconfigureTimestamp)
		assert.Nil(t, got[wantKey].ReservedAzureResourceID)
	})

	t.Run("clears deconfigure timestamp on the desired key", func(t *testing.T) {
		t.Parallel()
		s := testIntentSyncer(clocktesting.NewFakePassiveClock(now))
		stamp := metav1.NewTime(now.Add(-time.Hour))
		existing := map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus{
			wantKey: {DeconfigureTimestamp: &stamp, ReservedAzureResourceID: mustReservedID(t, "11111111-1111-1111-1111-111111111111")},
		}
		got, err := s.mergeRoleAssignments(existing, nil)
		require.NoError(t, err)
		require.Contains(t, got, wantKey)
		assert.Nil(t, got[wantKey].DeconfigureTimestamp)
	})

	t.Run("keeps a superseded key live until the replacement is configured", func(t *testing.T) {
		t.Parallel()
		s := testIntentSyncer(clocktesting.NewFakePassiveClock(now))
		staleKey := coreapi.RoleAssignmentKey{PrincipalID: "old-principal", RoleDefinitionResourceID: OwnerRoleDefinitionResourceID}
		existing := map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus{
			staleKey: {ReservedAzureResourceID: mustReservedID(t, "22222222-2222-2222-2222-222222222222")},
		}
		// The replacement is not configured yet (no StackDesire), so the superseded
		// key must be kept live (not stamped) to preserve the Owner grant.
		got, err := s.mergeRoleAssignments(existing, nil)
		require.NoError(t, err)
		require.Contains(t, got, wantKey, "desired key always present")
		require.Contains(t, got, staleKey, "superseded key kept live until replacement configured")
		assert.Nil(t, got[staleKey].DeconfigureTimestamp, "superseded key must not be stamped before replacement is configured")
	})

	t.Run("stamps a superseded key once the replacement is configured", func(t *testing.T) {
		t.Parallel()
		s := testIntentSyncer(clocktesting.NewFakePassiveClock(now))
		desiredReserved := mustReservedID(t, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
		staleKey := coreapi.RoleAssignmentKey{PrincipalID: "old-principal", RoleDefinitionResourceID: OwnerRoleDefinitionResourceID}
		existing := map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus{
			wantKey:  {ReservedAzureResourceID: desiredReserved},
			staleKey: {ReservedAzureResourceID: mustReservedID(t, "22222222-2222-2222-2222-222222222222")},
		}
		// The replacement assignment is managed by a current, successful apply, so it is
		// safe to begin removing the superseded one.
		desire := desireWithOutcome(t, azureresourcesapi.DeploymentStackDesireActionTypeApply, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, metav1.ConditionTrue, desiredReserved)
		got, err := s.mergeRoleAssignments(existing, desire)
		require.NoError(t, err)
		require.Contains(t, got, wantKey)
		assert.Nil(t, got[wantKey].DeconfigureTimestamp)
		require.Contains(t, got, staleKey, "superseded key kept while draining")
		require.NotNil(t, got[staleKey].DeconfigureTimestamp)
		assert.Equal(t, metav1.NewTime(now), *got[staleKey].DeconfigureTimestamp)
	})

	t.Run("keeps a draining key until the stack reconciles its removal", func(t *testing.T) {
		t.Parallel()
		s := testIntentSyncer(clocktesting.NewFakePassiveClock(now))
		stamp := metav1.NewTime(now.Add(-time.Hour))
		reserved := mustReservedID(t, "33333333-3333-3333-3333-333333333333")
		staleKey := coreapi.RoleAssignmentKey{PrincipalID: "old-principal", RoleDefinitionResourceID: OwnerRoleDefinitionResourceID}
		existing := map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus{
			staleKey: {DeconfigureTimestamp: &stamp, ReservedAzureResourceID: reserved},
		}
		// StackDesire has not reconciled the removal (no status).
		got, err := s.mergeRoleAssignments(existing, &azureresourcesapi.DeploymentStackDesire{})
		require.NoError(t, err)
		require.Contains(t, got, staleKey, "draining key kept until removal reconciled")
	})

	t.Run("drops a draining key once the stack reconciled its removal", func(t *testing.T) {
		t.Parallel()
		s := testIntentSyncer(clocktesting.NewFakePassiveClock(now))
		stamp := metav1.NewTime(now.Add(-time.Hour))
		reserved := mustReservedID(t, "44444444-4444-4444-4444-444444444444")
		staleKey := coreapi.RoleAssignmentKey{PrincipalID: "old-principal", RoleDefinitionResourceID: OwnerRoleDefinitionResourceID}
		existing := map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus{
			staleKey: {DeconfigureTimestamp: &stamp, ReservedAzureResourceID: reserved},
		}
		// A current, successful Apply whose observed state no longer manages the
		// reserved assignment confirms the removal.
		desire := &azureresourcesapi.DeploymentStackDesire{Spec: azureresourcesapi.DeploymentStackDesireSpec{Action: azureresourcesapi.DeploymentStackDesireActionTypeApply}}
		hash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(desire.Spec)
		require.NoError(t, err)
		desire.Status.ObservedSpecHash = hash
		desire.Status.Conditions = []metav1.Condition{{Type: azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, Status: metav1.ConditionTrue}}

		got, err := s.mergeRoleAssignments(existing, desire)
		require.NoError(t, err)
		assert.NotContains(t, got, staleKey, "draining key dropped once removal reconciled")
		require.Contains(t, got, wantKey)
	})
}

// desireWithOutcome builds a DeploymentStackDesire whose Status.ObservedSpecHash matches
// its Spec (so it is treated as the current outcome), with the given action, condition,
// and observed managed resources (all in the managed state).
func desireWithOutcome(t *testing.T, action azureresourcesapi.DeploymentStackDesireActionType, condType string, condStatus metav1.ConditionStatus, managedIDs ...*azcorearm.ResourceID) *azureresourcesapi.DeploymentStackDesire {
	t.Helper()
	resources := make([]*armdeploymentstacks.ManagedResourceReference, 0, len(managedIDs))
	for _, id := range managedIDs {
		resources = append(resources, &armdeploymentstacks.ManagedResourceReference{
			ID:     ptr.To(id.String()),
			Status: ptr.To(armdeploymentstacks.ResourceStatusModeManaged),
		})
	}
	desire := &azureresourcesapi.DeploymentStackDesire{
		Spec: azureresourcesapi.DeploymentStackDesireSpec{Action: action},
		Status: azureresourcesapi.DeploymentStackDesireStatus{
			Stack: &armdeploymentstacks.DeploymentStack{
				Properties: &armdeploymentstacks.DeploymentStackProperties{Resources: resources},
			},
		},
	}
	hash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(desire.Spec)
	require.NoError(t, err)
	desire.Status.ObservedSpecHash = hash
	desire.Status.Conditions = []metav1.Condition{{Type: condType, Status: condStatus}}
	return desire
}

func TestMockFPARoleAssignmentRemovedAndReconciled(t *testing.T) {
	t.Parallel()
	reserved := mustReservedID(t, "11111111-1111-1111-1111-111111111111")

	staleHashDesire := desireWithOutcome(t, azureresourcesapi.DeploymentStackDesireActionTypeApply, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, metav1.ConditionTrue)
	staleHashDesire.Status.ObservedSpecHash = "does-not-match"

	// The reserved assignment is still in the stack's resource set but Azure failed to
	// delete it (pending retry), so it is still present in Azure: not removed.
	deleteFailedDesire := desireWithOutcome(t, azureresourcesapi.DeploymentStackDesireActionTypeApply, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, metav1.ConditionTrue, reserved)
	deleteFailedDesire.Status.Stack.Properties.Resources[0].Status = ptr.To(armdeploymentstacks.ResourceStatusModeDeleteFailed)

	tests := []struct {
		name     string
		status   *coreapi.MockFPARoleAssignmentStatus
		desire   *azureresourcesapi.DeploymentStackDesire
		expected bool
	}{
		{
			name:     "never reserved means never materialized, so removed",
			status:   &coreapi.MockFPARoleAssignmentStatus{},
			desire:   nil,
			expected: true,
		},
		{
			name:     "reserved but no desire yet, not removed",
			status:   &coreapi.MockFPARoleAssignmentStatus{ReservedAzureResourceID: reserved},
			desire:   nil,
			expected: false,
		},
		{
			name:     "observed outcome does not match current spec, not removed",
			status:   &coreapi.MockFPARoleAssignmentStatus{ReservedAzureResourceID: reserved},
			desire:   staleHashDesire,
			expected: false,
		},
		{
			name:     "apply succeeded and reserved no longer managed, removed",
			status:   &coreapi.MockFPARoleAssignmentStatus{ReservedAzureResourceID: reserved},
			desire:   desireWithOutcome(t, azureresourcesapi.DeploymentStackDesireActionTypeApply, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, metav1.ConditionTrue),
			expected: true,
		},
		{
			name:     "apply succeeded but reserved still managed, not removed",
			status:   &coreapi.MockFPARoleAssignmentStatus{ReservedAzureResourceID: reserved},
			desire:   desireWithOutcome(t, azureresourcesapi.DeploymentStackDesireActionTypeApply, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, metav1.ConditionTrue, reserved),
			expected: false,
		},
		{
			name:     "apply succeeded but reserved delete failed (still present), not removed",
			status:   &coreapi.MockFPARoleAssignmentStatus{ReservedAzureResourceID: reserved},
			desire:   deleteFailedDesire,
			expected: false,
		},
		{
			name:     "apply not yet successful, not removed",
			status:   &coreapi.MockFPARoleAssignmentStatus{ReservedAzureResourceID: reserved},
			desire:   desireWithOutcome(t, azureresourcesapi.DeploymentStackDesireActionTypeApply, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied, metav1.ConditionFalse),
			expected: false,
		},
		{
			name:     "delete confirmed, removed",
			status:   &coreapi.MockFPARoleAssignmentStatus{ReservedAzureResourceID: reserved},
			desire:   desireWithOutcome(t, azureresourcesapi.DeploymentStackDesireActionTypeDelete, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyDeleted, metav1.ConditionTrue),
			expected: true,
		},
		{
			name:     "delete not yet confirmed, not removed",
			status:   &coreapi.MockFPARoleAssignmentStatus{ReservedAzureResourceID: reserved},
			desire:   desireWithOutcome(t, azureresourcesapi.DeploymentStackDesireActionTypeDelete, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyDeleted, metav1.ConditionFalse),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := (&mockFPARoleAssignmentsIntentSyncer{}).roleAssignmentRemovedAndReconciled(tt.status, tt.desire)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}
