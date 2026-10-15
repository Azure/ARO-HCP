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
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
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
	testManagementID = metadataapi.Must(azcorearm.ParseResourceID(
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
			ResourceID: mustParseID(t, kubeapplierapihelpers.ToClusterScopedReadDesireResourceIDString(
				testSub, testRG, testCluster, testDesire,
			)),
			PartitionKey: strings.ToLower(testManagementID.String()),
		},
		Spec: kubeapplierapi.ReadDesireSpec{
			ManagementCluster: testManagementID,
			TargetItem:        target,
		},
	}
}

// keyFor returns the typed key the controller uses for a given ReadDesire.
func keyFor(t *testing.T, d *kubeapplierapi.ReadDesire) keys.ReadDesireKey {
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
	target  kubeapplierapi.ResourceReference
	mu      sync.Mutex
	running bool
	started chan struct{}
	stopped chan struct{}
}

func newFakePerInstance(t kubeapplierapi.ResourceReference) *fakePerInstance {
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
	mock *kubeappliercosmosstoragetesting.MockKubeApplierDBClient,
	fakes *[]*fakePerInstance,
) *ReadDesireInformerManagingController {
	return &ReadDesireInformerManagingController{
		fetcher: &readDesireFetcher{crudByParent: mock},
		factory: &recordingFakeFactory{fakes: fakes},
		running: map[keys.ReadDesireKey]*runningInstance{},
		writer:  noopStatusWriter[kubeapplierapi.ReadDesire]{},
	}
}

