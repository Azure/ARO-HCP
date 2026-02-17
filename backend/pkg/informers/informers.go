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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// These durations indicate the maximum time it will take for us to notice a new instance of a particular type.
	// Remember that these will not fire in order, so it's entirely possible to get an operation for subscription we have no observed.
	SubscriptionRelistDuration           = 30 * time.Second
	ClusterRelistDuration                = 30 * time.Second
	NodePoolRelistDuration               = 30 * time.Second
	ExternalAuthRelistDuration           = 30 * time.Second
	ServiceProviderClusterRelistDuration = 30 * time.Second
	ActiveOperationsRelistDuration       = 10 * time.Second
)

// NewSubscriptionInformer creates an unstarted SharedIndexInformer for subscriptions
// using the default relist duration.
func NewSubscriptionInformer(lister database.GlobalLister[arm.Subscription]) cache.SharedIndexInformer {
	return NewSubscriptionInformerWithRelistDuration(lister, SubscriptionRelistDuration)
}

// NewSubscriptionInformerWithRelistDuration creates an unstarted SharedIndexInformer for subscriptions
// with a configurable relist duration.
func NewSubscriptionInformerWithRelistDuration(lister database.GlobalLister[arm.Subscription], relistDuration time.Duration) cache.SharedIndexInformer {
	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			logger := utils.LoggerFromContext(ctx)
			logger.Info("listing subscriptions")
			defer logger.Info("finished listing subscriptions")

			iter, err := lister.List(ctx, nil)
			if err != nil {
				return nil, err
			}

			list := &arm.SubscriptionList{}
			list.ResourceVersion = "0"
			for _, sub := range iter.Items(ctx) {
				list.Items = append(list.Items, *sub)
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
		lw,
		&arm.Subscription{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
		},
	)
}

// NewClusterInformer creates an unstarted SharedIndexInformer for clusters
// with a resource group index using the default relist duration.
func NewClusterInformer(lister database.GlobalLister[api.HCPOpenShiftCluster]) cache.SharedIndexInformer {
	return NewClusterInformerWithRelistDuration(lister, ClusterRelistDuration)
}

