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
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	operationtesting "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationNodePoolCreate_SynchronizeOperation(t *testing.T) {
	defaultNodePool := func(fixture *operationtesting.NodePoolTestFixture) *coreapi.HCPOpenShiftClusterNodePool {
		return fixture.NewNodePool()
	}

	nodePoolWithoutCSID := func(fixture *operationtesting.NodePoolTestFixture) *coreapi.HCPOpenShiftClusterNodePool {
		np := fixture.NewNodePool()
		np.ServiceProviderProperties.ClusterServiceID = nil
		return np
	}

	nodePoolWithDeletionTimestamp := func(fixture *operationtesting.NodePoolTestFixture) *coreapi.HCPOpenShiftClusterNodePool {
		np := fixture.NewNodePool()
		np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
		return np
	}

	nodePoolWithMismatchedActiveOperationID := func(fixture *operationtesting.NodePoolTestFixture) *coreapi.HCPOpenShiftClusterNodePool {
		np := fixture.NewNodePool()
		np.ServiceProviderProperties.ActiveOperationID = "other-operation"
		return np
	}

	nodePoolWithEmptyActiveOperationID := func(fixture *operationtesting.NodePoolTestFixture) *coreapi.HCPOpenShiftClusterNodePool {
		np := fixture.NewNodePool()
		np.ServiceProviderProperties.ActiveOperationID = ""
		return np
	}

	setupCSNodePoolStatus := func(t *testing.T, mock *ocm.MockClusterServiceClientSpec, fixture *operationtesting.NodePoolTestFixture, state, msg string) {
		t.Helper()
		nodePoolStatusBuilder := arohcpv1alpha1.NewNodePoolStatus().
			State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(state))
		if msg != "" {
			nodePoolStatusBuilder = nodePoolStatusBuilder.Message(msg)
		}
		nodePoolStatus, err := nodePoolStatusBuilder.Build()
		require.NoError(t, err)
		mock.EXPECT().
			GetNodePoolStatus(gomock.Any(), fixture.NodePoolInternalID).
			Return(nodePoolStatus, nil)
	}

	fixture := operationtesting.NewNodePoolTestFixture()
	preconditionExistingOperation := fixture.NewOperation(cosmosstorageutils.OperationRequestCreate)
	preconditionListerOperation := fixture.NewOperation(cosmosstorageutils.OperationRequestCreate)
	preconditionListerOperation.CosmosETag = "stale-etag"
	// Not seeded to Cosmos, so PrepareForCreate never runs. operationbase.UpdateOperationStatus still
	// requires a non-zero InstanceVersion before it will attempt the etag-checked replace.
	preconditionListerOperation.InstanceVersion = 1

	tests := []struct {
		name              string
		clock             utilsclock.PassiveClock
		nodePool          func(fixture *operationtesting.NodePoolTestFixture) *coreapi.HCPOpenShiftClusterNodePool
		setupCSMock       func(t *testing.T, mock *ocm.MockClusterServiceClientSpec, fixture *operationtesting.NodePoolTestFixture)
		existingOperation *coreapi.Operation
		// When not set, the controller uses an active operations lister that contains the existingOperation
		activeOperationsLister corelisters.ActiveOperationLister
		expectError            bool
		verifyDB               func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient)
	}{
		{
			name:              "node pool ready transitions to succeeded",
			nodePool:          defaultNodePool,
			existingOperation: fixture.NewOperation(cosmosstorageutils.OperationRequestCreate),
			setupCSMock: func(t *testing.T, mock *ocm.MockClusterServiceClientSpec, fixture *operationtesting.NodePoolTestFixture) {
				setupCSNodePoolStatus(t, mock, fixture, string(operationbase.NodePoolStateReady), "")
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateSucceeded, op.Status)

				nodePool, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).NodePools(operationtesting.TestClusterName).Get(ctx, operationtesting.TestNodePoolName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateSucceeded, nodePool.Properties.ProvisioningState)
				assert.Empty(t, nodePool.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:              "node pool installing transitions to provisioning",
			nodePool:          defaultNodePool,
			existingOperation: fixture.NewOperation(cosmosstorageutils.OperationRequestCreate),
			setupCSMock: func(t *testing.T, mock *ocm.MockClusterServiceClientSpec, fixture *operationtesting.NodePoolTestFixture) {
				setupCSNodePoolStatus(t, mock, fixture, string(operationbase.NodePoolStateInstalling), "")
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateProvisioning, op.Status)

				nodePool, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).NodePools(operationtesting.TestClusterName).Get(ctx, operationtesting.TestNodePoolName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateProvisioning, nodePool.Properties.ProvisioningState)
				assert.Equal(t, operationtesting.TestOperationName, nodePool.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:              "node pool error transitions to failed",
			nodePool:          defaultNodePool,
			existingOperation: fixture.NewOperation(cosmosstorageutils.OperationRequestCreate),
			setupCSMock: func(t *testing.T, mock *ocm.MockClusterServiceClientSpec, fixture *operationtesting.NodePoolTestFixture) {
				setupCSNodePoolStatus(t, mock, fixture, string(operationbase.NodePoolStateError), "node pool creation failed")
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateFailed, op.Status)
				assert.NotNil(t, op.Error)
				assert.Equal(t, "[clusterServiceNodePoolStatus] node pool creation failed", op.Error.Message)

				nodePool, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).NodePools(operationtesting.TestClusterName).Get(ctx, operationtesting.TestNodePoolName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateFailed, nodePool.Properties.ProvisioningState)
				assert.Empty(t, nodePool.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:              "node pool pending stays accepted",
			nodePool:          defaultNodePool,
			existingOperation: fixture.NewOperation(cosmosstorageutils.OperationRequestCreate),
			setupCSMock: func(t *testing.T, mock *ocm.MockClusterServiceClientSpec, fixture *operationtesting.NodePoolTestFixture) {
				setupCSNodePoolStatus(t, mock, fixture, string(operationbase.NodePoolStatePending), "")
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:              "node pool validating stays accepted",
			nodePool:          defaultNodePool,
			existingOperation: fixture.NewOperation(cosmosstorageutils.OperationRequestCreate),
			setupCSMock: func(t *testing.T, mock *ocm.MockClusterServiceClientSpec, fixture *operationtesting.NodePoolTestFixture) {
				setupCSNodePoolStatus(t, mock, fixture, string(operationbase.NodePoolStateValidating), "")
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:              "ClusterServiceID nil skips reconciliation",
			nodePool:          nodePoolWithoutCSID,
			existingOperation: fixture.NewOperation(cosmosstorageutils.OperationRequestCreate),
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:              "ActiveOperationID mismatch skips reconciliation",
			nodePool:          nodePoolWithMismatchedActiveOperationID,
			existingOperation: fixture.NewOperation(cosmosstorageutils.OperationRequestCreate),
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:              "empty ActiveOperationID skips reconciliation",
			nodePool:          nodePoolWithEmptyActiveOperationID,
			existingOperation: fixture.NewOperation(cosmosstorageutils.OperationRequestCreate),
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:              "DeletionTimestamp set skips reconciliation",
			nodePool:          nodePoolWithDeletionTimestamp,
			existingOperation: fixture.NewOperation(cosmosstorageutils.OperationRequestCreate),
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:  "deadline exceeded marks operation as failed",
			clock: clocktesting.NewFakePassiveClock(operationtesting.MustParseTime("2025-01-15T12:00:00Z")),
			nodePool: func(fixture *operationtesting.NodePoolTestFixture) *coreapi.HCPOpenShiftClusterNodePool {
				np := fixture.NewNodePool()
				deadline := metav1.NewTime(operationtesting.MustParseTime("2025-01-15T11:30:00Z"))
				np.ServiceProviderProperties.CreateOperationCompletionDeadline = &deadline
				return np
			},
			existingOperation: fixture.NewOperation(cosmosstorageutils.OperationRequestCreate),
			setupCSMock: func(t *testing.T, mock *ocm.MockClusterServiceClientSpec, fixture *operationtesting.NodePoolTestFixture) {
				setupCSNodePoolStatus(t, mock, fixture, string(operationbase.NodePoolStateInstalling), "")
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateFailed, op.Status)
				require.NotNil(t, op.Error)
				assert.Equal(t, coreapi.CloudErrorCodeInternalServerError, op.Error.Code)
				assert.Contains(t, op.Error.Message, "node pool creation did not complete before the deadline")
				assert.Contains(t, op.Error.Message, "cluster service node pool is installing")
			},
		},
		{
			name:  "deadline not yet exceeded continues with provisioning",
			clock: clocktesting.NewFakePassiveClock(operationtesting.MustParseTime("2025-01-15T11:00:00Z")),
			nodePool: func(fixture *operationtesting.NodePoolTestFixture) *coreapi.HCPOpenShiftClusterNodePool {
				np := fixture.NewNodePool()
				deadline := metav1.NewTime(operationtesting.MustParseTime("2025-01-15T11:30:00Z"))
				np.ServiceProviderProperties.CreateOperationCompletionDeadline = &deadline
				return np
			},
			existingOperation: fixture.NewOperation(cosmosstorageutils.OperationRequestCreate),
			setupCSMock: func(t *testing.T, mock *ocm.MockClusterServiceClientSpec, fixture *operationtesting.NodePoolTestFixture) {
				setupCSNodePoolStatus(t, mock, fixture, string(operationbase.NodePoolStateInstalling), "")
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateProvisioning, op.Status)
			},
		},
		{
			name:              "precondition failed on status update is ignored",
			nodePool:          defaultNodePool,
			existingOperation: preconditionExistingOperation,
			activeOperationsLister: &corelistertesting.SliceActiveOperationLister{
				Operations: []*coreapi.Operation{preconditionListerOperation},
			},
			setupCSMock: func(t *testing.T, mock *ocm.MockClusterServiceClientSpec, fixture *operationtesting.NodePoolTestFixture) {
				setupCSNodePoolStatus(t, mock, fixture, string(operationbase.NodePoolStateReady), "")
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateAccepted, op.Status, "operation should be unchanged after optimistic concurrency conflict")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			cluster := fixture.NewCluster()
			nodePool := tt.nodePool(fixture)

			resources := []any{cluster, nodePool, tt.existingOperation}
			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			activeOperationsLister := tt.activeOperationsLister
			if activeOperationsLister == nil {
				activeOperationsLister = &corelistertesting.DBActiveOperationLister{ResourcesDBClient: mockResourcesDBClient}
			}

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tt.setupCSMock != nil {
				tt.setupCSMock(t, mockCSClient, fixture)
			}

			testClock := tt.clock
			if testClock == nil {
				testClock = utilsclock.RealClock{}
			}
			controller := &operationNodePoolCreate{
				clock:                  testClock,
				resourcesDBClient:      mockResourcesDBClient,
				activeOperationsLister: activeOperationsLister,
				nodePoolLister:         &corelistertesting.DBNodePoolLister{ResourcesDBClient: mockResourcesDBClient},
				clusterServiceClient:   mockCSClient,
				notificationClient:     nil,
			}

			err = controller.SynchronizeOperation(ctx, fixture.OperationKey())
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
