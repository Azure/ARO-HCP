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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/conditions"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/desirestatuswriter"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/keys"
)

// testMgmtClusterID is the resourceID stamped into Spec.ManagementCluster.
var testMgmtClusterID = metadataapi.Must(azcorearm.ParseResourceID(
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

func configMapTarget(name string) kubeapplierapi.ResourceReference {
	return kubeapplierapi.ResourceReference{
		Group: "", Version: "v1", Resource: "configmaps", Namespace: "default", Name: name,
	}
}

// newCadenceController builds a controller wired only with the fields the
// cadence tests touch: a real workqueue and the supplied cfg (defaults
// applied). dyn/informer/writer stay nil because these tests never reach
// SyncOnce far enough to need them — except the error-requeue test, which
// substitutes its own erroring fetcher.
func newCadenceController(t *testing.T, cfg Config) *ApplyDesireController {
	t.Helper()
	cfg = cfg.withDefaults()
	return &ApplyDesireController{
		name: "ApplyDesireController",
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[keys.ApplyDesireKey](),
			workqueue.TypedRateLimitingQueueConfig[keys.ApplyDesireKey]{Name: "test"},
		),
		cfg: cfg,
	}
}

// errFetcher implements desirestatuswriter.Fetcher and always errors.
// Used to drive processNext down the AddRateLimited path.
type errFetcher struct{ err error }

func (f *errFetcher) Fetch(context.Context, keys.ApplyDesireKey) (*kubeapplierapi.ApplyDesire, error) {
	return nil, f.err
}

// staticFetcher implements desirestatuswriter.Fetcher by returning a deep-copy
// of the pre-loaded desire. Tests wire it into the controller so SyncOnce
// always has an object to work with.
type staticFetcher struct{ desire *kubeapplierapi.ApplyDesire }

func (f *staticFetcher) Fetch(context.Context, keys.ApplyDesireKey) (*kubeapplierapi.ApplyDesire, error) {
	return f.desire.DeepCopy(), nil
}

// capturingReplacer implements desirestatuswriter.Replacer by storing the
// latest status write so tests can assert on the resulting conditions.
type capturingReplacer struct{ last *kubeapplierapi.ApplyDesire }

func (r *capturingReplacer) Replace(_ context.Context, d *kubeapplierapi.ApplyDesire) error {
	r.last = d.DeepCopy()
	return nil
}

func mustKey(t *testing.T, d *kubeapplierapi.ApplyDesire) keys.ApplyDesireKey {
	t.Helper()
	key, err := keys.ApplyDesireKeyFromResourceID(d.GetResourceID())
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}
	return key
}

// newApplyDesire builds an ApplyDesire with Type=ServerSideApply, a populated
// TargetItem and kubeContent JSON. Pass nil kubeContent to exercise the
// empty-kubeContent PreCheck. Pass a partial target to exercise the
// targetItem-validation PreChecks.
func newApplyDesire(t *testing.T, name string, target kubeapplierapi.ResourceReference, kubeContent []byte) *kubeapplierapi.ApplyDesire {
	t.Helper()
	d := &kubeapplierapi.ApplyDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID: mustParseID(t, kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(
				"00000000-0000-0000-0000-000000000001", "rg", "cluster", name,
			)),
		},
		Spec: kubeapplierapi.ApplyDesireSpec{
			ManagementCluster: testMgmtClusterID,
			Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
			TargetItem:        target,
		},
	}
	if kubeContent != nil {
		d.Spec.ServerSideApply = &kubeapplierapi.ServerSideApplyConfig{
			KubeContent: &runtime.RawExtension{Raw: kubeContent},
		}
	}
	return d
}

