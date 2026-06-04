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
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database/informers"
	unionkubeapplier "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

const (
	testSub      = "00000000-0000-0000-0000-000000000001"
	testRG       = "rg"
	testCluster  = "c"
	testNodePool = "np"
)

var (
	mgmtAID = api.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/mgmt-a"))
	mgmtBID = api.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/2/managementclusters/mgmt-b"))
	mgmtUnregistered = api.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/9/managementclusters/mgmt-z"))
)

func mustParseID(t *testing.T, s string) *azcorearm.ResourceID {
	t.Helper()
	id, err := azcorearm.ParseResourceID(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return id
}

// newFakeInformer returns a fake SharedIndexInformer that starts synced and
// gives each AddEventHandler call back a fakeRegistration whose HasSynced is
// settable. Only the methods UnionDesireInformer actually uses are wired up;
// the others panic if called. This keeps the primitive tests focused on the
// union's own logic without dragging in real Reflector/ListWatch behavior.
func newFakeInformer() *fakeSharedIndexInformer {
	return &fakeSharedIndexInformer{synced: true}
}

func waitForSync(t *testing.T, ctx context.Context, fns ...cache.InformerSynced) {
	t.Helper()
	if !cache.WaitForCacheSync(ctx.Done(), fns...) {
		t.Fatal("informers did not sync")
	}
}

// ============================================================================
// UnionDesireInformer (primitive)
// ============================================================================

// 1. create empty union
// 2. assert HasSynced == true (vacuously, no subs)
func TestUnionDesireInformer_EmptyHasSynced(t *testing.T) {
	u := unionkubeapplier.NewUnionDesireInformer()
	if !u.HasSynced() {
		t.Errorf("empty union: HasSynced = false, want true (vacuously)")
	}
}

// 1. create union and synced informer A
// 2. add A to the union
// 3. assert union HasSynced == true
// 4. create unsynced informer B
// 5. add B to the union
// 6. assert union HasSynced == false (one sub still unsynced)
// 7. flip B to synced
// 8. assert union HasSynced == true
func TestUnionDesireInformer_HasSyncedReflectsAllSubs(t *testing.T) {
	u := unionkubeapplier.NewUnionDesireInformer()
	infA := newFakeInformer()
	if err := u.Add(mgmtAID, infA); err != nil {
		t.Fatalf("Add A: %v", err)
	}
	if !u.HasSynced() {
		t.Errorf("after Add A (synced): HasSynced = false, want true")
	}

	// Adding a not-yet-synced informer flips union HasSynced false.
	infB := newFakeInformer()
	infB.synced = false
	if err := u.Add(mgmtBID, infB); err != nil {
		t.Fatalf("Add B: %v", err)
	}
	if u.HasSynced() {
		t.Errorf("after Add B (unsynced): HasSynced = true, want false")
	}

	infB.synced = true
	if !u.HasSynced() {
		t.Errorf("after B syncs: HasSynced = false, want true")
	}
}

// 1. create union and informer A; add A
// 2. register handler on union
// 3. assert handler installed on A
// 4. create informer B; add B
// 5. assert handler installed on B (propagated to later-Added sub)
// 6. assert reg.HasSynced == true (both sub regs synced)
// 7. flip A's per-handler reg sync off
// 8. assert reg.HasSynced == false (union reg ANDs over all subs)
// 9. flip A's per-handler reg back on; RemoveEventHandler(reg)
// 10. assert handler removed from A and B; reg.HasSynced == false (entry forgotten)
func TestUnionDesireInformer_EventHandlerPropagation(t *testing.T) {
	u := unionkubeapplier.NewUnionDesireInformer()

	// Pre-register sub A, then register a handler. The handler should be
	// installed on A immediately.
	infA := newFakeInformer()
	if err := u.Add(mgmtAID, infA); err != nil {
		t.Fatalf("Add A: %v", err)
	}

	var addCount int32
	handler := cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) { atomic.AddInt32(&addCount, 1) },
	}
	reg, err := u.AddEventHandler(handler)
	if err != nil {
		t.Fatalf("AddEventHandler: %v", err)
	}
	if got := infA.handlerCount(); got != 1 {
		t.Errorf("sub A handler count after AddEventHandler = %d, want 1", got)
	}

	// Add B after the handler was registered — the handler should be
	// installed on B by Add.
	infB := newFakeInformer()
	if err := u.Add(mgmtBID, infB); err != nil {
		t.Fatalf("Add B: %v", err)
	}
	if got := infB.handlerCount(); got != 1 {
		t.Errorf("sub B handler count after Add = %d, want 1", got)
	}

	// Registration HasSynced: both subs are synced, both have the handler
	// reg, so true.
	if !reg.HasSynced() {
		t.Errorf("reg.HasSynced = false, want true (both subs synced)")
	}

	// Flip one sub's per-handler reg sync off: union reg should follow.
	infA.setRegSync(false)
	if reg.HasSynced() {
		t.Errorf("reg.HasSynced = true, want false (sub A reg desynced)")
	}
	infA.setRegSync(true)

	// RemoveEventHandler should remove from all subs and forget the entry.
	if err := u.RemoveEventHandler(reg); err != nil {
		t.Fatalf("RemoveEventHandler: %v", err)
	}
	if got := infA.handlerCount(); got != 0 {
		t.Errorf("sub A handler count after Remove = %d, want 0", got)
	}
	if got := infB.handlerCount(); got != 0 {
		t.Errorf("sub B handler count after Remove = %d, want 0", got)
	}
	if reg.HasSynced() {
		t.Errorf("reg.HasSynced after Remove: want false (entry forgotten)")
	}
}

