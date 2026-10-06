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

package rollout

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/validation"
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

func newSeedingSyncer(t *testing.T, ctx context.Context, cluster *coreapi.Cluster, rollouts ...*fleetapi.ControlPlaneVersionRollout) (*rolloutSeedingSyncer, *fleetcosmosstoragetesting.MockFleetDBClient) {
	t.Helper()
	mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster})
	require.NoError(t, err, "failed to build mock resources DB client")
	mockFleet, rolloutLister := newTestRolloutStore(t, rollouts...)
	return &rolloutSeedingSyncer{
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockDB},
		rolloutLister:                rolloutLister,
		fleetDBClient:                mockFleet,
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
		require.Empty(t, cmp.Diff(fleetapi.ControlPlaneVersionRolloutSpec{Version: coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}}, got.Spec))
		require.Empty(t, cmp.Diff(fleetapi.ControlPlaneVersionRolloutStatus{}, got.Status))
	})

	t.Run("no-op when the rollout already exists", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		existing := newTestRollout("stable-4.21", v("4.21.6"), fleetapi.ControlPlaneVersionRolloutStatus{})
		syncer, mockFleet := newSeedingSyncer(t, ctx, newTestCluster("c1", "stable", "4.21"), existing)
		syncer.fleetDBClient = nil // Existing cached rollouts require no live database access.

		require.NoError(t, syncer.SyncOnce(ctx, key))

		got, err := mockFleet.ControlPlaneVersionRollouts().Get(ctx, "stable-4.21")
		require.NoError(t, err)
		assert.Equal(t, int64(1), got.GetInstanceVersion(), "existing rollout must not be rewritten")
		require.NotNil(t, got.Spec.BestExactVersion, "existing rollout content must be preserved")
		assert.True(t, got.Spec.BestExactVersion.EQ(*v("4.21.6")))
	})

	t.Run("seeds pinned minor in addition to requested minor", func(t *testing.T) {
		ctx := context.Background()
		syncer, fleet := newSeedingSyncer(t, ctx, newTestCluster("c1", "stable", "4.22"))
		syncer.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{
			newTestServiceProviderCluster("c1", v("4.21.4"), nil, &coreapi.ServiceProviderClusterPinnedVersion{ExactVersion: v("4.21.4"), UntilExactVersion: v("4.21.6")}),
		}}
		require.NoError(t, syncer.SyncOnce(ctx, key))
		for _, yStreamChannel := range []string{"stable-4.22", "stable-4.21"} {
			got, err := fleet.ControlPlaneVersionRollouts().Get(ctx, yStreamChannel)
			require.NoError(t, err, "requested and pinned channels must both exist")
			require.Empty(t, cmp.Diff(newTestRollout(yStreamChannel, nil, fleetapi.ControlPlaneVersionRolloutStatus{}).Spec, got.Spec))
		}
	})

	t.Run("nightly uses exact override without a graph rollout", func(t *testing.T) {
		ctx := context.Background()
		syncer, fleet := newSeedingSyncer(t, ctx, newTestCluster("c1", "nightly", "4.22"))
		require.NoError(t, syncer.SyncOnce(ctx, key))
		_, err := fleet.ControlPlaneVersionRollouts().Get(ctx, "nightly-4.22")
		require.True(t, cosmosstorageutils.IsNotFoundError(err))
		best, err := NewCincinnatiBestVersionSelector().BestExactVersionForProfile(ctx, coreapi.VersionProfile{ID: "4.22", ChannelGroup: "nightly"})
		require.NoError(t, err)
		require.Nil(t, best)
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

func TestNewControlPlaneVersionRollout(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		profile coreapi.VersionProfile
		channel string
	}{
		{name: "minor", profile: coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}, channel: "stable-4.21"},
		{name: "exact", profile: coreapi.VersionProfile{ID: "4.21.5", ChannelGroup: "stable"}, channel: "stable-4.21"},
		{name: "prerelease", profile: coreapi.VersionProfile{ID: "4.22.0-rc.1", ChannelGroup: "fast"}, channel: "fast-4.22"},
		{name: "below floor", profile: coreapi.VersionProfile{ID: "4.19.5", ChannelGroup: "stable"}, channel: "stable-4.19"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := newControlPlaneVersionRollout(tc.profile)
			require.NoError(t, err)
			want := newTestRollout(tc.channel, nil, fleetapi.ControlPlaneVersionRolloutStatus{})
			require.Empty(t, cmp.Diff(want, got, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})))
			require.Empty(t, validation.ValidateControlPlaneVersionRolloutCreate(t.Context(), got))
		})
	}
}

func TestRolloutSeedingSyncer_ExistingProfiles(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		profile coreapi.VersionProfile
	}{
		{name: "normalized legacy no-op"},
		{name: "valid no-op", profile: coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			clock := clocktesting.NewFakeClock(statusTestNow)
			existing := newTestRollout("stable-4.21", v("4.21.6"), fleetapi.ControlPlaneVersionRolloutStatus{
				LastAssignmentTime: &metav1.Time{Time: clock.Now()},
				Conditions: []metav1.Condition{{
					Type: ConditionProgressing, Status: metav1.ConditionTrue, LastTransitionTime: metav1.Time{Time: clock.Now()}, Reason: "Canary",
				}},
				ClusterCountByDesiredExactVersion:            map[string]int64{"4.21.6": 5},
				MismatchedClusterCountByDesiredExactVersion:  map[string]int64{"4.21.6": 3},
				FailedClusterCountByDesiredExactVersion:      map[string]int64{"4.21.6": 1},
				ClusterCountByAchievedExactVersion:           map[string]int64{"4.21.6": 2},
				SuccessfulClusterCountByAchievedExactVersion: map[string]int64{"4.21.6": 1},
			})
			syncer, db := newSeedingSyncer(t, ctx, newTestCluster("c1", "stable", "4.21.5"), existing)
			crud := db.ControlPlaneVersionRollouts()
			stored, err := crud.Get(ctx, "stable-4.21")
			require.NoError(t, err)
			// Seed persisted shapes directly: new creates require a valid profile.
			data, ok := db.GetDocument(stored.GetCosmosUID())
			require.True(t, ok)
			var doc cosmosstorageutils.GenericDocument[fleetapi.ControlPlaneVersionRollout]
			require.NoError(t, json.Unmarshal(data, &doc))
			doc.Content.Spec.Version = tc.profile
			data, err = json.Marshal(doc)
			require.NoError(t, err)
			db.StoreDocument(doc.ID, data)
			before, err := crud.Get(ctx, "stable-4.21")
			require.NoError(t, err)

			err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
				SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: "c1",
			})
			require.NoError(t, err)
			got, err := crud.Get(ctx, "stable-4.21")
			require.NoError(t, err)
			want := before.DeepCopy()
			if tc.profile == (coreapi.VersionProfile{}) {
				want.Spec.Version = coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}
				require.Empty(t, validation.ValidateControlPlaneVersionRolloutCreate(ctx, got))
			}
			persisted, ok := db.GetDocument(doc.ID)
			require.True(t, ok)
			require.Equal(t, json.RawMessage(data), persisted, "seeding must not rewrite existing documents")
			require.Empty(t, cmp.Diff(want, got, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})))
			require.NoError(t, syncer.ensureRollout(ctx, coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}))
			after, err := crud.Get(ctx, "stable-4.21")
			require.NoError(t, err)
			require.Empty(t, cmp.Diff(got, after, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "repeated seeding must not rewrite a valid profile")
		})
	}
}
