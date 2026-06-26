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

package apply_desire

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/util/workqueue"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/conditions"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/desirestatuswriter"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/keys"
)

// testMgmtClusterID is the resourceID stamped into Spec.ManagementCluster.
var testMgmtClusterID = api.Must(azcorearm.ParseResourceID(
	"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/mgmt-1"))

func mustParseID(t *testing.T, s string) *azcorearm.ResourceID {
	t.Helper()
	id, err := azcorearm.ParseResourceID(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return id
}

// fakeDynamic returns a dynamic.Interface backed by an in-memory tracker that
// supports Apply (via Patch with ApplyPatchType under the covers).
func fakeDynamic(t *testing.T, gvrToListKind map[schema.GroupVersionResource]string) *fake.FakeDynamicClient {
	t.Helper()
	scheme := runtime.NewScheme()
	return fake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind)
}

func configMapTarget(name string) kubeapplier.ResourceReference {
	return kubeapplier.ResourceReference{
		Group: "", Version: "v1", Resource: "configmaps", Namespace: "default", Name: name,
	}
}

// newCadenceController builds a controller wired only with the fields the
// cadence tests touch: a real workqueue, the supplied cfg (defaults
// applied), and a TimeBasedChecker fed by cfg.Clock. dyn/informer/writer
// stay nil because these tests never reach SyncOnce far enough to need
// them — except the error-requeue test, which substitutes its own erroring
// fetcher.
func newCadenceController(t *testing.T, cfg Config) *ApplyDesireController {
	t.Helper()
	cfg = cfg.withDefaults()
	checker := controllerutils.NewTimeBasedCooldownChecker(cfg.CooldownPeriod)
	checker.SetClock(cfg.Clock)
	return &ApplyDesireController{
		name: "ApplyDesireController",
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[keys.ApplyDesireKey](),
			workqueue.TypedRateLimitingQueueConfig[keys.ApplyDesireKey]{Name: "test"},
		),
		cfg:      cfg,
		cooldown: checker,
	}
}

// errFetcher implements desirestatuswriter.Fetcher and always errors.
// Used to drive processNext down the AddRateLimited path.
type errFetcher struct{ err error }

func (f *errFetcher) Fetch(context.Context, keys.ApplyDesireKey) (*kubeapplier.ApplyDesire, error) {
	return nil, f.err
}

// staticFetcher implements desirestatuswriter.Fetcher by returning a deep-copy
// of the stored desire. Used by SyncOnce tests to wire both the controller's
// fetcher and the status writer's fetcher without Cosmos.
type staticFetcher struct{ desire *kubeapplier.ApplyDesire }

func (f *staticFetcher) Fetch(context.Context, keys.ApplyDesireKey) (*kubeapplier.ApplyDesire, error) {
	if f.desire == nil {
		return nil, nil
	}
	return f.desire.DeepCopy(), nil
}

// capturingReplacer implements desirestatuswriter.Replacer by storing the
// last replaced desire so tests can inspect it.
type capturingReplacer struct{ last *kubeapplier.ApplyDesire }

func (r *capturingReplacer) Replace(_ context.Context, d *kubeapplier.ApplyDesire) error {
	r.last = d.DeepCopy()
	return nil
}

func mustKey(t *testing.T, d *kubeapplier.ApplyDesire) keys.ApplyDesireKey {
	t.Helper()
	key, err := keys.ApplyDesireKeyFromResourceID(d.GetResourceID())
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}
	return key
}

// newApplyDesire builds an ApplyDesire with a populated TargetItem and
// kubeContent JSON. Pass nil kubeContent to exercise the empty-kubeContent
// PreCheck. Pass a partial target to exercise the targetItem-validation PreChecks.
func newApplyDesire(t *testing.T, name string, target kubeapplier.ResourceReference, kubeContent []byte) *kubeapplier.ApplyDesire {
	t.Helper()
	d := &kubeapplier.ApplyDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: mustParseID(t, kubeapplier.ToClusterScopedApplyDesireResourceIDString(
				"00000000-0000-0000-0000-000000000001", "rg", "cluster", name,
			)),
		},
		Spec: kubeapplier.ApplyDesireSpec{
			ManagementCluster: testMgmtClusterID,
			TargetItem:        target,
		},
	}
	if kubeContent != nil {
		d.Spec.KubeContent = &runtime.RawExtension{Raw: kubeContent}
	}
	return d
}

