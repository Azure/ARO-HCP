// Copyright 2025 Microsoft Corporation
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

package billing

import (
	"context"
	"fmt"
	"strings"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	internalcontrollerutils "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type orphanedBillingCleanup struct {
	internalcontrollerutils.CacheSyncWaiter
	name string

	clock           utilsclock.PassiveClock
	clusterLister   corelisters.ClusterLister
	billingLister   corelisters.BillingLister
	billingDBClient billingcosmosstorage.BillingDBClient

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[string]
}

const OrphanedBillingCleanupControllerName = "OrphanedBillingCleanup"
const orphanBillingGracePeriod = 24 * time.Hour

// NewOrphanedBillingCleanupController creates a controller that marks billing documents
// as deleted after an unreferenced record remains a candidate for the grace period.
func NewOrphanedBillingCleanupController(clock utilsclock.PassiveClock, billingDBClient billingcosmosstorage.BillingDBClient, clusterLister corelisters.ClusterLister, billingLister corelisters.BillingLister) controllerutils.Controller {
	c := &orphanedBillingCleanup{
		name:            OrphanedBillingCleanupControllerName,
		clock:           clock,
		clusterLister:   clusterLister,
		billingLister:   billingLister,
		billingDBClient: billingDBClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: OrphanedBillingCleanupControllerName,
			},
		),
	}

	return c
}

func (c *orphanedBillingCleanup) synchronizeAllBillingDocs(ctx context.Context) error {
	// Get all billing documents from the cached lister
	allBillingDocs, err := c.billingLister.List(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	for _, doc := range allBillingDocs {
		if doc.DeletionTime != nil {
			continue
		}
		logger := utils.LoggerFromContext(ctx).WithValues("billingDocID", doc.ID, "resourceID", doc.ResourceID)
		resourceID := doc.ResourceID
		// Cluster lookup and billing writes must refer to the same subscription.
		if doc.ID == "" || resourceID == nil || !strings.EqualFold(doc.SubscriptionID, resourceID.SubscriptionID) {
			logger.Info("billing identity is unresolved; retaining record", "event", "billing_orphan_identity_unresolved", "reason", "InvalidBillingIdentity")
			continue
		}
		// A record is an orphan candidate when its cluster is absent from the
		// cache, or the cluster's UID and link consistently identify another billing document.
		orphan := false
		cluster, err := c.clusterLister.Get(ctx, resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name)
		if cosmosstorageutils.IsNotFoundError(err) {
			orphan = true
		} else if err != nil {
			return utils.TrackError(fmt.Errorf("look up cluster for billing document %q: %w", doc.ID, err))
		} else {
			clusterUID := cluster.ServiceProviderProperties.ClusterUID
			billingDocumentID := cluster.ServiceProviderProperties.BillingDocumentCosmosID
			clusterReferencesThisDocument := billingDocumentID == doc.ID
			if !clusterReferencesThisDocument {
				billingLinkMatchesClusterUID := clusterUID != "" && billingDocumentID == clusterUID
				// A consistent link to another document leaves this record unreferenced.
				if billingLinkMatchesClusterUID {
					orphan = true
				} else {
					// Creation writes billing before linking it. Missing or conflicting
					// identity is not enough evidence to stop billing this record.
					logger.Info("billing identity is unresolved; retaining record", "event", "billing_orphan_identity_unresolved", "reason", "UnresolvedClusterBillingIdentity")
				}
			}
		}
		now := c.clock.Now()
		var patch billingcosmosstorage.BillingDocumentPatchOperations
		var event string
		if !orphan {
			if doc.OrphanedSince == nil {
				continue
			}
			// Clear the timer when the cluster references this record or its linkage
			// is ambiguous, so a later orphan observation starts a fresh grace period.
			patch.SetOrphanedSince(nil)
			event = "billing_orphan_candidate_cleared"
		} else if doc.OrphanedSince == nil || doc.OrphanedSince.IsZero() {
			// A missing or zero timestamp cannot establish elapsed time. Start the
			// grace period now and persist it so restarts do not lose the observation.
			patch.SetOrphanedSince(&now)
			event = "billing_orphan_candidate_recorded"
		} else {
			// Keep the original observation while it still appears orphaned;
			// resetting it on every sweep would prevent the grace period expiring.
			if now.Before(doc.OrphanedSince.Add(orphanBillingGracePeriod)) {
				continue
			}
			// The grace period ages observations, not proven deletion. Keep the
			// existing cutoff policy: use confirmation time rather than backdating.
			patch.SetDeletionTime(now)
			event = "billing_orphan_cutoff_recorded"
		}
		if err := c.billingDBClient.BillingDocs(doc.SubscriptionID).PatchByID(ctx, doc.ID, patch); err != nil {
			if cosmosstorageutils.IsNotFoundError(err) {
				continue
			}
			return utils.TrackError(fmt.Errorf("update orphan state for billing document %q: %w", doc.ID, err))
		}
		logger.Info("updated billing orphan state", "event", event, "observedAt", now, "previousOrphanedSince", doc.OrphanedSince, "gracePeriod", orphanBillingGracePeriod)
	}
	return nil
}

func (c *orphanedBillingCleanup) QueueForInformers(resyncDuration time.Duration, notifiers ...controllerutils.Notifier) error {
	// panic so that the developer error is noticed immediately
	panic("not implemented")
}

func (c *orphanedBillingCleanup) SyncOnce(ctx context.Context, _ any) error {
	logger := utils.LoggerFromContext(ctx)

	syncErr := c.synchronizeAllBillingDocs(ctx)
	if syncErr != nil {
		logger.Error(syncErr, "unable to synchronize billing documents")
	}

	return utils.TrackError(syncErr)
}

func (c *orphanedBillingCleanup) Run(ctx context.Context, threadiness int) {
	// don't let panics crash the process
	defer utilruntime.HandleCrash()
	// make sure the work queue is shutdown which will trigger workers to end
	defer c.queue.ShutDown()

	if !c.WaitForCacheSync(ctx) {
		return
	}

	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting")

	// start up your worker threads based on threadiness.  Some controllers
	// have multiple kinds of workers
	for i := 0; i < threadiness; i++ {
		// runWorker will loop until "something bad" happens.  The .Until will
		// then rekick the worker after 10 minutes
		go wait.UntilWithContext(ctx, c.runWorker, 10*time.Minute)
	}

	// TODO before switching to a regular informer, build a basic LRU "don't fire unless your cooldown is over"
	go wait.JitterUntilWithContext(ctx, func(ctx context.Context) { c.queue.Add("default") }, 60*time.Minute, 0.1, true)

	logger.Info("Started workers")

	// wait until we're told to stop
	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *orphanedBillingCleanup) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem deals with one item off the queue.  It returns false
// when it's time to quit.
func (c *orphanedBillingCleanup) processNextWorkItem(ctx context.Context) bool {
	ref, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(ref)

	logger := utils.LoggerFromContext(ctx)
	ctx = utils.ContextWithLogger(ctx, logger)

	controllerutils.ReconcileTotal.WithLabelValues(c.name).Inc()
	err := c.SyncOnce(ctx, ref)
	if err == nil {
		c.queue.Forget(ref)
		return true
	}

	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", ref)
	c.queue.AddRateLimited(ref)

	return true
}
