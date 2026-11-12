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
	"fmt"
	"slices"
	"sync"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	snapshotclientset "github.com/kubernetes-csi/external-snapshotter/client/v8/clientset/versioned"
	snapshotinformers "github.com/kubernetes-csi/external-snapshotter/client/v8/informers/externalversions/volumesnapshot/v1"
	snapshotlisters "github.com/kubernetes-csi/external-snapshotter/client/v8/listers/volumesnapshot/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	SnapshotPVCFinalizerCleanupControllerName = "snapshot-pvc-finalizer-cleanup"

	// snapshotPVCSourceProtectionFinalizer is the snapshot controller's hold on a
	// PVC that a VolumeSnapshot is reading as its source. The external-snapshotter
	// client module does not export it, so it is reproduced here verbatim.
	snapshotPVCSourceProtectionFinalizer = "snapshot.storage.kubernetes.io/pvc-as-source-protection"

	// pvcDeletionGracePeriod is how long a PVC must have been marked for deletion
	// while holding the snapshot finalizer before this controller considers
	// removing it. Legitimate teardown releases the claim in well under a second, so this is
	// orders of magnitude above the normal path and leaves ample room for skew
	// between the apiserver that stamps deletionTimestamp and this process.
	pvcDeletionGracePeriod = 1 * time.Minute
)

// SnapshotPVCFinalizerCleanupController releases PVCs held by the snapshot
// source-protection finalizer when their VolumeSnapshots no longer exist.
type SnapshotPVCFinalizerCleanupController struct {
	name                 string
	kubeClient           kubernetes.Interface
	snapshotClient       snapshotclientset.Interface
	pvcLister            corelisters.PersistentVolumeClaimLister
	volumeSnapshotLister snapshotlisters.VolumeSnapshotLister
	hasSynced            []cache.InformerSynced
	workqueue            workqueue.TypedRateLimitingInterface[string]
	// clock reads the current time for grace-period arithmetic only. This
	// controller never schedules: delayed retries go through workqueue.AddAfter,
	// which carries its own clock.
	clock clock.PassiveClock
}

// NewSnapshotPVCFinalizerCleanupController creates a new SnapshotPVCFinalizerCleanupController.
func NewSnapshotPVCFinalizerCleanupController(
	kubeClient kubernetes.Interface,
	snapshotClient snapshotclientset.Interface,
	pvcInformer coreinformers.PersistentVolumeClaimInformer,
	volumeSnapshotInformer snapshotinformers.VolumeSnapshotInformer,
) (*SnapshotPVCFinalizerCleanupController, error) {
	c := &SnapshotPVCFinalizerCleanupController{
		name:                 SnapshotPVCFinalizerCleanupControllerName,
		kubeClient:           kubeClient,
		snapshotClient:       snapshotClient,
		pvcLister:            pvcInformer.Lister(),
		volumeSnapshotLister: volumeSnapshotInformer.Lister(),
		hasSynced: []cache.InformerSynced{
			pvcInformer.Informer().HasSynced,
			volumeSnapshotInformer.Informer().HasSynced,
		},
		workqueue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{Name: SnapshotPVCFinalizerCleanupControllerName},
		),
		clock: clock.RealClock{},
	}

	for _, registration := range []struct {
		informer cache.SharedIndexInformer
		handler  cache.ResourceEventHandlerFuncs
	}{
		// A PVC acquiring a deletionTimestamp alongside the finalizer starts the
		// grace period; the sync it triggers schedules its own re-check.
		{pvcInformer.Informer(), cache.ResourceEventHandlerFuncs{
			AddFunc:    c.enqueuePVC,
			UpdateFunc: func(_, newObj interface{}) { c.enqueuePVC(newObj) },
		}},
		// The snapshot delete that leaves the finalizer behind is exactly the
		// event the snapshot controller declines to act on, so re-queue here.
		{volumeSnapshotInformer.Informer(), cache.ResourceEventHandlerFuncs{
			DeleteFunc: c.enqueueSnapshotSourcePVC,
		}},
	} {
		if _, err := registration.informer.AddEventHandler(registration.handler); err != nil {
			c.workqueue.ShutDown()
			return nil, fmt.Errorf("failed to add event handler: %w", err)
		}
	}

	return c, nil
}

func (c *SnapshotPVCFinalizerCleanupController) enqueuePVC(obj interface{}) {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	pvc, ok := obj.(*corev1.PersistentVolumeClaim)
	if !ok || !wedgeCandidate(pvc) {
		return
	}
	key, err := cache.MetaNamespaceKeyFunc(pvc)
	if err != nil {
		return
	}

	c.workqueue.Add(key)
}

func (c *SnapshotPVCFinalizerCleanupController) enqueueSnapshotSourcePVC(obj interface{}) {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	snapshot, ok := obj.(*snapshotv1.VolumeSnapshot)
	if !ok || snapshot.Namespace == "" || snapshot.Spec.Source.PersistentVolumeClaimName == nil {
		return
	}
	c.workqueue.Add(snapshot.Namespace + "/" + *snapshot.Spec.Source.PersistentVolumeClaimName)
}

// wedgeCandidate reports whether a PVC is in the shape this controller acts on:
// marked for deletion and still holding the snapshot source-protection
// finalizer. It says nothing about the grace period or about whether any
// VolumeSnapshot still justifies the hold.
func wedgeCandidate(pvc *corev1.PersistentVolumeClaim) bool {
	return !pvc.DeletionTimestamp.IsZero() && slices.Contains(pvc.Finalizers, snapshotPVCSourceProtectionFinalizer)
}

