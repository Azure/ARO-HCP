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

package controllerutils

import (
	"context"
	"errors"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type ClusterSyncer interface {
	SyncOnce(ctx context.Context, keyObj HCPClusterKey) error
}

type clusterWatchingController struct {
	name           string
	syncer         ClusterSyncer
	resyncDuration time.Duration

	subscriptionLister listers.SubscriptionLister
	cosmosClient       database.DBClient

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[HCPClusterKey]
}

// NewClusterWatchingController periodically looks up all clusters and queues them
func NewClusterWatchingController(
	name string,
	cosmosClient database.DBClient,
	subscriptionLister listers.SubscriptionLister,
	resyncDuration time.Duration,
	syncer ClusterSyncer,
) Controller {
	c := &clusterWatchingController{
		name:               name,
		subscriptionLister: subscriptionLister,
		cosmosClient:       cosmosClient,
		syncer:             syncer,
		resyncDuration:     resyncDuration,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[HCPClusterKey](),
			workqueue.TypedRateLimitingQueueConfig[HCPClusterKey]{
				Name: name,
			},
		),
	}

	return c
}

func (c *clusterWatchingController) SyncOnce(ctx context.Context, keyObj any) error {
	key := keyObj.(HCPClusterKey)

	syncErr := c.syncer.SyncOnce(ctx, key) // we'll handle this is a moment.

	controllerWriteErr := WriteController(
		ctx,
		c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Controllers(key.HCPClusterName),
		c.name,
		key.InitialController,
		ReportSyncError(syncErr),
	)

	return errors.Join(syncErr, controllerWriteErr)
}

func (c *clusterWatchingController) queueAllHCPClusters(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)

	allSubscriptions, err := c.subscriptionLister.List(ctx)
	if err != nil {
		logger.Error("unable to list subscriptions", "error", err)
	}
	for _, subscription := range allSubscriptions {
		subscriptionID := subscription.ResourceID.SubscriptionID
		allHCPClusters, err := c.cosmosClient.HCPClusters(subscriptionID, "").List(ctx, nil)
		if err != nil {
			logger.Error("unable to list HCP clusters", "error", err, "subscription_id", subscription.ResourceID.SubscriptionID)
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
			logger.Error("unable to iterate over HCP clusters", "error", err, "subscription_id", subscription.ResourceID.SubscriptionID)
		}
	}
}

func (c *clusterWatchingController) Run(ctx context.Context, threadiness int) {
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

	go wait.JitterUntilWithContext(ctx, c.queueAllHCPClusters, c.resyncDuration, 0.1, true)

	logger.Info("Started workers")

	// wait until we're told to stop
	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *clusterWatchingController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem deals with one item off the queue.  It returns false
// when it's time to quit.
func (c *clusterWatchingController) processNextWorkItem(ctx context.Context) bool {
	ref, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(ref)

	logger := utils.LoggerFromContext(ctx)
	logger = ref.AddLoggerValues(logger)
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
