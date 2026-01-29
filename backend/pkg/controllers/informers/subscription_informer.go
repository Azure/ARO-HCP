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

package informers

import (
	"context"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type subscriptionInformer struct {
	name string

	cosmosClient database.DBClient

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[string]

	subscriptionLister listers.BasicReaderMaintainer[arm.Subscription]
}

// NewSubscriptionInformerController periodically lists all subscriptions and updates the cache
func NewSubscriptionInformerController(cosmosClient database.DBClient, subscriptionLister listers.BasicReaderMaintainer[arm.Subscription]) controllerutils.Controller {
	c := &subscriptionInformer{
		name:         "SubscriptionInformer",
		cosmosClient: cosmosClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: "subscription-informer",
			},
		),

		subscriptionLister: subscriptionLister,
	}

	return c
}

func (c *subscriptionInformer) synchronizeSubscriptions(ctx context.Context, key string) error {
	newSubscriptions := []*arm.Subscription{}
	newSubscriptionsIterator, err := c.cosmosClient.Subscriptions().List(ctx, nil)
	if err != nil {
		return utils.TrackError(err)
	}
	for _, subscription := range newSubscriptionsIterator.Items(ctx) {
		newSubscriptions = append(newSubscriptions, subscription)
	}
	if err := newSubscriptionsIterator.GetError(); err != nil {
		return utils.TrackError(err)
	}
	newSubscriptionsByName := make(map[string]*arm.Subscription, len(newSubscriptions))
	for _, subscription := range newSubscriptions {
		newSubscriptionsByName[subscription.ResourceID.Name] = subscription
	}

	newLister := listers.NewReadOnlyContentLister(newSubscriptions, newSubscriptionsByName)
	c.subscriptionLister.ReplaceCache(ctx, newLister)

	return nil
}

func (c *subscriptionInformer) SyncOnce(ctx context.Context, keyObj any) error {
	key := keyObj.(string)

	return c.synchronizeSubscriptions(ctx, key) // we'll handle this is a moment.
}

func (c *subscriptionInformer) Run(ctx context.Context, threadiness int) {
	// don't let panics crash the process
	defer utilruntime.HandleCrash()
	// make sure the work queue is shutdown which will trigger workers to end
	defer c.queue.ShutDown()

	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues("controller_name", c.name)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting")

	// controller starts a single go func to poll and update subscriptions.
	// start up your worker threads based on threadiness.  Some controllers
	// have multiple kinds of workers
	for i := 0; i < threadiness; i++ {
		// runWorker will loop until "something bad" happens.  The .Until will
		// then rekick the worker after one second
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	go wait.JitterUntilWithContext(ctx, c.queueSync, time.Minute, 0.1, true)

	logger.Info("Started workers")

	// wait until we're told to stop
	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *subscriptionInformer) runWorker(ctx context.Context) {
	// hot loop until we're told to stop.  processNextWorkItem will
	// automatically wait until there's work available, so we don't worry
	// about secondary waits
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem deals with one item off the queue.  It returns false
// when it's time to quit.
func (c *subscriptionInformer) processNextWorkItem(ctx context.Context) bool {
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
	err := c.SyncOnce(ctx, ref)
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

func (c *subscriptionInformer) queueSync(ctx context.Context) {
	c.queue.Add("default")
}
