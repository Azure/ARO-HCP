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

package billingcontrollers

import (
	"context"
	"fmt"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type orphanedBillingCleanup struct {
	name string

	clock           utilsclock.PassiveClock
	clusterLister   listers.ClusterLister
	billingLister   listers.BillingLister
	billingDBClient database.BillingDBClient

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[string]
}

// NewOrphanedBillingCleanupController creates a controller that marks billing documents
// as deleted when their corresponding cluster no longer exists in Cosmos DB.
func NewOrphanedBillingCleanupController(clock utilsclock.PassiveClock, billingDBClient database.BillingDBClient, clusterLister listers.ClusterLister, billingLister listers.BillingLister) controllerutils.Controller {
	c := &orphanedBillingCleanup{
		name:            "OrphanedBillingCleanup",
		clock:           clock,
		clusterLister:   clusterLister,
		billingLister:   billingLister,
		billingDBClient: billingDBClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: "OrphanedBillingCleanup",
			},
		),
	}

	return c
}

func (c *orphanedBillingCleanup) synchronizeAllBillingDocs(ctx context.Context) error {
	logger := utils.LoggerFromContext(ctx)

	// Get all billing documents from the cached lister
	allBillingDocs, err := c.billingLister.List(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	for _, doc := range allBillingDocs {
		// Skip billing documents that are already marked as deleted
		if doc.DeletionTime != nil {
			continue
		}

		// Check if the cluster still exists
		resourceID := doc.ResourceID
		if resourceID == nil {
			logger.Info("billing document has nil resourceID",
				"billingDocID", doc.ID,
			)
			continue
		}

		clusterExists := true
		_, err := c.clusterLister.Get(ctx, resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name)
		if database.IsNotFoundError(err) {
			clusterExists = false
		} else if err != nil {
			return utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
		}

		if clusterExists {
			continue
		}

		deletionTime := c.clock.Now()
		var patchOperations database.BillingDocumentPatchOperations
		patchOperations.SetDeletionTime(deletionTime)
		err = c.billingDBClient.BillingDocs(doc.SubscriptionID).PatchByID(ctx, doc.ID, patchOperations)
		if err != nil {
			return utils.TrackError(err)
		}

		logger.Info("successfully marked orphaned billing document as deleted",
			"billingDocID", doc.ID,
			"deletionTime", deletionTime,
		)
	}

	return nil
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
