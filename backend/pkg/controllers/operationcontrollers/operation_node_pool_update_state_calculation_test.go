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
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	internallistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestHypershiftNodePoolOperationState(t *testing.T) {
	t.Parallel()

	fixture := newNodePoolTestFixture()

	tests := []struct {
		name              string
		nodePool          *api.HCPOpenShiftClusterNodePool
		csNodePool        *arohcpv1alpha1.NodePool
		readDesires       []*kubeapplier.ReadDesire
		wantState         arm.ProvisioningState
		wantMessageSubstr string
	}{
		{
			name:              "no ReadDesire returns Updating",
			nodePool:          fixture.newNodePool(),
			csNodePool:        testCSNodePoolWithNodeDrainTimeout(t, 0),
			readDesires:       nil,
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "Hypershift NodePool has not been observed yet",
		},
		{
			name:       "empty node pool matches empty Hypershift NodePool",
			nodePool:   fixture.newNodePool(),
			csNodePool: testCSNodePoolWithNodeDrainTimeout(t, 0),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, testNodePoolUpdateMatchingHypershiftNodePool(0)),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "replicas mismatch returns Updating",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.Replicas = 3
				return np
			}(),
			csNodePool: testCSNodePoolWithReplicasAndNodeDrainTimeout(t, 3, 0),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolUpdateMatchingHypershiftNodePool(0)
					np.Spec.Replicas = ptr.To(int32(1))
					np.Status.Replicas = 1
					return np
				}()),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "replicas is 1, want 3",
		},
		{
			name: "replicas match returns Succeeded",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.Replicas = 3
				return np
			}(),
			csNodePool: testCSNodePoolWithReplicasAndNodeDrainTimeout(t, 3, 0),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolUpdateMatchingHypershiftNodePool(0)
					np.Spec.Replicas = ptr.To(int32(3))
					np.Status.Replicas = 3
					np.Status.Conditions = []v1beta1.NodePoolCondition{
						{Type: v1beta1.NodePoolAllMachinesReadyConditionType, Status: corev1.ConditionTrue},
					}
					return np
				}()),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "spec replicas mismatch while status already at desired returns Updating",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.Replicas = 3
				return np
			}(),
			csNodePool: testCSNodePoolWithNodeDrainTimeout(t, 0),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolUpdateMatchingHypershiftNodePool(0)
					np.Spec.Replicas = ptr.To(int32(1))
					np.Status.Replicas = 3
					return np
				}()),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "replicas is 1, want 3",
		},
		{
			name: "autoscaling mismatch returns Updating",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{Min: 1, Max: 5}
				return np
			}(),
			csNodePool: testCSNodePoolWithAutoscalingAndNodeDrainTimeout(t, 1, 5, 0),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolUpdateMatchingHypershiftNodePool(0)
					np.Spec.AutoScaling = &v1beta1.NodePoolAutoScaling{Min: ptr.To(int32(2)), Max: 5}
					np.Spec.Replicas = nil
					np.Status.Replicas = 2
					return np
				}()),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "autoscaling is min=2 max=5, want min=1 max=5",
		},
		{
			name: "autoscaling match returns Succeeded",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{Min: 1, Max: 5}
				return np
			}(),
			csNodePool: testCSNodePoolWithAutoscalingAndNodeDrainTimeout(t, 1, 5, 0),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolUpdateMatchingHypershiftNodePool(0)
					np.Spec.AutoScaling = &v1beta1.NodePoolAutoScaling{Min: ptr.To(int32(1)), Max: 5}
					np.Spec.Replicas = nil
					np.Status.Replicas = 1
					np.Status.Conditions = []v1beta1.NodePoolCondition{
						{Type: v1beta1.NodePoolAllMachinesReadyConditionType, Status: corev1.ConditionTrue},
					}
					return np
				}()),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "replicas desired but observed autoscaling set returns Updating",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.Replicas = 3
				return np
			}(),
			csNodePool: testCSNodePoolWithReplicasAndNodeDrainTimeout(t, 3, 0),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolUpdateMatchingHypershiftNodePool(0)
					np.Spec.AutoScaling = &v1beta1.NodePoolAutoScaling{Min: ptr.To(int32(1)), Max: 5}
					np.Spec.Replicas = nil
					np.Status.Replicas = 3
					return np
				}()),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "autoscaling is set (min=1 max=5), want replicas=3",
		},
		{
			name: "autoscaling desired but observed replicas set returns Updating",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{Min: 1, Max: 5}
				return np
			}(),
			csNodePool: testCSNodePoolWithAutoscalingAndNodeDrainTimeout(t, 1, 5, 0),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolUpdateMatchingHypershiftNodePool(0)
					np.Spec.Replicas = ptr.To(int32(3))
					np.Spec.AutoScaling = nil
					np.Status.Replicas = 3
					return np
				}()),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "autoscaling is unset, want min=1 max=5",
		},
		{
			name: "autoscaling spec match status below min returns Updating",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{Min: 1, Max: 5}
				return np
			}(),
			csNodePool: testCSNodePoolWithAutoscalingAndNodeDrainTimeout(t, 1, 5, 0),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolUpdateMatchingHypershiftNodePool(0)
					np.Spec.AutoScaling = &v1beta1.NodePoolAutoScaling{Min: ptr.To(int32(1)), Max: 5}
					np.Spec.Replicas = nil
					np.Status.Replicas = 0
					return np
				}()),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "status replicas is 0, want >= 1 (autoscaling min)",
		},
		{
			name: "autoscaling spec match status above max returns Updating",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.AutoScaling = &api.NodePoolAutoScaling{Min: 1, Max: 5}
				return np
			}(),
			csNodePool: testCSNodePoolWithAutoscalingAndNodeDrainTimeout(t, 1, 5, 0),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolUpdateMatchingHypershiftNodePool(0)
					np.Spec.AutoScaling = &v1beta1.NodePoolAutoScaling{Min: ptr.To(int32(1)), Max: 5}
					np.Spec.Replicas = nil
					np.Status.Replicas = 6
					return np
				}()),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "status replicas is 6, want <= 5 (autoscaling max)",
		},
		{
			name: "labels mismatch returns Updating",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.Labels = map[string]string{"env": "prod"}
				return np
			}(),
			csNodePool: testCSNodePoolWithNodeDrainTimeout(t, 0),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, testNodePoolUpdateMatchingHypershiftNodePool(0)),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "nodeLabels are map[], want at least map[env:prod]",
		},
		{
			name: "labels exact match returns Succeeded",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.Labels = map[string]string{"env": "prod"}
				return np
			}(),
			csNodePool: testCSNodePoolWithNodeDrainTimeout(t, 0),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolUpdateMatchingHypershiftNodePool(0)
					np.Spec.NodeLabels = map[string]string{"env": "prod"}
					return np
				}()),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "temporary: labels match with extra observed labels returns Succeeded",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.Labels = map[string]string{"env": "prod"}
				return np
			}(),
			csNodePool: testCSNodePoolWithNodeDrainTimeout(t, 0),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolUpdateMatchingHypershiftNodePool(0)
					np.Spec.NodeLabels = map[string]string{"env": "prod", "managed-by": "other"}
					return np
				}()),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "taints mismatch returns Updating",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.Taints = []api.Taint{
					{Effect: api.EffectNoSchedule, Key: "key1", Value: "val1"},
				}
				return np
			}(),
			csNodePool: testCSNodePoolWithNodeDrainTimeout(t, 0),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, testNodePoolUpdateMatchingHypershiftNodePool(0)),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "missing desired taint {effect:NoSchedule key:key1 value:val1}",
		},
		{
			name: "taints exact match returns Succeeded",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.Taints = []api.Taint{
					{Effect: api.EffectNoSchedule, Key: "key1", Value: "val1"},
				}
				return np
			}(),
			csNodePool: testCSNodePoolWithNodeDrainTimeout(t, 0),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolUpdateMatchingHypershiftNodePool(0)
					np.Spec.Taints = []v1beta1.Taint{
						{Effect: corev1.TaintEffectNoSchedule, Key: "key1", Value: "val1"},
					}
					return np
				}()),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "temporary: taints match with extra observed taints returns Succeeded",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.Taints = []api.Taint{
					{Effect: api.EffectNoSchedule, Key: "key1", Value: "val1"},
				}
				return np
			}(),
			csNodePool: testCSNodePoolWithNodeDrainTimeout(t, 0),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolUpdateMatchingHypershiftNodePool(0)
					np.Spec.Taints = []v1beta1.Taint{
						{Effect: corev1.TaintEffectNoSchedule, Key: "key1", Value: "val1"},
						{Effect: corev1.TaintEffectNoExecute, Key: "internal", Value: "true"},
					}
					return np
				}()),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "explicit node drain timeout mismatch returns Updating",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.NodeDrainTimeoutMinutes = ptr.To(int32(15))
				return np
			}(),
			csNodePool: testCSNodePoolWithNodeDrainTimeout(t, 15),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolUpdateMatchingHypershiftNodePool(15)
					np.Spec.NodeDrainTimeout = &metav1.Duration{Duration: 10 * time.Minute}
					return np
				}()),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "nodeDrainTimeout is 10m0s, want 15m0s",
		},
		{
			name:       "inherited node drain timeout from CS returns Succeeded",
			nodePool:   fixture.newNodePool(),
			csNodePool: testCSNodePoolWithNodeDrainTimeout(t, 4),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, testNodePoolUpdateMatchingHypershiftNodePool(4)),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name:       "cosmos unset but CS frozen value not yet on hypershift returns Updating",
			nodePool:   fixture.newNodePool(),
			csNodePool: testCSNodePoolWithNodeDrainTimeout(t, 4),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, testNodePoolUpdateMatchingHypershiftNodePool(0)),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "nodeDrainTimeout is unset, want 4m0s",
		},
		{
			name:       "cosmos unset CS frozen value mismatches hypershift returns Updating",
			nodePool:   fixture.newNodePool(),
			csNodePool: testCSNodePoolWithNodeDrainTimeout(t, 4),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, testNodePoolUpdateMatchingHypershiftNodePool(3)),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "nodeDrainTimeout is 3m0s, want 4m0s",
		},
		{
			name:       "cosmos unset and CS unset returns Updating",
			nodePool:   fixture.newNodePool(),
			csNodePool: testCSNodePool(t),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, testNodePoolUpdateMatchingHypershiftNodePool(0)),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "Cluster Service node pool node_drain_grace_period is unset, want inherited cluster default",
		},
		{
			name: "status replicas mismatch returns Updating",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.Replicas = 3
				return np
			}(),
			csNodePool: testCSNodePoolWithReplicasAndNodeDrainTimeout(t, 3, 0),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolUpdateMatchingHypershiftNodePool(0)
					np.Spec.Replicas = ptr.To(int32(3))
					np.Status.Replicas = 1
					return np
				}()),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "status replicas is 1, want 3",
		},
		{
			name:       "scale to zero spec match status replicas nonzero returns Updating",
			nodePool:   fixture.newNodePool(),
			csNodePool: testCSNodePoolWithNodeDrainTimeout(t, 0),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolUpdateMatchingHypershiftNodePool(0)
					np.Status.Replicas = 1
					return np
				}()),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "status replicas is 1, want 0",
		},
		{
			name: "AllMachinesReady false returns Updating",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.Replicas = 3
				return np
			}(),
			csNodePool: testCSNodePoolWithReplicasAndNodeDrainTimeout(t, 3, 0),
			readDesires: []*kubeapplier.ReadDesire{
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
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "condition AllMachinesReady is False: waiting",
		},
		{
			name:       "scaling to zero skips AllMachinesReady check",
			nodePool:   fixture.newNodePool(),
			csNodePool: testCSNodePoolWithNodeDrainTimeout(t, 0),
			readDesires: []*kubeapplier.ReadDesire{
				newHypershiftNodePoolReadDesire(t, testNodePoolUpdateMatchingHypershiftNodePool(0)),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))

			controller := &operationNodePoolUpdate{
				readDesireLister: &internallistertesting.SliceReadDesireLister{
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

func TestClusterServiceNodePoolSpecOperationState(t *testing.T) {
	t.Parallel()

	fixture := newNodePoolTestFixture()
	controller := &operationNodePoolUpdate{}

	tests := []struct {
		name              string
		nodePool          *api.HCPOpenShiftClusterNodePool
		csNodePool        *arohcpv1alpha1.NodePool
		wantState         arm.ProvisioningState
		wantMessageSubstr string
	}{
		{
			name:       "matching labels and taints returns Succeeded",
			nodePool:   fixture.newNodePool(),
			csNodePool: testCSNodePoolWithNodeDrainTimeout(t, 0),
			wantState:  arm.ProvisioningStateSucceeded,
		},
		{
			name: "matching labels returns Succeeded",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.Labels = map[string]string{"env": "prod"}
				return np
			}(),
			csNodePool: testCSNodePoolWithLabels(t, map[string]string{"env": "prod"}),
			wantState:  arm.ProvisioningStateSucceeded,
		},
		{
			name: "label mismatch returns Updating",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.Labels = map[string]string{"env": "prod"}
				return np
			}(),
			csNodePool:        testCSNodePoolWithLabels(t, map[string]string{"env": "staging"}),
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: `Cluster Service node pool labels are map[env:staging], want map[env:prod]`,
		},
		{
			name: "stale label on CS after removal returns Updating",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.Labels = nil
				return np
			}(),
			csNodePool:        testCSNodePoolWithLabels(t, map[string]string{"env": "prod"}),
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: `Cluster Service node pool labels are map[env:prod], want map[]`,
		},
		{
			name: "matching taints returns Succeeded",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.Taints = []api.Taint{
					{Effect: api.EffectNoSchedule, Key: "key1", Value: "val1"},
				}
				return np
			}(),
			csNodePool: testCSNodePoolWithTaints(t, arohcpv1alpha1.NewTaint().
				Effect(string(api.EffectNoSchedule)).
				Key("key1").
				Value("val1")),
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name: "taint mismatch returns Updating",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.Taints = []api.Taint{
					{Effect: api.EffectNoSchedule, Key: "key1", Value: "val1"},
				}
				return np
			}(),
			csNodePool:        testCSNodePool(t),
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: `Cluster Service node pool taints are [], want [{NoSchedule key1 val1}]`,
		},
		{
			name: "stale taint on CS after removal returns Updating",
			nodePool: func() *api.HCPOpenShiftClusterNodePool {
				np := fixture.newNodePool()
				np.Properties.Taints = nil
				return np
			}(),
			csNodePool: testCSNodePoolWithTaints(t, arohcpv1alpha1.NewTaint().
				Effect(string(api.EffectNoSchedule)).
				Key("key1").
				Value("val1")),
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: `Cluster Service node pool taints are [{NoSchedule key1 val1}], want []`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			state, err := controller.clusterServiceNodePoolSpecOperationState(tt.nodePool, tt.csNodePool)
			require.NoError(t, err)
			assert.Equal(t, tt.wantState, state.ProvisioningState)
			if tt.wantMessageSubstr != "" {
				assert.Contains(t, state.Message, tt.wantMessageSubstr)
			}
		})
	}
}

