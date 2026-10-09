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
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
)

func TestAtLeastBackendVersion(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    bool
	}{
		{"4.19.999", false}, {"4.20", true}, {"4.20.0-rc.1", true},
		{"4.20.1+build", true}, {"4.21.0", true}, {"5.0", true},
		{"3.99.99", false}, {"", false}, {"invalid", false}, {"4.20.invalid", false},
	} {
		t.Run(tc.version, func(t *testing.T) {
			require.Equal(t, tc.want, atLeastBackendVersion(tc.version))
		})
	}
}

func TestCincinnatiDiscoveryProfiles(t *testing.T) {
	for _, tc := range []struct {
		name     string
		profiles []coreapi.VersionProfile
		want     []controllerutils.ControlPlaneVersionRolloutKey
		invalid  bool
	}{
		{name: "empty"},
		{
			name:     "sort and deduplicate normalized profiles",
			profiles: []coreapi.VersionProfile{{ChannelGroup: "stable", ID: "4.21.1"}, {ChannelGroup: "fast", ID: "4.20"}, {ChannelGroup: "stable", ID: "4.21"}},
			want:     []controllerutils.ControlPlaneVersionRolloutKey{{YStreamChannel: "fast-4.20"}, {YStreamChannel: "stable-4.21"}},
		},
		{
			name:     "floor and all allowed groups including prereleases",
			profiles: []coreapi.VersionProfile{{ChannelGroup: "stable", ID: "4.19"}, {ChannelGroup: "fast", ID: "4.20.2"}, {ChannelGroup: "nightly", ID: "4.21.0-0.nightly"}, {ChannelGroup: "candidate", ID: "4.20.0-rc.1"}},
			want:     []controllerutils.ControlPlaneVersionRolloutKey{{YStreamChannel: "candidate-4.20"}, {YStreamChannel: "fast-4.20"}, {YStreamChannel: "nightly-4.21"}},
		},
		{
			name:     "invalid rejects partial results",
			profiles: []coreapi.VersionProfile{{ChannelGroup: "stable", ID: "4.21"}, {ChannelGroup: "fast", ID: "invalid"}},
			invalid:  true,
		},
		{
			name:     "unknown group below floor is still invalid",
			profiles: []coreapi.VersionProfile{{ChannelGroup: "stable", ID: "4.21"}, {ChannelGroup: "other", ID: "4.19"}},
			invalid:  true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := append([]coreapi.VersionProfile(nil), tc.profiles...)
			got, err := discoverChannels(tc.profiles)
			require.Equal(t, tc.invalid, err != nil)
			require.Equal(t, tc.want, got)
			require.Equal(t, before, tc.profiles, "discovery must not mutate client profiles")
		})
	}
}

type cincinnatiGraphData struct {
	profiles []coreapi.VersionProfile
	err      error
}

func (f cincinnatiGraphData) VersionProfiles(context.Context) ([]coreapi.VersionProfile, error) {
	return f.profiles, f.err
}

type cincinnatiQueue struct {
	keys []controllerutils.ControlPlaneVersionRolloutKey
}

func (q *cincinnatiQueue) Enqueue(key any) {
	q.keys = append(q.keys, key.(controllerutils.ControlPlaneVersionRolloutKey))
}

func TestCincinnatiDiscoveryEnqueuesOnlySuccessfulBatches(t *testing.T) {
	queue := &cincinnatiQueue{}
	profile := coreapi.VersionProfile{ChannelGroup: "fast", ID: "4.21"}
	syncer := &cincinnatiSeedingSyncer{queue: queue}
	for _, data := range []cincinnatiGraphData{
		{profiles: []coreapi.VersionProfile{profile}, err: errors.New("HTTP unavailable")},
		{profiles: []coreapi.VersionProfile{profile, {ChannelGroup: "fast", ID: "invalid"}}},
	} {
		syncer.discovery = data
		require.Error(t, syncer.discover(t.Context()))
		require.Empty(t, queue.keys)
	}
	syncer.discovery = cincinnatiGraphData{profiles: []coreapi.VersionProfile{profile}}
	require.NoError(t, syncer.discover(t.Context()))
	require.Equal(t, []controllerutils.ControlPlaneVersionRolloutKey{{YStreamChannel: "fast-4.21"}}, queue.keys)
	// Previously queued work remains eligible after a later empty graph.
	syncer.discovery = cincinnatiGraphData{}
	require.NoError(t, syncer.discover(t.Context()))
	fleet, _ := newTestRolloutStore(t)
	syncer.fleetDBClient = fleet
	require.NoError(t, syncer.SyncOnce(t.Context(), queue.keys[0]))
	got, err := fleet.ControlPlaneVersionRollouts().Get(t.Context(), "fast-4.21")
	require.NoError(t, err)
	require.Equal(t, profile, got.Spec.Version)
}

