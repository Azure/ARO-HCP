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

package denyassignments

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestSelectExcludedPrincipalsForPUT(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	insideWait := metav1.NewTime(now.Add(-time.Hour))
	elapsedWait := metav1.NewTime(now.Add(-25 * time.Hour))
	olderWait := metav1.NewTime(now.Add(-2 * time.Hour))

	liveA := coreapi.DenyAssignmentExcludedIdentityKey{ResourceID: "id-a", PrincipalID: "principal-a"}
	liveB := coreapi.DenyAssignmentExcludedIdentityKey{ResourceID: "id-b", PrincipalID: "principal-b"}
	waitC := coreapi.DenyAssignmentExcludedIdentityKey{ResourceID: "id-c", PrincipalID: "principal-c"}
	waitD := coreapi.DenyAssignmentExcludedIdentityKey{ResourceID: "id-d", PrincipalID: "principal-d"}

	observed := func(principalID string) *coreapi.DenyAssignmentExcludedObservedIdentity {
		return &coreapi.DenyAssignmentExcludedObservedIdentity{PrincipalID: principalID}
	}

	t.Run("live desired principals are always included", func(t *testing.T) {
		t.Parallel()
		desired := map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedObservedIdentity{
			liveA: observed("principal-a"),
			liveB: observed("principal-b"),
		}
		included, dropped, err := selectExcludedPrincipalsForPUT(&coreapi.DenyAssignmentStatus{}, desired, now, 25)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"principal-a", "principal-b"}, included)
		assert.Empty(t, dropped)
	})

	t.Run("observed row with no timestamp is kept until Intent stamps cooldown", func(t *testing.T) {
		t.Parallel()
		status := &coreapi.DenyAssignmentStatus{
			ExcludedIdentities: map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedIdentityStatus{
				liveA: {ObservedIdentity: observed("principal-a")},
			},
		}
		included, dropped, err := selectExcludedPrincipalsForPUT(status, nil, now, 25)
		require.NoError(t, err)
		assert.Equal(t, []string{"principal-a"}, included)
		assert.Empty(t, dropped)
	})

	t.Run("cooldown inside 24h is included", func(t *testing.T) {
		t.Parallel()
		desired := map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedObservedIdentity{
			liveA: observed("principal-a"),
		}
		status := &coreapi.DenyAssignmentStatus{
			ExcludedIdentities: map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedIdentityStatus{
				liveA: {ObservedIdentity: observed("principal-a")},
				waitC: {DeconfigureTimestamp: &insideWait, ObservedIdentity: observed("principal-c")},
			},
		}
		included, dropped, err := selectExcludedPrincipalsForPUT(status, desired, now, 25)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"principal-a", "principal-c"}, included)
		assert.Empty(t, dropped)
	})

	t.Run("cooldown after 24h is dropped", func(t *testing.T) {
		t.Parallel()
		desired := map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedObservedIdentity{
			liveA: observed("principal-a"),
		}
		status := &coreapi.DenyAssignmentStatus{
			ExcludedIdentities: map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedIdentityStatus{
				liveA: {ObservedIdentity: observed("principal-a")},
				waitC: {DeconfigureTimestamp: &elapsedWait, ObservedIdentity: observed("principal-c")},
			},
		}
		included, dropped, err := selectExcludedPrincipalsForPUT(status, desired, now, 25)
		require.NoError(t, err)
		assert.Equal(t, []string{"principal-a"}, included)
		assert.Equal(t, []coreapi.DenyAssignmentExcludedIdentityKey{waitC}, dropped)
	})

	t.Run("LRU drops older waiters when over the principal limit", func(t *testing.T) {
		t.Parallel()
		desired := map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedObservedIdentity{
			liveA: observed("principal-a"),
			liveB: observed("principal-b"),
		}
		status := &coreapi.DenyAssignmentStatus{
			ExcludedIdentities: map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedIdentityStatus{
				liveA: {ObservedIdentity: observed("principal-a")},
				liveB: {ObservedIdentity: observed("principal-b")},
				waitC: {DeconfigureTimestamp: &insideWait, ObservedIdentity: observed("principal-c")},
				waitD: {DeconfigureTimestamp: &olderWait, ObservedIdentity: observed("principal-d")},
			},
		}
		included, dropped, err := selectExcludedPrincipalsForPUT(status, desired, now, 3)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"principal-a", "principal-b", "principal-c"}, included)
		assert.Equal(t, []coreapi.DenyAssignmentExcludedIdentityKey{waitD}, dropped)
	})

	t.Run("must-include over the limit is an error", func(t *testing.T) {
		t.Parallel()
		desired := map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedObservedIdentity{
			liveA: observed("principal-a"),
			liveB: observed("principal-b"),
		}
		_, _, err := selectExcludedPrincipalsForPUT(&coreapi.DenyAssignmentStatus{}, desired, now, 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceed Azure ExcludePrincipals limit")
	})
}

func TestSyncObservedExcludedIdentities(t *testing.T) {
	t.Parallel()

	liveA := coreapi.DenyAssignmentExcludedIdentityKey{ResourceID: "id-a", PrincipalID: "principal-a"}
	waitC := coreapi.DenyAssignmentExcludedIdentityKey{ResourceID: "id-c", PrincipalID: "principal-c"}
	insideWait := metav1.NewTime(time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC))

	status := &coreapi.DenyAssignmentStatus{
		ExcludedIdentities: map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedIdentityStatus{
			waitC: {
				DeconfigureTimestamp: &insideWait,
				ObservedIdentity:     &coreapi.DenyAssignmentExcludedObservedIdentity{PrincipalID: "principal-c", ClientID: "old-c"},
			},
		},
	}
	desired := map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedObservedIdentity{
		liveA: {PrincipalID: "principal-a", ClientID: "client-a", TenantID: "tenant-a"},
	}
	syncObservedExcludedIdentities(status, desired, map[coreapi.DenyAssignmentExcludedIdentityKey]struct{}{})

	require.Contains(t, status.ExcludedIdentities, liveA)
	require.NotNil(t, status.ExcludedIdentities[liveA].ObservedIdentity)
	assert.Equal(t, "client-a", status.ExcludedIdentities[liveA].ObservedIdentity.ClientID)
	assert.Nil(t, status.ExcludedIdentities[liveA].DeconfigureTimestamp)
	require.Contains(t, status.ExcludedIdentities, waitC)
	require.NotNil(t, status.ExcludedIdentities[waitC].DeconfigureTimestamp)

	syncObservedExcludedIdentities(status, desired, map[coreapi.DenyAssignmentExcludedIdentityKey]struct{}{waitC: {}})
	require.Contains(t, status.ExcludedIdentities, liveA)
	assert.NotContains(t, status.ExcludedIdentities, waitC)
}
