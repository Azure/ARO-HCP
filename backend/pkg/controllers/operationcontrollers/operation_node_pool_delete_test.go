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

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationNodePoolDelete_SynchronizeOperation(t *testing.T) {
	tests := []struct {
		name        string
		setupMock   func(ctrl *gomock.Controller, fixture *nodePoolTestFixture) ocm.ClusterServiceClientSpec
		expectError bool
		verify      func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *nodePoolTestFixture)
	}{
		{
			name: "node pool not found marks operation succeeded and removes node pool",
			setupMock: func(ctrl *gomock.Controller, fixture *nodePoolTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				notFoundErr, _ := ocmerrors.NewError().Status(http.StatusNotFound).Build()
				mockCSClient.EXPECT().
					GetNodePoolStatus(gomock.Any(), fixture.nodePoolInternalID).
					Return(nil, notFoundErr)
				return mockCSClient
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)

				// Verify node pool document was deleted
				_, err = db.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
				assert.Error(t, err, "node pool should have been deleted")
			},
		},
		{
			name: "node pool uninstalling updates operation to deleting",
			setupMock: func(ctrl *gomock.Controller, fixture *nodePoolTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				nodePoolStatus, _ := arohcpv1alpha1.NewNodePoolStatus().
					State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(string(NodePoolStateUninstalling))).
					Build()
				mockCSClient.EXPECT().
					GetNodePoolStatus(gomock.Any(), fixture.nodePoolInternalID).
					Return(nodePoolStatus, nil)
				return mockCSClient
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateDeleting, op.Status)

				// Node pool should still exist during uninstalling
				nodePool, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				assert.NotNil(t, nodePool)
			},
		},
		{
			name: "node pool ready during delete stays at current status",
			setupMock: func(ctrl *gomock.Controller, fixture *nodePoolTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				nodePoolStatus, _ := arohcpv1alpha1.NewNodePoolStatus().
					State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(string(NodePoolStateReady))).
					Build()
				mockCSClient.EXPECT().
					GetNodePoolStatus(gomock.Any(), fixture.nodePoolInternalID).
					Return(nodePoolStatus, nil)
				return mockCSClient
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *nodePoolTestFixture) {
				// When node pool is Ready during delete, operation stays at Accepted
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)

				// Node pool should still exist
				nodePool, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				assert.NotNil(t, nodePool)
			},
		},
		{
			name: "node pool error during delete transitions to failed",
			setupMock: func(ctrl *gomock.Controller, fixture *nodePoolTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				nodePoolStatus, _ := arohcpv1alpha1.NewNodePoolStatus().
					State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(string(NodePoolStateError))).
					Message("delete failed").
					Build()
				mockCSClient.EXPECT().
					GetNodePoolStatus(gomock.Any(), fixture.nodePoolInternalID).
					Return(nodePoolStatus, nil)
				return mockCSClient
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				assert.NotNil(t, op.Error)

				// Node pool should still exist on failure
				nodePool, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				assert.NotNil(t, nodePool)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			fixture := newNodePoolTestFixture()
			cluster := fixture.newCluster()
			nodePool := fixture.newNodePool()
			operation := fixture.newOperation(database.OperationRequestDelete)

			mockDB, err := databasetesting.NewMockDBClientWithResources(ctx, []any{cluster, nodePool, operation})
			require.NoError(t, err)

			mockCSClient := tt.setupMock(ctrl, fixture)

			controller := &operationNodePoolDelete{
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
