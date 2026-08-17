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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

func TestEligibleClusters(t *testing.T) {
	t.Parallel()
	best := *v("4.21.6")

	spcs := []*coreapi.ServiceProviderCluster{
		newTestSPC("below", v("4.21.4"), nil, nil),                           // eligible: below, unpinned
		newTestSPC("at-best", v("4.21.6"), nil, nil),                         // not eligible: already at best
		newTestSPC("above", v("4.21.8"), nil, nil),                           // not eligible: above best
		newTestSPC("no-desired", nil, nil, nil),                              // eligible: no desired yet
		newTestSPC("pin-release", v("4.21.4"), nil, pin("4.21.2", "4.21.6")), // eligible: pin releases at best
		newTestSPC("pin-hold", v("4.21.4"), nil, pin("4.21.2", "4.21.9")),    // not eligible: pin still holds
		newTestSPC("pin-forever", v("4.21.4"), nil, pin("4.21.2", "")),       // not eligible: no release version
	}

	eligible := eligibleClusters(spcs, best)

	names := map[string]bool{}
	for _, spc := range eligible {
		names[spcClusterName(spc)] = true
	}
	assert.True(t, names["below"])
	assert.True(t, names["no-desired"])
	assert.True(t, names["pin-release"])
	assert.False(t, names["at-best"])
	assert.False(t, names["above"])
	assert.False(t, names["pin-hold"])
	assert.False(t, names["pin-forever"])
	assert.Len(t, eligible, 3)
}

func rolloutWithCounts(bestStr string, desired, mismatched, achieved, successful, failed int64) *fleetapi.ControlPlaneVersionRollout {
	key := v(bestStr).String()
	return newTestRollout("stable-4.21", v(bestStr), fleetapi.ControlPlaneVersionRolloutStatus{
		ClusterCountByDesiredExactVersion:            map[string]int64{key: desired},
		MismatchedClusterCountByDesiredExactVersion:  map[string]int64{key: mismatched},
		ClusterCountByAchievedExactVersion:           map[string]int64{key: achieved},
		SuccessfulClusterCountByAchievedExactVersion: map[string]int64{key: successful},
		FailedClusterCountByDesiredExactVersion:      map[string]int64{key: failed},
	})
}

func TestRolloutDecision(t *testing.T) {
	t.Parallel()

	// canary=5, rolling=20 so the rolling threshold (20) exceeds the canary
	// threshold (5%+2=7) and the rolling branch is reachable.
	cfg := NewDefaultRolloutConfig()
	cfg.CanaryPercentage = 5
	cfg.RollingPercentage = 20

	tests := []struct {
		name          string
		rollout       *fleetapi.ControlPlaneVersionRollout
		totalClusters int
		eligibleCount int
		wantOutcome   rolloutOutcome
		wantSelect    int
	}{
		{
			name:        "no best version",
			rollout:     newTestRollout("stable-4.21", nil, fleetapi.ControlPlaneVersionRolloutStatus{}),
			wantOutcome: outcomeNoBest,
		},
		{
			name:          "failure budget - absolute",
			rollout:       rolloutWithCounts("4.21.6", 100, 0, 0, 0, 3),
			totalClusters: 100,
			eligibleCount: 50,
			wantOutcome:   outcomeFailure,
		},
		{
			name:          "failure budget - fraction",
			rollout:       rolloutWithCounts("4.21.6", 10, 0, 0, 0, 2),
			totalClusters: 100,
			eligibleCount: 50,
			wantOutcome:   outcomeFailure,
		},
		{
			name:          "no eligible clusters is stable",
			rollout:       rolloutWithCounts("4.21.6", 0, 0, 0, 0, 0),
			totalClusters: 100,
			eligibleCount: 0,
			wantOutcome:   outcomeStable,
		},
		{
			name:          "canary selects up to threshold",
			rollout:       rolloutWithCounts("4.21.6", 0, 0, 0, 0, 0),
			totalClusters: 100,
			eligibleCount: 50,
			wantOutcome:   outcomeCanary,
			wantSelect:    7, // ceil(5% of 100) + 2
		},
		{
			name:          "canary clamped by eligible count",
			rollout:       rolloutWithCounts("4.21.6", 0, 0, 0, 0, 0),
			totalClusters: 100,
			eligibleCount: 3,
			wantOutcome:   outcomeCanary,
			wantSelect:    3,
		},
		{
			name:          "canary gate waits for successful canaries",
			rollout:       rolloutWithCounts("4.21.6", 7, 5, 2, 2, 0), // inFlight=7 (>=7), successful=2 (<5)
			totalClusters: 100,
			eligibleCount: 50,
			wantOutcome:   outcomeProgressing,
		},
		{
			name:          "rolling selects after canary gate passes",
			rollout:       rolloutWithCounts("4.21.6", 7, 5, 2, 5, 0), // inFlight=7, successful=5 (gate open), rollingThreshold=20
			totalClusters: 100,
			eligibleCount: 50,
			wantOutcome:   outcomeRolling,
			wantSelect:    13, // 20 - 7
		},
		{
			name:          "steady progressing once rolling target met",
			rollout:       rolloutWithCounts("4.21.6", 20, 10, 10, 5, 0), // inFlight=20 >= rollingThreshold=20
			totalClusters: 100,
			eligibleCount: 50,
			wantOutcome:   outcomeProgressing,
			wantSelect:    0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := rolloutDecision(tc.rollout, tc.totalClusters, tc.eligibleCount, cfg)
			assert.Equal(t, tc.wantOutcome, got.Outcome, "outcome (message: %s)", got.Message)
			assert.Equal(t, tc.wantSelect, got.SelectCount, "selectCount")
		})
	}
}

func TestNormalClusterDesiredVersionSyncer_SyncOnce_Canary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const channel = "stable-4.21"

	clusters := []*coreapi.HCPOpenShiftCluster{
		newTestCluster("c1", "stable", "4.21"),
		newTestCluster("c2", "stable", "4.21"),
		newTestCluster("c3", "stable", "4.21"),
		newTestCluster("c4", "stable", "4.21"),
	}
	spcs := []*coreapi.ServiceProviderCluster{
		newTestSPC("c1", v("4.21.4"), nil, nil),
		newTestSPC("c2", v("4.21.4"), nil, nil),
		newTestSPC("c3", v("4.21.4"), nil, nil),
		newTestSPC("c4", v("4.21.4"), nil, nil),
	}

	resources := make([]any, 0, len(clusters)+len(spcs))
	for _, c := range clusters {
		resources = append(resources, c)
	}
	for _, s := range spcs {
		resources = append(resources, s)
	}
	mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
	require.NoError(t, err)

	// Fresh rollout at 4.21.6: canary threshold = ceil(5% of 4) + 2 = 3.
	store := newFakeRolloutStore(newTestRollout(channel, v("4.21.6"), fleetapi.ControlPlaneVersionRolloutStatus{}))

	syncer := NewNormalClusterDesiredVersionSyncer(
		mockDB, store, store,
		&corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		&corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		firstNSelector{},
		NewDefaultRolloutConfig(),
	)

	require.NoError(t, syncer.SyncOnce(ctx, controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: channel}))

	atBest := 0
	for _, name := range []string{"c1", "c2", "c3", "c4"} {
		updated, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, name).
			Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		require.NotNil(t, updated.Spec.ControlPlaneVersion.DesiredVersion)
		if updated.Spec.ControlPlaneVersion.DesiredVersion.EQ(*v("4.21.6")) {
			atBest++
		}
	}
	assert.Equal(t, 3, atBest, "canary should have advanced exactly 3 of 4 clusters to best")
}
