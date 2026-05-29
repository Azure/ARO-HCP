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

package kubeapplier

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/database/informers"
	"github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const controllerName = "union-kube-applier-informers-controller"

// ManagementClusterKey identifies one management cluster for the controller's
// workqueue. Today a stamp hosts a single management cluster (the "default"
// singleton, see fleet.ManagementClusterResourceName), so StampIdentifier
// alone is the identity in practice; ManagementClusterName is kept on the key
// because the cosmos resourceID encodes both segments and we don't want
// callers reaching into resourceID-string surgery. Together they reconstruct
// the full resourceID:
//
//	/providers/microsoft.redhatopenshift/stamps/{StampIdentifier}/managementClusters/{ManagementClusterName}
type ManagementClusterKey struct {
	StampIdentifier       string `json:"stampIdentifier"`
	ManagementClusterName string `json:"managementClusterName"`
}

// GetResourceID returns the management-cluster resourceID for this key.
func (k ManagementClusterKey) GetResourceID() *azcorearm.ResourceID {
	return api.Must(azcorearm.ParseResourceID(strings.ToLower(path.Join(
		"/providers", fleet.StampResourceType.String(), k.StampIdentifier,
		fleet.ManagementClusterResourceTypeName, k.ManagementClusterName,
	))))
}

// AddLoggerValues enriches logger with the standard resource-id key/value
// pairs derived from this management cluster's resourceID. Matches the
// shape of the other controller keys in backend/pkg/controllers/controllerutils
// (HCPClusterKey, HCPNodePoolKey, etc.).
func (k ManagementClusterKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues(
		utils.LogValues{}.
			AddLogValuesForResourceID(k.GetResourceID())...)
}

// PerMCKubeApplierInformerFactory builds a single management cluster's
// KubeApplierInformers on demand. The factory is decoupled from the
// controller so production wires it to a KubeApplierDBClients while tests
// can supply hand-rolled fakes.
//
// The returned KubeApplierInformers must be unstarted: the controller owns
// the lifecycle and will call RunWithContext on a child context. Returning
// nil signals "no per-MC informers available for this resourceID" — the
// controller silently skips this sync and waits for the next event for
// that MC.
type PerMCKubeApplierInformerFactory interface {
	NewKubeApplierInformers(ctx context.Context, managementClusterResourceID *azcorearm.ResourceID) informers.KubeApplierInformers
}

// UnionKubeApplierInformersController owns a UnionKubeApplierInformers and
// keeps it in sync with the set of management clusters reported by the
// configured management-cluster informer and lister.
//
// Event handlers enqueue a ManagementClusterKey onto an internal workqueue.
// Worker goroutines pull keys and call SyncOnce, which looks the management
// cluster up in the lister: if found, the worker ensures a per-MC
// sub-informer is registered with the union; if not found, the worker
// removes any existing registration. Lookup happens in SyncOnce, not in
// the event handler, so we always act on the current lister state rather
// than the stale snapshot embedded in the event payload.
//
// The controller does not own the management-cluster informer's lifecycle —
// the caller starts it. The controller installs an event handler in Run
// and removes it before returning.
type UnionKubeApplierInformersController struct {
	union      *UnionKubeApplierInformers
	mcInformer cache.SharedIndexInformer
	mcLister   listers.ManagementClusterLister
	factory    PerMCKubeApplierInformerFactory
	queue      workqueue.TypedRateLimitingInterface[ManagementClusterKey]

	mu   sync.Mutex
	subs map[string]*controllerSubEntry // key = lowercased(rid.String())
}

// controllerSubEntry tracks one per-MC informer the controller started so
// it can be cancelled and joined on remove or shutdown.
type controllerSubEntry struct {
	sub    informers.KubeApplierInformers
	cancel context.CancelFunc
	done   chan struct{}
}

// NewUnionKubeApplierInformersController returns a stopped controller. Call
// Run to start watching the management-cluster informer and wiring up
// per-MC sub-informers.
func NewUnionKubeApplierInformersController(
	mcInformer cache.SharedIndexInformer,
	mcLister listers.ManagementClusterLister,
	factory PerMCKubeApplierInformerFactory,
) *UnionKubeApplierInformersController {
	return &UnionKubeApplierInformersController{
		union:      NewUnionKubeApplierInformers(),
		mcInformer: mcInformer,
		mcLister:   mcLister,
		factory:    factory,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[ManagementClusterKey](),
			workqueue.TypedRateLimitingQueueConfig[ManagementClusterKey]{Name: controllerName},
		),
		subs: map[string]*controllerSubEntry{},
	}
}

