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

package nodepooldeletion

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	workv1 "open-cluster-management.io/api/work/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testScopedMaestroRESTURL  = "https://maestro.example.com:8000"
	testScopedMaestroGRPC     = "maestro.example.com:8090"
	testScopedMaestroConsumer = "test-consumer"
	testScopedMaestroEnv      = "test-env"
	// Stamp name must be 1-3 lowercase alphanumeric characters (fleet validation).
	testScopedStampID = "ts1"
)

func testScopedProvisionShardInternalID(t *testing.T) *api.InternalID {
	t.Helper()
	return api.Ptr(api.Must(api.NewInternalID(
		"/api/aro_hcp/v1alpha1/provision_shards/22222222-2222-2222-2222-222222222222")))
}

func testScopedMaestroSourceID(t *testing.T) string {
	t.Helper()
	return maestro.GenerateMaestroSourceID(testScopedMaestroEnv, testScopedProvisionShardInternalID(t).ID())
}

func newTestManagementClusterForScopedMaestro(t *testing.T) *fleet.ManagementCluster {
	t.Helper()
	mcRID := api.Must(fleet.ToManagementClusterResourceID(testScopedStampID))
	shardID := api.Must(api.NewInternalID(
		"/api/aro_hcp/v1alpha1/provision_shards/22222222-2222-2222-2222-222222222222"))
	return &fleet.ManagementCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: mcRID},
		ResourceID:     mcRID,
		Spec: fleet.ManagementClusterSpec{
			SchedulingPolicy: fleet.ManagementClusterSchedulingPolicySchedulable,
		},
		Status: fleet.ManagementClusterStatus{
			AKSResourceID: api.Must(azcorearm.ParseResourceID(
				"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/test-mgmt")),
			PublicDNSZoneResourceID: api.Must(azcorearm.ParseResourceID(
				"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/dnszones/example.com")),
			HostedClustersSecretsKeyVaultURL:                     "https://kv-cx-secrets.vault.azure.net",
			HostedClustersManagedIdentitiesKeyVaultURL:           "https://kv-cx-mi.vault.azure.net",
			HostedClustersSecretsKeyVaultManagedIdentityClientID: "12345678-1234-1234-1234-123456789012",
			ClusterServiceProvisionShardID:                       ptr.To(shardID),
			MaestroConsumerName:                                  testScopedMaestroConsumer,
			MaestroRESTAPIURL:                                    testScopedMaestroRESTURL,
			MaestroGRPCTarget:                                    testScopedMaestroGRPC,
		},
	}
}

func newTestServiceProviderClusterForScopedMaestro(t *testing.T, mcResourceID *azcorearm.ResourceID) *api.ServiceProviderCluster {
	t.Helper()
	spcRID := api.Must(azcorearm.ParseResourceID(api.ToServiceProviderClusterResourceIDString(
		testSubscriptionID, testResourceGroupName, testClusterName)))
	return &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spcRID},
		Status: api.ServiceProviderClusterStatus{
			ManagementClusterResourceID: mcResourceID,
		},
	}
}

func newTestSPNP(t *testing.T, bundles api.MaestroBundleReferenceList) *api.ServiceProviderNodePool {
	t.Helper()
	spnpResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/" + testNodePoolName +
			"/serviceProviderNodePools/default"))
	return &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
		Status: api.ServiceProviderNodePoolStatus{
			MaestroReadonlyBundles: bundles,
		},
	}
}

