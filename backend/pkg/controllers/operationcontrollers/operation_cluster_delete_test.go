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

package operationcontrollers

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	clocktesting "k8s.io/utils/clock/testing"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationClusterDelete_SynchronizeOperation(t *testing.T) {
	fixedTime := mustParseTime("2025-01-20T10:30:00Z")
	createdAt := mustParseTime("2025-01-15T10:30:00Z")

	tests := []struct {
		name        string
		setupMock   func(ctrl *gomock.Controller, fixture *clusterTestFixture) ocm.ClusterServiceClientSpec
		expectError bool
		verify      func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture)
	}{
		{
			name: "cluster not found marks billing as deleted and removes cluster",
			setupMock: func(ctrl *gomock.Controller, fixture *clusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				notFoundErr, _ := ocmerrors.NewError().Status(http.StatusNotFound).Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.clusterInternalID).
					Return(nil, notFoundErr)
				return mockCSClient
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture) {
				// Verify operation succeeded
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)

				// Verify cluster document was deleted
				_, err = db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				assert.Error(t, err, "cluster should have been deleted")
			},
		},
		{
			name: "cluster uninstalling updates operation to deleting",
			setupMock: func(ctrl *gomock.Controller, fixture *clusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, _ := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateUninstalling).
					Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.clusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateDeleting, op.Status)

				// Cluster should still exist during uninstalling
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.NotNil(t, cluster)
			},
		},
		{
			name: "cluster ready during delete stays at current status",
			setupMock: func(ctrl *gomock.Controller, fixture *clusterTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				clusterStatus, _ := arohcpv1alpha1.NewClusterStatus().
					State(arohcpv1alpha1.ClusterStateReady).
					Build()
				mockCSClient.EXPECT().
					GetClusterStatus(gomock.Any(), fixture.clusterInternalID).
					Return(clusterStatus, nil)
				return mockCSClient
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture) {
				// When cluster is Ready during delete, operation stays at Accepted
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
			name: "cluster error during delete transitions to failed",
			setupMock: func(ctrl *gomock.Controller, fixture *clusterTestFixture) ocm.ClusterServiceClientSpec {
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
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				assert.NotNil(t, op.Error)
				assert.Equal(t, "ERR001", op.Error.Code)

				// Cluster should still exist on failure
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.NotNil(t, cluster)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			fixture := newClusterTestFixture()
			cluster := fixture.newCluster(&createdAt)
			operation := fixture.newOperation(database.OperationRequestDelete)

			// Create billing document for deletion test
			billingDoc := database.NewBillingDocument(cluster.ServiceProviderProperties.ClusterUID, fixture.clusterResourceID)
			billingDoc.CreationTime = createdAt
			billingDoc.Location = testAzureLocation
			billingDoc.TenantID = testTenantID

			mockDB, err := databasetesting.NewMockDBClientWithResources(ctx, []any{cluster, operation})
			require.NoError(t, err)
			err = mockDB.CreateBillingDoc(ctx, billingDoc)
			require.NoError(t, err)

			mockCSClient := tt.setupMock(ctrl, fixture)

			controller := &operationClusterDelete{
				clock:                clocktesting.NewFakePassiveClock(fixedTime),
				cosmosClient:         mockDB,
				clusterServiceClient: mockCSClient,
				notificationClient:   nil,
			}

			err = controller.SynchronizeOperation(ctx, fixture.operationKey())

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, ctx, mockDB, fixture)
			}
		})
	}
}
