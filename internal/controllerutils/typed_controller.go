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
	"time"

	"github.com/prometheus/client_golang/prometheus"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// TypedController reconciles comparable keys using a deduplicating, rate-limited
// workqueue. It waits for registered caches before starting workers and attaches
// controller and key context to each worker reconciliation.
type TypedController[T comparable] struct {
	CacheSyncWaiter
	name           string
	sync           func(context.Context, T) error
	reconcileTotal *prometheus.CounterVec

	queue workqueue.TypedRateLimitingInterface[T]
}

// NewTypedController creates a controller that delegates reconciliation to sync.
// A non-nil reconcileTotal counts worker attempts, labeled by controller name.
func NewTypedController[T comparable](name string, sync func(context.Context, T) error, reconcileTotal *prometheus.CounterVec) *TypedController[T] {
	return &TypedController[T]{
		name:           name,
		sync:           sync,
		reconcileTotal: reconcileTotal,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[T](),
			workqueue.TypedRateLimitingQueueConfig[T]{
				Name: name,
			},
		),
	}
}

// Enqueue schedules a key immediately and preserves its retry count.
func (c *TypedController[T]) Enqueue(key T) {
	c.queue.Add(key)
}

// EnqueueAfter schedules a key after a delay and preserves its retry count.
func (c *TypedController[T]) EnqueueAfter(key T, duration time.Duration) {
	c.queue.AddAfter(key, duration)
}

// SyncOnce invokes reconciliation directly. Run supplies worker logging, metrics,
// and retries when processing queued keys.
func (c *TypedController[T]) SyncOnce(ctx context.Context, key T) error {
	return c.sync(ctx, key)
}

// Run waits for caches, starts workers, and shuts down the queue when ctx is canceled.
func (c *TypedController[T]) Run(ctx context.Context, threadiness int) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	if !c.WaitForCacheSync(ctx) {
		return
	}

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

func (c *TypedController[T]) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem reconciles one key and returns false when the queue shuts down.
func (c *TypedController[T]) processNextWorkItem(ctx context.Context) bool {
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
