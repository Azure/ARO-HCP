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

package creation

import (
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	kubeapplierlistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testManagementClusterResourceIDStr = "/subscriptions/mc-sub/resourceGroups/mc-rg/providers/Microsoft.ContainerService/managedClusters/mc-cluster"
	testControlPlaneNamespace          = "ocm-dev-abc123"
)

var testManagementClusterResourceID = metadataapi.Must(azcorearm.ParseResourceID(testManagementClusterResourceIDStr))

func newTestCluster() *coreapi.HCPOpenShiftCluster {
	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))

	csID := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123"))
	return &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   clusterResourceID,
			PartitionKey: strings.ToLower(clusterResourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   clusterResourceID,
				Name: testClusterName,
			},
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: coreapi.ProvisioningStateSucceeded,
			ClusterServiceID:  &csID,
		},
	}
}

func newTestServiceProviderCluster() *coreapi.ServiceProviderCluster {
	spcResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/serviceProviderClusters/" + coreapi.ServiceProviderClusterResourceName,
	))
	return &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   spcResourceID,
			PartitionKey: strings.ToLower(spcResourceID.SubscriptionID),
		},
		Status: coreapi.ServiceProviderClusterStatus{
			ManagementClusterResourceID: testManagementClusterResourceID,
			ControlPlaneNamespace:       testControlPlaneNamespace,
		},
	}
}

