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
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

type Controller interface {
	Run(ctx context.Context, threadiness int)
}

type hcpClusterKey struct {
	subscriptionID  string
	resourceGroupID string
	hcpClusterID    string
}

type clusterServiceToCustomerSync struct {
	cosmosClient database.DBClient

	clusterServiceClient ocm.ClusterServiceClientSpec

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[hcpClusterKey]
}

func NewClusterServiceToCustomerSyncController(cosmosClient database.DBClient, clusterServiceClient ocm.ClusterServiceClientSpec) Controller {
	c := &clusterServiceToCustomerSync{
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[hcpClusterKey](),
			workqueue.TypedRateLimitingQueueConfig[hcpClusterKey]{
				Name: "cluster-service-customer-sync",
			},
		),
	}

	return c
}

func (c *clusterServiceToCustomerSync) synchronizeHCPCluster(ctx context.Context, key hcpClusterKey) error {
	logger := klog.FromContext(ctx)

	cosmosHCPCluster, err := c.cosmosClient.HCPClusters(key.subscriptionID, key.resourceGroupID).Get(ctx, key.hcpClusterID)
	if err != nil {
		return fmt.Errorf("failed to get HCP cluster: %w", err)
	}

	csHCPCluster, err := c.clusterServiceClient.GetCluster(ctx, cosmosHCPCluster.InternalID)
	if err != nil {
		return fmt.Errorf("failed to get HCP cluster: %w", err)
	}

	desiredCosmosCluster := cosmosHCPCluster
	desiredCosmosCluster.CustomerDesiredState, err = customerDesiredClusterFromClusterService(cosmosHCPCluster.ResourceID, csHCPCluster)
	if err != nil {
		return fmt.Errorf("failed to convert customer desired state: %w", err)
	}

	if equality.Semantic.DeepEqual(desiredCosmosCluster.CustomerDesiredState, cosmosHCPCluster.CustomerDesiredState) {
		logger.V(4).Info("cosmos customer properties are up to date")
		return nil
	}

	logger.V(4).Info("writing customer properties to cosmos", "desired_state", desiredCosmosCluster.CustomerDesiredState)
	_, err = c.cosmosClient.HCPClusters(key.subscriptionID, key.resourceGroupID).Replace(ctx, desiredCosmosCluster)
	if err != nil {
		return err
	}

	return nil
}

func customerDesiredClusterFromClusterService(resourceID *azcorearm.ResourceID, csCluster *arohcpv1alpha1.Cluster) (database.CustomerDesiredHCPClusterState, error) {
	internalCluster, err := ocm.ConvertCStoHCPOpenShiftCluster(resourceID, csCluster)
	if err != nil {
		return database.CustomerDesiredHCPClusterState{}, err
	}
	return database.CustomerDesiredHCPClusterState{
		HCPOpenShiftCluster: internalCluster.Properties,
	}, nil
}

func (c *clusterServiceToCustomerSync) synchronizeAndReport(ctx context.Context, key hcpClusterKey) error {
	logger := klog.FromContext(ctx)
	logger = logger.WithValues(
		"subscription_id", key.subscriptionID,
		"resource_group", key.resourceGroupID,
		"hcp_cluster_name", key.resourceGroupID,
	)
	ctx = klog.NewContext(ctx, logger)
	logger.V(4).Info("start synchronizing")
	defer logger.V(4).Info("end synchronizing")

	syncErr := c.synchronizeHCPCluster(ctx, key)
	if syncErr == nil {
		// TODO write condition indicating successful sync
		return nil
	}

	// TODO write condition indicated failed sync
	logger.Error(syncErr, "failed to synchronize")

	return syncErr
}

func (c *clusterServiceToCustomerSync) queueAllHCPClusters(ctx context.Context) {
	logger := klog.FromContext(ctx)

	allSubscriptions := c.cosmosClient.ListAllSubscriptionDocs()
	for subscriptionID := range allSubscriptions.Items(ctx) {
		allHCPClusters, err := c.cosmosClient.HCPClusters(subscriptionID, "").List(ctx, nil)
		if err != nil {
			logger.Error(err, "unable to list HCP clusters", "subscription_id", subscriptionID)
			continue
		}

		for _, hcpCluster := range allHCPClusters.Items(ctx) {
			c.queue.Add(hcpClusterKey{
				subscriptionID:  hcpCluster.ResourceID.SubscriptionID,
				resourceGroupID: hcpCluster.ResourceID.ResourceGroupName,
				hcpClusterID:    hcpCluster.ResourceID.Name,
			})
		}
		if err := allHCPClusters.GetError(); err != nil {
			logger.Error(err, "unable to iterate over HCP clusters", "subscription_id", subscriptionID)
		}
	}
	if err := allSubscriptions.GetError(); err != nil {
		logger.Error(err, "unable to iterate over all subscriptions")
	}
}

func (c *clusterServiceToCustomerSync) Run(ctx context.Context, threadiness int) {
	// don't let panics crash the process
	defer utilruntime.HandleCrash()
	// make sure the work queue is shutdown which will trigger workers to end
	defer c.queue.ShutDown()
	logger := klog.FromContext(ctx)

	logger.Info("Starting clusterServiceToCustomerSync controller")

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
	logger.Info("Shutting down clusterServiceToCustomerSync controller")
}

func (c *clusterServiceToCustomerSync) runWorker(ctx context.Context) {
	// hot loop until we're told to stop.  processNextWorkItem will
	// automatically wait until there's work available, so we don't worry
	// about secondary waits
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem deals with one item off the queue.  It returns false
// when it's time to quit.
func (c *clusterServiceToCustomerSync) processNextWorkItem(ctx context.Context) bool {
	// Pull the next work item from queue.  It will be an object reference that we use to lookup
	// something in a cache
	ref, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	// you always have to indicate to the queue that you've completed a piece of
	// work
	defer c.queue.Done(ref)

	// Process the object reference.  This method will contains your "do stuff" logic
	err := c.synchronizeAndReport(ctx, ref)
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