func TestHypershiftNodePoolLabelsSpecMatchesDesired(t *testing.T) {
	t.Parallel()

	controller := &operationNodePoolUpdate{}

	tests := []struct {
		name       string
		desired    map[string]string
		observed   map[string]string
		wantMatch  bool
		wantSubstr string
	}{
		{
			name:      "both nil",
			desired:   nil,
			observed:  nil,
			wantMatch: true,
		},
		{
			name:      "temporary: desired empty with extra observed labels",
			desired:   map[string]string{},
			observed:  map[string]string{"managed-by": "other"},
			wantMatch: true,
		},
		{
			name:       "desired label missing on observed",
			desired:    map[string]string{"env": "prod"},
			observed:   nil,
			wantMatch:  false,
			wantSubstr: "nodeLabels are map[], want at least map[env:prod]",
		},
		{
			name:       "desired label value mismatch",
			desired:    map[string]string{"env": "prod"},
			observed:   map[string]string{"env": "staging"},
			wantMatch:  false,
			wantSubstr: "nodeLabels are map[env:staging], want at least map[env:prod]",
		},
		{
			name:      "desired labels match observed exactly",
			desired:   map[string]string{"env": "prod"},
			observed:  map[string]string{"env": "prod"},
			wantMatch: true,
		},
		{
			name:      "temporary:desired subset present with extra observed labels",
			desired:   map[string]string{"env": "prod"},
			observed:  map[string]string{"env": "prod", "zone": "east"},
			wantMatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			match, msg := controller.hypershiftNodePoolLabelsSpecMatchesDesired(tt.desired, tt.observed)
			assert.Equal(t, tt.wantMatch, match)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}

func TestHypershiftNodePoolReplicasOrAutoscalingSpecMatchesDesired(t *testing.T) {
	t.Parallel()

	controller := &operationNodePoolUpdate{}

	tests := []struct {
		name       string
		desired    *api.HCPOpenShiftClusterNodePool
		observed   v1beta1.NodePoolSpec
		wantMatch  bool
		wantSubstr string
	}{
		{
			name: "replicas match",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{Replicas: 3},
			},
			observed:  v1beta1.NodePoolSpec{Replicas: ptr.To(int32(3))},
			wantMatch: true,
		},
		{
			name: "replicas mismatch",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{Replicas: 3},
			},
			observed:   v1beta1.NodePoolSpec{Replicas: ptr.To(int32(1))},
			wantMatch:  false,
			wantSubstr: "replicas is 1, want 3",
		},
		{
			name: "replicas desired observed unset",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{Replicas: 3},
			},
			observed:   v1beta1.NodePoolSpec{},
			wantMatch:  false,
			wantSubstr: "replicas is 0, want 3",
		},
		{
			name: "autoscaling match",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					AutoScaling: &api.NodePoolAutoScaling{Min: 1, Max: 5},
				},
			},
			observed: v1beta1.NodePoolSpec{
				AutoScaling: &v1beta1.NodePoolAutoScaling{Min: ptr.To(int32(1)), Max: 5},
			},
			wantMatch: true,
		},
		{
			name: "autoscaling desired but observed unset",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					AutoScaling: &api.NodePoolAutoScaling{Min: 1, Max: 5},
				},
			},
			observed:   v1beta1.NodePoolSpec{},
			wantMatch:  false,
			wantSubstr: "autoscaling is unset, want min=1 max=5",
		},
		{
			name: "autoscaling mismatch",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					AutoScaling: &api.NodePoolAutoScaling{Min: 1, Max: 5},
				},
			},
			observed: v1beta1.NodePoolSpec{
				AutoScaling: &v1beta1.NodePoolAutoScaling{Min: ptr.To(int32(2)), Max: 5},
			},
			wantMatch:  false,
			wantSubstr: "autoscaling is min=2 max=5, want min=1 max=5",
		},
		{
			name: "autoscaling desired but observed replicas set",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					AutoScaling: &api.NodePoolAutoScaling{Min: 1, Max: 5},
				},
			},
			observed: v1beta1.NodePoolSpec{
				Replicas: ptr.To(int32(3)),
			},
			wantMatch:  false,
			wantSubstr: "autoscaling is unset, want min=1 max=5",
		},
		{
			name: "replicas desired but observed autoscaling set",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{Replicas: 3},
			},
			observed: v1beta1.NodePoolSpec{
				AutoScaling: &v1beta1.NodePoolAutoScaling{Min: ptr.To(int32(1)), Max: 5},
			},
			wantMatch:  false,
			wantSubstr: "autoscaling is set (min=1 max=5), want replicas=3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			match, msg := controller.hypershiftNodePoolReplicasOrAutoscalingSpecMatchesDesired(tt.desired, tt.observed)
			assert.Equal(t, tt.wantMatch, match)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}

