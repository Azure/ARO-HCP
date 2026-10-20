// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package controllerutils

import (
	"context"
	"time"

	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
)

// WatchSubscriptionsForClusters fans out only within the changed subscription,
// using the ClusterLister's BySubscription index rather than a global scan.
func WatchSubscriptionsForClusters(controller WatchingController, informers coreinformers.BackendInformers) error {
	source, _ := informers.Subscriptions()
	clusters, lister := informers.Clusters()
	return controller.QueueForMappedInformer(time.Minute, source, func(ctx context.Context, id *azcorearm.ResourceID) ([]*azcorearm.ResourceID, error) {
		if !metadataapi.ResourceTypeEqual(id.ResourceType, azcorearm.SubscriptionResourceType) {
			return nil, nil
		}
		items, err := lister.ListForSubscription(ctx, id.SubscriptionID)
		if err != nil {
			return nil, err
		}
		var ids []*azcorearm.ResourceID
		for _, item := range items {
			ids = append(ids, item.GetResourceID())
		}
		return ids, nil
	}, clusters.HasSynced)
}

// WatchClusterParentsForNodePools subscribes to explicit Cluster/provider sources
// and maps them through the node-pool lister's cluster index.
func WatchClusterParentsForNodePools(controller WatchingController, informers coreinformers.BackendInformers, sources ...cache.SharedIndexInformer) error {
	informer, lister := informers.NodePools()
	mapper := func(ctx context.Context, id *azcorearm.ResourceID) ([]*azcorearm.ResourceID, error) {
		items, err := lister.ListForCluster(ctx, id.SubscriptionID, id.ResourceGroupName, id.Name)
		if err != nil {
			return nil, err
		}
		var ids []*azcorearm.ResourceID
		for _, item := range items {
			ids = append(ids, item.GetResourceID())
		}
		return ids, nil
	}
	return watchClusterParents(controller, mapper, informer.HasSynced, sources...)
}

// WatchClusterParentsForExternalAuths maps parent changes to cached external auths.
func WatchClusterParentsForExternalAuths(controller WatchingController, informers coreinformers.BackendInformers, sources ...cache.SharedIndexInformer) error {
	informer, lister := informers.ExternalAuths()
	mapper := func(ctx context.Context, id *azcorearm.ResourceID) ([]*azcorearm.ResourceID, error) {
		items, err := lister.ListForCluster(ctx, id.SubscriptionID, id.ResourceGroupName, id.Name)
		if err != nil {
			return nil, err
		}
		var ids []*azcorearm.ResourceID
		for _, item := range items {
			ids = append(ids, item.GetResourceID())
		}
		return ids, nil
	}
	return watchClusterParents(controller, mapper, informer.HasSynced, sources...)
}

// WatchClusterParentsForCredentialRequests maps parent changes to cached requests.
func WatchClusterParentsForCredentialRequests(controller WatchingController, informers coreinformers.BackendInformers, sources ...cache.SharedIndexInformer) error {
	informer, lister := informers.SystemAdminCredentialRequests()
	mapper := func(ctx context.Context, id *azcorearm.ResourceID) ([]*azcorearm.ResourceID, error) {
		items, err := lister.ListForCluster(ctx, id.SubscriptionID, id.ResourceGroupName, id.Name)
		if err != nil {
			return nil, err
		}
		var ids []*azcorearm.ResourceID
		for _, item := range items {
			ids = append(ids, item.GetResourceID())
		}
		return ids, nil
	}
	return watchClusterParents(controller, mapper, informer.HasSynced, sources...)
}

// WatchClusterParentsForCredentialRevocations maps parent changes to cached revocations.
func WatchClusterParentsForCredentialRevocations(controller WatchingController, informers coreinformers.BackendInformers, sources ...cache.SharedIndexInformer) error {
	informer, lister := informers.SystemAdminCredentialRevocations()
	mapper := func(ctx context.Context, id *azcorearm.ResourceID) ([]*azcorearm.ResourceID, error) {
		items, err := lister.ListForCluster(ctx, id.SubscriptionID, id.ResourceGroupName, id.Name)
		if err != nil {
			return nil, err
		}
		var ids []*azcorearm.ResourceID
		for _, item := range items {
			ids = append(ids, item.GetResourceID())
		}
		return ids, nil
	}
	return watchClusterParents(controller, mapper, informer.HasSynced, sources...)
}

// Share only normalization and registration; each exported helper has one
// statically known lister dependency and its own cache-readiness requirement.
func watchClusterParents(controller WatchingController, mapper controllerutil.ResourceMapper, synced cache.InformerSynced, sources ...cache.SharedIndexInformer) error {
	for _, source := range sources {
		err := controller.QueueForMappedInformer(time.Minute, source, func(ctx context.Context, id *azcorearm.ResourceID) ([]*azcorearm.ResourceID, error) {
			if metadataapi.ResourceTypeEqual(id.ResourceType, coreapi.ServiceProviderClusterResourceType) {
				id = id.Parent
			}
			if id == nil || !metadataapi.ResourceTypeEqual(id.ResourceType, coreapi.ClusterResourceType) {
				return nil, nil
			}
			return mapper(ctx, id)
		}, synced)
		if err != nil {
			return err
		}
	}
	return nil
}

// WatchSubscriptionsForNodePools uses two indexed lookups (subscription ->
// clusters -> node pools), since node pools do not have a subscription index.
func WatchSubscriptionsForNodePools(controller WatchingController, informers coreinformers.BackendInformers) error {
	source, _ := informers.Subscriptions()
	clusters, clusterLister := informers.Clusters()
	nodePools, nodePoolLister := informers.NodePools()
	return controller.QueueForMappedInformer(time.Minute, source, func(ctx context.Context, id *azcorearm.ResourceID) ([]*azcorearm.ResourceID, error) {
		if !metadataapi.ResourceTypeEqual(id.ResourceType, azcorearm.SubscriptionResourceType) {
			return nil, nil
		}
		items, err := clusterLister.ListForSubscription(ctx, id.SubscriptionID)
		if err != nil {
			return nil, err
		}
		var ids []*azcorearm.ResourceID
		for _, cluster := range items {
			clusterID := cluster.GetResourceID()
			if clusterID == nil {
				continue
			}
			children, err := nodePoolLister.ListForCluster(ctx, clusterID.SubscriptionID, clusterID.ResourceGroupName, clusterID.Name)
			if err != nil {
				return nil, err
			}
			for _, child := range children {
				ids = append(ids, child.GetResourceID())
			}
		}
		return ids, nil
	}, clusters.HasSynced, nodePools.HasSynced)
}
