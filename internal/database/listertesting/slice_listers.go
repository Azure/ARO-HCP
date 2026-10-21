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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/database/listers"
)

// SliceStampLister implements listers.StampLister backed by a slice.
type SliceStampLister struct {
	Stamps []*fleet.Stamp
}

var _ listers.StampLister = &SliceStampLister{}

func (l *SliceStampLister) List(ctx context.Context) ([]*fleet.Stamp, error) {
	return l.Stamps, nil
}

func (l *SliceStampLister) Get(ctx context.Context, stampIdentifier string) (*fleet.Stamp, error) {
	key := fleet.ToStampResourceIDString(stampIdentifier)
	for _, s := range l.Stamps {
		if s.CosmosMetadata.ResourceID != nil && strings.EqualFold(s.CosmosMetadata.ResourceID.String(), key) {
			return s, nil
		}
	}
	return nil, database.NewNotFoundError()
}

// SliceManagementClusterLister implements listers.ManagementClusterLister backed by a slice.
type SliceManagementClusterLister struct {
	ManagementClusters []*fleet.ManagementCluster
}

var _ listers.ManagementClusterLister = &SliceManagementClusterLister{}

func (l *SliceManagementClusterLister) List(ctx context.Context) ([]*fleet.ManagementCluster, error) {
	return l.ManagementClusters, nil
}

func (l *SliceManagementClusterLister) Get(ctx context.Context, stampIdentifier string) (*fleet.ManagementCluster, error) {
	key := fleet.ToManagementClusterResourceIDString(stampIdentifier)
	for _, mc := range l.ManagementClusters {
		if mc.ResourceID != nil && strings.EqualFold(mc.ResourceID.String(), key) {
			return mc, nil
		}
	}
	return nil, database.NewNotFoundError()
}

func (l *SliceManagementClusterLister) GetByCSProvisionShardID(ctx context.Context, shardID string) (*fleet.ManagementCluster, error) {
	var matches []*fleet.ManagementCluster
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

// SliceApplyDesireLister implements listers.ApplyDesireLister backed by a slice.
// Tests can populate Desires directly and the lister scans on every call.
type SliceApplyDesireLister struct {
	Desires []*kubeapplier.ApplyDesire
}

var _ listers.ApplyDesireLister = &SliceApplyDesireLister{}

func (l *SliceApplyDesireLister) List(ctx context.Context) ([]*kubeapplier.ApplyDesire, error) {
	return l.Desires, nil
}

func (l *SliceApplyDesireLister) GetForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string,
) (*kubeapplier.ApplyDesire, error) {
	want := kubeapplier.ToClusterScopedApplyDesireResourceIDString(subscriptionID, resourceGroupName, clusterName, name)
	for _, d := range l.Desires {
		id := resourceIDOf(d)
		if id != nil && strings.EqualFold(id.String(), want) {
			return d, nil
		}
	}
	return nil, database.NewNotFoundError()
}

func (l *SliceApplyDesireLister) GetForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string,
) (*kubeapplier.ApplyDesire, error) {
	want := kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, nodePoolName, name,
	)
	for _, d := range l.Desires {
		id := resourceIDOf(d)
		if id != nil && strings.EqualFold(id.String(), want) {
			return d, nil
		}
	}
	return nil, database.NewNotFoundError()
}

func (l *SliceApplyDesireLister) ListForManagementCluster(
	ctx context.Context, managementClusterResourceID *azcorearm.ResourceID,
) ([]*kubeapplier.ApplyDesire, error) {
	if managementClusterResourceID == nil {
		return nil, nil
	}
	want := managementClusterResourceID.String()
	var out []*kubeapplier.ApplyDesire
	for _, d := range l.Desires {
		if mc := d.GetManagementCluster(); mc != nil && strings.EqualFold(mc.String(), want) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (l *SliceApplyDesireLister) ListForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName string,
) ([]*kubeapplier.ApplyDesire, error) {
	var out []*kubeapplier.ApplyDesire
	for _, d := range l.Desires {
		if underCluster(resourceIDOf(d), subscriptionID, resourceGroupName, clusterName) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (l *SliceApplyDesireLister) ListForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string,
) ([]*kubeapplier.ApplyDesire, error) {
	var out []*kubeapplier.ApplyDesire
	for _, d := range l.Desires {
		if underNodePool(resourceIDOf(d), subscriptionID, resourceGroupName, clusterName, nodePoolName) {
			out = append(out, d)
		}
	}
	return out, nil
}

// SliceDeleteDesireLister implements listers.DeleteDesireLister backed by a slice.
type SliceDeleteDesireLister struct {
	Desires []*kubeapplier.DeleteDesire
}

var _ listers.DeleteDesireLister = &SliceDeleteDesireLister{}

func (l *SliceDeleteDesireLister) List(ctx context.Context) ([]*kubeapplier.DeleteDesire, error) {
	return l.Desires, nil
}

func (l *SliceDeleteDesireLister) GetForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string,
) (*kubeapplier.DeleteDesire, error) {
	want := kubeapplier.ToClusterScopedDeleteDesireResourceIDString(subscriptionID, resourceGroupName, clusterName, name)
	for _, d := range l.Desires {
		id := resourceIDOf(d)
		if id != nil && strings.EqualFold(id.String(), want) {
			return d, nil
		}
	}
	return nil, database.NewNotFoundError()
}

func (l *SliceDeleteDesireLister) GetForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string,
) (*kubeapplier.DeleteDesire, error) {
	want := kubeapplier.ToNodePoolScopedDeleteDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, nodePoolName, name,
	)
	for _, d := range l.Desires {
		id := resourceIDOf(d)
		if id != nil && strings.EqualFold(id.String(), want) {
			return d, nil
		}
	}
	return nil, database.NewNotFoundError()
}

