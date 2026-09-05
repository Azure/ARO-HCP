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

package corelistertesting

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
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
		Clusters: []*coreapi.HCPOpenShiftCluster{cluster1, cluster2, cluster3},
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
		assert.True(t, cosmosstorageutils.IsNotFoundError(err))
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
		NodePools: []*coreapi.HCPOpenShiftClusterNodePool{np1, np2, np3},
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
		assert.True(t, cosmosstorageutils.IsNotFoundError(err))
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
	clusterOp1 := newTestOperation(testSubscriptionID, "op1", testSubscriptionID, testResourceGroupName, testClusterName)
	clusterOp2 := newTestOperation(testSubscriptionID, "op2", testSubscriptionID, testResourceGroupName, testClusterName)
	clusterOp3 := newTestOperation(testSubscriptionID, "op3", testSubscriptionID, testResourceGroupName, testClusterName2)
	npOp1 := newTestNodePoolOperation(testSubscriptionID, "np-op1", testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
	npOp2 := newTestNodePoolOperation(testSubscriptionID, "np-op2", testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
	npOp3 := newTestNodePoolOperation(testSubscriptionID, "np-op3", testSubscriptionID, testResourceGroupName, testClusterName, "nodepool-2")
	eaOp1 := newTestExternalAuthOperation(testSubscriptionID, "ea-op1", testSubscriptionID, testResourceGroupName, testClusterName, testExternalAuthName)
	eaOp2 := newTestExternalAuthOperation(testSubscriptionID, "ea-op2", testSubscriptionID, testResourceGroupName, testClusterName, "external-auth-2")

	lister := &SliceActiveOperationLister{
		Operations: []*coreapi.Operation{clusterOp1, clusterOp2, clusterOp3, npOp1, npOp2, npOp3, eaOp1, eaOp2},
	}

	ctx := context.Background()

	t.Run("List returns all operations", func(t *testing.T) {
		result, err := lister.List(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 8)
	})

	t.Run("Get returns matching operation", func(t *testing.T) {
		result, err := lister.Get(ctx, testSubscriptionID, "op1")
		require.NoError(t, err)
		assert.Equal(t, "op1", result.OperationID.Name)
	})

	t.Run("Get returns not found for non-existent operation", func(t *testing.T) {
		_, err := lister.Get(ctx, testSubscriptionID, "non-existent")
		require.Error(t, err)
		assert.True(t, cosmosstorageutils.IsNotFoundError(err))
	})

	t.Run("ListActiveOperationsForCluster returns operations for cluster including child resources", func(t *testing.T) {
		result, err := lister.ListActiveOperationsForCluster(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
		require.NoError(t, err)
		assert.Len(t, result, 7)
	})

	t.Run("ListActiveOperationsForNodePool returns operations for node pool", func(t *testing.T) {
		result, err := lister.ListActiveOperationsForNodePool(ctx, testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("ListActiveOperationsForExternalAuth returns operations for external auth", func(t *testing.T) {
		result, err := lister.ListActiveOperationsForExternalAuth(ctx, testSubscriptionID, testResourceGroupName, testClusterName, testExternalAuthName)
		require.NoError(t, err)
		assert.Len(t, result, 1)
	})
}

func TestSliceExternalAuthLister(t *testing.T) {
	ea1 := newTestExternalAuth(testSubscriptionID, testResourceGroupName, testClusterName, testExternalAuthName)
	ea2 := newTestExternalAuth(testSubscriptionID, testResourceGroupName, testClusterName, "external-auth-2")
	ea3 := newTestExternalAuth(testSubscriptionID, testResourceGroupName, testClusterName2, testExternalAuthName)

	lister := &SliceExternalAuthLister{
		ExternalAuths: []*coreapi.HCPOpenShiftClusterExternalAuth{ea1, ea2, ea3},
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
		assert.True(t, cosmosstorageutils.IsNotFoundError(err))
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
		ServiceProviderClusters: []*coreapi.ServiceProviderCluster{spc1, spc2},
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
		assert.True(t, cosmosstorageutils.IsNotFoundError(err))
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
		Subscriptions: []*coreapi.Subscription{sub1, sub2},
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
		assert.True(t, cosmosstorageutils.IsNotFoundError(err))
	})
}

func TestSliceClusterListerWithEmptySlice(t *testing.T) {
	lister := &SliceClusterLister{
		Clusters: []*coreapi.HCPOpenShiftCluster{},
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
		assert.True(t, cosmosstorageutils.IsNotFoundError(err))
	})

	t.Run("ListForResourceGroup returns empty slice", func(t *testing.T) {
		result, err := lister.ListForResourceGroup(ctx, testSubscriptionID, testResourceGroupName)
		require.NoError(t, err)
		assert.Empty(t, result)
	})
}

func TestSliceClusterListerWithNilResourceID(t *testing.T) {
	clusterWithNilID := &coreapi.HCPOpenShiftCluster{}
	validCluster := newTestCluster(testSubscriptionID, testResourceGroupName, testClusterName)

	lister := &SliceClusterLister{
		Clusters: []*coreapi.HCPOpenShiftCluster{clusterWithNilID, validCluster},
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

func newTestCluster(subscriptionID, resourceGroupName, clusterName string) *coreapi.HCPOpenShiftCluster {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName,
	))
	return &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: clusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: metadataapi.Ptr(metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/" + clusterName))),
		},
	}
}

func newTestNodePool(subscriptionID, resourceGroupName, clusterName, nodePoolName string) *coreapi.HCPOpenShiftClusterNodePool {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/nodePools/" + nodePoolName,
	))
	return &coreapi.HCPOpenShiftClusterNodePool{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(resourceID.SubscriptionID)},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: nodePoolName,
				Type: resourceID.ResourceType.String(),
			},
		},
	}
}

