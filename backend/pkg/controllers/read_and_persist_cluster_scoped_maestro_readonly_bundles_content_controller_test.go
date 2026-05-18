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
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func TestReadAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_ClusterNotFound(t *testing.T) {
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker:   &alwaysSyncCooldownChecker{},
		resourcesDBClient: mockResourcesDBClient,
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	err := syncer.SyncOnce(context.Background(), key)
	assert.NoError(t, err)
}

func TestReadAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_GetServiceProviderClusterError(t *testing.T) {
	ctx := context.Background()

	baseMockResourcesDBClient := databasetesting.NewMockResourcesDBClient()

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111"))),
		},
	}

	// Add the cluster to the database
	clustersCRUD := baseMockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	_, err := clustersCRUD.Create(ctx, cluster, nil)
	require.NoError(t, err)

	// Use error-injecting wrapper to simulate SPC Get error
	expectedError := fmt.Errorf("database error")
	mockResourcesDBClient := &errorInjectingResourcesDBClient{
		MockResourcesDBClient: baseMockResourcesDBClient,
		spcCRUD: &errorInjectingSPCCRUD{
			getErr: expectedError,
		},
	}

	syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker:   &alwaysSyncCooldownChecker{},
		resourcesDBClient: mockResourcesDBClient,
	}

	err = syncer.SyncOnce(ctx, key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get or create ServiceProviderCluster")
}

func TestReadAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_NoMaestroReadonlyBundlesRefs(t *testing.T) {
	ctx := context.Background()
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker:   &alwaysSyncCooldownChecker{},
		resourcesDBClient: mockResourcesDBClient,
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111"))),
		},
	}
	clustersCRUD := mockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	_, err := clustersCRUD.Create(ctx, cluster, nil)
	require.NoError(t, err)

	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/serviceProviderClusters/default"))
	spc := &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
	}
	spcCRUD := mockResourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = spcCRUD.Create(ctx, spc, nil)
	require.NoError(t, err)

	err = syncer.SyncOnce(ctx, key)
	assert.NoError(t, err)
}

func TestReadAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_GetProvisionShardError(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)

	syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker:      &alwaysSyncCooldownChecker{},
		resourcesDBClient:    mockResourcesDBClient,
		clusterServiceClient: mockClusterService,
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111"))),
		},
	}
	clustersCRUD := mockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	_, err := clustersCRUD.Create(ctx, cluster, nil)
	require.NoError(t, err)

	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/serviceProviderClusters/default"))
	spc := &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
		Status: api.ServiceProviderClusterStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{
				{Name: api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster, MaestroAPIMaestroBundleName: "bundle-name"},
			},
		},
	}
	spcCRUD := mockResourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = spcCRUD.Create(ctx, spc, nil)
	require.NoError(t, err)

	mockClusterService.EXPECT().
		GetClusterProvisionShard(gomock.Any(), *cluster.ServiceProviderProperties.ClusterServiceID).
		Return(nil, fmt.Errorf("provision shard error"))

	err = syncer.SyncOnce(ctx, key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get Cluster Provision Shard")
}

func TestReadAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer_SyncOnce_ReadAndPersistFlow(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
	mockMaestroClient := maestro.NewMockClient(ctrl)

	syncer := &readAndPersistClusterScopedMaestroReadonlyBundlesContentSyncer{
		cooldownChecker:                    &alwaysSyncCooldownChecker{},
		resourcesDBClient:                  mockResourcesDBClient,
		clusterServiceClient:               mockClusterService,
		maestroClientBuilder:               mockMaestroBuilder,
		maestroSourceEnvironmentIdentifier: "test-env",
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111"))),
		},
	}
	clustersCRUD := mockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	_, err := clustersCRUD.Create(ctx, cluster, nil)
	require.NoError(t, err)

	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/serviceProviderClusters/default"))
	spc := &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
		Status: api.ServiceProviderClusterStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{
				{Name: api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster, MaestroAPIMaestroBundleName: "bundle-name"},
			},
		},
	}
	spcCRUD := mockResourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = spcCRUD.Create(ctx, spc, nil)
	require.NoError(t, err)

	provisionShard := buildTestProvisionShard("test-consumer")
	mockClusterService.EXPECT().
		GetClusterProvisionShard(gomock.Any(), *cluster.ServiceProviderProperties.ClusterServiceID).
		Return(provisionShard, nil)

	restEndpoint := provisionShard.MaestroConfig().RestApiConfig().Url()
	grpcEndpoint := provisionShard.MaestroConfig().GrpcApiConfig().Url()
	consumerName := provisionShard.MaestroConfig().ConsumerName()
	sourceID := maestro.GenerateMaestroSourceID("test-env", provisionShard.ID())
	mockMaestroBuilder.EXPECT().
		NewClient(gomock.Any(), restEndpoint, grpcEndpoint, consumerName, sourceID).
		Return(mockMaestroClient, nil)

	validHCJSON := `{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"HostedCluster","metadata":{"name":"hc1","namespace":"ns1"}}`
	bundle := buildTestMaestroBundleWithStatusFeedback("bundle-name", "test-consumer", validHCJSON)
	mockMaestroClient.EXPECT().Get(gomock.Any(), "bundle-name", gomock.Any()).Return(bundle, nil)

	err = syncer.SyncOnce(ctx, key)
	require.NoError(t, err)

	mccCRUD := mockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).ManagementClusterContents(key.HCPClusterName)
	got, err := mccCRUD.Get(ctx, string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotNil(t, got.Status.KubeContent)
	require.Len(t, got.Status.KubeContent.Items, 1)
	// Decode and spot-check
	var u unstructured.Unstructured
	err = json.Unmarshal(got.Status.KubeContent.Items[0].Raw, &u)
	require.NoError(t, err)
	assert.Equal(t, "HostedCluster", u.GetKind())
	assert.Equal(t, "hc1", u.GetName())
}