func TestNodePoolScopedMaestroReadonlyBundlesDeleteController_NeedsWork(t *testing.T) {
	fixedNow := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		nodePool *api.HCPOpenShiftClusterNodePool
		want     bool
	}{
		{
			name:     "all nil — false",
			nodePool: newTestNodePool(t, nil),
			want:     false,
		},
		{
			name: "DeletionTimestamp only — false",
			nodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow}
			}),
			want: false,
		},
		{
			name: "DeletionTimestamp + CSDeletionTimestamp but CSID set — false",
			nodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow}
				np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow}
			}),
			want: false,
		},
		{
			name: "all conditions met — true",
			nodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow}
				np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow}
				np.ServiceProviderProperties.ClusterServiceID = nil
			}),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nodePoolMarkedForDeletion(tt.nodePool)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNodePoolScopedMaestroReadonlyBundlesDeleteController_SyncOnce(t *testing.T) {
	fixedNow := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	readyToDelete := func(np *api.HCPOpenShiftClusterNodePool) {
		np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
		np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-30 * time.Minute)}
		np.ServiceProviderProperties.ClusterServiceID = nil
	}

	mcResourceID := api.Must(fleet.ToManagementClusterResourceID(testScopedStampID))

	tests := []struct {
		name                   string
		existingNodePool       *api.HCPOpenShiftClusterNodePool
		existingSPNP           *api.ServiceProviderNodePool
		existingSPC            *api.ServiceProviderCluster
		fleetResources         []any
		setupMocks             func(*maestro.MockMaestroClientBuilder, *maestro.MockClient)
		wantErr                bool
		wantErrSubstr          string
		wantRemainingBundles   int
		wantRemainingBundleRef *api.MaestroBundleInternalName
	}{
		{
			name: "nodepool not found — no-op",
		},
		{
			name:             "nodepool not marked for deletion — no-op",
			existingNodePool: newTestNodePool(t, nil),
		},
		{
			name:             "no SPNP — no-op",
			existingNodePool: newTestNodePool(t, readyToDelete),
		},
		{
			name:             "SPNP with empty bundle list — no-op",
			existingNodePool: newTestNodePool(t, readyToDelete),
			existingSPNP:     newTestSPNP(t, api.MaestroBundleReferenceList{}),
		},
		{
			name:             "ServiceProviderCluster not found — error",
			existingNodePool: newTestNodePool(t, readyToDelete),
			existingSPNP: newTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
			}),
			wantErr:              true,
			wantErrSubstr:        "ServiceProviderCluster not found",
			wantRemainingBundles: 1,
		},
		{
			name:             "SPC without management cluster — clears bundle refs",
			existingNodePool: newTestNodePool(t, readyToDelete),
			existingSPC:      newTestServiceProviderClusterForScopedMaestro(t, nil),
			existingSPNP: newTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
			}),
			wantRemainingBundles: 0,
		},
		{
			name:             "single bundle — successful delete and confirmed gone",
			existingNodePool: newTestNodePool(t, readyToDelete),
			existingSPC:      newTestServiceProviderClusterForScopedMaestro(t, mcResourceID),
			existingSPNP: newTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
			}),
			fleetResources: []any{newTestManagementClusterForScopedMaestro(t)},
			setupMocks: func(mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				mb.EXPECT().NewClient(gomock.Any(), testScopedMaestroRESTURL, testScopedMaestroGRPC,
					testScopedMaestroConsumer, testScopedMaestroSourceID(t)).Return(mc, nil)
				mc.EXPECT().Delete(gomock.Any(), "name-a", metav1.DeleteOptions{}).Return(nil)
				mc.EXPECT().Get(gomock.Any(), "name-a", metav1.GetOptions{}).Return(nil,
					k8serrors.NewNotFound(schema.GroupResource{}, "name-a"))
			},
			wantRemainingBundles: 0,
		},
		{
			name:             "single bundle — delete ok but still exists in maestro, reference kept",
			existingNodePool: newTestNodePool(t, readyToDelete),
			existingSPC:      newTestServiceProviderClusterForScopedMaestro(t, mcResourceID),
			existingSPNP: newTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
			}),
			fleetResources: []any{newTestManagementClusterForScopedMaestro(t)},
			setupMocks: func(mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				mb.EXPECT().NewClient(gomock.Any(), testScopedMaestroRESTURL, testScopedMaestroGRPC,
					testScopedMaestroConsumer, testScopedMaestroSourceID(t)).Return(mc, nil)
				mc.EXPECT().Delete(gomock.Any(), "name-a", metav1.DeleteOptions{}).Return(nil)
				mc.EXPECT().Get(gomock.Any(), "name-a", metav1.GetOptions{}).Return(&workv1.ManifestWork{}, nil)
			},
			wantRemainingBundles: 1,
		},
		{
			name:             "single bundle — delete ok but Get returns error, reference kept",
			existingNodePool: newTestNodePool(t, readyToDelete),
			existingSPC:      newTestServiceProviderClusterForScopedMaestro(t, mcResourceID),
			existingSPNP: newTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
			}),
			fleetResources: []any{newTestManagementClusterForScopedMaestro(t)},
			setupMocks: func(mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				mb.EXPECT().NewClient(gomock.Any(), testScopedMaestroRESTURL, testScopedMaestroGRPC,
					testScopedMaestroConsumer, testScopedMaestroSourceID(t)).Return(mc, nil)
				mc.EXPECT().Delete(gomock.Any(), "name-a", metav1.DeleteOptions{}).Return(nil)
				mc.EXPECT().Get(gomock.Any(), "name-a", metav1.GetOptions{}).Return(nil, fmt.Errorf("maestro connection error"))
			},
			wantErr:              true,
			wantErrSubstr:        "failed to verify deletion of Maestro Bundle",
			wantRemainingBundles: 1,
		},
		{
			name:             "single bundle — maestro delete 404 then Get 404 treated as success",
			existingNodePool: newTestNodePool(t, readyToDelete),
			existingSPC:      newTestServiceProviderClusterForScopedMaestro(t, mcResourceID),
			existingSPNP: newTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
			}),
			fleetResources: []any{newTestManagementClusterForScopedMaestro(t)},
			setupMocks: func(mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				mb.EXPECT().NewClient(gomock.Any(), testScopedMaestroRESTURL, testScopedMaestroGRPC,
					testScopedMaestroConsumer, testScopedMaestroSourceID(t)).Return(mc, nil)
				mc.EXPECT().Delete(gomock.Any(), "name-a", metav1.DeleteOptions{}).Return(
					k8serrors.NewNotFound(schema.GroupResource{}, "name-a"))
				mc.EXPECT().Get(gomock.Any(), "name-a", metav1.GetOptions{}).Return(nil,
					k8serrors.NewNotFound(schema.GroupResource{}, "name-a"))
			},
			wantRemainingBundles: 0,
		},
		{
			name:             "single bundle — maestro error",
			existingNodePool: newTestNodePool(t, readyToDelete),
			existingSPC:      newTestServiceProviderClusterForScopedMaestro(t, mcResourceID),
			existingSPNP: newTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
			}),
			fleetResources: []any{newTestManagementClusterForScopedMaestro(t)},
			setupMocks: func(mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				mb.EXPECT().NewClient(gomock.Any(), testScopedMaestroRESTURL, testScopedMaestroGRPC,
					testScopedMaestroConsumer, testScopedMaestroSourceID(t)).Return(mc, nil)
				mc.EXPECT().Delete(gomock.Any(), "name-a", metav1.DeleteOptions{}).Return(fmt.Errorf("maestro connection error"))
			},
			wantErr:              true,
			wantErrSubstr:        "failed to delete Maestro Bundle",
			wantRemainingBundles: 1,
		},
		{
			name:             "multiple bundles — all succeed",
			existingNodePool: newTestNodePool(t, readyToDelete),
			existingSPC:      newTestServiceProviderClusterForScopedMaestro(t, mcResourceID),
			existingSPNP: newTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
				{Name: "bundleB", MaestroAPIMaestroBundleName: "name-b", MaestroAPIMaestroBundleID: "id-b"},
			}),
			fleetResources: []any{newTestManagementClusterForScopedMaestro(t)},
			setupMocks: func(mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				mb.EXPECT().NewClient(gomock.Any(), testScopedMaestroRESTURL, testScopedMaestroGRPC,
					testScopedMaestroConsumer, testScopedMaestroSourceID(t)).Return(mc, nil)
				mc.EXPECT().Delete(gomock.Any(), "name-a", metav1.DeleteOptions{}).Return(nil)
				mc.EXPECT().Get(gomock.Any(), "name-a", metav1.GetOptions{}).Return(nil,
					k8serrors.NewNotFound(schema.GroupResource{}, "name-a"))
				mc.EXPECT().Delete(gomock.Any(), "name-b", metav1.DeleteOptions{}).Return(nil)
				mc.EXPECT().Get(gomock.Any(), "name-b", metav1.GetOptions{}).Return(nil,
					k8serrors.NewNotFound(schema.GroupResource{}, "name-b"))
			},
			wantRemainingBundles: 0,
		},
		{
			name:             "multiple bundles — second delete fails",
			existingNodePool: newTestNodePool(t, readyToDelete),
			existingSPC:      newTestServiceProviderClusterForScopedMaestro(t, mcResourceID),
			existingSPNP: newTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
				{Name: "bundleB", MaestroAPIMaestroBundleName: "name-b", MaestroAPIMaestroBundleID: "id-b"},
			}),
			fleetResources: []any{newTestManagementClusterForScopedMaestro(t)},
			setupMocks: func(mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				mb.EXPECT().NewClient(gomock.Any(), testScopedMaestroRESTURL, testScopedMaestroGRPC,
					testScopedMaestroConsumer, testScopedMaestroSourceID(t)).Return(mc, nil)
				mc.EXPECT().Delete(gomock.Any(), "name-a", metav1.DeleteOptions{}).Return(nil)
				mc.EXPECT().Get(gomock.Any(), "name-a", metav1.GetOptions{}).Return(nil,
					k8serrors.NewNotFound(schema.GroupResource{}, "name-a"))
				mc.EXPECT().Delete(gomock.Any(), "name-b", metav1.DeleteOptions{}).Return(fmt.Errorf("maestro error"))
			},
			wantErr:                true,
			wantErrSubstr:          "failed to delete Maestro Bundle",
			wantRemainingBundles:   1,
			wantRemainingBundleRef: ptrTo(api.MaestroBundleInternalName("bundleB")),
		},
		{
			name:             "multiple bundles — first still exists after delete",
			existingNodePool: newTestNodePool(t, readyToDelete),
			existingSPC:      newTestServiceProviderClusterForScopedMaestro(t, mcResourceID),
			existingSPNP: newTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
				{Name: "bundleB", MaestroAPIMaestroBundleName: "name-b", MaestroAPIMaestroBundleID: "id-b"},
			}),
			fleetResources: []any{newTestManagementClusterForScopedMaestro(t)},
			setupMocks: func(mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				mb.EXPECT().NewClient(gomock.Any(), testScopedMaestroRESTURL, testScopedMaestroGRPC,
					testScopedMaestroConsumer, testScopedMaestroSourceID(t)).Return(mc, nil)
				mc.EXPECT().Delete(gomock.Any(), "name-a", metav1.DeleteOptions{}).Return(nil)
				mc.EXPECT().Get(gomock.Any(), "name-a", metav1.GetOptions{}).Return(&workv1.ManifestWork{}, nil)
				mc.EXPECT().Delete(gomock.Any(), "name-b", metav1.DeleteOptions{}).Return(nil)
				mc.EXPECT().Get(gomock.Any(), "name-b", metav1.GetOptions{}).Return(nil,
					k8serrors.NewNotFound(schema.GroupResource{}, "name-b"))
			},
			wantRemainingBundles:   1,
			wantRemainingBundleRef: ptrTo(api.MaestroBundleInternalName("bundleA")),
		},
		{
			name:             "bundle with empty maestro name — removed without maestro call",
			existingNodePool: newTestNodePool(t, readyToDelete),
			existingSPC:      newTestServiceProviderClusterForScopedMaestro(t, mcResourceID),
			existingSPNP: newTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "", MaestroAPIMaestroBundleID: ""},
			}),
			fleetResources: []any{newTestManagementClusterForScopedMaestro(t)},
			setupMocks: func(mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				mb.EXPECT().NewClient(gomock.Any(), testScopedMaestroRESTURL, testScopedMaestroGRPC,
					testScopedMaestroConsumer, testScopedMaestroSourceID(t)).Return(mc, nil)
			},
			wantRemainingBundles: 0,
		},
		{
			name:             "management cluster not in fleet DB",
			existingNodePool: newTestNodePool(t, readyToDelete),
			existingSPC:      newTestServiceProviderClusterForScopedMaestro(t, mcResourceID),
			existingSPNP: newTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
			}),
			fleetResources:       nil,
			wantErr:              true,
			wantErrSubstr:        "failed to get management cluster",
			wantRemainingBundles: 1,
		},
		{
			name:             "maestro client creation fails",
			existingNodePool: newTestNodePool(t, readyToDelete),
			existingSPC:      newTestServiceProviderClusterForScopedMaestro(t, mcResourceID),
			existingSPNP: newTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
			}),
			fleetResources: []any{newTestManagementClusterForScopedMaestro(t)},
			setupMocks: func(mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				mb.EXPECT().NewClient(gomock.Any(), testScopedMaestroRESTURL, testScopedMaestroGRPC,
					testScopedMaestroConsumer, testScopedMaestroSourceID(t)).Return(nil, fmt.Errorf("client error"))
			},
			wantErr:              true,
			wantErrSubstr:        "failed to create Maestro client",
			wantRemainingBundles: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)
			mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
			mockMaestroClient := maestro.NewMockClient(ctrl)

			if tt.setupMocks != nil {
				tt.setupMocks(mockMaestroBuilder, mockMaestroClient)
			}

			resources := []any{}
			if tt.existingNodePool != nil {
				resources = append(resources, tt.existingNodePool)
			}
			if tt.existingSPC != nil {
				resources = append(resources, tt.existingSPC)
			}
			if tt.existingSPNP != nil {
				resources = append(resources, tt.existingSPNP)
			}
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			fleetDBClient, err := databasetesting.NewMockFleetDBClientWithResources(ctx, tt.fleetResources)
			require.NoError(t, err)

			nodePoolsForLister := []*api.HCPOpenShiftClusterNodePool{}
			if tt.existingNodePool != nil {
				nodePoolsForLister = append(nodePoolsForLister, tt.existingNodePool)
			}
			spnpForLister := []*api.ServiceProviderNodePool{}
			if tt.existingSPNP != nil {
				spnpForLister = append(spnpForLister, tt.existingSPNP)
			}

			syncer := &nodePoolScopedMaestroReadonlyBundlesDeleteController{
				cooldownChecker:                    &alwaysSyncCooldownChecker{},
				nodePoolLister:                     &listertesting.SliceNodePoolLister{NodePools: nodePoolsForLister},
				serviceProviderNodePoolLister:      &listertesting.SliceServiceProviderNodePoolLister{ServiceProviderNodePools: spnpForLister},
				resourcesDBClient:                  mockResourcesDBClient,
				fleetDBClient:                      fleetDBClient,
				clusterServiceClient:               nil,
				maestroSourceEnvironmentIdentifier: testScopedMaestroEnv,
				maestroClientBuilder:               mockMaestroBuilder,
			}

			key := controllerutils.HCPNodePoolKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
				HCPNodePoolName:   testNodePoolName,
			}

			err = syncer.SyncOnce(ctx, key)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrSubstr)
			} else {
				require.NoError(t, err)
			}

			if tt.existingSPNP != nil {
				spnpCRUD := mockResourcesDBClient.ServiceProviderNodePools(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName)
				updatedSPNP, err := spnpCRUD.Get(ctx, api.ServiceProviderNodePoolResourceName)
				require.NoError(t, err)
				assert.Len(t, updatedSPNP.Status.MaestroReadonlyBundles, tt.wantRemainingBundles)

				if tt.wantRemainingBundleRef != nil {
					ref, err := updatedSPNP.Status.MaestroReadonlyBundles.Get(*tt.wantRemainingBundleRef)
					require.NoError(t, err)
					assert.NotNil(t, ref)
				}
			}
		})
	}
}

func ptrTo[T any](v T) *T {
	return &v
}
