// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package controllers

import (
	"context"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	backendutils "github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/controllerregistry"
	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
)

// Both registrations use this identical controller. Only injection changes its
// transitive accesses: first reads Subscription, second reads ExternalAuth.
// Both watch Cluster + ServiceProviderCluster, read Cluster, and write NodePool.
type controller struct {
	lister corelisters.ClusterLister
	lookup Lookup
}

func newController(c controllerContext) controllerregistry.Runnable {
	clusterInformer, clusterLister := c.Informers.Clusters()
	providerInformer, _ := c.Informers.ServiceProviderClusters()
	syncer := &controller{lister: clusterLister, lookup: c.Lookup}
	watcher := controllerutils.NewGenericWatchingController("fixture", coreapi.ClusterResourceType, syncer, nil)
	if err := watcher.QueueForInformers(time.Minute, clusterInformer, providerInformer); err != nil {
		panic(err)
	}
	return watcher
}

func (c *controller) SyncOnce(ctx context.Context, key string) error {
	if _, err := c.lister.Get(ctx, "subscription", "rg", key); err != nil {
		return err
	}
	crud := c.lookup.Resolve(ctx)
	return writeNodePool(ctx, crud)
}

func writeNodePool(ctx context.Context, crud cosmosstorageutils.ResourceCRUD[coreapi.NodePool, *coreapi.NodePool]) error {
	_, err := crud.Replace(ctx, &coreapi.NodePool{}, nil)
	return err
}

func (c *controller) MakeKey(id *azcorearm.ResourceID) string          { return id.Name }
func (c *controller) CooldownChecker() controllerutils.CooldownChecker { return nil }

type Lookup interface {
	Resolve(context.Context) cosmosstorageutils.ResourceCRUD[coreapi.NodePool, *coreapi.NodePool]
}
type subscriptionLookup struct {
	resources corecosmosstorage.ResourcesDBClient
}
type externalAuthLookup struct {
	resources corecosmosstorage.ResourcesDBClient
}

func (l *subscriptionLookup) Resolve(ctx context.Context) cosmosstorageutils.ResourceCRUD[coreapi.NodePool, *coreapi.NodePool] {
	_, _ = l.resources.Subscriptions().Get(ctx, "subscription")
	return l.resources.HCPClusters("subscription", "rg").NodePools("cluster")
}

func (l *externalAuthLookup) Resolve(ctx context.Context) cosmosstorageutils.ResourceCRUD[coreapi.NodePool, *coreapi.NodePool] {
	_, _ = l.resources.HCPClusters("subscription", "rg").ExternalAuth("cluster").Get(ctx, "auth")
	return l.resources.HCPClusters("subscription", "rg").NodePools("cluster")
}

// This separate case pins the real backend wrapper's inherited watch/read
// behavior and verifies that its own Controller status persistence is exempt.
type wrapped struct {
	provider corelisters.ServiceProviderClusterLister
}

func newWrappedController(c controllerContext) controllerregistry.Runnable {
	_, lister := c.Informers.ServiceProviderClusters()
	return backendutils.NewClusterWatchingController("fixture-wrapped", c.Resources, c.Informers, nil, time.Minute, &wrapped{provider: lister})
}
func (c *wrapped) SyncOnce(ctx context.Context, key backendutils.HCPClusterKey) error {
	_, err := c.provider.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	return err
}