func TestHypershiftNodePoolTaintsSpecMatchesDesired(t *testing.T) {
	t.Parallel()

	controller := &operationNodePoolUpdate{}

	tests := []struct {
		name       string
		desired    []api.Taint
		observed   []v1beta1.Taint
		wantMatch  bool
		wantSubstr string
	}{
		{
			name:      "both nil",
			desired:   nil,
			observed:  nil,
			wantMatch: true,
		},
		{
			name:      "temporary: desired empty with extra observed taints",
			desired:   []api.Taint{},
			observed:  []v1beta1.Taint{{Effect: corev1.TaintEffectNoSchedule, Key: "internal", Value: "true"}},
			wantMatch: true,
		},
		{
			name: "desired taint missing on observed",
			desired: []api.Taint{
				{Effect: api.EffectNoSchedule, Key: "key1", Value: "val1"},
			},
			observed:   nil,
			wantMatch:  false,
			wantSubstr: "missing desired taint {effect:NoSchedule key:key1 value:val1}",
		},
		{
			name: "desired taint effect mismatch",
			desired: []api.Taint{
				{Effect: api.EffectNoSchedule, Key: "key1", Value: "val1"},
			},
			observed: []v1beta1.Taint{
				{Effect: corev1.TaintEffectNoExecute, Key: "key1", Value: "val1"},
			},
			wantMatch:  false,
			wantSubstr: "missing desired taint {effect:NoSchedule key:key1 value:val1}",
		},
		{
			name: "desired taints match observed exactly",
			desired: []api.Taint{
				{Effect: api.EffectNoSchedule, Key: "key1", Value: "val1"},
			},
			observed: []v1beta1.Taint{
				{Effect: corev1.TaintEffectNoSchedule, Key: "key1", Value: "val1"},
			},
			wantMatch: true,
		},
		{
			name: "temporary: desired subset present with extra observed taints",
			desired: []api.Taint{
				{Effect: api.EffectNoSchedule, Key: "key1", Value: "val1"},
			},
			observed: []v1beta1.Taint{
				{Effect: corev1.TaintEffectNoSchedule, Key: "key1", Value: "val1"},
				{Effect: corev1.TaintEffectNoExecute, Key: "internal", Value: "true"},
			},
			wantMatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			match, msg := controller.hypershiftNodePoolTaintsSpecMatchesDesired(tt.desired, tt.observed)
			assert.Equal(t, tt.wantMatch, match)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}

func TestHypershiftNodePoolNodeDrainTimeoutSpecMatchesDesired(t *testing.T) {
	t.Parallel()

	controller := &operationNodePoolUpdate{}

	baseDesired := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{},
	}

	tests := []struct {
		name       string
		desired    *api.HCPOpenShiftClusterNodePool
		cs         func(t *testing.T) *arohcpv1alpha1.NodePool
		observed   *metav1.Duration
		wantMatch  bool
		wantSubstr string
	}{
		{
			name:    "cosmos unset and CS unset does not match",
			desired: baseDesired,
			cs: func(t *testing.T) *arohcpv1alpha1.NodePool {
				return testCSNodePool(t)
			},
			wantMatch:  false,
			wantSubstr: "Cluster Service node pool node_drain_grace_period is unset, want inherited cluster default",
		},
		{
			name: "cosmos explicit zero observed nil",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					NodeDrainTimeoutMinutes: ptr.To(int32(0)),
				},
			},
			cs: func(t *testing.T) *arohcpv1alpha1.NodePool {
				return testCSNodePoolWithReplicas(t, 3)
			},
			wantMatch: true,
		},
		{
			name: "cosmos explicit zero observed zero duration",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					NodeDrainTimeoutMinutes: ptr.To(int32(0)),
				},
			},
			cs: func(t *testing.T) *arohcpv1alpha1.NodePool {
				return testCSNodePoolWithReplicas(t, 3)
			},
			observed:   &metav1.Duration{Duration: 0},
			wantMatch:  false,
			wantSubstr: "unexpected hypershift NodePool nodeDrainTimeout set to an explicit zero duration, want 0s",
		},
		{
			name: "cosmos explicit zero observed nonzero duration",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					NodeDrainTimeoutMinutes: ptr.To(int32(0)),
				},
			},
			cs: func(t *testing.T) *arohcpv1alpha1.NodePool {
				return testCSNodePoolWithReplicas(t, 3)
			},
			observed:   &metav1.Duration{Duration: 4 * time.Minute},
			wantMatch:  false,
			wantSubstr: "nodeDrainTimeout is 4m0s, want unset",
		},
		{
			name: "cosmos explicit nonzero observed nil",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					NodeDrainTimeoutMinutes: ptr.To(int32(15)),
				},
			},
			cs: func(t *testing.T) *arohcpv1alpha1.NodePool {
				return testCSNodePoolWithReplicas(t, 3)
			},
			wantMatch:  false,
			wantSubstr: "hypershift NodePool nodeDrainTimeout is unset, want 15m0s",
		},
		{
			name:    "cosmos unset uses CS frozen value",
			desired: baseDesired,
			cs: func(t *testing.T) *arohcpv1alpha1.NodePool {
				return testCSNodePoolWithNodeDrainTimeout(t, 4)
			},
			observed:  &metav1.Duration{Duration: 4 * time.Minute},
			wantMatch: true,
		},
		{
			name:    "cosmos unset but CS frozen value is not yet on Hypershift",
			desired: baseDesired,
			cs: func(t *testing.T) *arohcpv1alpha1.NodePool {
				return testCSNodePoolWithNodeDrainTimeout(t, 4)
			},
			wantMatch:  false,
			wantSubstr: "hypershift NodePool nodeDrainTimeout is unset, want 4m0s",
		},
		{
			name:    "cosmos unset but CS frozen value mismatches hypershift",
			desired: baseDesired,
			cs: func(t *testing.T) *arohcpv1alpha1.NodePool {
				return testCSNodePoolWithNodeDrainTimeout(t, 4)
			},
			observed:   &metav1.Duration{Duration: 3 * time.Minute},
			wantMatch:  false,
			wantSubstr: "nodeDrainTimeout is 3m0s, want 4m0s",
		},
		{
			name:    "cosmos unset and CS frozen zero observed nil matches",
			desired: baseDesired,
			cs: func(t *testing.T) *arohcpv1alpha1.NodePool {
				return testCSNodePoolWithNodeDrainTimeout(t, 0)
			},
			wantMatch: true,
		},
		{
			name:    "cosmos unset and CS frozen zero but observed nonzero duration",
			desired: baseDesired,
			cs: func(t *testing.T) *arohcpv1alpha1.NodePool {
				return testCSNodePoolWithNodeDrainTimeout(t, 0)
			},
			observed:   &metav1.Duration{Duration: 4 * time.Minute},
			wantMatch:  false,
			wantSubstr: "nodeDrainTimeout is 4m0s, want unset",
		},
		{
			name:    "cosmos unset and CS frozen zero but observed explicit zero duration",
			desired: baseDesired,
			cs: func(t *testing.T) *arohcpv1alpha1.NodePool {
				return testCSNodePoolWithNodeDrainTimeout(t, 0)
			},
			observed:   &metav1.Duration{Duration: 0},
			wantMatch:  false,
			wantSubstr: "unexpected hypershift NodePool nodeDrainTimeout set to an explicit zero duration, want 0s",
		},
		{
			name:    "cosmos unset and CS frozen has non zero value but observed hypershift has explicit zero duration",
			desired: baseDesired,
			cs: func(t *testing.T) *arohcpv1alpha1.NodePool {
				return testCSNodePoolWithNodeDrainTimeout(t, 4)
			},
			observed:   &metav1.Duration{Duration: 0},
			wantMatch:  false,
			wantSubstr: "unexpected hypershift NodePool nodeDrainTimeout set to an explicit zero duration, want 4m0s",
		},
		{
			name: "cosmos explicit mismatches observed",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					NodeDrainTimeoutMinutes: ptr.To(int32(4)),
				},
			},
			cs: func(t *testing.T) *arohcpv1alpha1.NodePool {
				return testCSNodePoolWithNodeDrainTimeout(t, 4)
			},
			observed:   &metav1.Duration{Duration: 3 * time.Minute},
			wantMatch:  false,
			wantSubstr: "nodeDrainTimeout is 3m0s, want 4m0s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			match, msg := controller.hypershiftNodePoolNodeDrainTimeoutSpecMatchesDesired(tt.desired, tt.cs(t), tt.observed)
			assert.Equal(t, tt.wantMatch, match)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}

