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
	"encoding/json"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	internallistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func newNodePoolReadDesire(t *testing.T, nodePool *v1beta1.NodePool, conditions ...metav1.Condition) *kubeapplier.ReadDesire {
	t.Helper()
	raw, err := json.Marshal(nodePool)
	require.NoError(t, err)
	if conditions == nil {
		conditions = []metav1.Condition{
			{Type: kubeapplier.ConditionTypeSuccessful, Status: metav1.ConditionTrue, Reason: kubeapplier.ConditionReasonNoErrors},
		}
	}

	resourceID := api.Must(azcorearm.ParseResourceID(
		kubeapplier.ToNodePoolScopedReadDesireResourceIDString(
			testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName, maestrohelpers.ReadDesireNameReadonlyNodePool)))

	return &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
		Status: kubeapplier.ReadDesireStatus{
			Conditions:  conditions,
			KubeContent: &kruntime.RawExtension{Raw: raw},
		},
	}
}

func TestOperationNodePoolUpdate_SynchronizeOperation(t *testing.T) {
	tests := []struct {
		name             string
		nodePoolState    string
		nodePoolMsg      string
		nodePoolProps    *api.HCPOpenShiftClusterNodePoolProperties
		readDesireLister internallistertesting.SliceReadDesireLister
		expectError      bool
		verify           func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture)
	}{
		{
			name:          "node pool ready and management cluster spec and status match transitions to succeeded",
			nodePoolState: string(NodePoolStateReady),
			nodePoolProps: &api.HCPOpenShiftClusterNodePoolProperties{
				ProvisioningState: arm.ProvisioningStateAccepted,
				Replicas:          3,
			},
			readDesireLister: internallistertesting.SliceReadDesireLister{
				Desires: []*kubeapplier.ReadDesire{
					newNodePoolReadDesire(t, &v1beta1.NodePool{
						Spec: v1beta1.NodePoolSpec{
							Replicas: ptr.To(int32(3)),
						},
						Status: v1beta1.NodePoolStatus{
							Replicas: 3,
							Conditions: []v1beta1.NodePoolCondition{
								{Type: v1beta1.NodePoolReadyConditionType, Status: corev1.ConditionTrue},
							},
						},
					}),
				},
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
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
			name:          "node pool ready with matching spec but status replicas mismatch returns gate error",
			nodePoolState: string(NodePoolStateReady),
			nodePoolProps: &api.HCPOpenShiftClusterNodePoolProperties{
				ProvisioningState: arm.ProvisioningStateAccepted,
				Replicas:          3,
			},
			readDesireLister: internallistertesting.SliceReadDesireLister{
				Desires: []*kubeapplier.ReadDesire{
					newNodePoolReadDesire(t, &v1beta1.NodePool{
						Spec: v1beta1.NodePoolSpec{
							Replicas: ptr.To(int32(3)),
						},
						Status: v1beta1.NodePoolStatus{
							Replicas: 2,
						},
					}),
				},
			},
			expectError: true,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:          "node pool ready with autoscaling spec and status in range transitions to succeeded",
			nodePoolState: string(NodePoolStateReady),
			nodePoolProps: &api.HCPOpenShiftClusterNodePoolProperties{
				ProvisioningState: arm.ProvisioningStateAccepted,
				AutoScaling:       &api.NodePoolAutoScaling{Min: 2, Max: 5},
			},
			readDesireLister: internallistertesting.SliceReadDesireLister{
				Desires: []*kubeapplier.ReadDesire{
					newNodePoolReadDesire(t, &v1beta1.NodePool{
						Spec: v1beta1.NodePoolSpec{
							AutoScaling: &v1beta1.NodePoolAutoScaling{
								Min: ptr.To(int32(2)),
								Max: 5,
							},
						},
						Status: v1beta1.NodePoolStatus{
							Replicas: 3,
							Conditions: []v1beta1.NodePoolCondition{
								{Type: v1beta1.NodePoolAutoscalingEnabledConditionType, Status: corev1.ConditionTrue},
								{Type: v1beta1.NodePoolReadyConditionType, Status: corev1.ConditionTrue},
							},
						},
					}),
				},
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)
			},
		},
		{
			name:          "node pool ready with autoscaling spec match but status below min returns gate error",
			nodePoolState: string(NodePoolStateReady),
			nodePoolProps: &api.HCPOpenShiftClusterNodePoolProperties{
				ProvisioningState: arm.ProvisioningStateAccepted,
				AutoScaling:       &api.NodePoolAutoScaling{Min: 2, Max: 5},
			},
			readDesireLister: internallistertesting.SliceReadDesireLister{
				Desires: []*kubeapplier.ReadDesire{
					newNodePoolReadDesire(t, &v1beta1.NodePool{
						Spec: v1beta1.NodePoolSpec{
							AutoScaling: &v1beta1.NodePoolAutoScaling{
								Min: ptr.To(int32(2)),
								Max: 5,
							},
						},
						Status: v1beta1.NodePoolStatus{
							Replicas: 1,
							Conditions: []v1beta1.NodePoolCondition{
								{Type: v1beta1.NodePoolAutoscalingEnabledConditionType, Status: corev1.ConditionTrue},
							},
						},
					}),
				},
			},
			expectError: true,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:          "node pool ready with labels spec and status match transitions to succeeded",
			nodePoolState: string(NodePoolStateReady),
			nodePoolProps: &api.HCPOpenShiftClusterNodePoolProperties{
				ProvisioningState: arm.ProvisioningStateAccepted,
				Replicas:          1,
				Labels:            map[string]string{"role": "worker"},
			},
			readDesireLister: internallistertesting.SliceReadDesireLister{
				Desires: []*kubeapplier.ReadDesire{
					newNodePoolReadDesire(t, &v1beta1.NodePool{
						Spec: v1beta1.NodePoolSpec{
							Replicas:   ptr.To(int32(1)),
							NodeLabels: map[string]string{"role": "worker"},
						},
						Status: v1beta1.NodePoolStatus{
							Replicas: 1,
							Conditions: []v1beta1.NodePoolCondition{
								{Type: v1beta1.NodePoolReadyConditionType, Status: corev1.ConditionTrue},
							},
						},
					}),
				},
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)
			},
		},
		{
			name:          "node pool ready with labels spec mismatch returns gate error",
			nodePoolState: string(NodePoolStateReady),
			nodePoolProps: &api.HCPOpenShiftClusterNodePoolProperties{
				ProvisioningState: arm.ProvisioningStateAccepted,
				Replicas:          1,
				Labels:            map[string]string{"role": "worker"},
			},
			readDesireLister: internallistertesting.SliceReadDesireLister{
				Desires: []*kubeapplier.ReadDesire{
					newNodePoolReadDesire(t, &v1beta1.NodePool{
						Spec: v1beta1.NodePoolSpec{
							Replicas:   ptr.To(int32(1)),
							NodeLabels: map[string]string{"role": "infra"},
						},
						Status: v1beta1.NodePoolStatus{
							Replicas: 1,
							Conditions: []v1beta1.NodePoolCondition{
								{Type: v1beta1.NodePoolReadyConditionType, Status: corev1.ConditionTrue},
							},
						},
					}),
				},
			},
			expectError: true,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:          "node pool ready with taints spec mismatch returns gate error",
			nodePoolState: string(NodePoolStateReady),
			nodePoolProps: &api.HCPOpenShiftClusterNodePoolProperties{
				ProvisioningState: arm.ProvisioningStateAccepted,
				Replicas:          1,
				Taints: []api.Taint{
					{Key: "k1", Value: "v1", Effect: api.EffectNoSchedule},
				},
			},
			readDesireLister: internallistertesting.SliceReadDesireLister{
				Desires: []*kubeapplier.ReadDesire{
					newNodePoolReadDesire(t, &v1beta1.NodePool{
						Spec: v1beta1.NodePoolSpec{
							Replicas: ptr.To(int32(1)),
						},
						Status: v1beta1.NodePoolStatus{
							Replicas: 1,
							Conditions: []v1beta1.NodePoolCondition{
								{Type: v1beta1.NodePoolReadyConditionType, Status: corev1.ConditionTrue},
							},
						},
					}),
				},
			},
			expectError: true,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:          "node pool ready with node drain timeout spec mismatch returns gate error",
			nodePoolState: string(NodePoolStateReady),
			nodePoolProps: &api.HCPOpenShiftClusterNodePoolProperties{
				ProvisioningState:       arm.ProvisioningStateAccepted,
				Replicas:                1,
				NodeDrainTimeoutMinutes: ptr.To(int32(30)),
			},
			readDesireLister: internallistertesting.SliceReadDesireLister{
				Desires: []*kubeapplier.ReadDesire{
					newNodePoolReadDesire(t, &v1beta1.NodePool{
						Spec: v1beta1.NodePoolSpec{
							Replicas:         ptr.To(int32(1)),
							NodeDrainTimeout: &metav1.Duration{Duration: 15 * time.Minute},
						},
						Status: v1beta1.NodePoolStatus{
							Replicas: 1,
							Conditions: []v1beta1.NodePoolCondition{
								{Type: v1beta1.NodePoolReadyConditionType, Status: corev1.ConditionTrue},
							},
						},
					}),
				},
			},
			expectError: true,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:          "node pool ready with UpdatingConfig condition returns gate error",
			nodePoolState: string(NodePoolStateReady),
			nodePoolProps: &api.HCPOpenShiftClusterNodePoolProperties{
				ProvisioningState: arm.ProvisioningStateAccepted,
				Replicas:          3,
			},
			readDesireLister: internallistertesting.SliceReadDesireLister{
				Desires: []*kubeapplier.ReadDesire{
					newNodePoolReadDesire(t, &v1beta1.NodePool{
						Spec: v1beta1.NodePoolSpec{
							Replicas: ptr.To(int32(3)),
						},
						Status: v1beta1.NodePoolStatus{
							Replicas: 3,
							Conditions: []v1beta1.NodePoolCondition{
								{Type: v1beta1.NodePoolUpdatingConfigConditionType, Status: corev1.ConditionTrue},
								{Type: v1beta1.NodePoolReadyConditionType, Status: corev1.ConditionTrue},
							},
						},
					}),
				},
			},
			expectError: true,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:          "node pool ready but management cluster spec mismatch returns gate error",
			nodePoolState: string(NodePoolStateReady),
			nodePoolProps: &api.HCPOpenShiftClusterNodePoolProperties{
				ProvisioningState: arm.ProvisioningStateAccepted,
				AutoScaling:       &api.NodePoolAutoScaling{Min: 2, Max: 5},
			},
			readDesireLister: internallistertesting.SliceReadDesireLister{
				Desires: []*kubeapplier.ReadDesire{
					newNodePoolReadDesire(t, &v1beta1.NodePool{
						Spec: v1beta1.NodePoolSpec{
							Replicas: ptr.To(int32(3)),
						},
					}),
				},
			},
			expectError: true,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)

				nodePool, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, nodePool.Properties.ProvisioningState)
				assert.Equal(t, testOperationName, nodePool.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:          "node pool ready but ReadDesire missing returns gate error",
			nodePoolState: string(NodePoolStateReady),
			nodePoolProps: &api.HCPOpenShiftClusterNodePoolProperties{
				ProvisioningState: arm.ProvisioningStateAccepted,
				Replicas:          3,
			},
			readDesireLister: internallistertesting.SliceReadDesireLister{},
			expectError:      true,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:          "node pool updating transitions to updating",
			nodePoolState: string(NodePoolStateUpdating),
			nodePoolProps: &api.HCPOpenShiftClusterNodePoolProperties{
				ProvisioningState: arm.ProvisioningStateAccepted,
				Replicas:          3,
			},
			readDesireLister: internallistertesting.SliceReadDesireLister{
				Desires: []*kubeapplier.ReadDesire{
					newNodePoolReadDesire(t, &v1beta1.NodePool{
						Spec: v1beta1.NodePoolSpec{
							Replicas: ptr.To(int32(3)),
						},
					}),
				},
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
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
			name:          "node pool validating_update stays accepted",
			nodePoolState: string(NodePoolStateValidatingUpdate),
			nodePoolProps: &api.HCPOpenShiftClusterNodePoolProperties{
				ProvisioningState: arm.ProvisioningStateAccepted,
				Replicas:          3,
			},
			readDesireLister: internallistertesting.SliceReadDesireLister{},
			expectError:      false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:          "node pool pending_update stays accepted",
			nodePoolState: string(NodePoolStatePendingUpdate),
			nodePoolProps: &api.HCPOpenShiftClusterNodePoolProperties{
				ProvisioningState: arm.ProvisioningStateAccepted,
				Replicas:          3,
			},
			readDesireLister: internallistertesting.SliceReadDesireLister{},
			expectError:      false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:          "node pool recoverable_error transitions to failed",
			nodePoolState: string(NodePoolStateRecoverableError),
			nodePoolMsg:   "temporary error occurred",
			nodePoolProps: &api.HCPOpenShiftClusterNodePoolProperties{
				ProvisioningState: arm.ProvisioningStateAccepted,
				Replicas:          3,
			},
			readDesireLister: internallistertesting.SliceReadDesireLister{},
			expectError:      false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *nodePoolTestFixture) {
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
			if tt.nodePoolProps != nil {
				nodePool.Properties = *tt.nodePoolProps
			}
			operation := fixture.newOperation(database.OperationRequestUpdate)

			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, nodePool, operation})
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
				clock:                utilsclock.RealClock{},
				resourcesDBClient:    mockResourcesDBClient,
				clusterServiceClient: mockCSClient,
				readDesireLister:     &tt.readDesireLister,
				notificationClient:   nil,
			}

			err = controller.SynchronizeOperation(ctx, fixture.operationKey())

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, ctx, mockResourcesDBClient, fixture)
			}
		})
	}
}
