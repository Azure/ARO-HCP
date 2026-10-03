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
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
)

type seedQueue struct{ keys []rolloutSeedKey }

func (q *seedQueue) Enqueue(key rolloutSeedKey) { q.keys = append(q.keys, key) }

type failingSPCList struct {
	corelisters.ServiceProviderClusterLister
}

func (f failingSPCList) List(context.Context) ([]*coreapi.ServiceProviderCluster, error) {
	return nil, errors.New("SPC scan failed")
}

func TestReconcileSeeding(t *testing.T) {
	profile := coreapi.VersionProfile{ChannelGroup: "stable", ID: "4.21"}
	clock := clocktesting.NewFakeClock(statusTestNow)
	existing := newTestRollout("stable-4.21", v("4.21.6"), fleetapi.ControlPlaneVersionRolloutStatus{
		LastAssignmentTime:                &metav1.Time{Time: clock.Now()},
		Conditions:                        []metav1.Condition{{Type: "Progressing", Status: metav1.ConditionTrue}},
		ClusterCountByDesiredExactVersion: map[string]int64{"4.21.6": 7},
	})
	legacy := existing.DeepCopy()
	legacy.Spec.Version = coreapi.VersionProfile{}
	mismatch := existing.DeepCopy()
	mismatch.Spec.Version.ID = "4.22"
	partial := existing.DeepCopy()
	partial.Spec.Version.ID = ""
	for _, tc := range []struct {
		name           string
		profile        coreapi.VersionProfile
		existing, want *fleetapi.ControlPlaneVersionRollout
		wantError      bool
	}{
		{"create", profile, nil, newTestRollout("stable-4.21", nil, fleetapi.ControlPlaneVersionRolloutStatus{}), false},
		{"unchanged", profile, existing, nil, false},
		{"backfill preserves state", profile, legacy, existing, false},
		{"mismatched spec", profile, mismatch, nil, true},
		{"partial spec", profile, partial, nil, true},
		{"wrong key", coreapi.VersionProfile{ChannelGroup: "fast", ID: "4.21"}, legacy, nil, true},
		{"noncanonical key", coreapi.VersionProfile{ChannelGroup: "stable", ID: "4.21.3"}, nil, nil, true},
		{"invalid key", coreapi.VersionProfile{}, nil, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.existing.DeepCopy()
			got, err := reconcileSeeding(tc.profile, tc.existing)
			require.Equal(t, tc.wantError, err != nil)
			require.Empty(t, cmp.Diff(tc.want, got, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})))
			require.Empty(t, cmp.Diff(before, tc.existing, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "cached state must not change")
		})
	}
}

func TestDiscoveryProfiles(t *testing.T) {
	for _, tc := range []struct {
		name      string
		profiles  []coreapi.VersionProfile
		want      []rolloutSeedKey
		wantError bool
	}{
		{"empty", nil, nil, false},
		{"sort and deduplicate normalized profiles", []coreapi.VersionProfile{{ChannelGroup: "stable", ID: "4.21.1"}, {ChannelGroup: "fast", ID: "4.20"}, {ChannelGroup: "stable", ID: "4.21"}}, []rolloutSeedKey{{Version: coreapi.VersionProfile{ChannelGroup: "fast", ID: "4.20"}}, {Version: coreapi.VersionProfile{ChannelGroup: "stable", ID: "4.21"}}}, false},
		{"floor and normalization", []coreapi.VersionProfile{{ChannelGroup: "stable", ID: "4.19"}, {ChannelGroup: "fast", ID: "4.20.2"}, {ChannelGroup: "nightly", ID: "4.21.0-0.nightly"}}, []rolloutSeedKey{{Version: coreapi.VersionProfile{ChannelGroup: "fast", ID: "4.20"}}, {Version: coreapi.VersionProfile{ChannelGroup: "nightly", ID: "4.21"}}}, false},
		{"invalid rejects partial results", []coreapi.VersionProfile{{ChannelGroup: "stable", ID: "4.21"}, {ChannelGroup: "fast", ID: "invalid"}}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keys, err := discoverChannels(tc.profiles)
			require.Equal(t, tc.wantError, err != nil)
			require.Empty(t, cmp.Diff(tc.want, keys))
		})
	}
}

type getOnlyClusterLister struct{ corelisters.ClusterLister }