// Union returns the managed UnionKubeApplierInformers. Callers wire their
// event handlers and listers from this — it is updated dynamically as MCs
// come and go.
func (c *UnionKubeApplierInformersController) Union() *UnionKubeApplierInformers {
	return c.union
}

// Run installs an event handler on the management-cluster informer, runs
// `threadiness` worker goroutines that process the workqueue, and blocks
// until ctx is cancelled. The caller is responsible for starting the
// management-cluster informer; Run only registers handlers on it.
//
// On exit Run shuts down the workqueue (which unblocks the workers),
// waits for the workers to stop, removes the event handler, and cancels
// every per-MC sub-informer the controller started.
func (c *UnionKubeApplierInformersController) Run(ctx context.Context, threadiness int) {
	defer utilruntime.HandleCrash()

	logger := utils.LoggerFromContext(ctx).WithName(controllerName)
	ctx = logr.NewContext(ctx, logger)

	reg, err := c.mcInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			if k, ok := managementClusterKeyFromEvent(obj); ok {
				c.queue.Add(k)
			}
		},
		UpdateFunc: func(_, newObj any) {
			if k, ok := managementClusterKeyFromEvent(newObj); ok {
				c.queue.Add(k)
			}
		},
		DeleteFunc: func(obj any) {
			if k, ok := managementClusterKeyFromEvent(obj); ok {
				c.queue.Add(k)
			}
		},
	})
	if err != nil {
		logger.Error(err, "failed to add event handler to management-cluster informer")
		return
	}

	var wg sync.WaitGroup
	for i := 0; i < threadiness; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wait.UntilWithContext(ctx, c.runWorker, time.Second)
		}()
	}

	<-ctx.Done()

	// Order matters: shut down the queue first so workers in
	// queue.Get() return, then join them, then drop the event handler
	// (so any late-firing handlers can no-op against the shut queue),
	// then cancel per-MC sub-informers.
	c.queue.ShutDown()
	wg.Wait()
	if rmErr := c.mcInformer.RemoveEventHandler(reg); rmErr != nil {
		logger.Error(rmErr, "failed to remove event handler from management-cluster informer")
	}
	c.shutdownSubs(logger)
}

func (c *UnionKubeApplierInformersController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *UnionKubeApplierInformersController) processNextWorkItem(ctx context.Context) bool {
	key, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(key)

	if err := c.SyncOnce(ctx, key); err != nil {
		utilruntime.HandleErrorWithContext(ctx, err, "failed to sync management cluster",
			"stampIdentifier", key.StampIdentifier,
			"managementClusterName", key.ManagementClusterName,
		)
		c.queue.AddRateLimited(key)
		return true
	}
	c.queue.Forget(key)
	return true
}

// SyncOnce reconciles one management cluster: it looks the cluster up in
// the lister by full resourceID match and either ensures a per-MC
// sub-informer is registered (if the MC is present) or removed (if the MC
// is absent). The lister is queried via List rather than Get because there
// is no canonical management-cluster name we can pass to Get.
func (c *UnionKubeApplierInformersController) SyncOnce(ctx context.Context, key ManagementClusterKey) error {
	wantID := key.GetResourceID()
	wantKey := strings.ToLower(wantID.String())

	mcs, err := c.mcLister.List(ctx)
	if err != nil {
		return fmt.Errorf("listing management clusters for key %v: %w", key, err)
	}

	var found *fleet.ManagementCluster
	for _, mc := range mcs {
		rid := managementClusterResourceID(mc)
		if rid != nil && strings.EqualFold(rid.String(), wantKey) {
			found = mc
			break
		}
	}

	if found == nil {
		c.ensureRemoved(ctx, wantID)
		return nil
	}
	rid := managementClusterResourceID(found)
	if rid == nil {
		return fmt.Errorf("management cluster %v has no resourceID", key)
	}
	return c.ensureAdded(ctx, rid)
}

