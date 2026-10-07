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
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
)

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
	missingProfile := existing.DeepCopy()
	missingProfile.Spec.Version = coreapi.VersionProfile{}
	for _, tc := range []struct {
		name           string
		profile        coreapi.VersionProfile
		existing, want *fleetapi.ControlPlaneVersionRollout
	}{
		{"create", profile, nil, newTestRollout("stable-4.21", nil, fleetapi.ControlPlaneVersionRolloutStatus{})},
		{"unchanged", profile, existing, nil},
		{"missing profile unchanged", profile, missingProfile, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.existing.DeepCopy()
			got, err := reconcileSeeding(tc.profile, tc.existing)
			require.NoError(t, err)
			require.Empty(t, cmp.Diff(tc.want, got, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})))
			require.Empty(t, cmp.Diff(before, tc.existing, cmp.AllowUnexported(azcorearm.ResourceID{}, azcorearm.ResourceType{})), "cached state must not change")
		})
	}
}

func TestDiscoveryProfiles(t *testing.T) {
	for _, tc := range []struct {
		name      string
		profiles  []coreapi.VersionProfile
		want      []controllerutils.ControlPlaneVersionRolloutKey
		wantError bool
	}{
		{"empty", nil, nil, false},
		{"sort and deduplicate normalized profiles", []coreapi.VersionProfile{{ChannelGroup: "stable", ID: "4.21.1"}, {ChannelGroup: "fast", ID: "4.20"}, {ChannelGroup: "stable", ID: "4.21"}}, []controllerutils.ControlPlaneVersionRolloutKey{{YStreamChannel: "fast-4.20"}, {YStreamChannel: "stable-4.21"}}, false},
		{"floor and normalization", []coreapi.VersionProfile{{ChannelGroup: "stable", ID: "4.19"}, {ChannelGroup: "fast", ID: "4.20.2"}, {ChannelGroup: "nightly", ID: "4.21.0-0.nightly"}}, []controllerutils.ControlPlaneVersionRolloutKey{{YStreamChannel: "fast-4.20"}, {YStreamChannel: "nightly-4.21"}}, false},
		{"invalid rejects partial results", []coreapi.VersionProfile{{ChannelGroup: "stable", ID: "4.21"}, {ChannelGroup: "fast", ID: "invalid"}}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keys, err := discoverChannels(tc.profiles)
			require.Equal(t, tc.wantError, err != nil)
			require.Empty(t, cmp.Diff(tc.want, keys))
		})
	}
}

func TestSeederPreservesLiveStateAndDoesNotRecreateRetired(t *testing.T) {
	rollout := newTestRollout("stable-4.19", v("4.19.6"), fleetapi.ControlPlaneVersionRolloutStatus{ClusterCountByDesiredExactVersion: map[string]int64{"4.19.6": 9}})
	fleet, lister := newTestRolloutStore(t, rollout)
	syncer := &rolloutSeedingSyncer{
		clusterLister: &corelistertesting.SliceClusterLister{}, serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{},
		controllerLister: &corelistertesting.SliceControllerLister{},
		rolloutLister:    lister, fleetDBClient: fleet,
	}
	key := controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.19"}
	require.NoError(t, syncer.SyncOnce(t.Context(), key))
	got, err := fleet.ControlPlaneVersionRollouts().Get(t.Context(), "stable-4.19")
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(rollout.Spec, got.Spec))
	require.Empty(t, cmp.Diff(rollout.Status, got.Status))
	require.EqualValues(t, 1, got.GetInstanceVersion(), "existing state must not be rewritten")
	require.NoError(t, syncer.SyncOnce(t.Context(), key))
	again, err := fleet.ControlPlaneVersionRollouts().Get(t.Context(), "stable-4.19")
	require.NoError(t, err)
	require.Equal(t, got.GetEtag(), again.GetEtag(), "valid state must not be rewritten")
	require.NoError(t, fleet.ControlPlaneVersionRollouts().Delete(t.Context(), "stable-4.19"))
	require.NoError(t, syncer.SyncOnce(t.Context(), key))
	_, err = fleet.ControlPlaneVersionRollouts().Get(t.Context(), "stable-4.19")
	require.True(t, cosmosstorageutils.IsNotFoundError(err), "a stale seeding key must not resurrect a retired rollout")
	syncer.clusterLister = &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{newTestCluster("c1", "stable", "4.19")}}
	require.Error(t, syncer.SyncOnce(t.Context(), key), "creation retries while the soft-delete tombstone remains")
	for id := range fleet.GetAllDocuments() {
		fleet.DeleteDocument(id)
	}
	syncer.rolloutLister = &fleetlistertesting.SliceControlPlaneVersionRolloutLister{}
	require.NoError(t, syncer.SyncOnce(t.Context(), key), "absent legacy status requires no cleanup")
	got, err = fleet.ControlPlaneVersionRollouts().Get(t.Context(), "stable-4.19")
	require.NoError(t, err, "a live cached reference authorizes below-floor creation")
	require.Empty(t, cmp.Diff(rollout.Spec.Version, got.Spec.Version))
}

