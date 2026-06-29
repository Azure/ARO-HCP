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

package metrics

import (
	"context"
	"fmt"
	"strings"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Handler emits and deletes Prometheus metrics for a single Cosmos-backed type.
type Handler[T arm.CosmosPersistable] interface {
	Sync(ctx context.Context, obj T)
	Delete(key string)
}

// Controller watches a SharedIndexInformer and keeps metrics level-driven from
// the current cache contents.
type Controller[T arm.CosmosPersistable] struct {
	name    string
	indexer cache.Indexer
	queue   workqueue.TypedRateLimitingInterface[string]
	handler Handler[T]
}

// NewController creates a metrics controller for the given informer/handler pair.
func NewController[T arm.CosmosPersistable](
	name string,
	informer cache.SharedIndexInformer,
	handler Handler[T],
) *Controller[T] {
	c := &Controller[T]{
		name:    name,
		indexer: informer.GetIndexer(),
		handler: handler,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: name,
			},
		),
	}

	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    c.enqueue,
		UpdateFunc: func(_, newObj interface{}) { c.enqueue(newObj) },
		DeleteFunc: c.enqueue,
	})
	if err != nil {
		panic(err)
	}

	return c
}

func (c *Controller[T]) enqueue(obj interface{}) {
	key, err := resourceIDStoreKeyForObject(obj)
	if err != nil {
		logger := utils.DefaultLogger()
		logger = logger.WithValues(utils.LogValues{}.AddControllerName(c.name)...)
		logger.Error(err, "failed to compute key")
		return
	}
	c.queue.Add(key)
}

// Run starts the controller workers and blocks until ctx is cancelled.
func (c *Controller[T]) Run(ctx context.Context, threadiness int) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

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

func (c *Controller[T]) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *Controller[T]) processNextWorkItem(ctx context.Context) bool {
	key, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(key)

	controllerutils.ReconcileTotal.WithLabelValues(c.name).Inc()
	if err := c.syncResource(ctx, key); err != nil {
		utilruntime.HandleErrorWithContext(ctx, err, "Error syncing metrics", "key", key)
		c.queue.AddRateLimited(key)
		return true
	}

	c.queue.Forget(key)
	return true
}

func (c *Controller[T]) syncResource(ctx context.Context, key string) error {
	obj, exists, err := c.indexer.GetByKey(key)
	if err != nil {
		return err
	}

	if !exists {
		c.handler.Delete(key)
		return nil
	}

	typed, ok := obj.(T)
	if !ok {
		logger := utils.LoggerFromContext(ctx)
		logger.Info("unexpected object type in indexer", "key", key, "controller", c.name, "type", fmt.Sprintf("%T", obj))
		c.handler.Delete(key)
		return nil
	}

	c.handler.Sync(ctx, typed)
	return nil
}

func resourceIDStoreKeyForObject(obj interface{}) (string, error) {
	switch typed := obj.(type) {
	case cache.DeletedFinalStateUnknown:
		return resourceIDStoreKeyForTombstone(typed.Key, typed.Obj)
	case *cache.DeletedFinalStateUnknown:
		if typed == nil {
			return "", fmt.Errorf("tombstone missing key and object")
		}
		return resourceIDStoreKeyForTombstone(typed.Key, typed.Obj)
	case arm.CosmosPersistable:
		return resourceIDStoreKey(typed)
	default:
		return "", fmt.Errorf("unexpected object type %T", obj)
	}
}

const maxTombstoneUnwrapDepth = 32

func resourceIDStoreKeyForTombstone(key string, obj interface{}) (string, error) {
	if len(key) != 0 {
		return strings.ToLower(key), nil
	}

	current := obj
	for depth := 0; depth < maxTombstoneUnwrapDepth; depth++ {
		switch typed := current.(type) {
		case nil:
			return "", fmt.Errorf("tombstone missing key and object")
		case cache.DeletedFinalStateUnknown:
			if len(typed.Key) != 0 {
				return strings.ToLower(typed.Key), nil
			}
			current = typed.Obj
		case *cache.DeletedFinalStateUnknown:
			if typed == nil {
				return "", fmt.Errorf("tombstone missing key and object")
			}
			if len(typed.Key) != 0 {
				return strings.ToLower(typed.Key), nil
			}
			current = typed.Obj
		case arm.CosmosPersistable:
			return resourceIDStoreKey(typed)
		default:
			return "", fmt.Errorf("unexpected object type %T", current)
		}
	}

	return "", fmt.Errorf("tombstone exceeded max unwrap depth")
}

func resourceIDStoreKey(obj arm.CosmosPersistable) (string, error) {
	cosmosData := obj.GetCosmosData()
	if cosmosData == nil || cosmosData.GetResourceID() == nil {
		return "", fmt.Errorf("object %T is missing a resource ID", obj)
	}
	return ResourceIDMetricLabel(cosmosData.GetResourceID()), nil
}

func ResourceIDMetricLabel(resourceID *azcorearm.ResourceID) string {
	if resourceID == nil {
		return ""
	}
	return strings.ToLower(resourceID.String())
}

func SubscriptionIDMetricLabel(resourceID *azcorearm.ResourceID) string {
	if resourceID == nil {
		return ""
	}
	return strings.ToLower(resourceID.SubscriptionID)
}

func resourceIDToTypeMetricLabel(resourceID *azcorearm.ResourceID) string {
	if resourceID == nil {
		return "unknown"
	}
	return strings.ToLower(resourceID.ResourceType.String())
}

func phaseMetricLabel(status arm.ProvisioningState) string {
	return strings.ToLower(string(status))
}