// ensureAdded constructs and starts a per-MC sub-informer if the controller
// is not already tracking one for the given resourceID. Returns nil if the
// factory has nothing to give (e.g. the MC's container name isn't yet set
// in Status); the next event for this MC will re-enqueue.
//
// The whole method holds c.mu: the factory call, union.Add, and the
// goroutine launch are all fast (no I/O on a hot path), so the simpler
// single-critical-section shape avoids the two earlier check/check-again
// races at no real cost.
func (c *UnionKubeApplierInformersController) ensureAdded(ctx context.Context, rid *azcorearm.ResourceID) error {
	key := strings.ToLower(rid.String())
	logger := utils.LoggerFromContext(ctx)

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.subs[key]; exists {
		return nil
	}

	sub := c.factory.NewKubeApplierInformers(ctx, rid)
	if sub == nil {
		return nil
	}

	subCtx, cancel := context.WithCancel(ctx)
	entry := &controllerSubEntry{sub: sub, cancel: cancel, done: make(chan struct{})}

	if err := c.union.Add(rid, sub); err != nil {
		cancel()
		close(entry.done)
		return err
	}
	c.subs[key] = entry
	logger.Info("added per-MC sub-informer to union", "resourceID", rid)

	go func() {
		defer close(entry.done)
		sub.RunWithContext(subCtx)
	}()
	return nil
}

func (c *UnionKubeApplierInformersController) ensureRemoved(ctx context.Context, rid *azcorearm.ResourceID) {
	if rid == nil {
		return
	}
	key := strings.ToLower(rid.String())
	logger := utils.LoggerFromContext(ctx)

	c.mu.Lock()
	entry, ok := c.subs[key]
	if !ok {
		c.mu.Unlock()
		return
	}
	delete(c.subs, key)
	c.mu.Unlock()

	c.union.Remove(rid)
	entry.cancel()
	logger.Info("removed per-MC sub-informer from union; waiting for goroutine to stop", "resourceID", rid)
	<-entry.done
	logger.Info("per-MC sub-informer goroutine stopped", "resourceID", rid)
}

// shutdownSubs cancels every per-MC sub-informer started by the controller
// and waits for them all to stop. Safe to call once the workqueue has
// drained and the worker goroutines have exited.
func (c *UnionKubeApplierInformersController) shutdownSubs(logger logr.Logger) {
	c.mu.Lock()
	entries := make([]*controllerSubEntry, 0, len(c.subs))
	for _, e := range c.subs {
		entries = append(entries, e)
	}
	c.subs = map[string]*controllerSubEntry{}
	c.mu.Unlock()

	for _, e := range entries {
		e.cancel()
	}
	for _, e := range entries {
		<-e.done
	}
	if len(entries) > 0 {
		logger.Info("controller stopped", "subInformers", len(entries))
	}
}

// managementClusterFromEvent extracts a *fleet.ManagementCluster from an
// informer event payload, handling the DeletedFinalStateUnknown tombstone
// that the cache layer may deliver on delete.
func managementClusterFromEvent(obj any) *fleet.ManagementCluster {
	if mc, ok := obj.(*fleet.ManagementCluster); ok {
		return mc
	}
	if tomb, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		if mc, ok := tomb.Obj.(*fleet.ManagementCluster); ok {
			return mc
		}
	}
	return nil
}

// managementClusterResourceID returns the canonical resourceID for the
// management cluster, preferring the top-level ResourceID and falling
// back to the cosmos-metadata ResourceID. nil signals "no usable id".
func managementClusterResourceID(mc *fleet.ManagementCluster) *azcorearm.ResourceID {
	if mc == nil {
		return nil
	}
	if mc.ResourceID != nil {
		return mc.ResourceID
	}
	return mc.CosmosMetadata.ResourceID
}

// managementClusterKeyFromEvent computes the workqueue key for a
// management-cluster informer event. The key carries both the stamp
// identifier (the parent of an MC) and the management-cluster name; the
// actual MC payload is re-fetched from the lister inside SyncOnce.
func managementClusterKeyFromEvent(obj any) (ManagementClusterKey, bool) {
	mc := managementClusterFromEvent(obj)
	if mc == nil {
		return ManagementClusterKey{}, false
	}
	rid := managementClusterResourceID(mc)
	if rid == nil || rid.Parent == nil {
		return ManagementClusterKey{}, false
	}
	return ManagementClusterKey{
		StampIdentifier:       rid.Parent.Name,
		ManagementClusterName: rid.Name,
	}, true
}
