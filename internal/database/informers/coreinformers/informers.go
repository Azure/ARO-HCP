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

package coreinformers

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

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/informerutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/utils/armhelpers"
)

const (
	// These durations indicate the maximum time it will take for us to notice a new instance of a particular type.
	// Remember that these will not fire in order, so it's entirely possible to get an operation for subscription we have no observed.
	SubscriptionRelistDuration                    = 30 * time.Minute
	ClusterRelistDuration                         = 30 * time.Minute
	NodePoolRelistDuration                        = 30 * time.Minute
	ExternalAuthRelistDuration                    = 30 * time.Minute
	ServiceProviderClusterRelistDuration          = 30 * time.Minute
	ServiceProviderNodePoolRelistDuration         = 30 * time.Minute
	ControllerRelistDuration                      = 30 * time.Minute
	AllOperationsRelistDuration                   = 30 * time.Minute
	ActiveOperationsRelistDuration                = 30 * time.Minute
	ManagementClusterContentRelistDuration        = 30 * time.Second
	SystemAdminCredentialRequestRelistDuration    = 30 * time.Minute
	SystemAdminCredentialRevocationRelistDuration = 30 * time.Minute
	BillingRelistDuration                         = 30 * time.Second
)

// NewSubscriptionInformer creates an unstarted SharedIndexInformer for subscriptions
// using the default relist duration.
func NewSubscriptionInformer(lister cosmosstorageutils.GlobalLister[arm.Subscription], cosmosClient cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
	return NewSubscriptionInformerWithRelistDuration(lister, cosmosClient, SubscriptionRelistDuration)
}

// NewSubscriptionInformerWithRelistDuration creates an unstarted SharedIndexInformer for subscriptions
// with a configurable relist duration.
func NewSubscriptionInformerWithRelistDuration(lister cosmosstorageutils.GlobalLister[arm.Subscription], cosmosClient cosmosstorageutils.ChangeFeedClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := informerutils.NewChangeFeedListWatcher[arm.Subscription, *arm.Subscription, cosmosstorageutils.GenericDocument[arm.Subscription]](
		[]azcorearm.ResourceType{azcorearm.NewResourceType("Microsoft.Resources", "subscriptions")},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
	)

	return cache.NewSharedIndexInformerWithOptions(
		&informerutils.ListWatchWithoutWatchListSemantics{ListWatch: lw.ToListWatch()},
		&arm.Subscription{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod:      1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
			ObjectDescription: "Subscription",
		},
	)
}

// NewBillingInformer creates an unstarted SharedIndexInformer for billing documents
// with a subscription index using the default relist duration.
func NewBillingInformer(lister cosmosstorageutils.GlobalLister[billingcosmosstorage.BillingDocument]) cache.SharedIndexInformer {
	return NewBillingInformerWithRelistDuration(lister, BillingRelistDuration)
}

// NewBillingInformerWithRelistDuration creates an unstarted SharedIndexInformer for billing documents
// with a subscription index and a configurable relist duration.
func NewBillingInformerWithRelistDuration(lister cosmosstorageutils.GlobalLister[billingcosmosstorage.BillingDocument], relistDuration time.Duration) cache.SharedIndexInformer {
	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			logger := utils.LoggerFromContext(ctx)
			logger.Info("listing billing documents")
			defer logger.Info("finished listing billing documents")

			iter, err := lister.List(ctx, nil)
			if err != nil {
				return nil, err
			}

			list := &billingcosmosstorage.BillingDocumentList{}
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
			return informerutils.NewExpiringWatcher(ctx, relistDuration), nil
		},
	}

	return cache.NewSharedIndexInformerWithOptions(
		&informerutils.ListWatchWithoutWatchListSemantics{ListWatch: lw},
		&billingcosmosstorage.BillingDocument{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
			Indexers: cache.Indexers{
				corelisters.BySubscription: billingDocSubscriptionIndexFunc,
			},
			ObjectDescription: "BillingDocument",
		},
	)
}

// NewClusterInformer creates an unstarted SharedIndexInformer for clusters
// with a resource group index using the default relist duration.
func NewClusterInformer(lister cosmosstorageutils.GlobalLister[api.HCPOpenShiftCluster], cosmosClient cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
	return NewClusterInformerWithRelistDuration(lister, cosmosClient, ClusterRelistDuration)
}

