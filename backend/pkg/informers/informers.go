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

package informers

import (
	"context"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/utils/armhelpers"
)

// listWatchWithoutWatchListSemantics wraps a cache.ListWatch to opt out of
// WatchList semantics. Mirrors the unexported listWatcherWithWatchListSemanticsWrapper
// from client-go/tools/cache/listwatch.go.
// Client-go v0.35.1 enables WatchListClient by default, which requires the
// watch stream to send bookmark events. Our CosmosDB-backed informers use
// ExpiringWatcher which does not support this protocol.
type listWatchWithoutWatchListSemantics struct {
	*cache.ListWatch
}

func (listWatchWithoutWatchListSemantics) IsWatchListSemanticsUnSupported() bool { return true }

const (
	// These durations indicate the maximum time it will take for us to notice a new instance of a particular type.
	// Remember that these will not fire in order, so it's entirely possible to get an operation for subscription we have no observed.
	SubscriptionRelistDuration             = 30 * time.Minute
	ClusterRelistDuration                  = 30 * time.Minute
	NodePoolRelistDuration                 = 30 * time.Minute
	ExternalAuthRelistDuration             = 30 * time.Minute
	ServiceProviderClusterRelistDuration   = 30 * time.Minute
	ServiceProviderNodePoolRelistDuration  = 30 * time.Minute
	ControllerRelistDuration               = 30 * time.Minute
	AllOperationsRelistDuration            = 30 * time.Minute
	ActiveOperationsRelistDuration         = 30 * time.Minute
	ManagementClusterContentRelistDuration = 30 * time.Second
	BillingRelistDuration                  = 30 * time.Second
)

// NewSubscriptionInformer creates an unstarted SharedIndexInformer for subscriptions
// using the default relist duration.
func NewSubscriptionInformer(lister database.GlobalLister[arm.Subscription], cosmosClient database.ResourcesDBClient) cache.SharedIndexInformer {
	return NewSubscriptionInformerWithRelistDuration(lister, cosmosClient, SubscriptionRelistDuration)
}

// NewSubscriptionInformerWithRelistDuration creates an unstarted SharedIndexInformer for subscriptions
// with a configurable relist duration.
func NewSubscriptionInformerWithRelistDuration(lister database.GlobalLister[arm.Subscription], cosmosClient database.ResourcesDBClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := NewChangeFeedListWatcher[arm.Subscription, *arm.Subscription, database.GenericDocument[arm.Subscription]](
		[]azcorearm.ResourceType{azcorearm.NewResourceType("Microsoft.Resources", "subscriptions")},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
	)

	return cache.NewSharedIndexInformerWithOptions(
		&listWatchWithoutWatchListSemantics{lw.ToListWatch()},
		&arm.Subscription{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
		},
	)
}

// NewBillingInformer creates an unstarted SharedIndexInformer for billing documents
// with a subscription index using the default relist duration.
func NewBillingInformer(lister database.GlobalLister[database.BillingDocument]) cache.SharedIndexInformer {
	return NewBillingInformerWithRelistDuration(lister, BillingRelistDuration)
}

// NewBillingInformerWithRelistDuration creates an unstarted SharedIndexInformer for billing documents
// with a subscription index and a configurable relist duration.
func NewBillingInformerWithRelistDuration(lister database.GlobalLister[database.BillingDocument], relistDuration time.Duration) cache.SharedIndexInformer {
	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			logger := utils.LoggerFromContext(ctx)
			logger.Info("listing billing documents")
			defer logger.Info("finished listing billing documents")

			iter, err := lister.List(ctx, nil)
			if err != nil {
				return nil, err
			}

			list := &database.BillingDocumentList{}
			list.ResourceVersion = "0"
			for docID, doc := range iter.Items(ctx) {
				_ = docID
				list.Items = append(list.Items, *doc)
			}
			if err := iter.GetError(); err != nil {
				return nil, err
			}

			return list, nil
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			return NewExpiringWatcher(ctx, relistDuration), nil
		},
	}

	return cache.NewSharedIndexInformerWithOptions(
		&listWatchWithoutWatchListSemantics{lw},
		&database.BillingDocument{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
			Indexers: cache.Indexers{
				listers.BySubscription: billingDocSubscriptionIndexFunc,
			},
		},
	)
}

