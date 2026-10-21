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

package snapshotpvcfinalizercleanup

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/go-logr/logr"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	snapshotfake "github.com/kubernetes-csi/external-snapshotter/client/v8/clientset/versioned/fake"
	snapshotinformers "github.com/kubernetes-csi/external-snapshotter/client/v8/informers/externalversions"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kubeinformers "k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// Use real-looking HCP namespaces to exercise same-named PVCs in separate namespaces.
const (
	testCPNamespace = "ocm-arohcpci01-2t0a4erurouv88b1rf2h4lagi5f4egc1-g9n2f0c3o7n7b9s"

	// A second HCP on the same management cluster, which also has data-etcd-0/1/2.
	otherCPNamespace = "ocm-arohcpci01-2t0a4fuidh5b039vls64juui3rgosnua-x7g1l9m1p3x3x9r"

	pvcProtectionFinalizer = "kubernetes.io/pvc-protection"
)

var testNow = time.Date(2026, 9, 22, 22, 6, 43, 0, time.UTC)

type testFixture struct {
	t              *testing.T
	controller     *SnapshotPVCFinalizerCleanupController
	kubeClient     *kubefake.Clientset
	snapshotClient *snapshotfake.Clientset
	pvcStore       cache.Store
	snapshotStore  cache.Store
	clock          *clocktesting.FakePassiveClock
}

func newTestFixture(t *testing.T) *testFixture {
	t.Helper()

	kubeClient := kubefake.NewClientset()
	snapshotClient := snapshotfake.NewSimpleClientset()

	kubeInformers := kubeinformers.NewSharedInformerFactory(kubeClient, 0)
	snapshotInformerFactory := snapshotinformers.NewSharedInformerFactory(snapshotClient, 0)

	pvcInformer := kubeInformers.Core().V1().PersistentVolumeClaims()
	snapshotInformer := snapshotInformerFactory.Snapshot().V1().VolumeSnapshots()

	c, err := NewSnapshotPVCFinalizerCleanupController(kubeClient, snapshotClient, pvcInformer, snapshotInformer)
	if err != nil {
		t.Fatalf("NewSnapshotPVCFinalizerCleanupController() error = %v", err)
	}
	t.Cleanup(c.workqueue.ShutDown)
	if len(c.hasSynced) != 2 {
		t.Fatalf("expected only PVC and VolumeSnapshot cache syncs, got %d", len(c.hasSynced))
	}

	fakeClock := clocktesting.NewFakePassiveClock(testNow)
	c.clock = fakeClock

	// Tracked calls made while seeding are not part of what a test asserts on.
	kubeClient.ClearActions()
	snapshotClient.ClearActions()

	return &testFixture{
		t:              t,
		controller:     c,
		kubeClient:     kubeClient,
		snapshotClient: snapshotClient,
		pvcStore:       pvcInformer.Informer().GetStore(),
		snapshotStore:  snapshotInformer.Informer().GetStore(),
		clock:          fakeClock,
	}
}

// seed puts an object in both the API and the matching informer cache, which is
// the steady state. Tests that care about staleness diverge the two explicitly
// with addToAPI / addToCache / deleteFromAPI.
func (f *testFixture) seed(objects ...runtime.Object) {
	f.t.Helper()
	for _, obj := range objects {
		f.addToAPI(obj)
		f.addToCache(obj)
	}
	f.kubeClient.ClearActions()
	f.snapshotClient.ClearActions()
}

func (f *testFixture) addToAPI(obj runtime.Object) {
	f.t.Helper()
	var err error
	switch o := obj.(type) {
	case *corev1.Namespace:
		err = f.kubeClient.Tracker().Add(o)
	case *corev1.PersistentVolumeClaim:
		err = f.kubeClient.Tracker().Add(o)
	case *snapshotv1.VolumeSnapshot:
		err = f.snapshotClient.Tracker().Add(o)
	default:
		f.t.Fatalf("addToAPI: unsupported type %T", obj)
	}
	if err != nil {
		f.t.Fatalf("addToAPI: %v", err)
	}
}