// NewClusterInformerWithRelistDuration creates an unstarted SharedIndexInformer for clusters
// with a resource group index and a configurable relist duration.
func NewClusterInformerWithRelistDuration(lister cosmosstorageutils.GlobalLister[api.HCPOpenShiftCluster], cosmosClient cosmosstorageutils.ChangeFeedClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := informerutils.NewChangeFeedListWatcher[api.HCPOpenShiftCluster, *api.HCPOpenShiftCluster, cosmosstorageutils.GenericDocument[api.HCPOpenShiftCluster]](
		[]azcorearm.ResourceType{api.ClusterResourceType},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
	)

	return cache.NewSharedIndexInformerWithOptions(
		&informerutils.ListWatchWithoutWatchListSemantics{ListWatch: lw.ToListWatch()},
		&api.HCPOpenShiftCluster{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
			Indexers: cache.Indexers{
				corelisters.ByResourceGroup: resourceGroupIndexFunc,
			},
			ObjectDescription: "HCPOpenShiftCluster",
		},
	)
}

// NewNodePoolInformer creates an unstarted SharedIndexInformer for node pools
// with resource group and cluster indexes using the default relist duration.
func NewNodePoolInformer(lister cosmosstorageutils.GlobalLister[api.HCPOpenShiftClusterNodePool], cosmosClient cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
	return NewNodePoolInformerWithRelistDuration(lister, cosmosClient, NodePoolRelistDuration)
}

// NewNodePoolInformerWithRelistDuration creates an unstarted SharedIndexInformer for node pools
// with resource group and cluster indexes and a configurable relist duration.
func NewNodePoolInformerWithRelistDuration(lister cosmosstorageutils.GlobalLister[api.HCPOpenShiftClusterNodePool], cosmosClient cosmosstorageutils.ChangeFeedClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := informerutils.NewChangeFeedListWatcher[api.HCPOpenShiftClusterNodePool, *api.HCPOpenShiftClusterNodePool, cosmosstorageutils.GenericDocument[api.HCPOpenShiftClusterNodePool]](
		[]azcorearm.ResourceType{api.NodePoolResourceType},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
	)

	return cache.NewSharedIndexInformerWithOptions(
		&informerutils.ListWatchWithoutWatchListSemantics{ListWatch: lw.ToListWatch()},
		&api.HCPOpenShiftClusterNodePool{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
			Indexers: cache.Indexers{
				corelisters.ByResourceGroup: resourceGroupIndexFunc,
				corelisters.ByCluster:       clusterResourceIDIndexFunc,
			},
			ObjectDescription: "HCPOpenShiftClusterNodePool",
		},
	)
}

// NewExternalAuthInformer creates an unstarted SharedIndexInformer for external auths
// with resource group and cluster indexes using the default relist duration.
func NewExternalAuthInformer(lister cosmosstorageutils.GlobalLister[api.HCPOpenShiftClusterExternalAuth], cosmosClient cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
	return NewExternalAuthInformerWithRelistDuration(lister, cosmosClient, ExternalAuthRelistDuration)
}

// NewExternalAuthInformerWithRelistDuration creates an unstarted SharedIndexInformer for external auths
// with resource group and cluster indexes and a configurable relist duration.
func NewExternalAuthInformerWithRelistDuration(lister cosmosstorageutils.GlobalLister[api.HCPOpenShiftClusterExternalAuth], cosmosClient cosmosstorageutils.ChangeFeedClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := informerutils.NewChangeFeedListWatcher[api.HCPOpenShiftClusterExternalAuth, *api.HCPOpenShiftClusterExternalAuth, cosmosstorageutils.GenericDocument[api.HCPOpenShiftClusterExternalAuth]](
		[]azcorearm.ResourceType{api.ExternalAuthResourceType},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
	)

	return cache.NewSharedIndexInformerWithOptions(
		&informerutils.ListWatchWithoutWatchListSemantics{ListWatch: lw.ToListWatch()},
		&api.HCPOpenShiftClusterExternalAuth{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
			Indexers: cache.Indexers{
				corelisters.ByResourceGroup: resourceGroupIndexFunc,
				corelisters.ByCluster:       clusterResourceIDIndexFunc,
			},
			ObjectDescription: "HCPOpenShiftClusterExternalAuth",
		},
	)
}

