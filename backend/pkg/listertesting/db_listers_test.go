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

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

func TestDBClusterLister(t *testing.T) {
	ctx := context.Background()

	// Create test clusters in the mock DB
	cluster1 := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	cluster2 := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName2)
	cluster3 := newTestCluster(testSubscriptionID2, testResourceGroupName, testClusterName)

	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster1, cluster2, cluster3})
	require.NoError(t, err)

	lister := &DBClusterLister{ResourcesDBClient: mockResourcesDBClient}

	t.Run("List returns all clusters", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 3)
	})

	t.Run("Get returns matching cluster", func(t *testing.T) {
		result, err := lister.Get(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)
		assert.Equal(t, testClusterName, result.ID.Name)
	})

	t.Run("Get returns not found for non-existent cluster", func(t *testing.T) {
		_, err := lister.Get(ctx, testSubscriptionID, testResourceGroupName, "non-existent")
		require.Error(t, err)
		assert.True(t, database.IsNotFoundError(err))
	})

	t.Run("ListForResourceGroup returns clusters in resource group", func(t *testing.T) {
		result, err := lister.ListForResourceGroup(ctx, testSubscriptionID, testResourceGroupName)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})
}

func TestDBNodePoolLister(t *testing.T) {
	ctx := context.Background()

	// Create test cluster first (required for node pools)
	cluster := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName)

	// Create test node pools
	np1 := newTestNodePool(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
	np2 := newTestNodePool(testSubscriptionID, testResourceGroupName, testClusterName, "nodepool-2")

	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, np1, np2})
	require.NoError(t, err)

	lister := &DBNodePoolLister{ResourcesDBClient: mockResourcesDBClient}

	t.Run("List returns all node pools", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("Get returns matching node pool", func(t *testing.T) {
		result, err := lister.Get(ctx, testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
		require.NoError(t, err)
		assert.Equal(t, testNodePoolName, result.ID.Name)
	})

	t.Run("Get returns not found for non-existent node pool", func(t *testing.T) {
		_, err := lister.Get(ctx, testSubscriptionID, testResourceGroupName, testClusterName, "non-existent")
		require.Error(t, err)
		assert.True(t, database.IsNotFoundError(err))
	})

	t.Run("ListForCluster returns node pools for cluster", func(t *testing.T) {
		result, err := lister.ListForCluster(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("ListForResourceGroup returns node pools in resource group", func(t *testing.T) {
		result, err := lister.ListForResourceGroup(ctx, testSubscriptionID, testResourceGroupName)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})
}

func TestDBSubscriptionLister(t *testing.T) {
	ctx := context.Background()

	// Create test subscriptions
	sub1 := newTestSubscription(testSubscriptionID)
	sub1.State = arm.SubscriptionStateRegistered
	sub2 := newTestSubscription(testSubscriptionID2)
	sub2.State = arm.SubscriptionStateRegistered

	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{sub1, sub2})
	require.NoError(t, err)

	lister := &DBSubscriptionLister{ResourcesDBClient: mockResourcesDBClient}

	t.Run("List returns all subscriptions", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("Get returns matching subscription", func(t *testing.T) {
		result, err := lister.Get(ctx, testSubscriptionID)
		require.NoError(t, err)
		assert.Equal(t, testSubscriptionID, result.GetResourceID().SubscriptionID)
	})

	t.Run("Get returns not found for non-existent subscription", func(t *testing.T) {
		_, err := lister.Get(ctx, "22222222-2222-2222-2222-222222222222")
		require.Error(t, err)
		assert.True(t, database.IsNotFoundError(err))
	})
}

func TestDBActiveOperationLister(t *testing.T) {
	ctx := context.Background()

	// Create a test cluster first (required for operations)
	cluster := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName)

	// Create test operations
	op1 := newTestOperation(testSubscriptionID, "op1", testSubscriptionID, testResourceGroupName, testClusterName)
	op1.Status = arm.ProvisioningStateProvisioning
	op2 := newTestOperation(testSubscriptionID, "op2", testSubscriptionID, testResourceGroupName, testClusterName)
	op2.Status = arm.ProvisioningStateProvisioning

	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, op1, op2})
	require.NoError(t, err)

	lister := &DBActiveOperationLister{ResourcesDBClient: mockResourcesDBClient}

	t.Run("List returns all active operations", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("Get returns matching operation", func(t *testing.T) {
		result, err := lister.Get(ctx, testSubscriptionID, "op1")
		require.NoError(t, err)
		assert.Equal(t, "op1", result.OperationID.Name)
	})

	t.Run("Get returns not found for non-existent operation", func(t *testing.T) {
		_, err := lister.Get(ctx, testSubscriptionID, "non-existent")
		require.Error(t, err)
		assert.True(t, database.IsNotFoundError(err))
	})

	t.Run("ListActiveOperationsForCluster returns operations for cluster", func(t *testing.T) {
		result, err := lister.ListActiveOperationsForCluster(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})
}

func TestDBExternalAuthLister(t *testing.T) {
	ctx := context.Background()

	// Create test cluster first (required for external auths)
	cluster := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName)

	// Create test external auths
	ea1 := newTestExternalAuth(testSubscriptionID, testResourceGroupName, testClusterName, testExternalAuthName)
	ea2 := newTestExternalAuth(testSubscriptionID, testResourceGroupName, testClusterName, "external-auth-2")

	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, ea1, ea2})
	require.NoError(t, err)

	lister := &DBExternalAuthLister{ResourcesDBClient: mockResourcesDBClient}

	t.Run("List returns all external auths", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("Get returns matching external auth", func(t *testing.T) {
		result, err := lister.Get(ctx, testSubscriptionID, testResourceGroupName, testClusterName, testExternalAuthName)
		require.NoError(t, err)
		assert.Equal(t, testExternalAuthName, result.ID.Name)
	})

	t.Run("Get returns not found for non-existent external auth", func(t *testing.T) {
		_, err := lister.Get(ctx, testSubscriptionID, testResourceGroupName, testClusterName, "non-existent")
		require.Error(t, err)
		assert.True(t, database.IsNotFoundError(err))
	})

	t.Run("ListForCluster returns external auths for cluster", func(t *testing.T) {
		result, err := lister.ListForCluster(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("ListForResourceGroup returns external auths in resource group", func(t *testing.T) {
		result, err := lister.ListForResourceGroup(ctx, testSubscriptionID, testResourceGroupName)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})
}

func TestDBServiceProviderClusterLister(t *testing.T) {
	ctx := context.Background()

	// Create test cluster first (required for service provider clusters)
	cluster := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName)

	// Create test service provider cluster
	spc := newTestServiceProviderCluster(testSubscriptionID, testResourceGroupName, testClusterName)

	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc})
	require.NoError(t, err)

	lister := &DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDBClient}

	t.Run("List returns all service provider clusters", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 1)
	})

	t.Run("Get returns matching service provider cluster", func(t *testing.T) {
		result, err := lister.Get(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)
		require.NotNil(t, result.GetResourceID())
		assert.Equal(t, "default", result.GetResourceID().Name)
	})

	t.Run("Get returns not found for non-existent service provider cluster", func(t *testing.T) {
		_, err := lister.Get(ctx, testSubscriptionID, testResourceGroupName, "non-existent")
		require.Error(t, err)
		assert.True(t, database.IsNotFoundError(err))
	})

	t.Run("ListForCluster returns service provider clusters for cluster", func(t *testing.T) {
		result, err := lister.ListForCluster(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)
		assert.Len(t, result, 1)
	})
}