// NewClusterInformerWithRelistDuration creates an unstarted SharedIndexInformer for clusters
// with a resource group index and a configurable relist duration.
func NewClusterInformerWithRelistDuration(lister database.GlobalLister[api.HCPOpenShiftCluster], relistDuration time.Duration) cache.SharedIndexInformer {
	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			logger := utils.LoggerFromContext(ctx)
			logger.Info("listing clusters")
			defer logger.Info("finished listing clusters")

			iter, err := lister.List(ctx, nil)
			if err != nil {
				return nil, err
			}

			list := &api.HCPOpenShiftClusterList{}
			list.ResourceVersion = "0"
			for _, cluster := range iter.Items(ctx) {
				list.Items = append(list.Items, *cluster)
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
		lw,
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
func NewNodePoolInformer(lister database.GlobalLister[api.HCPOpenShiftClusterNodePool]) cache.SharedIndexInformer {
	return NewNodePoolInformerWithRelistDuration(lister, NodePoolRelistDuration)
}

// NewNodePoolInformerWithRelistDuration creates an unstarted SharedIndexInformer for node pools
// with resource group and cluster indexes and a configurable relist duration.
func NewNodePoolInformerWithRelistDuration(lister database.GlobalLister[api.HCPOpenShiftClusterNodePool], relistDuration time.Duration) cache.SharedIndexInformer {
	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			logger := utils.LoggerFromContext(ctx)
			logger.Info("listing node pools")
			defer logger.Info("finished listing node pools")

			iter, err := lister.List(ctx, nil)
			if err != nil {
				return nil, err
			}

			list := &api.HCPOpenShiftClusterNodePoolList{}
			list.ResourceVersion = "0"
			for _, np := range iter.Items(ctx) {
				list.Items = append(list.Items, *np)
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
		lw,
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
func NewExternalAuthInformer(lister database.GlobalLister[api.HCPOpenShiftClusterExternalAuth]) cache.SharedIndexInformer {
	return NewExternalAuthInformerWithRelistDuration(lister, ExternalAuthRelistDuration)
}

// NewExternalAuthInformerWithRelistDuration creates an unstarted SharedIndexInformer for external auths
// with resource group and cluster indexes and a configurable relist duration.
func NewExternalAuthInformerWithRelistDuration(lister database.GlobalLister[api.HCPOpenShiftClusterExternalAuth], relistDuration time.Duration) cache.SharedIndexInformer {
	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			logger := utils.LoggerFromContext(ctx)
			logger.Info("listing external auths")
			defer logger.Info("finished listing external auths")

			iter, err := lister.List(ctx, nil)
			if err != nil {
				return nil, err
			}

			list := &api.HCPOpenShiftClusterExternalAuthList{}
			list.ResourceVersion = "0"
			for _, ea := range iter.Items(ctx) {
				list.Items = append(list.Items, *ea)
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
		lw,
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
func NewServiceProviderClusterInformer(lister database.GlobalLister[api.ServiceProviderCluster]) cache.SharedIndexInformer {
	return NewServiceProviderClusterInformerWithRelistDuration(lister, ServiceProviderClusterRelistDuration)
}

// NewServiceProviderClusterInformerWithRelistDuration creates an unstarted SharedIndexInformer for service provider clusters
// with a cluster index and a configurable relist duration.
func NewServiceProviderClusterInformerWithRelistDuration(lister database.GlobalLister[api.ServiceProviderCluster], relistDuration time.Duration) cache.SharedIndexInformer {
	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			logger := utils.LoggerFromContext(ctx)
			logger.Info("listing service provider clusters")
			defer logger.Info("finished listing service provider clusters")

			iter, err := lister.List(ctx, nil)
			if err != nil {
				return nil, err
			}

			list := &api.ServiceProviderClusterList{}
			list.ResourceVersion = "0"
			for _, spc := range iter.Items(ctx) {
				list.Items = append(list.Items, *spc)
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
		lw,
		&api.ServiceProviderCluster{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
			Indexers: cache.Indexers{
				listers.ByCluster: clusterResourceIDIndexFunc,
			},
		},
	)
}

// NewActiveOperationInformer creates an unstarted SharedIndexInformer for
// active (non-terminal) operations with resource group and cluster indexes
// using the default relist duration.
func NewActiveOperationInformer(lister database.GlobalLister[api.Operation]) cache.SharedIndexInformer {
	return NewActiveOperationInformerWithRelistDuration(lister, ActiveOperationsRelistDuration)
}

// NewActiveOperationInformerWithRelistDuration creates an unstarted SharedIndexInformer for
// active (non-terminal) operations with resource group and cluster indexes
// and a configurable relist duration.
func NewActiveOperationInformerWithRelistDuration(lister database.GlobalLister[api.Operation], relistDuration time.Duration) cache.SharedIndexInformer {
	lw := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			logger := utils.LoggerFromContext(ctx)
			logger.Info("listing active operations")
			defer logger.Info("finished listing active operations")

			iter, err := lister.List(ctx, nil)
			if err != nil {
				return nil, err
			}

			list := &api.OperationList{}
			list.ResourceVersion = "0"
			for _, op := range iter.Items(ctx) {
				list.Items = append(list.Items, *op)
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
		lw,
		&api.Operation{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
			Indexers: cache.Indexers{
				listers.ByResourceGroup: activeOperationResourceGroupIndexFunc,
				listers.ByCluster:       activeOperationClusterIndexFunc,
			},
		},
	)
}

func resourceGroupIndexFunc(obj interface{}) ([]string, error) {
	switch castObj := obj.(type) {
	case arm.CosmosMetadataAccessor:
		return []string{api.ToResourceGroupResourceIDString(castObj.GetResourceID().SubscriptionID, castObj.GetResourceID().ResourceGroupName)}, nil
	case arm.CosmosPersistable:
		return []string{api.ToResourceGroupResourceIDString(castObj.GetCosmosData().ResourceID.SubscriptionID, castObj.GetCosmosData().ResourceID.ResourceGroupName)}, nil
	default:
		return nil, utils.TrackError(fmt.Errorf("unexpected type %T, expected api.CosmosMetadataAccessor or api.CosmosPersistable", obj))
	}
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
	switch {
	case resourceID == nil:
		return nil, nil

	case strings.EqualFold(resourceID.ResourceType.String(), api.ClusterResourceType.String()):
		return []string{strings.ToLower(resourceID.String())}, nil

	case resourceID.Parent == nil:
		return nil, nil
	case strings.EqualFold(resourceID.Parent.ResourceType.String(), api.ClusterResourceType.String()):
		return []string{strings.ToLower(resourceID.Parent.String())}, nil

	case resourceID.Parent.Parent == nil:
		return nil, nil
	case strings.EqualFold(resourceID.Parent.Parent.ResourceType.String(), api.ClusterResourceType.String()):
		return []string{strings.ToLower(resourceID.Parent.Parent.String())}, nil

	case resourceID.Parent.Parent.Parent == nil:
		return nil, nil
	case strings.EqualFold(resourceID.Parent.Parent.Parent.ResourceType.String(), api.ClusterResourceType.String()):
		return []string{strings.ToLower(resourceID.Parent.Parent.Parent.String())}, nil

	default:
		return nil, nil
	}
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
