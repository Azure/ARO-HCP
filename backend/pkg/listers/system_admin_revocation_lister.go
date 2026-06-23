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

// SystemAdminRevocationLister lists and gets SystemAdminRevocations from an informer's indexer.
type SystemAdminRevocationLister interface {
	List(ctx context.Context) ([]*api.SystemAdminRevocation, error)
	Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, revocationName string) (*api.SystemAdminRevocation, error)
	ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*api.SystemAdminRevocation, error)
	ListForCluster(ctx context.Context, subscriptionName, resourceGroupName, clusterName string) ([]*api.SystemAdminRevocation, error)
}

type systemAdminRevocationLister struct {
	indexer cache.Indexer
}

// NewSystemAdminRevocationLister creates a SystemAdminRevocationLister from a SharedIndexInformer's indexer.
func NewSystemAdminRevocationLister(indexer cache.Indexer) SystemAdminRevocationLister {
	return &systemAdminRevocationLister{indexer: indexer}
}

func (l *systemAdminRevocationLister) List(ctx context.Context) ([]*api.SystemAdminRevocation, error) {
	return listAll[api.SystemAdminRevocation](l.indexer)
}

// Get retrieves a single SystemAdminRevocation by subscription ID, resource group name, cluster name, and revocation name.
// The store key is the lowercased ResourceID string.
func (l *systemAdminRevocationLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, revocationName string) (*api.SystemAdminRevocation, error) {
	key := api.ToSystemAdminRevocationResourceIDString(subscriptionID, resourceGroupName, clusterName, revocationName)
	return getByKey[api.SystemAdminRevocation](l.indexer, key)
}

func (l *systemAdminRevocationLister) ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*api.SystemAdminRevocation, error) {
	key := api.ToResourceGroupResourceIDString(subscriptionName, resourceGroupName)
	return listFromIndex[api.SystemAdminRevocation](l.indexer, ByResourceGroup, key)
}

func (l *systemAdminRevocationLister) ListForCluster(ctx context.Context, subscriptionName, resourceGroupName, clusterName string) ([]*api.SystemAdminRevocation, error) {
	key := api.ToClusterResourceIDString(subscriptionName, resourceGroupName, clusterName)
	return listFromIndex[api.SystemAdminRevocation](l.indexer, ByCluster, key)
}
