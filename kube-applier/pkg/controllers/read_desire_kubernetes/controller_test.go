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

package read_desire_kubernetes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/openshift/library-go/pkg/manifestclient"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/conditions"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/desirestatuswriter"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/keys"
)

const (
	testSub      = "00000000-0000-0000-0000-000000000001"
	testRG       = "rg"
	testCluster  = "c"
	testDesire   = "d"
	testTargetNs = "default"
)

// testMgmtID is the resourceID stamped into Spec.ManagementCluster; testMgmt
// is the lowercased-string form used as the Cosmos partition key.
var (
	testMgmtID = metadataapi.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/mgmt-1"))
)

func mustParseID(t *testing.T, s string) *azcorearm.ResourceID {
	t.Helper()
	id, err := azcorearm.ParseResourceID(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return id
}

func newReadDesire(t *testing.T, target kubeapplierapi.ResourceReference) *kubeapplierapi.ReadDesire {
	t.Helper()
	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   mustParseID(t, kubeapplierapihelpers.ToClusterScopedReadDesireResourceIDString(testSub, testRG, testCluster, testDesire)),
			PartitionKey: strings.ToLower(testMgmtID.String()),
		},
		Spec: kubeapplierapi.ReadDesireSpec{
			ManagementCluster: testMgmtID,
			TargetItem:        target,
		},
	}
}

// recordingWriter captures the outcome of every UpdateStatus call so tests can
// assert on what the controller would persist. The desire pointer is shared
// with the test so subsequent reads see prior mutations.
type recordingWriter struct {
	updates []*kubeapplierapi.ReadDesire
	desire  *kubeapplierapi.ReadDesire
}

func (w *recordingWriter) UpdateStatus(ctx context.Context, key keys.ReadDesireKey, mutate desirestatuswriter.MutateFunc[kubeapplierapi.ReadDesire]) error {
	if w.desire == nil {
		return nil
	}
	cp := *w.desire
	mutate(&cp)
	w.updates = append(w.updates, &cp)
	*w.desire = cp
	return nil
}

func configMapTarget(name string) kubeapplierapi.ResourceReference {
	return kubeapplierapi.ResourceReference{
		Group: "", Version: "v1", Resource: "configmaps", Namespace: testTargetNs, Name: name,
	}
}

// dynamicForTestdata builds a dynamic.Interface backed by library-go's
// manifestclient over the named testdata directory. The manifestclient uses
// its embedded default discovery for built-in resources, so a ConfigMap
// target resolves without us shipping discovery YAML alongside the manifests.
func dynamicForTestdata(t *testing.T, dir string) dynamic.Interface {
	t.Helper()
	httpClient := &http.Client{Transport: manifestclient.NewRoundTripper(dir)}
	dyn, err := dynamic.NewForConfigAndClient(manifestclient.RecommendedRESTConfig(), httpClient)
	if err != nil {
		t.Fatalf("dynamic.NewForConfigAndClient: %v", err)
	}
	return dyn
}

// startSyncedControllerRaw builds the controller via the real constructor over
// a Cosmos mock pre-seeded with desire, starts its informer, and waits for the
// cache to sync. It leaves the controller's real desirestatuswriter in place so
// callers that care about the write path (no-op suppression) can exercise it or
// swap in their own writer. The mock is returned so callers can wire a counting
// replacer or inspect stored state. The test's parent ctx cancels everything.
func startSyncedControllerRaw(
	t *testing.T,
	ctx context.Context,
	target kubeapplierapi.ResourceReference,
	desire *kubeapplierapi.ReadDesire,
	dyn dynamic.Interface,
) (*ReadDesireKubernetesController, *kubeappliercosmosstoragetesting.MockKubeApplierDBClient) {
	t.Helper()

	key, err := keys.ReadDesireKeyFromResourceID(desire.GetResourceID())
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}

	// Pre-populate a MockKubeApplierDBClient with the desire so the
	// controller's fetcher can read it back via the live-client contract.
	mock := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	crud, err := mock.ReadDesiresForCluster(testSub, testRG, testCluster)
	if err != nil {
		t.Fatalf("ReadDesiresForCluster: %v", err)
	}
	if _, err := crud.Create(ctx, desire, nil); err != nil {
		t.Fatalf("seed Create: %v", err)
	}

	c, err := NewReadDesireKubernetesController(key, target, dyn, mock)
	if err != nil {
		t.Fatalf("NewReadDesireKubernetesController: %v", err)
	}

	// Run the per-instance informer just long enough for it to sync against the
	// manifestclient-backed list. The test's parent ctx will cancel everything.
	go c.informer.RunWithContext(ctx)
	syncCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !cache.WaitForCacheSync(syncCtx.Done(), c.informer.HasSynced) {
		t.Fatal("informer did not sync within 5s")
	}
	return c, mock
}

