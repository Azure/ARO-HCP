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
	"strings"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/listers/listerutils"
)

// ClusterLister lists and gets Clusters from an informer's indexer.
type ClusterLister interface {
	List(ctx context.Context) ([]*coreapi.Cluster, error)
	ListForSubscription(ctx context.Context, subscriptionID string) ([]*coreapi.Cluster, error)
	Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) (*coreapi.Cluster, error)
	ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*coreapi.Cluster, error)
}

// clusterLister implements ClusterLister backed by a SharedIndexInformer.
type clusterLister struct {
	indexer cache.Indexer
}

// NewClusterLister creates an ClusterLister from a SharedIndexInformer's indexer.
func NewClusterLister(indexer cache.Indexer) ClusterLister {
	return &clusterLister{
		indexer: indexer,
	}
}

func (l *clusterLister) List(ctx context.Context) ([]*coreapi.Cluster, error) {
	return listerutils.ListAll[coreapi.Cluster](l.indexer)
}

// ListForSubscription lists clusters across all resource groups in a subscription.
func (l *clusterLister) ListForSubscription(ctx context.Context, subscriptionID string) ([]*coreapi.Cluster, error) {
	return listerutils.ListFromIndex[coreapi.Cluster](l.indexer, BySubscription, strings.ToLower(subscriptionID))
}

// Get retrieves a single Cluster by subscription ID, resource group name, and cluster name.
// The store key is the lowercased ResourceID string:
//
//	/subscriptions/<sub>/resourcegroups/<rg>/providers/microsoft.redhatopenshift/hcpopenshiftclusters/<name>
func (l *clusterLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) (*coreapi.Cluster, error) {
	key := coreapihelpers.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	return listerutils.GetByKey[coreapi.Cluster](l.indexer, key)
}

func (l *clusterLister) ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*coreapi.Cluster, error) {
	key := coreapihelpers.ToResourceGroupResourceIDString(subscriptionName, resourceGroupName)
	return listerutils.ListFromIndex[coreapi.Cluster](l.indexer, ByResourceGroup, key)
}