func TestHypershiftNodePoolStatusReplicasMatchesDesired(t *testing.T) {
	t.Parallel()

	controller := &operationNodePoolUpdate{}

	tests := []struct {
		name             string
		desired          *api.HCPOpenShiftClusterNodePool
		observedReplicas int32
		wantMatch        bool
		wantSubstr       string
	}{
		{
			name: "fixed replicas match",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{Replicas: 3},
			},
			observedReplicas: 3,
			wantMatch:        true,
		},
		{
			name: "fixed replicas mismatch",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{Replicas: 3},
			},
			observedReplicas: 1,
			wantMatch:        false,
			wantSubstr:       "status replicas is 1, want 3",
		},
		{
			name: "autoscaling within range",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					AutoScaling: &api.NodePoolAutoScaling{Min: 1, Max: 5},
				},
			},
			observedReplicas: 3,
			wantMatch:        true,
		},
		{
			name: "autoscaling below min",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					AutoScaling: &api.NodePoolAutoScaling{Min: 2, Max: 5},
				},
			},
			observedReplicas: 1,
			wantMatch:        false,
			wantSubstr:       "status replicas is 1, want >= 2 (autoscaling min)",
		},
		{
			name: "autoscaling above max",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					AutoScaling: &api.NodePoolAutoScaling{Min: 1, Max: 5},
				},
			},
			observedReplicas: 6,
			wantMatch:        false,
			wantSubstr:       "status replicas is 6, want <= 5 (autoscaling max)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			match, msg := controller.hypershiftNodePoolStatusReplicasMatchesDesired(tt.desired, tt.observedReplicas)
			assert.Equal(t, tt.wantMatch, match)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}