func (getOnlyClusterLister) List(context.Context) ([]*coreapi.Cluster, error) {
	panic("event must not scan clusters")
}

type getOnlySPCLister struct {
	corelisters.ServiceProviderClusterLister
}

func (getOnlySPCLister) List(context.Context) ([]*coreapi.ServiceProviderCluster, error) {
	panic("event must not scan SPCs")
}

func TestSeedingEventsUseTargetedCacheReads(t *testing.T) {
	cluster := newTestCluster("c1", "nightly", "4.21.0-0.nightly")
	spc := newTestServiceProviderCluster("c1", v("4.19.1"), nil, nil)
	queue := &seedQueue{}
	syncer := &rolloutSeedingSyncer{
		clusterLister:                getOnlyClusterLister{&corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}}},
		serviceProviderClusterLister: getOnlySPCLister{&corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{spc}}},
		queue:                        queue,
	}
	handler := syncer.eventHandler()
	for _, obj := range []any{cluster, spc} {
		handler.OnAdd(obj, false)
		require.Empty(t, cmp.Diff(sets.New(
			rolloutSeedKey{Version: coreapi.VersionProfile{ChannelGroup: "nightly", ID: "4.21"}},
			rolloutSeedKey{Version: coreapi.VersionProfile{ChannelGroup: "nightly", ID: "4.19"}},
		), sets.New(queue.keys...)), "either event enqueues every normalized reference")
		queue.keys = nil
	}
	updatedCluster, updatedSPC := cluster.DeepCopy(), spc.DeepCopy()
	updatedCluster.Status.Conditions = []metav1.Condition{{Type: "Unrelated", Status: metav1.ConditionTrue}}
	updatedSPC.Spec.PinnedVersion.UntilExactVersion = v("4.18.1")
	updatedSPC.Spec.ControlPlaneVersion.DesiredVersionLastTransitionTime = &metav1.Time{Time: statusTestNow}
	handler.OnUpdate(cluster, updatedCluster)
	handler.OnUpdate(spc, updatedSPC)
	require.Empty(t, queue.keys, "unrelated status and pin threshold updates must be ignored")
	updatedSPC.Spec.PinnedVersion.ExactVersion = v("4.17.1")
	handler.OnUpdate(spc, updatedSPC)
	require.Contains(t, queue.keys, rolloutSeedKey{Version: coreapi.VersionProfile{ChannelGroup: "nightly", ID: "4.17"}})
	queue.keys = nil
	updatedCluster.CustomerProperties.Version.ChannelGroup = "fast"
	handler.OnUpdate(cluster, updatedCluster)
	require.Contains(t, queue.keys, rolloutSeedKey{Version: coreapi.VersionProfile{ChannelGroup: "fast", ID: "4.19"}}, "group change must also repair SPC references")
}

