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

	"github.com/Azure/ARO-HCP/internal/api"
)

// ServiceProviderNodePoolLister lists and gets ServiceProviderNodePools from an informer's indexer.
type ServiceProviderNodePoolLister interface {
	List(ctx context.Context) ([]*api.ServiceProviderNodePool, error)
	Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) (*api.ServiceProviderNodePool, error)
	ListForNodePool(ctx context.Context, subscriptionName, resourceGroupName, clusterName, nodePoolName string) ([]*api.ServiceProviderNodePool, error)
}

// serviceProviderNodePoolLister implements ServiceProviderNodePoolLister backed by a SharedIndexInformer.
type serviceProviderNodePoolLister struct {
	indexer cache.Indexer
}

// NewServiceProviderNodePoolLister creates a ServiceProviderNodePoolLister from a SharedIndexInformer's indexer.
func NewServiceProviderNodePoolLister(indexer cache.Indexer) ServiceProviderNodePoolLister {
	return &serviceProviderNodePoolLister{
		indexer: indexer,
	}
}

func (l *serviceProviderNodePoolLister) List(ctx context.Context) ([]*api.ServiceProviderNodePool, error) {
	return listAll[api.ServiceProviderNodePool](l.indexer)
}

// Get retrieves a single ServiceProviderNodePool by subscription ID, resource group name, cluster name, and node pool name.
// ServiceProviderNodePool is a singleton resource with the name "default".
// The store key is the lowercased ResourceID string:
//
//	/subscriptions/<sub>/resourcegroups/<rg>/providers/microsoft.redhatopenshift/hcpopenshiftclusters/<cluster>/serviceproviderclusters/default
func (l *serviceProviderNodePoolLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) (*api.ServiceProviderNodePool, error) {
	key := api.ToServiceProviderNodePoolResourceIDString(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	return getByKey[api.ServiceProviderNodePool](l.indexer, key)
}

func (l *serviceProviderNodePoolLister) ListForNodePool(ctx context.Context, subscriptionName, resourceGroupName, clusterName, nodePoolName string) ([]*api.ServiceProviderNodePool, error) {
	key := api.ToNodePoolResourceIDString(subscriptionName, resourceGroupName, clusterName, nodePoolName)
	return listFromIndex[api.ServiceProviderNodePool](l.indexer, ByNodePool, key)
}
