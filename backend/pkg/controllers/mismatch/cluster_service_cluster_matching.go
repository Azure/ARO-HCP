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

package mismatch

import (
	"context"
	"fmt"
	"strings"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	utilsclock "k8s.io/utils/clock"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type clusterServiceClusterMatching struct {
	name  string
	clock utilsclock.PassiveClock

	subscriptionLister   corelisters.SubscriptionLister
	resourcesDBClient    corecosmosstorage.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[string]
}

// NewClusterServiceClusterMatchingController periodically looks for mismatched cluster-service and cosmos clusters
func NewClusterServiceClusterMatchingController(clock utilsclock.PassiveClock, resourcesDBClient corecosmosstorage.ResourcesDBClient, subscriptionLister corelisters.SubscriptionLister, clusterServiceClient ocm.ClusterServiceClientSpec) controllerutils.Controller {
	c := &clusterServiceClusterMatching{
		name:                 "ClusterServiceMatchingClusters",
		clock:                clock,
		subscriptionLister:   subscriptionLister,
		resourcesDBClient:    resourcesDBClient,
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

func (c *clusterServiceClusterMatching) getAllCosmosObjs(ctx context.Context) (map[string]*coreapi.HCPOpenShiftCluster, []*coreapi.HCPOpenShiftCluster, error) {
	clusterServiceIDToCluster := map[string]*coreapi.HCPOpenShiftCluster{}
	ret := []*coreapi.HCPOpenShiftCluster{}

	allSubscriptions, err := c.subscriptionLister.List(ctx)
	if err != nil {
		return nil, nil, utils.TrackError(err)
	}
	for _, subscription := range allSubscriptions {
		subscriptionID := subscription.ResourceID.SubscriptionID
		allHCPClusters, err := c.resourcesDBClient.HCPClusters(subscriptionID, "").List(ctx, nil)
		if err != nil {
			return nil, nil, utils.TrackError(err)
		}

		for _, cluster := range allHCPClusters.Items(ctx) {
			ret = append(ret, cluster)
			// we skip items without a ClusterServiceID or PendingClusterServiceID because they may be about to get them and shouldn't be deleted.
			if cluster.ServiceProviderProperties.ClusterServiceID == nil && cluster.ServiceProviderProperties.PendingClusterServiceID == nil {
				continue
			}

			var clusterServiceID string
			if cluster.ServiceProviderProperties.ClusterServiceID != nil {
				clusterServiceID = cluster.ServiceProviderProperties.ClusterServiceID.String()
			} else {
				clusterServiceID = cluster.ServiceProviderProperties.PendingClusterServiceID.String()
			}

			existingCluster, exists := clusterServiceIDToCluster[clusterServiceID]
			if exists {
				return nil, nil, utils.TrackError(fmt.Errorf("duplicate obj found: %s, owned by %q and %q", cluster.ID.String(), existingCluster.ID.String(), cluster.ID.String()))
			}
			clusterServiceIDToCluster[clusterServiceID] = cluster
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

	clusterServiceIDToCosmosCluster, _, err := c.getAllCosmosObjs(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	_, allClusterServiceClusters, err := c.getAllClusterServiceObjs(ctx)
	if err != nil {
		if strings.Contains(err.Error(), "no healthy upstream") {
			// this error happens when cluster-service is down.  If cluster-service is down and we have content in cosmos to work on
			// other controllers will start reporting errors.
			// this particular controller fires based purely on time, so there might actually be no content.
			// In that case we don't want to fail and report a retrying controller, so we'll suppress the string
			// we get back
			//    expected response content type 'application/json' but received 'text/plain' and content 'no healthy upstream'
			logger.Error(err, "failed to get cluster-service clusters, probably because cluster-service is down")
			return nil
		}

		return utils.TrackError(err)
	}

	for _, clusterServiceCluster := range allClusterServiceClusters {
		clusterLogger := logger.WithValues("clusterServiceID", clusterServiceCluster.HREF())
		_, exists := clusterServiceIDToCosmosCluster[clusterServiceCluster.HREF()]
		if exists {
			continue
		}

		clusterServiceCreationTimestamp, ok := clusterServiceCluster.GetCreationTimestamp()
		if !ok {
			clusterLogger.Error(
				utils.TrackError(fmt.Errorf("cluster service cluster without creation_timestamp and a matching cosmos cluster detected")),
				"cluster service cluster without creation_timestamp and a matching cosmos cluster detected")
			continue
		}

		// if the cluster service cluster isn't older than an hour, we skip it
		if c.clock.Since(clusterServiceCreationTimestamp) < time.Hour {
			clusterLogger.Info("cluster service cluster doesn't have matching cosmos cluster detected but is not old enough to delete")
			continue
		}

		// before deleting, double check in the cosmos database that the cluster service cluster is still not in the cosmos database
		clusterServiceAzurePlatform, ok := clusterServiceCluster.GetAzure()
		if !ok {
			clusterLogger.Error(
				utils.TrackError(fmt.Errorf("cluster service cluster without Azure properties and a matching cosmos cluster detected")),
				"cluster service cluster without Azure properties and a matching cosmos cluster detected")
			continue
		}
		_, err := c.resourcesDBClient.HCPClusters(clusterServiceAzurePlatform.SubscriptionID(), clusterServiceAzurePlatform.ResourceGroupName()).
			Get(ctx, clusterServiceAzurePlatform.ResourceName())
		if err == nil {
			clusterLogger.Info("cluster service cluster exists in the cosmos database, no need to delete")
			continue
		}

		if !cosmosstorageutils.IsNotFoundError(err) {
			clusterLogger.Error(err, "error getting cluster service cluster from cosmos database")
			continue
		}

		// cluster is confirmed to not be in cosmos database, we can delete it from cluster service
		clusterServiceID, err := metadataapi.NewInternalID(clusterServiceCluster.HREF())
		if err != nil {
			clusterLogger.Error(err, "error creating internal ID for cluster service cluster")
			continue
		}
		err = c.clusterServiceClient.DeleteCluster(ctx, clusterServiceID)
		if err != nil {
			clusterLogger.Error(err, "error deleting cluster service cluster")
			continue
		}
		clusterLogger.Info("cluster service cluster without matching cosmos cluster deleted from cluster service")
	}

	return nil
}

func (c *clusterServiceClusterMatching) QueueForInformers(resyncDuration time.Duration, notifiers ...controllerutils.Notifier) error {
	// panic so that the developer error is noticed immediately
	panic("not implemented")
}

func (c *clusterServiceClusterMatching) SyncOnce(ctx context.Context, _ any) error {
	logger := utils.LoggerFromContext(ctx)

	syncErr := c.synchronizeAllClusters(ctx) // we'll handle this is a moment.
	if syncErr != nil {
		logger.Error(syncErr, "unable to synchronize all clusters")
	}

	return utils.TrackError(syncErr)
}

func (c *clusterServiceClusterMatching) Run(ctx context.Context, threadiness int) {
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

	// TODO before switching to a regular informer, build a basic LRU "don't fire unless your cooldown is over"
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
