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
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
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
		Clusters: []*api.HCPOpenShiftCluster{cluster1, cluster2, cluster3},
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
		assert.True(t, database.IsResponseError(err, http.StatusNotFound))
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
		NodePools: []*api.HCPOpenShiftClusterNodePool{np1, np2, np3},
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
		assert.True(t, database.IsResponseError(err, http.StatusNotFound))
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
		Operations: []*api.Operation{op1, op2, op3},
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
		assert.True(t, database.IsResponseError(err, http.StatusNotFound))
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
		ExternalAuths: []*api.HCPOpenShiftClusterExternalAuth{ea1, ea2, ea3},
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
		assert.True(t, database.IsResponseError(err, http.StatusNotFound))
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
		ServiceProviderClusters: []*api.ServiceProviderCluster{spc1, spc2},
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
		assert.True(t, database.IsResponseError(err, http.StatusNotFound))
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
		Subscriptions: []*arm.Subscription{sub1, sub2},
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
		assert.True(t, database.IsResponseError(err, http.StatusNotFound))
	})
}

func TestSliceClusterListerWithEmptySlice(t *testing.T) {
	lister := &SliceClusterLister{
		Clusters: []*api.HCPOpenShiftCluster{},
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
		assert.True(t, database.IsResponseError(err, http.StatusNotFound))
	})

	t.Run("ListForResourceGroup returns empty slice", func(t *testing.T) {
		result, err := lister.ListForResourceGroup(ctx, testSubscriptionID, testResourceGroupName)
		require.NoError(t, err)
		assert.Empty(t, result)
	})
}

func TestSliceClusterListerWithNilResourceID(t *testing.T) {
	clusterWithNilID := &api.HCPOpenShiftCluster{}
	validCluster := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName)

	lister := &SliceClusterLister{
		Clusters: []*api.HCPOpenShiftCluster{clusterWithNilID, validCluster},
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

func newTestCluster(subscriptionID, resourceGroupName, clusterName string) *api.HCPOpenShiftCluster {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName,
	))
	return &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: clusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/" + clusterName)),
		},
	}
}

func newTestNodePool(subscriptionID, resourceGroupName, clusterName, nodePoolName string) *api.HCPOpenShiftClusterNodePool {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/nodePools/" + nodePoolName,
	))
	return &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: nodePoolName,
				Type: resourceID.ResourceType.String(),
			},
		},
	}
}

func newTestOperation(subscriptionID, operationName, targetSubscription, targetResourceGroup, targetCluster string) *api.Operation {
	operationResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/" + operationName,
	))
	externalID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + targetSubscription +
			"/resourceGroups/" + targetResourceGroup +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + targetCluster,
	))
	return &api.Operation{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID: operationResourceID,
		},
		OperationID: operationResourceID,
		ExternalID:  externalID,
	}
}

func newTestExternalAuth(subscriptionID, resourceGroupName, clusterName, externalAuthName string) *api.HCPOpenShiftClusterExternalAuth {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/externalAuths/" + externalAuthName,
	))
	return &api.HCPOpenShiftClusterExternalAuth{
		ProxyResource: arm.NewProxyResource(resourceID),
	}
}

func newTestServiceProviderCluster(subscriptionID, resourceGroupName, clusterName string) *api.ServiceProviderCluster {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/serviceProviderClusters/default",
	))
	return &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID: resourceID,
		},
		ResourceID: *resourceID,
	}
}

func newTestClusterController(subscriptionID, resourceGroupName, clusterName, controllerName string) *api.Controller {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/hcpOpenShiftControllers/" + controllerName,
	))
	return &api.Controller{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID: resourceID,
		},
		ResourceID: resourceID,
	}
}

func newTestNodePoolController(subscriptionID, resourceGroupName, clusterName, nodePoolName, controllerName string) *api.Controller {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/nodePools/" + nodePoolName +
			"/hcpOpenShiftControllers/" + controllerName,
	))
	return &api.Controller{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID: resourceID,
		},
		ResourceID: resourceID,
	}
}

func newTestExternalAuthController(subscriptionID, resourceGroupName, clusterName, externalAuthName, controllerName string) *api.Controller {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/externalAuths/" + externalAuthName +
			"/hcpOpenShiftControllers/" + controllerName,
	))
	return &api.Controller{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID: resourceID,
		},
		ResourceID: resourceID,
	}
}

func newTestSubscription(subscriptionID string) *arm.Subscription {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID,
	))
	return &arm.Subscription{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID: resourceID,
		},
		ResourceID: resourceID,
	}
}