func TestHypershiftNodePoolAllMachinesReadyConditionStatusMatchesDesired(t *testing.T) {
	t.Parallel()

	controller := &operationNodePoolUpdate{}

	tests := []struct {
		name       string
		conditions []v1beta1.NodePoolCondition
		wantMatch  bool
		wantSubstr string
	}{
		{
			name: "condition true",
			conditions: []v1beta1.NodePoolCondition{
				{Type: v1beta1.NodePoolAllMachinesReadyConditionType, Status: corev1.ConditionTrue},
			},
			wantMatch: true,
		},
		{
			name: "condition false",
			conditions: []v1beta1.NodePoolCondition{
				{Type: v1beta1.NodePoolAllMachinesReadyConditionType, Status: corev1.ConditionFalse, Message: "waiting"},
			},
			wantMatch:  false,
			wantSubstr: "condition AllMachinesReady is False: waiting",
		},
		{
			name:       "condition not yet reported",
			conditions: nil,
			wantMatch:  false,
			wantSubstr: "condition AllMachinesReady not yet reported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			match, msg := controller.hypershiftNodePoolAllMachinesReadyConditionStatusMatchesDesired(tt.conditions)
			assert.Equal(t, tt.wantMatch, match)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}

func TestHypershiftNodePoolStatusMatchesDesired(t *testing.T) {
	t.Parallel()

	controller := &operationNodePoolUpdate{}

	tests := []struct {
		name       string
		desired    *api.HCPOpenShiftClusterNodePool
		observed   v1beta1.NodePoolStatus
		wantMatch  bool
		wantSubstr string
	}{
		{
			name: "fixed replicas with ready machines",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{Replicas: 3},
			},
			observed: v1beta1.NodePoolStatus{
				Replicas: 3,
				Conditions: []v1beta1.NodePoolCondition{
					{Type: v1beta1.NodePoolAllMachinesReadyConditionType, Status: corev1.ConditionTrue},
				},
			},
			wantMatch: true,
		},
		{
			name: "scaling to zero skips AllMachinesReady",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{Replicas: 0},
			},
			observed: v1beta1.NodePoolStatus{
				Replicas: 0,
			},
			wantMatch: true,
		},
		{
			name: "autoscaling at min with ready machines returns match",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					AutoScaling: &api.NodePoolAutoScaling{Min: 1, Max: 5},
				},
			},
			observed: v1beta1.NodePoolStatus{
				Replicas: 1,
				Conditions: []v1beta1.NodePoolCondition{
					{Type: v1beta1.NodePoolAllMachinesReadyConditionType, Status: corev1.ConditionTrue},
				},
			},
			wantMatch: true,
		},
		{
			name: "autoscaling in range but AllMachinesReady false returns mismatch",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					AutoScaling: &api.NodePoolAutoScaling{Min: 1, Max: 5},
				},
			},
			observed: v1beta1.NodePoolStatus{
				Replicas: 3,
				Conditions: []v1beta1.NodePoolCondition{
					{Type: v1beta1.NodePoolAllMachinesReadyConditionType, Status: corev1.ConditionFalse, Message: "waiting"},
				},
			},
			wantMatch:  false,
			wantSubstr: "condition AllMachinesReady is False: waiting",
		},
		{
			name: "autoscaling in range but AllMachinesReady not reported returns mismatch",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					AutoScaling: &api.NodePoolAutoScaling{Min: 1, Max: 5},
				},
			},
			observed: v1beta1.NodePoolStatus{
				Replicas: 3,
			},
			wantMatch:  false,
			wantSubstr: "condition AllMachinesReady not yet reported",
		},
		{
			name: "autoscaling status below min",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					AutoScaling: &api.NodePoolAutoScaling{Min: 2, Max: 5},
				},
			},
			observed: v1beta1.NodePoolStatus{
				Replicas: 1,
			},
			wantMatch:  false,
			wantSubstr: "status replicas is 1, want >= 2 (autoscaling min)",
		},
		{
			name: "autoscaling status above max",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					AutoScaling: &api.NodePoolAutoScaling{Min: 1, Max: 5},
				},
			},
			observed: v1beta1.NodePoolStatus{
				Replicas: 6,
			},
			wantMatch:  false,
			wantSubstr: "status replicas is 6, want <= 5 (autoscaling max)",
		},
		{
			name: "replicas mismatch fails before AllMachinesReady",
			desired: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{Replicas: 3},
			},
			observed: v1beta1.NodePoolStatus{
				Replicas: 1,
				Conditions: []v1beta1.NodePoolCondition{
					{Type: v1beta1.NodePoolAllMachinesReadyConditionType, Status: corev1.ConditionTrue},
				},
			},
			wantMatch:  false,
			wantSubstr: "status replicas is 1, want 3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			match, msg := controller.hypershiftNodePoolStatusMatchesDesired(tt.desired, tt.observed)
			assert.Equal(t, tt.wantMatch, match)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}

