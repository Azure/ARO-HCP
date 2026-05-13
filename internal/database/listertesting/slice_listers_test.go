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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	fleetapi "github.com/Azure/ARO-HCP/internal/apis/fleet"
	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
	"github.com/Azure/ARO-HCP/internal/database"
)

func newTestManagementCluster(name, shardID string) *fleetapi.ManagementCluster {
	resourceID := resourcesapi.Must(fleetapi.ToManagementClusterResourceID(name))
	return &fleetapi.ManagementCluster{
		CosmosMetadata: resourcesapi.CosmosMetadata{
			ResourceID: resourceID,
		},
		ResourceID: resourceID,
		Status: fleetapi.ManagementClusterStatus{
			ClusterServiceProvisionShardID: ptr.To(resourcesapi.Must(resourcesapi.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/" + shardID))),
		},
	}
}

func TestSliceManagementClusterLister(t *testing.T) {
	mc1 := newTestManagementCluster("m1", "11111111-1111-1111-1111-111111111111")
	mc2 := newTestManagementCluster("m2", "22222222-2222-2222-2222-222222222222")

	lister := &SliceManagementClusterLister{
		ManagementClusters: []*fleetapi.ManagementCluster{mc1, mc2},
	}

	ctx := context.Background()

	t.Run("List returns all management clusters", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("Get returns matching management cluster", func(t *testing.T) {
		result, err := lister.Get(ctx, "m1")
		require.NoError(t, err)
		assert.Equal(t, "m1", result.ResourceID.Parent.Name)
	})

	t.Run("Get returns not found for non-existent management cluster", func(t *testing.T) {
		_, err := lister.Get(ctx, "non-existent")
		require.Error(t, err)
		assert.True(t, database.IsNotFoundError(err))
	})

	t.Run("GetByCSProvisionShard returns matching management cluster", func(t *testing.T) {
		csShardID := resourcesapi.Must(resourcesapi.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/11111111-1111-1111-1111-111111111111"))
		result, err := lister.GetByCSProvisionShardID(ctx, csShardID.ID())
		require.NoError(t, err)
		assert.Equal(t, "m1", result.ResourceID.Parent.Name)
	})

	t.Run("GetByCSProvisionShard returns not found for non-existent shard", func(t *testing.T) {
		csShardID := resourcesapi.Must(resourcesapi.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/99999999-9999-9999-9999-999999999999"))
		_, err := lister.GetByCSProvisionShardID(ctx, csShardID.ID())
		require.Error(t, err)
		assert.True(t, database.IsNotFoundError(err))
	})

	t.Run("GetByCSProvisionShard returns error for duplicate shards", func(t *testing.T) {
		mc3 := newTestManagementCluster("m3", "11111111-1111-1111-1111-111111111111")
		dupLister := &SliceManagementClusterLister{
			ManagementClusters: []*fleetapi.ManagementCluster{mc1, mc3},
		}
		csShardID := resourcesapi.Must(resourcesapi.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/11111111-1111-1111-1111-111111111111"))
		_, err := dupLister.GetByCSProvisionShardID(ctx, csShardID.ID())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected at most 1")
	})
}
