// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package controllerutils

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

type mappedRegistration struct {
	cache.ResourceEventHandlerRegistration
	ready atomic.Bool
}

func (r *mappedRegistration) HasSynced() bool { return r.ready.Load() }

type mappedInformer struct {
	cache.SharedIndexInformer
	handler      cache.ResourceEventHandler
	registration mappedRegistration
	ready        atomic.Bool
	err          error
}

func (i *mappedInformer) HasSynced() bool { return i.ready.Load() }
func (i *mappedInformer) AddEventHandlerWithOptions(h cache.ResourceEventHandler, _ cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error) {
	i.handler = h
	return &i.registration, i.err
}

func TestMappedWatchEvents(t *testing.T) {
	parent := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub1"))
	child := metadataapi.Must(azcorearm.ParseResourceID(testClusterARMID))
	for _, event := range []string{"add", "update", "resync", "delete", "tombstone", "pointer tombstone"} {
		t.Run(event, func(t *testing.T) {
			c := NewGenericWatchingController("mapped", coreapi.ClusterResourceType, &stringSyncer{cooldown: neverAllowCooldown{}}, nil)
			t.Cleanup(c.queue.ShutDown)
			c.AddInterestingResource(azcorearm.SubscriptionResourceType, "*")
			source := &mappedInformer{}
			calls := 0
			require.NoError(t, c.QueueForMappedInformer(time.Minute, source, func(_ context.Context, id *azcorearm.ResourceID) ([]*azcorearm.ResourceID, error) {
				calls++
				require.Equal(t, parent.String(), id.String())
				return []*azcorearm.ResourceID{child, child, nil, parent}, nil
			}))
			obj := &coreapi.CosmosMetadata{ResourceID: parent}
			switch event {
			case "add":
				source.handler.OnAdd(obj, false)
			case "update":
				source.handler.OnUpdate(obj.DeepCopy(), obj)
			case "resync":
				source.handler.OnUpdate(obj, obj)
			case "delete":
				source.handler.OnDelete(obj)
			case "tombstone":
				source.handler.OnDelete(cache.DeletedFinalStateUnknown{Obj: obj})
			case "pointer tombstone":
				source.handler.OnDelete(&cache.DeletedFinalStateUnknown{Obj: obj})
			}
			require.Positive(t, calls)
			require.Equal(t, []string{child.String()}, popAllQueue(c), "mapped keys bypass child interest/cooldown and are deduplicated; wrong key types are ignored")
			source.handler.OnAdd(&coreapi.CosmosMetadata{ResourceID: child}, false)
			source.handler.OnDelete(cache.DeletedFinalStateUnknown{Obj: "bad"})
			source.handler.OnDelete((*cache.DeletedFinalStateUnknown)(nil))
			require.Empty(t, popAllQueue(c), "irrelevant source types and malformed tombstones are ignored")
		})
	}
}

func TestMappedWatchRetriesAndOldNewParents(t *testing.T) {
	c, child, _ := newTestWatchingController()
	t.Cleanup(c.queue.ShutDown)
	source := &mappedInformer{}
	fail := true
	var seen []string
	require.NoError(t, c.QueueForMappedInformer(time.Minute, source, func(_ context.Context, id *azcorearm.ResourceID) ([]*azcorearm.ResourceID, error) {
		seen = append(seen, id.Name)
		if fail {
			return nil, errors.New("cache unavailable")
		}
		return []*azcorearm.ResourceID{child}, nil
	}))
	old := &coreapi.CosmosMetadata{ResourceID: metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/old"))}
	newObj := &coreapi.CosmosMetadata{ResourceID: metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/new"))}
	source.handler.OnAdd(old, false)
	require.Empty(t, popAllQueue(c))
	fail = false
	source.handler.OnUpdate(old, newObj)
	require.Equal(t, []string{"old", "old", "new"}, seen, "updates map both old and new relationships")
	require.Equal(t, []string{child.String()}, popAllQueue(c))
	source.handler.OnUpdate(old, old)
	require.Equal(t, []string{child.String()}, popAllQueue(c), "unchanged resync retries a failed mapping")
	require.Error(t, c.QueueForMappedInformer(0, source, nil), "mapping errors need a periodic retry source")
	failure := errors.New("registration failed")
	require.ErrorIs(t, c.QueueForMappedInformer(time.Minute, &mappedInformer{err: failure}, func(context.Context, *azcorearm.ResourceID) ([]*azcorearm.ResourceID, error) { return nil, nil }), failure)
}

func TestMappedWatchReadiness(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _, _ := newTestWatchingController()
		defer c.queue.ShutDown()
		source := &mappedInformer{}
		var childrenReady atomic.Bool
		require.NoError(t, c.QueueForMappedInformer(time.Minute, source, func(context.Context, *azcorearm.ResourceID) ([]*azcorearm.ResourceID, error) { return nil, nil }, childrenReady.Load))
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		result := make(chan bool, 1)
		go func() { result <- c.WaitForCacheSync(ctx) }()
		synctest.Wait()
		require.Empty(t, result)
		source.ready.Store(true)
		source.registration.ready.Store(true)
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		require.Empty(t, result, "child lister cache must be ready too")
		childrenReady.Store(true)
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		require.True(t, <-result)
	})
}
