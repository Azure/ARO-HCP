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
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/openshift/library-go/pkg/manifestclient"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
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
	testMgmtID = api.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/mgmt-1"))
	testMgmt = strings.ToLower(testMgmtID.String())
)

func mustParseID(t *testing.T, s string) *azcorearm.ResourceID {
	t.Helper()
	id, err := azcorearm.ParseResourceID(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return id
}

func newReadDesire(t *testing.T, target kubeapplier.ResourceReference) *kubeapplier.ReadDesire {
	t.Helper()
	return &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: mustParseID(t, kubeapplier.ToClusterScopedReadDesireResourceIDString(testSub, testRG, testCluster, testDesire)),
		},
		Spec: kubeapplier.ReadDesireSpec{
			ManagementCluster: testMgmtID,
			TargetItem:        target,
		},
	}
}

// recordingWriter captures the outcome of every UpdateStatus call so tests can
// assert on what the controller would persist. The desire pointer is shared
// with the test so subsequent reads see prior mutations.
type recordingWriter struct {
	updates []*kubeapplier.ReadDesire
	desire  *kubeapplier.ReadDesire
}

func (w *recordingWriter) UpdateStatus(ctx context.Context, key keys.ReadDesireKey, mutate desirestatuswriter.MutateFunc[kubeapplier.ReadDesire]) error {
	if w.desire == nil {
		return nil
	}
	cp := *w.desire
	mutate(&cp)
	w.updates = append(w.updates, &cp)
	*w.desire = cp
	return nil
}

func configMapTarget(name string) kubeapplier.ResourceReference {
	return kubeapplier.ResourceReference{
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

// startSyncedController builds the controller via the real constructor, starts
// its informer, and waits for the cache to sync. The test owns the cancel
// function and runs SyncOnce against a deterministic cache state.
func startSyncedController(
	t *testing.T,
	ctx context.Context,
	target kubeapplier.ResourceReference,
	desire *kubeapplier.ReadDesire,
	dyn dynamic.Interface,
) (*ReadDesireKubernetesController, *recordingWriter) {
	t.Helper()

	key, err := keys.ReadDesireKeyFromResourceID(desire.GetResourceID())
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}

	// Pre-populate a MockKubeApplierDBClient with the desire so the
	// controller's fetcher can read it back via the live-client contract.
	mock := databasetesting.NewMockKubeApplierDBClient()
	parent := database.ResourceParent{
		SubscriptionID: testSub, ResourceGroupName: testRG, ClusterName: testCluster,
	}
	crud, err := mock.KubeApplier(testMgmt).ReadDesires(parent)
	if err != nil {
		t.Fatalf("ReadDesires(parent): %v", err)
	}
	if _, err := crud.Create(ctx, desire, nil); err != nil {
		t.Fatalf("seed Create: %v", err)
	}

	c, err := NewReadDesireKubernetesController(key, target, dyn, mock.KubeApplier(testMgmt))
	if err != nil {
		t.Fatalf("NewReadDesireKubernetesController: %v", err)
	}
	// Replace the writer with a recorder so tests can assert on status updates
	// without exercising the full desirestatuswriter -> CRUD chain.
	w := &recordingWriter{desire: desire}
	c.writer = w

	// Run the per-instance informer just long enough for it to sync against the
	// manifestclient-backed list. The test's parent ctx will cancel everything.
	go c.informer.RunWithContext(ctx)
	syncCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !cache.WaitForCacheSync(syncCtx.Done(), c.informer.HasSynced) {
		t.Fatal("informer did not sync within 5s")
	}
	return c, w
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
	cond := findCond(last.Status.Conditions, kubeapplier.ConditionTypeSuccessful)
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
	cond := findCond(last.Status.Conditions, kubeapplier.ConditionTypeSuccessful)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Errorf("Successful=%v, want True", cond)
	}
}

// TestNewReadDesireKubernetesController_RejectsIncompleteTarget exercises the
// pre-flight validation in the constructor: missing version, resource, or name
// returns a *PreCheckError without touching the dynamic client.
func TestNewReadDesireKubernetesController_RejectsIncompleteTarget(t *testing.T) {
	cases := []struct {
		name   string
		target kubeapplier.ResourceReference
	}{
		{
			name:   "missing version",
			target: kubeapplier.ResourceReference{Resource: "configmaps", Name: "x"},
		},
		{
			name:   "missing resource",
			target: kubeapplier.ResourceReference{Version: "v1", Name: "x"},
		},
		{
			name:   "missing name",
			target: kubeapplier.ResourceReference{Version: "v1", Resource: "configmaps"},
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

func findCond(conds []metav1.Condition, t string) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == t {
			return &conds[i]
		}
	}
	return nil
}
