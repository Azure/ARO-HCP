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

func TestOperationClusterUpdate_SynchronizeOperation(t *testing.T) {
	tests := []struct {
		name         string
		clusterState arohcpv1alpha1.ClusterState
		expectError  bool
		verify       func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture)
	}{
		{
			name:         "cluster ready transitions to succeeded",
			clusterState: arohcpv1alpha1.ClusterStateReady,
			expectError:  false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)

				// Verify cluster provisioning state was also updated
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, cluster.ServiceProviderProperties.ProvisioningState)
				assert.Empty(t, cluster.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:         "cluster updating transitions to updating",
			clusterState: arohcpv1alpha1.ClusterStateUpdating,
			expectError:  false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateUpdating, op.Status)

				// Verify cluster still has active operation
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateUpdating, cluster.ServiceProviderProperties.ProvisioningState)
				assert.Equal(t, testOperationName, cluster.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:         "cluster error transitions to failed",
			clusterState: arohcpv1alpha1.ClusterStateError,
			expectError:  false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				assert.NotNil(t, op.Error)

				// Verify cluster also shows failed
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, cluster.ServiceProviderProperties.ProvisioningState)
				assert.Empty(t, cluster.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:         "cluster pending stays accepted",
			clusterState: arohcpv1alpha1.ClusterStatePending,
			expectError:  false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
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
			cluster := fixture.newCluster(nil)
			operation := fixture.newOperation(database.OperationRequestUpdate)

			mockDB, err := databasetesting.NewMockDBClientWithResources(ctx, []any{cluster, operation})
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			clusterStatus, err := arohcpv1alpha1.NewClusterStatus().
				State(tt.clusterState).
				Build()
			require.NoError(t, err)

			mockCSClient.EXPECT().
				GetClusterStatus(gomock.Any(), fixture.clusterInternalID).
				Return(clusterStatus, nil)

			controller := &operationClusterUpdate{
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