// startSyncedController is the common case: startSyncedControllerRaw plus a
// recordingWriter swapped in so tests can assert on the mutate callback's
// effect without exercising the full desirestatuswriter -> CRUD chain.
func startSyncedController(
	t *testing.T,
	ctx context.Context,
	target kubeapplierapi.ResourceReference,
	desire *kubeapplierapi.ReadDesire,
	dyn dynamic.Interface,
) (*ReadDesireKubernetesController, *recordingWriter) {
	t.Helper()
	c, _ := startSyncedControllerRaw(t, ctx, target, desire, dyn)
	w := &recordingWriter{desire: desire}
	c.writer = w
	return c, w
}

// countingReplacer wraps a desirestatuswriter.Replacer and counts how many
// times Replace is actually invoked, i.e. how many Cosmos writes the real
// StatusWriter issues after its DeepEqual no-op check.
type countingReplacer struct {
	inner desirestatuswriter.Replacer[kubeapplierapi.ReadDesire]
	count int
}

func (r *countingReplacer) Replace(ctx context.Context, desired *kubeapplierapi.ReadDesire) error {
	r.count++
	return r.inner.Replace(ctx, desired)
}

func TestSyncOnce_TargetExists_PopulatesKubeContent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	target := configMapTarget("hello")
	desire := newReadDesire(t, target)
	dyn := dynamicForTestdata(t, "testdata/configmap_present")

	c, w := startSyncedController(t, ctx, target, desire, dyn)
	if err := c.SyncOnce(ctx); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if len(w.updates) == 0 {
		t.Fatal("no status update recorded")
	}
	last := w.updates[len(w.updates)-1]
	if last.Status.KubeContent == nil || len(last.Status.KubeContent.Raw) == 0 {
		t.Fatal("KubeContent is empty after sync")
	}
	var got map[string]any
	if err := json.Unmarshal(last.Status.KubeContent.Raw, &got); err != nil {
		t.Fatalf("unmarshal kubeContent: %v", err)
	}
	if got["kind"] != "ConfigMap" {
		t.Errorf("kind = %v, want ConfigMap", got["kind"])
	}
	cond := findCond(last.Status.Conditions, kubeapplierapi.ConditionTypeSuccessful)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Errorf("Successful=%v, want True", cond)
	}
}

func TestSyncOnce_TargetAbsent_ReportsSuccessful(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	target := configMapTarget("missing")
	desire := newReadDesire(t, target)
	dyn := dynamicForTestdata(t, "testdata/configmap_absent")

	c, w := startSyncedController(t, ctx, target, desire, dyn)
	if err := c.SyncOnce(ctx); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if len(w.updates) == 0 {
		t.Fatal("no status update recorded")
	}
	last := w.updates[len(w.updates)-1]
	if last.Status.KubeContent != nil {
		t.Errorf("KubeContent should be nil when target is absent, got %s", last.Status.KubeContent.Raw)
	}
	cond := findCond(last.Status.Conditions, kubeapplierapi.ConditionTypeSuccessful)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Errorf("Successful=%v, want True", cond)
	}
}

