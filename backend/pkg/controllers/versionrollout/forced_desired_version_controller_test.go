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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"

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

func TestForcedClusterDesiredVersionSyncer_Decisions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		desired      *semver.Version
		pinned       *coreapi.ServiceProviderClusterPinnedVersion
		best         *semver.Version
		exactVersion *semver.Version
		policy       coreapi.ZStreamUpdatePolicy
		wantChanged  bool
		wantDesired  *semver.Version
		wantClear    bool
	}{
		{
			name: "Immediate advances to best", policy: coreapi.ImmediateZStreamUpdatePolicy,
			desired: v("4.21.4"), best: v("4.21.6"), wantChanged: true, wantDesired: v("4.21.6"),
		},
		{
			name: "Immediate waits for initial assignment", policy: coreapi.ImmediateZStreamUpdatePolicy,
			best: v("4.21.6"),
		},
		{
			name: "Immediate waits for best", policy: coreapi.ImmediateZStreamUpdatePolicy,
			desired: v("4.21.4"),
		},
		{
			name: "Immediate at best is a no-op", policy: coreapi.ImmediateZStreamUpdatePolicy,
			desired: v("4.21.6"), best: v("4.21.6"),
		},
		{
			name: "Immediate never downgrades", policy: coreapi.ImmediateZStreamUpdatePolicy,
			desired: v("4.21.8"), best: v("4.21.6"),
		},
		{
			name: "Immediate never changes minor", policy: coreapi.ImmediateZStreamUpdatePolicy,
			desired: v("4.20.4"), best: v("4.21.6"),
		},
		{
			name: "unknown policy has no effect", policy: "Unknown",
			desired: v("4.21.4"), best: v("4.21.6"),
		},
		{
			name: "SRE pin takes precedence over Immediate", policy: coreapi.ImmediateZStreamUpdatePolicy,
			desired: v("4.21.4"), best: v("4.21.6"), pinned: pin("4.21.2", "4.21.8"),
			wantChanged: true, wantDesired: v("4.21.2"),
		},
		{
			name: "exact version takes precedence over Immediate", policy: coreapi.ImmediateZStreamUpdatePolicy,
			desired: v("4.21.4"), best: v("4.21.6"), exactVersion: v("4.21.2"),
			wantChanged: true, wantDesired: v("4.21.2"),
		},
		{
			name:        "not pinned and no exact version is a no-op",
			desired:     v("4.21.4"),
			pinned:      nil,
			best:        v("4.21.6"),
			wantChanged: false,
		},
		{
			name:         "experimental exact version when unpinned - hold at exact",
			desired:      v("4.21.4"),
			pinned:       nil,
			best:         v("4.21.6"),
			exactVersion: v("4.17.3"),
			wantChanged:  true,
			wantDesired:  v("4.17.3"),
		},
		{
			name:         "experimental exact version already set - no-op",
			desired:      v("4.17.3"),
			pinned:       nil,
			exactVersion: v("4.17.3"),
			wantChanged:  false,
		},
		{
			name:         "pin takes precedence over experimental exact version",
			desired:      v("4.21.4"),
			pinned:       pin("4.21.2", "4.21.6"),
			best:         v("4.21.4"),
			exactVersion: v("4.17.3"),
			wantChanged:  true,
			wantDesired:  v("4.21.2"), // pin's exact wins, not the experimental exact
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
			ctx := context.Background()
			serviceProviderCluster := newTestServiceProviderCluster("c1", tc.desired, nil, tc.pinned)
			cluster := newTestCluster("c1", "stable", "4.21")
			cluster.ServiceProviderProperties.ExperimentalFeatures = coreapi.ExperimentalFeatures{ControlPlaneExactVersion: tc.exactVersion, ZStreamUpdatePolicy: tc.policy}
			resourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
			require.NoError(t, err)
			// Key the fixture by the looked-up minor, including malformed best
			// versions to verify that Immediate cannot change the minor.
			minor := "4.21"
			if tc.pinned != nil && tc.pinned.ExactVersion != nil {
				minor = minorString(*tc.pinned.ExactVersion)
			} else if tc.desired != nil {
				minor = minorString(*tc.desired)
			}
			_, rolloutLister := newTestRolloutStore(t, newTestRollout(yStreamChannel("stable", minor), tc.best, fleetapi.ControlPlaneVersionRolloutStatus{}))
			serviceProviderClusterLister := &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: resourcesDB}
			before, err := serviceProviderClusterLister.Get(ctx, testSubscriptionID, testResourceGroupName, "c1")
			require.NoError(t, err)
			syncer := &forcedClusterDesiredVersionSyncer{
				clock: clocktesting.NewFakeClock(statusTestNow), resourcesDBClient: resourcesDB, rolloutLister: rolloutLister,
				clusterLister: &corelistertesting.DBClusterLister{ResourcesDBClient: resourcesDB}, serviceProviderClusterLister: serviceProviderClusterLister,
			}
			require.NoError(t, syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
				SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: "c1",
			}))
			updated, err := serviceProviderClusterLister.Get(ctx, testSubscriptionID, testResourceGroupName, "c1")
			require.NoError(t, err)
			if !tc.wantChanged {
				assert.Equal(t, before, updated, "no-op must not write the provider document")
				return
			}
			assert.Equal(t, tc.wantDesired, updated.Spec.ControlPlaneVersion.DesiredVersion)
			require.NotNil(t, updated.Spec.ControlPlaneVersion.DesiredVersionLastTransitionTime)
			assert.True(t, updated.Spec.ControlPlaneVersion.DesiredVersionLastTransitionTime.Time.Equal(statusTestNow))
			if tc.wantClear {
				assert.Empty(t, updated.Spec.PinnedVersion)
			} else {
				assert.Equal(t, before.Spec.PinnedVersion, updated.Spec.PinnedVersion)
			}
		})
	}
}