// testNodePoolUpdateMatchingHypershiftNodePool returns a Hypershift NodePool that
// matches the default node pool fixture for node pool update state calculation tests.
func testNodePoolUpdateMatchingHypershiftNodePool(nodeDrainTimeoutMinutes int32) *v1beta1.NodePool {
	spec := v1beta1.NodePoolSpec{
		Replicas: ptr.To(int32(0)),
	}
	if nodeDrainTimeoutMinutes != 0 {
		spec.NodeDrainTimeout = &metav1.Duration{Duration: time.Duration(nodeDrainTimeoutMinutes) * time.Minute}
	}
	return &v1beta1.NodePool{
		Spec: spec,
		Status: v1beta1.NodePoolStatus{
			Replicas: 0,
		},
	}
}

func newHypershiftNodePoolReadDesire(t *testing.T, nodePool *v1beta1.NodePool) *kubeapplier.ReadDesire {
	t.Helper()
	raw, err := json.Marshal(nodePool)
	require.NoError(t, err)

	resourceID := api.Must(azcorearm.ParseResourceID(
		kubeapplier.ToNodePoolScopedReadDesireResourceIDString(
			testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName,
			kubeapplierhelpers.ReadDesireNameReadonlyNodePool)))

	return &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		Status: kubeapplier.ReadDesireStatus{
			Conditions: []metav1.Condition{
				{Type: kubeapplier.ConditionTypeSuccessful, Status: metav1.ConditionTrue, Reason: kubeapplier.ConditionReasonNoErrors},
			},
			KubeContent: &kruntime.RawExtension{Raw: raw},
		},
	}
}

