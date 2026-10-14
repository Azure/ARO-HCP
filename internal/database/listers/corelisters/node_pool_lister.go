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
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/listers/listerutils"
)

// NodePoolLister lists and gets NodePools from an informer's indexer.
type NodePoolLister interface {
	List(ctx context.Context) ([]*coreapi.NodePool, error)
	Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) (*coreapi.NodePool, error)
	ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*coreapi.NodePool, error)
	ListForCluster(ctx context.Context, subscriptionName, resourceGroupName, clusterName string) ([]*coreapi.NodePool, error)
}

// nodePoolLister implements NodePoolLister backed by a SharedIndexInformer.
type nodePoolLister struct {
	indexer cache.Indexer
}

// NewNodePoolLister creates an NodePoolLister from a SharedIndexInformer's indexer.
func NewNodePoolLister(indexer cache.Indexer) NodePoolLister {
	return &nodePoolLister{
		indexer: indexer,
	}
}

func (l *nodePoolLister) List(ctx context.Context) ([]*coreapi.NodePool, error) {
	return listerutils.ListAll[coreapi.NodePool](l.indexer)
}

// Get retrieves a single NodePool by subscription ID, resource group name, cluster name, and node pool name.
// The store key is the lowercased ResourceID string:
//
//	/subscriptions/<sub>/resourcegroups/<rg>/providers/microsoft.redhatopenshift/hcpopenshiftclusters/<cluster>/nodepools/<name>
func (l *nodePoolLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) (*coreapi.NodePool, error) {
	key := coreapihelpers.ToNodePoolResourceIDString(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	return listerutils.GetByKey[coreapi.NodePool](l.indexer, key)
}

func (l *nodePoolLister) ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*coreapi.NodePool, error) {
	key := coreapihelpers.ToResourceGroupResourceIDString(subscriptionName, resourceGroupName)
	return listerutils.ListFromIndex[coreapi.NodePool](l.indexer, ByResourceGroup, key)
}

func (l *nodePoolLister) ListForCluster(ctx context.Context, subscriptionName, resourceGroupName, clusterName string) ([]*coreapi.NodePool, error) {
	key := coreapihelpers.ToClusterResourceIDString(subscriptionName, resourceGroupName, clusterName)
	return listerutils.ListFromIndex[coreapi.NodePool](l.indexer, ByCluster, key)
}
