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

// NodePoolLister lists and gets NodePools from an informer's indexer.
type NodePoolLister interface {
	List(ctx context.Context) ([]*api.HCPOpenShiftClusterNodePool, error)
	Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) (*api.HCPOpenShiftClusterNodePool, error)
	ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*api.HCPOpenShiftClusterNodePool, error)
	ListForCluster(ctx context.Context, subscriptionName, resourceGroupName, clusterName string) ([]*api.HCPOpenShiftClusterNodePool, error)
}

// hcpOpenShiftClusterNodePoolLister implements NodePoolLister backed by a SharedIndexInformer.
type hcpOpenShiftClusterNodePoolLister struct {
	indexer cache.Indexer
}

// NewNodePoolLister creates an NodePoolLister from a SharedIndexInformer's indexer.
func NewNodePoolLister(indexer cache.Indexer) NodePoolLister {
	return &hcpOpenShiftClusterNodePoolLister{
		indexer: indexer,
	}
}

func (l *hcpOpenShiftClusterNodePoolLister) List(ctx context.Context) ([]*api.HCPOpenShiftClusterNodePool, error) {
	return listAll[api.HCPOpenShiftClusterNodePool](l.indexer)
}

// Get retrieves a single HCPOpenShiftClusterNodePool by subscription ID, resource group name, cluster name, and node pool name.
// The store key is the lowercased ResourceID string:
//
//	/subscriptions/<sub>/resourcegroups/<rg>/providers/microsoft.redhatopenshift/hcpopenshiftclusters/<cluster>/nodepools/<name>
func (l *hcpOpenShiftClusterNodePoolLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) (*api.HCPOpenShiftClusterNodePool, error) {
	key := api.ToNodePoolResourceIDString(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	return getByKey[api.HCPOpenShiftClusterNodePool](l.indexer, key)
}

func (l *hcpOpenShiftClusterNodePoolLister) ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*api.HCPOpenShiftClusterNodePool, error) {
	key := api.ToResourceGroupResourceIDString(subscriptionName, resourceGroupName)
	return listFromIndex[api.HCPOpenShiftClusterNodePool](l.indexer, ByResourceGroup, key)
}

func (l *hcpOpenShiftClusterNodePoolLister) ListForCluster(ctx context.Context, subscriptionName, resourceGroupName, clusterName string) ([]*api.HCPOpenShiftClusterNodePool, error) {
	key := api.ToClusterResourceIDString(subscriptionName, resourceGroupName, clusterName)
	return listFromIndex[api.HCPOpenShiftClusterNodePool](l.indexer, ByCluster, key)
}