func testCSNodePool(t *testing.T) *arohcpv1alpha1.NodePool {
	t.Helper()
	csNodePool, err := arohcpv1alpha1.NewNodePool().Replicas(0).Build()
	require.NoError(t, err)
	return csNodePool
}

func testCSNodePoolWithReplicas(t *testing.T, replicas int) *arohcpv1alpha1.NodePool {
	t.Helper()
	csNodePool, err := arohcpv1alpha1.NewNodePool().Replicas(replicas).Build()
	require.NoError(t, err)
	return csNodePool
}

func testCSNodePoolWithNodeDrainTimeout(t *testing.T, minutes int32) *arohcpv1alpha1.NodePool {
	t.Helper()
	csNodePool, err := arohcpv1alpha1.NewNodePool().
		Replicas(0).
		NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
			Unit("minutes").
			Value(float64(minutes))).
		Build()
	require.NoError(t, err)
	return csNodePool
}

func testCSNodePoolWithReplicasAndNodeDrainTimeout(t *testing.T, replicas int, minutes int32) *arohcpv1alpha1.NodePool {
	t.Helper()
	csNodePool, err := arohcpv1alpha1.NewNodePool().
		Replicas(replicas).
		NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
			Unit("minutes").
			Value(float64(minutes))).
		Build()
	require.NoError(t, err)
	return csNodePool
}

func testCSNodePoolWithAutoscalingAndNodeDrainTimeout(t *testing.T, min, max int, minutes int32) *arohcpv1alpha1.NodePool {
	t.Helper()
	csNodePool, err := arohcpv1alpha1.NewNodePool().
		Autoscaling(arohcpv1alpha1.NewNodePoolAutoscaling().
			MinReplica(min).
			MaxReplica(max)).
		NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
			Unit("minutes").
			Value(float64(minutes))).
		Build()
	require.NoError(t, err)
	return csNodePool
}

func testCSNodePoolWithLabels(t *testing.T, labels map[string]string) *arohcpv1alpha1.NodePool {
	t.Helper()
	csNodePool, err := arohcpv1alpha1.NewNodePool().
		Replicas(0).
		Labels(labels).
		Build()
	require.NoError(t, err)
	return csNodePool
}

func testCSNodePoolWithTaints(t *testing.T, taints ...*arohcpv1alpha1.TaintBuilder) *arohcpv1alpha1.NodePool {
	t.Helper()
	builder := arohcpv1alpha1.NewNodePool().Replicas(0)
	if len(taints) > 0 {
		builder.Taints(taints...)
	}
	csNodePool, err := builder.Build()
	require.NoError(t, err)
	return csNodePool
}