// NewClusterInformer creates an unstarted SharedIndexInformer for clusters
// with a resource group index using the default relist duration.
func NewClusterInformer(lister database.GlobalLister[api.HCPOpenShiftCluster], cosmosClient database.ResourcesDBClient) cache.SharedIndexInformer {
	return NewClusterInformerWithRelistDuration(lister, cosmosClient, ClusterRelistDuration)
}

// NewClusterInformerWithRelistDuration creates an unstarted SharedIndexInformer for clusters
// with a resource group index and a configurable relist duration.
func NewClusterInformerWithRelistDuration(lister database.GlobalLister[api.HCPOpenShiftCluster], cosmosClient database.ResourcesDBClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := NewChangeFeedListWatcher[api.HCPOpenShiftCluster, *api.HCPOpenShiftCluster, database.GenericDocument[api.HCPOpenShiftCluster]](
		[]azcorearm.ResourceType{api.ClusterResourceType},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
	)

	return cache.NewSharedIndexInformerWithOptions(
		&listWatchWithoutWatchListSemantics{lw.ToListWatch()},
		&api.HCPOpenShiftCluster{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
			Indexers: cache.Indexers{
				listers.ByResourceGroup: resourceGroupIndexFunc,
			},
		},
	)
}

// NewNodePoolInformer creates an unstarted SharedIndexInformer for node pools
// with resource group and cluster indexes using the default relist duration.
func NewNodePoolInformer(lister database.GlobalLister[api.HCPOpenShiftClusterNodePool], cosmosClient database.ResourcesDBClient) cache.SharedIndexInformer {
	return NewNodePoolInformerWithRelistDuration(lister, cosmosClient, NodePoolRelistDuration)
}

// NewNodePoolInformerWithRelistDuration creates an unstarted SharedIndexInformer for node pools
// with resource group and cluster indexes and a configurable relist duration.
func NewNodePoolInformerWithRelistDuration(lister database.GlobalLister[api.HCPOpenShiftClusterNodePool], cosmosClient database.ResourcesDBClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := NewChangeFeedListWatcher[api.HCPOpenShiftClusterNodePool, *api.HCPOpenShiftClusterNodePool, database.GenericDocument[api.HCPOpenShiftClusterNodePool]](
		[]azcorearm.ResourceType{api.NodePoolResourceType},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
	)

	return cache.NewSharedIndexInformerWithOptions(
		&listWatchWithoutWatchListSemantics{lw.ToListWatch()},
		&api.HCPOpenShiftClusterNodePool{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
			Indexers: cache.Indexers{
				listers.ByResourceGroup: resourceGroupIndexFunc,
				listers.ByCluster:       clusterResourceIDIndexFunc,
			},
		},
	)
}

// NewExternalAuthInformer creates an unstarted SharedIndexInformer for external auths
// with resource group and cluster indexes using the default relist duration.
func NewExternalAuthInformer(lister database.GlobalLister[api.HCPOpenShiftClusterExternalAuth], cosmosClient database.ResourcesDBClient) cache.SharedIndexInformer {
	return NewExternalAuthInformerWithRelistDuration(lister, cosmosClient, ExternalAuthRelistDuration)
}

// NewExternalAuthInformerWithRelistDuration creates an unstarted SharedIndexInformer for external auths
// with resource group and cluster indexes and a configurable relist duration.
func NewExternalAuthInformerWithRelistDuration(lister database.GlobalLister[api.HCPOpenShiftClusterExternalAuth], cosmosClient database.ResourcesDBClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := NewChangeFeedListWatcher[api.HCPOpenShiftClusterExternalAuth, *api.HCPOpenShiftClusterExternalAuth, database.GenericDocument[api.HCPOpenShiftClusterExternalAuth]](
		[]azcorearm.ResourceType{api.ExternalAuthResourceType},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
	)

	return cache.NewSharedIndexInformerWithOptions(
		&listWatchWithoutWatchListSemantics{lw.ToListWatch()},
		&api.HCPOpenShiftClusterExternalAuth{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
			Indexers: cache.Indexers{
				listers.ByResourceGroup: resourceGroupIndexFunc,
				listers.ByCluster:       clusterResourceIDIndexFunc,
			},
		},
	)
}

