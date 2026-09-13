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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

func TestClusterYStreamChannel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		channelGroup string
		versionID    string
		wantChannel  string
		wantOK       bool
	}{
		{name: "minor only", channelGroup: "stable", versionID: "4.21", wantChannel: "stable-4.21", wantOK: true},
		{name: "full version", channelGroup: "stable", versionID: "4.21.5", wantChannel: "stable-4.21", wantOK: true},
		{name: "fast group", channelGroup: "fast", versionID: "4.22.0", wantChannel: "fast-4.22", wantOK: true},
		{name: "no channel group", channelGroup: "", versionID: "4.21", wantOK: false},
		{name: "unparseable version", channelGroup: "stable", versionID: "not-a-version", wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := clusterYStreamChannel(newTestCluster("c1", tc.channelGroup, tc.versionID))
			assert.Equal(t, tc.wantOK, ok, "ok")
			if tc.wantOK {
				assert.Equal(t, tc.wantChannel, got, "channel")
			}
		})
	}
}

func newSeedingSyncer(t *testing.T, ctx context.Context, cluster *coreapi.HCPOpenShiftCluster, rollouts ...*fleetapi.ControlPlaneVersionRollout) (*rolloutSeedingSyncer, *fleetcosmosstoragetesting.MockFleetDBClient) {
	t.Helper()
	mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster})
	require.NoError(t, err, "failed to build mock resources DB client")
	mockFleet, lister := newTestRolloutStore(t, rollouts...)
	return &rolloutSeedingSyncer{
		clusterLister: &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		rolloutLister: lister,
		fleetDBClient: mockFleet,
	}, mockFleet
}

func TestRolloutSeedingSyncer_SyncOnce(t *testing.T) {
	t.Parallel()

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    "c1",
	}

	t.Run("creates rollout when the channel has none", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		syncer, mockFleet := newSeedingSyncer(t, ctx, newTestCluster("c1", "stable", "4.21"))

		require.NoError(t, syncer.SyncOnce(ctx, key))

		got, err := mockFleet.ControlPlaneVersionRollouts().Get(ctx, "stable-4.21")
		require.NoError(t, err, "expected the rollout to have been created")
		assert.Nil(t, got.Spec.BestExactVersion, "a freshly seeded rollout has no best version")
	})

	t.Run("no-op when the rollout already exists", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		existing := newTestRollout("stable-4.21", v("4.21.6"), fleetapi.ControlPlaneVersionRolloutStatus{})
		syncer, mockFleet := newSeedingSyncer(t, ctx, newTestCluster("c1", "stable", "4.21"), existing)

		require.NoError(t, syncer.SyncOnce(ctx, key))

		got, err := mockFleet.ControlPlaneVersionRollouts().Get(ctx, "stable-4.21")
		require.NoError(t, err)
		assert.Equal(t, int64(1), got.GetInstanceVersion(), "existing rollout must not be rewritten")
		require.NotNil(t, got.Spec.BestExactVersion, "existing rollout content must be preserved")
		assert.True(t, got.Spec.BestExactVersion.EQ(*v("4.21.6")))
	})

	t.Run("skips a cluster with no channel group", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		syncer, mockFleet := newSeedingSyncer(t, ctx, newTestCluster("c1", "", "4.21"))

		require.NoError(t, syncer.SyncOnce(ctx, key))

		_, err := mockFleet.ControlPlaneVersionRollouts().Get(ctx, "-4.21")
		assert.True(t, cosmosstorageutils.IsNotFoundError(err), "no rollout should be created without a channel group")
	})

	t.Run("skips a cluster being deleted", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		cluster := newTestCluster("c1", "stable", "4.21")
		cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: statusTestNow}
		syncer, mockFleet := newSeedingSyncer(t, ctx, cluster)

		require.NoError(t, syncer.SyncOnce(ctx, key))

		_, err := mockFleet.ControlPlaneVersionRollouts().Get(ctx, "stable-4.21")
		assert.True(t, cosmosstorageutils.IsNotFoundError(err), "no rollout should be created for a deleting cluster")
	})
}