func TestReferenceRepairPreservesLegacyHealthUntilObservedSuccess(t *testing.T) {
	cluster := newTestCluster("c1", "stable", "4.19")
	cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: statusTestNow}
	key := controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: "c1"}
	legacy := key.InitialController(RolloutSeedingControllerName)
	other := key.InitialController("OtherController")
	resources, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(t.Context(), []any{cluster, legacy, other})
	require.NoError(t, err)
	fleet, _ := newTestRolloutStore(t)
	controllers := &corelistertesting.SliceControllerLister{Controllers: []*coreapi.Controller{legacy, other}}
	syncer := &rolloutSeedingSyncer{
		clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}},
		serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{},
		controllerLister:             controllers,
		rolloutLister:                &fleetlistertesting.SliceControlPlaneVersionRolloutLister{}, fleetDBClient: fleet, resourcesDBClient: resources,
	}
	crud := resources.HCPClusters(testSubscriptionID, testResourceGroupName).Controllers("c1")
	rolloutKey := controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.19"}
	require.Error(t, syncer.SyncOnce(t.Context(), rolloutKey), "unobserved rollout prevents cleanup")
	_, err = crud.Get(t.Context(), RolloutSeedingControllerName)
	require.NoError(t, err)
	syncer.serviceProviderClusterLister = failingSPCList{}
	require.Error(t, syncer.SyncOnce(t.Context(), rolloutKey), "failed inventory prevents cleanup")
	_, err = crud.Get(t.Context(), RolloutSeedingControllerName)
	require.NoError(t, err)
	syncer.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{}
	observed := newTestRollout("stable-4.19", nil, fleetapi.ControlPlaneVersionRolloutStatus{})
	observed.Spec.Version = coreapi.VersionProfile{}
	syncer.rolloutLister = &fleetlistertesting.SliceControlPlaneVersionRolloutLister{ControlPlaneVersionRollouts: []*fleetapi.ControlPlaneVersionRollout{observed}}
	require.NoError(t, syncer.SyncOnce(t.Context(), rolloutKey))
	_, err = crud.Get(t.Context(), RolloutSeedingControllerName)
	require.True(t, cosmosstorageutils.IsNotFoundError(err))
	_, err = crud.Get(t.Context(), "OtherController")
	require.NoError(t, err)
	require.NoError(t, syncer.SyncOnce(t.Context(), rolloutKey), "cleanup is idempotent")
	controllers.Controllers = []*coreapi.Controller{other}
	syncer.resourcesDBClient = nil
	require.NoError(t, syncer.SyncOnce(t.Context(), rolloutKey), "observed status deletion must stop Resources client calls")
}

