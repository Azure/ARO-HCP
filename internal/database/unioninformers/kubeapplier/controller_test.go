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

package kubeapplier_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database/informers"
	"github.com/Azure/ARO-HCP/internal/database/listertesting"
	unionkubeapplier "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

// --- fake informer --------------------------------------------------------

// fakeMCInformer is a minimal cache.SharedIndexInformer fake that supports
// installing/removing event handlers and exposes Emit{Add,Update,Delete}
// so the test can deliver synthetic events to the controller. Methods we
// don't need panic via the embedded interface.
type fakeMCInformer struct {
	cache.SharedIndexInformer
	mu       sync.Mutex
	handlers map[*fakeMCRegistration]cache.ResourceEventHandler
}

type fakeMCRegistration struct{ owner *fakeMCInformer }

func (r *fakeMCRegistration) HasSynced() bool { return true }

func newFakeMCInformer() *fakeMCInformer {
	return &fakeMCInformer{handlers: map[*fakeMCRegistration]cache.ResourceEventHandler{}}
}

func (f *fakeMCInformer) AddEventHandler(h cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error) {
	r := &fakeMCRegistration{owner: f}
	f.mu.Lock()
	f.handlers[r] = h
	f.mu.Unlock()
	return r, nil
}

func (f *fakeMCInformer) RemoveEventHandler(reg cache.ResourceEventHandlerRegistration) error {
	r, ok := reg.(*fakeMCRegistration)
	if !ok || r.owner != f {
		return nil
	}
	f.mu.Lock()
	delete(f.handlers, r)
	f.mu.Unlock()
	return nil
}

func (f *fakeMCInformer) HasSynced() bool { return true }

func (f *fakeMCInformer) snapshotHandlers() []cache.ResourceEventHandler {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]cache.ResourceEventHandler, 0, len(f.handlers))
	for _, h := range f.handlers {
		out = append(out, h)
	}
	return out
}

func (f *fakeMCInformer) handlerCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.handlers)
}

func (f *fakeMCInformer) emitAdd(obj any) {
	for _, h := range f.snapshotHandlers() {
		h.OnAdd(obj, false)
	}
}

func (f *fakeMCInformer) emitDelete(obj any) {
	for _, h := range f.snapshotHandlers() {
		h.OnDelete(obj)
	}
}

func (f *fakeMCInformer) emitUpdate(oldObj, newObj any) {
	for _, h := range f.snapshotHandlers() {
		h.OnUpdate(oldObj, newObj)
	}
}

// --- stub factory ---------------------------------------------------------

// stubFactory builds an informers.KubeApplierInformers per call by wiring
// up the per-MC mock DB client registered for the given resourceID. Calls
// for unregistered resourceIDs return nil and bump a counter so tests can
// assert "factory had nothing to give yet".
type stubFactory struct {
	relist time.Duration

	mu       sync.Mutex
	clients  map[string]*databasetesting.MockKubeApplierDBClient // keyed by lower(rid.String())
	started  []*stubFactoryRun
	missCntr atomic.Int32
}

type stubFactoryRun struct {
	rid *azcorearm.ResourceID
	inf informers.KubeApplierInformers
}

func newStubFactory(relist time.Duration) *stubFactory {
	return &stubFactory{relist: relist, clients: map[string]*databasetesting.MockKubeApplierDBClient{}}
}

// register associates a mock per-MC client with the given resourceID.
func (s *stubFactory) register(rid *azcorearm.ResourceID, mock *databasetesting.MockKubeApplierDBClient) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients[strings.ToLower(rid.String())] = mock
}

func (s *stubFactory) NewKubeApplierInformers(
	ctx context.Context, rid *azcorearm.ResourceID,
) informers.KubeApplierInformers {
	s.mu.Lock()
	mock, ok := s.clients[strings.ToLower(rid.String())]
	s.mu.Unlock()
	if !ok {
		s.missCntr.Add(1)
		return nil
	}
	inf := informers.NewKubeApplierInformersWithRelistDuration(ctx, mock.Listers(), mock, &s.relist)
	s.mu.Lock()
	s.started = append(s.started, &stubFactoryRun{rid: rid, inf: inf})
	s.mu.Unlock()
	return inf
}