// NewServiceProviderClusterInformer creates an unstarted SharedIndexInformer for service provider clusters
// with a cluster index using the default relist duration.
func NewServiceProviderClusterInformer(lister cosmosstorageutils.GlobalLister[api.ServiceProviderCluster], cosmosClient cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
	return NewServiceProviderClusterInformerWithRelistDuration(lister, cosmosClient, ServiceProviderClusterRelistDuration)
}

// NewServiceProviderClusterInformerWithRelistDuration creates an unstarted SharedIndexInformer for service provider clusters
// with a cluster index and a configurable relist duration.
func NewServiceProviderClusterInformerWithRelistDuration(lister cosmosstorageutils.GlobalLister[api.ServiceProviderCluster], cosmosClient cosmosstorageutils.ChangeFeedClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := informerutils.NewChangeFeedListWatcher[api.ServiceProviderCluster, *api.ServiceProviderCluster, cosmosstorageutils.GenericDocument[api.ServiceProviderCluster]](
		[]azcorearm.ResourceType{api.ServiceProviderClusterResourceType},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
	)

	return cache.NewSharedIndexInformerWithOptions(
		&informerutils.ListWatchWithoutWatchListSemantics{ListWatch: lw.ToListWatch()},
		&api.ServiceProviderCluster{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
			Indexers: cache.Indexers{
				corelisters.ByCluster: clusterResourceIDIndexFunc,
			},
			ObjectDescription: "ServiceProviderCluster",
		},
	)
}

// NewManagementClusterContentInformer creates an unstarted SharedIndexInformer for management cluster contents
// with cluster and node pool indexes using the default relist duration.
func NewManagementClusterContentInformer(lister cosmosstorageutils.GlobalLister[api.ManagementClusterContent]) cache.SharedIndexInformer {
	return NewManagementClusterContentInformerWithRelistDuration(lister, ManagementClusterContentRelistDuration)
}

// NewManagementClusterContentInformerWithRelistDuration creates an unstarted SharedIndexInformer for management cluster contents
// with cluster and node pool indexes and a configurable relist duration.
func NewManagementClusterContentInformerWithRelistDuration(lister cosmosstorageutils.GlobalLister[api.ManagementClusterContent], relistDuration time.Duration) cache.SharedIndexInformer {
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
			return informerutils.NewExpiringWatcher(ctx, relistDuration), nil
		},
	}

	return cache.NewSharedIndexInformerWithOptions(
		&informerutils.ListWatchWithoutWatchListSemantics{ListWatch: lw},
		&api.ManagementClusterContent{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
			Indexers: cache.Indexers{
				corelisters.ByCluster:  clusterResourceIDIndexFunc,
				corelisters.ByNodePool: nodePoolResourceIDIndexFunc,
			},
			ObjectDescription: "ManagementClusterContent",
		},
	)
}

// NewServiceProviderNodePoolInformer creates an unstarted SharedIndexInformer for service provider node pools
// with a node pool index using the default relist duration.
func NewServiceProviderNodePoolInformer(lister cosmosstorageutils.GlobalLister[api.ServiceProviderNodePool], cosmosClient cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
	return NewServiceProviderNodePoolInformerWithRelistDuration(lister, cosmosClient, ServiceProviderNodePoolRelistDuration)
}

// NewServiceProviderNodePoolInformerWithRelistDuration creates an unstarted SharedIndexInformer for service provider node pools
// with a node pool index and a configurable relist duration.
func NewServiceProviderNodePoolInformerWithRelistDuration(lister cosmosstorageutils.GlobalLister[api.ServiceProviderNodePool], cosmosClient cosmosstorageutils.ChangeFeedClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := informerutils.NewChangeFeedListWatcher[api.ServiceProviderNodePool, *api.ServiceProviderNodePool, cosmosstorageutils.GenericDocument[api.ServiceProviderNodePool]](
		[]azcorearm.ResourceType{api.ServiceProviderNodePoolResourceType},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
	)

	return cache.NewSharedIndexInformerWithOptions(
		&informerutils.ListWatchWithoutWatchListSemantics{ListWatch: lw.ToListWatch()},
		&api.ServiceProviderNodePool{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
			Indexers: cache.Indexers{
				corelisters.ByNodePool: nodePoolResourceIDIndexFunc,
			},
			ObjectDescription: "ServiceProviderNodePool",
		},
	)
}