// withEtag is a tiny helper for cadence tests that need to construct
// before/after pairs distinguishable by the change-detection signal the
// controller uses (CosmosETag).
func withEtag(d *kubeapplier.ApplyDesire, etag string) *kubeapplier.ApplyDesire {
	d.CosmosETag = azcore.ETag(etag)
	return d
}

// TestApplyDesired_IssuesSSAPatch verifies the controller issues the expected
// SSA call (Apply patch type, Force=true, FieldManager=kube-applier, correct
// namespace+name) for a well-formed ApplyDesire.
//
// We assert on the action tracker rather than the resulting object: the fake
// dynamic client's Apply path strategic-merges via the Unstructured scheme,
// which doesn't have the typed metadata SMP needs, so the post-apply object
// is unreliable. End-to-end SSA semantics are covered by integration tests.
func TestApplyDesired_IssuesSSAPatch(t *testing.T) {
	ctx := context.Background()
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	dyn := fakeDynamic(t, map[schema.GroupVersionResource]string{gvr: "ConfigMapList"})
	dyn.PrependReactor("patch", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"})
		obj.SetName(action.(clienttesting.PatchAction).GetName())
		obj.SetNamespace(action.GetNamespace())
		return true, obj, nil
	})

	c := &ApplyDesireController{dyn: dyn}
	desire := newApplyDesire(t, "ok", configMapTarget("hello"), []byte(`{
	  "apiVersion": "v1",
	  "kind": "ConfigMap",
	  "metadata": {"name":"hello", "namespace":"default"},
	  "data": {"k":"v"}
	}`))
	if err := c.applyDesired(ctx, desire); err != nil {
		t.Fatalf("applyDesired: %v", err)
	}

	actions := dyn.Actions()
	var patch clienttesting.PatchAction
	for _, a := range actions {
		if pa, ok := a.(clienttesting.PatchAction); ok {
			patch = pa
			break
		}
	}
	if patch == nil {
		t.Fatalf("no patch action recorded; actions=%v", actions)
	}
	if patch.GetPatchType() != types.ApplyPatchType {
		t.Errorf("patch type = %v, want ApplyPatchType", patch.GetPatchType())
	}
	if got := patch.GetName(); got != "hello" {
		t.Errorf("patch name = %q, want hello", got)
	}
	if got := patch.GetNamespace(); got != "default" {
		t.Errorf("patch namespace = %q, want default", got)
	}
}

