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

	t.Run("live identities are always included", func(t *testing.T) {
		t.Parallel()
		status := &coreapi.DenyAssignmentStatus{
			ExcludedIdentities: map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedIdentityStatus{
				liveA: {Phase: coreapi.DenyAssignmentExcludedIdentityPhaseConfigured},
				liveB: {Phase: coreapi.DenyAssignmentExcludedIdentityPhasePendingConfigure},
			},
		}
		included, dropped, err := selectExcludedPrincipalsForPUT(status, now, 25)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"principal-a", "principal-b"}, included)
		assert.Empty(t, dropped)
	})

	t.Run("PendingDeconfigure inside 24h is included", func(t *testing.T) {
		t.Parallel()
		status := &coreapi.DenyAssignmentStatus{
			ExcludedIdentities: map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedIdentityStatus{
				liveA: {Phase: coreapi.DenyAssignmentExcludedIdentityPhaseConfigured},
				waitC: {Phase: coreapi.DenyAssignmentExcludedIdentityPhasePendingDeconfigure, DeconfigureTimestamp: &insideWait},
			},
		}
		included, dropped, err := selectExcludedPrincipalsForPUT(status, now, 25)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"principal-a", "principal-c"}, included)
		assert.Empty(t, dropped)
	})

	t.Run("PendingDeconfigure after 24h is dropped", func(t *testing.T) {
		t.Parallel()
		status := &coreapi.DenyAssignmentStatus{
			ExcludedIdentities: map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedIdentityStatus{
				liveA: {Phase: coreapi.DenyAssignmentExcludedIdentityPhaseConfigured},
				waitC: {Phase: coreapi.DenyAssignmentExcludedIdentityPhasePendingDeconfigure, DeconfigureTimestamp: &elapsedWait},
			},
		}
		included, dropped, err := selectExcludedPrincipalsForPUT(status, now, 25)
		require.NoError(t, err)
		assert.Equal(t, []string{"principal-a"}, included)
		assert.Equal(t, []coreapi.DenyAssignmentExcludedIdentityKey{waitC}, dropped)
	})

	t.Run("LRU drops older waiters when over the principal limit", func(t *testing.T) {
		t.Parallel()
		status := &coreapi.DenyAssignmentStatus{
			ExcludedIdentities: map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedIdentityStatus{
				liveA: {Phase: coreapi.DenyAssignmentExcludedIdentityPhaseConfigured},
				liveB: {Phase: coreapi.DenyAssignmentExcludedIdentityPhaseConfigured},
				waitC: {Phase: coreapi.DenyAssignmentExcludedIdentityPhasePendingDeconfigure, DeconfigureTimestamp: &insideWait},
				waitD: {Phase: coreapi.DenyAssignmentExcludedIdentityPhasePendingDeconfigure, DeconfigureTimestamp: &olderWait},
			},
		}
		included, dropped, err := selectExcludedPrincipalsForPUT(status, now, 3)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"principal-a", "principal-b", "principal-c"}, included)
		assert.Equal(t, []coreapi.DenyAssignmentExcludedIdentityKey{waitD}, dropped)
	})

	t.Run("must-include over the limit is an error", func(t *testing.T) {
		t.Parallel()
		status := &coreapi.DenyAssignmentStatus{
			ExcludedIdentities: map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedIdentityStatus{
				liveA: {Phase: coreapi.DenyAssignmentExcludedIdentityPhaseConfigured},
				liveB: {Phase: coreapi.DenyAssignmentExcludedIdentityPhaseConfigured},
			},
		}
		_, _, err := selectExcludedPrincipalsForPUT(status, now, 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceed Azure ExcludePrincipals limit")
	})
}
