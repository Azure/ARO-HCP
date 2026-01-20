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

package mismatchcontrollers

import (
	"context"
	"fmt"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type clusterServiceClusterMatching struct {
	name string

	subscriptionLister   listers.SubscriptionLister
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[string]
}

// NewClusterServiceClusterMatchingController periodically looks for mismatched cluster-service and cosmos clusters
func NewClusterServiceClusterMatchingController(cosmosClient database.DBClient, subscriptionLister listers.SubscriptionLister, clusterServiceClient ocm.ClusterServiceClientSpec) controllerutils.Controller {
	c := &clusterServiceClusterMatching{
		name:                 "ClusterServiceMatchingClusters",
		subscriptionLister:   subscriptionLister,
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: "ClusterServiceMatchingClusters",
			},
		),
	}

	return c
}

func (c *clusterServiceClusterMatching) getAllCosmosObjs(ctx context.Context) (map[string]*api.HCPOpenShiftCluster, []*api.HCPOpenShiftCluster, error) {
	clusterServiceIDToCluster := map[string]*api.HCPOpenShiftCluster{}
	ret := []*api.HCPOpenShiftCluster{}

	allSubscriptions, err := c.subscriptionLister.List(ctx)
	if err != nil {
		return nil, nil, utils.TrackError(err)
	}
	for _, subscription := range allSubscriptions {
		subscriptionID := subscription.ResourceID.SubscriptionID
		allHCPClusters, err := c.cosmosClient.HCPClusters(subscriptionID, "").List(ctx, nil)
		if err != nil {
			return nil, nil, utils.TrackError(err)
		}

		for _, cluster := range allHCPClusters.Items(ctx) {
			ret = append(ret, cluster)
			existingCluster, exists := clusterServiceIDToCluster[cluster.ServiceProviderProperties.ClusterServiceID.String()]
			if exists {
				return nil, nil, utils.TrackError(fmt.Errorf("duplicate obj found: %s, owned by %q and %q", cluster.ID.String(), existingCluster.ID.String(), cluster.ID.String()))
			}
			clusterServiceIDToCluster[cluster.ServiceProviderProperties.ClusterServiceID.String()] = cluster
		}
		if err := allHCPClusters.GetError(); err != nil {
			return nil, nil, utils.TrackError(err)
		}
	}

	return clusterServiceIDToCluster, ret, nil
}

func (c *clusterServiceClusterMatching) getAllClusterServiceObjs(ctx context.Context) (map[string]*arohcpv1alpha1.Cluster, []*arohcpv1alpha1.Cluster, error) {
	clusterServiceIDToCluster := map[string]*arohcpv1alpha1.Cluster{}
	ret := []*arohcpv1alpha1.Cluster{}

	clusterIterator := c.clusterServiceClient.ListClusters("")
	for cluster := range clusterIterator.Items(ctx) {
		ret = append(ret, cluster)
		existingCluster, exists := clusterServiceIDToCluster[cluster.HREF()]
		if exists {
			return nil, nil, utils.TrackError(fmt.Errorf("duplicate obj found: %s, owned by %q and %q", cluster.HREF(), existingCluster.ID(), cluster.ID()))
		}
		clusterServiceIDToCluster[cluster.HREF()] = cluster
	}
	if err := clusterIterator.GetError(); err != nil {
		return nil, nil, utils.TrackError(err)
	}

	return clusterServiceIDToCluster, ret, nil
}

func (c *clusterServiceClusterMatching) synchronizeAllClusters(ctx context.Context) error {
	logger := utils.LoggerFromContext(ctx)

	clusterServiceIDToCosmosCluster, allCosmosClusters, err := c.getAllCosmosObjs(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	clusterServiceIDToClusterServiceCluster, allClusterServiceClusters, err := c.getAllClusterServiceObjs(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	// now make sure that we can find a matching clusterservice cluster for all cosmos clusters
	for _, cosmosCluster := range allCosmosClusters {
		_, exists := clusterServiceIDToClusterServiceCluster[cosmosCluster.ServiceProviderProperties.ClusterServiceID.String()]
		if !exists {
			logger.Error("cosmos cluster doesn't have matching cluster-service cluster",
				"cosmosResourceID", cosmosCluster.ID,
				"clusterServiceID", cosmosCluster.ServiceProviderProperties.ClusterServiceID,
			)
		}
	}

	for _, clusterServiceCluster := range allClusterServiceClusters {
		_, exists := clusterServiceIDToCosmosCluster[clusterServiceCluster.HREF()]
		if !exists {
			logger.Error("cluster service cluster doesn't have matching cosmos cluster",
				"clusterServiceID", clusterServiceCluster.HREF(),
			)
		}
	}

	return nil
}

func (c *clusterServiceClusterMatching) SyncOnce(ctx context.Context, _ any) error {
	logger := utils.LoggerFromContext(ctx)

	syncErr := c.synchronizeAllClusters(ctx) // we'll handle this is a moment.
	if syncErr != nil {
		logger.Error("unable to synchronize all clusters", "error", syncErr)
	}

	return utils.TrackError(syncErr)
}

func (c *clusterServiceClusterMatching) Run(ctx context.Context, threadiness int) {
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

	go wait.JitterUntilWithContext(ctx, func(ctx context.Context) { c.queue.Add("default") }, 60*time.Minute, 0.1, true)

	logger.Info("Started workers")

	// wait until we're told to stop
	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *clusterServiceClusterMatching) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem deals with one item off the queue.  It returns false
// when it's time to quit.
func (c *clusterServiceClusterMatching) processNextWorkItem(ctx context.Context) bool {
	ref, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(ref)

	logger := utils.LoggerFromContext(ctx)
	ctx = utils.ContextWithLogger(ctx, logger)

	err := c.SyncOnce(ctx, ref)
	if err == nil {
		c.queue.Forget(ref)
		return true
	}

	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", ref)
	c.queue.AddRateLimited(ref)

	return true
}