func (l *SliceDeleteDesireLister) ListForManagementCluster(
	ctx context.Context, managementClusterResourceID *azcorearm.ResourceID,
) ([]*kubeapplier.DeleteDesire, error) {
	if managementClusterResourceID == nil {
		return nil, nil
	}
	want := managementClusterResourceID.String()
	var out []*kubeapplier.DeleteDesire
	for _, d := range l.Desires {
		if mc := d.GetManagementCluster(); mc != nil && strings.EqualFold(mc.String(), want) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (l *SliceDeleteDesireLister) ListForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName string,
) ([]*kubeapplier.DeleteDesire, error) {
	var out []*kubeapplier.DeleteDesire
	for _, d := range l.Desires {
		if underCluster(resourceIDOf(d), subscriptionID, resourceGroupName, clusterName) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (l *SliceDeleteDesireLister) ListForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string,
) ([]*kubeapplier.DeleteDesire, error) {
	var out []*kubeapplier.DeleteDesire
	for _, d := range l.Desires {
		if underNodePool(resourceIDOf(d), subscriptionID, resourceGroupName, clusterName, nodePoolName) {
			out = append(out, d)
		}
	}
	return out, nil
}

// SliceReadDesireLister implements listers.ReadDesireLister backed by a slice.
type SliceReadDesireLister struct {
	Desires []*kubeapplier.ReadDesire
}

var _ listers.ReadDesireLister = &SliceReadDesireLister{}

func (l *SliceReadDesireLister) List(ctx context.Context) ([]*kubeapplier.ReadDesire, error) {
	return l.Desires, nil
}

func (l *SliceReadDesireLister) GetForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string,
) (*kubeapplier.ReadDesire, error) {
	want := kubeapplier.ToClusterScopedReadDesireResourceIDString(subscriptionID, resourceGroupName, clusterName, name)
	for _, d := range l.Desires {
		id := resourceIDOf(d)
		if id != nil && strings.EqualFold(id.String(), want) {
			return d, nil
		}
	}
	return nil, database.NewNotFoundError()
}

func (l *SliceReadDesireLister) GetForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string,
) (*kubeapplier.ReadDesire, error) {
	want := kubeapplier.ToNodePoolScopedReadDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, nodePoolName, name,
	)
	for _, d := range l.Desires {
		id := resourceIDOf(d)
		if id != nil && strings.EqualFold(id.String(), want) {
			return d, nil
		}
	}
	return nil, database.NewNotFoundError()
}

func (l *SliceReadDesireLister) ListForManagementCluster(
	ctx context.Context, managementClusterResourceID *azcorearm.ResourceID,
) ([]*kubeapplier.ReadDesire, error) {
	if managementClusterResourceID == nil {
		return nil, nil
	}
	want := managementClusterResourceID.String()
	var out []*kubeapplier.ReadDesire
	for _, d := range l.Desires {
		if mc := d.GetManagementCluster(); mc != nil && strings.EqualFold(mc.String(), want) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (l *SliceReadDesireLister) ListForCluster(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName string,
) ([]*kubeapplier.ReadDesire, error) {
	var out []*kubeapplier.ReadDesire
	for _, d := range l.Desires {
		if underCluster(resourceIDOf(d), subscriptionID, resourceGroupName, clusterName) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (l *SliceReadDesireLister) ListForNodePool(
	ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string,
) ([]*kubeapplier.ReadDesire, error) {
	var out []*kubeapplier.ReadDesire
	for _, d := range l.Desires {
		if underNodePool(resourceIDOf(d), subscriptionID, resourceGroupName, clusterName, nodePoolName) {
			out = append(out, d)
		}
	}
	return out, nil
}