// withEtag is a tiny helper for cadence tests that need to construct
// before/after pairs distinguishable by the change-detection signal the
// controller uses (CosmosETag).
func withEtag(d *kubeapplierapi.ApplyDesire, etag string) *kubeapplierapi.ApplyDesire {
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
	if _, err := c.applyDesired(ctx, desire); err != nil {
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
	if got := ssaFieldManager(t, actions); got != FieldManager {
		t.Errorf("patch FieldManager = %q, want %q (default)", got, FieldManager)
	}
}

// ssaFieldManager returns the FieldManager recorded on the server-side-apply
// (apply-patch) action captured by the fake dynamic client. The fake routes
// Apply through Patch with ApplyPatchType and stashes ApplyOptions.FieldManager
// in the action's PatchOptions, so this is how tests observe which manager the
// controller selected. Fails the test if no apply-patch action was recorded.
func ssaFieldManager(t *testing.T, actions []clienttesting.Action) string {
	t.Helper()
	for _, a := range actions {
		pa, ok := a.(clienttesting.PatchActionImpl)
		if !ok || pa.GetPatchType() != types.ApplyPatchType {
			continue
		}
		return pa.PatchOptions.FieldManager
	}
	t.Fatalf("no apply-patch action recorded; actions=%v", actions)
	return ""
}

// TestApplyDesired_FieldManagerSelection verifies applyDesired selects the SSA
// field manager per-desire: the default FieldManager const when the override is
// unset (nil) or empty, and the override verbatim when set to a non-empty
// value. The non-empty case supports migrating field ownership cleanly from
// another manager (e.g. cluster-service).
func TestApplyDesired_FieldManagerSelection(t *testing.T) {
	override := "cluster-service"
	empty := ""
	cases := []struct {
		name             string
		fieldManager     *string
		wantFieldManager string
	}{
		{name: "override unset uses default", fieldManager: nil, wantFieldManager: FieldManager},
		{name: "override honored when set", fieldManager: &override, wantFieldManager: override},
		{name: "empty-string override falls back to default", fieldManager: &empty, wantFieldManager: FieldManager},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
			desire.Spec.ServerSideApply.FieldManager = tc.fieldManager

			if _, err := c.applyDesired(ctx, desire); err != nil {
				t.Fatalf("applyDesired: %v", err)
			}
			if got := ssaFieldManager(t, dyn.Actions()); got != tc.wantFieldManager {
				t.Errorf("SSA FieldManager = %q, want %q", got, tc.wantFieldManager)
			}
		})
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
		target      kubeapplierapi.ResourceReference
		kubeContent []byte
		wantSubstr  string
	}{
		{
			name:        "missing version in targetItem",
			target:      kubeapplierapi.ResourceReference{Resource: "configmaps", Namespace: "default", Name: "x"},
			kubeContent: validKubeContent,
			wantSubstr:  "version, resource, and name",
		},
		{
			name:        "missing resource in targetItem",
			target:      kubeapplierapi.ResourceReference{Version: "v1", Namespace: "default", Name: "x"},
			kubeContent: validKubeContent,
			wantSubstr:  "version, resource, and name",
		},
		{
			name:        "empty kubeContent",
			target:      configMapTarget("x"),
			kubeContent: nil,
			wantSubstr:  "spec.serverSideApply.kubeContent is empty",
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
			_, err := c.applyDesired(ctx, newApplyDesire(t, "x", tc.target, tc.kubeContent))
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

// TestHandleAdd_QueuesImmediately verifies that a brand-new ApplyDesire
// goes onto the workqueue immediately.
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

// TestHandleUpdate_EtagChangeQueuesImmediately verifies that when Cosmos
// etag differs, the controller treats the update as a content change and
// queues immediately.
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

// TestProcessNext_ErrorRequeues verifies that when SyncOnce returns an
// error, processNext rate-limits a requeue.
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

// TestHandleUpdate_UnchangedEtagQueues verifies that unchanged-etag updates
// (informer resyncs) still enqueue the key. The informer's ResyncPeriod
// controls how often these fire; the controller does not gate them further.
func TestHandleUpdate_UnchangedEtagQueues(t *testing.T) {
	c := newCadenceController(t, Config{})

	oldDesire := withEtag(newApplyDesire(t, "ok", configMapTarget("hello"), nil), "v1")
	newDesire := withEtag(newApplyDesire(t, "ok", configMapTarget("hello"), nil), "v1")
	key := mustKey(t, newDesire)

	c.handleUpdate(oldDesire, newDesire)
	if got := c.queue.Len(); got != 1 {
		t.Fatalf("queue.Len after unchanged-etag update = %d, want 1", got)
	}
	gotKey, _ := c.queue.Get()
	if gotKey != key {
		t.Errorf("queued key = %v, want %v", gotKey, key)
	}
}

// TestSyncOnce_AppliedKubeGenerationSetOnSuccess verifies that after a
// successful SSA, SyncOnce records the metadata.generation from the
// Kubernetes object returned by the apply call in
// status.AppliedKubeGeneration.
func TestSyncOnce_AppliedKubeGenerationSetOnSuccess(t *testing.T) {
	ctx := context.Background()
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	dyn := fakeDynamic(t, map[schema.GroupVersionResource]string{gvr: "ConfigMapList"})
	dyn.PrependReactor("patch", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"})
		obj.SetName(action.(clienttesting.PatchAction).GetName())
		obj.SetNamespace(action.GetNamespace())
		// Simulate the apiserver returning the object with metadata.generation set.
		obj.SetGeneration(3)
		return true, obj, nil
	})

	desire := newApplyDesire(t, "ok", configMapTarget("hello"), []byte(`{
	  "apiVersion": "v1",
	  "kind": "ConfigMap",
	  "metadata": {"name":"hello", "namespace":"default"},
	  "data": {"k":"v"}
	}`))

	fetcher := &staticFetcher{desire: desire}
	replacer := &capturingReplacer{}

	c := &ApplyDesireController{
		dyn:     dyn,
		fetcher: fetcher,
		writer: desirestatuswriter.New[kubeapplierapi.ApplyDesire, keys.ApplyDesireKey, *kubeapplierapi.ApplyDesire](
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
	if replacer.last.Status.AppliedKubeGeneration == nil {
		t.Fatal("AppliedKubeGeneration is nil after successful apply, want non-nil")
	}
	if got := *replacer.last.Status.AppliedKubeGeneration; got != 3 {
		t.Errorf("AppliedKubeGeneration = %d, want 3 (metadata.generation from SSA response)", got)
	}
	// A ServerSideApply success sets the operation-specific SuccessfullyApplied
	// condition and mirrors it onto the legacy Successful condition.
	for _, condType := range []string{kubeapplierapi.ConditionTypeSuccessfullyApplied, kubeapplierapi.ConditionTypeSuccessful} {
		if got := findCond(replacer.last.Status.Conditions, condType); got == nil || got.Status != metav1.ConditionTrue {
			t.Errorf("%s=%v, want True after successful apply", condType, got)
		}
	}
	if got := findCond(replacer.last.Status.Conditions, kubeapplierapi.ConditionTypeSuccessfullyDeleted); got != nil {
		t.Errorf("SuccessfullyDeleted should not be set on a ServerSideApply, got %v", got)
	}
}

// TestSyncOnce_AppliedKubeGenerationNilOnFailure verifies that after a failed
// SSA (PreCheckError for missing kubeContent), SyncOnce sets
// status.AppliedKubeGeneration to nil.
func TestSyncOnce_AppliedKubeGenerationNilOnFailure(t *testing.T) {
	ctx := context.Background()
	dyn := fakeDynamic(t, map[schema.GroupVersionResource]string{
		{Group: "", Version: "v1", Resource: "configmaps"}: "ConfigMapList",
	})

	// Build a desire that will fail: nil kubeContent triggers PreCheckError.
	desire := newApplyDesire(t, "fail", configMapTarget("hello"), nil)
	// Pre-seed AppliedKubeGeneration so we can confirm it gets cleared.
	var prevGen int64 = 5
	desire.Status.AppliedKubeGeneration = &prevGen

	fetcher := &staticFetcher{desire: desire}
	replacer := &capturingReplacer{}

	c := &ApplyDesireController{
		dyn:     dyn,
		fetcher: fetcher,
		writer: desirestatuswriter.New[kubeapplierapi.ApplyDesire, keys.ApplyDesireKey, *kubeapplierapi.ApplyDesire](
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
	if replacer.last.Status.AppliedKubeGeneration != nil {
		t.Errorf("AppliedKubeGeneration = %d after failed apply, want nil",
			*replacer.last.Status.AppliedKubeGeneration)
	}
}

// ---------------------------------------------------------------------------
// Delete path helpers and tests (ported from delete_desire/controller_test.go)
// ---------------------------------------------------------------------------

// newDeleteDesire builds an ApplyDesire with Type=Delete and a populated
// TargetItem. Used to exercise the evaluateDelete state machine.
func newDeleteDesire(t *testing.T, name string, target kubeapplierapi.ResourceReference) *kubeapplierapi.ApplyDesire {
	t.Helper()
	return &kubeapplierapi.ApplyDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID: mustParseID(t, kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(
				"00000000-0000-0000-0000-000000000001", "rg", "cluster", name,
			)),
		},
		Spec: kubeapplierapi.ApplyDesireSpec{
			ManagementCluster: testMgmtClusterID,
			Type:              kubeapplierapi.ApplyDesireTypeDelete,
			TargetItem:        target,
		},
	}
}

// newConfigMap builds an *unstructured.Unstructured ConfigMap. When
// withDeletionTS is true a deletionTimestamp is set (simulating a
// terminating object with a finalizer in flight).
func newConfigMap(name, ns string, withDeletionTS bool) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"})
	obj.SetName(name)
	obj.SetNamespace(ns)
	obj.SetUID(types.UID(name + "-uid"))
	if withDeletionTS {
		dt := metav1.NewTime(time.Now().Add(-time.Second))
		obj.SetDeletionTimestamp(&dt)
	}
	return obj
}

// findCond locates a condition by type in a slice, or returns nil.
func findCond(conds []metav1.Condition, condType string) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == condType {
			return &conds[i]
		}
	}
	return nil
}

