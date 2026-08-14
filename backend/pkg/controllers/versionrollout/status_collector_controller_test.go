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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

func TestComputeRolloutStatusCounts(t *testing.T) {
	t.Parallel()

	cfg := NewDefaultRolloutConfig()
	cfg.MinVersionReadyDuration = time.Hour
	cfg.MaxUpgradeDuration["4.21"] = 2 * time.Hour

	spcs := []*coreapi.ServiceProviderCluster{
		// achieved 4.21.6, stable long enough -> successful
		newTestSPC("done-stable", v("4.21.6"), []coreapi.HCPClusterActiveVersion{completed("4.21.6")}, nil),
		// achieved 4.21.6, but not stable long enough -> achieved but not successful
		newTestSPC("done-fresh", v("4.21.6"), []coreapi.HCPClusterActiveVersion{completed("4.21.6")}, nil),
		// desires 4.21.6, still in flight (base is 4.21.4) -> mismatched
		newTestSPC("upgrading", v("4.21.6"), []coreapi.HCPClusterActiveVersion{partial("4.21.6"), completed("4.21.4")}, nil),
		// desires 4.21.6, in flight beyond max upgrade duration -> failed
		newTestSPC("stuck", v("4.21.6"), []coreapi.HCPClusterActiveVersion{partial("4.21.6"), completed("4.21.4")}, nil),
	}

	ages := fakeAgeSource{
		achieved: map[string]time.Duration{
			"done-stable": 2 * time.Hour, // > MinVersionReadyDuration
			"done-fresh":  10 * time.Minute,
		},
		mismatch: map[string]time.Duration{
			"upgrading": 30 * time.Minute, // < MaxUpgradeDuration
			"stuck":     3 * time.Hour,    // > MaxUpgradeDuration
		},
	}

	counts := computeRolloutStatusCounts(spcs, cfg, ages)

	assert.Equal(t, int64(4), counts.Desired["4.21.6"], "all four desire 4.21.6")
	assert.Equal(t, int64(2), counts.Achieved["4.21.6"], "two have achieved 4.21.6")
	assert.Equal(t, int64(1), counts.Successful["4.21.6"], "one has been stable long enough")
	assert.Equal(t, int64(2), counts.Mismatched["4.21.6"], "two are still upgrading")
	assert.Equal(t, int64(1), counts.Failed["4.21.6"], "one is stuck beyond the max upgrade duration")
	// bases of in-flight clusters also count as achieved 4.21.4
	assert.Equal(t, int64(2), counts.Achieved["4.21.4"])
}

func TestComputeRolloutStatusCounts_UnknownAgesLeaveFailedAndSuccessfulEmpty(t *testing.T) {
	t.Parallel()
	cfg := NewDefaultRolloutConfig()
	cfg.MaxUpgradeDuration["4.21"] = time.Hour

	spcs := []*coreapi.ServiceProviderCluster{
		newTestSPC("done", v("4.21.6"), []coreapi.HCPClusterActiveVersion{completed("4.21.6")}, nil),
		newTestSPC("upgrading", v("4.21.6"), []coreapi.HCPClusterActiveVersion{partial("4.21.6"), completed("4.21.4")}, nil),
	}

	counts := computeRolloutStatusCounts(spcs, cfg, UnknownVersionAgeSource{})

	assert.Equal(t, int64(1), counts.Achieved["4.21.6"])
	assert.Equal(t, int64(1), counts.Mismatched["4.21.6"])
	assert.Empty(t, counts.Successful, "unknown ages leave successful empty")
	assert.Empty(t, counts.Failed, "unknown ages leave failed empty")
}

func TestStatusCollectorSyncer_SyncOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const channel = "stable-4.21"

	clusters := []*coreapi.HCPOpenShiftCluster{
		newTestCluster("c1", "stable", "4.21"),
		newTestCluster("c2", "stable", "4.21"),
		newTestCluster("other-group", "fast", "4.21"), // different channel group, excluded
		newTestCluster("other-minor", "stable", "4.22"),
	}
	spcs := []*coreapi.ServiceProviderCluster{
		newTestSPC("c1", v("4.21.6"), []coreapi.HCPClusterActiveVersion{completed("4.21.6")}, nil),
		newTestSPC("c2", v("4.21.6"), []coreapi.HCPClusterActiveVersion{partial("4.21.6"), completed("4.21.4")}, nil),
		newTestSPC("other-group", v("4.21.6"), nil, nil),
		newTestSPC("other-minor", v("4.22.0"), nil, nil),
	}

	store := newFakeRolloutStore(newTestRollout(channel, v("4.21.6"), fleetapi.ControlPlaneVersionRolloutStatus{}))
	syncer := NewStatusCollectorSyncer(
		store, store,
		&corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: spcs},
		&corelistertesting.SliceClusterLister{Clusters: clusters},
		UnknownVersionAgeSource{},
		NewDefaultRolloutConfig(),
	)

	require.NoError(t, syncer.SyncOnce(ctx, RolloutKey{YStreamChannel: channel}))

	got, err := store.Get(ctx, channel)
	require.NoError(t, err)
	// Only c1 and c2 are in the stable-4.21 channel.
	assert.Equal(t, int64(2), got.Status.ClusterCountByDesiredExactVersion["4.21.6"])
	assert.Equal(t, int64(1), got.Status.ClusterCountByAchievedExactVersion["4.21.6"], "only c1 fully achieved 4.21.6")
	assert.Equal(t, int64(1), got.Status.MismatchedClusterCountByDesiredExactVersion["4.21.6"], "c2 still upgrading")
}
