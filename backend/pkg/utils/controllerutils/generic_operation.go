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
	"strings"
	"time"

	"github.com/go-logr/logr"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type OperationSynchronizer interface {
	ShouldProcess(ctx context.Context, operation *coreapi.Operation) bool
	SynchronizeOperation(ctx context.Context, key OperationKey) error
}

type genericOperation struct {
	name string

	synchronizer      OperationSynchronizer
	resourcesDBClient corecosmosstorage.ResourcesDBClient

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[OperationKey]
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
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
) Controller {
	c := &genericOperation{
		name:              name,
		synchronizer:      synchronizer,
		resourcesDBClient: resourcesDBClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[OperationKey](),
			workqueue.TypedRateLimitingQueueConfig[OperationKey]{
				Name: name,
			},
		),
	}

	// this happens when unit tests don't want triggering.  This isn't beautiful, but fails to do nothing which is pretty safe.
	if activeOperationInformer != nil {
		logger := utils.DefaultLogger()
		logger = logger.WithValues(
			utils.LogValues{}.AddControllerName(c.name)...,
		)

		_, err := activeOperationInformer.AddEventHandlerWithOptions(
			cache.ResourceEventHandlerFuncs{
				AddFunc:    c.enqueueAdd,
				UpdateFunc: c.enqueueUpdate,
			},
			cache.HandlerOptions{
				Logger:       &logger,
				ResyncPeriod: ptr.To(activeOperationScanInterval),
			})
		if err != nil {
			panic(err) // coding error
		}
	}

	return c
}

func (c *genericOperation) controllerCRUD(key OperationKey) cosmosstorageutils.ResourceCRUD[coreapi.Controller, *coreapi.Controller] {
	parentResourceID := key.GetParentResourceID()
	sub := parentResourceID.SubscriptionID
	rg := parentResourceID.ResourceGroupName

	switch {
	case strings.EqualFold(parentResourceID.ResourceType.String(), coreapi.NodePoolResourceType.String()):
		return c.resourcesDBClient.HCPClusters(sub, rg).NodePools(parentResourceID.Parent.Name).Controllers(parentResourceID.Name)
	case strings.EqualFold(parentResourceID.ResourceType.String(), coreapi.ExternalAuthResourceType.String()):
		return c.resourcesDBClient.HCPClusters(sub, rg).ExternalAuth(parentResourceID.Parent.Name).Controllers(parentResourceID.Name)
	default:
		return c.resourcesDBClient.HCPClusters(sub, rg).Controllers(parentResourceID.Name)
	}
}

func (c *genericOperation) QueueForInformers(resyncDuration time.Duration, notifiers ...Notifier) error {
	// panic so that the developer error is noticed immediately
	panic("not implemented")
}

func (c *genericOperation) SyncOnce(ctx context.Context, keyObj any) error {
	key := keyObj.(OperationKey)
	controllerCRUD := c.controllerCRUD(key)

	defer utilruntime.HandleCrash(DegradedControllerPanicHandler(
		ctx,
		controllerCRUD,
		c.name,
		key.InitialController))

	syncErr := c.synchronizer.SynchronizeOperation(ctx, key)

	controllerWriteErr := WriteController(
		ctx,
		controllerCRUD,
		c.name,
		key.InitialController,
		ReportSyncError(syncErr),
	)

	return errors.Join(syncErr, controllerWriteErr)
}

// Run check do_nothing.go for basic doc details.
func (c *genericOperation) Run(ctx context.Context, threadiness int) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	ctx = utils.ContextWithControllerName(ctx, c.name)
	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues(utils.LogValues{}.AddControllerName(c.name)...)
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

	ReconcileTotal.WithLabelValues(c.name).Inc()
	err := c.SyncOnce(ctx, ref)
	if err == nil {
		c.queue.Forget(ref)
		return true
	}

	ReconcileErrors.WithLabelValues(c.name).Inc()
	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", ref)
	c.queue.AddRateLimited(ref)

	return true
}

func (c *genericOperation) enqueueAdd(newObj interface{}) {
	logger := utils.DefaultLogger()
	logger = logger.WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx := logr.NewContext(context.TODO(), logger)
	ctx = utils.ContextWithControllerName(ctx, c.name)

	castObj := newObj.(*coreapi.Operation)
	if castObj.ExternalID == nil {
		return
	}
	key := OperationKey{
		SubscriptionID:   castObj.ExternalID.SubscriptionID,
		OperationName:    castObj.ResourceID.Name,
		ParentResourceID: castObj.ExternalID.String(),
	}
	logger = key.AddLoggerValues(logger)
	ctx = logr.NewContext(ctx, logger)

	// we check here whether we should queue or not. If our view is stale and another update is coming, then we are guaranteed to be
	// informed of an update again.  At this point we can check if it meets our preconditions again.
	if !c.synchronizer.ShouldProcess(ctx, castObj) {
		return
	}

	c.queue.Add(key)
}

func (c *genericOperation) enqueueUpdate(_ interface{}, newObj interface{}) {
	c.enqueueAdd(newObj)
}
