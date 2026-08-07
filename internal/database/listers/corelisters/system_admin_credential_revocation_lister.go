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

// SystemAdminCredentialRevocationLister lists and gets SystemAdminCredentialRevocations from an informer's indexer.
type SystemAdminCredentialRevocationLister interface {
	List(ctx context.Context) ([]*coreapi.SystemAdminCredentialRevocation, error)
	Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, revocationName string) (*coreapi.SystemAdminCredentialRevocation, error)
	ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*coreapi.SystemAdminCredentialRevocation, error)
}

// systemAdminCredentialRevocationLister implements SystemAdminCredentialRevocationLister backed by a SharedIndexInformer.
type systemAdminCredentialRevocationLister struct {
	indexer cache.Indexer
}

// NewSystemAdminCredentialRevocationLister creates a SystemAdminCredentialRevocationLister from a SharedIndexInformer's indexer.
func NewSystemAdminCredentialRevocationLister(indexer cache.Indexer) SystemAdminCredentialRevocationLister {
	return &systemAdminCredentialRevocationLister{
		indexer: indexer,
	}
}

func (l *systemAdminCredentialRevocationLister) List(ctx context.Context) ([]*coreapi.SystemAdminCredentialRevocation, error) {
	return listerutils.ListAll[coreapi.SystemAdminCredentialRevocation](l.indexer)
}

// Get retrieves a single SystemAdminCredentialRevocation by subscription ID, resource group name, cluster name,
// and revocation name. The store key is the lowercased ResourceID string.
func (l *systemAdminCredentialRevocationLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, revocationName string) (*coreapi.SystemAdminCredentialRevocation, error) {
	key := coreapi.ToSystemAdminCredentialRevocationResourceIDString(subscriptionID, resourceGroupName, clusterName, revocationName)
	return listerutils.GetByKey[coreapi.SystemAdminCredentialRevocation](l.indexer, key)
}

// ListForCluster retrieves all SystemAdminCredentialRevocations for a given cluster.
func (l *systemAdminCredentialRevocationLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*coreapi.SystemAdminCredentialRevocation, error) {
	key := coreapi.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	return listerutils.ListFromIndex[coreapi.SystemAdminCredentialRevocation](l.indexer, ByCluster, key)
}
