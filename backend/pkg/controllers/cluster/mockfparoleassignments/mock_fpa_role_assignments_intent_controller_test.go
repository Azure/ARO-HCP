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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

const (
	testMockFPAPrincipalID = "mock-fpa-principal"
	testManagedRGScope     = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-managed-rg"
)

func testIntentSyncer(clock *clocktesting.FakePassiveClock) *mockFPARoleAssignmentIntentSyncer {
	return &mockFPARoleAssignmentIntentSyncer{
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

// stackDesireManaging builds a StackDesire whose last-observed Azure state reports
// the given reserved assignment as managed.
func stackDesireManaging(reserved *azcorearm.ResourceID) *coreapi.StackDesire {
	return &coreapi.StackDesire{
		Status: coreapi.StackDesireStatus{
			Stack: &armdeploymentstacks.DeploymentStack{
				Properties: &armdeploymentstacks.DeploymentStackProperties{
					Resources: []*armdeploymentstacks.ManagedResourceReference{
						{ID: ptr.To(reserved.String()), Status: ptr.To(armdeploymentstacks.ResourceStatusModeManaged)},
					},
				},
			},
		},
	}
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
		got := s.mergeRoleAssignments(nil, nil)
		require.Contains(t, got, wantKey)
		require.NotNil(t, got[wantKey])
		assert.Nil(t, got[wantKey].DeconfigureTimestamp)
		require.NotNil(t, got[wantKey].TargetIdentity)
		assert.Equal(t, testMockFPAPrincipalID, got[wantKey].TargetIdentity.PrincipalID)
	})

	t.Run("clears deconfigure timestamp on the desired key", func(t *testing.T) {
		t.Parallel()
		s := testIntentSyncer(clocktesting.NewFakePassiveClock(now))
		stamp := metav1.NewTime(now.Add(-time.Hour))
		existing := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			wantKey: {DeconfigureTimestamp: &stamp, ReservedAzureResourceID: mustReservedID(t, "11111111-1111-1111-1111-111111111111")},
		}
		got := s.mergeRoleAssignments(existing, nil)
		require.Contains(t, got, wantKey)
		assert.Nil(t, got[wantKey].DeconfigureTimestamp)
	})

	t.Run("keeps a superseded key live until the replacement is configured", func(t *testing.T) {
		t.Parallel()
		s := testIntentSyncer(clocktesting.NewFakePassiveClock(now))
		staleKey := coreapi.RoleAssignmentKey{PrincipalID: "old-principal", RoleDefinitionResourceID: OwnerRoleDefinitionResourceID}
		existing := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			staleKey: {ReservedAzureResourceID: mustReservedID(t, "22222222-2222-2222-2222-222222222222")},
		}
		// The replacement is not configured yet (no StackDesire), so the superseded
		// key must be kept live (not stamped) to preserve the Owner grant.
		got := s.mergeRoleAssignments(existing, nil)
		require.Contains(t, got, wantKey, "desired key always present")
		require.Contains(t, got, staleKey, "superseded key kept live until replacement configured")
		assert.Nil(t, got[staleKey].DeconfigureTimestamp, "superseded key must not be stamped before replacement is configured")
	})

	t.Run("stamps a superseded key once the replacement is configured", func(t *testing.T) {
		t.Parallel()
		s := testIntentSyncer(clocktesting.NewFakePassiveClock(now))
		desiredReserved := mustReservedID(t, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
		staleKey := coreapi.RoleAssignmentKey{PrincipalID: "old-principal", RoleDefinitionResourceID: OwnerRoleDefinitionResourceID}
		existing := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			wantKey:  {ReservedAzureResourceID: desiredReserved},
			staleKey: {ReservedAzureResourceID: mustReservedID(t, "22222222-2222-2222-2222-222222222222")},
		}
		// The replacement assignment is managed in the stack, so it is safe to begin
		// removing the superseded one.
		desire := stackDesireManaging(desiredReserved)
		got := s.mergeRoleAssignments(existing, desire)
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
		existing := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			staleKey: {DeconfigureTimestamp: &stamp, ReservedAzureResourceID: reserved},
		}
		// StackDesire has not reconciled the removal (no status).
		got := s.mergeRoleAssignments(existing, &coreapi.StackDesire{})
		require.Contains(t, got, staleKey, "draining key kept until removal reconciled")
	})

	t.Run("drops a draining key once the stack reconciled its removal", func(t *testing.T) {
		t.Parallel()
		s := testIntentSyncer(clocktesting.NewFakePassiveClock(now))
		stamp := metav1.NewTime(now.Add(-time.Hour))
		reserved := mustReservedID(t, "44444444-4444-4444-4444-444444444444")
		staleKey := coreapi.RoleAssignmentKey{PrincipalID: "old-principal", RoleDefinitionResourceID: OwnerRoleDefinitionResourceID}
		existing := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			staleKey: {DeconfigureTimestamp: &stamp, ReservedAzureResourceID: reserved},
		}
		// A current, successful Apply whose observed state no longer manages the
		// reserved assignment confirms the removal.
		desire := &coreapi.StackDesire{Spec: coreapi.StackDesireSpec{Action: coreapi.StackDesireActionApply}}
		hash, err := coreapi.StackDesireSpecHash(desire.Spec)
		require.NoError(t, err)
		desire.Status.ObservedSpecHash = hash
		desire.Status.Conditions = []metav1.Condition{{Type: coreapi.StackDesireConditionSuccessfullyApplied, Status: metav1.ConditionTrue}}

		got := s.mergeRoleAssignments(existing, desire)
		assert.NotContains(t, got, staleKey, "draining key dropped once removal reconciled")
		require.Contains(t, got, wantKey)
	})
}