func (f *testFixture) addToCache(obj runtime.Object) {
	f.t.Helper()
	var err error
	switch o := obj.(type) {
	case *corev1.PersistentVolumeClaim:
		err = f.pvcStore.Add(o)
	case *snapshotv1.VolumeSnapshot:
		err = f.snapshotStore.Add(o)
	default:
		f.t.Fatalf("addToCache: unsupported type %T", obj)
	}
	if err != nil {
		f.t.Fatalf("addToCache: %v", err)
	}
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	return utils.ContextWithLogger(t.Context(), logr.Discard())
}

func controlPlaneNamespace(name string, deletedAt *time.Time) *corev1.Namespace {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"hypershift.openshift.io/hosted-control-plane": "true",
			},
		},
	}
	if deletedAt != nil {
		ts := metav1.NewTime(*deletedAt)
		namespace.DeletionTimestamp = &ts
		namespace.Spec.Finalizers = []corev1.FinalizerName{corev1.FinalizerKubernetes}
	}
	return namespace
}

// pvc builds a PVC with an explicit finalizer set. deletedAt nil means a live PVC.
func pvc(namespace, name string, deletedAt *time.Time, finalizers ...string) *corev1.PersistentVolumeClaim {
	claim := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       namespace,
			Name:            name,
			UID:             types.UID(namespace + "/" + name),
			ResourceVersion: "1",
			Finalizers:      append([]string{}, finalizers...),
		},
	}
	if deletedAt != nil {
		ts := metav1.NewTime(*deletedAt)
		claim.DeletionTimestamp = &ts
	}
	return claim
}

// wedged is the shape this controller exists for: deleting, and holding the
// snapshot finalizer behind the ordinary pvc-protection finalizer.
func wedged(namespace, name string, deletedAt time.Time) *corev1.PersistentVolumeClaim {
	return pvc(namespace, name, &deletedAt, pvcProtectionFinalizer, snapshotPVCSourceProtectionFinalizer)
}

func snapshotFor(namespace, name, pvcName string) *snapshotv1.VolumeSnapshot {
	return &snapshotv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			UID:       types.UID(namespace + "/" + name),
		},
		Spec: snapshotv1.VolumeSnapshotSpec{
			Source: snapshotv1.VolumeSnapshotSource{PersistentVolumeClaimName: &pvcName},
		},
	}
}

// snapshotFromContent is a pre-provisioned snapshot: it has no PVC source at all
// and therefore cannot justify a pvc-as-source-protection finalizer on anything.
func snapshotFromContent(namespace, name, contentName string) *snapshotv1.VolumeSnapshot {
	return &snapshotv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: snapshotv1.VolumeSnapshotSpec{
			Source: snapshotv1.VolumeSnapshotSource{VolumeSnapshotContentName: &contentName},
		},
	}
}

func ago(d time.Duration) time.Time { return testNow.Add(-d) }

func ptr(t time.Time) *time.Time { return &t }

// updatedPVCs retains each written object so assertions can check both
// finalizers and the resourceVersion used for the update.
func (f *testFixture) updatedPVCs() map[string]*corev1.PersistentVolumeClaim {
	f.t.Helper()
	result := map[string]*corev1.PersistentVolumeClaim{}
	for _, action := range f.kubeClient.Actions() {
		update, ok := action.(clienttesting.UpdateAction)
		if !ok || action.GetVerb() != "update" {
			continue
		}
		claim, ok := update.GetObject().(*corev1.PersistentVolumeClaim)
		if !ok {
			f.t.Fatalf("update of unexpected object: %#v", update.GetObject())
		}
		if update.GetSubresource() != "" {
			f.t.Fatalf("finalizers must be written on the main resource, not %q", update.GetSubresource())
		}
		key := claim.Namespace + "/" + claim.Name
		if _, exists := result[key]; exists {
			f.t.Fatalf("PVC %s was updated more than once", key)
		}
		result[key] = claim
	}
	return result
}

