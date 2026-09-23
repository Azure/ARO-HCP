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
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type GenericSyncer[T comparable] interface {
	SyncOnce(ctx context.Context, keyObj T) error
	CooldownChecker() CooldownChecker
	MakeKey(resourceID *azcorearm.ResourceID) T
}

// AfterEnqueuer allows scheduling a workqueue item for processing after an
// explicit delay. Validation controllers use this to implement
// EarliestRetryAfter semantics.
type AfterEnqueuer interface {
	EnqueueAfter(keyObj any, duration time.Duration)
}

type Notifier interface {
	AddEventHandlerWithOptions(handler cache.ResourceEventHandler, options cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error)
}

type GenericWatchingController[T comparable] struct {
	name           string
	resourceType   azcorearm.ResourceType
	syncer         GenericSyncer[T]
	reconcileTotal *prometheus.CounterVec

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[T]
}

// NewGenericWatchingController creates a controller that watches Cosmos-backed
// informers and delegates reconciliation to syncer.
func NewGenericWatchingController[T comparable](name string, resourceType azcorearm.ResourceType, syncer GenericSyncer[T], reconcileTotal *prometheus.CounterVec) *GenericWatchingController[T] {
	c := &GenericWatchingController[T]{
		name:           name,
		resourceType:   resourceType,
		syncer:         syncer,
		reconcileTotal: reconcileTotal,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[T](),
			workqueue.TypedRateLimitingQueueConfig[T]{
				Name: name,
			},
		),
	}

	return c
}

func (c *GenericWatchingController[T]) EnqueueAfter(keyObj any, duration time.Duration) {
	key, ok := keyObj.(T)
	if !ok {
		return
	}
	c.queue.AddAfter(key, duration)
}

func (c *GenericWatchingController[T]) SyncOnce(ctx context.Context, keyObj any) error {
	key, ok := keyObj.(T)
	if !ok {
		return fmt.Errorf("invalid key type %T", keyObj)
	}

	return c.syncer.SyncOnce(ctx, key)
}

func (c *GenericWatchingController[T]) Run(ctx context.Context, threadiness int) {
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

	logger.Info("Started workers")

	// wait until we're told to stop
	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *GenericWatchingController[T]) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem deals with one item off the queue.  It returns false
// when it's time to quit.
func (c *GenericWatchingController[T]) processNextWorkItem(ctx context.Context) bool {
	ref, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(ref)

	logger := utils.LoggerFromContext(ctx)
	logger = utils.AddLoggerValues(logger, ref)
	ctx = utils.ContextWithLogger(ctx, logger)

	if c.reconcileTotal != nil {
		c.reconcileTotal.WithLabelValues(c.name).Inc()
	}
	err := c.SyncOnce(ctx, ref)
	if err == nil {
		c.queue.Forget(ref)
		return true
	}

	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", ref)
	c.queue.AddRateLimited(ref)

	return true
}

// QueueForInformers is equivalent to calling QueueForInformersWithMaxDepth with maxDepth of -1.
// See QueueForInformersWithMaxDepth for more details.
func (c *GenericWatchingController[T]) QueueForInformers(resyncDuration time.Duration, notifiers ...Notifier) error {
	return c.QueueForInformersWithMaxDepth(resyncDuration, -1, notifiers...)
}