// TestSyncOnce_TargetAbsent_ClearsExistingContent covers invariant 4: when the
// target has disappeared (exists==false) but KubeContent was previously set,
// the content is cleared to nil.
func TestSyncOnce_TargetAbsent_ClearsExistingContent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	//GIVEN: a controller with a stored configmap, which is no longer present in the mgmt cluster
	target := configMapTarget("missing")
	desire := newReadDesire(t, target)
	desire.Status.KubeContent = &runtime.RawExtension{Raw: []byte(`{"kind":"ConfigMap"}`)}
	dyn := dynamicForTestdata(t, "testdata/configmap_absent")

	c, w := startSyncedController(t, ctx, target, desire, dyn)

	// WHEN: the controller is synced
	if err := c.SyncOnce(ctx); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	// THEN: the status update clears the KubeContent
	if len(w.updates) == 0 {
		t.Fatal("no status update recorded")
	}
	last := w.updates[len(w.updates)-1]
	if last.Status.KubeContent != nil {
		t.Errorf("KubeContent should be cleared when target vanished, got %s", last.Status.KubeContent.Raw)
	}
}

// TestNewReadDesireKubernetesController_RejectsIncompleteTarget exercises the
// pre-flight validation in the constructor: missing version, resource, or name
// returns a *PreCheckError without touching the dynamic client.
func TestNewReadDesireKubernetesController_RejectsIncompleteTarget(t *testing.T) {
	cases := []struct {
		name   string
		target kubeapplierapi.ResourceReference
	}{
		{
			name:   "missing version",
			target: kubeapplierapi.ResourceReference{Resource: "configmaps", Name: "x"},
		},
		{
			name:   "missing resource",
			target: kubeapplierapi.ResourceReference{Version: "v1", Name: "x"},
		},
		{
			name:   "missing name",
			target: kubeapplierapi.ResourceReference{Version: "v1", Resource: "configmaps"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewReadDesireKubernetesController(keys.ReadDesireKey{}, tc.target, nil, nil)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if _, ok := err.(*conditions.PreCheckError); !ok {
				t.Errorf("error %v is not *PreCheckError", err)
			}
			if !strings.Contains(err.Error(), "version, resource, and name") {
				t.Errorf("error %q lacks expected substring", err.Error())
			}
		})
	}
}

// TestSyncOnce_NoOpWhenOnlyFormattingDiffers is the core regression test: when
// the stored KubeContent is semantically identical to the freshly observed
// object but differs only in JSON formatting (as it does after a Cosmos
// round-trip), SyncOnce must not republish the content. Only the Successful
// condition is (re)affirmed, so the writer's DeepEqual sees no change and no
// Cosmos write / instanceVersion bump occurs.
func TestSyncOnce_NoOpWhenOnlyFormattingDiffers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// GIVEN: a controller with a stored KubeContent that is semantically identical to the freshly observed object but differs only in JSON formatting
	target := configMapTarget("hello")
	dyn := dynamicForTestdata(t, "testdata/configmap_present")

	// First, capture the canonical (Go-marshaled) payload the controller
	// produces for the observed object.
	canonical := getRawKubeContentFromUpdate(ctx, t, target, dyn)

	var reformatted bytes.Buffer
	if err := json.Indent(&reformatted, canonical, "", "  "); err != nil {
		t.Fatalf("json.Indent: %v", err)
	}
	reformattedBytes := append([]byte(nil), reformatted.Bytes()...)

	desire := newReadDesire(t, target)
	desire.Status.KubeContent = &runtime.RawExtension{Raw: reformattedBytes}
	c, w := startSyncedController(t, ctx, target, desire, dyn)

	// WHEN: the controller is synced
	if err := c.SyncOnce(ctx); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	// THEN: the status update is a no-op on content: the stored (reformatted) bytes are left untouched, proving the guard did not republish the canonical newRaw.
	if len(w.updates) == 0 {
		t.Fatal("no status update recorded")
	}
	last := w.updates[len(w.updates)-1]
	if last.Status.KubeContent == nil {
		t.Fatal("KubeContent unexpectedly cleared on no-op sync")
	}
	if !bytes.Equal(last.Status.KubeContent.Raw, reformattedBytes) {
		t.Errorf("KubeContent was rewritten on a no-op sync:\n got  %s\n want %s",
			last.Status.KubeContent.Raw, reformattedBytes)
	}
}

func getRawKubeContentFromUpdate(ctx context.Context, t *testing.T, target kubeapplierapi.ResourceReference, dyn dynamic.Interface) []byte {
	probeDesire := newReadDesire(t, target)
	probe, probeW := startSyncedController(t, ctx, target, probeDesire, dyn)
	if err := probe.SyncOnce(ctx); err != nil {
		t.Fatalf("probe SyncOnce: %v", err)
	}
	if len(probeW.updates) == 0 || probeW.updates[len(probeW.updates)-1].Status.KubeContent == nil {
		t.Fatal("probe sync did not populate KubeContent")
	}
	kubeContent := append([]byte(nil), probeW.updates[len(probeW.updates)-1].Status.KubeContent.Raw...)
	return kubeContent
}