func TestDBControllerLister(t *testing.T) {
	ctx := context.Background()

	// Create parent resources
	cluster1 := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	cluster2 := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName2)
	np := newTestNodePool(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
	ea := newTestExternalAuth(testSubscriptionID, testResourceGroupName, testClusterName, testExternalAuthName)

	// Create controllers under different parents
	clusterCtrl1 := newTestClusterController(testSubscriptionID, testResourceGroupName, testClusterName, "ctrl-cluster-1")
	clusterCtrl2 := newTestClusterController(testSubscriptionID, testResourceGroupName, testClusterName2, "ctrl-cluster-2")
	npCtrl := newTestNodePoolController(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName, "ctrl-np")
	eaCtrl := newTestExternalAuthController(testSubscriptionID, testResourceGroupName, testClusterName, testExternalAuthName, "ctrl-ea")

	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{
		cluster1, cluster2, np, ea,
		clusterCtrl1, clusterCtrl2, npCtrl, eaCtrl,
	})
	require.NoError(t, err)

	lister := &DBControllerLister{ResourcesDBClient: mockResourcesDBClient}

	t.Run("List returns all controllers", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 4)
	})

	t.Run("ListForResourceGroup returns all controllers in resource group", func(t *testing.T) {
		result, err := lister.ListForResourceGroup(ctx, testSubscriptionID, testResourceGroupName)
		require.NoError(t, err)
		assert.Len(t, result, 4)
	})

	t.Run("ListForCluster returns controllers under cluster1", func(t *testing.T) {
		result, err := lister.ListForCluster(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)
		// cluster1 has: clusterCtrl1, npCtrl, eaCtrl (all nested under cluster1)
		assert.Len(t, result, 3)
	})

	t.Run("ListForCluster returns controllers under cluster2", func(t *testing.T) {
		result, err := lister.ListForCluster(ctx, testSubscriptionID, testResourceGroupName, testClusterName2)
		require.NoError(t, err)
		// cluster2 has: clusterCtrl2
		assert.Len(t, result, 1)
	})

	t.Run("ListForNodePool returns controllers under nodepool", func(t *testing.T) {
		result, err := lister.ListForNodePool(ctx, testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
		require.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, "ctrl-np", result[0].ResourceID.Name)
	})

	t.Run("ListForExternalAuth returns controllers under externalauth", func(t *testing.T) {
		result, err := lister.ListForExternalAuth(ctx, testSubscriptionID, testResourceGroupName, testClusterName, testExternalAuthName)
		require.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, "ctrl-ea", result[0].ResourceID.Name)
	})

	t.Run("ListForNodePool returns empty for non-existent nodepool", func(t *testing.T) {
		result, err := lister.ListForNodePool(ctx, testSubscriptionID, testResourceGroupName, testClusterName, "non-existent")
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("ListForExternalAuth returns empty for non-existent externalauth", func(t *testing.T) {
		result, err := lister.ListForExternalAuth(ctx, testSubscriptionID, testResourceGroupName, testClusterName, "non-existent")
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("ListForResourceGroup returns empty for different subscription", func(t *testing.T) {
		result, err := lister.ListForResourceGroup(ctx, testSubscriptionID2, testResourceGroupName)
		require.NoError(t, err)
		assert.Empty(t, result)
	})
}

func TestDBManagementClusterContentLister(t *testing.T) {
	ctx := context.Background()

	cluster1 := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	cluster2 := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName2)
	np := newTestNodePool(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)

	mccCluster1 := newTestClusterScopedManagementClusterContent(testSubscriptionID, testResourceGroupName, testClusterName, "mcc-under-cluster")
	mccCluster2 := newTestClusterScopedManagementClusterContent(testSubscriptionID, testResourceGroupName, testClusterName2, "mcc-under-cluster2")
	mccNP := newTestNodePoolScopedManagementClusterContent(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName, "mcc-under-np")

	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{
		cluster1, cluster2, np,
		mccCluster1, mccCluster2, mccNP,
	})
	require.NoError(t, err)

	lister := &DBManagementClusterContentLister{ResourcesDBClient: mockResourcesDBClient}

	t.Run("List returns all management cluster contents", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 3)
	})

	t.Run("ListForCluster returns cluster-scoped and node-pool-scoped MCC for that cluster", func(t *testing.T) {
		result, err := lister.ListForCluster(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)
		assert.Len(t, result, 2)
		names := []string{result[0].GetResourceID().Name, result[1].GetResourceID().Name}
		assert.Contains(t, names, "mcc-under-cluster")
		assert.Contains(t, names, "mcc-under-np")
	})

	t.Run("ListForCluster returns only MCC for other cluster", func(t *testing.T) {
		result, err := lister.ListForCluster(ctx, testSubscriptionID, testResourceGroupName, testClusterName2)
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, "mcc-under-cluster2", result[0].GetResourceID().Name)
	})

	t.Run("ListForNodePool returns only node-pool-scoped MCC", func(t *testing.T) {
		result, err := lister.ListForNodePool(ctx, testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, "mcc-under-np", result[0].GetResourceID().Name)
	})

	t.Run("ListForNodePool returns empty for non-existent node pool", func(t *testing.T) {
		result, err := lister.ListForNodePool(ctx, testSubscriptionID, testResourceGroupName, testClusterName, "non-existent")
		require.NoError(t, err)
		assert.Empty(t, result)
	})
}

