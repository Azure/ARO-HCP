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

package operations

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	operationtesting "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/billingcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationClusterDelete_SynchronizeOperation(t *testing.T) {
	fixedTime := operationtesting.MustParseTime("2025-01-20T10:30:00Z")
	createdAt := operationtesting.MustParseTime("2025-01-15T10:30:00Z")

	fixture := operationtesting.NewClusterTestFixture()

	clusterPassingReconcileGate := func() *coreapi.HCPOpenShiftCluster {
		now := time.Now()
		cluster := fixture.NewCluster(nil)
		cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: now}
		cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: now}
		return cluster
	}

	testCases := []struct {
		name                           string
		nodePools                      []*coreapi.HCPOpenShiftClusterNodePool
		externalAuths                  []*coreapi.HCPOpenShiftClusterExternalAuth
		usesNewClusterDeletionApproach bool
		existingCluster                *coreapi.HCPOpenShiftCluster
		setupCSMock                    func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec
		wantErr                        bool
		verifyDB                       func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient)
	}{
		{
			name:            "legacy approach: cluster not found marks billing as deleted and removes cluster",
			existingCluster: fixture.NewCluster(&createdAt),
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				notFoundErr, _ := ocmerrors.NewError().Status(http.StatusNotFound).Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(nil, notFoundErr)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateSucceeded, op.Status)

				_, err = db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				assert.Error(t, err, "cluster should have been deleted")
			},
		},
		{
			name:            "legacy approach: cluster not found does not remove cluster while nodepools exist",
			existingCluster: fixture.NewCluster(&createdAt),
			nodePools: []*coreapi.HCPOpenShiftClusterNodePool{
				operationtesting.NewNodePoolTestFixture().NewNodePool(),
			},
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				notFoundErr, _ := ocmerrors.NewError().Status(http.StatusNotFound).Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(nil, notFoundErr)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status)

				// Cluster should still exist
				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				require.NoError(t, err)
				assert.NotNil(t, cluster)
			},
		},
		{
			name:            "legacy approach: cluster not found does not remove cluster while external auths exist",
			existingCluster: fixture.NewCluster(&createdAt),
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				notFoundErr, _ := ocmerrors.NewError().Status(http.StatusNotFound).Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(nil, notFoundErr)
				return mockCSClient
			},
			wantErr: false,
			externalAuths: []*coreapi.HCPOpenShiftClusterExternalAuth{
				operationtesting.NewExternalAuthTestFixture().NewExternalAuth(),
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				// Operation should remain non-terminal since external auths still exist
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status)

				// Cluster should still exist
				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				require.NoError(t, err)
				assert.NotNil(t, cluster)
			},
		},
		{
			name:            "legacy approach: cluster uninstalling updates operation to deleting",
			existingCluster: fixture.NewCluster(&createdAt),
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, _ := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateUninstalling).
					Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateDeleting, op.Status)

				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				require.NoError(t, err)
				assert.NotNil(t, cluster)
			},
		},
		{
			name:            "legacy approach: cluster ready during delete stays at current status",
			existingCluster: fixture.NewCluster(&createdAt),
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, _ := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateReady).
					Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status)

				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				require.NoError(t, err)
				assert.NotNil(t, cluster)
			},
		},
		{
			name:            "legacy approach: cluster error during delete transitions to failed",
			existingCluster: fixture.NewCluster(&createdAt),
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, _ := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateError).
					ProvisionErrorCode("ERR001").
					ProvisionErrorMessage("delete failed").
					Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateFailed, op.Status)
				assert.NotNil(t, op.Error)
				assert.Equal(t, "ERR001", op.Error.Code)

				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				require.NoError(t, err)
				assert.NotNil(t, cluster)
			},
		},
		{
			name:                           "cluster document gone completes operation",
			usesNewClusterDeletionApproach: true,
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateSucceeded, op.Status)
			},
		},
		{
			name:                           "shouldReconcile gate not passed skips cluster service",
			usesNewClusterDeletionApproach: true,
			existingCluster: func() *coreapi.HCPOpenShiftCluster {
				cluster := fixture.NewCluster(nil)
				cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				return cluster
			}(),
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:                           "shouldReconcile gate not passed when ClusterServiceID is nil",
			usesNewClusterDeletionApproach: true,
			existingCluster: func() *coreapi.HCPOpenShiftCluster {
				cluster := fixture.NewCluster(nil)
				cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: time.Now()}
				cluster.ServiceProviderProperties.ClusterServiceID = nil
				return cluster
			}(),
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:                           "reconcile gate passed and CS uninstalling updates operation to deleting",
			usesNewClusterDeletionApproach: true,
			existingCluster:                clusterPassingReconcileGate(),
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, _ := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateUninstalling).
					Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateDeleting, op.Status)
			},
		},
		{
			name:                           "reconcile gate passed and CS error marks operation failed",
			usesNewClusterDeletionApproach: true,
			existingCluster:                clusterPassingReconcileGate(),
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, _ := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateError).
					ProvisionErrorCode("ERR001").
					ProvisionErrorMessage("delete failed").
					Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateFailed, op.Status)
				assert.NotNil(t, op.Error)
				assert.Equal(t, "ERR001", op.Error.Code)
			},
		},
		{
			name:                           "reconcile gate passed and CS Ready waits for Cosmos deletion",
			usesNewClusterDeletionApproach: true,
			existingCluster:                clusterPassingReconcileGate(),
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, _ := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateReady).
					Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status, "operation should stay at Accepted, waiting for Cosmos Cluster document deletion")
			},
		},
		{
			name:                           "reconcile gate passed and CS 404 skips operation update",
			usesNewClusterDeletionApproach: true,
			existingCluster:                clusterPassingReconcileGate(),
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				notFoundErr, _ := ocmerrors.NewError().Status(http.StatusNotFound).Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(nil, notFoundErr)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status, "operation should stay at Accepted, waiting for ID clearer")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			operation := fixture.NewOperation(cosmosstorageutils.OperationRequestDelete)
			// TODO remove this once the new deletion approach is fully rolled out in all ARO-HCP permanent environments, for all regions.
			operation.UsesNewClusterDeletionApproach = tc.usesNewClusterDeletionApproach

			mockBillingDBClient := billingcosmosstoragetesting.NewMockBillingDBClient()
			if tc.existingCluster != nil {
				billingDoc := billingcosmosstorage.NewBillingDocument(tc.existingCluster.ServiceProviderProperties.ClusterUID, fixture.ClusterResourceID)
				billingDoc.CreationTime = createdAt
				billingDoc.Location = operationtesting.TestAzureLocation
				billingDoc.TenantID = operationtesting.TestTenantID
				err := mockBillingDBClient.BillingDocs(fixture.ClusterResourceID.SubscriptionID).Create(ctx, billingDoc)
				require.NoError(t, err)
			}

			resources := []any{operation}
			if tc.existingCluster != nil {
				resources = append(resources, tc.existingCluster)
			}
			for _, nodePool := range tc.nodePools {
				resources = append(resources, nodePool)
			}
			for _, externalAuth := range tc.externalAuths {
				resources = append(resources, externalAuth)
			}
			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			var mockCSClient ocm.ClusterServiceClientSpec
			if tc.setupCSMock != nil {
				mockCSClient = tc.setupCSMock(ctrl, fixture)
			}

			controller := &operationClusterDelete{
				clock:                clocktesting.NewFakePassiveClock(fixedTime),
				resourcesDBClient:    mockResourcesDBClient,
				billingDBClient:      mockBillingDBClient,
				clusterServiceClient: mockCSClient,
				notificationClient:   nil,
			}

			err = controller.SynchronizeOperation(ctx, fixture.OperationKey())
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tc.verifyDB != nil {
				tc.verifyDB(t, ctx, mockResourcesDBClient)
			}
		})
	}
}