func TestSeederSkipsAbsentLegacyStatus(t *testing.T) {
	cluster := newTestCluster("c1", "stable", "4.21")
	key := controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: "c1"}
	for _, controllers := range [][]*coreapi.Controller{nil, {key.InitialController("OtherController")}} {
		fleet, lister := newTestRolloutStore(t, newTestRollout("stable-4.21", nil, fleetapi.ControlPlaneVersionRolloutStatus{}))
		syncer := &rolloutSeedingSyncer{
			clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}},
			serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{},
			controllerLister:             &corelistertesting.SliceControllerLister{Controllers: controllers},
			fleetDBClient:                fleet,
			rolloutLister:                lister,
			// Any Resources client call would panic; absent status must stay cache-only.
			resourcesDBClient: nil,
		}
		require.NoError(t, syncer.SyncOnce(t.Context(), controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.21"}))
	}
}

func TestSeedingPartialReferences(t *testing.T) {
	fleet, lister := newTestRolloutStore(t)
	syncer := &rolloutSeedingSyncer{
		clusterLister:                &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{newTestCluster("c1", "stable", "4.19")}},
		serviceProviderClusterLister: failingSPCList{},
		fleetDBClient:                fleet, rolloutLister: lister,
	}
	require.ErrorContains(t, syncer.SyncOnce(t.Context(), controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.19"}), "SPC scan failed")
	_, err := fleet.ControlPlaneVersionRollouts().Get(t.Context(), "stable-4.19")
	require.NoError(t, err, "known current references permit additive repair despite incomplete inventory")
}

func TestSeedingQueuedReferencesAreNotAuthorization(t *testing.T) {
	cluster := newTestCluster("c1", "stable", "4.21")
	spc := newTestServiceProviderCluster("c1", v("4.19.1"), nil, nil)
	clusters := &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}}
	spcs := &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{spc}}
	queue := &candidateQueue{}
	require.NoError(t, enqueueRolloutReferences(t.Context(), spc, clusters, spcs, queue))
	require.Contains(t, queue.channels, "stable-4.19")
	fleet, lister := newTestRolloutStore(t)
	syncer := &rolloutSeedingSyncer{clusterLister: clusters, serviceProviderClusterLister: spcs, controllerLister: &corelistertesting.SliceControllerLister{}, fleetDBClient: fleet, rolloutLister: lister}
	spcs.ServiceProviderClusters = nil
	key := controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.19"}
	require.NoError(t, syncer.SyncOnce(t.Context(), key))
	_, err := fleet.ControlPlaneVersionRollouts().Get(t.Context(), key.YStreamChannel)
	require.True(t, cosmosstorageutils.IsNotFoundError(err), "removed references cannot authorize stale queued work")
	cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: statusTestNow}
	spcs.ServiceProviderClusters = []*coreapi.ServiceProviderCluster{spc}
	require.NoError(t, syncer.SyncOnce(t.Context(), key), "absent legacy status requires no cleanup")
	_, err = fleet.ControlPlaneVersionRollouts().Get(t.Context(), key.YStreamChannel)
	require.NoError(t, err, "references from deleting clusters still authorize creation")
}

func TestSeederSupportedChannelWithoutReferences(t *testing.T) {
	fleet, lister := newTestRolloutStore(t)
	syncer := &rolloutSeedingSyncer{
		clusterLister: &corelistertesting.SliceClusterLister{}, serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{},
		fleetDBClient: fleet, rolloutLister: lister,
	}
	key := controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "fast-4.21"}
	require.NoError(t, syncer.SyncOnce(t.Context(), key))
	got, err := fleet.ControlPlaneVersionRollouts().Get(t.Context(), key.YStreamChannel)
	require.NoError(t, err)
	require.Equal(t, coreapi.VersionProfile{ChannelGroup: "fast", ID: "4.21"}, got.Spec.Version)
	require.Error(t, syncer.SyncOnce(t.Context(), controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "fast-4.21.1"}), "noncanonical channels are not seeded")
}

type fakeGraphData struct {
	profiles []coreapi.VersionProfile
	err      error
}

func (f fakeGraphData) VersionProfiles(context.Context) ([]coreapi.VersionProfile, error) {
	return f.profiles, f.err
}

