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
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationClusterDelete_SynchronizeOperation(t *testing.T) {
	fixedTime := mustParseTime("2025-01-20T10:30:00Z")
	createdAt := mustParseTime("2025-01-15T10:30:00Z")

	fixture := newClusterTestFixture()

	clusterPassingReconcileGate := func() *api.HCPOpenShiftCluster {
		now := time.Now()
		cluster := fixture.newCluster(nil)
		cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: now}
		cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: now}
		return cluster
	}

	testCases := []struct {
		name                           string
		nodePools                      []*api.HCPOpenShiftClusterNodePool
		externalAuths                  []*api.HCPOpenShiftClusterExternalAuth
		usesNewClusterDeletionApproach bool
		existingCluster                *api.HCPOpenShiftCluster
		setupCSMock                    func(ctrl *gomock.Controller, fixture *clusterTestFixture) ocm.ClusterServiceClientSpec
		wantErr                        bool
		verifyDB                       func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name:            "legacy approach: cluster not found marks billing as deleted and removes cluster",
			existingCluster: fixture.newCluster(&createdAt),
			setupCSMock: func(ctrl *gomock.Controller, fixture *clusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				notFoundErr, _ := ocmerrors.NewError().Status(http.StatusNotFound).Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.clusterInternalID).
					Return(nil, notFoundErr)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)

				_, err = db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				assert.Error(t, err, "cluster should have been deleted")
			},
		},
		{
			name:            "legacy approach: cluster not found does not remove cluster while nodepools exist",
			existingCluster: fixture.newCluster(&createdAt),
			nodePools: []*api.HCPOpenShiftClusterNodePool{
				newNodePoolTestFixture().newNodePool(),
			},
			setupCSMock: func(ctrl *gomock.Controller, fixture *clusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				notFoundErr, _ := ocmerrors.NewError().Status(http.StatusNotFound).Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.clusterInternalID).
					Return(nil, notFoundErr)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)

				// Cluster should still exist
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.NotNil(t, cluster)
			},
		},
		{
			name:            "legacy approach: cluster not found does not remove cluster while external auths exist",
			existingCluster: fixture.newCluster(&createdAt),
			setupCSMock: func(ctrl *gomock.Controller, fixture *clusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				notFoundErr, _ := ocmerrors.NewError().Status(http.StatusNotFound).Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.clusterInternalID).
					Return(nil, notFoundErr)
				return mockCSClient
			},
			wantErr: false,
			externalAuths: []*api.HCPOpenShiftClusterExternalAuth{
				newExternalAuthTestFixture().newExternalAuth(),
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				// Operation should remain non-terminal since external auths still exist
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)

				// Cluster should still exist
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.NotNil(t, cluster)
			},
		},
		{
			name:            "legacy approach: cluster uninstalling updates operation to deleting",
			existingCluster: fixture.newCluster(&createdAt),
			setupCSMock: func(ctrl *gomock.Controller, fixture *clusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, _ := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateUninstalling).
					Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.clusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateDeleting, op.Status)

				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.NotNil(t, cluster)
			},
		},
		{
			name:            "legacy approach: cluster ready during delete stays at current status",
			existingCluster: fixture.newCluster(&createdAt),
			setupCSMock: func(ctrl *gomock.Controller, fixture *clusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, _ := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateReady).
					Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.clusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)

				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.NotNil(t, cluster)
			},
		},
		{
			name:            "legacy approach: cluster error during delete transitions to failed",
			existingCluster: fixture.newCluster(&createdAt),
			setupCSMock: func(ctrl *gomock.Controller, fixture *clusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, _ := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateError).
					ProvisionErrorCode("ERR001").
					ProvisionErrorMessage("delete failed").
					Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.clusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				assert.NotNil(t, op.Error)
				assert.Equal(t, "ERR001", op.Error.Code)

				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.NotNil(t, cluster)
			},
		},
		{
			name:                           "cluster document gone completes operation",
			usesNewClusterDeletionApproach: true,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)
			},
		},
		{
			name:                           "shouldReconcile gate not passed skips cluster service",
			usesNewClusterDeletionApproach: true,
			existingCluster: func() *api.HCPOpenShiftCluster {
				cluster := fixture.newCluster(nil)
				cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				return cluster
			}(),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:                           "shouldReconcile gate not passed when ClusterServiceID is nil",
			usesNewClusterDeletionApproach: true,
			existingCluster: func() *api.HCPOpenShiftCluster {
				cluster := fixture.newCluster(nil)
				cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: time.Now()}
				cluster.ServiceProviderProperties.ClusterServiceID = nil
				return cluster
			}(),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:                           "reconcile gate passed and CS uninstalling updates operation to deleting",
			usesNewClusterDeletionApproach: true,
			existingCluster:                clusterPassingReconcileGate(),
			setupCSMock: func(ctrl *gomock.Controller, fixture *clusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, _ := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateUninstalling).
					Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.clusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateDeleting, op.Status)
			},
		},
		{
			name:                           "reconcile gate passed and CS error marks operation failed",
			usesNewClusterDeletionApproach: true,
			existingCluster:                clusterPassingReconcileGate(),
			setupCSMock: func(ctrl *gomock.Controller, fixture *clusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, _ := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateError).
					ProvisionErrorCode("ERR001").
					ProvisionErrorMessage("delete failed").
					Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.clusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				assert.NotNil(t, op.Error)
				assert.Equal(t, "ERR001", op.Error.Code)
			},
		},
		{
			name:                           "reconcile gate passed and CS Ready waits for Cosmos deletion",
			usesNewClusterDeletionApproach: true,
			existingCluster:                clusterPassingReconcileGate(),
			setupCSMock: func(ctrl *gomock.Controller, fixture *clusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, _ := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateReady).
					Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.clusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status, "operation should stay at Accepted, waiting for Cosmos Cluster document deletion")
			},
		},
		{
			name:                           "reconcile gate passed and CS 404 skips operation update",
			usesNewClusterDeletionApproach: true,
			existingCluster:                clusterPassingReconcileGate(),
			setupCSMock: func(ctrl *gomock.Controller, fixture *clusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				notFoundErr, _ := ocmerrors.NewError().Status(http.StatusNotFound).Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.clusterInternalID).
					Return(nil, notFoundErr)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status, "operation should stay at Accepted, waiting for ID clearer")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			operation := fixture.newOperation(database.OperationRequestDelete)
			// TODO remove this once the new deletion approach is fully rolled out in all ARO-HCP permanent environments, for all regions.
			operation.UsesNewClusterDeletionApproach = tc.usesNewClusterDeletionApproach

			mockBillingDBClient := databasetesting.NewMockBillingDBClient()
			if tc.existingCluster != nil {
				billingDoc := database.NewBillingDocument(tc.existingCluster.ServiceProviderProperties.ClusterUID, fixture.clusterResourceID)
				billingDoc.CreationTime = createdAt
				billingDoc.Location = testAzureLocation
				billingDoc.TenantID = testTenantID
				err := mockBillingDBClient.BillingDocs(fixture.clusterResourceID.SubscriptionID).Create(ctx, billingDoc)
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
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
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

			err = controller.SynchronizeOperation(ctx, fixture.operationKey())
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