func TestDBClusterListerWithEmptyDB(t *testing.T) {
	ctx := context.Background()
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()

	lister := &DBClusterLister{ResourcesDBClient: mockResourcesDBClient}

	t.Run("List returns empty slice", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("Get returns not found", func(t *testing.T) {
		_, err := lister.Get(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.Error(t, err)
		assert.True(t, database.IsNotFoundError(err))
	})

	t.Run("ListForResourceGroup returns empty slice", func(t *testing.T) {
		result, err := lister.ListForResourceGroup(ctx, testSubscriptionID, testResourceGroupName)
		require.NoError(t, err)
		assert.Empty(t, result)
	})
}

func TestDBClusterListerWithDeletes(t *testing.T) {
	ctx := context.Background()
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()

	// Create test clusters
	cluster1 := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	cluster2 := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName2)

	clusterCRUD := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName)
	_, err := clusterCRUD.Create(ctx, cluster1, nil)
	require.NoError(t, err)
	_, err = clusterCRUD.Create(ctx, cluster2, nil)
	require.NoError(t, err)

	lister := &DBClusterLister{ResourcesDBClient: mockResourcesDBClient}

	t.Run("List returns both clusters before delete", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("Get returns cluster before delete", func(t *testing.T) {
		result, err := lister.Get(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)
		assert.Equal(t, testClusterName, result.ID.Name)
	})

	// Delete cluster1
	err = clusterCRUD.Delete(ctx, testClusterName)
	require.NoError(t, err)

	t.Run("List returns only remaining cluster after delete", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, testClusterName2, result[0].ID.Name)
	})

	t.Run("Get returns not found for deleted cluster", func(t *testing.T) {
		_, err := lister.Get(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.Error(t, err)
		assert.True(t, database.IsNotFoundError(err))
	})

	t.Run("Get still returns non-deleted cluster", func(t *testing.T) {
		result, err := lister.Get(ctx, testSubscriptionID, testResourceGroupName, testClusterName2)
		require.NoError(t, err)
		assert.Equal(t, testClusterName2, result.ID.Name)
	})

	t.Run("ListForResourceGroup returns only remaining cluster", func(t *testing.T) {
		result, err := lister.ListForResourceGroup(ctx, testSubscriptionID, testResourceGroupName)
		require.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, testClusterName2, result[0].ID.Name)
	})
}

