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

package read_desire_manager

import (
	"context"
	"strings"
	"sync"
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/desirestatuswriter"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/keys"
)

const (
	testSub     = "00000000-0000-0000-0000-000000000001"
	testRG      = "rg"
	testCluster = "c"
	testDesire  = "d"
)

// testManagementID is the resourceID stamped into Spec.ManagementCluster;
// testManagement is the lowercased-string form used as the Cosmos partition key.
var (
	testManagementID = api.Must(azcorearm.ParseResourceID(
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

func newReadDesire(t *testing.T, target kubeapplier.ResourceReference) *kubeapplier.ReadDesire {
	t.Helper()
	return &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: mustParseID(t, kubeapplier.ToClusterScopedReadDesireResourceIDString(
				testSub, testRG, testCluster, testDesire,
			)),
			PartitionKey: strings.ToLower(testManagementID.String()),
		},
		Spec: kubeapplier.ReadDesireSpec{
			ManagementCluster: testManagementID,
			TargetItem:        target,
		},
	}
}

// keyFor returns the typed key the controller uses for a given ReadDesire.
func keyFor(t *testing.T, d *kubeapplier.ReadDesire) keys.ReadDesireKey {
	t.Helper()
	k, err := keys.ReadDesireKeyFromResourceID(d.GetResourceID())
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}
	return k
}

// fakePerInstance is a stand-in for ReadDesireKubernetesController that
// records its lifecycle so the manager test can assert on start/stop ordering.
type fakePerInstance struct {
	target  kubeapplier.ResourceReference
	mu      sync.Mutex
	running bool
	started chan struct{}
	stopped chan struct{}
}

func newFakePerInstance(t kubeapplier.ResourceReference) *fakePerInstance {
	return &fakePerInstance{
		target:  t,
		started: make(chan struct{}),
		stopped: make(chan struct{}),
	}
}

func (f *fakePerInstance) Run(ctx context.Context) {
	f.mu.Lock()
	f.running = true
	close(f.started)
	f.mu.Unlock()
	<-ctx.Done()
	f.mu.Lock()
	f.running = false
	close(f.stopped)
	f.mu.Unlock()
}

func (f *fakePerInstance) IsRunning() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}

// newTestController builds a manager that pulls desires from the supplied
// MockKubeApplierDBClient and uses a recording fake-factory, so lifecycle
// tests can run without spinning up real reflectors or workqueues.
func newTestController(
	mock *databasetesting.MockKubeApplierDBClient,
	fakes *[]*fakePerInstance,
) *ReadDesireInformerManagingController {
	return &ReadDesireInformerManagingController{
		fetcher: &readDesireFetcher{crudByParent: mock},
		factory: &recordingFakeFactory{fakes: fakes},
		running: map[keys.ReadDesireKey]*runningInstance{},
		writer:  noopStatusWriter[kubeapplier.ReadDesire, keys.ReadDesireKey]{},
	}
}

// loadDesires inserts the provided ReadDesires into the mock under the
// canonical (subscriptionID, resourceGroup, cluster) parent. The test desires
// all share the same parent in this file's fixtures.
func loadDesires(t *testing.T, mock *databasetesting.MockKubeApplierDBClient, ds ...*kubeapplier.ReadDesire) {
	t.Helper()
	crud, err := mock.ReadDesiresForCluster(testSub, testRG, testCluster)
	if err != nil {
		t.Fatalf("ReadDesiresForCluster: %v", err)
	}
	for _, d := range ds {
		if _, err := crud.Create(context.Background(), d, nil); err != nil {
			t.Fatalf("Create %s: %v", d.GetResourceID(), err)
		}
	}
}

// deleteDesire removes a ReadDesire from the mock store.
func deleteDesire(t *testing.T, mock *databasetesting.MockKubeApplierDBClient, d *kubeapplier.ReadDesire) {
	t.Helper()
	crud, err := mock.ReadDesiresForCluster(testSub, testRG, testCluster)
	if err != nil {
		t.Fatalf("ReadDesiresForCluster: %v", err)
	}
	if err := crud.Delete(context.Background(), d.GetResourceID().Name); err != nil {
		t.Fatalf("Delete %s: %v", d.GetResourceID(), err)
	}
}

// replaceDesire swaps a ReadDesire's TargetItem in the mock store.
func replaceDesire(t *testing.T, mock *databasetesting.MockKubeApplierDBClient, d *kubeapplier.ReadDesire) {
	t.Helper()
	crud, err := mock.ReadDesiresForCluster(testSub, testRG, testCluster)
	if err != nil {
		t.Fatalf("ReadDesiresForCluster: %v", err)
	}
	// Refresh the desire's etag from the live store so Replace is accepted.
	live, err := crud.Get(context.Background(), d.GetResourceID().Name)
	if err != nil {
		t.Fatalf("Get %s: %v", d.GetResourceID(), err)
	}
	d.CosmosETag = live.CosmosETag
	if _, err := crud.Replace(context.Background(), d, nil); err != nil {
		t.Fatalf("Replace %s: %v", d.GetResourceID(), err)
	}
}