// 1. create union and informer A; add A
// 2. register handler on union
// 3. Remove A from the union
// 4. assert handler detached from A
// 5. create informer B; add B (handler entry is still tracked on the union)
// 6. assert handler installed on B
// 7. assert reg.HasSynced == true
func TestUnionDesireInformer_RemoveDetachesHandlers(t *testing.T) {
	u := unionkubeapplier.NewUnionDesireInformer()
	infA := newFakeInformer()
	if err := u.Add(mgmtAID, infA); err != nil {
		t.Fatalf("Add A: %v", err)
	}

	reg, err := u.AddEventHandler(cache.ResourceEventHandlerFuncs{})
	if err != nil {
		t.Fatalf("AddEventHandler: %v", err)
	}

	u.Remove(mgmtAID)
	if got := infA.handlerCount(); got != 0 {
		t.Errorf("sub A handler count after Remove = %d, want 0", got)
	}

	// The handler entry is still tracked on the union: a new Add picks it up.
	infB := newFakeInformer()
	if err := u.Add(mgmtBID, infB); err != nil {
		t.Fatalf("Add B: %v", err)
	}
	if got := infB.handlerCount(); got != 1 {
		t.Errorf("sub B handler count after Add (post-Remove) = %d, want 1", got)
	}
	if !reg.HasSynced() {
		t.Errorf("reg.HasSynced after re-Add: want true")
	}
}

// 1. create union and informer A1; add A1
// 2. register handler on union
// 3. create informer A2; add A2 under the same rid (replace A1)
// 4. assert handler detached from A1
// 5. assert handler attached to A2
// 6. assert reg.HasSynced == true
func TestUnionDesireInformer_AddReplaces(t *testing.T) {
	u := unionkubeapplier.NewUnionDesireInformer()
	infA1 := newFakeInformer()
	if err := u.Add(mgmtAID, infA1); err != nil {
		t.Fatalf("Add A1: %v", err)
	}

	reg, err := u.AddEventHandler(cache.ResourceEventHandlerFuncs{})
	if err != nil {
		t.Fatalf("AddEventHandler: %v", err)
	}

	// Replace A1 with A2. The handler should be detached from A1 and
	// reattached to A2.
	infA2 := newFakeInformer()
	if err := u.Add(mgmtAID, infA2); err != nil {
		t.Fatalf("Add A2 (replace): %v", err)
	}
	if got := infA1.handlerCount(); got != 0 {
		t.Errorf("sub A1 handler count after replace = %d, want 0", got)
	}
	if got := infA2.handlerCount(); got != 1 {
		t.Errorf("sub A2 handler count after replace = %d, want 1", got)
	}
	if !reg.HasSynced() {
		t.Errorf("reg.HasSynced after replace: want true")
	}
}

