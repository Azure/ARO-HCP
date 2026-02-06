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

package operationcontrollers

import (
	"context"
	"errors"
	"net/http"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/backend/oldoperationscanner"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type OperationSynchronizer interface {
	ShouldProcess(ctx context.Context, operation *api.Operation) bool
	SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error
}

type genericOperation struct {
	name string

	cooldownChecker controllerutils.CooldownChecker
	synchronizer    OperationSynchronizer
	cosmosClient    database.DBClient

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[controllerutils.OperationKey]
}

// NewGenericOperationController returns a Controller that updates Cosmos DB documents
// tracking ongoing asynchronous operations. Each Controller instance has a unique
// OperationSynchronizer that reconciles a particular type of asynchronous operation,
// like cluster creation or node pool deletion.
func NewGenericOperationController(
	name string,
	synchronizer OperationSynchronizer,
	activeOperationScanInterval time.Duration,
	activeOperationInformer cache.SharedIndexInformer,
	cosmosClient database.DBClient,
) controllerutils.Controller {
	c := &genericOperation{
		name:            name,
		cooldownChecker: controllerutils.NewTimeBasedCooldownChecker(10 * time.Second),
		synchronizer:    synchronizer,
		cosmosClient:    cosmosClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[controllerutils.OperationKey](),
			workqueue.TypedRateLimitingQueueConfig[controllerutils.OperationKey]{
				Name: name,
			},
		),
	}

	// this happens when unit tests don't want triggering.  This isn't beautiful, but fails to do nothing which is pretty safe.
	if activeOperationInformer != nil {
		_, err := activeOperationInformer.AddEventHandlerWithOptions(
			cache.ResourceEventHandlerFuncs{
				AddFunc:    c.enqueueAdd,
				UpdateFunc: c.enqueueUpdate,
			},
			cache.HandlerOptions{
				ResyncPeriod: ptr.To(activeOperationScanInterval),
			})
		if err != nil {
			panic(err) // coding error
		}
	}

	return c
}

// PostAsyncNotification submits an POST request with status payload to the given URL.
func PostAsyncNotification(notificationClient *http.Client) database.PostAsyncNotificationFunc {
	return func(ctx context.Context, operation *api.Operation) error {
		return oldoperationscanner.PostAsyncNotification(ctx, notificationClient, operation)
	}
}

func (c *genericOperation) SyncOnce(ctx context.Context, keyObj any) error {
	key := keyObj.(controllerutils.OperationKey)

	syncErr := c.synchronizer.SynchronizeOperation(ctx, key)

	parentResourceID := key.GetParentResourceID()
	controllerWriteErr := controllerutils.WriteController(
		ctx,
		c.cosmosClient.HCPClusters(key.SubscriptionID, parentResourceID.ResourceGroupName).Controllers(parentResourceID.Name),
		c.name,
		key.InitialController,
		controllerutils.ReportSyncError(syncErr),
	)

	return errors.Join(syncErr, controllerWriteErr)
}

// Run check do_nothing.go for basic doc details.
func (c *genericOperation) Run(ctx context.Context, threadiness int) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues("controller_name", c.name)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting")

	for i := 0; i < threadiness; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	logger.Info("Started workers")

	<-ctx.Done()
	logger.Info("Shutting down")
}

// runWorker check do_nothing.go for doc details.
func (c *genericOperation) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem check do_nothing.go for doc details.
func (c *genericOperation) processNextWorkItem(ctx context.Context) bool {
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

func (c *genericOperation) enqueueAdd(newObj interface{}) {
	castObj := newObj.(*api.Operation)
	if castObj.ExternalID == nil {
		return
	}
	key := controllerutils.OperationKey{
		SubscriptionID:   castObj.ExternalID.SubscriptionID,
		OperationName:    castObj.ResourceID.Name,
		ParentResourceID: castObj.ExternalID.String(),
	}

	if !c.cooldownChecker.CanSync(context.TODO(), key) {
		return
	}
	// we check here whether we should queue or not. If our view is stale and another update is coming, then we are guaranteed to be
	// informed of an update again.  At this point we can check if it meets our preconditions again.
	if !c.synchronizer.ShouldProcess(context.Background(), castObj) {
		return
	}

	c.queue.Add(key)
}

func (c *genericOperation) enqueueUpdate(_ interface{}, newObj interface{}) {
	c.enqueueAdd(newObj)
}