func TestOperationClusterDelete_SynchronizeOperation_ClusterResourcesApplyDesiresGate(t *testing.T) {
	fixedTime := operationtesting.MustParseTime("2025-01-20T10:30:00Z")
	fixture := operationtesting.NewClusterTestFixture()

	managementClusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"))

	clusterPassingReconcileGate := func() *coreapi.HCPOpenShiftCluster {
		now := time.Now()
		cluster := fixture.NewCluster(nil)
		cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: now}
		cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: now}
		return cluster
	}
	newSPC := func(mc *azcorearm.ResourceID) *coreapi.ServiceProviderCluster {
		spcResourceID := metadataapi.Must(azcorearm.ParseResourceID(
			fixture.ClusterResourceID.String() + "/serviceProviderClusters/" + coreapi.ServiceProviderClusterResourceName))
		return &coreapi.ServiceProviderCluster{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   spcResourceID,
				PartitionKey: strings.ToLower(spcResourceID.SubscriptionID),
			},
			Status: coreapi.ServiceProviderClusterStatus{
				ManagementClusterResourceID: mc,
			},
		}
	}
	taggedDesire := func(name string) *kubeapplierapi.ApplyDesire {
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(
			kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(
				operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName, operationtesting.TestClusterName, name)))
		return &kubeapplierapi.ApplyDesire{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: strings.ToLower(managementClusterResourceID.String()),
			},
			Spec: kubeapplierapi.ApplyDesireSpec{
				ManagementCluster: managementClusterResourceID,
			},
			Tags: map[string]string{kubeapplierapi.TagControllerName: kubeapplierapi.ClusterResourcesControllerName},
		}
	}

	testCases := []struct {
		name               string
		spc                *coreapi.ServiceProviderCluster
		kubeApplierDesires []any
		setupCSMock        func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec
		wantStatus         coreapi.ProvisioningState
	}{
		{
			name:               "tagged ClusterResourcesController ApplyDesire present -> operation held non-terminal",
			spc:                newSPC(managementClusterResourceID),
			kubeApplierDesires: []any{taggedDesire("cluster-resource-desire")},
			// No CS mock: the gate returns before reconcile, so ClusterService must not be called.
			wantStatus: coreapi.ProvisioningStateAccepted,
		},
		{
			name:               "no tagged ApplyDesires -> operation proceeds to reconcile",
			spc:                newSPC(managementClusterResourceID),
			kubeApplierDesires: nil,
			setupCSMock: func(ctrl *gomock.Controller, fixture *operationtesting.ClusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, _ := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateUninstalling).
					Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.ClusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			wantStatus: coreapi.ProvisioningStateDeleting,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			operation := fixture.NewOperation(cosmosstorageutils.OperationRequestDelete)
			operation.UsesNewClusterDeletionApproach = true

			resources := []any{operation, clusterPassingReconcileGate()}
			if tc.spc != nil {
				resources = append(resources, tc.spc)
			}
			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			mockKubeApplierDBClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
			mockKubeApplierClient, err := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClientWithResources(ctx, tc.kubeApplierDesires)
			require.NoError(t, err)
			mockKubeApplierDBClients.Register(managementClusterResourceID, mockKubeApplierClient)

			var mockCSClient ocm.ClusterServiceClientSpec
			if tc.setupCSMock != nil {
				mockCSClient = tc.setupCSMock(ctrl, fixture)
			}

			controller := &operationClusterDelete{
				clock:                clocktesting.NewFakePassiveClock(fixedTime),
				resourcesDBClient:    mockResourcesDBClient,
				billingDBClient:      billingcosmosstoragetesting.NewMockBillingDBClient(),
				kubeApplierDBClients: mockKubeApplierDBClients,
				clusterServiceClient: mockCSClient,
				notificationClient:   nil,
			}

			require.NoError(t, controller.SynchronizeOperation(ctx, fixture.OperationKey()))

			op, err := mockResourcesDBClient.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
			require.NoError(t, err)
			assert.Equal(t, tc.wantStatus, op.Status)
		})
	}
}
