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
	"fmt"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
)

// ManagementClusterLister lists and gets management clusters from an informer's indexer.
type ManagementClusterLister interface {
	List(ctx context.Context) ([]*api.ManagementCluster, error)
	Get(ctx context.Context, name string) (*api.ManagementCluster, error)
	GetByCSProvisionShardID(ctx context.Context, shardID string) (*api.ManagementCluster, error)
}

// informerBasedManagementClusterLister implements ManagementClusterLister backed by a SharedIndexInformer.
type informerBasedManagementClusterLister struct {
	indexer cache.Indexer
}

// NewManagementClusterLister creates a ManagementClusterLister from a SharedIndexInformer's indexer.
func NewManagementClusterLister(indexer cache.Indexer) ManagementClusterLister {
	return &informerBasedManagementClusterLister{
		indexer: indexer,
	}
}

func (l *informerBasedManagementClusterLister) List(ctx context.Context) ([]*api.ManagementCluster, error) {
	return listAll[api.ManagementCluster](l.indexer)
}

// Get retrieves a single management cluster by name.
// The store key is the lowercased resource ID string:
//
//	/providers/microsoft.redhatopenshift/hcpopenshiftmanagementclusters/<name>
func (l *informerBasedManagementClusterLister) Get(ctx context.Context, name string) (*api.ManagementCluster, error) {
	key := api.ToManagementClusterResourceIDString(name)
	return getByKey[api.ManagementCluster](l.indexer, key)
}

// GetByCSProvisionShardID retrieves a single management cluster by its CS provision shard ID.
// Returns NotFoundError if no match, or an error if multiple matches exist (should be 1:1).
func (l *informerBasedManagementClusterLister) GetByCSProvisionShardID(ctx context.Context, shardID string) (*api.ManagementCluster, error) {
	results, err := listFromIndex[api.ManagementCluster](l.indexer, ByCSProvisionShardID, shardID)
	if err != nil {
		return nil, err
	}
	switch len(results) {
	case 0:
		return nil, database.NewNotFoundError()
	case 1:
		return results[0], nil
	default:
		return nil, fmt.Errorf("expected at most 1 management cluster for CS provision shard ID %q, got %d", shardID, len(results))
	}
}
