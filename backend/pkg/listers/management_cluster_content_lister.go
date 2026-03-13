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

// ManagementClusterContentLister lists ManagementClusterContent from an informer's indexer.
type ManagementClusterContentLister interface {
	List(ctx context.Context) ([]*api.ManagementClusterContent, error)
	ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*api.ManagementClusterContent, error)
}

// managementClusterContentLister implements ManagementClusterContentLister backed by a SharedIndexInformer.
type managementClusterContentLister struct {
	indexer cache.Indexer
}

// NewManagementClusterContentLister creates a ManagementClusterContentLister from a SharedIndexInformer's indexer.
func NewManagementClusterContentLister(indexer cache.Indexer) ManagementClusterContentLister {
	return &managementClusterContentLister{
		indexer: indexer,
	}
}

func (l *managementClusterContentLister) List(ctx context.Context) ([]*api.ManagementClusterContent, error) {
	return listAll[api.ManagementClusterContent](l.indexer)
}

func (l *managementClusterContentLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*api.ManagementClusterContent, error) {
	key := api.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	return listFromIndex[api.ManagementClusterContent](l.indexer, ByCluster, key)
}
