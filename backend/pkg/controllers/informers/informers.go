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

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
)

const (
	// ByResourceGroup is the indexer name for looking up resources by resource group.
	// Keys are in the format: /subscriptions/<sub>/resourcegroups/<rg>
	ByResourceGroup = "byResourceGroup"

	// ByCluster is the indexer name for looking up resources by parent cluster.
	// Keys are the full lowercase cluster resource ID string.
	ByCluster = "byCluster"

	// These durations indicate the maximum time it will take for us to notice a new instance of a particular type.
	// Remember that these will not fire in order, so it's entirely possible to get an operation for subscription we have no observed.
	SubscriptionRelistDuration     = 30 * time.Second
	ClusterRelistDuration          = 30 * time.Second
	NodePoolRelistDuration         = 30 * time.Second
	ActiveOperationsRelistDuration = 10 * time.Second
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
			return NewExpiringWatcher(relistDuration), nil
		},
	}

	return cache.NewSharedIndexInformerWithOptions(
		lw,
		&arm.Subscription{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour,
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
			return NewExpiringWatcher(relistDuration), nil
		},
	}

	return cache.NewSharedIndexInformerWithOptions(
		lw,
		&api.HCPOpenShiftCluster{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour,
			Indexers: cache.Indexers{
				ByResourceGroup: clusterResourceGroupIndexFunc,
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
			return NewExpiringWatcher(relistDuration), nil
		},
	}

	return cache.NewSharedIndexInformerWithOptions(
		lw,
		&api.HCPOpenShiftClusterNodePool{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour,
			Indexers: cache.Indexers{
				ByResourceGroup: nodePoolResourceGroupIndexFunc,
				ByCluster:       nodePoolClusterIndexFunc,
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
			return NewExpiringWatcher(relistDuration), nil
		},
	}

	return cache.NewSharedIndexInformerWithOptions(
		lw,
		&api.Operation{},
		cache.SharedIndexInformerOptions{
			ResyncPeriod: 1 * time.Hour,
			Indexers: cache.Indexers{
				ByResourceGroup: activeOperationResourceGroupIndexFunc,
				ByCluster:       activeOperationClusterIndexFunc,
			},
		},
	)
}

// resourceGroupKey returns the resource group index key for a resource ID in the
// format: /subscriptions/<sub>/resourcegroups/<rg>
func resourceGroupKey(id interface{ String() string }) string {
	// Parse the resource ID string to extract subscription and resource group.
	// Resource IDs follow the pattern:
	//   /subscriptions/<sub>/resourceGroups/<rg>/providers/...
	parts := strings.Split(strings.ToLower(id.String()), "/")
	// Find subscriptions and resourcegroups segments.
	var sub, rg string
	for i := 0; i < len(parts)-1; i++ {
		switch parts[i] {
		case "subscriptions":
			sub = parts[i+1]
		case "resourcegroups":
			rg = parts[i+1]
		}
	}
	if sub == "" || rg == "" {
		return ""
	}
	return fmt.Sprintf("/subscriptions/%s/resourcegroups/%s", sub, rg)
}

// clusterResourceGroupIndexFunc indexes clusters by resource group.
func clusterResourceGroupIndexFunc(obj interface{}) ([]string, error) {
	cluster, ok := obj.(*api.HCPOpenShiftCluster)
	if !ok {
		return nil, fmt.Errorf("expected *api.HCPOpenShiftCluster, got %T", obj)
	}
	if cluster.ID == nil {
		return nil, nil
	}
	key := resourceGroupKey(cluster.ID)
	if key == "" {
		return nil, nil
	}
	return []string{key}, nil
}

// nodePoolResourceGroupIndexFunc indexes node pools by resource group.
func nodePoolResourceGroupIndexFunc(obj interface{}) ([]string, error) {
	np, ok := obj.(*api.HCPOpenShiftClusterNodePool)
	if !ok {
		return nil, fmt.Errorf("expected *api.HCPOpenShiftClusterNodePool, got %T", obj)
	}
	if np.ID == nil {
		return nil, nil
	}
	key := resourceGroupKey(np.ID)
	if key == "" {
		return nil, nil
	}
	return []string{key}, nil
}

// nodePoolClusterIndexFunc indexes node pools by their parent cluster resource ID.
func nodePoolClusterIndexFunc(obj interface{}) ([]string, error) {
	np, ok := obj.(*api.HCPOpenShiftClusterNodePool)
	if !ok {
		return nil, fmt.Errorf("expected *api.HCPOpenShiftClusterNodePool, got %T", obj)
	}
	if np.ID == nil || np.ID.Parent == nil {
		return nil, nil
	}
	return []string{strings.ToLower(np.ID.Parent.String())}, nil
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
	key := resourceGroupKey(op.ExternalID)
	if key == "" {
		return nil, nil
	}
	return []string{key}, nil
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
	if op.ExternalID == nil {
		return nil, nil
	}

	if strings.EqualFold(op.ExternalID.ResourceType.String(), api.ClusterResourceType.String()) {
		return []string{strings.ToLower(op.ExternalID.String())}, nil
	}

	// For child resources (nodepools, externalauths), use the parent cluster ID
	// only if the parent is actually a cluster.
	if op.ExternalID.Parent != nil &&
		strings.EqualFold(op.ExternalID.Parent.ResourceType.String(), api.ClusterResourceType.String()) {
		return []string{strings.ToLower(op.ExternalID.Parent.String())}, nil
	}

	return nil, nil
}