// recordingFakeFactory is the test PerInstanceFactory: it constructs a
// fakePerInstance per Build call and appends it to the test's slice so
// lifecycle assertions can find it by index.
type recordingFakeFactory struct {
	fakes *[]*fakePerInstance
}

func (f *recordingFakeFactory) Build(
	_ keys.ReadDesireKey, target kubeapplier.ResourceReference,
) (PerInstanceController, error) {
	fake := newFakePerInstance(target)
	*f.fakes = append(*f.fakes, fake)
	return fake, nil
}

func TestManagerSyncOnce_LaunchesPerInstanceController(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	target := kubeapplier.ResourceReference{Resource: "configmaps", Namespace: "default", Name: "x"}
	desire := newReadDesire(t, target)
	mock := databasetesting.NewMockKubeApplierDBClient()
	loadDesires(t, mock, desire)
	var fakes []*fakePerInstance
	c := newTestController(mock, &fakes)
	key := keyFor(t, desire)

	if err := c.SyncOnce(ctx, key); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if len(fakes) != 1 {
		t.Fatalf("factory called %d times, want 1", len(fakes))
	}
	<-fakes[0].started
	if !c.Running(key) {
		t.Errorf("manager.Running(%v) = false, want true", key)
	}
	if !fakes[0].IsRunning() {
		t.Errorf("per-instance not running")
	}
}

func TestManagerSyncOnce_RestartsOnTargetChange(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	t1 := kubeapplier.ResourceReference{Resource: "configmaps", Namespace: "default", Name: "x"}
	t2 := kubeapplier.ResourceReference{Resource: "configmaps", Namespace: "default", Name: "y"}
	desire := newReadDesire(t, t1)
	mock := databasetesting.NewMockKubeApplierDBClient()
	loadDesires(t, mock, desire)
	var fakes []*fakePerInstance
	c := newTestController(mock, &fakes)
	key := keyFor(t, desire)

	if err := c.SyncOnce(ctx, key); err != nil {
		t.Fatalf("first SyncOnce: %v", err)
	}
	<-fakes[0].started

	// Mutate the desire's TargetItem in the store and resync.
	desire.Spec.TargetItem = t2
	replaceDesire(t, mock, desire)
	if err := c.SyncOnce(ctx, key); err != nil {
		t.Fatalf("second SyncOnce: %v", err)
	}

	<-fakes[0].stopped
	if fakes[0].IsRunning() {
		t.Errorf("first fake should have stopped on target change")
	}
	if len(fakes) != 2 {
		t.Fatalf("expected 2 factory calls (start, restart), got %d", len(fakes))
	}
	<-fakes[1].started
	if fakes[1].target != t2 {
		t.Errorf("second factory got target %v, want %v", fakes[1].target, t2)
	}
}

func TestManagerSyncOnce_NoOpWhenTargetUnchanged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	target := kubeapplier.ResourceReference{Resource: "configmaps", Namespace: "default", Name: "x"}
	desire := newReadDesire(t, target)
	mock := databasetesting.NewMockKubeApplierDBClient()
	loadDesires(t, mock, desire)
	var fakes []*fakePerInstance
	c := newTestController(mock, &fakes)
	key := keyFor(t, desire)

	if err := c.SyncOnce(ctx, key); err != nil {
		t.Fatalf("first SyncOnce: %v", err)
	}
	if err := c.SyncOnce(ctx, key); err != nil {
		t.Fatalf("second SyncOnce: %v", err)
	}
	if got, want := len(fakes), 1; got != want {
		t.Errorf("factory called %d times, want %d (unchanged target)", got, want)
	}
}

func TestManagerSyncOnce_StopsOnDelete(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	target := kubeapplier.ResourceReference{Resource: "configmaps", Namespace: "default", Name: "x"}
	desire := newReadDesire(t, target)
	mock := databasetesting.NewMockKubeApplierDBClient()
	loadDesires(t, mock, desire)
	var fakes []*fakePerInstance
	c := newTestController(mock, &fakes)
	key := keyFor(t, desire)

	if err := c.SyncOnce(ctx, key); err != nil {
		t.Fatalf("first SyncOnce: %v", err)
	}
	<-fakes[0].started

	// Remove the desire from the store and resync.
	deleteDesire(t, mock, desire)
	if err := c.SyncOnce(ctx, key); err != nil {
		t.Fatalf("second SyncOnce: %v", err)
	}
	<-fakes[0].stopped
	if c.Running(key) {
		t.Errorf("manager.Running(%v) = true after delete; want false", key)
	}
}

// noopStatusWriter discards mutations. Status conditions aren't what these
// lifecycle tests are checking; we get full coverage of those in the
// conditions package's own tests.
type noopStatusWriter[T any, K comparable] struct{}

func (noopStatusWriter[T, K]) UpdateStatus(ctx context.Context, key K, mutate desirestatuswriter.MutateFunc[T]) error {
	return nil
}