// NewServiceProviderClusterInformer creates an unstarted SharedIndexInformer for service provider clusters
// with a cluster index using the default relist duration.
func NewServiceProviderClusterInformer(lister database.GlobalLister[api.ServiceProviderCluster], cosmosClient database.ResourcesDBClient) cache.SharedIndexInformer {
	return NewServiceProviderClusterInformerWithRelistDuration(lister, cosmosClient, ServiceProviderClusterRelistDuration)
}

// NewServiceProviderClusterInformerWithRelistDuration creates an unstarted SharedIndexInformer for service provider clusters
// with a cluster index and a configurable relist duration.
func NewServiceProviderClusterInformerWithRelistDuration(lister database.GlobalLister[api.ServiceProviderCluster], cosmosClient database.ResourcesDBClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := NewChangeFeedListWatcher[api.ServiceProviderCluster, *api.ServiceProviderCluster, database.GenericDocument[api.ServiceProviderCluster]](
		[]azcorearm.ResourceType{api.ServiceProviderClusterResourceType},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
	)

	return cache.NewSharedIndexInformerWithOptions(
		&listWatchWithoutWatchListSemantics{lw.ToListWatch()},
		&api.ServiceProviderCluster{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
			Indexers: cache.Indexers{
				listers.ByCluster: clusterResourceIDIndexFunc,
			},
		},
	)
}

// NewManagementClusterContentInformer creates an unstarted SharedIndexInformer for management cluster contents
// with cluster and node pool indexes using the default relist duration.
func NewManagementClusterContentInformer(lister database.GlobalLister[api.ManagementClusterContent]) cache.SharedIndexInformer {
	return NewManagementClusterContentInformerWithRelistDuration(lister, ManagementClusterContentRelistDuration)
}

// NewManagementClusterContentInformerWithRelistDuration creates an unstarted SharedIndexInformer for management cluster contents
// with cluster and node pool indexes and a configurable relist duration.
func NewManagementClusterContentInformerWithRelistDuration(lister database.GlobalLister[api.ManagementClusterContent], relistDuration time.Duration) cache.SharedIndexInformer {
	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			logger := utils.LoggerFromContext(ctx)
			logger.Info("listing management cluster contents")
			defer logger.Info("finished listing management cluster contents")

			iter, err := lister.List(ctx, nil)
			if err != nil {
				return nil, err
			}

			list := &api.ManagementClusterContentList{}
			list.ResourceVersion = "0"
			for _, mcc := range iter.Items(ctx) {
				list.Items = append(list.Items, *mcc)
			}
			if err := iter.GetError(); err != nil {
				return nil, err
			}

			return list, nil
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			return NewExpiringWatcher(ctx, relistDuration), nil
		},
	}

	return cache.NewSharedIndexInformerWithOptions(
		&listWatchWithoutWatchListSemantics{lw},
		&api.ManagementClusterContent{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
			Indexers: cache.Indexers{
				listers.ByCluster:  clusterResourceIDIndexFunc,
				listers.ByNodePool: nodePoolResourceIDIndexFunc,
			},
		},
	)
}

// NewServiceProviderNodePoolInformer creates an unstarted SharedIndexInformer for service provider node pools
// with a node pool index using the default relist duration.
func NewServiceProviderNodePoolInformer(lister database.GlobalLister[api.ServiceProviderNodePool], cosmosClient database.ResourcesDBClient) cache.SharedIndexInformer {
	return NewServiceProviderNodePoolInformerWithRelistDuration(lister, cosmosClient, ServiceProviderNodePoolRelistDuration)
}