func TestDBClusterListerWithUpdates(t *testing.T) {
	ctx := context.Background()
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()

	// Create test cluster
	cluster := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	cluster.ServiceProviderProperties.Console.URL = "https://original.example.com"

	clusterCRUD := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName)
	createdCluster, err := clusterCRUD.Create(ctx, cluster, nil)
	require.NoError(t, err)

	lister := &DBClusterLister{ResourcesDBClient: mockResourcesDBClient}

	t.Run("Get returns original value before update", func(t *testing.T) {
		result, err := lister.Get(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)
		assert.Equal(t, "https://original.example.com", result.ServiceProviderProperties.Console.URL)
	})

	// Update the cluster
	createdCluster.ServiceProviderProperties.Console.URL = "https://updated.example.com"
	_, err = clusterCRUD.Replace(ctx, createdCluster, nil)
	require.NoError(t, err)

	t.Run("Get returns updated value after update", func(t *testing.T) {
		result, err := lister.Get(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)
		assert.Equal(t, "https://updated.example.com", result.ServiceProviderProperties.Console.URL)
	})

	t.Run("List returns updated cluster", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, "https://updated.example.com", result[0].ServiceProviderProperties.Console.URL)
	})
}

func TestDBNodePoolListerWithDeletes(t *testing.T) {
	ctx := context.Background()
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()

	// Create test cluster first
	cluster := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	clusterCRUD := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName)
	_, err := clusterCRUD.Create(ctx, cluster, nil)
	require.NoError(t, err)

	// Create test node pools
	np1 := newTestNodePool(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
	np2 := newTestNodePool(testSubscriptionID, testResourceGroupName, testClusterName, "nodepool-2")

	nodePoolCRUD := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName)
	_, err = nodePoolCRUD.Create(ctx, np1, nil)
	require.NoError(t, err)
	_, err = nodePoolCRUD.Create(ctx, np2, nil)
	require.NoError(t, err)

	lister := &DBNodePoolLister{ResourcesDBClient: mockResourcesDBClient}

	t.Run("List returns both node pools before delete", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	// Delete np1
	err = nodePoolCRUD.Delete(ctx, testNodePoolName)
	require.NoError(t, err)

	t.Run("List returns only remaining node pool after delete", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, "nodepool-2", result[0].ID.Name)
	})

	t.Run("Get returns not found for deleted node pool", func(t *testing.T) {
		_, err := lister.Get(ctx, testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
		require.Error(t, err)
		assert.True(t, database.IsNotFoundError(err))
	})

	t.Run("ListForCluster returns only remaining node pool", func(t *testing.T) {
		result, err := lister.ListForCluster(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)
		assert.Len(t, result, 1)
	})
}