// 1. create union and register a handler (so Add tries to install on the new sub)
// 2. create an informer that errors on AddEventHandler
// 3. attempt to Add the erroring informer
// 4. assert returned error is the sentinel
// 5. assert union HasSynced == true (failed sub rolled back, not retained)
func TestUnionDesireInformer_AddPropagatesErrorAndRollsBack(t *testing.T) {
	u := unionkubeapplier.NewUnionDesireInformer()

	// Pre-register a handler so Add will try to install on the new sub.
	if _, err := u.AddEventHandler(cache.ResourceEventHandlerFuncs{}); err != nil {
		t.Fatalf("AddEventHandler: %v", err)
	}

	sentinel := errors.New("boom")
	bad := &erroringSharedIndexInformer{addErr: sentinel}
	if err := u.Add(mgmtAID, bad); !errors.Is(err, sentinel) {
		t.Errorf("Add with erroring sub: want sentinel, got %v", err)
	}
	// The failed sub should NOT be retained, so HasSynced is still vacuously true.
	if !u.HasSynced() {
		t.Errorf("after failed Add: HasSynced = false, want true (sub rolled back)")
	}
}

// 1. create union
// 2. Add(nil rid, informer) — assert no error, no-op
// 3. Add(rid, nil sub) — assert no error, no-op
// 4. Remove(nil) — assert no panic
// 5. Remove(unregistered rid) — assert no panic
func TestUnionDesireInformer_NilGuards(t *testing.T) {
	u := unionkubeapplier.NewUnionDesireInformer()
	if err := u.Add(nil, newFakeInformer()); err != nil {
		t.Errorf("Add(nil rid): want no-op nil, got %v", err)
	}
	if err := u.Add(mgmtAID, nil); err != nil {
		t.Errorf("Add(nil sub): want no-op nil, got %v", err)
	}
	u.Remove(nil)              // no-op
	u.Remove(mgmtUnregistered) // no-op
}

// 1. create union and two informers A, B
// 2. spawn goroutine 1: loop Add/Remove for A and B
// 3. spawn goroutine 2: loop HasSynced
// 4. main goroutine: loop HasSynced 1000 times
// 5. signal stop and wait for both goroutines
// 6. race detector flags any data race on the union's internal state
func TestUnionDesireInformer_ConcurrentAddRemoveVsRead(t *testing.T) {
	u := unionkubeapplier.NewUnionDesireInformer()
	infA := newFakeInformer()
	infB := newFakeInformer()

	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = u.Add(mgmtAID, infA)
				_ = u.Add(mgmtBID, infB)
				u.Remove(mgmtAID)
				u.Remove(mgmtBID)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = u.HasSynced()
			}
		}
	}()

	for i := 0; i < 1000; i++ {
		_ = u.HasSynced()
	}
	close(stop)
	wg.Wait()
}

// ============================================================================
// UnionKubeApplierInformers (aggregator) — end-to-end via real informers
// ============================================================================

func newApplyDesire(t *testing.T, idStr string, mgmt *azcorearm.ResourceID) *kubeapplier.ApplyDesire {
	t.Helper()
	return &kubeapplier.ApplyDesire{
		CosmosMetadata: api.CosmosMetadata{ResourceID: mustParseID(t, idStr), PartitionKey: strings.ToLower(mgmt.String())},
		Spec: kubeapplier.ApplyDesireSpec{
			ManagementCluster: mgmt,
			KubeContent:       &runtime.RawExtension{Raw: []byte(`{"apiVersion":"v1","kind":"ConfigMap"}`)},
		},
	}
}

