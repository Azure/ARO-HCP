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

type childTestRegistration struct {
	cache.ResourceEventHandlerRegistration
	ready atomic.Bool
}

func (r *childTestRegistration) HasSynced() bool { return r.ready.Load() }

type childTestNotifier struct {
	handler      cache.ResourceEventHandler
	registration childTestRegistration
	ready        atomic.Bool
	err          error
	options      cache.HandlerOptions
}

func (n *childTestNotifier) AddEventHandlerWithOptions(h cache.ResourceEventHandler, options cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error) {
	n.handler = h
	n.options = options
	return &n.registration, n.err
}

func TestEveryGenericWatchHandlesDeletion(t *testing.T) {
	for _, api := range []string{"ordinary", "depth-limited", "child", "descendant"} {
		t.Run(api, func(t *testing.T) {
			c, cluster, _ := newTestWatchingController()
			t.Cleanup(c.queue.ShutDown)
			n := &childTestNotifier{}
			var err error
			switch api {
			case "ordinary":
				err = c.QueueForInformers(0, n)
			case "depth-limited":
				err = c.QueueForInformersWithMaxDepth(0, 1, n)
			case "child":
				err = c.QueueForChildInformers(0, n)
			case "descendant":
				err = c.QueueForDescendantInformers(0, 2, n)
			}
			require.NoError(t, err)
			require.NotNil(t, n.options.ResyncPeriod)
			require.Zero(t, *n.options.ResyncPeriod, "zero must disable synthetic resync")
			id := metadataapi.Must(azcorearm.ParseResourceID(testClusterARMID + "/applyDesires/a"))
			n.handler.OnDelete(cache.DeletedFinalStateUnknown{Obj: &coreapi.CosmosMetadata{ResourceID: id}})
			require.Equal(t, []string{cluster.String()}, popAllQueue(c), "all APIs must deliver tombstone deletes")
		})
	}
}
func (n *childTestNotifier) HasSynced() bool { return n.ready.Load() }

func TestChildWatchOwnerMappingAndDelete(t *testing.T) {
	for _, owner := range []string{testClusterARMID, testNodePoolARMID, testClusterARMID + "/externalAuths/auth", testClusterARMID + "/systemAdminCredentialRequests/cred", testClusterARMID + "/systemAdminCredentialRevocations/revoke"} {
		t.Run(owner, func(t *testing.T) {
			id := metadataapi.Must(azcorearm.ParseResourceID(owner))
			child := metadataapi.Must(azcorearm.ParseResourceID(owner + "/applyDesires/desired"))
			c := NewGenericWatchingController("child-test", id.ResourceType, &stringSyncer{cooldown: neverAllowCooldown{}}, nil)
			t.Cleanup(c.queue.ShutDown)
			// Register both deep and direct types so maxDepth, rather than the
			// interest filter alone, must reject a grandchild.
			deep := metadataapi.Must(azcorearm.ParseResourceID(owner + "/otherParents/nested/applyDesires/desired"))
			c.AddInterestingResource(child.ResourceType, "desired")
			c.AddInterestingResource(deep.ResourceType, "desired")
			n := &childTestNotifier{}
			require.NoError(t, c.QueueForChildInformers(time.Minute, n))
			obj := &coreapi.CosmosMetadata{ResourceID: child}
			obj.SetEtag("new")
			old := obj.DeepCopy()
			old.SetEtag("old")
			for _, event := range []string{"add", "update", "delete", "tombstone", "pointer tombstone"} {
				t.Run(event, func(t *testing.T) {
					switch event {
					case "add":
						n.handler.OnAdd(obj, false)
					case "update":
						n.handler.OnUpdate(old, obj)
					case "delete":
						n.handler.OnDelete(obj)
					case "tombstone":
						n.handler.OnDelete(cache.DeletedFinalStateUnknown{Obj: obj})
					case "pointer tombstone":
						n.handler.OnDelete(&cache.DeletedFinalStateUnknown{Obj: obj})
					}
					require.Equal(t, []string{owner}, popAllQueue(c), "child changes bypass resync cooldown and enqueue the owner")
				})
			}
			n.handler.OnDelete(&coreapi.CosmosMetadata{ResourceID: deep})
			wrongName := metadataapi.Must(azcorearm.ParseResourceID(owner + "/applyDesires/unrelated"))
			n.handler.OnDelete(&coreapi.CosmosMetadata{ResourceID: wrongName})
			n.handler.OnDelete(cache.DeletedFinalStateUnknown{Obj: "bad object"})
			n.handler.OnDelete((*cache.DeletedFinalStateUnknown)(nil))
			require.Empty(t, popAllQueue(c), "grandchildren, unrelated names and malformed tombstones must not enqueue")
		})
	}
}

func TestChildWatchCacheReadiness(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _, _ := newTestWatchingController()
		defer c.queue.ShutDown()
		n := &childTestNotifier{}
		require.NoError(t, c.QueueForChildInformers(time.Minute, n))
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		result := make(chan bool, 1)
		go func() { result <- c.WaitForCacheSync(ctx) }()
		synctest.Wait()
		require.Empty(t, result, "wait for source cache")
		n.ready.Store(true)
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		require.Empty(t, result, "source sync alone does not establish handler readiness")
		n.registration.ready.Store(true)
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		require.True(t, <-result)
	})
	t.Run("registration errors propagate", func(t *testing.T) {
		c, _, _ := newTestWatchingController()
		defer c.queue.ShutDown()
		failure := errors.New("registration failed")
		require.ErrorIs(t, c.QueueForChildInformers(time.Minute, &childTestNotifier{err: failure}), failure)
	})
}

func TestDescendantWatchExplicitDepth(t *testing.T) {
	c, _, _ := newTestWatchingController()
	defer c.queue.ShutDown()
	n := &childTestNotifier{}
	require.Error(t, c.QueueForDescendantInformers(time.Minute, 0, n))
	require.NoError(t, c.QueueForDescendantInformers(time.Minute, 2, n))
	id := metadataapi.Must(azcorearm.ParseResourceID(testNodePoolARMID + "/readDesires/r"))
	n.handler.OnDelete(&coreapi.CosmosMetadata{ResourceID: id})
	require.Equal(t, []string{testClusterARMID}, popAllQueue(c))
	id = metadataapi.Must(azcorearm.ParseResourceID(testNodePoolARMID + "/nested/n/readDesires/r"))
	n.handler.OnDelete(&coreapi.CosmosMetadata{ResourceID: id})
	require.Empty(t, popAllQueue(c))
}