func TestDBNodePoolListerWithUpdates(t *testing.T) {
	ctx := context.Background()
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()

	// Create test cluster first
	cluster := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	clusterCRUD := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName)
	_, err := clusterCRUD.Create(ctx, cluster, nil)
	require.NoError(t, err)

	// Create test node pool
	np := newTestNodePool(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
	np.Properties.Replicas = 3

	nodePoolCRUD := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName)
	createdNP, err := nodePoolCRUD.Create(ctx, np, nil)
	require.NoError(t, err)

	lister := &DBNodePoolLister{ResourcesDBClient: mockResourcesDBClient}

	t.Run("Get returns original replicas before update", func(t *testing.T) {
		result, err := lister.Get(ctx, testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
		require.NoError(t, err)
		assert.Equal(t, int32(3), result.Properties.Replicas)
	})

	// Update the node pool
	createdNP.Properties.Replicas = 5
	_, err = nodePoolCRUD.Replace(ctx, createdNP, nil)
	require.NoError(t, err)

	t.Run("Get returns updated replicas after update", func(t *testing.T) {
		result, err := lister.Get(ctx, testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
		require.NoError(t, err)
		assert.Equal(t, int32(5), result.Properties.Replicas)
	})
}

func TestDBSubscriptionListerWithDeletes(t *testing.T) {
	ctx := context.Background()
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()

	// Create test subscriptions
	sub1 := newTestSubscription(testSubscriptionID)
	sub1.State = arm.SubscriptionStateRegistered
	sub2 := newTestSubscription(testSubscriptionID2)
	sub2.State = arm.SubscriptionStateRegistered

	subCRUD := mockResourcesDBClient.Subscriptions()
	_, err := subCRUD.Create(ctx, sub1, nil)
	require.NoError(t, err)
	_, err = subCRUD.Create(ctx, sub2, nil)
	require.NoError(t, err)

	lister := &DBSubscriptionLister{ResourcesDBClient: mockResourcesDBClient}

	t.Run("List returns both subscriptions before delete", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	// Delete sub1
	err = subCRUD.Delete(ctx, testSubscriptionID)
	require.NoError(t, err)

	t.Run("List returns only remaining subscription after delete", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, testSubscriptionID2, result[0].GetResourceID().SubscriptionID)
	})

	t.Run("Get returns not found for deleted subscription", func(t *testing.T) {
		_, err := lister.Get(ctx, testSubscriptionID)
		require.Error(t, err)
		assert.True(t, database.IsNotFoundError(err))
	})
}