// TestSyncOnce_NoOpResync_NoCosmosWrite is the end-to-end guarantee that a steady-state resync
// (observed object semantically unchanged, Successful already True) must issue ZERO Cosmos writes.
// Unlike TestSyncOnce_NoOpWhenOnlyFormattingDiffers, which swaps in a recordingWriter
// and only asserts the mutate callback leaves KubeContent alone, this test
// drives the REAL desirestatuswriter (fetch -> mutate -> DeepEqual -> replace)
// and counts how often it actually reaches the replacer.
func TestSyncOnce_NoOpResync_NoCosmosWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// GIVEN: a desire whose stored KubeContent equals the observed object modulo
	target := configMapTarget("hello")
	dyn := dynamicForTestdata(t, "testdata/configmap_present")

	canonical := getRawKubeContentFromUpdate(ctx, t, target, dyn)
	var reformatted bytes.Buffer
	if err := json.Indent(&reformatted, canonical, "", "  "); err != nil {
		t.Fatalf("json.Indent: %v", err)
	}

	desire := newReadDesire(t, target)
	desire.Status.KubeContent = &runtime.RawExtension{Raw: append([]byte(nil), reformatted.Bytes()...)}
	// Pre-set Successful=True so the no-op branch's SetSuccessful is itself a
	// no-op; otherwise the first cycle would legitimately flip Unknown->True and
	// write once (invariant 2), which is not what this test measures.
	conditions.SetSuccessful(&desire.Status.Conditions, nil)

	c, mock := startSyncedControllerRaw(t, ctx, target, desire, dyn)

	// Drive the real writer, but wrap its replacer so we can count actual writes.
	rep := &countingReplacer{inner: &readDesireReplacer{crudByParent: mock}}
	c.writer = desirestatuswriter.New[kubeapplierapi.ReadDesire, keys.ReadDesireKey, *kubeapplierapi.ReadDesire](
		&readDesireFetcher{crudByParent: mock}, rep,
	)

	// WHEN: a resync fires against unchanged state.
	if err := c.SyncOnce(ctx); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	// THEN: the writer's DeepEqual short-circuits before the replacer -> no write.
	if rep.count != 0 {
		t.Errorf("no-op resync issued %d Cosmos write(s), want 0", rep.count)
	}
}

// TestSyncOnce_PublishesRealChange proves that a genuine content difference
// still publishes the freshly observed payload verbatim.
func TestSyncOnce_PublishesRealChange(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	//GIVEN: a controller with a stored configmap, which has a stale payload
	target := configMapTarget("hello")
	desire := newReadDesire(t, target)
	dyn := dynamicForTestdata(t, "testdata/configmap_present")

	stale := []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"hello","namespace":"default"},"data":{"stale":"true"}}`)
	desire.Status.KubeContent = &runtime.RawExtension{Raw: stale}

	c, w := startSyncedController(t, ctx, target, desire, dyn)

	// WHEN: the controller is synced
	if err := c.SyncOnce(ctx); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	// THEN: the status update publishes the freshly observed payload verbatim
	if len(w.updates) == 0 {
		t.Fatal("no status update recorded")
	}
	last := w.updates[len(w.updates)-1]
	if last.Status.KubeContent == nil {
		t.Fatal("KubeContent nil after publishing a real change")
	}
	if bytes.Equal(last.Status.KubeContent.Raw, stale) {
		t.Fatal("KubeContent still holds the stale payload; real change was not published")
	}
	var got map[string]any
	if err := json.Unmarshal(last.Status.KubeContent.Raw, &got); err != nil {
		t.Fatalf("unmarshal kubeContent: %v", err)
	}
	if got["kind"] != "ConfigMap" {
		t.Errorf("kind = %v, want ConfigMap", got["kind"])
	}
}

func findCond(conds []metav1.Condition, t string) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == t {
			return &conds[i]
		}
	}
	return nil
}
