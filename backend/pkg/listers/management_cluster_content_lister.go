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

package listers

import (
	"context"

	"k8s.io/client-go/tools/cache"

	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
)

// ManagementClusterContentLister lists ManagementClusterContent from the shared informer indexer.
type ManagementClusterContentLister interface {
	List(ctx context.Context) ([]*resourcesapi.ManagementClusterContent, error)
	GetForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName, managementClusterContentName string) (*resourcesapi.ManagementClusterContent, error)
	ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*resourcesapi.ManagementClusterContent, error)
	ListForNodePool(ctx context.Context, subscriptionName, resourceGroupName, clusterName, nodePoolName string) ([]*resourcesapi.ManagementClusterContent, error)
}

// managementClusterContentLister implements ManagementClusterContentLister backed by a SharedIndexInformer.
type managementClusterContentLister struct {
	indexer cache.Indexer
}

// NewManagementClusterContentLister creates a ManagementClusterContentLister from a SharedIndexInformer's indexer.
func NewManagementClusterContentLister(indexer cache.Indexer) ManagementClusterContentLister {
	return &managementClusterContentLister{
		indexer: indexer,
	}
}

func (l *managementClusterContentLister) GetForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName, managementClusterContentName string) (*resourcesapi.ManagementClusterContent, error) {
	key := resourcesapi.ToManagementClusterContentResourceIDString(subscriptionID, resourceGroupName, clusterName, managementClusterContentName)
	return getByKey[resourcesapi.ManagementClusterContent](l.indexer, key)
}

func (l *managementClusterContentLister) List(ctx context.Context) ([]*resourcesapi.ManagementClusterContent, error) {
	return listAll[resourcesapi.ManagementClusterContent](l.indexer)
}

func (l *managementClusterContentLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*resourcesapi.ManagementClusterContent, error) {
	key := resourcesapi.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	return listFromIndex[resourcesapi.ManagementClusterContent](l.indexer, ByCluster, key)
}

func (l *managementClusterContentLister) ListForNodePool(ctx context.Context, subscriptionName, resourceGroupName, clusterName, nodePoolName string) ([]*resourcesapi.ManagementClusterContent, error) {
	key := resourcesapi.ToNodePoolResourceIDString(subscriptionName, resourceGroupName, clusterName, nodePoolName)
	return listFromIndex[resourcesapi.ManagementClusterContent](l.indexer, ByNodePool, key)
}
