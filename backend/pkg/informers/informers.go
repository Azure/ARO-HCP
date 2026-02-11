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
	"fmt"
	"strings"
	"time"

	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
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
func NewSubscriptionInformer(cosmosDBListWatch *CosmosDBListWatch) cache.SharedIndexInformer {
	return NewSubscriptionInformerWithRelistDuration(cosmosDBListWatch, SubscriptionRelistDuration)
}

// NewSubscriptionInformerWithRelistDuration creates an unstarted SharedIndexInformer for subscriptions
// with a configurable relist duration.
func NewSubscriptionInformerWithRelistDuration(cosmosDBListWatch *CosmosDBListWatch, relistDuration time.Duration) cache.SharedIndexInformer {
	return cosmosDBListWatch.NewSubscriptionInformer(
		relistDuration,
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour, // this is only a default.  Shorter resyncs can be added when registering handlers.
		},
	)
}

// NewClusterInformer creates an unstarted SharedIndexInformer for clusters
// with a resource group index using the default relist duration.
func NewClusterInformer(cosmosDBListWatch *CosmosDBListWatch) cache.SharedIndexInformer {
	return NewClusterInformerWithRelistDuration(cosmosDBListWatch, ClusterRelistDuration)
}

// NewClusterInformerWithRelistDuration creates an unstarted SharedIndexInformer for clusters
// with a resource group index and a configurable relist duration.
func NewClusterInformerWithRelistDuration(cosmosDBListWatch *CosmosDBListWatch, relistDuration time.Duration) cache.SharedIndexInformer {
	return cosmosDBListWatch.NewClusterInformer(
		relistDuration,
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
func NewNodePoolInformer(cosmosDBListWatch *CosmosDBListWatch) cache.SharedIndexInformer {
	return NewNodePoolInformerWithRelistDuration(cosmosDBListWatch, NodePoolRelistDuration)
}

// NewNodePoolInformerWithRelistDuration creates an unstarted SharedIndexInformer for node pools
// with resource group and cluster indexes and a configurable relist duration.
func NewNodePoolInformerWithRelistDuration(cosmosDBListWatch *CosmosDBListWatch, relistDuration time.Duration) cache.SharedIndexInformer {
	return cosmosDBListWatch.NewNodePoolInformer(
		relistDuration,
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
func NewExternalAuthInformer(cosmosDBListWatch *CosmosDBListWatch) cache.SharedIndexInformer {
	return NewExternalAuthInformerWithRelistDuration(cosmosDBListWatch, ExternalAuthRelistDuration)
}

// NewExternalAuthInformerWithRelistDuration creates an unstarted SharedIndexInformer for external auths
// with resource group and cluster indexes and a configurable relist duration.
func NewExternalAuthInformerWithRelistDuration(cosmosDBListWatch *CosmosDBListWatch, relistDuration time.Duration) cache.SharedIndexInformer {
	return cosmosDBListWatch.NewExternalAuthInformer(
		relistDuration,
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
func NewServiceProviderClusterInformer(cosmosDBListWatch *CosmosDBListWatch) cache.SharedIndexInformer {
	return NewServiceProviderClusterInformerWithRelistDuration(cosmosDBListWatch, ServiceProviderClusterRelistDuration)
}

// NewServiceProviderClusterInformerWithRelistDuration creates an unstarted SharedIndexInformer for service provider clusters
// with a cluster index and a configurable relist duration.
func NewServiceProviderClusterInformerWithRelistDuration(cosmosDBListWatch *CosmosDBListWatch, relistDuration time.Duration) cache.SharedIndexInformer {
	return cosmosDBListWatch.NewServiceProviderClusterInformer(
		relistDuration,
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
func NewActiveOperationInformer(cosmosDBListWatch *CosmosDBListWatch) cache.SharedIndexInformer {
	return NewActiveOperationInformerWithRelistDuration(cosmosDBListWatch, ActiveOperationsRelistDuration)
}

// NewActiveOperationInformerWithRelistDuration creates an unstarted SharedIndexInformer for
// active (non-terminal) operations with resource group and cluster indexes
// and a configurable relist duration.
func NewActiveOperationInformerWithRelistDuration(cosmosDBListWatch *CosmosDBListWatch, relistDuration time.Duration) cache.SharedIndexInformer {
	return cosmosDBListWatch.NewActiveOperationInformer(
		relistDuration,
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