func (s *stubFactory) misses() int { return int(s.missCntr.Load()) }

func (s *stubFactory) runs() []*stubFactoryRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*stubFactoryRun, len(s.started))
	copy(out, s.started)
	return out
}

// --- fixtures -------------------------------------------------------------

// stamp identifiers and canonical management-cluster resourceIDs. The
// SliceManagementClusterLister.Get uses fleet.ToManagementClusterResourceIDString,
// so MCs registered with the lister must carry the matching canonical form.
var (
	ctlMgmtAStamp = "1"
	ctlMgmtBStamp = "2"
	ctlMgmtAID    = api.Must(fleet.ToManagementClusterResourceID(ctlMgmtAStamp))
	ctlMgmtBID    = api.Must(fleet.ToManagementClusterResourceID(ctlMgmtBStamp))
)

const (
	ctlSub      = "00000000-0000-0000-0000-000000000001"
	ctlRG       = "rg"
	ctlCluster  = "c"
	ctlNodePool = "np"
)

func ctlNewApplyDesire(t *testing.T, idStr string, mgmt *azcorearm.ResourceID) *kubeapplier.ApplyDesire {
	t.Helper()
	id, err := azcorearm.ParseResourceID(idStr)
	if err != nil {
		t.Fatalf("parse %q: %v", idStr, err)
	}
	return &kubeapplier.ApplyDesire{
		CosmosMetadata: api.CosmosMetadata{ResourceID: id, PartitionKey: strings.ToLower(mgmt.String())},
		Spec: kubeapplier.ApplyDesireSpec{
			ManagementCluster: mgmt,
			Type:              kubeapplier.ApplyDesireTypeServerSideApply,
			ServerSideApply:   &kubeapplier.ServerSideApplyConfig{KubeContent: &runtime.RawExtension{Raw: []byte(`{"apiVersion":"v1","kind":"ConfigMap"}`)}},
		},
	}
}

func ctlMC(rid *azcorearm.ResourceID) *fleet.ManagementCluster {
	return &fleet.ManagementCluster{
		CosmosMetadata: api.CosmosMetadata{ResourceID: rid, PartitionKey: strings.ToLower(rid.Name)},
		ResourceID:     rid,
	}
}

// =============================================================================
// Tests
// =============================================================================

//  1. Build a controller with a fake MC informer and a stub factory that
//     returns mock-backed sub-informers for mgmt-a (preloaded with two
//     ApplyDesires).
//  2. Seed the lister with mgmt-a so SyncOnce.Get returns it.
//  3. Run the controller with one worker.
//  4. Emit an Add for mgmt-a from the MC informer.
//  5. Poll until the union ApplyDesireLister.ListForManagementCluster
//     returns both desires.
//  6. Cancel ctx and assert Run returns.
func TestController_AddRegistersSubInformer(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	relist := 100 * time.Millisecond

	mockA, err := databasetesting.NewMockKubeApplierDBClientWithResources(ctx, []any{
		ctlNewApplyDesire(t,
			kubeapplier.ToClusterScopedApplyDesireResourceIDString(ctlSub, ctlRG, ctlCluster, "a1"),
			ctlMgmtAID),
		ctlNewApplyDesire(t,
			kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(ctlSub, ctlRG, ctlCluster, ctlNodePool, "a2"),
			ctlMgmtAID),
	})
	if err != nil {
		t.Fatalf("NewMockKubeApplierDBClientWithResources: %v", err)
	}

	factory := newStubFactory(relist)
	factory.register(ctlMgmtAID, mockA)

	mcInformer := newFakeMCInformer()
	mcLister := &listertesting.SliceManagementClusterLister{
		ManagementClusters: []*fleet.ManagementCluster{ctlMC(ctlMgmtAID)},
	}
	ctl := unionkubeapplier.NewUnionKubeApplierInformersController(mcInformer, mcLister, factory)

	runDone := startController(t, ctx, ctl, mcInformer, 1)

	mcInformer.emitAdd(ctlMC(ctlMgmtAID))

	_, applyLister := ctl.Union().ApplyDesires()
	waitUntil(t, ctx, "lister sees both desires for mgmt-a", func() bool {
		got, err := applyLister.ListForManagementCluster(ctx, ctlMgmtAID)
		return err == nil && len(got) == 2
	})

	cancel()
	waitChan(t, runDone, "controller Run returned")
}

