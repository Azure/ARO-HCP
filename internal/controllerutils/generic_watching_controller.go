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
	"strings"
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

// Enqueuer schedules work immediately after an input change. Unlike delayed
// requeues, these events should not increment the workqueue retry metric.
type Enqueuer interface {
	Enqueue(keyObj any)
}

type Notifier interface {
	AddEventHandlerWithOptions(handler cache.ResourceEventHandler, options cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error)
}

type GenericWatchingController[T comparable] struct {
	CacheSyncWaiter
	name           string
	resourceType   azcorearm.ResourceType
	syncer         GenericSyncer[T]
	reconcileTotal *prometheus.CounterVec

	// interestingResources maps lowercase resource types to lowercase resource names.
	// "*" matches every name of a registered type. An empty map accepts all events.
	interestingResources map[string][]string

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[T]
}

// NewGenericWatchingController creates a controller that watches Cosmos-backed
// informers and delegates reconciliation to syncer.
func NewGenericWatchingController[T comparable](name string, resourceType azcorearm.ResourceType, syncer GenericSyncer[T], reconcileTotal *prometheus.CounterVec) *GenericWatchingController[T] {
	c := &GenericWatchingController[T]{
		name:                 name,
		resourceType:         resourceType,
		syncer:               syncer,
		reconcileTotal:       reconcileTotal,
		interestingResources: make(map[string][]string),
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[T](),
			workqueue.TypedRateLimitingQueueConfig[T]{
				Name: name,
			},
		),
	}

	return c
}

// AddInterestingResource accepts events for a resource type and name, ignoring case.
// The name "*" accepts every resource of that type. With no registrations all
// events are accepted. Register interests before informers start delivering
// resource events; registrations are not safe to mutate concurrently with events.
func (c *GenericWatchingController[T]) AddInterestingResource(resourceType azcorearm.ResourceType, resourceName string) {
	key := strings.ToLower(resourceType.String())
	c.interestingResources[key] = append(c.interestingResources[key], strings.ToLower(resourceName))
}

func (c *GenericWatchingController[T]) Enqueue(keyObj any) {
	key, ok := keyObj.(T)
	if !ok {
		return
	}
	c.queue.Add(key)
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

	if !c.WaitForCacheSync(ctx) {
		return
	}

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
	return c.queueForInformers(resyncDuration, maxDepth, notifiers...)
}

// QueueForChildInformers subscribes to direct children of the controller's key
// resource. Like all generic watches, it observes deletions:
// removing a child can unblock teardown or change a status aggregate. Resource
// interests still filter the original child before the one-hop owner mapping.
func (c *GenericWatchingController[T]) QueueForChildInformers(resyncDuration time.Duration, notifiers ...Notifier) error {
	return c.queueForInformers(resyncDuration, 1, notifiers...)
}

// QueueForDescendantInformers extends child mapping to an explicit depth, for
// controllers consuming both direct children and node-pool-nested documents.
// It uses resource IDs already carried by the event; no lookup is required.
func (c *GenericWatchingController[T]) QueueForDescendantInformers(resyncDuration time.Duration, maxDepth int, notifiers ...Notifier) error {
	if maxDepth < 1 {
		return fmt.Errorf("descendant watches require a positive maximum depth")
	}
	return c.queueForInformers(resyncDuration, maxDepth, notifiers...)
}

func (c *GenericWatchingController[T]) queueForInformers(resyncDuration time.Duration, maxDepth int, notifiers ...Notifier) error {
	errs := []error{}

	logger := utils.DefaultLogger()
	logger = logger.WithValues(
		utils.LogValues{}.AddControllerName(c.name)...,
	)

	for _, notifier := range notifiers {
		handler := cache.ResourceEventHandlerFuncs{
			AddFunc:    c.enqueueCosmosAddFunc(maxDepth),
			UpdateFunc: c.enqueueCosmosUpdateFunc(maxDepth),
		}
		handler.DeleteFunc = func(obj any) {
			switch tombstone := obj.(type) {
			case cache.DeletedFinalStateUnknown:
				obj = tombstone.Obj
			case *cache.DeletedFinalStateUnknown:
				if tombstone == nil {
					return
				}
				obj = tombstone.Obj
			}
			if resource, ok := obj.(coreapi.CosmosPersistable); ok && resource.GetCosmosData() != nil {
				c.EnqueueResourceIDAddWithMaxDepth(resource.GetCosmosData().GetResourceID(), true, maxDepth)
			}
		}
		registration, err := notifier.AddEventHandlerWithOptions(
			handler,
			cache.HandlerOptions{
				Logger:       &logger,
				ResyncPeriod: ptr.To(resyncDuration),
			})
		errs = append(errs, err)
		if err == nil {
			// Sources can be ordinary informers or a union notifier. The union's
			// aggregate discovery barrier remains supplied by WithCacheSyncs.
			if source, ok := notifier.(interface{ HasSynced() bool }); ok {
				c.AddCacheSyncs(source.HasSynced)
			}
			if registration != nil {
				c.AddCacheSyncs(registration.HasSynced)
			}
		}
	}
	return errors.Join(errs...)
}

// EnqueueResourceIDAdd is equivalent to calling EnqueueResourceIDAddWithMaxDepth with a maxDepth of -1.
// See EnqueueResourceIDAddWithMaxDepth for more details.
// It is exposed so that individual controllers can add other items to requeue based on easily.
func (c *GenericWatchingController[T]) EnqueueResourceIDAdd(resourceID *azcorearm.ResourceID, changed bool) {
	c.EnqueueResourceIDAddWithMaxDepth(resourceID, changed, -1)
}

func (c *GenericWatchingController[T]) isResourceInteresting(resourceID *azcorearm.ResourceID) bool {
	if len(c.interestingResources) == 0 {
		return true
	}
	currResourceType := strings.ToLower(resourceID.ResourceType.String())
	currResourceName := strings.ToLower(resourceID.Name)
	for _, interestingResourceName := range c.interestingResources[currResourceType] {
		if interestingResourceName == "*" {
			return true
		}
		if currResourceName == interestingResourceName {
			return true
		}
	}
	return false
}

// EnqueueResourceIDAddWithMaxDepth traverses resourceID and its parents according to maxDepth until it
// finds a resourceID that is of the resource type of c.resourceType and adds it if found. Each walk to Parent consumes one level.
// maxDepth is the maximum number of parent hops to traverse when searching for a resourceID of type c.resourceType.
// maxDepth 0 means only the resourceID itself is considered.
// maxDepth -1 (or any negative value) means no limit. The parent walk continues until a match or nil parent is reached.
// It is exposed so that individual controllers can add other items to requeue based on easily.
// When there's a match of resourceType: when changed is true, the resourceID is added to the queue immediately. Otherwise, the
// resourceID is added to the queue only if the cooldown checker allows it.
// Interesting-resource filters apply to the original event, not the ancestors
// visited while resolving the controller's key.
func (c *GenericWatchingController[T]) EnqueueResourceIDAddWithMaxDepth(resourceID *azcorearm.ResourceID, changed bool, maxDepth int) {
	if resourceID == nil {
		return
	}
	if !c.isResourceInteresting(resourceID) {
		return
	}

	for !metadataapi.ResourceTypeEqual(resourceID.ResourceType, c.resourceType) {
		if maxDepth == 0 {
			return
		}
		if maxDepth > 0 {
			maxDepth--
		}
		resourceID = resourceID.Parent
		if resourceID == nil {
			return
		}
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
