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

package frontend

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/operation"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/admission"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Admission must not silently reintroduce live inventory lists.
type noInventoryListsDB struct {
	corecosmosstorage.ResourcesDBClient
}

func (d noInventoryListsDB) HCPClusters(subscriptionID, resourceGroupName string) corecosmosstorage.HCPClusterCRUD {
	return noClusterListCRUD{d.ResourcesDBClient.HCPClusters(subscriptionID, resourceGroupName)}
}

type noClusterListCRUD struct {
	corecosmosstorage.HCPClusterCRUD
}

func (c noClusterListCRUD) List(context.Context, *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[coreapi.Cluster], error) {
	return nil, fmt.Errorf("unexpected live cluster inventory list")
}

func (c noClusterListCRUD) NodePools(clusterName string) corecosmosstorage.NodePoolsCRUD {
	return noNodePoolListCRUD{c.HCPClusterCRUD.NodePools(clusterName)}
}

type noNodePoolListCRUD struct {
	corecosmosstorage.NodePoolsCRUD
}

func (c noNodePoolListCRUD) List(context.Context, *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[coreapi.NodePool], error) {
	return nil, fmt.Errorf("unexpected live node pool inventory list")
}

func TestClusterCreateAdmissionCachedInventory(t *testing.T) {
	cluster := coreapitesting.MinimumValidClusterTestCase()
	otherGroup := coreapi.NewDefaultCluster(metadataapi.Must(azcorearm.ParseResourceID(strings.Replace(cluster.ID.String(), cluster.ID.ResourceGroupName, "other-group", 1))), coreapitesting.TestLocation)
	otherSubscription := coreapi.NewDefaultCluster(metadataapi.Must(azcorearm.ParseResourceID(strings.Replace(cluster.ID.String(), cluster.ID.SubscriptionID, "11111111-2222-4333-8444-555555555555", 1))), coreapitesting.TestLocation)
	nodePool := coreapi.NewDefaultNodePool(metadataapi.Must(azcorearm.ParseResourceID(otherGroup.ID.String()+"/nodePools/pool")), coreapitesting.TestLocation)
	orphan := coreapi.NewDefaultNodePool(metadataapi.Must(azcorearm.ParseResourceID(cluster.ID.String()+"-absent/nodePools/orphan")), coreapitesting.TestLocation)
	foreignPool := coreapi.NewDefaultNodePool(metadataapi.Must(azcorearm.ParseResourceID(otherSubscription.ID.String()+"/nodePools/foreign")), coreapitesting.TestLocation)
	for _, c := range []*coreapi.Cluster{cluster, otherGroup, otherSubscription} {
		c.SetResourceID(c.ID)
		c.SetPartitionKey(c.ID.SubscriptionID)
	}
	for _, p := range []*coreapi.NodePool{nodePool, orphan, foreignPool} {
		p.SetResourceID(p.ID)
		p.SetPartitionKey(p.ID.SubscriptionID)
	}
	f := NewTestFrontend(t)
	// The cache alone contains these resources, including an orphan that must not
	// be considered without its parent cluster in the collision inventory.
	f.clusterLister = corelisters.NewClusterLister(newTestClusterInformer(t, cluster, otherGroup, otherSubscription).GetIndexer())
	f.nodePoolLister = corelisters.NewNodePoolLister(newTestNodePoolInformer(t, nodePool, orphan, foreignPool).GetIndexer())
	f.resourcesDBClient = noInventoryListsDB{f.resourcesDBClient}
	subscription := newTestSubscription(cluster.ID.SubscriptionID, coreapi.SubscriptionStateRegistered, nil)
	ctx, err := f.newClusterAdmissionContext(t.Context(), operation.Operation{Type: operation.Create}, subscription, cluster, nil)
	require.NoError(t, err)
	require.ElementsMatch(t, []*coreapi.Cluster{cluster, otherGroup}, ctx.SubscriptionClusters)
	require.Equal(t, []*coreapi.NodePool{nodePool}, ctx.SubscriptionNodePools)
	beforeClusters := []*coreapi.Cluster{cluster.DeepCopy(), otherGroup.DeepCopy()}
	beforePools := []*coreapi.NodePool{nodePool.DeepCopy()}
	_ = admission.MutateCluster(t.Context(), ctx, operation.Operation{Type: operation.Create}, cluster.DeepCopy(), nil)
	_ = admission.AdmitCluster(t.Context(), ctx, operation.Operation{Type: operation.Create}, cluster.DeepCopy(), nil)
	require.ElementsMatch(t, beforeClusters, ctx.SubscriptionClusters, "admission must not mutate cached clusters")
	require.Equal(t, beforePools, ctx.SubscriptionNodePools, "admission must not mutate cached node pools")
}

