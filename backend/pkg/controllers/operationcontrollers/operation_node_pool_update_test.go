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
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationNodePoolUpdate_SynchronizeOperation(t *testing.T) {
	tests := []struct {
		name          string
		nodePoolState string
		nodePoolMsg   string
		expectError   bool
		verify        func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *nodePoolTestFixture)
	}{
		{
			name:          "node pool ready transitions to succeeded",
			nodePoolState: string(NodePoolStateReady),
			expectError:   false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)

				// Verify node pool provisioning state was also updated
				nodePool, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, nodePool.Properties.ProvisioningState)
				assert.Empty(t, nodePool.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:          "node pool updating transitions to updating",
			nodePoolState: string(NodePoolStateUpdating),
			expectError:   false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateUpdating, op.Status)

				// Verify node pool still has active operation
				nodePool, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateUpdating, nodePool.Properties.ProvisioningState)
				assert.Equal(t, testOperationName, nodePool.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:          "node pool validating_update stays accepted",
			nodePoolState: string(NodePoolStateValidatingUpdate),
			expectError:   false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:          "node pool pending_update stays accepted",
			nodePoolState: string(NodePoolStatePendingUpdate),
			expectError:   false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:          "node pool recoverable_error transitions to failed",
			nodePoolState: string(NodePoolStateRecoverableError),
			nodePoolMsg:   "temporary error occurred",
			expectError:   false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				assert.NotNil(t, op.Error)
				assert.Equal(t, "temporary error occurred", op.Error.Message)
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
			operation := fixture.newOperation(database.OperationRequestUpdate)

			mockDB, err := databasetesting.NewMockDBClientWithResources(ctx, []any{cluster, nodePool, operation})
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			nodePoolStatusBuilder := arohcpv1alpha1.NewNodePoolStatus().
				State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(tt.nodePoolState))
			if tt.nodePoolMsg != "" {
				nodePoolStatusBuilder = nodePoolStatusBuilder.Message(tt.nodePoolMsg)
			}
			nodePoolStatus, err := nodePoolStatusBuilder.Build()
			require.NoError(t, err)

			mockCSClient.EXPECT().
				GetNodePoolStatus(gomock.Any(), fixture.nodePoolInternalID).
				Return(nodePoolStatus, nil)

			controller := &operationNodePoolUpdate{
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
