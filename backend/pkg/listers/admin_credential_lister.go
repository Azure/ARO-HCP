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

// AdminCredentialLister lists and gets ClusterAdminCredentials from an informer's indexer.
type AdminCredentialLister interface {
	List(ctx context.Context) ([]*api.ClusterAdminCredential, error)
	Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, adminCredentialName string) (*api.ClusterAdminCredential, error)
	ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*api.ClusterAdminCredential, error)
	ListForCluster(ctx context.Context, subscriptionName, resourceGroupName, clusterName string) ([]*api.ClusterAdminCredential, error)
}

// clusterAdminCredentialLister implements AdminCredentialLister backed by a SharedIndexInformer.
type clusterAdminCredentialLister struct {
	indexer cache.Indexer
}

// NewAdminCredentialLister creates an AdminCredentialLister from a SharedIndexInformer's indexer.
func NewAdminCredentialLister(indexer cache.Indexer) AdminCredentialLister {
	return &clusterAdminCredentialLister{
		indexer: indexer,
	}
}

func (l *clusterAdminCredentialLister) List(ctx context.Context) ([]*api.ClusterAdminCredential, error) {
	return listAll[api.ClusterAdminCredential](l.indexer)
}

// Get retrieves a single ClusterAdminCredential by subscription ID, resource group name, cluster name, and credential name.
// The store key is the lowercased ResourceID string:
//
//	/subscriptions/<sub>/resourcegroups/<rg>/providers/microsoft.redhatopenshift/hcpopenshiftclusters/<cluster>/admincredentials/<name>
func (l *clusterAdminCredentialLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, adminCredentialName string) (*api.ClusterAdminCredential, error) {
	key := api.ToAdminCredentialResourceIDString(subscriptionID, resourceGroupName, clusterName, adminCredentialName)
	return getByKey[api.ClusterAdminCredential](l.indexer, key)
}

func (l *clusterAdminCredentialLister) ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*api.ClusterAdminCredential, error) {
	key := api.ToResourceGroupResourceIDString(subscriptionName, resourceGroupName)
	return listFromIndex[api.ClusterAdminCredential](l.indexer, ByResourceGroup, key)
}

func (l *clusterAdminCredentialLister) ListForCluster(ctx context.Context, subscriptionName, resourceGroupName, clusterName string) ([]*api.ClusterAdminCredential, error) {
	key := api.ToClusterResourceIDString(subscriptionName, resourceGroupName, clusterName)
	return listFromIndex[api.ClusterAdminCredential](l.indexer, ByCluster, key)
}
