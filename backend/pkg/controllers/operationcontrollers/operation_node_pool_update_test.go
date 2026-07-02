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

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/lru"
	"k8s.io/utils/ptr"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationNodePoolUpdate_SynchronizeOperation(t *testing.T) {
	t.Parallel()
	testClockNow := mustParseTime("2024-06-01T12:00:00Z")
	fixture := newNodePoolTestFixture()
	newNodePool := func(version string) *api.HCPOpenShiftClusterNodePool {
		np := fixture.newNodePool()
		np.Properties.Version.ID = version
		return np
	}
	newServiceProviderNodePool := func(desiredVersion *semver.Version) *api.ServiceProviderNodePool {
		sp := fixture.newServiceProviderNodePool()
		sp.Spec.NodePoolVersion.DesiredVersion = desiredVersion
		return sp
	}
	tests := []struct {
		name                              string
		mockCS                            func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec)
		nodePool                          *api.HCPOpenShiftClusterNodePool
		serviceProviderNodePool           *api.ServiceProviderNodePool
		nodePoolVersionController         *api.Controller
		desiredVersionMismatchFirstSeenAt time.Time
		expectError                       bool
		verifyDB                          func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture)
	}{
		{
			name: "node pool ready transitions to succeeded",
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				nodePoolStatus, err := arohcpv1alpha1.NewNodePoolStatus().
					State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(string(NodePoolStateReady))).
					Build()
				require.NoError(t, err)
				mockCS.EXPECT().
					GetNodePoolStatus(gomock.Any(), gomock.Any()).
					Return(nodePoolStatus, nil)
			},
			nodePool:                  newNodePool("4.19.0"),
			serviceProviderNodePool:   newServiceProviderNodePool(ptr.To(semver.MustParse("4.19.0"))),
			nodePoolVersionController: fixture.newNodePoolVersionController(nil),
			expectError:               false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
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
			name: "node pool updating transitions to updating",
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				nodePoolStatus, err := arohcpv1alpha1.NewNodePoolStatus().
					State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(string(NodePoolStateUpdating))).
					Build()
				require.NoError(t, err)
				mockCS.EXPECT().
					GetNodePoolStatus(gomock.Any(), gomock.Any()).
					Return(nodePoolStatus, nil)
			},
			nodePool:                  newNodePool("4.19.0"),
			serviceProviderNodePool:   newServiceProviderNodePool(ptr.To(semver.MustParse("4.19.0"))),
			nodePoolVersionController: fixture.newNodePoolVersionController(nil),
			expectError:               false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
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
			name: "node pool validating_update stays accepted",
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				nodePoolStatus, err := arohcpv1alpha1.NewNodePoolStatus().
					State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(string(NodePoolStateValidatingUpdate))).
					Build()
				require.NoError(t, err)
				mockCS.EXPECT().
					GetNodePoolStatus(gomock.Any(), gomock.Any()).
					Return(nodePoolStatus, nil)
			},
			nodePool:                  newNodePool("4.19.0"),
			serviceProviderNodePool:   newServiceProviderNodePool(ptr.To(semver.MustParse("4.19.0"))),
			nodePoolVersionController: fixture.newNodePoolVersionController(nil),
			expectError:               false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name: "node pool pending_update stays accepted",
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				nodePoolStatus, err := arohcpv1alpha1.NewNodePoolStatus().
					State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(string(NodePoolStatePendingUpdate))).
					Build()
				require.NoError(t, err)
				mockCS.EXPECT().
					GetNodePoolStatus(gomock.Any(), gomock.Any()).
					Return(nodePoolStatus, nil)
			},
			nodePool:                  newNodePool("4.19.0"),
			serviceProviderNodePool:   newServiceProviderNodePool(ptr.To(semver.MustParse("4.19.0"))),
			nodePoolVersionController: fixture.newNodePoolVersionController(nil),
			expectError:               false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name: "node pool recoverable_error transitions to failed",
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				nodePoolStatus, err := arohcpv1alpha1.NewNodePoolStatus().
					State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(string(NodePoolStateRecoverableError))).
					Message("temporary error occurred").
					Build()
				require.NoError(t, err)
				mockCS.EXPECT().
					GetNodePoolStatus(gomock.Any(), gomock.Any()).
					Return(nodePoolStatus, nil)
			},
			nodePool:                  newNodePool("4.19.0"),
			serviceProviderNodePool:   newServiceProviderNodePool(ptr.To(semver.MustParse("4.19.0"))),
			nodePoolVersionController: fixture.newNodePoolVersionController(nil),
			expectError:               false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				assert.NotNil(t, op.Error)
				assert.Contains(t, op.Error.Message, "temporary error occurred")
			},
		},
		{
			name: "node pool ready with exact version match transitions operation to succeeded",
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				nodePoolStatus, err := arohcpv1alpha1.NewNodePoolStatus().
					State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(string(NodePoolStateReady))).
					Build()
				require.NoError(t, err)
				mockCS.EXPECT().
					GetNodePoolStatus(gomock.Any(), gomock.Any()).
					Return(nodePoolStatus, nil)
			},
			nodePool:                  newNodePool("4.20.5"),
			serviceProviderNodePool:   newServiceProviderNodePool(ptr.To(semver.MustParse("4.20.5"))),
			nodePoolVersionController: fixture.newNodePoolVersionController(nil),
			expectError:               false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
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
			name: "node pool with version mismatch and IntentFailed condition marks operation failed",
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				nodePoolStatus, err := arohcpv1alpha1.NewNodePoolStatus().
					State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(string(NodePoolStateReady))).
					Build()
				require.NoError(t, err)
				mockCS.EXPECT().
					GetNodePoolStatus(gomock.Any(), gomock.Any()).
					Return(nodePoolStatus, nil)
			},
			nodePool:                newNodePool("4.21.5"),
			serviceProviderNodePool: newServiceProviderNodePool(ptr.To(semver.MustParse("4.20.5"))),
			nodePoolVersionController: fixture.newNodePoolVersionController([]metav1.Condition{
				{
					Type:    api.ControllerConditionTypeIntentFailed,
					Status:  metav1.ConditionTrue,
					Reason:  api.VersionUpgradeNotAcceptedReason,
					Message: "invalid node pool version 4.21.5: cannot exceed control plane version 4.20.5",
				},
			}),
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				require.NotNil(t, op.Error)
				assert.Equal(t, arm.CloudErrorCodeInvalidRequestContent, op.Error.Code)
				assert.Contains(t, op.Error.Message, "cannot exceed control plane version")

				nodePool, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, nodePool.Properties.ProvisioningState)
				assert.Empty(t, nodePool.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name: "node pool with version mismatch without IntentFailed leaves operation accepted",
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				nodePoolStatus, err := arohcpv1alpha1.NewNodePoolStatus().
					State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(string(NodePoolStateReady))).
					Build()
				require.NoError(t, err)
				mockCS.EXPECT().
					GetNodePoolStatus(gomock.Any(), gomock.Any()).
					Return(nodePoolStatus, nil)
			},
			nodePool:                  newNodePool("4.20.5"),
			serviceProviderNodePool:   newServiceProviderNodePool(ptr.To(semver.MustParse("4.20.4"))),
			nodePoolVersionController: fixture.newNodePoolVersionController(nil),
			expectError:               false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
				assert.Nil(t, op.Error)

				nodePool, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				assert.Equal(t, testOperationName, nodePool.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name: "node pool with version mismatch without IntentFailed leaves operation accepted when first seen within 59s",
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				nodePoolStatus, err := arohcpv1alpha1.NewNodePoolStatus().
					State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(string(NodePoolStateReady))).
					Build()
				require.NoError(t, err)
				mockCS.EXPECT().
					GetNodePoolStatus(gomock.Any(), gomock.Any()).
					Return(nodePoolStatus, nil)
			},
			nodePool:                          newNodePool("4.20.5"),
			serviceProviderNodePool:           newServiceProviderNodePool(ptr.To(semver.MustParse("4.20.4"))),
			nodePoolVersionController:         fixture.newNodePoolVersionController(nil),
			desiredVersionMismatchFirstSeenAt: testClockNow.Add(-50 * time.Second),
			expectError:                       false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
				assert.Nil(t, op.Error)

				nodePool, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				assert.Equal(t, testOperationName, nodePool.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name: "node pool with version mismatch without IntentFailed stays accepted even after 59s",
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				nodePoolStatus, err := arohcpv1alpha1.NewNodePoolStatus().
					State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(string(NodePoolStateReady))).
					Build()
				require.NoError(t, err)
				mockCS.EXPECT().
					GetNodePoolStatus(gomock.Any(), gomock.Any()).
					Return(nodePoolStatus, nil)
			},
			nodePool:                          newNodePool("4.20.5"),
			serviceProviderNodePool:           newServiceProviderNodePool(ptr.To(semver.MustParse("4.20.4"))),
			nodePoolVersionController:         fixture.newNodePoolVersionController(nil),
			desiredVersionMismatchFirstSeenAt: testClockNow.Add(-60 * time.Second),
			expectError:                       false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
				assert.Nil(t, op.Error)

				nodePool, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				assert.Equal(t, testOperationName, nodePool.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name: "node pool with exact version match but cluster service still updating transitions operation to updating",
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				nodePoolStatus, err := arohcpv1alpha1.NewNodePoolStatus().
					State(arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(string(NodePoolStateUpdating))).
					Build()
				require.NoError(t, err)
				mockCS.EXPECT().
					GetNodePoolStatus(gomock.Any(), gomock.Any()).
					Return(nodePoolStatus, nil)
			},
			nodePool:                  newNodePool("4.20.5"),
			serviceProviderNodePool:   newServiceProviderNodePool(ptr.To(semver.MustParse("4.20.5"))),
			nodePoolVersionController: fixture.newNodePoolVersionController(nil),
			expectError:               false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateUpdating, op.Status)

				nodePool, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateUpdating, nodePool.Properties.ProvisioningState)
				assert.Equal(t, testOperationName, nodePool.ServiceProviderProperties.ActiveOperationID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))
			ctrl := gomock.NewController(t)

			cluster := fixture.newCluster()
			operation := fixture.newOperation(database.OperationRequestUpdate)

			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, tt.nodePool, operation})
			require.NoError(t, err)

			_, err = mockResourcesDBClient.ServiceProviderNodePools(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName).Create(ctx, tt.serviceProviderNodePool, nil)
			require.NoError(t, err)

			_, err = mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).
				NodePools(testClusterName).Controllers(testNodePoolName).Create(ctx, tt.nodePoolVersionController, nil)
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			tt.mockCS(t, mockCSClient)

			fakeClock := clocktesting.NewFakeClock(testClockNow)
			controller := &operationNodePoolUpdate{
				resourcesDBClient:               mockResourcesDBClient,
				clusterServiceClient:            mockCSClient,
				notificationClient:              nil,
				clock:                           fakeClock,
				desiredVersionMismatchFirstSeen: lru.New(100000),
			}
			if !tt.desiredVersionMismatchFirstSeenAt.IsZero() {
				controller.desiredVersionMismatchFirstSeen.Add(operation.ResourceID.String(), tt.desiredVersionMismatchFirstSeenAt)
			}

			err = controller.SynchronizeOperation(ctx, fixture.operationKey())

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.verifyDB != nil {
				tt.verifyDB(t, ctx, mockResourcesDBClient, fixture)
			}
		})
	}
}