func TestSeederMigratesLiveStateAndDoesNotRecreateRetired(t *testing.T) {
	rollout := newTestRollout("stable-4.19", v("4.19.6"), fleetapi.ControlPlaneVersionRolloutStatus{ClusterCountByDesiredExactVersion: map[string]int64{"4.19.6": 9}})
	fleet, lister := newTestRolloutStore(t, rollout)
	// Simulate a persisted document from before spec.version existed, retaining
	// the real store's ETag so Replace exercises optimistic concurrency.
	for id, data := range fleet.GetAllDocuments() {
		var document map[string]any
		require.NoError(t, json.Unmarshal(data, &document))
		// GenericDocument stores the API object under properties.
		properties := document["properties"].(map[string]any)
		delete(properties["spec"].(map[string]any), "version")
		data, err := json.Marshal(document)
		require.NoError(t, err)
		fleet.StoreDocument(id, data)
	}
	queue := &seedQueue{}
	syncer := &rolloutSeedingSyncer{
		clusterLister: &corelistertesting.SliceClusterLister{}, serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{},
		rolloutLister: lister, fleetDBClient: fleet, queue: queue,
	}
	require.NoError(t, syncer.repairReferences(t.Context()))
	key := rolloutSeedKey{Version: rollout.Spec.Version}
	require.Empty(t, cmp.Diff([]rolloutSeedKey{key}, queue.keys), "unused legacy documents below the floor must migrate")
	require.NoError(t, syncer.SyncOnce(t.Context(), key))
	got, err := fleet.ControlPlaneVersionRollouts().Get(t.Context(), "stable-4.19")
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(rollout.Spec, got.Spec))
	require.Empty(t, cmp.Diff(rollout.Status, got.Status))
	require.EqualValues(t, 2, got.GetInstanceVersion(), "backfill must replace using the live ETag")
	require.NoError(t, syncer.SyncOnce(t.Context(), key))
	again, err := fleet.ControlPlaneVersionRollouts().Get(t.Context(), "stable-4.19")
	require.NoError(t, err)
	require.Equal(t, got.GetEtag(), again.GetEtag(), "valid state must not be rewritten")
	require.NoError(t, fleet.ControlPlaneVersionRollouts().Delete(t.Context(), "stable-4.19"))
	require.NoError(t, syncer.SyncOnce(t.Context(), key))
	_, err = fleet.ControlPlaneVersionRollouts().Get(t.Context(), "stable-4.19")
	require.True(t, cosmosstorageutils.IsNotFoundError(err), "a queued migration must not resurrect a retired rollout")
	syncer.clusterLister = &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{newTestCluster("c1", "stable", "4.19")}}
	require.Error(t, syncer.SyncOnce(t.Context(), key), "creation retries while the soft-delete tombstone remains")
	for id := range fleet.GetAllDocuments() {
		fleet.DeleteDocument(id)
	}
	require.NoError(t, syncer.SyncOnce(t.Context(), key))
	got, err = fleet.ControlPlaneVersionRollouts().Get(t.Context(), "stable-4.19")
	require.NoError(t, err, "a live cached reference authorizes below-floor creation")
	require.Empty(t, cmp.Diff(key.Version, got.Spec.Version))
}

func TestReferenceRepairPreservesLegacyHealthUntilObservedSuccess(t *testing.T) {
	cluster := newTestCluster("c1", "stable", "4.19")
	cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: statusTestNow}
	key := controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: "c1"}
	legacy := key.InitialController(RolloutSeedingControllerName)
	other := key.InitialController("OtherController")
	resources, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(t.Context(), []any{cluster, legacy, other})
	require.NoError(t, err)
	fleet, lister := newTestRolloutStore(t)
	queue := &seedQueue{}
	syncer := &rolloutSeedingSyncer{
		clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}},
		serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{},
		rolloutLister:                lister, fleetDBClient: fleet, resourcesDBClient: resources, queue: queue,
	}
	crud := resources.HCPClusters(testSubscriptionID, testResourceGroupName).Controllers("c1")
	require.Error(t, syncer.repairReferences(t.Context()), "missing rollout prevents cleanup")
	_, err = crud.Get(t.Context(), RolloutSeedingControllerName)
	require.NoError(t, err)
	require.Len(t, queue.keys, 1)
	require.NoError(t, syncer.SyncOnce(t.Context(), queue.keys[0]))
	syncer.serviceProviderClusterLister = failingSPCList{}
	require.Error(t, syncer.repairReferences(t.Context()), "failed inventory prevents cleanup")
	_, err = crud.Get(t.Context(), RolloutSeedingControllerName)
	require.NoError(t, err)
	syncer.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{}
	require.NoError(t, syncer.repairReferences(t.Context()))
	_, err = crud.Get(t.Context(), RolloutSeedingControllerName)
	require.True(t, cosmosstorageutils.IsNotFoundError(err))
	_, err = crud.Get(t.Context(), "OtherController")
	require.NoError(t, err)
	require.NoError(t, syncer.repairReferences(t.Context()), "cleanup is idempotent")
}

func TestRepairPartialReferencesAndLegacyInventory(t *testing.T) {
	legacy := newTestRollout("fast-4.18", nil, fleetapi.ControlPlaneVersionRolloutStatus{})
	legacy.Spec.Version = coreapi.VersionProfile{}
	queue := &seedQueue{}
	syncer := &rolloutSeedingSyncer{
		clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{newTestCluster("c1", "stable", "4.19")}},
		serviceProviderClusterLister: failingSPCList{},
		rolloutLister:                &fleetlistertesting.SliceControlPlaneVersionRolloutLister{ControlPlaneVersionRollouts: []*fleetapi.ControlPlaneVersionRollout{legacy}},
		queue:                        queue,
	}
	require.Error(t, syncer.repairReferences(t.Context()))
	require.Empty(t, cmp.Diff(sets.New(
		rolloutSeedKey{Version: coreapi.VersionProfile{ChannelGroup: "stable", ID: "4.19"}},
		rolloutSeedKey{Version: coreapi.VersionProfile{ChannelGroup: "fast", ID: "4.18"}},
	), sets.New(queue.keys...)), "partial references and legacy inventory survive a failed SPC scan")
}