//  1. Build controller with two MCs registered with the factory; each MC
//     has a single distinguishable ApplyDesire. Seed the lister with both.
//  2. Emit Adds for both, wait for the union to see 3 desires total.
//  3. Drop mgmt-a from the lister and emit a Delete event for it.
//  4. Wait for the union to see only 1 desire (mgmt-b's).
//  5. Assert ListForManagementCluster(mgmt-a) returns nil.
//  6. Cancel ctx and assert Run returns.
func TestController_RemoveDropsSubInformer(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	relist := 100 * time.Millisecond
	factory := newStubFactory(relist)

	mockA, err := databasetesting.NewMockKubeApplierDBClientWithResources(ctx, []any{
		ctlNewApplyDesire(t,
			kubeapplier.ToClusterScopedApplyDesireResourceIDString(ctlSub, ctlRG, ctlCluster, "a1"),
			ctlMgmtAID),
		ctlNewApplyDesire(t,
			kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(ctlSub, ctlRG, ctlCluster, ctlNodePool, "a2"),
			ctlMgmtAID),
	})
	if err != nil {
		t.Fatalf("mock A: %v", err)
	}
	factory.register(ctlMgmtAID, mockA)

	mockB, err := databasetesting.NewMockKubeApplierDBClientWithResources(ctx, []any{
		ctlNewApplyDesire(t,
			kubeapplier.ToClusterScopedApplyDesireResourceIDString(ctlSub, ctlRG, "other-cluster", "b1"),
			ctlMgmtBID),
	})
	if err != nil {
		t.Fatalf("mock B: %v", err)
	}
	factory.register(ctlMgmtBID, mockB)

	mcInformer := newFakeMCInformer()
	mcLister := &mutableMCLister{}
	mcLister.set(ctlMC(ctlMgmtAID), ctlMC(ctlMgmtBID))
	ctl := unionkubeapplier.NewUnionKubeApplierInformersController(mcInformer, mcLister, factory)

	runDone := startController(t, ctx, ctl, mcInformer, 2)

	mcInformer.emitAdd(ctlMC(ctlMgmtAID))
	mcInformer.emitAdd(ctlMC(ctlMgmtBID))

	_, applyLister := ctl.Union().ApplyDesires()
	waitUntil(t, ctx, "lister sees both MCs (3 desires total)", func() bool {
		got, err := applyLister.List(ctx)
		return err == nil && len(got) == 3
	})

	// Simulate the MC informer's cache update: removing the MC from the
	// lister happens before DeleteFunc fires, so SyncOnce will see NotFound.
	mcLister.set(ctlMC(ctlMgmtBID))
	mcInformer.emitDelete(ctlMC(ctlMgmtAID))

	waitUntil(t, ctx, "lister sees only mgmt-b after delete", func() bool {
		got, err := applyLister.List(ctx)
		return err == nil && len(got) == 1
	})
	gotA, err := applyLister.ListForManagementCluster(ctx, ctlMgmtAID)
	if err != nil {
		t.Fatalf("ListForManagementCluster mgmt-a after Remove: %v", err)
	}
	if gotA != nil {
		t.Errorf("ListForManagementCluster mgmt-a after Remove: want nil, got %v", gotA)
	}

	cancel()
	waitChan(t, runDone, "controller Run returned")
}