func TestDBSubscriptionListerWithUpdates(t *testing.T) {
	ctx := context.Background()
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()

	// Create test subscription
	sub := newTestSubscription(testSubscriptionID)
	sub.State = arm.SubscriptionStateRegistered

	subCRUD := mockResourcesDBClient.Subscriptions()
	createdSub, err := subCRUD.Create(ctx, sub, nil)
	require.NoError(t, err)

	lister := &DBSubscriptionLister{ResourcesDBClient: mockResourcesDBClient}

	t.Run("Get returns original state before update", func(t *testing.T) {
		result, err := lister.Get(ctx, testSubscriptionID)
		require.NoError(t, err)
		assert.Equal(t, arm.SubscriptionStateRegistered, result.State)
	})

	// Update the subscription
	createdSub.State = arm.SubscriptionStateSuspended
	_, err = subCRUD.Replace(ctx, createdSub, nil)
	require.NoError(t, err)

	t.Run("Get returns updated state after update", func(t *testing.T) {
		result, err := lister.Get(ctx, testSubscriptionID)
		require.NoError(t, err)
		assert.Equal(t, arm.SubscriptionStateSuspended, result.State)
	})
}

func TestDBActiveOperationListerWithDeletes(t *testing.T) {
	ctx := context.Background()
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()

	// Create a test cluster first
	cluster := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	clusterCRUD := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName)
	_, err := clusterCRUD.Create(ctx, cluster, nil)
	require.NoError(t, err)

	// Create test operations
	op1 := newTestOperation(testSubscriptionID, "op1", testSubscriptionID, testResourceGroupName, testClusterName)
	op1.Status = arm.ProvisioningStateProvisioning
	op2 := newTestOperation(testSubscriptionID, "op2", testSubscriptionID, testResourceGroupName, testClusterName)
	op2.Status = arm.ProvisioningStateProvisioning

	opCRUD := mockResourcesDBClient.Operations(testSubscriptionID)
	_, err = opCRUD.Create(ctx, op1, nil)
	require.NoError(t, err)
	_, err = opCRUD.Create(ctx, op2, nil)
	require.NoError(t, err)

	lister := &DBActiveOperationLister{ResourcesDBClient: mockResourcesDBClient}

	t.Run("List returns both operations before delete", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	// Delete op1
	err = opCRUD.Delete(ctx, "op1")
	require.NoError(t, err)

	t.Run("List returns only remaining operation after delete", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, "op2", result[0].OperationID.Name)
	})

	t.Run("Get returns not found for deleted operation", func(t *testing.T) {
		_, err := lister.Get(ctx, testSubscriptionID, "op1")
		require.Error(t, err)
		assert.True(t, database.IsNotFoundError(err))
	})
}

func TestDBActiveOperationListerWithUpdates(t *testing.T) {
	ctx := context.Background()
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()

	// Create a test cluster first
	cluster := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	clusterCRUD := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName)
	_, err := clusterCRUD.Create(ctx, cluster, nil)
	require.NoError(t, err)

	// Create test operation
	op := newTestOperation(testSubscriptionID, "op1", testSubscriptionID, testResourceGroupName, testClusterName)
	op.Status = arm.ProvisioningStateProvisioning

	opCRUD := mockResourcesDBClient.Operations(testSubscriptionID)
	createdOp, err := opCRUD.Create(ctx, op, nil)
	require.NoError(t, err)

	lister := &DBActiveOperationLister{ResourcesDBClient: mockResourcesDBClient}

	t.Run("List returns operation with Provisioning status", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, arm.ProvisioningStateProvisioning, result[0].Status)
	})

	// Update operation to terminal state (Succeeded)
	createdOp.Status = arm.ProvisioningStateSucceeded
	_, err = opCRUD.Replace(ctx, createdOp, nil)
	require.NoError(t, err)

	t.Run("List excludes operation after update to terminal state", func(t *testing.T) {
		// ActiveOperations lister should filter out terminal states
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("Get still returns operation even in terminal state", func(t *testing.T) {
		// Get retrieves by ID regardless of state
		result, err := lister.Get(ctx, testSubscriptionID, "op1")
		require.NoError(t, err)
		assert.Equal(t, arm.ProvisioningStateSucceeded, result.Status)
	})
}

// Tests for NewmockResourcesDBClientWithResources initialization helper

func TestNewMockResourcesDBClientWithResources_Clusters(t *testing.T) {
	ctx := context.Background()

	cluster1 := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	cluster2 := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName2)
	cluster3 := newTestCluster(testSubscriptionID2, testResourceGroupName, testClusterName)

	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster1, cluster2, cluster3})
	require.NoError(t, err)

	lister := &DBClusterLister{ResourcesDBClient: mockResourcesDBClient}

	t.Run("List returns all initialized clusters", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 3)
	})

	t.Run("Get returns initialized cluster", func(t *testing.T) {
		result, err := lister.Get(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)
		assert.Equal(t, testClusterName, result.ID.Name)
	})

	t.Run("ListForResourceGroup returns clusters in resource group", func(t *testing.T) {
		result, err := lister.ListForResourceGroup(ctx, testSubscriptionID, testResourceGroupName)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})
}

