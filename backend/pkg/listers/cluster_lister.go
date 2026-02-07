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

// ClusterLister lists and gets Clusters from an informer's indexer.
type ClusterLister interface {
	List(ctx context.Context) ([]*api.HCPOpenShiftCluster, error)
	Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) (*api.HCPOpenShiftCluster, error)
	ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*api.HCPOpenShiftCluster, error)
}

// hcpOpenShiftClusterLister implements ClusterLister backed by a SharedIndexInformer.
type hcpOpenShiftClusterLister struct {
	indexer cache.Indexer
}

// NewClusterLister creates an ClusterLister from a SharedIndexInformer's indexer.
func NewClusterLister(indexer cache.Indexer) ClusterLister {
	return &hcpOpenShiftClusterLister{
		indexer: indexer,
	}
}

func (l *hcpOpenShiftClusterLister) List(ctx context.Context) ([]*api.HCPOpenShiftCluster, error) {
	return listAll[api.HCPOpenShiftCluster](l.indexer)
}

// Get retrieves a single HCPOpenShiftCluster by subscription ID, resource group name, and cluster name.
// The store key is the lowercased ResourceID string:
//
//	/subscriptions/<sub>/resourcegroups/<rg>/providers/microsoft.redhatopenshift/hcpopenshiftclusters/<name>
func (l *hcpOpenShiftClusterLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) (*api.HCPOpenShiftCluster, error) {
	key := api.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	return getByKey[api.HCPOpenShiftCluster](l.indexer, key)
}

func (l *hcpOpenShiftClusterLister) ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*api.HCPOpenShiftCluster, error) {
	key := api.ToResourceGroupResourceIDString(subscriptionName, resourceGroupName)
	return listFromIndex[api.HCPOpenShiftCluster](l.indexer, ByResourceGroup, key)
}
