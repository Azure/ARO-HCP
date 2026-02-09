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

// ServiceProviderClusterLister lists and gets ServiceProviderClusters from an informer's indexer.
type ServiceProviderClusterLister interface {
	List(ctx context.Context) ([]*api.ServiceProviderCluster, error)
	Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) (*api.ServiceProviderCluster, error)
	ListForCluster(ctx context.Context, subscriptionName, resourceGroupName, clusterName string) ([]*api.ServiceProviderCluster, error)
}

// serviceProviderClusterLister implements ServiceProviderClusterLister backed by a SharedIndexInformer.
type serviceProviderClusterLister struct {
	indexer cache.Indexer
}

// NewServiceProviderClusterLister creates a ServiceProviderClusterLister from a SharedIndexInformer's indexer.
func NewServiceProviderClusterLister(indexer cache.Indexer) ServiceProviderClusterLister {
	return &serviceProviderClusterLister{
		indexer: indexer,
	}
}

func (l *serviceProviderClusterLister) List(ctx context.Context) ([]*api.ServiceProviderCluster, error) {
	return listAll[api.ServiceProviderCluster](l.indexer)
}

// Get retrieves a single ServiceProviderCluster by subscription ID, resource group name, and cluster name.
// ServiceProviderCluster is a singleton resource with the name "default".
// The store key is the lowercased ResourceID string:
//
//	/subscriptions/<sub>/resourcegroups/<rg>/providers/microsoft.redhatopenshift/hcpopenshiftclusters/<cluster>/serviceproviderclusters/default
func (l *serviceProviderClusterLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) (*api.ServiceProviderCluster, error) {
	key := api.ToServiceProviderClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	return getByKey[api.ServiceProviderCluster](l.indexer, key)
}

func (l *serviceProviderClusterLister) ListForCluster(ctx context.Context, subscriptionName, resourceGroupName, clusterName string) ([]*api.ServiceProviderCluster, error) {
	key := api.ToClusterResourceIDString(subscriptionName, resourceGroupName, clusterName)
	return listFromIndex[api.ServiceProviderCluster](l.indexer, ByCluster, key)
}
