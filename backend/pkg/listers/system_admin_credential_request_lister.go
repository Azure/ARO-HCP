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

// SystemAdminCredentialRequestLister lists and gets SystemAdminCredentialRequests from an informer's indexer.
type SystemAdminCredentialRequestLister interface {
	List(ctx context.Context) ([]*api.SystemAdminCredentialRequest, error)
	Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, credentialName string) (*api.SystemAdminCredentialRequest, error)
	ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*api.SystemAdminCredentialRequest, error)
}

// systemAdminCredentialRequestLister implements SystemAdminCredentialRequestLister backed by a SharedIndexInformer.
type systemAdminCredentialRequestLister struct {
	indexer cache.Indexer
}

// NewSystemAdminCredentialRequestLister creates a SystemAdminCredentialRequestLister from a SharedIndexInformer's indexer.
func NewSystemAdminCredentialRequestLister(indexer cache.Indexer) SystemAdminCredentialRequestLister {
	return &systemAdminCredentialRequestLister{
		indexer: indexer,
	}
}

func (l *systemAdminCredentialRequestLister) List(ctx context.Context) ([]*api.SystemAdminCredentialRequest, error) {
	return listAll[api.SystemAdminCredentialRequest](l.indexer)
}

// Get retrieves a single SystemAdminCredentialRequest by subscription ID, resource group name, cluster name,
// and credential name. The store key is the lowercased ResourceID string.
func (l *systemAdminCredentialRequestLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, credentialName string) (*api.SystemAdminCredentialRequest, error) {
	key := api.ToSystemAdminCredentialRequestResourceIDString(subscriptionID, resourceGroupName, clusterName, credentialName)
	return getByKey[api.SystemAdminCredentialRequest](l.indexer, key)
}

// ListForCluster retrieves all SystemAdminCredentialRequests for a given cluster.
func (l *systemAdminCredentialRequestLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*api.SystemAdminCredentialRequest, error) {
	key := api.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	return listFromIndex[api.SystemAdminCredentialRequest](l.indexer, ByCluster, key)
}
