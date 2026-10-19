// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package controllerutils

import (
	"context"
	"fmt"
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

// WatchClusterParents maps Cluster and/or ServiceProviderCluster events to a
// chosen child collection. A provider document belongs to the same cluster as
// the children, so its immediate parent supplies the indexed lookup scope.
// Sources are explicit: callers subscribe only to the parent state they read.
func WatchClusterParents(controller WatchingController, informers coreinformers.BackendInformers, childType azcorearm.ResourceType, sources ...cache.SharedIndexInformer) error {
	var mapper controllerutil.ResourceMapper
	var synced cache.InformerSynced
	switch {
	case metadataapi.ResourceTypeEqual(childType, coreapi.NodePoolResourceType):
		informer, lister := informers.NodePools()
		synced = informer.HasSynced
		mapper = func(ctx context.Context, id *azcorearm.ResourceID) ([]*azcorearm.ResourceID, error) {
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
	case metadataapi.ResourceTypeEqual(childType, coreapi.ExternalAuthResourceType):
		informer, lister := informers.ExternalAuths()
		synced = informer.HasSynced
		mapper = func(ctx context.Context, id *azcorearm.ResourceID) ([]*azcorearm.ResourceID, error) {
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
	case metadataapi.ResourceTypeEqual(childType, coreapi.SystemAdminCredentialRequestResourceType):
		informer, lister := informers.SystemAdminCredentialRequests()
		synced = informer.HasSynced
		mapper = func(ctx context.Context, id *azcorearm.ResourceID) ([]*azcorearm.ResourceID, error) {
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
	case metadataapi.ResourceTypeEqual(childType, coreapi.SystemAdminCredentialRevocationResourceType):
		informer, lister := informers.SystemAdminCredentialRevocations()
		synced = informer.HasSynced
		mapper = func(ctx context.Context, id *azcorearm.ResourceID) ([]*azcorearm.ResourceID, error) {
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
	default:
		return fmt.Errorf("unsupported cluster child type %s", childType)
	}
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