func TestDiscoveryEnqueuesProfilesOrReportsFailure(t *testing.T) {
	queue := &candidateQueue{}
	profile := coreapi.VersionProfile{ChannelGroup: "fast", ID: "4.21"}
	syncer := &rolloutSeedingSyncer{discovery: fakeGraphData{profiles: []coreapi.VersionProfile{profile}, err: errors.New("HTTP unavailable")}, queue: queue}
	require.ErrorContains(t, syncer.discover(t.Context()), "HTTP unavailable")
	require.Empty(t, queue.channels)
	syncer.discovery = fakeGraphData{profiles: []coreapi.VersionProfile{profile}}
	require.NoError(t, syncer.discover(t.Context()))
	require.Equal(t, []string{"fast-4.21"}, queue.channels)
}

type seedingTestSyncer func(context.Context, controllerutils.ControlPlaneVersionRolloutKey) error

func (f seedingTestSyncer) SyncOnce(ctx context.Context, key controllerutils.ControlPlaneVersionRolloutKey) error {
	return f(ctx, key)
}

func (seedingTestSyncer) CooldownChecker() controllerutil.CooldownChecker { return nil }

func newTestSeedingController(syncer seedingTestSyncer, discover func(context.Context) error) *rolloutSeedingController {
	return &rolloutSeedingController{
		GenericWatchingController: controllerutils.NewControlPlaneVersionRolloutWatchingController(RolloutSeedingControllerName, nil, 5*time.Minute, syncer),
		discover:                  discover,
		clock:                     clock.RealClock{},
	}
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
	var discoveryCalls, workerCalls atomic.Int32
	controller := newTestSeedingController(func(context.Context, controllerutils.ControlPlaneVersionRolloutKey) error {
		if workerCalls.Add(1) == 1 {
			return errors.New("retry worker")
		}
		return nil
	}, func(context.Context) error { discoveryCalls.Add(1); return errors.New("discovery failed") })
	controller.clock = clock
	controller.Enqueue(controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "stable-4.21"})
	require.Zero(t, discoveryCalls.Load(), "construction must not start producers")
	var synced atomic.Bool
	controller.AddCacheSyncs(synced.Load)
	done := make(chan struct{})
	go func() { controller.Run(ctx, 1); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
			require.EqualValues(t, 1, clock.stopped.Load(), "shutdown must stop the discovery ticker")
		case <-time.After(5 * time.Second):
			t.Error("controller did not stop")
		}
	})
	require.Zero(t, discoveryCalls.Load(), "producers must wait for caches")
	synced.Store(true)
	require.Eventually(t, func() bool {
		return discoveryCalls.Load() == 1 && workerCalls.Load() == 2 && clock.Waiters() == 1
	}, 5*time.Second, time.Millisecond)
	for calls := int32(2); calls <= 4; calls++ {
		clock.Step(4 * time.Minute)
		require.Equal(t, calls-1, discoveryCalls.Load(), "producer errors must not enter queue retries")
		clock.Step(time.Minute)
		require.Equal(t, 1, clock.Waiters(), "ticks must retain the discovery waiter")
		require.Eventually(t, func() bool {
			return discoveryCalls.Load() == calls && workerCalls.Load() == 2
		}, 5*time.Second, time.Millisecond)
	}
}

func TestSeederCacheCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	clock := clocktesting.NewFakeClock(statusTestNow)
	var calls atomic.Int32
	controller := newTestSeedingController(func(context.Context, controllerutils.ControlPlaneVersionRolloutKey) error { calls.Add(1); return nil }, func(context.Context) error { calls.Add(1); return nil })
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
	controller := newTestSeedingController(func(context.Context, controllerutils.ControlPlaneVersionRolloutKey) error { return nil }, func(ctx context.Context) error {
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

func TestBlockedDiscoveryDoesNotBlockEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	processed := make(chan controllerutils.ControlPlaneVersionRolloutKey, 2)
	controller := newTestSeedingController(func(_ context.Context, key controllerutils.ControlPlaneVersionRolloutKey) error {
		processed <- key
		return nil
	}, func(ctx context.Context) error { close(started); <-ctx.Done(); return nil })
	event := controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: "fast-4.22"}
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
	select {
	case key := <-processed:
		require.Equal(t, event, key)
	case <-time.After(5 * time.Second):
		t.Fatal("discovery blocked event work")
	}
}