func TestCincinnatiSeedingPreservesExistingState(t *testing.T) {
	for _, channel := range []string{"stable-4.19", "stable-4.21"} {
		t.Run(channel, func(t *testing.T) {
			existing := newTestRollout(channel, v("4.21.6"), fleetapi.ControlPlaneVersionRolloutStatus{ClusterCountByDesiredExactVersion: map[string]int64{"4.21.6": 9}})
			fleet, _ := newTestRolloutStore(t, existing)
			before := fleet.GetAllDocuments()
			syncer := &cincinnatiSeedingSyncer{fleetDBClient: fleet}
			require.NoError(t, syncer.SyncOnce(t.Context(), controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: channel}))
			require.Equal(t, before, fleet.GetAllDocuments(), "existing documents, including their etags, must remain untouched")
		})
	}
}

func TestCincinnatiSeedingQueuedFloorGuard(t *testing.T) {
	fleet, _ := newTestRolloutStore(t)
	syncer := &cincinnatiSeedingSyncer{fleetDBClient: fleet}
	for _, channel := range []string{"stable-4.19", "nightly-4.19", "fast-3.99"} {
		require.NoError(t, syncer.SyncOnce(t.Context(), controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: channel}))
	}
	require.Empty(t, fleet.GetAllDocuments(), "queued below-floor keys must not create prospective rollouts")
	for _, channel := range []string{"stable-4.20", "nightly-4.21", "candidate-5.0"} {
		key := controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: channel}
		require.NoError(t, syncer.SyncOnce(t.Context(), key))
		got, err := fleet.ControlPlaneVersionRollouts().Get(t.Context(), channel)
		require.NoError(t, err)
		want, err := newControlPlaneVersionRollout(channel)
		require.NoError(t, err)
		require.Equal(t, want.Spec, got.Spec)
		require.Equal(t, want.Status, got.Status)
		require.Equal(t, want.ResourceID, got.ResourceID)
		require.Equal(t, want.PartitionKey, got.PartitionKey)
	}
	for _, channel := range []string{"fast-4.21.1", "other-4.21", "stable-invalid"} {
		require.Error(t, syncer.SyncOnce(t.Context(), controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: channel}))
	}
}

type cincinnatiFleetClient struct {
	fleetcosmosstorage.FleetDBClient
	crud cosmosstorageutils.ValidatingResourceCRUD[fleetapi.ControlPlaneVersionRollout, *fleetapi.ControlPlaneVersionRollout]
}

func (c cincinnatiFleetClient) ControlPlaneVersionRollouts() cosmosstorageutils.ValidatingResourceCRUD[fleetapi.ControlPlaneVersionRollout, *fleetapi.ControlPlaneVersionRollout] {
	return c.crud
}

type cincinnatiGetHook struct {
	cosmosstorageutils.ValidatingResourceCRUD[fleetapi.ControlPlaneVersionRollout, *fleetapi.ControlPlaneVersionRollout]
	get func(context.Context, string) (*fleetapi.ControlPlaneVersionRollout, error)
}

func (c cincinnatiGetHook) Get(ctx context.Context, name string) (*fleetapi.ControlPlaneVersionRollout, error) {
	return c.get(ctx, name)
}

func TestCincinnatiSeedingRetriesStorageErrors(t *testing.T) {
	fleet, _ := newTestRolloutStore(t)
	crud := fleet.ControlPlaneVersionRollouts()
	key := controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.21"}
	syncer := &cincinnatiSeedingSyncer{fleetDBClient: cincinnatiFleetClient{crud: cincinnatiGetHook{
		ValidatingResourceCRUD: crud,
		get: func(context.Context, string) (*fleetapi.ControlPlaneVersionRollout, error) {
			return nil, errors.New("read unavailable")
		},
	}}}
	require.ErrorContains(t, syncer.SyncOnce(t.Context(), key), "read unavailable")
	require.Empty(t, fleet.GetAllDocuments())
	syncer.fleetDBClient = cincinnatiFleetClient{crud: cincinnatiGetHook{
		ValidatingResourceCRUD: crud,
		get: func(ctx context.Context, name string) (*fleetapi.ControlPlaneVersionRollout, error) {
			got, err := crud.Get(ctx, name)
			require.True(t, cosmosstorageutils.IsNotFoundError(err))
			_, createErr := crud.Create(ctx, newTestRollout(name, nil, fleetapi.ControlPlaneVersionRolloutStatus{}), nil)
			require.NoError(t, createErr)
			return got, err
		},
	}}
	require.Error(t, syncer.SyncOnce(t.Context(), key), "a concurrent create must be retried")
	syncer.fleetDBClient = fleet
	before := fleet.GetAllDocuments()
	require.NoError(t, syncer.SyncOnce(t.Context(), key), "retry observes the concurrent create")
	require.Equal(t, before, fleet.GetAllDocuments())
	require.NoError(t, crud.Delete(t.Context(), key.YStreamChannel))
	require.Error(t, syncer.SyncOnce(t.Context(), key), "creation retries while a soft-delete tombstone remains")
}
