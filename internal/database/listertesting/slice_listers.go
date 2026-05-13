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

package listertesting

import (
	"context"
	"fmt"
	"strings"

	fleetapi "github.com/Azure/ARO-HCP/internal/apis/fleet"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
)

// SliceStampLister implements dblisters.StampLister backed by a slice.
type SliceStampLister struct {
	Stamps []*fleetapi.Stamp
}

var _ dblisters.StampLister = &SliceStampLister{}

func (l *SliceStampLister) List(ctx context.Context) ([]*fleetapi.Stamp, error) {
	return l.Stamps, nil
}

func (l *SliceStampLister) Get(ctx context.Context, stampIdentifier string) (*fleetapi.Stamp, error) {
	key := fleetapi.ToStampResourceIDString(stampIdentifier)
	for _, s := range l.Stamps {
		if s.CosmosMetadata.ResourceID != nil && strings.EqualFold(s.CosmosMetadata.ResourceID.String(), key) {
			return s, nil
		}
	}
	return nil, database.NewNotFoundError()
}

// SliceManagementClusterLister implements dblisters.ManagementClusterLister backed by a slice.
type SliceManagementClusterLister struct {
	ManagementClusters []*fleetapi.ManagementCluster
}

var _ dblisters.ManagementClusterLister = &SliceManagementClusterLister{}

func (l *SliceManagementClusterLister) List(ctx context.Context) ([]*fleetapi.ManagementCluster, error) {
	return l.ManagementClusters, nil
}

func (l *SliceManagementClusterLister) Get(ctx context.Context, stampIdentifier string) (*fleetapi.ManagementCluster, error) {
	key := fleetapi.ToManagementClusterResourceIDString(stampIdentifier)
	for _, mc := range l.ManagementClusters {
		if mc.ResourceID != nil && strings.EqualFold(mc.ResourceID.String(), key) {
			return mc, nil
		}
	}
	return nil, database.NewNotFoundError()
}

func (l *SliceManagementClusterLister) GetByCSProvisionShardID(ctx context.Context, shardID string) (*fleetapi.ManagementCluster, error) {
	var matches []*fleetapi.ManagementCluster
	for _, mc := range l.ManagementClusters {
		if mc.Status.ClusterServiceProvisionShardID != nil && mc.Status.ClusterServiceProvisionShardID.ID() == shardID {
			matches = append(matches, mc)
		}
	}
	switch len(matches) {
	case 0:
		return nil, database.NewNotFoundError()
	case 1:
		return matches[0], nil
	default:
		return nil, fmt.Errorf("expected at most 1 management cluster for CS provision shard ID %q, got %d", shardID, len(matches))
	}
}
