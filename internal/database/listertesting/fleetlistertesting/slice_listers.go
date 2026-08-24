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

// Package fleetlistertesting provides slice-backed test implementations of the
// fleet listers (Stamp, ManagementCluster, ManagementClusterScheduling).
package fleetlistertesting

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
)

// SliceStampLister implements fleetlisters.StampLister backed by a slice.
type SliceStampLister struct {
	Stamps []*fleetapi.Stamp
}

var _ fleetlisters.StampLister = &SliceStampLister{}

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
	return nil, cosmosstorageutils.NewNotFoundError()
}

// SliceManagementClusterLister implements fleetlisters.ManagementClusterLister backed by a slice.
type SliceManagementClusterLister struct {
	ManagementClusters []*fleetapi.ManagementCluster
}

var _ fleetlisters.ManagementClusterLister = &SliceManagementClusterLister{}

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
	return nil, cosmosstorageutils.NewNotFoundError()
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
		return nil, cosmosstorageutils.NewNotFoundError()
	case 1:
		return matches[0], nil
	default:
		return nil, fmt.Errorf("expected at most 1 management cluster for CS provision shard ID %q, got %d", shardID, len(matches))
	}
}

// SliceManagementClusterSchedulingLister implements
// fleetlisters.ManagementClusterSchedulingLister backed by a slice.
type SliceManagementClusterSchedulingLister struct {
	Schedulings []*fleetapi.ManagementClusterScheduling
}

var _ fleetlisters.ManagementClusterSchedulingLister = &SliceManagementClusterSchedulingLister{}

func (l *SliceManagementClusterSchedulingLister) List(ctx context.Context) ([]*fleetapi.ManagementClusterScheduling, error) {
	return l.Schedulings, nil
}

func (l *SliceManagementClusterSchedulingLister) Get(ctx context.Context, stampIdentifier string) (*fleetapi.ManagementClusterScheduling, error) {
	key := fleetapi.ToManagementClusterSchedulingResourceIDString(stampIdentifier)
	for _, s := range l.Schedulings {
		if s.ResourceID != nil && strings.EqualFold(s.ResourceID.String(), key) {
			return s, nil
		}
	}
	return nil, cosmosstorageutils.NewNotFoundError()
}