func TestNewMockResourcesDBClientWithResources_NodePools(t *testing.T) {
	ctx := context.Background()

	cluster := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	np1 := newTestNodePool(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
	np2 := newTestNodePool(testSubscriptionID, testResourceGroupName, testClusterName, "nodepool-2")

	// Create cluster first, then node pools
	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, np1, np2})
	require.NoError(t, err)

	lister := &DBNodePoolLister{ResourcesDBClient: mockResourcesDBClient}

	t.Run("List returns all initialized node pools", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("Get returns initialized node pool", func(t *testing.T) {
		result, err := lister.Get(ctx, testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
		require.NoError(t, err)
		assert.Equal(t, testNodePoolName, result.ID.Name)
	})

	t.Run("ListForCluster returns node pools for cluster", func(t *testing.T) {
		result, err := lister.ListForCluster(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})
}

func TestNewMockResourcesDBClientWithResources_Subscriptions(t *testing.T) {
	ctx := context.Background()

	sub1 := newTestSubscription(testSubscriptionID)
	sub1.State = arm.SubscriptionStateRegistered
	sub2 := newTestSubscription(testSubscriptionID2)
	sub2.State = arm.SubscriptionStateRegistered

	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{sub1, sub2})
	require.NoError(t, err)

	lister := &DBSubscriptionLister{ResourcesDBClient: mockResourcesDBClient}

	t.Run("List returns all initialized subscriptions", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("Get returns initialized subscription", func(t *testing.T) {
		result, err := lister.Get(ctx, testSubscriptionID)
		require.NoError(t, err)
		assert.Equal(t, testSubscriptionID, result.GetResourceID().SubscriptionID)
	})
}

func TestNewMockResourcesDBClientWithResources_Operations(t *testing.T) {
	ctx := context.Background()

	cluster := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	op1 := newTestOperation(testSubscriptionID, "op1", testSubscriptionID, testResourceGroupName, testClusterName)
	op1.Status = arm.ProvisioningStateProvisioning
	op2 := newTestOperation(testSubscriptionID, "op2", testSubscriptionID, testResourceGroupName, testClusterName)
	op2.Status = arm.ProvisioningStateProvisioning

	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, op1, op2})
	require.NoError(t, err)

	lister := &DBActiveOperationLister{ResourcesDBClient: mockResourcesDBClient}

	t.Run("List returns all initialized operations", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("Get returns initialized operation", func(t *testing.T) {
		result, err := lister.Get(ctx, testSubscriptionID, "op1")
		require.NoError(t, err)
		assert.Equal(t, "op1", result.OperationID.Name)
	})

	t.Run("ListActiveOperationsForCluster returns operations for cluster", func(t *testing.T) {
		result, err := lister.ListActiveOperationsForCluster(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})
}

func TestNewMockResourcesDBClientWithResources_ExternalAuths(t *testing.T) {
	ctx := context.Background()

	cluster := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	ea1 := newTestExternalAuth(testSubscriptionID, testResourceGroupName, testClusterName, testExternalAuthName)
	ea2 := newTestExternalAuth(testSubscriptionID, testResourceGroupName, testClusterName, "external-auth-2")

	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, ea1, ea2})
	require.NoError(t, err)

	lister := &DBExternalAuthLister{ResourcesDBClient: mockResourcesDBClient}

	t.Run("List returns all initialized external auths", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("Get returns initialized external auth", func(t *testing.T) {
		result, err := lister.Get(ctx, testSubscriptionID, testResourceGroupName, testClusterName, testExternalAuthName)
		require.NoError(t, err)
		assert.Equal(t, testExternalAuthName, result.ID.Name)
	})

	t.Run("ListForCluster returns external auths for cluster", func(t *testing.T) {
		result, err := lister.ListForCluster(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})
}

func TestNewMockResourcesDBClientWithResources_ServiceProviderClusters(t *testing.T) {
	ctx := context.Background()

	cluster := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	spc := newTestServiceProviderCluster(testSubscriptionID, testResourceGroupName, testClusterName)

	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc})
	require.NoError(t, err)

	lister := &DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDBClient}

	t.Run("List returns all initialized service provider clusters", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 1)
	})

	t.Run("Get returns initialized service provider cluster", func(t *testing.T) {
		result, err := lister.Get(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)
		require.NotNil(t, result.GetResourceID())
		assert.Equal(t, "default", result.GetResourceID().Name)
	})

	t.Run("ListForCluster returns service provider clusters for cluster", func(t *testing.T) {
		result, err := lister.ListForCluster(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)
		assert.Len(t, result, 1)
	})
}