//  1. Build controller with no factory entries registered for mgmt-a (so
//     factory returns nil even though the lister knows about mgmt-a).
//  2. Run the controller.
//  3. Emit an Add for mgmt-a; SyncOnce sees the MC, calls factory, gets
//     nil — silently skips. Assert no union registration happened and the
//     factory recorded a miss.
//  4. Register the factory entry and emit an Update for mgmt-a.
//  5. Wait for the union to pick up the sub.
//  6. Cancel and join.
func TestController_FactoryNilSkipsRegistrationAndRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	relist := 100 * time.Millisecond
	factory := newStubFactory(relist)

	mcInformer := newFakeMCInformer()
	mcLister := &mutableMCLister{}
	mcLister.set(ctlMC(ctlMgmtAID))
	ctl := unionkubeapplier.NewUnionKubeApplierInformersController(mcInformer, mcLister, factory)

	runDone := startController(t, ctx, ctl, mcInformer, 1)

	mcInformer.emitAdd(ctlMC(ctlMgmtAID))

	waitUntil(t, ctx, "factory miss recorded", func() bool {
		return factory.misses() >= 1
	})
	if !ctl.Union().HasSynced() {
		t.Errorf("empty union: HasSynced = false, want true (no sub registered)")
	}

	// Wire up the factory and emit Update so the controller retries.
	mockA, err := databasetesting.NewMockKubeApplierDBClientWithResources(ctx, []any{
		ctlNewApplyDesire(t,
			kubeapplier.ToClusterScopedApplyDesireResourceIDString(ctlSub, ctlRG, ctlCluster, "a1"),
			ctlMgmtAID),
	})
	if err != nil {
		t.Fatalf("mock A: %v", err)
	}
	factory.register(ctlMgmtAID, mockA)
	mcInformer.emitUpdate(ctlMC(ctlMgmtAID), ctlMC(ctlMgmtAID))

	_, applyLister := ctl.Union().ApplyDesires()
	waitUntil(t, ctx, "lister sees mgmt-a after retry", func() bool {
		got, err := applyLister.ListForManagementCluster(ctx, ctlMgmtAID)
		return err == nil && len(got) == 1
	})

	cancel()
	waitChan(t, runDone, "controller Run returned")
}

// 1. Build controller with two MCs in lister + factory.
// 2. Emit Adds for both; wait for both subs to be visible (factory.runs == 2).
// 3. Cancel ctx and join Run. After Run returns:
//   - the union should expose no listers' worth of data
//   - the per-MC sub-informer goroutines should have exited
//     (controllerSubEntry done channels closed — implicit because shutdown
//     waits on them).
func TestController_ContextCancelStopsAllSubs(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	relist := 100 * time.Millisecond
	factory := newStubFactory(relist)

	for _, rid := range []*azcorearm.ResourceID{ctlMgmtAID, ctlMgmtBID} {
		factory.register(rid, databasetesting.NewMockKubeApplierDBClient())
	}

	mcInformer := newFakeMCInformer()
	mcLister := &mutableMCLister{}
	mcLister.set(ctlMC(ctlMgmtAID), ctlMC(ctlMgmtBID))
	ctl := unionkubeapplier.NewUnionKubeApplierInformersController(mcInformer, mcLister, factory)

	runDone := startController(t, ctx, ctl, mcInformer, 2)

	mcInformer.emitAdd(ctlMC(ctlMgmtAID))
	mcInformer.emitAdd(ctlMC(ctlMgmtBID))

	waitUntil(t, ctx, "two subs started", func() bool {
		return len(factory.runs()) == 2
	})

	cancel()
	waitChan(t, runDone, "controller Run returned")

	_, applyLister := ctl.Union().ApplyDesires()
	got, err := applyLister.List(ctx)
	if err != nil {
		t.Fatalf("List after shutdown: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List after shutdown: len = %d, want 0", len(got))
	}
}

// 1. Build controller; seed lister + factory for mgmt-a.
// 2. Emit Add for mgmt-a; wait for sub to register.
// 3. Emit Add for mgmt-a several times (duplicate or replayed events).
// 4. After a brief settle, assert factory was invoked only once.
// 5. Cancel and join.
func TestController_DuplicateAddIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	relist := 100 * time.Millisecond
	factory := newStubFactory(relist)
	factory.register(ctlMgmtAID, databasetesting.NewMockKubeApplierDBClient())

	mcInformer := newFakeMCInformer()
	mcLister := &mutableMCLister{}
	mcLister.set(ctlMC(ctlMgmtAID))
	ctl := unionkubeapplier.NewUnionKubeApplierInformersController(mcInformer, mcLister, factory)

	runDone := startController(t, ctx, ctl, mcInformer, 1)

	mcInformer.emitAdd(ctlMC(ctlMgmtAID))
	waitUntil(t, ctx, "first Add registers sub", func() bool {
		return len(factory.runs()) == 1
	})

	for i := 0; i < 3; i++ {
		mcInformer.emitAdd(ctlMC(ctlMgmtAID))
	}
	// Give the controller a moment to (not) act on duplicates.
	time.Sleep(150 * time.Millisecond)
	if got := len(factory.runs()); got != 1 {
		t.Errorf("duplicate Add: factory runs = %d, want 1", got)
	}

	cancel()
	waitChan(t, runDone, "controller Run returned")
}

