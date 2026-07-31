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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/json"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/lru"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	internallistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationNodePoolUpdate_SynchronizeOperation(t *testing.T) {
	testClockNow := mustParseTime("2024-06-01T12:00:00Z")
	fixture := newNodePoolTestFixture()

	newNodePoolWithVersion := func(version string, mutate ...func(*api.HCPOpenShiftClusterNodePool)) *api.HCPOpenShiftClusterNodePool {
		nodePool := fixture.newNodePool()
		nodePool.Properties.Version.ID = version
		for _, fn := range mutate {
			if fn != nil {
				fn(nodePool)
			}
		}
		return nodePool
	}

	newOperationAccepted := func() *api.Operation {
		return fixture.newOperation(database.OperationRequestUpdate)
	}

	newServiceProviderNodePoolWithDesiredVersion := func(version string) *api.ServiceProviderNodePool {
		sp := fixture.newServiceProviderNodePool()
		sp.Spec.NodePoolVersion.DesiredVersion = ptr.To(semver.MustParse(version))
		return sp
	}

	newDefaultNodePoolVersionController := func() *api.Controller {
		return fixture.newNodePoolVersionController(nil)
	}

	newNodePoolVersionControllerWithConditions := func(conditions []metav1.Condition) *api.Controller {
		return fixture.newNodePoolVersionController(conditions)
	}

	newPassingCachedNodePoolReadDesire := func(nodePool *api.HCPOpenShiftClusterNodePool) *kubeapplier.ReadDesire {
		return newNodePoolReadDesire(t, nodePool, fixture.newCluster())
	}

	setupMockCSClientForNodePoolState := func(state NodePoolStateValue, statusMessage ...string) func(*ocm.MockClusterServiceClientSpec) {
		return func(mock *ocm.MockClusterServiceClientSpec) {
			stateBuilder := arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(string(state))
			statusBuilder := arohcpv1alpha1.NewNodePoolStatus().State(stateBuilder)
			if len(statusMessage) > 0 {
				statusBuilder = statusBuilder.Message(statusMessage[0])
			}
			csNodePool, err := arohcpv1alpha1.NewNodePool().
				Replicas(0).
				NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
					Unit("minutes").
					Value(float64(0))).
				Status(statusBuilder).
				Build()
			require.NoError(t, err)
			mock.EXPECT().
				GetNodePool(gomock.Any(), gomock.Any()).
				Return(csNodePool, nil)
		}
	}

	setupMockCSClientForNodePoolReadyWithSpec := func(replicas int, nodeDrainMinutes int32) func(*ocm.MockClusterServiceClientSpec) {
		return func(mock *ocm.MockClusterServiceClientSpec) {
			stateBuilder := arohcpv1alpha1.NewNodePoolState().NodePoolStateValue(string(NodePoolStateReady))
			statusBuilder := arohcpv1alpha1.NewNodePoolStatus().State(stateBuilder)
			csNodePool, err := arohcpv1alpha1.NewNodePool().
				Replicas(replicas).
				NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
					Unit("minutes").
					Value(float64(nodeDrainMinutes))).
				Status(statusBuilder).
				Build()
			require.NoError(t, err)
			mock.EXPECT().
				GetNodePool(gomock.Any(), gomock.Any()).
				Return(csNodePool, nil)
		}
	}

	testCases := []struct {
		name             string
		existingNodePool *api.HCPOpenShiftClusterNodePool
		// When not set, the controller uses a node pool lister that contains the existingNodePool.
		nodePoolLister    listers.NodePoolLister
		existingOperation *api.Operation
		// When not set, the controller uses an active operations lister that contains the existingOperation.
		activeOperationsLister          listers.ActiveOperationLister
		existingServiceProviderNodePool *api.ServiceProviderNodePool
		// When not set, the controller uses a service provider node pool lister that contains the existingServiceProviderNodePool.
		serviceProviderNodePoolLister     listers.ServiceProviderNodePoolLister
		existingNodePoolVersionController *api.Controller
		// When set, wires a ReadDesireLister containing this cached Hypershift NodePool mirror.
		cachedNodePoolReadDesire *kubeapplier.ReadDesire
		seedMismatchFirstSeenAt  time.Time
		setupMockCSClient        func(*ocm.MockClusterServiceClientSpec)
		wantErr                  bool
		verifyDB                 func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name:                            "cs node pool ready transitions operation to succeeded",
			existingNodePool:                newNodePoolWithVersion("4.19.0"),
			existingOperation:               newOperationAccepted(),
			existingServiceProviderNodePool: newServiceProviderNodePoolWithDesiredVersion("4.19.0"),
			cachedNodePoolReadDesire:        newPassingCachedNodePoolReadDesire(newNodePoolWithVersion("4.19.0")),
			setupMockCSClient:               setupMockCSClientForNodePoolState(NodePoolStateReady),
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
			name:                            "cs node pool updating transitions operation to updating",
			existingNodePool:                newNodePoolWithVersion("4.19.0"),
			existingOperation:               newOperationAccepted(),
			existingServiceProviderNodePool: newServiceProviderNodePoolWithDesiredVersion("4.19.0"),
			cachedNodePoolReadDesire:        newPassingCachedNodePoolReadDesire(newNodePoolWithVersion("4.19.0")),
			setupMockCSClient:               setupMockCSClientForNodePoolState(NodePoolStateUpdating),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateUpdating, op.Status)

				nodePool, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateUpdating, nodePool.Properties.ProvisioningState)
				assert.Equal(t, testOperationName, nodePool.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:                            "cs node pool validating_update keeps operation accepted",
			existingNodePool:                newNodePoolWithVersion("4.19.0"),
			existingOperation:               newOperationAccepted(),
			existingServiceProviderNodePool: newServiceProviderNodePoolWithDesiredVersion("4.19.0"),
			cachedNodePoolReadDesire:        newPassingCachedNodePoolReadDesire(newNodePoolWithVersion("4.19.0")),
			setupMockCSClient:               setupMockCSClientForNodePoolState(NodePoolStateValidatingUpdate),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:                            "cs node pool pending_update keeps operation accepted",
			existingNodePool:                newNodePoolWithVersion("4.19.0"),
			existingOperation:               newOperationAccepted(),
			existingServiceProviderNodePool: newServiceProviderNodePoolWithDesiredVersion("4.19.0"),
			cachedNodePoolReadDesire:        newPassingCachedNodePoolReadDesire(newNodePoolWithVersion("4.19.0")),
			setupMockCSClient:               setupMockCSClientForNodePoolState(NodePoolStatePendingUpdate),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:                            "cs node pool recoverable_error transitions operation to failed",
			existingNodePool:                newNodePoolWithVersion("4.19.0"),
			existingOperation:               newOperationAccepted(),
			existingServiceProviderNodePool: newServiceProviderNodePoolWithDesiredVersion("4.19.0"),
			cachedNodePoolReadDesire:        newPassingCachedNodePoolReadDesire(newNodePoolWithVersion("4.19.0")),
			setupMockCSClient:               setupMockCSClientForNodePoolState(NodePoolStateRecoverableError, "temporary error occurred"),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				assert.NotNil(t, op.Error)
				assert.Contains(t, op.Error.Message, "temporary error occurred")
			},
		},
		{
			name:                            "customer version mismatch with IntentFailed on NodePoolVersion controller marks operation failed",
			existingNodePool:                newNodePoolWithVersion("4.21.5"),
			existingOperation:               newOperationAccepted(),
			existingServiceProviderNodePool: newServiceProviderNodePoolWithDesiredVersion("4.20.5"),
			existingNodePoolVersionController: newNodePoolVersionControllerWithConditions([]metav1.Condition{
				{
					Type:    api.ControllerConditionTypeIntentFailed,
					Status:  metav1.ConditionTrue,
					Reason:  api.VersionUpgradeNotAcceptedReason,
					Message: "invalid node pool version 4.21.5: cannot exceed control plane version 4.20.5",
				},
			}),
			cachedNodePoolReadDesire: newPassingCachedNodePoolReadDesire(newNodePoolWithVersion("4.21.5")),
			setupMockCSClient:        setupMockCSClientForNodePoolState(NodePoolStateReady),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
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
			name:                              "customer version mismatch without NodePoolVersion IntentFailed leaves operation accepted",
			existingNodePool:                  newNodePoolWithVersion("4.20.5"),
			existingOperation:                 newOperationAccepted(),
			existingServiceProviderNodePool:   newServiceProviderNodePoolWithDesiredVersion("4.20.4"),
			existingNodePoolVersionController: newDefaultNodePoolVersionController(),
			cachedNodePoolReadDesire:          newPassingCachedNodePoolReadDesire(newNodePoolWithVersion("4.20.5")),
			setupMockCSClient:                 setupMockCSClientForNodePoolState(NodePoolStateReady),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
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
			name:                            "customer version mismatch without NodePoolVersion IntentFailed leaves operation accepted when first seen within 129s",
			existingNodePool:                newNodePoolWithVersion("4.20.5"),
			existingOperation:               newOperationAccepted(),
			existingServiceProviderNodePool: newServiceProviderNodePoolWithDesiredVersion("4.20.4"),
			existingNodePoolVersionController: newNodePoolVersionControllerWithConditions([]metav1.Condition{
				{Type: api.ControllerConditionTypeIntentFailed, Status: metav1.ConditionFalse},
			}),
			cachedNodePoolReadDesire: newPassingCachedNodePoolReadDesire(newNodePoolWithVersion("4.20.5")),
			seedMismatchFirstSeenAt:  testClockNow.Add(-120 * time.Second),
			setupMockCSClient:        setupMockCSClientForNodePoolState(NodePoolStateReady),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
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
			name:                            "customer version mismatch without IntentFailed fails when mismatch first seen exceeds 129s",
			existingNodePool:                newNodePoolWithVersion("4.20.5"),
			existingOperation:               newOperationAccepted(),
			existingServiceProviderNodePool: newServiceProviderNodePoolWithDesiredVersion("4.20.4"),
			existingNodePoolVersionController: newNodePoolVersionControllerWithConditions([]metav1.Condition{
				{Type: api.ControllerConditionTypeIntentFailed, Status: metav1.ConditionFalse},
			}),
			cachedNodePoolReadDesire: newPassingCachedNodePoolReadDesire(newNodePoolWithVersion("4.20.5")),
			seedMismatchFirstSeenAt:  testClockNow.Add(-130 * time.Second),
			setupMockCSClient:        setupMockCSClientForNodePoolState(NodePoolStateReady),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				require.NotNil(t, op.Error)
				assert.Equal(t, arm.CloudErrorCodeInvalidRequestContent, op.Error.Code)

				nodePool, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				wantMessageSubstr := fmt.Sprintf(
					"timed out after 129s waiting for resolution of desired version from '%s' node pool version",
					nodePool.Properties.Version.ID,
				)
				assert.Contains(t, op.Error.Message, wantMessageSubstr)

				assert.Equal(t, arm.ProvisioningStateFailed, nodePool.Properties.ProvisioningState)
				assert.Empty(t, nodePool.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:                            "exact version match but cs node pool still updating transitions operation to updating",
			existingNodePool:                newNodePoolWithVersion("4.20.5"),
			existingOperation:               newOperationAccepted(),
			existingServiceProviderNodePool: newServiceProviderNodePoolWithDesiredVersion("4.20.5"),
			cachedNodePoolReadDesire:        newPassingCachedNodePoolReadDesire(newNodePoolWithVersion("4.20.5")),
			setupMockCSClient:               setupMockCSClientForNodePoolState(NodePoolStateUpdating),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateUpdating, op.Status)

				nodePool, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateUpdating, nodePool.Properties.ProvisioningState)
				assert.Equal(t, testOperationName, nodePool.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name: "cs node pool ready with hypershift node drain spec mismatch keeps operation updating",
			existingNodePool: newNodePoolWithVersion("4.19.0", func(nodePool *api.HCPOpenShiftClusterNodePool) {
				nodePool.Properties.NodeDrainTimeoutMinutes = ptr.To(int32(60))
			}),
			existingOperation:               newOperationAccepted(),
			existingServiceProviderNodePool: newServiceProviderNodePoolWithDesiredVersion("4.19.0"),
			cachedNodePoolReadDesire:        newHypershiftNodePoolReadDesire(t, testNodePoolUpdateMatchingHypershiftNodePool(30)),
			setupMockCSClient:               setupMockCSClientForNodePoolReadyWithSpec(0, 60),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateUpdating, op.Status)
				assert.Nil(t, op.Error)

				nodePool, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateUpdating, nodePool.Properties.ProvisioningState)
				assert.Equal(t, testOperationName, nodePool.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name: "cs node pool ready with hypershift replicas spec mismatch keeps operation updating",
			existingNodePool: newNodePoolWithVersion("4.19.0", func(nodePool *api.HCPOpenShiftClusterNodePool) {
				nodePool.Properties.Replicas = 3
			}),
			existingOperation:               newOperationAccepted(),
			existingServiceProviderNodePool: newServiceProviderNodePoolWithDesiredVersion("4.19.0"),
			cachedNodePoolReadDesire: newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
				np := testNodePoolUpdateMatchingHypershiftNodePool(0)
				np.Spec.Replicas = ptr.To(int32(1))
				np.Status.Replicas = 1
				return np
			}()),
			setupMockCSClient: setupMockCSClientForNodePoolReadyWithSpec(3, 0),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateUpdating, op.Status)
				assert.Nil(t, op.Error)

				nodePool, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateUpdating, nodePool.Properties.ProvisioningState)
				assert.Equal(t, testOperationName, nodePool.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name: "shouldReconcile gate not passed when ClusterServiceID is nil",
			existingNodePool: newNodePoolWithVersion("4.19.0", func(nodePool *api.HCPOpenShiftClusterNodePool) {
				nodePool.ServiceProviderProperties.ClusterServiceID = nil
			}),
			existingOperation: newOperationAccepted(),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name: "shouldReconcile gate not passed when node pool is deleting",
			existingNodePool: newNodePoolWithVersion("4.19.0", func(nodePool *api.HCPOpenShiftClusterNodePool) {
				nodePool.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: testClockNow}
			}),
			existingOperation: newOperationAccepted(),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:              "node pool not in lister cache leaves operation unchanged",
			existingNodePool:  newNodePoolWithVersion("4.19.0"),
			existingOperation: newOperationAccepted(),
			nodePoolLister:    &listertesting.SliceNodePoolLister{},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)

				nodePool, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				assert.Equal(t, testOperationName, nodePool.ServiceProviderProperties.ActiveOperationID)
				assert.Equal(t, arm.ProvisioningStateAccepted, nodePool.Properties.ProvisioningState)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)

			resources := []any{}
			if tc.existingNodePool != nil {
				resources = append(resources, tc.existingNodePool)
			}
			if tc.existingOperation != nil {
				resources = append(resources, tc.existingOperation)
			}
			if tc.existingServiceProviderNodePool != nil {
				resources = append(resources, tc.existingServiceProviderNodePool)
			}
			if tc.existingNodePoolVersionController != nil {
				resources = append(resources, tc.existingNodePoolVersionController)
			}

			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			var readDesireLister dblisters.ReadDesireLister
			if tc.cachedNodePoolReadDesire != nil {
				readDesireLister = &internallistertesting.SliceReadDesireLister{
					Desires: []*kubeapplier.ReadDesire{tc.cachedNodePoolReadDesire},
				}
			}

			nodePoolLister := tc.nodePoolLister
			if nodePoolLister == nil {
				nodePoolLister = &listertesting.DBNodePoolLister{ResourcesDBClient: mockResourcesDBClient}
			}
			activeOperationsLister := tc.activeOperationsLister
			if activeOperationsLister == nil {
				activeOperationsLister = &listertesting.DBActiveOperationLister{ResourcesDBClient: mockResourcesDBClient}
			}
			serviceProviderNodePoolLister := tc.serviceProviderNodePoolLister
			if serviceProviderNodePoolLister == nil {
				serviceProviderNodePoolLister = &listertesting.DBServiceProviderNodePoolLister{ResourcesDBClient: mockResourcesDBClient}
			}

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tc.setupMockCSClient != nil {
				tc.setupMockCSClient(mockCSClient)
			}

			fakeClock := clocktesting.NewFakeClock(testClockNow)
			controller := &operationNodePoolUpdate{
				resourcesDBClient:               mockResourcesDBClient,
				clusterServiceClient:            mockCSClient,
				nodePoolLister:                  nodePoolLister,
				serviceProviderNodePoolLister:   serviceProviderNodePoolLister,
				readDesireLister:                readDesireLister,
				activeOperationsLister:          activeOperationsLister,
				notificationClient:              nil,
				clock:                           fakeClock,
				desiredVersionMismatchFirstSeen: lru.New(100000),
			}
			if !tc.seedMismatchFirstSeenAt.IsZero() {
				require.NotNil(t, tc.existingOperation)
				controller.desiredVersionMismatchFirstSeen.Add(strings.ToLower(tc.existingOperation.ResourceID.String()), tc.seedMismatchFirstSeenAt)
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

func newNodePoolReadDesire(t *testing.T, nodePool *api.HCPOpenShiftClusterNodePool, cluster *api.HCPOpenShiftCluster) *kubeapplier.ReadDesire {
	t.Helper()

	hsNodePool := nodePoolToHypershiftNodePool(nodePool, cluster)
	raw, err := json.Marshal(hsNodePool)
	require.NoError(t, err)

	resourceID := api.Must(azcorearm.ParseResourceID(
		kubeapplier.ToNodePoolScopedReadDesireResourceIDString(
			testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName,
			kubeapplierhelpers.ReadDesireNameReadonlyNodePool)))

	return &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
		Status: kubeapplier.ReadDesireStatus{
			Conditions: []metav1.Condition{
				{Type: kubeapplier.ConditionTypeSuccessful, Status: metav1.ConditionTrue, Reason: kubeapplier.ConditionReasonNoErrors},
			},
			KubeContent: &kruntime.RawExtension{Raw: raw},
		},
	}
}

