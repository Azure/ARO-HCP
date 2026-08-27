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

package versionrollout

import (
	"context"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

func pin(exact, until string) *coreapi.ServiceProviderClusterPinnedVersion {
	p := &coreapi.ServiceProviderClusterPinnedVersion{ExactVersion: v(exact)}
	if until != "" {
		p.UntilExactVersion = v(until)
	}
	return p
}

func TestComputeForcedDesiredVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		desired     *semver.Version
		pinned      *coreapi.ServiceProviderClusterPinnedVersion
		best        *semver.Version
		wantChanged bool
		wantDesired *semver.Version
		wantClear   bool
	}{
		{
			name:        "not pinned is a no-op",
			desired:     v("4.21.4"),
			pinned:      nil,
			best:        v("4.21.6"),
			wantChanged: false,
		},
		{
			name:        "pin without exact version is a no-op",
			pinned:      &coreapi.ServiceProviderClusterPinnedVersion{UntilExactVersion: v("4.21.6")},
			best:        v("4.21.6"),
			wantChanged: false,
		},
		{
			name:        "best reaches until - adopt best and clear pin",
			desired:     v("4.21.2"),
			pinned:      pin("4.21.2", "4.21.6"),
			best:        v("4.21.6"),
			wantChanged: true,
			wantDesired: v("4.21.6"),
			wantClear:   true,
		},
		{
			name:        "best exceeds until - adopt best and clear pin",
			desired:     v("4.21.2"),
			pinned:      pin("4.21.2", "4.21.6"),
			best:        v("4.21.8"),
			wantChanged: true,
			wantDesired: v("4.21.8"),
			wantClear:   true,
		},
		{
			name:        "best below until - hold at pinned exact version",
			desired:     v("4.21.6"),
			pinned:      pin("4.21.2", "4.21.6"),
			best:        v("4.21.4"),
			wantChanged: true,
			wantDesired: v("4.21.2"),
			wantClear:   false,
		},
		{
			name:        "already at pinned exact version - no-op",
			desired:     v("4.21.2"),
			pinned:      pin("4.21.2", "4.21.6"),
			best:        v("4.21.4"),
			wantChanged: false,
		},
		{
			name:        "no until version holds at pinned exact indefinitely",
			desired:     nil,
			pinned:      pin("4.21.2", ""),
			best:        v("4.99.9"),
			wantChanged: true,
			wantDesired: v("4.21.2"),
			wantClear:   false,
		},
		{
			name:        "best not yet known - hold at pinned exact",
			desired:     nil,
			pinned:      pin("4.21.2", "4.21.6"),
			best:        nil,
			wantChanged: true,
			wantDesired: v("4.21.2"),
			wantClear:   false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			spc := newTestSPC("c1", tc.desired, nil, tc.pinned)
			got := computeForcedDesiredVersion(spc, tc.best)
			assert.Equal(t, tc.wantChanged, got.Changed, "changed")
			assert.Equal(t, tc.wantClear, got.ClearPin, "clearPin")
			if tc.wantDesired == nil {
				assert.Nil(t, got.NewDesired)
			} else {
				require.NotNil(t, got.NewDesired)
				assert.True(t, got.NewDesired.EQ(*tc.wantDesired), "desired got %v want %v", got.NewDesired, tc.wantDesired)
			}
		})
	}
}

func TestForcedClusterDesiredVersionSyncer_SyncOnce(t *testing.T) {
	t.Parallel()

	const clusterName = "c1"

	tests := []struct {
		name           string
		cluster        *coreapi.HCPOpenShiftCluster
		spc            *coreapi.ServiceProviderCluster
		rollout        *fleetapi.ControlPlaneVersionRollout
		wantDesired    *semver.Version
		wantPinCleared bool
	}{
		{
			name:        "holds at pinned exact when best is below until",
			cluster:     newTestCluster(clusterName, "stable", "4.21"),
			spc:         newTestSPC(clusterName, nil, nil, pin("4.21.2", "4.21.6")),
			rollout:     newTestRollout("stable-4.21", v("4.21.4"), fleetapi.ControlPlaneVersionRolloutStatus{}),
			wantDesired: v("4.21.2"),
		},
		{
			name:           "adopts best and clears pin when best reaches until",
			cluster:        newTestCluster(clusterName, "stable", "4.21"),
			spc:            newTestSPC(clusterName, v("4.21.2"), nil, pin("4.21.2", "4.21.6")),
			rollout:        newTestRollout("stable-4.21", v("4.21.6"), fleetapi.ControlPlaneVersionRolloutStatus{}),
			wantDesired:    v("4.21.6"),
			wantPinCleared: true,
		},
		{
			name:        "no rollout yet holds at pinned exact",
			cluster:     newTestCluster(clusterName, "stable", "4.21"),
			spc:         newTestSPC(clusterName, nil, nil, pin("4.21.2", "4.21.6")),
			rollout:     nil,
			wantDesired: v("4.21.2"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{tc.cluster, tc.spc})
			require.NoError(t, err)

			var rollouts []*fleetapi.ControlPlaneVersionRollout
			if tc.rollout != nil {
				rollouts = append(rollouts, tc.rollout)
			}
			_, lister := newTestRolloutStore(t, rollouts...)

			syncer := &forcedClusterDesiredVersionSyncer{
				clock:                        utilsclock.RealClock{},
				resourcesDBClient:            mockDB,
				clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
				serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
				rolloutLister:                lister,
			}

			key := controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    clusterName,
			}
			require.NoError(t, syncer.SyncOnce(ctx, key))

			updated, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, clusterName).
				Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)

			require.NotNil(t, updated.Spec.ControlPlaneVersion.DesiredVersion)
			assert.True(t, updated.Spec.ControlPlaneVersion.DesiredVersion.EQ(*tc.wantDesired),
				"desired got %v want %v", updated.Spec.ControlPlaneVersion.DesiredVersion, tc.wantDesired)
			if tc.wantPinCleared {
				assert.Nil(t, updated.Spec.PinnedVersion.ExactVersion, "expected pin cleared")
			} else {
				assert.NotNil(t, updated.Spec.PinnedVersion.ExactVersion, "expected pin retained")
			}
		})
	}
}