// NewSystemAdminCredentialRequestInformer creates an unstarted SharedIndexInformer for
// SystemAdminCredentialRequests with a cluster index using the default relist duration.
func NewSystemAdminCredentialRequestInformer(lister cosmosstorageutils.GlobalLister[api.SystemAdminCredentialRequest], cosmosClient cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
	return NewSystemAdminCredentialRequestInformerWithRelistDuration(lister, cosmosClient, SystemAdminCredentialRequestRelistDuration)
}

// NewSystemAdminCredentialRequestInformerWithRelistDuration creates an unstarted SharedIndexInformer
// for SystemAdminCredentialRequests with a cluster index and a configurable relist duration.
func NewSystemAdminCredentialRequestInformerWithRelistDuration(lister cosmosstorageutils.GlobalLister[api.SystemAdminCredentialRequest], cosmosClient cosmosstorageutils.ChangeFeedClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := informerutils.NewChangeFeedListWatcher[api.SystemAdminCredentialRequest, *api.SystemAdminCredentialRequest, cosmosstorageutils.GenericDocument[api.SystemAdminCredentialRequest]](
		[]azcorearm.ResourceType{api.SystemAdminCredentialRequestResourceType},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
	)

	return cache.NewSharedIndexInformerWithOptions(
		&informerutils.ListWatchWithoutWatchListSemantics{ListWatch: lw.ToListWatch()},
		&api.SystemAdminCredentialRequest{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
			Indexers: cache.Indexers{
				corelisters.ByCluster: clusterResourceIDIndexFunc,
			},
			ObjectDescription: "SystemAdminCredentialRequest",
		},
	)
}

// NewSystemAdminCredentialRevocationInformer creates an unstarted SharedIndexInformer for
// SystemAdminCredentialRevocations with a cluster index using the default relist duration.
func NewSystemAdminCredentialRevocationInformer(lister cosmosstorageutils.GlobalLister[api.SystemAdminCredentialRevocation], cosmosClient cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
	return NewSystemAdminCredentialRevocationInformerWithRelistDuration(lister, cosmosClient, SystemAdminCredentialRevocationRelistDuration)
}

// NewSystemAdminCredentialRevocationInformerWithRelistDuration creates an unstarted SharedIndexInformer
// for SystemAdminCredentialRevocations with a cluster index and a configurable relist duration.
func NewSystemAdminCredentialRevocationInformerWithRelistDuration(lister cosmosstorageutils.GlobalLister[api.SystemAdminCredentialRevocation], cosmosClient cosmosstorageutils.ChangeFeedClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := informerutils.NewChangeFeedListWatcher[api.SystemAdminCredentialRevocation, *api.SystemAdminCredentialRevocation, cosmosstorageutils.GenericDocument[api.SystemAdminCredentialRevocation]](
		[]azcorearm.ResourceType{api.SystemAdminCredentialRevocationResourceType},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
	)

	return cache.NewSharedIndexInformerWithOptions(
		&informerutils.ListWatchWithoutWatchListSemantics{ListWatch: lw.ToListWatch()},
		&api.SystemAdminCredentialRevocation{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
			Indexers: cache.Indexers{
				corelisters.ByCluster: clusterResourceIDIndexFunc,
			},
			ObjectDescription: "SystemAdminCredentialRevocation",
		},
	)
}

// NewControllerInformer creates an unstarted SharedIndexInformer for controllers
// using the default relist duration.
func NewControllerInformer(lister cosmosstorageutils.GlobalLister[api.Controller], cosmosClient cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
	return NewControllerInformerWithRelistDuration(lister, cosmosClient, ControllerRelistDuration)
}

// NewControllerInformerWithRelistDuration creates an unstarted SharedIndexInformer for controllers
// with a configurable relist duration. Controllers live under three different
// ARM resource types (cluster-scoped, nodepool-scoped, externalauth-scoped) so
// the change feed filter accepts all three.
func NewControllerInformerWithRelistDuration(lister cosmosstorageutils.GlobalLister[api.Controller], cosmosClient cosmosstorageutils.ChangeFeedClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := informerutils.NewChangeFeedListWatcher[api.Controller, *api.Controller, cosmosstorageutils.GenericDocument[api.Controller]](
		[]azcorearm.ResourceType{
			api.ClusterControllerResourceType,
			api.NodePoolControllerResourceType,
			api.ExternalAuthControllerResourceType,
			api.SystemAdminCredentialRequestControllerResourceType,
			api.SystemAdminCredentialRevocationControllerResourceType,
		},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
	)

	return cache.NewSharedIndexInformerWithOptions(
		&informerutils.ListWatchWithoutWatchListSemantics{ListWatch: lw.ToListWatch()},
		&api.Controller{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour,
			Indexers: cache.Indexers{
				corelisters.ByResourceGroup: resourceGroupIndexFunc,
				corelisters.ByCluster:       clusterResourceIDIndexFunc,
				corelisters.ByNodePool:      nodePoolResourceIDIndexFunc,
				corelisters.ByExternalAuth:  externalAuthResourceIDIndexFunc,
			},
			ObjectDescription: "Controller",
		},
	)
}