// NewServiceProviderNodePoolInformerWithRelistDuration creates an unstarted SharedIndexInformer for service provider node pools
// with a node pool index and a configurable relist duration.
func NewServiceProviderNodePoolInformerWithRelistDuration(lister database.GlobalLister[api.ServiceProviderNodePool], cosmosClient database.ResourcesDBClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := NewChangeFeedListWatcher[api.ServiceProviderNodePool, *api.ServiceProviderNodePool, database.GenericDocument[api.ServiceProviderNodePool]](
		[]azcorearm.ResourceType{api.ServiceProviderNodePoolResourceType},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
	)

	return cache.NewSharedIndexInformerWithOptions(
		&listWatchWithoutWatchListSemantics{lw.ToListWatch()},
		&api.ServiceProviderNodePool{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
			Indexers: cache.Indexers{
				listers.ByNodePool: nodePoolResourceIDIndexFunc,
			},
		},
	)
}

// NewControllerInformer creates an unstarted SharedIndexInformer for controllers
// using the default relist duration.
func NewControllerInformer(lister database.GlobalLister[api.Controller], cosmosClient database.ResourcesDBClient) cache.SharedIndexInformer {
	return NewControllerInformerWithRelistDuration(lister, cosmosClient, ControllerRelistDuration)
}

// NewControllerInformerWithRelistDuration creates an unstarted SharedIndexInformer for controllers
// with a configurable relist duration. Controllers live under three different
// ARM resource types (cluster-scoped, nodepool-scoped, externalauth-scoped) so
// the change feed filter accepts all three.
func NewControllerInformerWithRelistDuration(lister database.GlobalLister[api.Controller], cosmosClient database.ResourcesDBClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := NewChangeFeedListWatcher[api.Controller, *api.Controller, database.GenericDocument[api.Controller]](
		[]azcorearm.ResourceType{
			api.ClusterControllerResourceType,
			api.NodePoolControllerResourceType,
			api.ExternalAuthControllerResourceType,
		},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
	)

	return cache.NewSharedIndexInformerWithOptions(
		&listWatchWithoutWatchListSemantics{lw.ToListWatch()},
		&api.Controller{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour,
			Indexers: cache.Indexers{
				listers.ByResourceGroup: resourceGroupIndexFunc,
				listers.ByCluster:       clusterResourceIDIndexFunc,
				listers.ByNodePool:      nodePoolResourceIDIndexFunc,
				listers.ByExternalAuth:  externalAuthResourceIDIndexFunc,
			},
		},
	)
}

// NewOperationInformer creates an unstarted SharedIndexInformer for all
// operations (including terminal) using the default relist duration. This is
// used by the metrics controller so that completed operations remain visible
// in Prometheus until the 7-day Cosmos TTL removes them.
func NewOperationInformer(lister database.GlobalLister[api.Operation], cosmosClient database.ResourcesDBClient) cache.SharedIndexInformer {
	return NewOperationInformerWithRelistDuration(lister, cosmosClient, AllOperationsRelistDuration)
}

// NewOperationInformerWithRelistDuration creates an unstarted SharedIndexInformer
// for all operations (including terminal) with a configurable relist duration.
func NewOperationInformerWithRelistDuration(lister database.GlobalLister[api.Operation], cosmosClient database.ResourcesDBClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := NewChangeFeedListWatcher[api.Operation, *api.Operation, database.GenericDocument[api.Operation]](
		[]azcorearm.ResourceType{api.OperationStatusResourceType},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
	)

	return cache.NewSharedIndexInformerWithOptions(
		&listWatchWithoutWatchListSemantics{lw.ToListWatch()},
		&api.Operation{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour,
		},
	)
}

// NewActiveOperationInformer creates an unstarted SharedIndexInformer for
// active (non-terminal) operations with resource group and cluster indexes
// using the default relist duration.
func NewActiveOperationInformer(lister database.GlobalLister[api.Operation], cosmosClient database.ResourcesDBClient) cache.SharedIndexInformer {
	return NewActiveOperationInformerWithRelistDuration(lister, cosmosClient, ActiveOperationsRelistDuration)
}