func (f *testFixture) assertNoWrites() {
	f.t.Helper()
	for _, action := range append(f.kubeClient.Actions(), f.snapshotClient.Actions()...) {
		switch action.GetVerb() {
		case "get", "list", "watch":
		default:
			f.t.Fatalf("unexpected mutation: %#v", action)
		}
	}
}

func (f *testFixture) assertNoAPITraffic() {
	f.t.Helper()
	if actions := append(f.kubeClient.Actions(), f.snapshotClient.Actions()...); len(actions) != 0 {
		f.t.Fatalf("expected no API traffic, got %#v", actions)
	}
}

func TestSyncHandler(t *testing.T) {
	tests := []struct {
		name      string
		namespace *corev1.Namespace
		pvcs      []*corev1.PersistentVolumeClaim
		snapshots []*snapshotv1.VolumeSnapshot
		// setup runs after seeding, to diverge cache from API or install reactors.
		setup func(f *testFixture)
		key   string

		// wantFinalizers maps namespace/name to the finalizers expected on the
		// written object. A PVC absent from this map must not be written at all.
		wantFinalizers      map[string][]string
		wantRequeueAfter    time.Duration
		wantErr             bool
		wantErrorReason     metav1.StatusReason
		wantNoAPITraffic    bool
		wantResourceVersion string
		wantActions         []string
	}{
		{
			name: "a wedged PVC is released without a Namespace object",
			pvcs: []*corev1.PersistentVolumeClaim{wedged(testCPNamespace, "data-etcd-1", ago(39*time.Minute))},
			wantFinalizers: map[string][]string{
				testCPNamespace + "/data-etcd-1": {pvcProtectionFinalizer},
			},
			wantActions: []string{
				"list volumesnapshots " + testCPNamespace + "/",
				"update persistentvolumeclaims " + testCPNamespace + "/data-etcd-1",
			},
		},
		{
			name: "a stale cached resourceVersion causes an update conflict",
			setup: func(f *testFixture) {
				cached := wedged(testCPNamespace, "data-etcd-1", ago(39*time.Minute))
				cached.ResourceVersion = "1"
				f.addToCache(cached)

				live := wedged(testCPNamespace, "data-etcd-1", ago(39*time.Minute))
				live.ResourceVersion = "99"
				f.addToAPI(live)
				// The fake client does not enforce resourceVersion preconditions.
				f.kubeClient.PrependReactor("update", "persistentvolumeclaims", func(action clienttesting.Action) (bool, runtime.Object, error) {
					updated := action.(clienttesting.UpdateAction).GetObject().(*corev1.PersistentVolumeClaim)
					if updated.ResourceVersion != live.ResourceVersion {
						return true, nil, apierrors.NewConflict(corev1.Resource("persistentvolumeclaims"), updated.Name, errors.New("resource version changed"))
					}
					return false, nil, nil
				})
			},
			wantFinalizers: map[string][]string{
				testCPNamespace + "/data-etcd-1": {pvcProtectionFinalizer},
			},
			wantResourceVersion: "1",
			wantErr:             true,
			wantErrorReason:     metav1.StatusReasonConflict,
		},
		{
			name:           "a live VolumeSnapshot naming the PVC blocks removal",
			pvcs:           []*corev1.PersistentVolumeClaim{wedged(testCPNamespace, "data-etcd-1", ago(39*time.Minute))},
			snapshots:      []*snapshotv1.VolumeSnapshot{snapshotFor(testCPNamespace, "velero-data-etcd-1-h24zs", "data-etcd-1")},
			wantFinalizers: nil,
		},
		{
			// The dangerous direction: our cache is missing a snapshot that really
			// exists. Deciding from the cache here would strip a live interlock,
			// so the live list is what decides.
			name: "a snapshot visible only to the API blocks removal",
			pvcs: []*corev1.PersistentVolumeClaim{wedged(testCPNamespace, "data-etcd-1", ago(39*time.Minute))},
			setup: func(f *testFixture) {
				f.addToAPI(snapshotFor(testCPNamespace, "velero-data-etcd-1-h24zs", "data-etcd-1"))
			},
			wantFinalizers: nil,
		},
		{
			// Cached presence conservatively blocks until the informer converges.
			// The resulting delete or relist event re-queues the source PVC.
			name: "a snapshot visible only to the informer cache blocks removal",
			pvcs: []*corev1.PersistentVolumeClaim{wedged(testCPNamespace, "data-etcd-1", ago(39*time.Minute))},
			setup: func(f *testFixture) {
				f.addToCache(snapshotFor(testCPNamespace, "velero-data-etcd-1-h24zs", "data-etcd-1"))
			},
			wantFinalizers: nil,
		},
		{
			name:           "a PVC inside the grace period is requeued, not released",
			pvcs:           []*corev1.PersistentVolumeClaim{wedged(testCPNamespace, "data-etcd-1", ago(30*time.Second))},
			wantFinalizers: nil,
			// Candidates inside the grace period are never read from the API; the
			// cache alone is enough to decide to wait.
			wantNoAPITraffic: true,
			wantRequeueAfter: 30 * time.Second,
		},
		{
			name: "a PVC is released at the one-minute grace-period boundary",
			pvcs: []*corev1.PersistentVolumeClaim{wedged(testCPNamespace, "data-etcd-1", ago(time.Minute))},
			wantFinalizers: map[string][]string{
				testCPNamespace + "/data-etcd-1": {pvcProtectionFinalizer},
			},
		},
		{
			name:             "a PVC that is not being deleted is never touched",
			pvcs:             []*corev1.PersistentVolumeClaim{pvc(testCPNamespace, "data-etcd-1", nil, pvcProtectionFinalizer, snapshotPVCSourceProtectionFinalizer)},
			wantFinalizers:   nil,
			wantNoAPITraffic: true,
		},
		{
			name:      "a deleting PVC in an active namespace is released",
			namespace: controlPlaneNamespace(testCPNamespace, nil),
			pvcs:      []*corev1.PersistentVolumeClaim{wedged(testCPNamespace, "data-etcd-1", ago(39*time.Minute))},
			wantFinalizers: map[string][]string{
				testCPNamespace + "/data-etcd-1": {pvcProtectionFinalizer},
			},
		},
		{
			name: "a deleting PVC in an unlabeled namespace is released",
			namespace: func() *corev1.Namespace {
				namespace := controlPlaneNamespace(testCPNamespace, ptr(ago(40*time.Minute)))
				delete(namespace.Labels, "hypershift.openshift.io/hosted-control-plane")
				return namespace
			}(),
			pvcs: []*corev1.PersistentVolumeClaim{wedged(testCPNamespace, "data-etcd-1", ago(39*time.Minute))},
			wantFinalizers: map[string][]string{
				testCPNamespace + "/data-etcd-1": {pvcProtectionFinalizer},
			},
		},
		{
			name: "a deleting PVC in a namespace with a false control-plane label is released",
			namespace: func() *corev1.Namespace {
				namespace := controlPlaneNamespace(testCPNamespace, ptr(ago(40*time.Minute)))
				namespace.Labels["hypershift.openshift.io/hosted-control-plane"] = "false"
				return namespace
			}(),
			pvcs: []*corev1.PersistentVolumeClaim{wedged(testCPNamespace, "data-etcd-1", ago(39*time.Minute))},
			wantFinalizers: map[string][]string{
				testCPNamespace + "/data-etcd-1": {pvcProtectionFinalizer},
			},
		},
		{
			// pvc-protection is kube-controller-manager's, held while a pod still
			// mounts the volume. It resolves on its own and is not ours to remove.
			name:             "a PVC holding only kubernetes.io/pvc-protection is out of scope",
			pvcs:             []*corev1.PersistentVolumeClaim{pvc(testCPNamespace, "data-etcd-1", ptr(ago(39*time.Minute)), pvcProtectionFinalizer)},
			wantFinalizers:   nil,
			wantNoAPITraffic: true,
		},
		{
			name: "removal strips only the snapshot finalizer, in place",
			pvcs: []*corev1.PersistentVolumeClaim{
				pvc(testCPNamespace, "data-etcd-1", ptr(ago(39*time.Minute)),
					"external-provisioner.volume.kubernetes.io/finalizer",
					snapshotPVCSourceProtectionFinalizer,
					pvcProtectionFinalizer),
			},
			wantFinalizers: map[string][]string{
				testCPNamespace + "/data-etcd-1": {
					"external-provisioner.volume.kubernetes.io/finalizer",
					pvcProtectionFinalizer,
				},
			},
		},
		{
			// Every HCP on a management cluster has data-etcd-0/1/2, so scope must
			// be the queued namespace/name and never the PVC name alone.
			name: "a same-named PVC belonging to another HCP is untouched",
			pvcs: []*corev1.PersistentVolumeClaim{
				wedged(testCPNamespace, "data-etcd-1", ago(39*time.Minute)),
				wedged(otherCPNamespace, "data-etcd-1", ago(39*time.Minute)),
			},
			wantFinalizers: map[string][]string{
				testCPNamespace + "/data-etcd-1": {pvcProtectionFinalizer},
			},
		},
		{
			name:      "a snapshot sourced from a different PVC does not protect this one",
			pvcs:      []*corev1.PersistentVolumeClaim{wedged(testCPNamespace, "data-etcd-1", ago(39*time.Minute))},
			snapshots: []*snapshotv1.VolumeSnapshot{snapshotFor(testCPNamespace, "velero-data-etcd-2-bnxz6", "data-etcd-2")},
			wantFinalizers: map[string][]string{
				testCPNamespace + "/data-etcd-1": {pvcProtectionFinalizer},
			},
		},
		{
			name:      "a pre-provisioned snapshot with no PVC source does not block",
			pvcs:      []*corev1.PersistentVolumeClaim{wedged(testCPNamespace, "data-etcd-1", ago(39*time.Minute))},
			snapshots: []*snapshotv1.VolumeSnapshot{snapshotFromContent(testCPNamespace, "restored", "snapcontent-abc")},
			wantFinalizers: map[string][]string{
				testCPNamespace + "/data-etcd-1": {pvcProtectionFinalizer},
			},
		},
		{
			// A snapshot that is itself terminating is still a snapshot. It may be
			// stalled for a real reason, and releasing the source under it is exactly
			// the interlock violation this controller must not commit.
			name: "a referencing snapshot that is itself being deleted still blocks",
			pvcs: []*corev1.PersistentVolumeClaim{wedged(testCPNamespace, "data-etcd-1", ago(39*time.Minute))},
			setup: func(f *testFixture) {
				snapshot := snapshotFor(testCPNamespace, "velero-data-etcd-1-h24zs", "data-etcd-1")
				deletedAt := metav1.NewTime(ago(time.Minute))
				snapshot.DeletionTimestamp = &deletedAt
				snapshot.Finalizers = []string{"snapshot.storage.kubernetes.io/volumesnapshot-bound-protection"}
				f.seed(snapshot)
			},
			wantFinalizers: nil,
		},
		{
			// A ready snapshot would not block the upstream controller, but this controller
			// deliberately stays narrower than upstream: if the object exists, we defer.
			name: "a ReadyToUse referencing snapshot still blocks",
			pvcs: []*corev1.PersistentVolumeClaim{wedged(testCPNamespace, "data-etcd-1", ago(39*time.Minute))},
			setup: func(f *testFixture) {
				snapshot := snapshotFor(testCPNamespace, "velero-data-etcd-1-kpll7", "data-etcd-1")
				ready := true
				snapshot.Status = &snapshotv1.VolumeSnapshotStatus{ReadyToUse: &ready}
				f.seed(snapshot)
			},
			wantFinalizers: nil,
		},
		{
			name: "a failed VolumeSnapshot list fails the sync without writing",
			pvcs: []*corev1.PersistentVolumeClaim{wedged(testCPNamespace, "data-etcd-1", ago(39*time.Minute))},
			setup: func(f *testFixture) {
				f.snapshotClient.PrependReactor("list", "volumesnapshots", func(clienttesting.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("apiserver unavailable")
				})
			},
			wantFinalizers: nil,
			wantErr:        true,
		},
		{
			// A truncated list is not evidence of absence.
			name: "a paginated VolumeSnapshot list fails closed",
			pvcs: []*corev1.PersistentVolumeClaim{wedged(testCPNamespace, "data-etcd-1", ago(39*time.Minute))},
			setup: func(f *testFixture) {
				f.snapshotClient.PrependReactor("list", "volumesnapshots", func(clienttesting.Action) (bool, runtime.Object, error) {
					return true, &snapshotv1.VolumeSnapshotList{ListMeta: metav1.ListMeta{Continue: "more"}}, nil
				})
			},
			wantFinalizers: nil,
			wantErr:        true,
		},
		{
			name: "a PVC already gone from the API returns an update error for retry",
			setup: func(f *testFixture) {
				claim := wedged(testCPNamespace, "data-etcd-1", ago(39*time.Minute))
				f.addToCache(claim)
			},
			wantFinalizers: map[string][]string{
				testCPNamespace + "/data-etcd-1": {pvcProtectionFinalizer},
			},
			wantErr:         true,
			wantErrorReason: metav1.StatusReasonNotFound,
		},
		{
			// The API rejects an update with a stale UID; the fake client does not.
			name: "a PVC recreated under the same name rejects a stale UID",
			setup: func(f *testFixture) {
				f.addToCache(wedged(testCPNamespace, "data-etcd-1", ago(39*time.Minute)))
				replacement := wedged(testCPNamespace, "data-etcd-1", ago(39*time.Minute))
				replacement.UID = types.UID("recreated")
				f.addToAPI(replacement)
				f.kubeClient.PrependReactor("update", "persistentvolumeclaims", func(action clienttesting.Action) (bool, runtime.Object, error) {
					updated := action.(clienttesting.UpdateAction).GetObject().(*corev1.PersistentVolumeClaim)
					if updated.UID != replacement.UID {
						return true, nil, apierrors.NewConflict(corev1.Resource("persistentvolumeclaims"), updated.Name, errors.New("UID changed"))
					}
					return false, nil, nil
				})
			},
			wantFinalizers: map[string][]string{
				testCPNamespace + "/data-etcd-1": {pvcProtectionFinalizer},
			},
			wantErr:         true,
			wantErrorReason: metav1.StatusReasonConflict,
		},
		{
			name: "a cache reflecting the snapshot controller's release writes nothing",
			setup: func(f *testFixture) {
				released := pvc(testCPNamespace, "data-etcd-1", ptr(ago(39*time.Minute)), pvcProtectionFinalizer)
				f.seed(released)
			},
			wantFinalizers:   nil,
			wantNoAPITraffic: true,
		},
		{
			name: "a conflicting update is surfaced for retry",
			pvcs: []*corev1.PersistentVolumeClaim{wedged(testCPNamespace, "data-etcd-1", ago(39*time.Minute))},
			setup: func(f *testFixture) {
				f.kubeClient.PrependReactor("update", "persistentvolumeclaims", func(clienttesting.Action) (bool, runtime.Object, error) {
					return true, nil, apierrors.NewConflict(corev1.Resource("persistentvolumeclaims"), "data-etcd-1", errors.New("resource version changed"))
				})
			},
			wantFinalizers: map[string][]string{
				testCPNamespace + "/data-etcd-1": {pvcProtectionFinalizer},
			},
			wantErr: true,
		},
		{
			name:             "a malformed workqueue key is a permanent error",
			key:              "a/b/c",
			wantErr:          true,
			wantNoAPITraffic: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newTestFixture(t)
			if tt.namespace != nil {
				f.addToAPI(tt.namespace)
			}
			for _, claim := range tt.pvcs {
				f.seed(claim)
			}
			for _, snapshot := range tt.snapshots {
				f.seed(snapshot)
			}
			if tt.setup != nil {
				tt.setup(f)
			}

			var actions []string
			if tt.wantActions != nil {
				record := func(action clienttesting.Action) (bool, runtime.Object, error) {
					name := ""
					switch a := action.(type) {
					case clienttesting.GetAction:
						name = a.GetName()
					case clienttesting.UpdateAction:
						obj, ok := a.GetObject().(metav1.Object)
						if !ok {
							t.Fatalf("update of unexpected object: %#v", a.GetObject())
						}
						name = obj.GetName()
					}
					actions = append(actions, action.GetVerb()+" "+action.GetResource().Resource+" "+action.GetNamespace()+"/"+name)
					return false, nil, nil
				}
				f.kubeClient.PrependReactor("*", "*", record)
				f.snapshotClient.PrependReactor("*", "*", record)
			}

			key := tt.key
			if key == "" {
				key = testCPNamespace + "/data-etcd-1"
			}

			requeueAfter, err := f.controller.syncHandler(testContext(t), key)
			if (err != nil) != tt.wantErr {
				t.Fatalf("syncHandler() error = %v, wantErr %t", err, tt.wantErr)
			}
			if tt.wantErrorReason != "" && apierrors.ReasonForError(err) != tt.wantErrorReason {
				t.Errorf("syncHandler() error reason = %v, want %v", apierrors.ReasonForError(err), tt.wantErrorReason)
			}
			if requeueAfter != tt.wantRequeueAfter {
				t.Errorf("syncHandler() requeueAfter = %v, want %v", requeueAfter, tt.wantRequeueAfter)
			}
			if tt.wantActions != nil && !slices.Equal(actions, tt.wantActions) {
				t.Errorf("API actions = %v, want %v", actions, tt.wantActions)
			}
			for _, action := range f.kubeClient.Actions() {
				if action.GetResource().Resource == "namespaces" {
					t.Errorf("unexpected Namespace API call: %#v", action)
				}
				if action.GetResource().Resource == "persistentvolumeclaims" && action.GetVerb() == "get" {
					t.Errorf("unexpected live PVC GET: %#v", action)
				}
			}

			got := f.updatedPVCs()
			if len(tt.wantFinalizers) == 0 {
				if len(got) != 0 {
					t.Fatalf("expected no PVC writes, got %v", got)
				}
			}
			for name, want := range tt.wantFinalizers {
				have, ok := got[name]
				if !ok {
					t.Errorf("PVC %s was not written; writes = %v", name, got)
					continue
				}
				if !slices.Equal(have.Finalizers, want) {
					t.Errorf("PVC %s finalizers = %v, want %v", name, have.Finalizers, want)
				}
				if tt.wantResourceVersion != "" && have.ResourceVersion != tt.wantResourceVersion {
					t.Errorf("PVC %s resourceVersion = %q, want %q", name, have.ResourceVersion, tt.wantResourceVersion)
				}
			}
			for name := range got {
				if _, ok := tt.wantFinalizers[name]; !ok {
					t.Errorf("unexpected write to PVC %s", name)
				}
			}

			if tt.wantNoAPITraffic {
				f.assertNoAPITraffic()
			}
			if len(tt.wantFinalizers) == 0 {
				f.assertNoWrites()
			}
		})
	}
}

