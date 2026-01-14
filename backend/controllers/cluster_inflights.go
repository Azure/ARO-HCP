package controllers

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

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// TODO decide granularity/scope of the controller
// TODO decide name of the controller (is inflight the correct term)
// TODO decide workqueue key. This will change depending on the scope
// of the controller
type clusterInflights struct {
	name string

	cosmosClient database.DBClient

	clusterValidations []ClusterValidation

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[HCPClusterKey]

	CreateAzureWidget func() (string, error)
}

// NewClusterInflightsController
func NewClusterInflightsController(
	cosmosClient database.DBClient,
	azureFPAClientBuilder azureclient.ClientBuilder,
) Controller {

	// TODO this might not be how we instantiate the validations nor introduce them,
	// for example we might decide that the clusterInflights type just receive an array of them directly,
	// but it showcases what kind of dependencies are used and when are they needed.
	var clusterValidations []ClusterValidation
	clusterValidations = append(clusterValidations,
		NewAzureRpRegistrationValidation(
			"azure-rp-registration-validation",
			azureFPAClientBuilder,
		),
	)

	c := &clusterInflights{
		name:         "ClusterInflights",
		cosmosClient: cosmosClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[HCPClusterKey](),
			workqueue.TypedRateLimitingQueueConfig[HCPClusterKey]{
				Name: "cluster-inflights-workqueue",
			},
		),
		clusterValidations: clusterValidations,
	}

	return c
}

func (c *clusterInflights) synchronizeHCPCluster(ctx context.Context, key HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cosmosHCPCluster, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get HCP cluster: %w", err)
	}

	// Check to see if you have work to do here. You may also choose to look at state you saved for yourself for later,
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
			"controller_degraded", getCondition(existingController.Status.Conditions, "Degraded"),
		)
	}

	// TODO simple showcase running the validations sequentially. This can be changed to run them in parallel, persisting
	// their results to coordinate, retry, etc. This does not address details on all of those other concerns yet, which
	// will need to be addressed too.
	for _, validation := range c.clusterValidations {
		err := validation.Validate(ctx, cosmosHCPCluster)
		if err != nil {
			logger.Error("failed to validate cluster", "error", err)
			return err
		}
	}

	return nil
}

func (c *clusterInflights) SyncOnce(ctx context.Context, keyObj any) error {
	key := keyObj.(HCPClusterKey)

	syncErr := c.synchronizeHCPCluster(ctx, key) // we'll handle this is a moment.

	controllerWriteErr := writeController(
		ctx,
		c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Controllers(key.HCPClusterName),
		c.name,
		key.initialController,
		reportSyncError(syncErr),
	)

	return errors.Join(syncErr, controllerWriteErr)
}

func (c *clusterInflights) queueAllHCPClusters(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)

	allSubscriptions, err := c.cosmosClient.Subscriptions().List(ctx, nil)
	if err != nil {
		logger.Error("unable to list subscriptions", "error", err)
	}

	for subscriptionID := range allSubscriptions.Items(ctx) {
		allHCPClusters, err := c.cosmosClient.HCPClusters(subscriptionID, "").List(ctx, nil)
		if err != nil {
			logger.Error("unable to list HCP clusters", "error", err, "subscription_id", subscriptionID)
			continue
		}

		for _, hcpCluster := range allHCPClusters.Items(ctx) {
			c.queue.Add(HCPClusterKey{
				SubscriptionID:    hcpCluster.ID.SubscriptionID,
				ResourceGroupName: hcpCluster.ID.ResourceGroupName,
				HCPClusterName:    hcpCluster.ID.Name,
			})
		}
		if err := allHCPClusters.GetError(); err != nil {
			logger.Error("unable to iterate over HCP clusters", "error", err, "subscription_id", subscriptionID)
		}
	}
	if err := allSubscriptions.GetError(); err != nil {
		logger.Error("unable to iterate over all subscriptions", "error", err)
	}
}

func (c *clusterInflights) Run(ctx context.Context, threadiness int) {
	// don't let panics crash the process
	defer utilruntime.HandleCrash()
	// make sure the work queue is shutdown which will trigger workers to end
	defer c.queue.ShutDown()

	logger := utils.LoggerFromContext(ctx)
	logger.With("controller_name", c.name)
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

func (c *clusterInflights) runWorker(ctx context.Context) {
	// hot loop until we're told to stop.  processNextWorkItem will
	// automatically wait until there's work available, so we don't worry
	// about secondary waits
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem deals with one item off the queue.  It returns false
// when it's time to quit.
func (c *clusterInflights) processNextWorkItem(ctx context.Context) bool {
	// Pull the next work item from queue. It will be an object reference that we use to lookup
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
