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

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[HCPClusterKey]

	CreateAzureWidget func() (string, error)
}

// NewClusterInflightsController
func NewClusterInflightsController(cosmosClient database.DBClient) Controller {
	c := &clusterInflights{
		name:         "ClusterInflights",
		cosmosClient: cosmosClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[HCPClusterKey](),
			workqueue.TypedRateLimitingQueueConfig[HCPClusterKey]{
				Name: "cluster-inflights-workqueue",
			},
		),
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

	// TODO we need to move cluster inflights from CS to the RP. The list of cluster inflights
	// that we run in CS are separated by inflights that run when a cluster is created and inflights
	// that run when a cluster is updated. At the moment of writing this (2025-01-08) the list of inflights
	// in CS are:
	// Cluster Creation Inflights:
	// - Azure RP registration validation: we check if in the customer's azure subscription the expected resource
	//   providers are registered
	// - Azure ARO-HCP Cluster Resource Group existence validation: we validate that the resource group of the
	//   aro-hcp cluster exist in Azure
	// - Azure ARO-HCP Cluster Managed Resource Group non existence validation: we validate that the managed resource group
	//   of the aro-hcp cluster must NOT exist beforehand
	// - ARO-HCP Cluster Managed Identities existence validation: we validate that the ARO-HCP Cluster identities
	//   exist in Azure. The identities are the control plane identities, the data plane identities and the
	//   service managed identity
	// - ARO-HCP Cluster Subnet validations: we perform a subset of validations related to it. For example. its existence,
	//   its location, the existence of its parent VNet, etc.
	// - IP addresses quota validation: we validate that the customer's subscription has enough quota in the Azure
	//   location of the Cluster so the provisioning process can create the public IPs required for it
	// - ARO-HCP Cluster Network security group validation: we perform a subset of validations related to it. For example,
	//   its existence, its location matches the cluster's location, etc.
	// - ARO-HCP Cluster Service Managed Identity permissions validation: we validate that the Service Managed Identity
	//   has the expected set of permissions (via Azure check access api v2) so the cluster can be work correctly
	// - [WIP] ARO-HCP Clusters Service Control Plane identities permissions
	//   validation: Same as the previous point but for the Service Control Plane identities
	// - [WIP] ARO-HCP Clusters Service Data Plane identities permissions
	//   validation: Same as the previous point but for the Service Control Plane identities
	// Cluster Update Inflights:
	// - None as of now but we anticipate that we will need the ability to run some of them at that
	//   point in time.
	// Over time, it is possible more are added in CS that are not described here at the moment of writing this.
	// As an important note, not all validations that CS does are done in inflights. There are a set of static
	// validations that occur synchronously during the API call, and also a set of "CS preflight" validations that also
	// occur synchronously during the API call. This also means that different checks on a given element can occur
	// at different points in time. For example, for the cluster's subnet there are some static checks that
	// occur during the API call (for faster feedback) and then an inflight validates another aspects of it.
	// TODO decide how we are going to organize inflights execution and how do they map to controller(s). In CS the
	// dimensions are:
	// - Inflight check (what the check does)
	// - Resource Type (Cluster, NodePool)
	// - Resource Action (Create, Update, ...)
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
