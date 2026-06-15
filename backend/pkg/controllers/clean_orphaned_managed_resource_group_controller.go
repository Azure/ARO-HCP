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

package controllers

import (
	"context"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type cleanOrphanedManagedResourceGroup struct {
	name string

	subscriptionLister   listers.SubscriptionLister
	resourcesDBClient    database.ResourcesDBClient
	resourceGroupsClient azureclient.ResourceGroupsClient

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[string]
}

// NewCleanOrphanedManagedResourceGroupController periodically looks for managed resource groups
// that are not referenced by any HCPOpenShiftCluster in the database and deletes them.
func NewCleanOrphanedManagedResourceGroupController(
	subscriptionLister listers.SubscriptionLister,
	resourcesDBClient database.ResourcesDBClient,
	resourceGroupsClient azureclient.ResourceGroupsClient,
) controllerutils.Controller {
	c := &cleanOrphanedManagedResourceGroup{
		name:                 "CleanOrphanedManagedResourceGroup",
		subscriptionLister:   subscriptionLister,
		resourcesDBClient:    resourcesDBClient,
		resourceGroupsClient: resourceGroupsClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: "CleanOrphanedManagedResourceGroup",
			},
		),
	}

	return c
}

// SyncOnce implements the main sync logic for the controller.
func (c *cleanOrphanedManagedResourceGroup) SyncOnce(ctx context.Context, _ any) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("Syncing orphaned managed resource groups")

	// Get all subscriptions
	subscriptions, err := c.subscriptionLister.List(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	logger.Info("Retrieved subscriptions", "count", len(subscriptions))

	// TODO: Implement rest of the logic

	logger.Info("End of orphaned managed resource groups sync")
	return nil
}

func (c *cleanOrphanedManagedResourceGroup) Run(ctx context.Context, threadiness int) {
	// don't let panics crash the process
	defer utilruntime.HandleCrash()
	// make sure the work queue is shutdown which will trigger workers to end
	defer c.queue.ShutDown()

	ctx = utils.ContextWithControllerName(ctx, c.name)
	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting")

	// start up your worker threads based on threadiness.  Some controllers
	// have multiple kinds of workers
	for i := 0; i < threadiness; i++ {
		// runWorker will loop until "something bad" happens.  The .Until will
		// then rekick the worker after one second
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	// We run this periodically enqueuing an arbitrary item named "doWork" to trigger the sync.
	go wait.JitterUntilWithContext(ctx, func(ctx context.Context) { c.queue.Add("doWork") }, 10*time.Minute, 0.1, true)

	logger.Info("Started workers")

	// wait until we're told to stop
	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *cleanOrphanedManagedResourceGroup) runWorker(ctx context.Context) {
	defer utilruntime.HandleCrash()
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem deals with one item off the queue.  It returns false
// when it's time to quit.
func (c *cleanOrphanedManagedResourceGroup) processNextWorkItem(ctx context.Context) bool {
	ref, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(ref)

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