func TestDesiresCreator_SyncOnce(t *testing.T) {
	testKey := controllerutils.SystemAdminCredentialRequestKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		CredentialName:    testCredentialName,
	}

	tests := []struct {
		name                      string
		setupDB                   func(db *corecosmosstoragetesting.MockResourcesDBClient)
		spcLister                 *corelistertesting.SliceServiceProviderClusterLister
		registerKubeApplierClient bool
		expectError               bool
		verify                    func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, kubeApplierClients *kubeappliercosmosstoragetesting.MockKubeApplierDBClients)
	}{
		{
			name:        "cluster not found is no-op",
			setupDB:     func(db *corecosmosstoragetesting.MockResourcesDBClient) {},
			spcLister:   &corelistertesting.SliceServiceProviderClusterLister{},
			expectError: false,
		},
		{
			name: "credential not found is no-op",
			setupDB: func(db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster := newTestCluster()
				_, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Create(context.Background(), cluster, nil)
				require.NoError(t, err)
			},
			spcLister:   &corelistertesting.SliceServiceProviderClusterLister{},
			expectError: false,
		},
		{
			name: "cluster with DeletionTimestamp is no-op",
			setupDB: func(db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster := newTestCluster()
				now := metav1.Now()
				cluster.ServiceProviderProperties.DeletionTimestamp = &now
				_, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Create(context.Background(), cluster, nil)
				require.NoError(t, err)
				createTestCredentialRequest(t, db, testCredentialName)
			},
			spcLister:   &corelistertesting.SliceServiceProviderClusterLister{},
			expectError: false,
		},
		{
			name: "cluster without ClusterServiceID is no-op",
			setupDB: func(db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster := newTestCluster()
				cluster.ServiceProviderProperties.ClusterServiceID = nil
				_, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Create(context.Background(), cluster, nil)
				require.NoError(t, err)
				createTestCredentialRequest(t, db, testCredentialName)
			},
			spcLister:   &corelistertesting.SliceServiceProviderClusterLister{},
			expectError: false,
		},
		{
			name: "already issued credential is no-op",
			setupDB: func(db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster := newTestCluster()
				_, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Create(context.Background(), cluster, nil)
				require.NoError(t, err)
				createTestCredentialRequest(t, db, testCredentialName,
					withCondition(coreapi.SystemAdminCredentialRequestConditionIssued))
			},
			spcLister:   &corelistertesting.SliceServiceProviderClusterLister{},
			expectError: false,
		},
		{
			name: "ServiceProviderCluster not found is no-op",
			setupDB: func(db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster := newTestCluster()
				_, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Create(context.Background(), cluster, nil)
				require.NoError(t, err)
				createTestCredentialRequest(t, db, testCredentialName)
			},
			spcLister:   &corelistertesting.SliceServiceProviderClusterLister{},
			expectError: false,
		},
		{
			name: "ServiceProviderCluster without ManagementClusterResourceID is no-op",
			setupDB: func(db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster := newTestCluster()
				_, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Create(context.Background(), cluster, nil)
				require.NoError(t, err)
				createTestCredentialRequest(t, db, testCredentialName)
			},
			spcLister: &corelistertesting.SliceServiceProviderClusterLister{
				ServiceProviderClusters: []*coreapi.ServiceProviderCluster{
					func() *coreapi.ServiceProviderCluster {
						spc := newTestServiceProviderCluster()
						spc.Status.ManagementClusterResourceID = nil
						return spc
					}(),
				},
			},
			expectError: false,
		},
		{
			name: "ServiceProviderCluster without ControlPlaneNamespace is no-op",
			setupDB: func(db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster := newTestCluster()
				_, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Create(context.Background(), cluster, nil)
				require.NoError(t, err)
				createTestCredentialRequest(t, db, testCredentialName)
			},
			spcLister: &corelistertesting.SliceServiceProviderClusterLister{
				ServiceProviderClusters: []*coreapi.ServiceProviderCluster{
					func() *coreapi.ServiceProviderCluster {
						spc := newTestServiceProviderCluster()
						spc.Status.ControlPlaneNamespace = ""
						return spc
					}(),
				},
			},
			expectError: false,
		},
		{
			name: "kube-applier client not available is no-op",
			setupDB: func(db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster := newTestCluster()
				_, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Create(context.Background(), cluster, nil)
				require.NoError(t, err)
				createTestCredentialRequest(t, db, testCredentialName)
			},
			spcLister: &corelistertesting.SliceServiceProviderClusterLister{
				ServiceProviderClusters: []*coreapi.ServiceProviderCluster{newTestServiceProviderCluster()},
			},
			expectError: false,
		},
		{
			name:                      "pending credential creates CSR, CSRApproval, and CSR ReadDesire",
			registerKubeApplierClient: true,
			setupDB: func(db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster := newTestCluster()
				_, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Create(context.Background(), cluster, nil)
				require.NoError(t, err)
				createTestCredentialRequest(t, db, testCredentialName)
			},
			spcLister: &corelistertesting.SliceServiceProviderClusterLister{
				ServiceProviderClusters: []*coreapi.ServiceProviderCluster{newTestServiceProviderCluster()},
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, kubeApplierClients *kubeappliercosmosstoragetesting.MockKubeApplierDBClients) {
				client := kubeApplierClients.For(ctx, testManagementClusterResourceID)
				require.NotNil(t, client, "kube-applier client should exist")

				applyCRUD, err := client.ApplyDesiresForSystemAdminCredentialRequest(testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName)
				require.NoError(t, err)
				applyIter, err := applyCRUD.List(ctx, nil)
				require.NoError(t, err)
				var applyDesires []*kubeapplierapi.ApplyDesire
				for _, d := range applyIter.Items(ctx) {
					applyDesires = append(applyDesires, d)
				}
				require.NoError(t, applyIter.GetError())
				assert.Len(t, applyDesires, 2, "should have CSR and CSRApproval ApplyDesires")

				var desireNames []string
				for _, d := range applyDesires {
					desireNames = append(desireNames, d.ResourceID.Name)
				}
				assert.Contains(t, desireNames, "systemadmincredentialcsr", "should have CSR ApplyDesire")
				assert.Contains(t, desireNames, "systemadmincredentialcsrapproval", "should have CSRApproval ApplyDesire")

				readCRUD, err := client.ReadDesiresForSystemAdminCredentialRequest(testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName)
				require.NoError(t, err)
				readIter, err := readCRUD.List(ctx, nil)
				require.NoError(t, err)
				var readDesires []*kubeapplierapi.ReadDesire
				for _, d := range readIter.Items(ctx) {
					readDesires = append(readDesires, d)
				}
				require.NoError(t, readIter.GetError())
				assert.Len(t, readDesires, 1, "should have CSR ReadDesire")
				if len(readDesires) > 0 {
					assert.Equal(t, "systemadmincredentialcsr", readDesires[0].ResourceID.Name, "ReadDesire should be for CSR")
				}

				for _, ad := range applyDesires {
					assert.Equal(t, kubeapplierapi.ApplyDesireTypeServerSideApply, ad.Spec.Type, "ApplyDesire type")
					assert.NotNil(t, ad.Spec.ManagementCluster, "ManagementCluster should be set")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			tt.setupDB(db)

			kubeApplierClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
			if tt.registerKubeApplierClient {
				mockKubeApplierClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
				kubeApplierClients.Register(testManagementClusterResourceID, mockKubeApplierClient)
			}

			syncer := &desiresCreator{
				resourcesDBClient:            db,
				kubeApplierDBClients:         kubeApplierClients,
				serviceProviderClusterLister: tt.spcLister,
				applyDesireLister:            &kubeapplierlistertesting.SliceApplyDesireLister{},
				readDesireLister:             &kubeapplierlistertesting.SliceReadDesireLister{},
			}

			err := syncer.SyncOnce(ctx, testKey)

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, ctx, db, kubeApplierClients)
			}
		})
	}
}