func newTestOperation(subscriptionID, operationName, targetSubscription, targetResourceGroup, targetCluster string) *coreapi.Operation {
	operationResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/" + operationName,
	))
	externalID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + targetSubscription +
			"/resourceGroups/" + targetResourceGroup +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + targetCluster,
	))
	return &coreapi.Operation{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   operationResourceID,
			PartitionKey: strings.ToLower(operationResourceID.SubscriptionID),
		},
		OperationID: operationResourceID,
		ExternalID:  externalID,
	}
}

func newTestNodePoolOperation(subscriptionID, operationName, targetSubscription, targetResourceGroup, targetCluster, nodePoolName string) *coreapi.Operation {
	operationResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/" + operationName,
	))
	externalID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + targetSubscription +
			"/resourceGroups/" + targetResourceGroup +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + targetCluster +
			"/nodePools/" + nodePoolName,
	))
	return &coreapi.Operation{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   operationResourceID,
			PartitionKey: strings.ToLower(operationResourceID.SubscriptionID),
		},
		OperationID: operationResourceID,
		ExternalID:  externalID,
	}
}

func newTestExternalAuthOperation(subscriptionID, operationName, targetSubscription, targetResourceGroup, targetCluster, externalAuthName string) *coreapi.Operation {
	operationResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/" + operationName,
	))
	externalID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + targetSubscription +
			"/resourceGroups/" + targetResourceGroup +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + targetCluster +
			"/externalAuths/" + externalAuthName,
	))
	return &coreapi.Operation{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   operationResourceID,
			PartitionKey: strings.ToLower(operationResourceID.SubscriptionID),
		},
		OperationID: operationResourceID,
		ExternalID:  externalID,
	}
}

func newTestExternalAuth(subscriptionID, resourceGroupName, clusterName, externalAuthName string) *coreapi.HCPOpenShiftClusterExternalAuth {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/externalAuths/" + externalAuthName,
	))
	return &coreapi.HCPOpenShiftClusterExternalAuth{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(resourceID.SubscriptionID)},
		ProxyResource:  coreapi.NewProxyResource(resourceID),
	}
}

func newTestServiceProviderCluster(subscriptionID, resourceGroupName, clusterName string) *coreapi.ServiceProviderCluster {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/serviceProviderClusters/default",
	))
	return &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
	}
}

func newTestClusterController(subscriptionID, resourceGroupName, clusterName, controllerName string) *coreapi.Controller {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/hcpOpenShiftControllers/" + controllerName,
	))
	return &coreapi.Controller{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
	}
}

func newTestNodePoolController(subscriptionID, resourceGroupName, clusterName, nodePoolName, controllerName string) *coreapi.Controller {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/nodePools/" + nodePoolName +
			"/hcpOpenShiftControllers/" + controllerName,
	))
	return &coreapi.Controller{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
	}
}

func newTestExternalAuthController(subscriptionID, resourceGroupName, clusterName, externalAuthName, controllerName string) *coreapi.Controller {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/externalAuths/" + externalAuthName +
			"/hcpOpenShiftControllers/" + controllerName,
	))
	return &coreapi.Controller{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
	}
}

func newTestSubscription(subscriptionID string) *coreapi.Subscription {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID,
	))
	return &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
	}
}

func newTestClusterScopedManagementClusterContent(subscriptionID, resourceGroupName, clusterName, mccName string) *coreapi.ManagementClusterContent {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/managementClusterContents/" + mccName,
	))
	return &coreapi.ManagementClusterContent{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(resourceID.SubscriptionID)},
	}
}

func newTestNodePoolScopedManagementClusterContent(subscriptionID, resourceGroupName, clusterName, nodePoolName, mccName string) *coreapi.ManagementClusterContent {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/nodePools/" + nodePoolName +
			"/managementClusterContents/" + mccName,
	))
	return &coreapi.ManagementClusterContent{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(resourceID.SubscriptionID)},
	}
}
