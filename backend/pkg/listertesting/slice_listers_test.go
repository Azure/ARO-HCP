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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	metaapi "github.com/Azure/ARO-HCP/internal/apis/meta"
	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
	armresourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources/arm"
	"github.com/Azure/ARO-HCP/internal/database"
)

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	testSubscriptionID2   = "11111111-1111-1111-1111-111111111111"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
	testClusterName2      = "other-cluster"
	testNodePoolName      = "test-nodepool"
	testExternalAuthName  = "test-external-auth"
)

func TestSliceClusterLister(t *testing.T) {
	cluster1 := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	cluster2 := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName2)
	cluster3 := newTestCluster(testSubscriptionID2, testResourceGroupName, testClusterName)

	lister := &SliceClusterLister{
		Clusters: []*resourcesapi.HCPOpenShiftCluster{cluster1, cluster2, cluster3},
	}

	ctx := context.Background()

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

func TestSliceNodePoolLister(t *testing.T) {
	np1 := newTestNodePool(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
	np2 := newTestNodePool(testSubscriptionID, testResourceGroupName, testClusterName, "nodepool-2")
	np3 := newTestNodePool(testSubscriptionID, testResourceGroupName, testClusterName2, testNodePoolName)

	lister := &SliceNodePoolLister{
		NodePools: []*resourcesapi.HCPOpenShiftClusterNodePool{np1, np2, np3},
	}

	ctx := context.Background()

	t.Run("List returns all node pools", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 3)
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

	t.Run("ListForResourceGroup returns node pools in resource group", func(t *testing.T) {
		result, err := lister.ListForResourceGroup(ctx, testSubscriptionID, testResourceGroupName)
		require.NoError(t, err)
		assert.Len(t, result, 3)
	})

	t.Run("ListForCluster returns node pools for cluster", func(t *testing.T) {
		result, err := lister.ListForCluster(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})
}

func TestSliceActiveOperationLister(t *testing.T) {
	op1 := newTestOperation(testSubscriptionID, "op1", testSubscriptionID, testResourceGroupName, testClusterName)
	op2 := newTestOperation(testSubscriptionID, "op2", testSubscriptionID, testResourceGroupName, testClusterName)
	op3 := newTestOperation(testSubscriptionID, "op3", testSubscriptionID, testResourceGroupName, testClusterName2)

	lister := &SliceActiveOperationLister{
		Operations: []*resourcesapi.Operation{op1, op2, op3},
	}

	ctx := context.Background()

	t.Run("List returns all operations", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 3)
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

func TestSliceExternalAuthLister(t *testing.T) {
	ea1 := newTestExternalAuth(testSubscriptionID, testResourceGroupName, testClusterName, testExternalAuthName)
	ea2 := newTestExternalAuth(testSubscriptionID, testResourceGroupName, testClusterName, "external-auth-2")
	ea3 := newTestExternalAuth(testSubscriptionID, testResourceGroupName, testClusterName2, testExternalAuthName)

	lister := &SliceExternalAuthLister{
		ExternalAuths: []*resourcesapi.HCPOpenShiftClusterExternalAuth{ea1, ea2, ea3},
	}

	ctx := context.Background()

	t.Run("List returns all external auths", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 3)
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

	t.Run("ListForResourceGroup returns external auths in resource group", func(t *testing.T) {
		result, err := lister.ListForResourceGroup(ctx, testSubscriptionID, testResourceGroupName)
		require.NoError(t, err)
		assert.Len(t, result, 3)
	})

	t.Run("ListForCluster returns external auths for cluster", func(t *testing.T) {
		result, err := lister.ListForCluster(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})
}

func TestSliceServiceProviderClusterLister(t *testing.T) {
	spc1 := newTestServiceProviderCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	spc2 := newTestServiceProviderCluster(testSubscriptionID, testResourceGroupName, testClusterName2)

	lister := &SliceServiceProviderClusterLister{
		ServiceProviderClusters: []*resourcesapi.ServiceProviderCluster{spc1, spc2},
	}

	ctx := context.Background()

	t.Run("List returns all service provider clusters", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("Get returns matching service provider cluster", func(t *testing.T) {
		result, err := lister.Get(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)
		require.NotNil(t, result.GetResourceID())
		assert.Equal(t, testClusterName, result.GetResourceID().Parent.Name)
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

func TestSliceSubscriptionLister(t *testing.T) {
	sub1 := newTestSubscription(testSubscriptionID)
	sub2 := newTestSubscription(testSubscriptionID2)

	lister := &SliceSubscriptionLister{
		Subscriptions: []*armresourcesapi.Subscription{sub1, sub2},
	}

	ctx := context.Background()

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

func TestSliceClusterListerWithEmptySlice(t *testing.T) {
	lister := &SliceClusterLister{
		Clusters: []*resourcesapi.HCPOpenShiftCluster{},
	}

	ctx := context.Background()

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

func TestSliceClusterListerWithNilResourceID(t *testing.T) {
	clusterWithNilID := &resourcesapi.HCPOpenShiftCluster{}
	validCluster := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName)

	lister := &SliceClusterLister{
		Clusters: []*resourcesapi.HCPOpenShiftCluster{clusterWithNilID, validCluster},
	}

	ctx := context.Background()

	t.Run("List includes all clusters", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("Get skips clusters with nil ID", func(t *testing.T) {
		result, err := lister.Get(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)
		assert.Equal(t, testClusterName, result.ID.Name)
	})

	t.Run("ListForResourceGroup skips clusters with nil ID", func(t *testing.T) {
		result, err := lister.ListForResourceGroup(ctx, testSubscriptionID, testResourceGroupName)
		require.NoError(t, err)
		assert.Len(t, result, 1)
	})
}

// Helper functions to create test resources

func newTestCluster(subscriptionID, resourceGroupName, clusterName string) *resourcesapi.HCPOpenShiftCluster {
	resourceID := resourcesapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName,
	))
	return &resourcesapi.HCPOpenShiftCluster{
		TrackedResource: armresourcesapi.TrackedResource{
			Resource: armresourcesapi.Resource{
				ID:   resourceID,
				Name: clusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: resourcesapi.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: resourcesapi.Ptr(resourcesapi.Must(resourcesapi.NewInternalID("/api/clusters_mgmt/v1/clusters/" + clusterName))),
		},
	}
}

func newTestNodePool(subscriptionID, resourceGroupName, clusterName, nodePoolName string) *resourcesapi.HCPOpenShiftClusterNodePool {
	resourceID := resourcesapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/nodePools/" + nodePoolName,
	))
	return &resourcesapi.HCPOpenShiftClusterNodePool{
		TrackedResource: armresourcesapi.TrackedResource{
			Resource: armresourcesapi.Resource{
				ID:   resourceID,
				Name: nodePoolName,
				Type: resourceID.ResourceType.String(),
			},
		},
	}
}

func newTestOperation(subscriptionID, operationName, targetSubscription, targetResourceGroup, targetCluster string) *resourcesapi.Operation {
	operationResourceID := resourcesapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/" + operationName,
	))
	externalID := resourcesapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + targetSubscription +
			"/resourceGroups/" + targetResourceGroup +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + targetCluster,
	))
	return &resourcesapi.Operation{
		CosmosMetadata: metaapi.CosmosMetadata{
			ResourceID: operationResourceID,
		},
		OperationID: operationResourceID,
		ExternalID:  externalID,
	}
}

func newTestExternalAuth(subscriptionID, resourceGroupName, clusterName, externalAuthName string) *resourcesapi.HCPOpenShiftClusterExternalAuth {
	resourceID := resourcesapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/externalAuths/" + externalAuthName,
	))
	return &resourcesapi.HCPOpenShiftClusterExternalAuth{
		ProxyResource: armresourcesapi.NewProxyResource(resourceID),
	}
}

func newTestServiceProviderCluster(subscriptionID, resourceGroupName, clusterName string) *resourcesapi.ServiceProviderCluster {
	resourceID := resourcesapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/serviceProviderClusters/default",
	))
	return &resourcesapi.ServiceProviderCluster{
		CosmosMetadata: metaapi.CosmosMetadata{
			ResourceID: resourceID,
		},
	}
}

