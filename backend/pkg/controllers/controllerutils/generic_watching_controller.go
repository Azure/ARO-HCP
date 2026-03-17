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

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
)

type GenericSyncer[T comparable] interface {
	SyncOnce(ctx context.Context, keyObj T) error
	CooldownChecker() CooldownChecker
	MakeKey(resourceID *azcorearm.ResourceID) T
}

type Notifier interface {
	AddEventHandlerWithOptions(handler cache.ResourceEventHandler, options cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error)
}

type genericWatchingController[T comparable] struct {
	name         string
	resourceType azcorearm.ResourceType
	syncer       GenericSyncer[T]

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[T]
}

// NewClusterWatchingController periodically looks up all clusters and queues them
// cooldownDuration is how long to wait before allowing a new notification to fire the controller.
// Since our detection of change is coarse, we are being triggered every few second without new information.
// Until we get a changefeed, the cooldownDuration value is effectively the min resync time.
// This does NOT prevent us from re-executing on errors, so errors will continue to trigger fast checks as expected.
func newGenericWatchingController[T comparable](name string, resourceType azcorearm.ResourceType, syncer GenericSyncer[T]) *genericWatchingController[T] {
	c := &genericWatchingController[T]{
		name:         name,
		resourceType: resourceType,
		syncer:       syncer,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[T](),
			workqueue.TypedRateLimitingQueueConfig[T]{
				Name: name,
			},
		),
	}

	return c
}

func (c *genericWatchingController[T]) SyncOnce(ctx context.Context, keyObj any) error {
	key, ok := keyObj.(T)
	if !ok {
		return fmt.Errorf("invalid key type %T", keyObj)
	}

	return c.syncer.SyncOnce(ctx, key)
}

func (c *genericWatchingController[T]) Run(ctx context.Context, threadiness int) {
	// don't let panics crash the process
	defer utilruntime.HandleCrash()
	// make sure the work queue is shutdown which will trigger workers to end
	defer c.queue.ShutDown()

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

func (c *genericWatchingController[T]) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem deals with one item off the queue.  It returns false
// when it's time to quit.
func (c *genericWatchingController[T]) processNextWorkItem(ctx context.Context) bool {
	ref, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(ref)

	logger := utils.LoggerFromContext(ctx)
	logger = AddLoggerValues(logger, ref)
	ctx = utils.ContextWithLogger(ctx, logger)

	ReconcileTotal.WithLabelValues(c.name).Inc()
	err := c.SyncOnce(ctx, ref)
	if err == nil {
		c.queue.Forget(ref)
		return true
	}

	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", ref)
	c.queue.AddRateLimited(ref)

	return true
}

func (c *genericWatchingController[T]) QueueForInformers(resyncDuration time.Duration, notifiers ...Notifier) error {
	errs := []error{}
	for _, notifier := range notifiers {
		_, err := notifier.AddEventHandlerWithOptions(
			cache.ResourceEventHandlerFuncs{
				AddFunc:    c.EnqueueCosmosAdd,
				UpdateFunc: c.EnqueueCosmosUpdate,
			},
			cache.HandlerOptions{
				ResyncPeriod: ptr.To(resyncDuration),
			})
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// EnqueueResourceIDAdd traverses to find a resourceID that is an hcpcluster and adds it if found.
// It is exposed so that individual controllers can add other items to requeue based on easily.
func (c *genericWatchingController[T]) EnqueueResourceIDAdd(resourceID *azcorearm.ResourceID, changed bool) {
	if resourceID == nil {
		return
	}
	if !apihelpers.ResourceTypeEqual(resourceID.ResourceType, c.resourceType) {
		c.EnqueueResourceIDAdd(resourceID.Parent, changed)
		return
	}

	key := c.syncer.MakeKey(resourceID)

	logger := utils.DefaultLogger()
	logger = logger.WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	logger = AddLoggerValues(logger, key)
	ctx := logr.NewContext(context.TODO(), logger)

	if changed {
		// when state has changed, fire immediately
		c.queue.Add(key)
		return
	}

	if !c.syncer.CooldownChecker().CanSync(ctx, key) {
		return
	}

	c.queue.Add(key)
}

func (c *genericWatchingController[T]) EnqueueCosmosAdd(newObj any) {
	c.EnqueueResourceIDAdd(newObj.(arm.CosmosPersistable).GetCosmosData().GetResourceID(), true)
}

func (c *genericWatchingController[T]) EnqueueCosmosUpdate(oldObj, newObj any) {
	changed := oldObj.(arm.CosmosPersistable).GetCosmosData().GetEtag() != newObj.(arm.CosmosPersistable).GetCosmosData().GetEtag()
	c.EnqueueResourceIDAdd(newObj.(arm.CosmosPersistable).GetCosmosData().GetResourceID(), changed)
}