// NewActiveOperationInformerWithRelistDuration creates an unstarted SharedIndexInformer for
// active (non-terminal) operations with resource group and cluster indexes
// and a configurable relist duration.
func NewActiveOperationInformerWithRelistDuration(lister database.GlobalLister[api.Operation], cosmosClient database.ResourcesDBClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := NewChangeFeedListWatcher[api.Operation, *api.Operation, database.GenericDocument[api.Operation]](
		[]azcorearm.ResourceType{api.OperationStatusResourceType},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
	)

	return cache.NewSharedIndexInformerWithOptions(
		&listWatchWithoutWatchListSemantics{lw.ToListWatch()},
		&api.Operation{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
			Indexers: cache.Indexers{
				listers.ByResourceGroup: activeOperationResourceGroupIndexFunc,
				listers.ByCluster:       activeOperationClusterIndexFunc,
				listers.ByNodePool:      activeOperationNodePoolIndexFunc,
				listers.ByExternalAuth:  activeOperationExternalAuthIndexFunc,
			},
		},
	)
}

func resourceGroupIndexFunc(obj interface{}) ([]string, error) {
	switch castObj := obj.(type) {
	case arm.CosmosMetadataAccessor:
		if castObj.GetResourceID() == nil {
			return nil, utils.TrackError(fmt.Errorf("obj is missing resourceID: %T %v", obj, obj))
		}
		return []string{api.ToResourceGroupResourceIDString(castObj.GetResourceID().SubscriptionID, castObj.GetResourceID().ResourceGroupName)}, nil
	case arm.CosmosPersistable:
		if castObj.GetCosmosData() == nil || castObj.GetCosmosData().ResourceID == nil {
			return nil, utils.TrackError(fmt.Errorf("obj is missing resourceID: %T %v", obj, obj))
		}
		return []string{api.ToResourceGroupResourceIDString(castObj.GetCosmosData().ResourceID.SubscriptionID, castObj.GetCosmosData().ResourceID.ResourceGroupName)}, nil
	default:
		return nil, utils.TrackError(fmt.Errorf("unexpected type %T, expected api.CosmosMetadataAccessor or api.CosmosPersistable", obj))
	}
}

// selfOrDirectParentResourceID returns the lowercased resource ID string of
// either resourceID itself (when its type matches) or its direct Parent (when
// the parent's type matches). It is non-recursive on purpose: indexing by
// "self-or-direct-parent" gives ListFor<Cluster|NodePool|ExternalAuth> the
// "direct child only" semantics we want, so e.g. a Controller hanging off a
// NodePool is indexed under that NodePool but NOT under the grandparent
// Cluster. If a future caller needs a deeper-ancestor lookup, add a separate
// helper for that case rather than reintroducing recursion here.
func selfOrDirectParentResourceID(resourceType azcorearm.ResourceType, resourceID *azcorearm.ResourceID) ([]string, error) {
	if resourceID == nil {
		return nil, nil
	}
	if armhelpers.ResourceTypeEqual(resourceID.ResourceType, resourceType) {
		return []string{strings.ToLower(resourceID.String())}, nil
	}
	if resourceID.Parent == nil {
		return nil, nil
	}
	if armhelpers.ResourceTypeEqual(resourceID.Parent.ResourceType, resourceType) {
		return []string{strings.ToLower(resourceID.Parent.String())}, nil
	}
	return nil, nil
}

func clusterResourceIDIndexFunc(obj interface{}) ([]string, error) {
	switch castObj := obj.(type) {
	case arm.CosmosMetadataAccessor:
		return clusterResourceIDFromResourceID(castObj.GetResourceID())
	case arm.CosmosPersistable:
		return clusterResourceIDFromResourceID(castObj.GetCosmosData().ResourceID)
	default:
		return nil, utils.TrackError(fmt.Errorf("unexpected type %T, expected api.CosmosMetadataAccessor or api.CosmosPersistable", obj))
	}
}

func clusterResourceIDFromResourceID(resourceID *azcorearm.ResourceID) ([]string, error) {
	return selfOrDirectParentResourceID(api.ClusterResourceType, resourceID)
}

func externalAuthResourceIDIndexFunc(obj interface{}) ([]string, error) {
	switch castObj := obj.(type) {
	case arm.CosmosMetadataAccessor:
		return externalAuthResourceIDFromResourceID(castObj.GetResourceID())
	case arm.CosmosPersistable:
		return externalAuthResourceIDFromResourceID(castObj.GetCosmosData().ResourceID)
	default:
		return nil, utils.TrackError(fmt.Errorf("unexpected type %T, expected api.CosmosMetadataAccessor or api.CosmosPersistable", obj))
	}
}

