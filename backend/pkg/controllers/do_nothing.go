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

package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type doNothingExample struct {
	name string

	subscriptionLister listers.SubscriptionLister
	cosmosClient       database.DBClient

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[controllerutils.HCPClusterKey]

	CreateAzureWidget func() (string, error)
}

// NewDoNothingExampleController periodically lists all clusters and for each out when the cluster was created and its state.
func NewDoNothingExampleController(cosmosClient database.DBClient, subscriptionLister listers.SubscriptionLister) controllerutils.Controller {
	c := &doNothingExample{
		name:               "DoNothingExample",
		subscriptionLister: subscriptionLister,
		cosmosClient:       cosmosClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[controllerutils.HCPClusterKey](),
			workqueue.TypedRateLimitingQueueConfig[controllerutils.HCPClusterKey]{
				Name: "do-nothing-example",
			},
		),
	}

	return c
}

func (c *doNothingExample) synchronizeHCPCluster(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cosmosHCPCluster, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get HCP cluster: %w", err)
	}

	// Check to see if you have work to do here.  You may also choose to look at state you saved for yourself for later,
	// but this is slightly less desireable and you should always force a recheck of the actual state of the world after
	// a certain staleness.
	existingController, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Controllers(key.HCPClusterName).Get(ctx, c.name)
	if err != nil && !database.IsResponseError(err, http.StatusNotFound) {
		return fmt.Errorf("failed to get existing controller state: %w", err)
	}

	if existingController == nil {
		logger.Info("starting work for item the first time",
			"provisioning_state", cosmosHCPCluster.ServiceProviderProperties.ProvisioningState,
		)
	} else {
		logger.Info("starting work for item",
			"provisioning_state", cosmosHCPCluster.ServiceProviderProperties.ProvisioningState,
			"controller_degraded", controllerutils.GetCondition(existingController.Status.Conditions, "Degraded"),
		)
	}

	// Do your work here.  If anything fails, return an error to have it recorded.
	// A typical controller that enforced the invariant that every Cluster has an AzureThing will take action like this
	//  1. If no name of AzureThing, decide you need a name and continue.
	//  2. Decide on a name for the new AzureThing.  It should have a random suffix.  This ensures no names are special,
	//     predictable, or likely to conflict.
	//  3. Store the name of the AzureThing into cosmos. Do this BEFORE creation so that if you create the Azure thing,
	//     but somehow fail to store that information, you don't end up losing the AzureThing and recreate it.
	//  4. At this point you need an AzureThing/thingName that is "done".  Check to see if the AzureThing/thingName exists.
	//  5. If it exists and if it is "done", you can store its doneness in a way that you recheck after duration/Y+jitter.
	//  6. If it exists and is not "done", you can store its lack of doneness in a way that you recheck after duration/Z+jitter and requeue.
	//  7. If it doesn't exist, then create it.
	//  8. After creating it, treat it like you just got it and store its doneness, when to recheck, and requeue.
	// This ordering is crash-safe for keeping track of things we create without transactions.
	// When building controllers, imagine that process exits after every line: does it recover on the next restart with zero human intervention.
	// Notice that you NEVER use a poll/wait inside of the processing loop.  We don't hold a thread busy doing that.

	return nil
}

func (c *doNothingExample) SyncOnce(ctx context.Context, keyObj any) error {
	key := keyObj.(controllerutils.HCPClusterKey)

	syncErr := c.synchronizeHCPCluster(ctx, key) // we'll handle this is a moment.

	controllerWriteErr := controllerutils.WriteController(
		ctx,
		c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Controllers(key.HCPClusterName),
		c.name,
		key.InitialController,
		controllerutils.ReportSyncError(syncErr),
	)

	return errors.Join(syncErr, controllerWriteErr)
}

func (c *doNothingExample) queueAllHCPClusters(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)

	allSubscriptions, err := c.subscriptionLister.List(ctx)
	if err != nil {
		logger.Error(err, "unable to list subscriptions")
	}
	for _, subscription := range allSubscriptions {
		subscriptionID := subscription.ResourceID.SubscriptionID
		allHCPClusters, err := c.cosmosClient.HCPClusters(subscriptionID, "").List(ctx, nil)
		if err != nil {
			logger.Error(err, "unable to list HCP clusters", "subscription_id", subscription.ResourceID.SubscriptionID)
			continue
		}

		for _, hcpCluster := range allHCPClusters.Items(ctx) {
			c.queue.Add(controllerutils.HCPClusterKey{
				SubscriptionID:    hcpCluster.ID.SubscriptionID,
				ResourceGroupName: hcpCluster.ID.ResourceGroupName,
				HCPClusterName:    hcpCluster.ID.Name,
			})
		}
		if err := allHCPClusters.GetError(); err != nil {
			logger.Error(err, "unable to iterate over HCP clusters", "subscription_id", subscription.ResourceID.SubscriptionID)
		}
	}
}

func (c *doNothingExample) Run(ctx context.Context, threadiness int) {
	// don't let panics crash the process
	defer utilruntime.HandleCrash()
	// make sure the work queue is shutdown which will trigger workers to end
	defer c.queue.ShutDown()

	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues("controller_name", c.name)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting")

	// start up your worker threads based on threadiness.  Some controllers
	// have multiple kinds of workers
	for i := 0; i < threadiness; i++ {
		// runWorker will loop until "something bad" happens.  The .Until will
		// then rekick the worker after one second
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	go wait.JitterUntilWithContext(ctx, c.queueAllHCPClusters, time.Minute, 0.1, true)

	logger.Info("Started workers")

	// wait until we're told to stop
	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *doNothingExample) runWorker(ctx context.Context) {
	// hot loop until we're told to stop.  processNextWorkItem will
	// automatically wait until there's work available, so we don't worry
	// about secondary waits
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem deals with one item off the queue.  It returns false
// when it's time to quit.
func (c *doNothingExample) processNextWorkItem(ctx context.Context) bool {
	// Pull the next work item from queue.  It will be an object reference that we use to lookup
	// something in a cache
	ref, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	// you always have to indicate to the queue that you've completed a piece of
	// work
	defer c.queue.Done(ref)

	// add the logger values to allow searching by the context.
	logger := utils.LoggerFromContext(ctx)
	logger = ref.AddLoggerValues(logger)
	ctx = utils.ContextWithLogger(ctx, logger)

	// Process the object reference.  This method will contains your "do stuff" logic
	err := c.SyncOnce(ctx, ref)
	if err == nil {
		// if you had no error, tell the queue to stop tracking history for your
		// item. This will reset things like failure counts for per-item rate
		// limiting
		c.queue.Forget(ref)
		return true
	}

	// there was a failure so be sure to report it.  This method allows for
	// pluggable error handling which can be used for things like
	// cluster-monitoring
	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", ref)

	// since we failed, we should requeue the item to work on later.  This
	// method will add a backoff to avoid hotlooping on particular items
	// (they're probably still not going to work right away) and overall
	// controller protection (everything I've done is broken, this controller
	// needs to calm down or it can starve other useful work) cases.
	c.queue.AddRateLimited(ref)

	return true
}
