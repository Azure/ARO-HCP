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

package controllerutils

import (
	"context"
	"errors"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/go-logr/logr"
)

type NodePoolSyncer interface {
	SyncOnce(ctx context.Context, keyObj HCPNodePoolKey) error
	CooldownChecker() CooldownChecker
}

type nodePoolWatchingController struct {
	name   string
	syncer NodePoolSyncer

	cosmosClient database.DBClient

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[HCPNodePoolKey]
}

// NewNodePoolWatchingController periodically looks up all NodePools and queues them
// cooldownDuration is how long to wait before allowing a new notification to fire the controller.
// Since our detection of change is coarse, we are being triggered every few second without new information.
// Until we get a changefeed, the cooldownDuration value is effectively the min resync time.
// This does NOT prevent us from re-executing on errors, so errors will continue to trigger fast checks as expected.
func NewNodePoolWatchingController(
	name string,
	cosmosClient database.DBClient,
	NodePoolInformer cache.SharedIndexInformer,
	resyncDuration time.Duration,
	syncer NodePoolSyncer,
) Controller {
	c := &nodePoolWatchingController{
		name:         name,
		cosmosClient: cosmosClient,
		syncer:       syncer,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedItemBasedRateLimiter[HCPNodePoolKey](),
			workqueue.TypedRateLimitingQueueConfig[HCPNodePoolKey]{
				Name: name,
			},
		),
	}

	// this happens when unit tests don't want triggering.  This isn't beautiful, but fails to do nothing which is pretty safe.
	if NodePoolInformer != nil {
		_, err := NodePoolInformer.AddEventHandlerWithOptions(
			cache.ResourceEventHandlerFuncs{
				AddFunc:    c.enqueueAdd,
				UpdateFunc: c.enqueueUpdate,
			},
			cache.HandlerOptions{
				ResyncPeriod: ptr.To(resyncDuration),
			})
		if err != nil {
			panic(err) // coding error
		}
	}

	return c
}

func (c *nodePoolWatchingController) SyncOnce(ctx context.Context, keyObj any) error {
	key := keyObj.(HCPNodePoolKey)

	syncErr := c.syncer.SyncOnce(ctx, key) // we'll handle this is a moment.

	controllerWriteErr := WriteController(
		ctx,
		c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).Controllers(key.HCPNodePoolName),
		c.name,
		key.InitialController,
		ReportSyncError(syncErr),
	)

	return errors.Join(syncErr, controllerWriteErr)
}

func (c *nodePoolWatchingController) Run(ctx context.Context, threadiness int) {
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

	logger.Info("Started workers")

	// wait until we're told to stop
	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *nodePoolWatchingController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem deals with one item off the queue.  It returns false
// when it's time to quit.
func (c *nodePoolWatchingController) processNextWorkItem(ctx context.Context) bool {
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

func (c *nodePoolWatchingController) enqueueAdd(newObj interface{}) {
	logger := utils.DefaultLogger()
	logger = logger.WithValues("controller_name", c.name)
	ctx := logr.NewContext(context.TODO(), logger)

	castObj := newObj.(*api.HCPOpenShiftClusterNodePool)
	key := HCPNodePoolKey{
		SubscriptionID:    castObj.ID.SubscriptionID,
		ResourceGroupName: castObj.ID.ResourceGroupName,
		HCPClusterName:    castObj.ID.Parent.Name,
		HCPNodePoolName:   castObj.ID.Name,
	}
	logger = key.AddLoggerValues(logger)
	ctx = logr.NewContext(ctx, logger)

	if !c.syncer.CooldownChecker().CanSync(ctx, key) {
		return
	}

	c.queue.Add(key)
}

func (c *nodePoolWatchingController) enqueueUpdate(_ interface{}, newObj interface{}) {
	c.enqueueAdd(newObj)
}