// SyncOnce can also be called directly without an informer event, which
// is the unit-of-work the worker uses. Verify it works against a known
// MC and against an unknown one (should remove if previously added,
// otherwise no-op).
func TestController_SyncOnceDirect(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	relist := 100 * time.Millisecond
	factory := newStubFactory(relist)
	factory.register(ctlMgmtAID, databasetesting.NewMockKubeApplierDBClient())

	mcInformer := newFakeMCInformer()
	mcLister := &mutableMCLister{}
	mcLister.set(ctlMC(ctlMgmtAID))
	ctl := unionkubeapplier.NewUnionKubeApplierInformersController(mcInformer, mcLister, factory)

	runDone := startController(t, ctx, ctl, mcInformer, 1)

	// Direct SyncOnce on a known MC adds it.
	if err := ctl.SyncOnce(ctx, unionkubeapplier.ManagementClusterKey{
		StampIdentifier:       ctlMgmtAStamp,
		ManagementClusterName: fleet.ManagementClusterResourceName,
	}); err != nil {
		t.Fatalf("SyncOnce known: %v", err)
	}
	_, applyLister := ctl.Union().ApplyDesires()
	gotA, err := applyLister.ListForManagementCluster(ctx, ctlMgmtAID)
	if err != nil {
		t.Fatalf("ListForManagementCluster: %v", err)
	}
	if gotA == nil {
		t.Errorf("expected mgmt-a to be registered after direct SyncOnce; got nil")
	}

	// Direct SyncOnce on an unknown MC is a no-op (lister List doesn't
	// contain the key; nothing was added, so ensureRemoved finds nothing
	// to delete).
	if err := ctl.SyncOnce(ctx, unionkubeapplier.ManagementClusterKey{
		StampIdentifier:       "99",
		ManagementClusterName: fleet.ManagementClusterResourceName,
	}); err != nil {
		t.Errorf("SyncOnce unknown: %v", err)
	}

	cancel()
	waitChan(t, runDone, "controller Run returned")
}

// --- mutable lister -------------------------------------------------------

// mutableMCLister is a small extension of listertesting.SliceManagementClusterLister
// whose contents can be mutated by tests during a run. We compose rather
// than redeclare to keep delegating List/Get/GetByCSProvisionShardID.
type mutableMCLister struct {
	mu    sync.Mutex
	inner listertesting.SliceManagementClusterLister
}

func (m *mutableMCLister) set(mcs ...*fleet.ManagementCluster) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inner.ManagementClusters = append([]*fleet.ManagementCluster(nil), mcs...)
}

func (m *mutableMCLister) List(ctx context.Context) ([]*fleet.ManagementCluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inner.List(ctx)
}

func (m *mutableMCLister) Get(ctx context.Context, stampIdentifier string) (*fleet.ManagementCluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inner.Get(ctx, stampIdentifier)
}

func (m *mutableMCLister) GetByCSProvisionShardID(ctx context.Context, shardID string) (*fleet.ManagementCluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inner.GetByCSProvisionShardID(ctx, shardID)
}

// --- test helpers ---------------------------------------------------------

// startController launches ctl.Run on a goroutine and waits for the
// controller to install its event handler on the fake informer, so tests
// don't race the handler installation when calling emitAdd/Delete/Update.
// Returns a channel that closes when Run returns.
func startController(t *testing.T, ctx context.Context, ctl *unionkubeapplier.UnionKubeApplierInformersController, mcInformer *fakeMCInformer, threadiness int) <-chan struct{} {
	t.Helper()
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		ctl.Run(ctx, threadiness)
	}()
	waitUntil(t, ctx, "controller installed event handler", func() bool {
		return mcInformer.handlerCount() == 1
	})
	return runDone
}

func waitUntil(t *testing.T, ctx context.Context, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitChan(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}