func (c *SnapshotPVCFinalizerCleanupController) Run(ctx context.Context, workers int) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	ctx = utils.ContextWithControllerName(ctx, c.name)
	logger := klog.FromContext(ctx).WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx = klog.NewContext(ctx, logger)

	logger.Info("Starting snapshot PVC finalizer cleanup controller")
	logger.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(ctx.Done(), c.hasSynced...); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	logger.Info("Starting workers", "count", workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer utilruntime.HandleCrash()
			defer wg.Done()
			c.runWorker(ctx)
		}()
	}

	logger.Info("Snapshot PVC finalizer cleanup controller started")
	<-ctx.Done()
	logger.Info("Shutting down snapshot PVC finalizer cleanup controller")
	c.workqueue.ShutDown()
	wg.Wait()

	return nil
}

func (c *SnapshotPVCFinalizerCleanupController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *SnapshotPVCFinalizerCleanupController) processNextWorkItem(ctx context.Context) bool {
	key, shutdown := c.workqueue.Get()
	if shutdown {
		return false
	}
	defer c.workqueue.Done(key)

	ctx = utils.ContextWithLogger(ctx, utils.AddLoggerValues(utils.LoggerFromContext(ctx), key))

	requeueAfter, err := c.syncHandler(ctx, key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("error syncing PVC %q: %w", key, err))
		c.workqueue.AddRateLimited(key)
		return true
	}

	c.workqueue.Forget(key)
	// A candidate still inside its grace period has no event left to wait for:
	// it already holds its final shape, so only the clock can advance it.
	if requeueAfter > 0 {
		c.workqueue.AddAfter(key, requeueAfter)
	}
	return true
}

// syncHandler reconciles one deleting PVC. The returned duration asks for a
// re-check once that candidate leaves the grace period.
func (c *SnapshotPVCFinalizerCleanupController) syncHandler(ctx context.Context, key string) (time.Duration, error) {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return 0, fmt.Errorf("invalid key %q: %w", key, err)
	}

	cachedPVC, err := c.pvcLister.PersistentVolumeClaims(namespace).Get(name)
	if apierrors.IsNotFound(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get cached PVC %q: %w", key, err)
	}
	if !wedgeCandidate(cachedPVC) {
		return 0, nil
	}
	if remaining := c.gracePeriodRemaining(cachedPVC); remaining > 0 {
		return remaining, nil
	}

	return 0, c.reconcilePVC(ctx, cachedPVC)
}

func (c *SnapshotPVCFinalizerCleanupController) reconcilePVC(ctx context.Context, cachedPVC *corev1.PersistentVolumeClaim) error {
	blocked, err := c.cachedSnapshotBlocksRemoval(cachedPVC)
	if err != nil {
		return err
	}
	if blocked {
		return nil
	}

	snapshots, err := c.snapshotClient.SnapshotV1().VolumeSnapshots(cachedPVC.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list VolumeSnapshots in namespace %q: %w", cachedPVC.Namespace, err)
	}
	if snapshots.Continue != "" {
		return fmt.Errorf("incomplete VolumeSnapshot list in namespace %q", cachedPVC.Namespace)
	}

	for i := range snapshots.Items {
		sourcePVCName := snapshots.Items[i].Spec.Source.PersistentVolumeClaimName
		if sourcePVCName != nil && *sourcePVCName == cachedPVC.Name {
			// A live snapshot still references the PVC, so keep the finalizer.
			return nil
		}
	}

	updated := cachedPVC.DeepCopy()
	updated.Finalizers = slices.DeleteFunc(updated.Finalizers, func(finalizer string) bool {
		return finalizer == snapshotPVCSourceProtectionFinalizer
	})
	if err := c.updatePVC(ctx, updated); err != nil {
		return fmt.Errorf("failed to remove snapshot finalizer from PVC %s/%s: %w", cachedPVC.Namespace, cachedPVC.Name, err)
	}

	klog.FromContext(ctx).Info("Removed stale snapshot source-protection finalizer from PVC", "namespace", cachedPVC.Namespace, "name", cachedPVC.Name)
	return nil
}

func (c *SnapshotPVCFinalizerCleanupController) cachedSnapshotBlocksRemoval(pvc *corev1.PersistentVolumeClaim) (bool, error) {
	cachedSnapshots, err := c.volumeSnapshotLister.VolumeSnapshots(pvc.Namespace).List(labels.Everything())
	if err != nil {
		return false, fmt.Errorf("failed to list cached VolumeSnapshots in namespace %q: %w", pvc.Namespace, err)
	}

	for _, cachedSnapshot := range cachedSnapshots {
		sourcePVCName := cachedSnapshot.Spec.Source.PersistentVolumeClaimName
		if sourcePVCName != nil && *sourcePVCName == pvc.Name {
			return true, nil
		}
	}

	return false, nil
}

func (c *SnapshotPVCFinalizerCleanupController) updatePVC(ctx context.Context, pvc *corev1.PersistentVolumeClaim) error {
	_, err := c.kubeClient.CoreV1().PersistentVolumeClaims(pvc.Namespace).Update(ctx, pvc, metav1.UpdateOptions{})
	return err
}

func (c *SnapshotPVCFinalizerCleanupController) gracePeriodRemaining(pvc *corev1.PersistentVolumeClaim) time.Duration {
	return max(pvcDeletionGracePeriod-c.clock.Now().Sub(pvc.DeletionTimestamp.Time), 0)
}