func newTestClusterController(subscriptionID, resourceGroupName, clusterName, controllerName string) *resourcesapi.Controller {
	resourceID := resourcesapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/hcpOpenShiftControllers/" + controllerName,
	))
	return &resourcesapi.Controller{
		CosmosMetadata: metaapi.CosmosMetadata{
			ResourceID: resourceID,
		},
	}
}

func newTestNodePoolController(subscriptionID, resourceGroupName, clusterName, nodePoolName, controllerName string) *resourcesapi.Controller {
	resourceID := resourcesapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/nodePools/" + nodePoolName +
			"/hcpOpenShiftControllers/" + controllerName,
	))
	return &resourcesapi.Controller{
		CosmosMetadata: metaapi.CosmosMetadata{
			ResourceID: resourceID,
		},
	}
}

func newTestExternalAuthController(subscriptionID, resourceGroupName, clusterName, externalAuthName, controllerName string) *resourcesapi.Controller {
	resourceID := resourcesapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/externalAuths/" + externalAuthName +
			"/hcpOpenShiftControllers/" + controllerName,
	))
	return &resourcesapi.Controller{
		CosmosMetadata: metaapi.CosmosMetadata{
			ResourceID: resourceID,
		},
	}
}

func newTestSubscription(subscriptionID string) *armresourcesapi.Subscription {
	resourceID := resourcesapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID,
	))
	return &armresourcesapi.Subscription{
		CosmosMetadata: metaapi.CosmosMetadata{
			ResourceID: resourceID,
		},
		ResourceID: resourceID,
	}
}

func newTestClusterScopedManagementClusterContent(subscriptionID, resourceGroupName, clusterName, mccName string) *resourcesapi.ManagementClusterContent {
	resourceID := resourcesapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/managementClusterContents/" + mccName,
	))
	return &resourcesapi.ManagementClusterContent{
		CosmosMetadata: metaapi.CosmosMetadata{ResourceID: resourceID},
	}
}

func newTestNodePoolScopedManagementClusterContent(subscriptionID, resourceGroupName, clusterName, nodePoolName, mccName string) *resourcesapi.ManagementClusterContent {
	resourceID := resourcesapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/nodePools/" + nodePoolName +
			"/managementClusterContents/" + mccName,
	))
	return &resourcesapi.ManagementClusterContent{
		CosmosMetadata: metaapi.CosmosMetadata{ResourceID: resourceID},
	}
}