// loadDesires inserts the provided ReadDesires into the mock under the
// canonical (subscriptionID, resourceGroup, cluster) parent. The test desires
// all share the same parent in this file's fixtures.
func loadDesires(t *testing.T, mock *kubeappliercosmosstoragetesting.MockKubeApplierDBClient, ds ...*kubeapplierapi.ReadDesire) {
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
func deleteDesire(t *testing.T, mock *kubeappliercosmosstoragetesting.MockKubeApplierDBClient, d *kubeapplierapi.ReadDesire) {
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
func replaceDesire(t *testing.T, mock *kubeappliercosmosstoragetesting.MockKubeApplierDBClient, d *kubeapplierapi.ReadDesire) {
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
	_ keys.ReadDesireKey, target kubeapplierapi.ResourceReference,
) (PerInstanceController, error) {
	fake := newFakePerInstance(target)
	*f.fakes = append(*f.fakes, fake)
	return fake, nil
}

func TestManagerSyncOnce_LaunchesPerInstanceController(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	target := kubeapplierapi.ResourceReference{Resource: "configmaps", Namespace: "default", Name: "x"}
	desire := newReadDesire(t, target)
	mock := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
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

	t1 := kubeapplierapi.ResourceReference{Resource: "configmaps", Namespace: "default", Name: "x"}
	t2 := kubeapplierapi.ResourceReference{Resource: "configmaps", Namespace: "default", Name: "y"}
	desire := newReadDesire(t, t1)
	mock := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
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

	target := kubeapplierapi.ResourceReference{Resource: "configmaps", Namespace: "default", Name: "x"}
	desire := newReadDesire(t, target)
	mock := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
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

	target := kubeapplierapi.ResourceReference{Resource: "configmaps", Namespace: "default", Name: "x"}
	desire := newReadDesire(t, target)
	mock := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
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
type noopStatusWriter[T any] struct{}

func (noopStatusWriter[T]) UpdateStatusFrom(context.Context, *T, desirestatuswriter.MutateFunc[T]) error {
	return nil
}

type retargetingFactoryStore struct {
	current *kubeapplierapi.ReadDesire
	updated *kubeapplierapi.ReadDesire
	targets []kubeapplierapi.ResourceReference
	fetches int
	writes  int
}

func (store *retargetingFactoryStore) Fetch(context.Context, keys.ReadDesireKey) (*kubeapplierapi.ReadDesire, error) {
	store.fetches++
	return store.current, nil
}

func (store *retargetingFactoryStore) Replace(_ context.Context, desired *kubeapplierapi.ReadDesire) error {
	if desired.CosmosETag != store.current.CosmosETag {
		return &azcore.ResponseError{StatusCode: http.StatusPreconditionFailed}
	}
	store.current = desired.DeepCopy()
	store.writes++
	return nil
}

func (store *retargetingFactoryStore) Build(_ keys.ReadDesireKey, target kubeapplierapi.ResourceReference) (PerInstanceController, error) {
	store.targets = append(store.targets, target)
	if len(store.targets) == 1 {
		store.current = store.updated
	}
	return nil, fmt.Errorf("cannot watch %s", target.Name)
}

func TestRetargetRejectsStaleConstructionErrorAndRetries(t *testing.T) {
	for _, worker := range []bool{false, true} {
		name := "SyncOnce"
		if worker {
			name = "processNext"
		}
		t.Run(name, func(t *testing.T) {
			observed := newReadDesire(t, kubeapplierapi.ResourceReference{Version: "v1", Resource: "configmaps", Name: "original"})
			observed.CosmosETag = "v1"
			original := observed.DeepCopy()
			updated := observed.DeepCopy()
			updated.CosmosETag = "v2"
			updated.Spec.TargetItem.Name = "updated"
			store := &retargetingFactoryStore{current: observed, updated: updated}
			controller := &ReadDesireInformerManagingController{
				fetcher: store, factory: store,
				writer: desirestatuswriter.New[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire](store),
				queue:  workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[keys.ReadDesireKey]()),
			}
			t.Cleanup(controller.queue.ShutDown)
			key := keyFor(t, observed)
			if worker {
				controller.queue.Add(key)
				if !controller.processNext(t.Context()) || controller.queue.NumRequeues(key) != 1 {
					t.Fatal("conflict did not requeue construction")
				}
			} else {
				err := controller.SyncOnce(t.Context(), key)
				var responseErr *azcore.ResponseError
				if !errors.As(err, &responseErr) || responseErr.StatusCode != http.StatusPreconditionFailed {
					t.Fatalf("SyncOnce error = %v, want PreconditionFailed (412)", err)
				}
				if _, ok := err.(*utils.LineTrackingError); !ok || errors.Unwrap(err) != responseErr || !strings.Contains(err.Error(), "desirestatuswriter.go:") {
					t.Fatalf("SyncOnce error = %v (%T), want tracked conflict from status writer", err, err)
				}
			}
			if store.fetches != 1 || store.writes != 0 || len(store.current.Status.Conditions) != 0 || !reflect.DeepEqual(observed, original) {
				t.Fatalf("stale construction error was published, mutated, or re-fetched: fetches=%d, writes=%d", store.fetches, store.writes)
			}
			if worker {
				deadline := time.Now().Add(5 * time.Second)
				for controller.queue.Len() == 0 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if controller.queue.Len() != 1 || !controller.processNext(t.Context()) || controller.queue.NumRequeues(key) != 0 {
					t.Fatal("retry did not finish after observing the retarget")
				}
			} else if err := controller.SyncOnce(t.Context(), key); err != nil {
				t.Fatal(err)
			}
			if store.fetches != 2 || store.writes != 1 || !reflect.DeepEqual(store.targets, []kubeapplierapi.ResourceReference{observed.Spec.TargetItem, updated.Spec.TargetItem}) {
				t.Fatalf("retry did not rebuild against current target: fetches=%d, writes=%d, targets=%v", store.fetches, store.writes, store.targets)
			}
			if got := store.current.Status.Conditions; len(got) != 1 || got[0].Status != metav1.ConditionFalse || got[0].Message != "cannot watch updated" {
				t.Fatalf("retry conditions = %v, want new target's construction error", got)
			}
		})
	}
}