type fakeGraphData struct {
	profiles []coreapi.VersionProfile
	err      error
}

func (f fakeGraphData) VersionProfiles(context.Context) ([]coreapi.VersionProfile, error) {
	return f.profiles, f.err
}

func TestDiscoveryEnqueuesProfilesOrReportsFailure(t *testing.T) {
	queue := &seedQueue{}
	profile := coreapi.VersionProfile{ChannelGroup: "fast", ID: "4.21"}
	syncer := &rolloutSeedingSyncer{discovery: fakeGraphData{profiles: []coreapi.VersionProfile{profile}, err: errors.New("HTTP unavailable")}, queue: queue}
	require.ErrorContains(t, syncer.discover(t.Context()), "HTTP unavailable")
	require.Empty(t, queue.keys)
	syncer.discovery = fakeGraphData{profiles: []coreapi.VersionProfile{profile}}
	require.NoError(t, syncer.discover(t.Context()))
	require.Empty(t, cmp.Diff([]rolloutSeedKey{{Version: profile}}, queue.keys))
}

// FakeClock's ticker Stop is a no-op, so record calls independently of Waiters.
type seederClock struct {
	*clocktesting.FakeClock
	stopped atomic.Int32
}

func (c *seederClock) NewTicker(d time.Duration) clock.Ticker {
	return &seederTicker{Ticker: c.FakeClock.NewTicker(d), stopped: &c.stopped}
}

type seederTicker struct {
	clock.Ticker
	stopped *atomic.Int32
}

func (t *seederTicker) Stop() {
	t.Ticker.Stop()
	t.stopped.Add(1)
}

func TestSeederSourcesAndWorkerRetriesAreIndependent(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	clock := &seederClock{FakeClock: clocktesting.NewFakeClock(statusTestNow)}
	var discoveryCalls, repairCalls, workerCalls atomic.Int32
	controller := newPeriodicRolloutController(RolloutSeedingControllerName, func(context.Context, rolloutSeedKey) error {
		if workerCalls.Add(1) == 1 {
			return errors.New("retry worker")
		}
		return nil
	})
	controller.clock = clock
	controller.producers = []func(context.Context) error{
		func(context.Context) error { discoveryCalls.Add(1); return errors.New("discovery failed") },
		func(context.Context) error {
			repairCalls.Add(1)
			controller.Enqueue(rolloutSeedKey{Version: coreapi.VersionProfile{ChannelGroup: "stable", ID: "4.21"}})
			return nil
		},
	}
	require.Zero(t, discoveryCalls.Load(), "construction must not start producers")
	var synced atomic.Bool
	controller.AddCacheSyncs(synced.Load)
	done := make(chan struct{})
	go func() { controller.Run(ctx, 1); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
			require.EqualValues(t, 2, clock.stopped.Load(), "shutdown must stop every producer ticker")
		case <-time.After(5 * time.Second):
			t.Error("controller did not stop")
		}
	})
	require.Zero(t, discoveryCalls.Load(), "producers must wait for caches")
	synced.Store(true)
	require.Eventually(t, func() bool {
		return discoveryCalls.Load() == 1 && repairCalls.Load() == 1 && workerCalls.Load() == 2 && clock.Waiters() == 2
	}, 5*time.Second, time.Millisecond)
	for calls := int32(2); calls <= 4; calls++ {
		clock.Step(4 * time.Minute)
		require.Equal(t, calls-1, discoveryCalls.Load(), "producer errors must not enter queue retries")
		clock.Step(time.Minute)
		require.Equal(t, 2, clock.Waiters(), "ticks must retain the existing producer waiters")
		require.Eventually(t, func() bool {
			return discoveryCalls.Load() == calls && repairCalls.Load() == calls && workerCalls.Load() == calls+1
		}, 5*time.Second, time.Millisecond)
	}
}

func TestSeederCacheCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	clock := clocktesting.NewFakeClock(statusTestNow)
	var calls atomic.Int32
	controller := newPeriodicRolloutController(RolloutSeedingControllerName, func(context.Context, rolloutSeedKey) error { calls.Add(1); return nil }, func(context.Context) error { calls.Add(1); return nil })
	controller.clock = clock
	controller.AddCacheSyncs(func() bool { return false })
	done := make(chan struct{})
	go func() { controller.Run(ctx, 1); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cache cancellation did not stop controller")
	}
	require.Zero(t, calls.Load(), "unsynced caches must prevent all producers and workers")
	require.Zero(t, clock.Waiters(), "cache cancellation must not leave a producer ticker")
}

func TestSeederSlowProducerTickerAndShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	clock := &seederClock{FakeClock: clocktesting.NewFakeClock(statusTestNow)}
	started := make(chan time.Time, 3)
	release := make(chan struct{})
	stopping := make(chan struct{}, 3)
	shutdown := make(chan struct{})
	var active atomic.Int32
	var overlapping atomic.Bool
	controller := newPeriodicRolloutController(RolloutSeedingControllerName, func(context.Context, rolloutSeedKey) error { return nil }, func(ctx context.Context) error {
		if active.Add(1) != 1 {
			overlapping.Store(true)
		}
		defer active.Add(-1)
		started <- clock.Now()
		select {
		case <-release:
		case <-ctx.Done():
			stopping <- struct{}{}
			<-shutdown
		}
		return nil
	})
	controller.clock = clock
	done := make(chan struct{})
	go func() { controller.Run(ctx, 1); close(done) }()
	t.Cleanup(func() {
		cancel()
		close(shutdown)
		select {
		case <-done:
			require.Zero(t, active.Load(), "shutdown must wait for the producer")
			require.False(t, overlapping.Load(), "slow producer calls must never overlap")
			require.EqualValues(t, 1, clock.stopped.Load(), "shutdown must stop the producer ticker")
		case <-time.After(5 * time.Second):
			t.Error("controller did not stop")
		}
	})
	for _, elapsed := range []time.Duration{0, 12 * time.Minute, 15 * time.Minute} {
		select {
		case got := <-started:
			require.Empty(t, cmp.Diff(statusTestNow.Add(elapsed), got), "producer must follow the ticker cadence")
		case <-time.After(5 * time.Second):
			t.Fatal("producer did not start")
		}
		require.Equal(t, 1, clock.Waiters(), "one ticker must remain registered even while the producer is blocked")
		switch elapsed {
		case 0:
			clock.Step(12 * time.Minute)
		case 12 * time.Minute:
			clock.Step(2 * time.Minute)
		default:
			continue
		}
		select {
		case release <- struct{}{}:
		case <-time.After(5 * time.Second):
			t.Fatal("producer did not accept release")
		}
		if elapsed == 12*time.Minute {
			clock.Step(time.Minute)
		}
	}
	cancel()
	select {
	case <-stopping:
	case <-time.After(5 * time.Second):
		t.Fatal("producer did not observe cancellation")
	}
	select {
	case <-done:
		t.Fatal("controller returned before the producer finished shutting down")
	default:
	}
}

func TestBlockedDiscoveryDoesNotBlockRepairOrEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	processed := make(chan rolloutSeedKey, 2)
	controller := newPeriodicRolloutController(RolloutSeedingControllerName, func(_ context.Context, key rolloutSeedKey) error {
		processed <- key
		return nil
	})
	repair := rolloutSeedKey{Version: coreapi.VersionProfile{ChannelGroup: "stable", ID: "4.21"}}
	event := rolloutSeedKey{Version: coreapi.VersionProfile{ChannelGroup: "fast", ID: "4.22"}}
	controller.producers = []func(context.Context) error{
		func(ctx context.Context) error { close(started); <-ctx.Done(); return nil },
		func(context.Context) error { controller.Enqueue(repair); return nil },
	}
	done := make(chan struct{})
	go func() { controller.Run(ctx, 1); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("controller did not stop")
		}
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("discovery did not start")
	}
	controller.Enqueue(event)
	got := sets.New[rolloutSeedKey]()
	for range 2 {
		select {
		case key := <-processed:
			got.Insert(key)
		case <-time.After(5 * time.Second):
			t.Fatal("discovery blocked reference repair or event work")
		}
	}
	require.Empty(t, cmp.Diff(sets.New(repair, event), got))
}