func TestClusterUpdateAdmissionMissingServiceProviderNodePool(t *testing.T) {
	for _, tc := range []struct {
		name       string
		parentLive bool
		spLive     bool
	}{
		{name: "missing SPNP and deleted parent"},
		{name: "missing SPNP and live parent", parentLive: true},
		{name: "live SPNP with stale parent inventory", spLive: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := NewTestFrontend(t)
			db := f.resourcesDBClient
			cluster := coreapitesting.MinimumValidClusterTestCase()
			_, err := corecosmosstorage.GetOrCreateServiceProviderCluster(t.Context(), db, cluster.ID)
			require.NoError(t, err)
			cached := coreapi.NewDefaultNodePool(metadataapi.Must(azcorearm.ParseResourceID(coreapitesting.TestNodePoolResourceID)), coreapitesting.TestLocation)
			cached.SetResourceID(cached.ID)
			cached.SetPartitionKey(cached.ID.SubscriptionID)
			cached.Properties.Version.ID = "4.19.0"
			f.nodePoolLister = corelisters.NewNodePoolLister(newTestNodePoolInformer(t, cached).GetIndexer())
			if tc.parentLive {
				live := cached.DeepCopy()
				live.Properties.Version.ID = "4.20.0"
				_, err := db.HCPClusters(live.ID.SubscriptionID, live.ID.ResourceGroupName).NodePools(live.ID.Parent.Name).Create(t.Context(), live, nil)
				require.NoError(t, err)
			}
			if tc.spLive {
				_, err := corecosmosstorage.GetOrCreateServiceProviderNodePool(t.Context(), db, cached.ID)
				require.NoError(t, err)
			}
			f.resourcesDBClient = noInventoryListsDB{db}
			subscription := newTestSubscription(cluster.ID.SubscriptionID, coreapi.SubscriptionStateRegistered, nil)
			ctx, err := f.newClusterAdmissionContext(t.Context(), operation.Operation{Type: operation.Update}, subscription, cluster, cluster.ID)
			require.NoError(t, err)
			sp, err := db.ServiceProviderNodePools(cached.ID.SubscriptionID, cached.ID.ResourceGroupName, cached.ID.Parent.Name, cached.ID.Name).Get(t.Context(), coreapi.ServiceProviderNodePoolResourceName)
			if !tc.parentLive && !tc.spLive {
				require.Empty(t, ctx.ClusterNodePools)
				require.True(t, cosmosstorageutils.IsNotFoundError(err), "must not recreate SPNP for a stale deleted pool")
				return
			}
			require.NoError(t, err)
			require.Len(t, ctx.ClusterNodePools, 1)
			require.Equal(t, sp, ctx.ClusterNodePools[0].ServiceProviderNodePool)
			wantVersion := cached.Properties.Version.ID
			if tc.parentLive {
				wantVersion = "4.20.0"
			}
			require.Equal(t, wantVersion, ctx.ClusterNodePools[0].NodePool.Properties.Version.ID)
			inventory, err := f.nodePoolLister.List(t.Context())
			require.NoError(t, err)
			require.Equal(t, cached, inventory[0], "live reads must not mutate the cache")
		})
	}
}

func TestDeleteNodePoolCachedLastPoolInventory(t *testing.T) {
	f := NewTestFrontend(t)
	db := corecosmosstoragetesting.NewMockResourcesDBClient()
	cluster := coreapitesting.MinimumValidClusterTestCase()
	cluster.SetResourceID(cluster.ID)
	cluster.SetPartitionKey(cluster.ID.SubscriptionID)
	_, err := db.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).Create(t.Context(), cluster, nil)
	require.NoError(t, err)
	pool := coreapi.NewDefaultNodePool(metadataapi.Must(azcorearm.ParseResourceID(coreapitesting.TestNodePoolResourceID)), coreapitesting.TestLocation)
	pool.SetResourceID(pool.ID)
	pool.SetPartitionKey(pool.ID.SubscriptionID)
	pool.Properties.ProvisioningState = coreapi.ProvisioningStateSucceeded
	_, err = db.HCPClusters(pool.ID.SubscriptionID, pool.ID.ResourceGroupName).NodePools(pool.ID.Parent.Name).Create(t.Context(), pool, nil)
	require.NoError(t, err)
	f.resourcesDBClient = noInventoryListsDB{db}
	f.nodePoolLister = corelisters.NewNodePoolLister(newTestNodePoolInformer(t, pool).GetIndexer())
	request := httptest.NewRequest("DELETE", pool.ID.String(), nil)
	request = request.WithContext(utils.ContextWithResourceID(t.Context(), pool.ID))
	err = f.DeleteNodePool(httptest.NewRecorder(), request)
	require.ErrorContains(t, err, "The last node pool can not be deleted")
}