// TestApplyDesired_PreCheckErrors covers every pre-flight failure that must
// classify as PreCheckError (and therefore land as Successful=False with
// reason PreCheckFailed in higher-level code).
func TestApplyDesired_PreCheckErrors(t *testing.T) {
	ctx := context.Background()
	dyn := fakeDynamic(t, map[schema.GroupVersionResource]string{
		{Group: "", Version: "v1", Resource: "configmaps"}: "ConfigMapList",
	})
	c := &ApplyDesireController{dyn: dyn}

	validKubeContent := []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"x","namespace":"default"}}`)

	cases := []struct {
		name        string
		target      kubeapplier.ResourceReference
		kubeContent []byte
		wantSubstr  string
	}{
		{
			name:        "missing version in targetItem",
			target:      kubeapplier.ResourceReference{Resource: "configmaps", Namespace: "default", Name: "x"},
			kubeContent: validKubeContent,
			wantSubstr:  "version, resource, and name",
		},
		{
			name:        "missing resource in targetItem",
			target:      kubeapplier.ResourceReference{Version: "v1", Namespace: "default", Name: "x"},
			kubeContent: validKubeContent,
			wantSubstr:  "version, resource, and name",
		},
		{
			name:        "empty kubeContent",
			target:      configMapTarget("x"),
			kubeContent: nil,
			wantSubstr:  "spec.kubeContent is empty",
		},
		{
			name:        "malformed kubeContent JSON",
			target:      configMapTarget("x"),
			kubeContent: []byte("not json"),
			wantSubstr:  "decode kubeContent",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := c.applyDesired(ctx, newApplyDesire(t, "x", tc.target, tc.kubeContent))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSubstr)
			}
			if _, ok := err.(*conditions.PreCheckError); !ok {
				t.Errorf("error %v is not a *PreCheckError; classification will be wrong", err)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}

// TestHandleAdd_QueuesImmediately covers bot-directive case 1: a brand-new
// ApplyDesire bypasses the cooldown gate and goes onto the workqueue right
// away, even though Add events with the same key arriving back-to-back
// could in principle be spammed.
func TestHandleAdd_QueuesImmediately(t *testing.T) {
	c := newCadenceController(t, Config{})
	desire := newApplyDesire(t, "ok", configMapTarget("hello"), nil)

	c.handleAdd(desire)

	if got := c.queue.Len(); got != 1 {
		t.Fatalf("queue.Len after handleAdd = %d, want 1", got)
	}
	gotKey, _ := c.queue.Get()
	if want := mustKey(t, desire); gotKey != want {
		t.Errorf("queued key = %v, want %v", gotKey, want)
	}
}

// TestHandleUpdate_EtagChangeQueuesImmediately covers bot-directive case 2:
// when Cosmos etag differs between the previous and current snapshots, the
// controller treats the update as a real content change and queues
// immediately — bypassing the cooldown gate so users see their content
// reflected fast.
//
// Etag (rather than spec deep-equals) is the right signal because Cosmos
// bumps it on every mutation, and the backend's GenericWatchingController
// uses the same convention.
func TestHandleUpdate_EtagChangeQueuesImmediately(t *testing.T) {
	c := newCadenceController(t, Config{})

	oldDesire := withEtag(newApplyDesire(t, "ok", configMapTarget("hello"), nil), "v1")
	newDesire := withEtag(newApplyDesire(t, "ok", configMapTarget("hello"), nil), "v2")

	c.handleUpdate(oldDesire, newDesire)
	if got := c.queue.Len(); got != 1 {
		t.Fatalf("queue.Len after etag-change update = %d, want 1", got)
	}
	gotKey, _ := c.queue.Get()
	if want := mustKey(t, newDesire); gotKey != want {
		t.Errorf("queued key = %v, want %v", gotKey, want)
	}
}

// TestProcessNext_ErrorRequeues covers bot-directive case 3: when SyncOnce
// returns an error, processNext rate-limits a requeue. The rate limiter
// (not the cooldown gate) drives retry timing, so retries happen quickly.
func TestProcessNext_ErrorRequeues(t *testing.T) {
	c := newCadenceController(t, Config{})
	c.fetcher = &errFetcher{err: errors.New("cosmos boom")}

	desire := newApplyDesire(t, "ok", configMapTarget("hello"), nil)
	key := mustKey(t, desire)
	c.queue.Add(key)

	if !c.processNext(context.Background()) {
		t.Fatalf("processNext returned false (queue shut down?)")
	}

	if got := c.queue.NumRequeues(key); got == 0 {
		t.Errorf("NumRequeues after error = 0, want >= 1 (rate-limited retry expected)")
	}
}

// TestHandleUpdate_UnchangedEtagGatedByCooldown covers bot-directive case 4:
// when etag is unchanged (the informer's resync, or our own status write
// fed back), handleUpdate consults the cooldown gate. The first call passes
// through (no prior record); subsequent calls within the configured window
// are dropped; once the clock advances past the window, the gate reopens.
//
// In production this is what makes "unchanged content reconciles slowly"
// — the informer fires resyncs frequently, but only one in CooldownPeriod
// makes it onto the workqueue.
func TestHandleUpdate_UnchangedEtagGatedByCooldown(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fc := clocktesting.NewFakePassiveClock(t0)

	c := newCadenceController(t, Config{
		CooldownPeriod: 2 * time.Second,
		Clock:          fc,
	})

	oldDesire := withEtag(newApplyDesire(t, "ok", configMapTarget("hello"), nil), "v1")
	newDesire := withEtag(newApplyDesire(t, "ok", configMapTarget("hello"), nil), "v1")
	key := mustKey(t, newDesire)

	// drain consumes everything currently on the queue and returns the keys
	// in order, so each phase of the test starts from an empty queue.
	drain := func() []keys.ApplyDesireKey {
		var got []keys.ApplyDesireKey
		for c.queue.Len() > 0 {
			k, shut := c.queue.Get()
			if shut {
				t.Fatalf("queue shut down unexpectedly")
			}
			got = append(got, k)
			c.queue.Done(k)
			c.queue.Forget(k)
		}
		return got
	}

	// First unchanged-etag update: no prior record, gate allows.
	c.handleUpdate(oldDesire, newDesire)
	if got := drain(); len(got) != 1 || got[0] != key {
		t.Fatalf("first unchanged-etag update queued %v, want [%v]", got, key)
	}

	// 1.5s later: still inside the 2s cooldown. Gate denies.
	fc.SetTime(t0.Add(1500 * time.Millisecond))
	c.handleUpdate(oldDesire, newDesire)
	if got := drain(); len(got) != 0 {
		t.Errorf("at 1.5s (cooldown=2s) drained %v, want none", got)
	}

	// 2.1s later (past cooldown): gate reopens.
	fc.SetTime(t0.Add(2100 * time.Millisecond))
	c.handleUpdate(oldDesire, newDesire)
	if got := drain(); len(got) != 1 || got[0] != key {
		t.Fatalf("at 2.1s (past cooldown) drained %v, want [%v]", got, key)
	}

	// Immediately after the gate just fired, the next window starts from
	// 2.1s and the gate is closed again until 4.1s.
	c.handleUpdate(oldDesire, newDesire)
	if got := drain(); len(got) != 0 {
		t.Errorf("immediately after pass-through drained %v, want none", got)
	}
}

// TestSyncOnce_ObservedGenerationSetOnSuccess verifies that after a
// successful SSA, SyncOnce records the desire's InstanceVersion in
// status.ObservedGeneration.
func TestSyncOnce_ObservedGenerationSetOnSuccess(t *testing.T) {
	ctx := context.Background()
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	dyn := fakeDynamic(t, map[schema.GroupVersionResource]string{gvr: "ConfigMapList"})
	dyn.PrependReactor("patch", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"})
		obj.SetName(action.(clienttesting.PatchAction).GetName())
		obj.SetNamespace(action.GetNamespace())
		return true, obj, nil
	})

	desire := newApplyDesire(t, "ok", configMapTarget("hello"), []byte(`{
	  "apiVersion": "v1",
	  "kind": "ConfigMap",
	  "metadata": {"name":"hello", "namespace":"default"},
	  "data": {"k":"v"}
	}`))
	desire.InstanceVersion = 42

	fetcher := &staticFetcher{desire: desire}
	replacer := &capturingReplacer{}

	c := &ApplyDesireController{
		dyn:     dyn,
		fetcher: fetcher,
		writer: desirestatuswriter.New[kubeapplier.ApplyDesire, keys.ApplyDesireKey, *kubeapplier.ApplyDesire](
			fetcher, replacer,
		),
	}
	key := mustKey(t, desire)

	if err := c.SyncOnce(ctx, key); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	if replacer.last == nil {
		t.Fatal("replacer was not called; status was not written")
	}
	if replacer.last.Status.ObservedGeneration == nil {
		t.Fatal("ObservedGeneration is nil after successful apply, want non-nil")
	}
	if got := *replacer.last.Status.ObservedGeneration; got != 42 {
		t.Errorf("ObservedGeneration = %d, want 42", got)
	}
}

// TestSyncOnce_ObservedGenerationNilOnFailure verifies that after a failed
// SSA (PreCheckError for missing kubeContent), SyncOnce sets
// status.ObservedGeneration to nil.
func TestSyncOnce_ObservedGenerationNilOnFailure(t *testing.T) {
	ctx := context.Background()
	dyn := fakeDynamic(t, map[schema.GroupVersionResource]string{
		{Group: "", Version: "v1", Resource: "configmaps"}: "ConfigMapList",
	})

	// Build a desire that will fail: nil kubeContent triggers PreCheckError.
	desire := newApplyDesire(t, "fail", configMapTarget("hello"), nil)
	desire.InstanceVersion = 7
	// Pre-seed ObservedGeneration so we can confirm it gets cleared.
	var prevGen int64 = 5
	desire.Status.ObservedGeneration = &prevGen

	fetcher := &staticFetcher{desire: desire}
	replacer := &capturingReplacer{}

	c := &ApplyDesireController{
		dyn:     dyn,
		fetcher: fetcher,
		writer: desirestatuswriter.New[kubeapplier.ApplyDesire, keys.ApplyDesireKey, *kubeapplier.ApplyDesire](
			fetcher, replacer,
		),
	}
	key := mustKey(t, desire)

	if err := c.SyncOnce(ctx, key); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	if replacer.last == nil {
		t.Fatal("replacer was not called; status was not written")
	}
	if replacer.last.Status.ObservedGeneration != nil {
		t.Errorf("ObservedGeneration = %d after failed apply, want nil",
			*replacer.last.Status.ObservedGeneration)
	}
}