func nodePoolToHypershiftNodePool(nodePool *api.HCPOpenShiftClusterNodePool, cluster *api.HCPOpenShiftCluster) *v1beta1.NodePool {
	effectiveNodeDrainMinutes := nodePool.Properties.NodeDrainTimeoutMinutes
	if effectiveNodeDrainMinutes == nil {
		effectiveNodeDrainMinutes = &cluster.CustomerProperties.NodeDrainTimeoutMinutes
	}

	np := &v1beta1.NodePool{
		Spec: v1beta1.NodePoolSpec{
			NodeLabels: nodePool.Properties.Labels,
			Replicas:   ptr.To(nodePool.Properties.Replicas),
		},
		Status: v1beta1.NodePoolStatus{
			Replicas: nodePool.Properties.Replicas,
			Conditions: []v1beta1.NodePoolCondition{
				{
					Type:   v1beta1.NodePoolAllMachinesReadyConditionType,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
	if *effectiveNodeDrainMinutes != 0 {
		np.Spec.NodeDrainTimeout = &metav1.Duration{Duration: time.Duration(*effectiveNodeDrainMinutes) * time.Minute}
	}
	if nodePool.Properties.AutoScaling != nil {
		np.Spec.AutoScaling = &v1beta1.NodePoolAutoScaling{
			Min: ptr.To(nodePool.Properties.AutoScaling.Min),
			Max: nodePool.Properties.AutoScaling.Max,
		}
		np.Spec.Replicas = nil
		np.Status.Replicas = nodePool.Properties.AutoScaling.Min
	}
	if len(nodePool.Properties.Taints) > 0 {
		np.Spec.Taints = make([]v1beta1.Taint, 0, len(nodePool.Properties.Taints))
		for _, taint := range nodePool.Properties.Taints {
			np.Spec.Taints = append(np.Spec.Taints, v1beta1.Taint{
				Key:    taint.Key,
				Value:  taint.Value,
				Effect: corev1.TaintEffect(taint.Effect),
			})
		}
	}
	return np
}