// buildPerMCInformers constructs a started informers.KubeApplierInformers
// against a mock DB containing the supplied seed ApplyDesires for the given
// management cluster. The mock isolates each MC into its own container, so
// listing-by-MC and per-MC informer wiring both work correctly.
func buildPerMCInformers(t *testing.T, ctx context.Context, seed ...*kubeapplier.ApplyDesire) informers.KubeApplierInformers {
	t.Helper()
	resources := make([]any, 0, len(seed))
	for _, d := range seed {
		resources = append(resources, d)
	}
	mock, err := databasetesting.NewMockKubeApplierDBClientWithResources(ctx, resources)
	if err != nil {
		t.Fatalf("NewMockKubeApplierDBClientWithResources: %v", err)
	}
	relist := 250 * time.Millisecond
	info := informers.NewKubeApplierInformersWithRelistDuration(ctx, mock.Listers(), &relist)
	go info.RunWithContext(ctx)
	apply, _ := info.ApplyDesires()
	delete, _ := info.DeleteDesires()
	read, _ := info.ReadDesires()
	waitForSync(t, ctx, apply.HasSynced, delete.HasSynced, read.HasSynced)
	return info
}

// 1. build per-MC KubeApplierInformers subA seeded with 2 ApplyDesires (mgmtA)
// 2. build per-MC KubeApplierInformers subB seeded with 1 ApplyDesire (mgmtB)
// 3. create empty UnionKubeApplierInformers
// 4. Add subA under mgmtA and subB under mgmtB
// 5. assert union HasSynced == true
// 6. assert union ApplyDesireLister.List returns 3 (aggregated)
// 7. assert ListForManagementCluster(mgmtA) returns 2 (delegates to subA)
// 8. register event handler on the union ApplyDesire informer
// 9. wait for reg.HasSynced
// 10. Remove mgmtA from the union
// 11. assert union List returns 1 (only mgmtB's fixture remains)
// 12. assert ListForManagementCluster(mgmtA) returns nil
func TestUnionKubeApplierInformers_EndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	subA := buildPerMCInformers(t, ctx,
		newApplyDesire(t,
			kubeapplier.ToClusterScopedApplyDesireResourceIDString(testSub, testRG, testCluster, "a1"),
			mgmtAID),
		newApplyDesire(t,
			kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(testSub, testRG, testCluster, testNodePool, "a2"),
			mgmtAID),
	)
	subB := buildPerMCInformers(t, ctx,
		newApplyDesire(t,
			kubeapplier.ToClusterScopedApplyDesireResourceIDString(testSub, testRG, "other-cluster", "b1"),
			mgmtBID),
	)

	u := unionkubeapplier.NewUnionKubeApplierInformers()
	if err := u.Add(mgmtAID, subA); err != nil {
		t.Fatalf("Add A: %v", err)
	}
	if err := u.Add(mgmtBID, subB); err != nil {
		t.Fatalf("Add B: %v", err)
	}

	// HasSynced across the union after both subs are added + synced.
	if !u.HasSynced() {
		t.Errorf("HasSynced = false, want true (both subs synced)")
	}

	applyInf, applyLister := u.ApplyDesires()
	deleteInf, _ := u.DeleteDesires()
	readInf, _ := u.ReadDesires()
	_ = deleteInf
	_ = readInf

	// Union lister sees both MCs' fixtures.
	all, err := applyLister.List(ctx)
	if err != nil {
		t.Fatalf("union ApplyDesireLister.List: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("union List len = %d, want 3", len(all))
	}

	// Per-MC scoping still works: ListForManagementCluster delegates to the
	// single sub's lister registered under that rid.
	gotA, err := applyLister.ListForManagementCluster(ctx, mgmtAID)
	if err != nil {
		t.Fatalf("ListForManagementCluster mgmt-a: %v", err)
	}
	if len(gotA) != 2 {
		t.Errorf("ListForManagementCluster mgmt-a len = %d, want 2", len(gotA))
	}

	// Event handlers registered on the union see events from every sub.
	// Both subs already synced, so the registration's HasSynced flips
	// true once the union has installed the handler on each.
	reg, err := applyInf.AddEventHandler(cache.ResourceEventHandlerFuncs{})
	if err != nil {
		t.Fatalf("AddEventHandler: %v", err)
	}
	waitForSync(t, ctx, reg.HasSynced)

	// Remove drops both lister and informer registrations for that MC.
	u.Remove(mgmtAID)
	after, err := applyLister.List(ctx)
	if err != nil {
		t.Fatalf("List after Remove: %v", err)
	}
	if len(after) != 1 {
		t.Errorf("List after Remove mgmt-a: len = %d, want 1", len(after))
	}
	gotARemoved, err := applyLister.ListForManagementCluster(ctx, mgmtAID)
	if err != nil {
		t.Fatalf("ListForManagementCluster mgmt-a after Remove: %v", err)
	}
	if gotARemoved != nil {
		t.Errorf("ListForManagementCluster mgmt-a after Remove: want nil, got %v", gotARemoved)
	}
}

