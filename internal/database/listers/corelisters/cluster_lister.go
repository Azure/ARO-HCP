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

package corelisters

import (
	"context"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/listers/listerutils"
)

// ClusterLister lists and gets Clusters from an informer's indexer.
type ClusterLister interface {
	List(ctx context.Context) ([]*coreapi.HCPOpenShiftCluster, error)
	Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) (*coreapi.HCPOpenShiftCluster, error)
	ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*coreapi.HCPOpenShiftCluster, error)
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

func (l *hcpOpenShiftClusterLister) List(ctx context.Context) ([]*coreapi.HCPOpenShiftCluster, error) {
	return listerutils.ListAll[coreapi.HCPOpenShiftCluster](l.indexer)
}

// Get retrieves a single HCPOpenShiftCluster by subscription ID, resource group name, and cluster name.
// The store key is the lowercased ResourceID string:
//
//	/subscriptions/<sub>/resourcegroups/<rg>/providers/microsoft.redhatopenshift/hcpopenshiftclusters/<name>
func (l *hcpOpenShiftClusterLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) (*coreapi.HCPOpenShiftCluster, error) {
	key := coreapi.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	return listerutils.GetByKey[coreapi.HCPOpenShiftCluster](l.indexer, key)
}

func (l *hcpOpenShiftClusterLister) ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*coreapi.HCPOpenShiftCluster, error) {
	key := coreapi.ToResourceGroupResourceIDString(subscriptionName, resourceGroupName)
	return listerutils.ListFromIndex[coreapi.HCPOpenShiftCluster](l.indexer, ByResourceGroup, key)
}