// assertLegacyMirrors verifies the legacy Successful condition mirrors the
// operation-specific primary condition (same status/reason/message), which the
// controller dual-writes for backwards compatibility.
func assertLegacyMirrors(t *testing.T, conds []metav1.Condition, primary *metav1.Condition) {
	t.Helper()
	legacy := findCond(conds, kubeapplierapi.ConditionTypeSuccessful)
	if legacy == nil {
		t.Fatalf("legacy Successful condition not set (want mirror of %s)", primary.Type)
	}
	if legacy.Status != primary.Status || legacy.Reason != primary.Reason || legacy.Message != primary.Message {
		t.Errorf("legacy Successful=%+v does not mirror %s=%+v", legacy, primary.Type, primary)
	}
}

func TestEvaluateDelete_TargetGoneIsSuccessful(t *testing.T) {
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		{Version: "v1", Resource: "configmaps"}: "ConfigMapList",
	})
	c := &ApplyDesireController{dyn: dyn}

	desire := newDeleteDesire(t, "d", kubeapplierapi.ResourceReference{
		Version: "v1", Resource: "configmaps", Namespace: "default", Name: "missing",
	})
	mutate := c.evaluateDelete(context.Background(), desire)
	mutate(desire)
	got := findCond(desire.Status.Conditions, kubeapplierapi.ConditionTypeSuccessfullyDeleted)
	if got == nil || got.Status != metav1.ConditionTrue {
		t.Errorf("SuccessfullyDeleted=%v, want True (target absent)", got)
	}
	assertLegacyMirrors(t, desire.Status.Conditions, got)
}