func TestEnqueuePVC(t *testing.T) {
	tests := []struct {
		name string
		obj  interface{}
		want string
	}{
		{
			name: "a wedged PVC enqueues itself",
			obj:  wedged(testCPNamespace, "data-etcd-1", ago(time.Minute)),
			want: testCPNamespace + "/data-etcd-1",
		},
		{
			name: "a tombstoned wedged PVC enqueues itself",
			obj: cache.DeletedFinalStateUnknown{
				Key: testCPNamespace + "/data-etcd-1",
				Obj: wedged(testCPNamespace, "data-etcd-1", ago(time.Minute)),
			},
			want: testCPNamespace + "/data-etcd-1",
		},
		{
			name: "a deleting PVC enqueues itself in any namespace",
			obj:  wedged("ordinary", "claim", ago(time.Minute)),
			want: "ordinary/claim",
		},
		{
			name: "a live PVC is ignored",
			obj:  pvc(testCPNamespace, "data-etcd-1", nil, snapshotPVCSourceProtectionFinalizer),
		},
		{
			name: "a deleting PVC without the snapshot finalizer is ignored",
			obj:  pvc(testCPNamespace, "data-etcd-1", ptr(ago(time.Minute)), pvcProtectionFinalizer),
		},
		{name: "unexpected type is ignored", obj: &corev1.Pod{}},
		{name: "nil is ignored"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newTestFixture(t)
			f.controller.enqueuePVC(tt.obj)
			f.assertNoAPITraffic()
			if tt.want == "" {
				if got := f.controller.workqueue.Len(); got != 0 {
					t.Fatalf("workqueue length = %d, want 0", got)
				}
				return
			}
			got, shutdown := f.controller.workqueue.Get()
			if shutdown {
				t.Fatal("workqueue shut down before returning the queued PVC")
			}
			defer f.controller.workqueue.Done(got)
			if got != tt.want {
				t.Fatalf("workqueue key = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEnqueueSnapshotSourcePVC(t *testing.T) {
	tests := []struct {
		name string
		obj  interface{}
		want string
	}{
		{
			name: "a VolumeSnapshot deletion enqueues its source PVC",
			obj:  snapshotFor(testCPNamespace, "velero-data-etcd-1-h24zs", "data-etcd-1"),
			want: testCPNamespace + "/data-etcd-1",
		},
		{
			name: "a tombstoned VolumeSnapshot enqueues its source PVC",
			obj: cache.DeletedFinalStateUnknown{
				Key: testCPNamespace + "/velero-data-etcd-1-h24zs",
				Obj: snapshotFor(testCPNamespace, "velero-data-etcd-1-h24zs", "data-etcd-1"),
			},
			want: testCPNamespace + "/data-etcd-1",
		},
		{
			name: "a snapshot deleted in any namespace enqueues its source PVC",
			obj:  snapshotFor("ordinary", "snapshot", "claim"),
			want: "ordinary/claim",
		},
		{
			name: "a pre-provisioned VolumeSnapshot is ignored",
			obj:  snapshotFromContent(testCPNamespace, "restored", "snapcontent-abc"),
		},
		{name: "unexpected type is ignored", obj: &corev1.Pod{}},
		{name: "nil is ignored"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newTestFixture(t)
			f.controller.enqueueSnapshotSourcePVC(tt.obj)
			f.assertNoAPITraffic()
			if tt.want == "" {
				if got := f.controller.workqueue.Len(); got != 0 {
					t.Fatalf("workqueue length = %d, want 0", got)
				}
				return
			}
			got, shutdown := f.controller.workqueue.Get()
			if shutdown {
				t.Fatal("workqueue shut down before returning the source PVC")
			}
			defer f.controller.workqueue.Done(got)
			if got != tt.want {
				t.Fatalf("workqueue key = %q, want %q", got, tt.want)
			}
		})
	}
}