// QueueForInformersWithMaxDepth adds event handlers to the notifiers for the controller with a given max depth.
// maxDepth is the maximum number of parent hops to traverse when searching for a resourceID whose type is c.resourceType. Each
// walk to Parent consumes one level.
// maxDepth 0 means only the resourceID itself is considered.
// maxDepth -1 (or any negative value) means no limit. The parent walk continues until a match or nil parent is reached.
// It is exposed so that individual controllers can add other items to requeue based on easily.
func (c *GenericWatchingController[T]) QueueForInformersWithMaxDepth(resyncDuration time.Duration, maxDepth int, notifiers ...Notifier) error {
	errs := []error{}

	logger := utils.DefaultLogger()
	logger = logger.WithValues(
		utils.LogValues{}.AddControllerName(c.name)...,
	)

	for _, notifier := range notifiers {
		_, err := notifier.AddEventHandlerWithOptions(
			cache.ResourceEventHandlerFuncs{
				AddFunc:    c.enqueueCosmosAddFunc(maxDepth),
				UpdateFunc: c.enqueueCosmosUpdateFunc(maxDepth),
			},
			cache.HandlerOptions{
				Logger:       &logger,
				ResyncPeriod: ptr.To(resyncDuration),
			})
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// EnqueueResourceIDAdd is equivalent to calling EnqueueResourceIDAddWithMaxDepth with a maxDepth of -1.
// See EnqueueResourceIDAddWithMaxDepth for more details.
// It is exposed so that individual controllers can add other items to requeue based on easily.
func (c *GenericWatchingController[T]) EnqueueResourceIDAdd(resourceID *azcorearm.ResourceID, changed bool) {
	c.EnqueueResourceIDAddWithMaxDepth(resourceID, changed, -1)
}

// EnqueueResourceIDAddWithMaxDepth traverses resourceID and its parents according to maxDepth until it
// finds a resourceID that is of the resource type of c.resourceType and adds it if found. Each walk to Parent consumes one level.
// maxDepth is the maximum number of parent hops to traverse when searching for a resourceID of type c.resourceType.
// maxDepth 0 means only the resourceID itself is considered.
// maxDepth -1 (or any negative value) means no limit. The parent walk continues until a match or nil parent is reached.
// It is exposed so that individual controllers can add other items to requeue based on easily.
// When there's a match of resourceType: when changed is true, the resourceID is added to the queue immediately. Otherwise, the
// resourceID is added to the queue only if the cooldown checker allows it.
func (c *GenericWatchingController[T]) EnqueueResourceIDAddWithMaxDepth(resourceID *azcorearm.ResourceID, changed bool, maxDepth int) {
	if resourceID == nil {
		return
	}
	if !metadataapi.ResourceTypeEqual(resourceID.ResourceType, c.resourceType) {
		if maxDepth == 0 {
			return
		}
		nextDepth := maxDepth
		if maxDepth > 0 {
			nextDepth = maxDepth - 1
		}
		c.EnqueueResourceIDAddWithMaxDepth(resourceID.Parent, changed, nextDepth)
		return
	}

	key := c.syncer.MakeKey(resourceID)

	logger := utils.DefaultLogger()
	logger = logger.WithValues(
		utils.LogValues{}.
			AddControllerName(c.name).
			AddLogValuesForResourceID(resourceID)...,
	)
	logger = utils.AddLoggerValues(logger, key)
	ctx := logr.NewContext(context.TODO(), logger)
	ctx = utils.ContextWithControllerName(ctx, c.name)

	if changed {
		// when state has changed, fire immediately
		c.queue.Add(key)
		return
	}

	if cooldownChecker := c.syncer.CooldownChecker(); cooldownChecker != nil && !cooldownChecker.CanSync(ctx, key) {
		logger.Info("Skipping notification")
		return
	}

	c.queue.Add(key)
}

func (c *GenericWatchingController[T]) enqueueCosmosAddFunc(maxDepth int) func(any) {
	return func(newObj any) {
		c.enqueueCosmosAddWithMaxDepth(newObj, maxDepth)
	}
}

func (c *GenericWatchingController[T]) enqueueCosmosAddWithMaxDepth(newObj any, maxDepth int) {
	c.EnqueueResourceIDAddWithMaxDepth(newObj.(coreapi.CosmosPersistable).GetCosmosData().GetResourceID(), true, maxDepth)
}

func (c *GenericWatchingController[T]) enqueueCosmosUpdateFunc(maxDepth int) func(any, any) {
	return func(oldObj, newObj any) {
		c.enqueueCosmosUpdateWithMaxDepth(oldObj, newObj, maxDepth)
	}
}

func (c *GenericWatchingController[T]) enqueueCosmosUpdateWithMaxDepth(oldObj, newObj any, maxDepth int) {
	changed := oldObj.(coreapi.CosmosPersistable).GetCosmosData().GetEtag() != newObj.(coreapi.CosmosPersistable).GetCosmosData().GetEtag()
	c.EnqueueResourceIDAddWithMaxDepth(newObj.(coreapi.CosmosPersistable).GetCosmosData().GetResourceID(), changed, maxDepth)
}