func TestEvaluateDelete_TargetWithDeletionTimestampWaits(t *testing.T) {
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			{Version: "v1", Resource: "configmaps"}: "ConfigMapList",
		},
		newConfigMap("doomed", "default", true))
	c := &ApplyDesireController{dyn: dyn}

	desire := newDeleteDesire(t, "d", kubeapplierapi.ResourceReference{
		Version: "v1", Resource: "configmaps", Namespace: "default", Name: "doomed",
	})
	mutate := c.evaluateDelete(context.Background(), desire)
	mutate(desire)
	got := findCond(desire.Status.Conditions, kubeapplierapi.ConditionTypeSuccessfullyDeleted)
	if got == nil || got.Status != metav1.ConditionFalse {
		t.Fatalf("SuccessfullyDeleted=%v, want False (waiting)", got)
	}
	if got.Reason != kubeapplierapi.ConditionReasonWaitingForDeletion {
		t.Errorf("Reason = %q, want %q", got.Reason, kubeapplierapi.ConditionReasonWaitingForDeletion)
	}
	if !strings.Contains(got.Message, "doomed-uid") {
		t.Errorf("Message %q does not contain UID", got.Message)
	}
	assertLegacyMirrors(t, desire.Status.Conditions, got)
}

func TestEvaluateDelete_PresentNoTSIssuesDelete_ThenWaitsForFinalizers(t *testing.T) {
	cm := newConfigMap("d1", "default", false)
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			{Version: "v1", Resource: "configmaps"}: "ConfigMapList",
		},
		cm)

	// Reactor: when delete is issued, instead of removing the object, set
	// deletionTimestamp + UID — simulating a finalizer in flight.
	dyn.PrependReactor("delete", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"})
		da := action.(clienttesting.DeleteAction)
		obj.SetName(da.GetName())
		obj.SetNamespace(da.GetNamespace())
		dt := metav1.NewTime(time.Now())
		obj.SetDeletionTimestamp(&dt)
		return true, obj, nil
	})
	// Wire a counter via two-stage reactor: first call passes through (default tracker
	// returns cm without DT), second call returns terminating object.
	calls := 0
	dyn.PrependReactor("get", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
		calls++
		if calls < 2 {
			return false, nil, nil // let default reactor handle it
		}
		dt := metav1.NewTime(time.Now())
		obj := newConfigMap("d1", "default", false)
		obj.SetDeletionTimestamp(&dt)
		return true, obj, nil
	})

	c := &ApplyDesireController{dyn: dyn}
	desire := newDeleteDesire(t, "d", kubeapplierapi.ResourceReference{
		Version: "v1", Resource: "configmaps", Namespace: "default", Name: "d1",
	})
	mutate := c.evaluateDelete(context.Background(), desire)
	mutate(desire)
	got := findCond(desire.Status.Conditions, kubeapplierapi.ConditionTypeSuccessfullyDeleted)
	if got == nil || got.Status != metav1.ConditionFalse || got.Reason != kubeapplierapi.ConditionReasonWaitingForDeletion {
		t.Errorf("SuccessfullyDeleted=%v, want False/WaitingForDeletion", got)
	}
	assertLegacyMirrors(t, desire.Status.Conditions, got)
}

