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

	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/database"
)

// ManagementClusterLister lists and gets management clusters from an informer's indexer.
type ManagementClusterLister interface {
	List(ctx context.Context) ([]*fleet.ManagementCluster, error)
	Get(ctx context.Context, stampIdentifier string) (*fleet.ManagementCluster, error)
	GetByCSProvisionShardID(ctx context.Context, shardID string) (*fleet.ManagementCluster, error)
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

func (l *informerBasedManagementClusterLister) List(ctx context.Context) ([]*fleet.ManagementCluster, error) {
	return listAll[fleet.ManagementCluster](l.indexer)
}

// Get retrieves a single management cluster by stamp identifier.
func (l *informerBasedManagementClusterLister) Get(ctx context.Context, stampIdentifier string) (*fleet.ManagementCluster, error) {
	key := fleet.ToManagementClusterResourceIDString(stampIdentifier)
	return getByKey[fleet.ManagementCluster](l.indexer, key)
}

// GetByCSProvisionShardID retrieves a single management cluster by its CS provision shard ID.
func (l *informerBasedManagementClusterLister) GetByCSProvisionShardID(ctx context.Context, shardID string) (*fleet.ManagementCluster, error) {
	results, err := listFromIndex[fleet.ManagementCluster](l.indexer, ByCSProvisionShard, shardID)
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
