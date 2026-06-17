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

// SystemAdminCredentialLister lists and gets SystemAdminCredentials from an informer's indexer.
type SystemAdminCredentialLister interface {
	List(ctx context.Context) ([]*api.SystemAdminCredential, error)
	Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, credentialName string) (*api.SystemAdminCredential, error)
	ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*api.SystemAdminCredential, error)
	ListForCluster(ctx context.Context, subscriptionName, resourceGroupName, clusterName string) ([]*api.SystemAdminCredential, error)
}

type systemAdminCredentialLister struct {
	indexer cache.Indexer
}

// NewSystemAdminCredentialLister creates a SystemAdminCredentialLister from a SharedIndexInformer's indexer.
func NewSystemAdminCredentialLister(indexer cache.Indexer) SystemAdminCredentialLister {
	return &systemAdminCredentialLister{indexer: indexer}
}

func (l *systemAdminCredentialLister) List(ctx context.Context) ([]*api.SystemAdminCredential, error) {
	return listAll[api.SystemAdminCredential](l.indexer)
}

// Get retrieves a single SystemAdminCredential by subscription ID, resource group name, cluster name, and credential name.
// The store key is the lowercased ResourceID string.
func (l *systemAdminCredentialLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, credentialName string) (*api.SystemAdminCredential, error) {
	key := api.ToSystemAdminCredentialResourceIDString(subscriptionID, resourceGroupName, clusterName, credentialName)
	return getByKey[api.SystemAdminCredential](l.indexer, key)
}

func (l *systemAdminCredentialLister) ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*api.SystemAdminCredential, error) {
	key := api.ToResourceGroupResourceIDString(subscriptionName, resourceGroupName)
	return listFromIndex[api.SystemAdminCredential](l.indexer, ByResourceGroup, key)
}

func (l *systemAdminCredentialLister) ListForCluster(ctx context.Context, subscriptionName, resourceGroupName, clusterName string) ([]*api.SystemAdminCredential, error) {
	key := api.ToClusterResourceIDString(subscriptionName, resourceGroupName, clusterName)
	return listFromIndex[api.SystemAdminCredential](l.indexer, ByCluster, key)
}