func TestEvaluateDelete_DeleteAPIErrorClassifiesAsKubeAPIError(t *testing.T) {
	cm := newConfigMap("d2", "default", false)
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			{Version: "v1", Resource: "configmaps"}: "ConfigMapList",
		},
		cm)
	dyn.PrependReactor("delete", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewServiceUnavailable("apiserver unavailable")
	})
	c := &ApplyDesireController{dyn: dyn}
	desire := newDeleteDesire(t, "d", kubeapplierapi.ResourceReference{
		Version: "v1", Resource: "configmaps", Namespace: "default", Name: "d2",
	})
	mutate := c.evaluateDelete(context.Background(), desire)
	mutate(desire)
	got := findCond(desire.Status.Conditions, kubeapplierapi.ConditionTypeSuccessfullyDeleted)
	if got == nil || got.Status != metav1.ConditionFalse || got.Reason != kubeapplierapi.ConditionReasonKubeAPIError {
		t.Errorf("SuccessfullyDeleted=%v, want False/KubeAPIError", got)
	}
	assertLegacyMirrors(t, desire.Status.Conditions, got)
}

func TestEvaluateDelete_BadTargetIsPreCheckFailed(t *testing.T) {
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), nil)
	c := &ApplyDesireController{dyn: dyn}
	desire := newDeleteDesire(t, "d", kubeapplierapi.ResourceReference{
		// Missing Resource and Name.
	})
	mutate := c.evaluateDelete(context.Background(), desire)
	mutate(desire)
	got := findCond(desire.Status.Conditions, kubeapplierapi.ConditionTypeSuccessfullyDeleted)
	if got == nil || got.Reason != kubeapplierapi.ConditionReasonPreCheckFailed {
		t.Errorf("SuccessfullyDeleted=%v, want PreCheckFailed", got)
	}
	assertLegacyMirrors(t, desire.Status.Conditions, got)
}