// NewOperationInformer creates an unstarted SharedIndexInformer for all
// operations (including terminal) using the default relist duration. This is
// used by the metrics controller so that completed operations remain visible
// in Prometheus until the 7-day Cosmos TTL removes them.
func NewOperationInformer(lister cosmosstorageutils.GlobalLister[api.Operation], cosmosClient cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
	return NewOperationInformerWithRelistDuration(lister, cosmosClient, AllOperationsRelistDuration)
}

// NewOperationInformerWithRelistDuration creates an unstarted SharedIndexInformer
// for all operations (including terminal) with a configurable relist duration.
func NewOperationInformerWithRelistDuration(lister cosmosstorageutils.GlobalLister[api.Operation], cosmosClient cosmosstorageutils.ChangeFeedClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := informerutils.NewChangeFeedListWatcher[api.Operation, *api.Operation, cosmosstorageutils.GenericDocument[api.Operation]](
		[]azcorearm.ResourceType{api.OperationStatusResourceType},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
	)

	return cache.NewSharedIndexInformerWithOptions(
		&informerutils.ListWatchWithoutWatchListSemantics{ListWatch: lw.ToListWatch()},
		&api.Operation{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod:      1 * time.Hour,
			ObjectDescription: "Operation",
		},
	)
}

// NewActiveOperationInformer creates an unstarted SharedIndexInformer for
// active (non-terminal) operations with resource group and cluster indexes
// using the default relist duration.
func NewActiveOperationInformer(lister cosmosstorageutils.GlobalLister[api.Operation], cosmosClient cosmosstorageutils.ChangeFeedClient) cache.SharedIndexInformer {
	return NewActiveOperationInformerWithRelistDuration(lister, cosmosClient, ActiveOperationsRelistDuration)
}

// NewActiveOperationInformerWithRelistDuration creates an unstarted SharedIndexInformer for
// active (non-terminal) operations with resource group and cluster indexes
// and a configurable relist duration.
func NewActiveOperationInformerWithRelistDuration(lister cosmosstorageutils.GlobalLister[api.Operation], cosmosClient cosmosstorageutils.ChangeFeedClient, relistDuration time.Duration) cache.SharedIndexInformer {
	lw := informerutils.NewChangeFeedListWatcher[api.Operation, *api.Operation, cosmosstorageutils.GenericDocument[api.Operation]](
		[]azcorearm.ResourceType{api.OperationStatusResourceType},
		utilsclock.RealClock{},
		lister,
		cosmosClient,
		relistDuration,
	).WithShouldDeliverItemFn(func(obj *api.Operation) bool {
		return !obj.Status.IsTerminal()
	})

	return cache.NewSharedIndexInformerWithOptions(
		&informerutils.ListWatchWithoutWatchListSemantics{ListWatch: lw.ToListWatch()},
		&api.Operation{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
			Indexers: cache.Indexers{
				corelisters.ByResourceGroup: activeOperationResourceGroupIndexFunc,
				corelisters.ByCluster:       activeOperationClusterIndexFunc,
				corelisters.ByNodePool:      activeOperationNodePoolIndexFunc,
				corelisters.ByExternalAuth:  activeOperationExternalAuthIndexFunc,
			},
			ObjectDescription: "ActiveOperation",
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
	doc, ok := obj.(*billingcosmosstorage.BillingDocument)
	if !ok {
		return nil, utils.TrackError(fmt.Errorf("unexpected type %T, expected *billingcosmosstorage.BillingDocument", obj))
	}
	if doc.SubscriptionID == "" {
		return nil, nil
	}
	return []string{strings.ToLower(doc.SubscriptionID)}, nil
}