func TestForcedClusterDesiredVersionSyncer_SyncOnce(t *testing.T) {
	t.Parallel()

	const clusterName = "c1"

	tests := []struct {
		name                   string
		cluster                *coreapi.Cluster
		serviceProviderCluster *coreapi.ServiceProviderCluster
		rollout                *fleetapi.ControlPlaneVersionRollout
		wantDesired            *semver.Version
		wantPinCleared         bool
	}{
		{
			name:                   "holds at pinned exact when best is below until",
			cluster:                newTestCluster(clusterName, "stable", "4.21"),
			serviceProviderCluster: newTestServiceProviderCluster(clusterName, nil, nil, pin("4.21.2", "4.21.6")),
			rollout:                newTestRollout("stable-4.21", v("4.21.4"), fleetapi.ControlPlaneVersionRolloutStatus{}),
			wantDesired:            v("4.21.2"),
		},
		{
			name:                   "adopts best and clears pin when best reaches until",
			cluster:                newTestCluster(clusterName, "stable", "4.21"),
			serviceProviderCluster: newTestServiceProviderCluster(clusterName, v("4.21.2"), nil, pin("4.21.2", "4.21.6")),
			rollout:                newTestRollout("stable-4.21", v("4.21.6"), fleetapi.ControlPlaneVersionRolloutStatus{}),
			wantDesired:            v("4.21.6"),
			wantPinCleared:         true,
		},
		{
			name:                   "no rollout yet holds at pinned exact",
			cluster:                newTestCluster(clusterName, "stable", "4.21"),
			serviceProviderCluster: newTestServiceProviderCluster(clusterName, nil, nil, pin("4.21.2", "4.21.6")),
			rollout:                nil,
			wantDesired:            v("4.21.2"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{tc.cluster, tc.serviceProviderCluster})
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

// TestForcedClusterDesiredVersionSyncer_SyncOnce_ExperimentalExactVersion verifies
// that an unpinned cluster whose ExperimentalFeatures.ControlPlaneExactVersion is
// set is held at that exact version by the forced controller (no pin is created).
func TestForcedClusterDesiredVersionSyncer_SyncOnce_ExperimentalExactVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const clusterName = "c1"

	cluster := newTestCluster(clusterName, "stable", "4.21")
	cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion = v("4.17.3")
	serviceProviderCluster := newTestServiceProviderCluster(clusterName, nil, nil, nil) // unpinned, no desired yet

	mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	_, lister := newTestRolloutStore(t) // no rollout needed for the exact-version path

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
	assert.True(t, updated.Spec.ControlPlaneVersion.DesiredVersion.EQ(*v("4.17.3")),
		"expected desired held at experimental exact version, got %v", updated.Spec.ControlPlaneVersion.DesiredVersion)
	assert.Nil(t, updated.Spec.PinnedVersion.ExactVersion, "no pin should be created for an experimental exact version")
}

func TestForcedClusterDesiredVersionSyncer_Immediate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		rollout     *fleetapi.ControlPlaneVersionRollout
		wantDesired *semver.Version
	}{
		{
			name:        "canaries still progressing",
			rollout:     rolloutWithCounts("4.21.6", 3, 3, 0, 0, 0),
			wantDesired: v("4.21.6"),
		},
		{
			name:        "canaries exceeded failure budget",
			rollout:     rolloutWithCounts("4.21.6", 3, 3, 0, 0, 3),
			wantDesired: v("4.21.6"),
		},
		{
			name:        "rollout not created yet",
			wantDesired: v("4.21.4"),
		},
		{
			name:        "best not selected yet",
			rollout:     newTestRollout("stable-4.21", nil, fleetapi.ControlPlaneVersionRolloutStatus{}),
			wantDesired: v("4.21.4"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			// Use the desired minor's channel even while a different minor is requested.
			cluster := newTestCluster("immediate", "stable", "4.22")
			cluster.ServiceProviderProperties.ExperimentalFeatures.ZStreamUpdatePolicy = coreapi.ImmediateZStreamUpdatePolicy
			serviceProviderCluster := newTestServiceProviderCluster("immediate", v("4.21.4"), nil, nil)
			mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
			require.NoError(t, err)
			var rollouts []*fleetapi.ControlPlaneVersionRollout
			if tc.rollout != nil {
				// The same fleet state cannot advance an ordinary cluster. Also keep
				// an active batch cooldown: Immediate does not reserve normal batches.
				decision := rolloutDecision(tc.rollout, 4, 1, NewDefaultRolloutConfig())
				require.Zero(t, decision.SelectCount)
				tc.rollout.Status.LastAssignmentTime = &metav1.Time{Time: statusTestNow}
				rollouts = append(rollouts, tc.rollout)
			}
			_, lister := newTestRolloutStore(t, rollouts...)
			var beforeRollout *fleetapi.ControlPlaneVersionRollout
			if tc.rollout != nil {
				beforeRollout, err = lister.Get(ctx, "stable-4.21")
				require.NoError(t, err)
			}
			syncer := &forcedClusterDesiredVersionSyncer{
				clock: clocktesting.NewFakeClock(statusTestNow), resourcesDBClient: mockDB,
				clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
				serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
				rolloutLister:                lister,
			}
			key := controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: "immediate"}
			require.NoError(t, syncer.SyncOnce(ctx, key))
			updated, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, "immediate").Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			assert.Equal(t, tc.wantDesired, updated.Spec.ControlPlaneVersion.DesiredVersion)
			if tc.wantDesired.GT(*serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion) {
				require.NotNil(t, updated.Spec.ControlPlaneVersion.DesiredVersionLastTransitionTime)
				assert.True(t, updated.Spec.ControlPlaneVersion.DesiredVersionLastTransitionTime.Time.Equal(statusTestNow))
			}
			assert.Empty(t, updated.Spec.PinnedVersion, "Immediate must not create a pin")
			if tc.rollout != nil {
				after, err := lister.Get(ctx, "stable-4.21")
				require.NoError(t, err)
				assert.Equal(t, beforeRollout.Status, after.Status, "Immediate must not change fleet rollout status")
			}
		})
	}
}
