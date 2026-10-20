// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package controllerutils

import (
	"context"
	"fmt"
	"time"

	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ResourceMapper resolves an input event to reconciliation resources using
// informer-backed listers. Implementations must not perform network requests.
type ResourceMapper func(context.Context, *azcorearm.ResourceID) ([]*azcorearm.ResourceID, error)

// QueueForMappedInformer supports relationships that ancestor traversal cannot
// express. The interest filter applies to the original event, not mapped keys.
// Periodic source resync retries mapping errors; primary-resource initial lists
// and resyncs cover children not yet present in the cache during a parent event.
func (c *GenericWatchingController[T]) QueueForMappedInformer(resync time.Duration, source cache.SharedIndexInformer, mapper ResourceMapper, dependencies ...cache.InformerSynced) error {
	if resync <= 0 {
		return fmt.Errorf("mapped watches require a positive resync period")
	}
	if source == nil || mapper == nil {
		return fmt.Errorf("mapped watches require a source and mapper")
	}
	logger := utils.DefaultLogger().WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx := utils.ContextWithControllerName(context.Background(), c.name)
	ctx = utils.ContextWithLogger(ctx, logger)
	enqueue := func(obj any) {
		switch tombstone := obj.(type) {
		case cache.DeletedFinalStateUnknown:
			obj = tombstone.Obj
		case *cache.DeletedFinalStateUnknown:
			if tombstone == nil {
				return
			}
			obj = tombstone.Obj
		}
		resource, ok := obj.(coreapi.CosmosPersistable)
		if !ok || resource.GetCosmosData() == nil {
			return
		}
		id := resource.GetCosmosData().GetResourceID()
		if id == nil || !c.isResourceInteresting(id) {
			return
		}
		ids, err := mapper(ctx, id)
		if err != nil {
			logger.Error(err, "Cannot map parent event; source resync will retry", "resourceID", id.String())
			return
		}
		for _, child := range ids {
			if child == nil || !metadataapi.ResourceTypeEqual(child.ResourceType, c.resourceType) {
				continue
			}
			// A parent change invalidates child state independently of the child's
			// own ETag/cooldown. Queue deduplication still bounds repeated keys.
			c.queue.Add(c.syncer.MakeKey(child))
		}
	}
	registration, err := source.AddEventHandlerWithOptions(cache.ResourceEventHandlerFuncs{
		AddFunc: enqueue, DeleteFunc: enqueue,
		UpdateFunc: func(oldObj, newObj any) { enqueue(oldObj); enqueue(newObj) },
	}, cache.HandlerOptions{Logger: &logger, ResyncPeriod: ptr.To(resync)})
	if err != nil {
		return err
	}
	c.AddCacheSyncs(source.HasSynced)
	c.AddCacheSyncs(dependencies...)
	if registration != nil {
		c.AddCacheSyncs(registration.HasSynced)
	}
	return nil
}