func TestNewMockResourcesDBClientWithResources_MixedTypes(t *testing.T) {
	ctx := context.Background()

	// Create a mix of all resource types
	cluster := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	np := newTestNodePool(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
	op := newTestOperation(testSubscriptionID, "op1", testSubscriptionID, testResourceGroupName, testClusterName)
	op.Status = arm.ProvisioningStateProvisioning
	ea := newTestExternalAuth(testSubscriptionID, testResourceGroupName, testClusterName, testExternalAuthName)
	spc := newTestServiceProviderCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	sub := newTestSubscription(testSubscriptionID)
	sub.State = arm.SubscriptionStateRegistered

	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{
		cluster,
		np,
		op,
		ea,
		spc,
		sub,
	})
	require.NoError(t, err)

	t.Run("All clusters can be listed", func(t *testing.T) {
		lister := &DBClusterLister{ResourcesDBClient: mockResourcesDBClient}
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 1)
	})

	t.Run("All node pools can be listed", func(t *testing.T) {
		lister := &DBNodePoolLister{ResourcesDBClient: mockResourcesDBClient}
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 1)
	})

	t.Run("All operations can be listed", func(t *testing.T) {
		lister := &DBActiveOperationLister{ResourcesDBClient: mockResourcesDBClient}
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 1)
	})

	t.Run("All external auths can be listed", func(t *testing.T) {
		lister := &DBExternalAuthLister{ResourcesDBClient: mockResourcesDBClient}
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 1)
	})

	t.Run("All service provider clusters can be listed", func(t *testing.T) {
		lister := &DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDBClient}
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 1)
	})

	t.Run("All subscriptions can be listed", func(t *testing.T) {
		lister := &DBSubscriptionLister{ResourcesDBClient: mockResourcesDBClient}
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 1)
	})
}

func TestNewMockResourcesDBClientWithResources_EmptySlice(t *testing.T) {
	ctx := context.Background()

	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{})
	require.NoError(t, err)
	require.NotNil(t, mockResourcesDBClient)

	lister := &DBClusterLister{ResourcesDBClient: mockResourcesDBClient}
	result, err := lister.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestNewMockResourcesDBClientWithResources_UnsupportedType(t *testing.T) {
	ctx := context.Background()

	// Try to add an unsupported type
	_, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{"unsupported string"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported resource type")
}

func TestNewMockResourcesDBClientWithResources_NilResourceID(t *testing.T) {
	ctx := context.Background()

	// Create a cluster without a resource ID
	clusterWithNilID := &api.HCPOpenShiftCluster{}

	_, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{clusterWithNilID})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing resource ID")
}
