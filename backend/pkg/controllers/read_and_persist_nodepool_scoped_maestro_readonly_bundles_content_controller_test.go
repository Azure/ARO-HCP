// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

// errorInjectingResourcesDBClientForNodePoolReadPersist wraps mockResourcesDBClient to return error-injecting CRUDs.
type errorInjectingResourcesDBClientForNodePoolReadPersist struct {
	*databasetesting.MockResourcesDBClient
	spnpCRUD database.ServiceProviderNodePoolCRUD
}

func (e *errorInjectingResourcesDBClientForNodePoolReadPersist) ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName string) database.ServiceProviderNodePoolCRUD {
	if e.spnpCRUD != nil {
		return e.spnpCRUD
	}
	return e.MockResourcesDBClient.ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName)
}

var _ database.ResourcesDBClient = &errorInjectingResourcesDBClientForNodePoolReadPersist{}

// errorInjectingSPNPCRUD wraps ServiceProviderNodePoolCRUD to allow error injection.
type errorInjectingSPNPCRUD struct {
	database.ServiceProviderNodePoolCRUD
	getErr error
}

func (e *errorInjectingSPNPCRUD) Get(ctx context.Context, resourceID string) (*api.ServiceProviderNodePool, error) {
	if e.getErr != nil {
		return nil, e.getErr
	}
	return e.ServiceProviderNodePoolCRUD.Get(ctx, resourceID)
}

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_NodePoolNotFound(t *testing.T) {
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker:   &alwaysSyncCooldownChecker{},
		resourcesDBClient: mockResourcesDBClient,
	}

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	// No nodepool in DB -> Get returns NotFound -> SyncOnce returns nil (no work to do)
	err := syncer.SyncOnce(context.Background(), key)
	assert.NoError(t, err)
}

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_EmptyClusterServiceID(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)

	syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker:      &alwaysSyncCooldownChecker{},
		resourcesDBClient:    mockResourcesDBClient,
		clusterServiceClient: mockClusterService,
	}

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	nodepoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool"))
	nodepool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodepoolResourceID,
				Name: "test-nodepool",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.InternalID{},
		},
	}
	nodepoolsCRUD := mockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err := nodepoolsCRUD.Create(ctx, nodepool, nil)
	require.NoError(t, err)

	bundleInternalName := api.MaestroBundleInternalNameReadonlyHypershiftNodePool
	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool/serviceProviderNodePools/default"))
	spnp := &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
		Status: api.ServiceProviderNodePoolStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{
				{Name: bundleInternalName, MaestroAPIMaestroBundleName: "bundle-name"},
			},
		},
	}
	spnpCRUD := mockResourcesDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	_, err = spnpCRUD.Create(ctx, spnp, nil)
	require.NoError(t, err)

	// Cluster service ID not yet populated: skip sync (no OCM / Maestro calls).
	err = syncer.SyncOnce(ctx, key)
	assert.NoError(t, err)
}

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_GetServiceProviderNodePoolError(t *testing.T) {
	ctx := context.Background()

	baseMockResourcesDBClient := databasetesting.NewMockResourcesDBClient()

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	nodepoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool"))
	nodepool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodepoolResourceID,
				Name: "test-nodepool",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	nodepoolsCRUD := baseMockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err := nodepoolsCRUD.Create(ctx, nodepool, nil)
	require.NoError(t, err)

	expectedError := fmt.Errorf("database error")
	mockResourcesDBClient := &errorInjectingResourcesDBClientForNodePoolReadPersist{
		MockResourcesDBClient: baseMockResourcesDBClient,
		spnpCRUD: &errorInjectingSPNPCRUD{
			getErr: expectedError,
		},
	}

	syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker:   &alwaysSyncCooldownChecker{},
		resourcesDBClient: mockResourcesDBClient,
	}

	err = syncer.SyncOnce(ctx, key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get or create ServiceProviderNodePool")
}

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_NoMaestroReadonlyBundlesRefs(t *testing.T) {
	ctx := context.Background()
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker:   &alwaysSyncCooldownChecker{},
		resourcesDBClient: mockResourcesDBClient,
	}

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	nodepoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool"))
	nodepool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodepoolResourceID,
				Name: "test-nodepool",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	nodepoolsCRUD := mockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err := nodepoolsCRUD.Create(ctx, nodepool, nil)
	require.NoError(t, err)

	// SPNP with no bundle references -> SyncOnce returns nil (nothing to process)
	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool/serviceProviderNodePools/default"))
	spnp := &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
	}
	spnpCRUD := mockResourcesDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	_, err = spnpCRUD.Create(ctx, spnp, nil)
	require.NoError(t, err)

	err = syncer.SyncOnce(ctx, key)
	assert.NoError(t, err)
}

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_GetProvisionShardError(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)

	syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker:      &alwaysSyncCooldownChecker{},
		resourcesDBClient:    mockResourcesDBClient,
		clusterServiceClient: mockClusterService,
	}

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	nodepoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool"))
	nodepool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodepoolResourceID,
				Name: "test-nodepool",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	nodepoolsCRUD := mockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err := nodepoolsCRUD.Create(ctx, nodepool, nil)
	require.NoError(t, err)

	bundleInternalName := api.MaestroBundleInternalNameReadonlyHypershiftNodePool
	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool/serviceProviderNodePools/default"))
	spnp := &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
		Status: api.ServiceProviderNodePoolStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{
				{Name: bundleInternalName, MaestroAPIMaestroBundleName: "bundle-name"},
			},
		},
	}
	spnpCRUD := mockResourcesDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	_, err = spnpCRUD.Create(ctx, spnp, nil)
	require.NoError(t, err)

	mockClusterService.EXPECT().
		GetClusterProvisionShard(gomock.Any(), nodepool.ServiceProviderProperties.ClusterServiceID).
		Return(nil, fmt.Errorf("provision shard error"))

	err = syncer.SyncOnce(ctx, key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get Cluster Provision Shard")
}

func TestReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_ReadAndPersistFlow(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
	mockMaestroClient := maestro.NewMockClient(ctrl)

	syncer := &readAndPersistNodePoolScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker:                    &alwaysSyncCooldownChecker{},
		resourcesDBClient:                  mockResourcesDBClient,
		clusterServiceClient:               mockClusterService,
		maestroClientBuilder:               mockMaestroBuilder,
		maestroSourceEnvironmentIdentifier: "test-env",
	}

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	nodepoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool"))
	nodepool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodepoolResourceID,
				Name: "test-nodepool",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	nodepoolsCRUD := mockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err := nodepoolsCRUD.Create(ctx, nodepool, nil)
	require.NoError(t, err)

	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool/serviceProviderNodePools/default"))
	spnp := &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
		Status: api.ServiceProviderNodePoolStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{
				{Name: api.MaestroBundleInternalNameReadonlyHypershiftNodePool, MaestroAPIMaestroBundleName: "bundle-name"},
			},
		},
	}
	spnpCRUD := mockResourcesDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	_, err = spnpCRUD.Create(ctx, spnp, nil)
	require.NoError(t, err)

	provisionShard := buildTestProvisionShard("test-consumer")
	mockClusterService.EXPECT().
		GetClusterProvisionShard(gomock.Any(), nodepool.ServiceProviderProperties.ClusterServiceID).
		Return(provisionShard, nil)

	restEndpoint := provisionShard.MaestroConfig().RestApiConfig().Url()
	grpcEndpoint := provisionShard.MaestroConfig().GrpcApiConfig().Url()
	consumerName := provisionShard.MaestroConfig().ConsumerName()
	sourceID := maestro.GenerateMaestroSourceID("test-env", provisionShard.ID())
	mockMaestroBuilder.EXPECT().
		NewClient(gomock.Any(), restEndpoint, grpcEndpoint, consumerName, sourceID).
		Return(mockMaestroClient, nil)

	validNPJSON := `{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"NodePool","metadata":{"name":"np1","namespace":"ns1"}}`
	bundle := buildTestMaestroBundleWithStatusFeedback("bundle-name", "test-consumer", validNPJSON)
	mockMaestroClient.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(bundle, nil)

	err = syncer.SyncOnce(ctx, key)
	require.NoError(t, err)

	mccCRUD := mockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).ManagementClusterContents(key.HCPNodePoolName)
	got, err := mccCRUD.Get(ctx, string(api.MaestroBundleInternalNameReadonlyHypershiftNodePool))
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotNil(t, got.Status.KubeContent)
	require.Len(t, got.Status.KubeContent.Items, 1)

	var u unstructured.Unstructured
	err = json.Unmarshal(got.Status.KubeContent.Items[0].Raw, &u)
	require.NoError(t, err)
	assert.Equal(t, "NodePool", u.GetKind())
	assert.Equal(t, "np1", u.GetName())
}