// 1. create empty aggregator
// 2. Add(nil, nil) — assert no error, no-op
// 3. Add(rid, nil sub) — assert no error, no-op
// 4. Remove(nil) — assert no panic
func TestUnionKubeApplierInformers_NilGuards(t *testing.T) {
	u := unionkubeapplier.NewUnionKubeApplierInformers()
	if err := u.Add(nil, nil); err != nil {
		t.Errorf("Add(nil, nil): want nil, got %v", err)
	}
	if err := u.Add(mgmtAID, nil); err != nil {
		t.Errorf("Add(rid, nil): want nil, got %v", err)
	}
	u.Remove(nil)
}

// --- helpers -------------------------------------------------------------

// erroringSharedIndexInformer is a stub used to exercise the Add-rolls-back
// error path. Only AddEventHandler/AddEventHandlerWithResyncPeriod return the
// configured error; the other SharedIndexInformer methods panic if called.
type erroringSharedIndexInformer struct {
	cache.SharedIndexInformer
	addErr error
}

func (e *erroringSharedIndexInformer) AddEventHandler(_ cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error) {
	return nil, e.addErr
}
func (e *erroringSharedIndexInformer) AddEventHandlerWithResyncPeriod(_ cache.ResourceEventHandler, _ time.Duration) (cache.ResourceEventHandlerRegistration, error) {
	return nil, e.addErr
}
func (e *erroringSharedIndexInformer) RemoveEventHandler(_ cache.ResourceEventHandlerRegistration) error {
	return nil
}
func (e *erroringSharedIndexInformer) HasSynced() bool { return false }

// fakeSharedIndexInformer is a focused fake for the UnionDesireInformer
// primitive tests: it records every handler registered on it and gives back
// a fakeRegistration whose HasSynced is settable. Only the methods the union
// actually calls are wired; the rest panic via the embedded interface.
type fakeSharedIndexInformer struct {
	cache.SharedIndexInformer

	mu     sync.Mutex
	synced bool
	regs   map[*fakeRegistration]struct{}
}

type fakeRegistration struct {
	synced bool
}

func (r *fakeRegistration) HasSynced() bool { return r.synced }

func (f *fakeSharedIndexInformer) AddEventHandler(_ cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.regs == nil {
		f.regs = map[*fakeRegistration]struct{}{}
	}
	r := &fakeRegistration{synced: f.synced}
	f.regs[r] = struct{}{}
	return r, nil
}

func (f *fakeSharedIndexInformer) AddEventHandlerWithResyncPeriod(handler cache.ResourceEventHandler, _ time.Duration) (cache.ResourceEventHandlerRegistration, error) {
	return f.AddEventHandler(handler)
}

func (f *fakeSharedIndexInformer) RemoveEventHandler(reg cache.ResourceEventHandlerRegistration) error {
	r, ok := reg.(*fakeRegistration)
	if !ok {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.regs, r)
	return nil
}

func (f *fakeSharedIndexInformer) HasSynced() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.synced
}

// handlerCount returns how many handlers are currently installed on this fake.
func (f *fakeSharedIndexInformer) handlerCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.regs)
}

// setRegSync sets every currently-installed registration's HasSynced flag.
// Tests use this to drive the union registration's HasSynced semantics.
func (f *fakeSharedIndexInformer) setRegSync(synced bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for r := range f.regs {
		r.synced = synced
	}
}