func externalAuthResourceIDFromResourceID(resourceID *azcorearm.ResourceID) ([]string, error) {
	return selfOrDirectParentResourceID(api.ExternalAuthResourceType, resourceID)
}

// activeOperationResourceGroupIndexFunc indexes operations by the resource group
// of their ExternalID.
func activeOperationResourceGroupIndexFunc(obj interface{}) ([]string, error) {
	op, ok := obj.(*api.Operation)
	if !ok {
		return nil, fmt.Errorf("expected *api.Operation, got %T", obj)
	}
	if op.ExternalID == nil {
		return nil, nil
	}

	return []string{api.ToResourceGroupResourceIDString(op.ExternalID.SubscriptionID, op.ExternalID.ResourceGroupName)}, nil
}

// activeOperationClusterIndexFunc indexes operations by their associated cluster
// resource ID, derived from ExternalID. If ExternalID is a cluster resource ID,
// it is used directly. If it is a child resource (nodepool, externalauth), the
// parent cluster resource ID is used.
func activeOperationClusterIndexFunc(obj interface{}) ([]string, error) {
	op, ok := obj.(*api.Operation)
	if !ok {
		return nil, fmt.Errorf("expected *api.Operation, got %T", obj)
	}

	return clusterResourceIDFromResourceID(op.ExternalID)
}

// activeOperationNodePoolIndexFunc indexes operations by their associated node pool
// resource ID, derived from ExternalID. If ExternalID is a node pool resource ID,
// it is used directly. If it is a descendant of a node pool, the parent node pool
// resource ID is used.
func activeOperationNodePoolIndexFunc(obj interface{}) ([]string, error) {
	op, ok := obj.(*api.Operation)
	if !ok {
		return nil, fmt.Errorf("expected *api.Operation, got %T", obj)
	}

	return nodePoolResourceIDFromResourceID(op.ExternalID)
}

// activeOperationExternalAuthIndexFunc indexes operations by their associated
// external auth resource ID, derived from ExternalID. If ExternalID is an external
// auth resource ID, it is used directly. If it is a descendant of an external auth,
// the parent external auth resource ID is used.
func activeOperationExternalAuthIndexFunc(obj interface{}) ([]string, error) {
	op, ok := obj.(*api.Operation)
	if !ok {
		return nil, fmt.Errorf("expected *api.Operation, got %T", obj)
	}

	return externalAuthResourceIDFromResourceID(op.ExternalID)
}

// nodePoolResourceIDIndexFunc indexes objects by the node pool resource ID of their nearest
// nodePool ancestor in the ARM path (Cosmos metadata resource ID).
func nodePoolResourceIDIndexFunc(obj interface{}) ([]string, error) {
	switch castObj := obj.(type) {
	case arm.CosmosMetadataAccessor:
		return nodePoolResourceIDFromResourceID(castObj.GetResourceID())
	case arm.CosmosPersistable:
		return nodePoolResourceIDFromResourceID(castObj.GetCosmosData().ResourceID)
	default:
		return nil, utils.TrackError(fmt.Errorf("unexpected type %T, expected arm.CosmosMetadataAccessor or arm.CosmosPersistable", obj))
	}
}

func nodePoolResourceIDFromResourceID(resourceID *azcorearm.ResourceID) ([]string, error) {
	return selfOrDirectParentResourceID(api.NodePoolResourceType, resourceID)
}

// billingDocSubscriptionIndexFunc indexes billing documents by their subscription ID.
func billingDocSubscriptionIndexFunc(obj interface{}) ([]string, error) {
	doc, ok := obj.(*database.BillingDocument)
	if !ok {
		return nil, utils.TrackError(fmt.Errorf("unexpected type %T, expected *database.BillingDocument", obj))
	}
	if doc.SubscriptionID == "" {
		return nil, nil
	}
	return []string{strings.ToLower(doc.SubscriptionID)}, nil
}
