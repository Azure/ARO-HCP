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
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationNodePoolCreate_SynchronizeOperation(t *testing.T) {
	defaultNodePool := func(fixture *nodePoolTestFixture) *api.HCPOpenShiftClusterNodePool {
		return fixture.newNodePool()
	}

	nodePoolWithoutCSID := func(fixture *nodePoolTestFixture) *api.HCPOpenShiftClusterNodePool {
		np := fixture.newNodePool()
		np.ServiceProviderProperties.ClusterServiceID = nil
		return np
	}

	nodePoolWithDeletionTimestamp := func(fixture *nodePoolTestFixture) *api.HCPOpenShiftClusterNodePool {
		np := fixture.newNodePool()
		np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
		return np
	}

	nodePoolWithMismatchedActiveOperationID := func(fixture *nodePoolTestFixture) *api.HCPOpenShiftClusterNodePool {
		np := fixture.newNodePool()
		np.ServiceProviderProperties.ActiveOperationID = "other-operation"
		return np
	}

	nodePoolWithEmptyActiveOperationID := func(fixture *nodePoolTestFixture) *api.HCPOpenShiftClusterNodePool {
		np := fixture.newNodePool()
		np.ServiceProviderProperties.ActiveOperationID = ""
		return np
	}

	setupCSNodePoolStatus := func(t *testing.T, mock *ocm.MockClusterServiceClientSpec, fixture *nodePoolTestFixture, state, msg string) {
		t.Helper()
		nodePoolStatusBuilder := arohcpv1alpha1.NewNodePoolStatus().
			State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(state))
		if msg != "" {
			nodePoolStatusBuilder = nodePoolStatusBuilder.Message(msg)
		}
		nodePoolStatus, err := nodePoolStatusBuilder.Build()
		require.NoError(t, err)
		mock.EXPECT().
			GetNodePoolStatus(gomock.Any(), fixture.nodePoolInternalID).
			Return(nodePoolStatus, nil)
	}

	fixture := newNodePoolTestFixture()
	preconditionExistingOperation := fixture.newOperation(database.OperationRequestCreate)
	preconditionListerOperation := fixture.newOperation(database.OperationRequestCreate)
	preconditionListerOperation.CosmosETag = "stale-etag"
	// Not seeded to Cosmos, so PrepareForCreate never runs. UpdateOperationStatus still
	// requires a non-zero InstanceVersion before it will attempt the etag-checked replace.
	preconditionListerOperation.InstanceVersion = 1

	tests := []struct {
		name              string
		nodePool          func(fixture *nodePoolTestFixture) *api.HCPOpenShiftClusterNodePool
		setupCSMock       func(t *testing.T, mock *ocm.MockClusterServiceClientSpec, fixture *nodePoolTestFixture)
		existingOperation *api.Operation
		// When not set, the controller uses an active operations lister that contains the existingOperation
		activeOperationsLister listers.ActiveOperationLister
		expectError            bool
		verifyDB               func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name:              "node pool ready transitions to succeeded",
			nodePool:          defaultNodePool,
			existingOperation: fixture.newOperation(database.OperationRequestCreate),
			setupCSMock: func(t *testing.T, mock *ocm.MockClusterServiceClientSpec, fixture *nodePoolTestFixture) {
				setupCSNodePoolStatus(t, mock, fixture, string(NodePoolStateReady), "")
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)

				nodePool, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, nodePool.Properties.ProvisioningState)
				assert.Empty(t, nodePool.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:              "node pool installing transitions to provisioning",
			nodePool:          defaultNodePool,
			existingOperation: fixture.newOperation(database.OperationRequestCreate),
			setupCSMock: func(t *testing.T, mock *ocm.MockClusterServiceClientSpec, fixture *nodePoolTestFixture) {
				setupCSNodePoolStatus(t, mock, fixture, string(NodePoolStateInstalling), "")
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateProvisioning, op.Status)

				nodePool, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateProvisioning, nodePool.Properties.ProvisioningState)
				assert.Equal(t, testOperationName, nodePool.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:              "node pool error transitions to failed",
			nodePool:          defaultNodePool,
			existingOperation: fixture.newOperation(database.OperationRequestCreate),
			setupCSMock: func(t *testing.T, mock *ocm.MockClusterServiceClientSpec, fixture *nodePoolTestFixture) {
				setupCSNodePoolStatus(t, mock, fixture, string(NodePoolStateError), "node pool creation failed")
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				assert.NotNil(t, op.Error)
				assert.Equal(t, "node pool creation failed", op.Error.Message)

				nodePool, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, nodePool.Properties.ProvisioningState)
				assert.Empty(t, nodePool.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:              "node pool pending stays accepted",
			nodePool:          defaultNodePool,
			existingOperation: fixture.newOperation(database.OperationRequestCreate),
			setupCSMock: func(t *testing.T, mock *ocm.MockClusterServiceClientSpec, fixture *nodePoolTestFixture) {
				setupCSNodePoolStatus(t, mock, fixture, string(NodePoolStatePending), "")
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:              "node pool validating stays accepted",
			nodePool:          defaultNodePool,
			existingOperation: fixture.newOperation(database.OperationRequestCreate),
			setupCSMock: func(t *testing.T, mock *ocm.MockClusterServiceClientSpec, fixture *nodePoolTestFixture) {
				setupCSNodePoolStatus(t, mock, fixture, string(NodePoolStateValidating), "")
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:              "ClusterServiceID nil skips reconciliation",
			nodePool:          nodePoolWithoutCSID,
			existingOperation: fixture.newOperation(database.OperationRequestCreate),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:              "ActiveOperationID mismatch skips reconciliation",
			nodePool:          nodePoolWithMismatchedActiveOperationID,
			existingOperation: fixture.newOperation(database.OperationRequestCreate),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:              "empty ActiveOperationID skips reconciliation",
			nodePool:          nodePoolWithEmptyActiveOperationID,
			existingOperation: fixture.newOperation(database.OperationRequestCreate),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:              "DeletionTimestamp set skips reconciliation",
			nodePool:          nodePoolWithDeletionTimestamp,
			existingOperation: fixture.newOperation(database.OperationRequestCreate),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:              "precondition failed on status update is ignored",
			nodePool:          defaultNodePool,
			existingOperation: preconditionExistingOperation,
			activeOperationsLister: &listertesting.SliceActiveOperationLister{
				Operations: []*api.Operation{preconditionListerOperation},
			},
			setupCSMock: func(t *testing.T, mock *ocm.MockClusterServiceClientSpec, fixture *nodePoolTestFixture) {
				setupCSNodePoolStatus(t, mock, fixture, string(NodePoolStateReady), "")
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status, "operation should be unchanged after optimistic concurrency conflict")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			cluster := fixture.newCluster()
			nodePool := tt.nodePool(fixture)

			resources := []any{cluster, nodePool, tt.existingOperation}
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			activeOperationsLister := tt.activeOperationsLister
			if activeOperationsLister == nil {
				activeOperationsLister = &listertesting.DBActiveOperationLister{ResourcesDBClient: mockResourcesDBClient}
			}

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tt.setupCSMock != nil {
				tt.setupCSMock(t, mockCSClient, fixture)
			}

			controller := &operationNodePoolCreate{
				clock:                  utilsclock.RealClock{},
				resourcesDBClient:      mockResourcesDBClient,
				activeOperationsLister: activeOperationsLister,
				nodePoolLister:         &listertesting.DBNodePoolLister{ResourcesDBClient: mockResourcesDBClient},
				clusterServiceClient:   mockCSClient,
				notificationClient:     nil,
			}

			err = controller.SynchronizeOperation(ctx, fixture.operationKey())
			if tt.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tt.verifyDB != nil {
				tt.verifyDB(t, ctx, mockResourcesDBClient)
			}
		})
	}
}
