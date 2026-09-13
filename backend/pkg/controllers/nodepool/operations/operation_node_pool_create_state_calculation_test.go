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

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	operationtesting "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// TestCreateHypershiftNodePoolOperationState covers the create controller's thin wrapper around the
// shared hypershiftNodePoolOperationState in operation_node_pool_state_calculation.go. The underlying
// comparison logic is identical to (and exhaustively covered by) the update controller's
// TestHypershiftNodePoolOperationState in operation_node_pool_update_state_calculation_test.go; this
// test only confirms create's wrapper plumbs ProvisioningStateProvisioning rather than
// ProvisioningStateUpdating for the not-yet-matching case, since "Updating" is not a valid state for a
// create operation.
func TestCreateHypershiftNodePoolOperationState(t *testing.T) {
	t.Parallel()

	fixture := operationtesting.NewNodePoolTestFixture()

	tests := []struct {
		name              string
		nodePool          *coreapi.HCPOpenShiftClusterNodePool
		csNodePool        *arohcpv1alpha1.NodePool
		readDesires       []*kubeapplierapi.ReadDesire
		wantState         coreapi.ProvisioningState
		wantMessageSubstr string
	}{
		{
			name:              "no ReadDesire returns Provisioning",
			nodePool:          fixture.NewNodePool(),
			csNodePool:        testCSNodePoolWithNodeDrainTimeout(t, 0),
			readDesires:       nil,
			wantState:         coreapi.ProvisioningStateProvisioning,
			wantMessageSubstr: "Hypershift NodePool has not been observed yet",
		},
		{
			name:       "empty node pool matches empty Hypershift NodePool",
			nodePool:   fixture.NewNodePool(),
			csNodePool: testCSNodePoolWithNodeDrainTimeout(t, 0),
			readDesires: []*kubeapplierapi.ReadDesire{
				newHypershiftNodePoolReadDesire(t, testNodePoolUpdateMatchingHypershiftNodePool(0)),
			},
			wantState: coreapi.ProvisioningStateSucceeded,
		},
		{
			name: "replicas mismatch returns Provisioning",
			nodePool: func() *coreapi.HCPOpenShiftClusterNodePool {
				np := fixture.NewNodePool()
				np.Properties.Replicas = 3
				return np
			}(),
			csNodePool: testCSNodePoolWithReplicasAndNodeDrainTimeout(t, 3, 0),
			readDesires: []*kubeapplierapi.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolUpdateMatchingHypershiftNodePool(0)
					np.Spec.Replicas = ptr.To(int32(1))
					np.Status.Replicas = 1
					return np
				}()),
			},
			wantState:         coreapi.ProvisioningStateProvisioning,
			wantMessageSubstr: "replicas is 1, want 3",
		},
		{
			name: "AllMachinesReady false returns Provisioning",
			nodePool: func() *coreapi.HCPOpenShiftClusterNodePool {
				np := fixture.NewNodePool()
				np.Properties.Replicas = 3
				return np
			}(),
			csNodePool: testCSNodePoolWithReplicasAndNodeDrainTimeout(t, 3, 0),
			readDesires: []*kubeapplierapi.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolUpdateMatchingHypershiftNodePool(0)
					np.Spec.Replicas = ptr.To(int32(3))
					np.Status.Replicas = 3
					np.Status.Conditions = []v1beta1.NodePoolCondition{
						{Type: v1beta1.NodePoolAllMachinesReadyConditionType, Status: corev1.ConditionFalse, Message: "waiting"},
					}
					return np
				}()),
			},
			wantState:         coreapi.ProvisioningStateProvisioning,
			wantMessageSubstr: "condition AllMachinesReady is False: waiting",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))

			controller := &operationNodePoolCreate{
				readDesireLister: &kubeapplierlistertesting.SliceReadDesireLister{
					Desires: tt.readDesires,
				},
			}

			state, err := controller.hypershiftNodePoolOperationState(ctx, tt.nodePool, tt.csNodePool)
			require.NoError(t, err)
			assert.Equal(t, tt.wantState, state.ProvisioningState)
			if tt.wantMessageSubstr != "" {
				assert.Contains(t, state.Message, tt.wantMessageSubstr)
			}
		})
	}
}
