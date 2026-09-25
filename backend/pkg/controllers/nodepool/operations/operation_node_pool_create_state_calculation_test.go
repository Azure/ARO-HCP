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

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	operationtesting "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestCreateHypershiftNodePoolOperationState(t *testing.T) {
	t.Parallel()

	fixture := operationtesting.NewNodePoolTestFixture()

	tests := []struct {
		name              string
		nodePool          *coreapi.NodePool
		readDesires       []*kubeapplierapi.ReadDesire
		wantState         coreapi.ProvisioningState
		wantMessageSubstr string
	}{
		{
			name:              "no ReadDesire returns Provisioning",
			nodePool:          fixture.NewNodePool(),
			readDesires:       nil,
			wantState:         coreapi.ProvisioningStateProvisioning,
			wantMessageSubstr: "hypershift node pool not cached yet",
		},
		{
			name:     "empty node pool matches empty Hypershift NodePool",
			nodePool: fixture.NewNodePool(),
			readDesires: []*kubeapplierapi.ReadDesire{
				newHypershiftNodePoolReadDesire(t, testNodePoolCreateMatchingHypershiftNodePool()),
			},
			wantState: coreapi.ProvisioningStateSucceeded,
		},
		{
			name: "replicas mismatch returns Provisioning",
			nodePool: func() *coreapi.NodePool {
				np := fixture.NewNodePool()
				np.Properties.Replicas = 3
				return np
			}(),
			readDesires: []*kubeapplierapi.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolCreateMatchingHypershiftNodePool()
					np.Spec.Replicas = ptr.To(int32(1))
					np.Status.Replicas = 1
					return np
				}()),
			},
			wantState:         coreapi.ProvisioningStateProvisioning,
			wantMessageSubstr: "replicas is 1, want 3",
		},
		{
			name: "replicas match returns Succeeded",
			nodePool: func() *coreapi.NodePool {
				np := fixture.NewNodePool()
				np.Properties.Replicas = 3
				return np
			}(),
			readDesires: []*kubeapplierapi.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolCreateMatchingHypershiftNodePool()
					np.Spec.Replicas = ptr.To(int32(3))
					np.Status.Replicas = 3
					np.Status.Conditions = []v1beta1.NodePoolCondition{
						{Type: v1beta1.NodePoolAllNodesHealthyConditionType, Status: corev1.ConditionTrue},
						{Type: v1beta1.NodePoolAllMachinesReadyConditionType, Status: corev1.ConditionTrue},
					}
					return np
				}()),
			},
			wantState: coreapi.ProvisioningStateSucceeded,
		},
		{
			name: "spec replicas mismatch while status already at desired returns Provisioning",
			nodePool: func() *coreapi.NodePool {
				np := fixture.NewNodePool()
				np.Properties.Replicas = 3
				return np
			}(),
			readDesires: []*kubeapplierapi.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolCreateMatchingHypershiftNodePool()
					np.Spec.Replicas = ptr.To(int32(1))
					np.Status.Replicas = 3
					return np
				}()),
			},
			wantState:         coreapi.ProvisioningStateProvisioning,
			wantMessageSubstr: "replicas is 1, want 3",
		},
		{
			name: "autoscaling mismatch returns Provisioning",
			nodePool: func() *coreapi.NodePool {
				np := fixture.NewNodePool()
				np.Properties.AutoScaling = &coreapi.NodePoolAutoScaling{Min: 1, Max: 5}
				return np
			}(),
			readDesires: []*kubeapplierapi.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolCreateMatchingHypershiftNodePool()
					np.Spec.AutoScaling = &v1beta1.NodePoolAutoScaling{Min: ptr.To(int32(2)), Max: 5}
					np.Spec.Replicas = nil
					np.Status.Replicas = 2
					return np
				}()),
			},
			wantState:         coreapi.ProvisioningStateProvisioning,
			wantMessageSubstr: "autoscaling is min=2 max=5, want min=1 max=5",
		},
		{
			name: "autoscaling match returns Succeeded",
			nodePool: func() *coreapi.NodePool {
				np := fixture.NewNodePool()
				np.Properties.AutoScaling = &coreapi.NodePoolAutoScaling{Min: 1, Max: 5}
				return np
			}(),
			readDesires: []*kubeapplierapi.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolCreateMatchingHypershiftNodePool()
					np.Spec.AutoScaling = &v1beta1.NodePoolAutoScaling{Min: ptr.To(int32(1)), Max: 5}
					np.Spec.Replicas = nil
					np.Status.Replicas = 1
					np.Status.Conditions = []v1beta1.NodePoolCondition{
						{Type: v1beta1.NodePoolAllNodesHealthyConditionType, Status: corev1.ConditionTrue},
						{Type: v1beta1.NodePoolAllMachinesReadyConditionType, Status: corev1.ConditionTrue},
					}
					return np
				}()),
			},
			wantState: coreapi.ProvisioningStateSucceeded,
		},
		{
			name: "replicas desired but observed autoscaling set returns Provisioning",
			nodePool: func() *coreapi.NodePool {
				np := fixture.NewNodePool()
				np.Properties.Replicas = 3
				return np
			}(),
			readDesires: []*kubeapplierapi.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolCreateMatchingHypershiftNodePool()
					np.Spec.AutoScaling = &v1beta1.NodePoolAutoScaling{Min: ptr.To(int32(1)), Max: 5}
					np.Spec.Replicas = nil
					np.Status.Replicas = 3
					return np
				}()),
			},
			wantState:         coreapi.ProvisioningStateProvisioning,
			wantMessageSubstr: "autoscaling is set (min=1 max=5), want replicas=3",
		},
		{
			name: "autoscaling desired but observed replicas set returns Provisioning",
			nodePool: func() *coreapi.NodePool {
				np := fixture.NewNodePool()
				np.Properties.AutoScaling = &coreapi.NodePoolAutoScaling{Min: 1, Max: 5}
				return np
			}(),
			readDesires: []*kubeapplierapi.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolCreateMatchingHypershiftNodePool()
					np.Spec.Replicas = ptr.To(int32(3))
					np.Spec.AutoScaling = nil
					np.Status.Replicas = 3
					return np
				}()),
			},
			wantState:         coreapi.ProvisioningStateProvisioning,
			wantMessageSubstr: "autoscaling is unset, want min=1 max=5",
		},
		{
			name: "autoscaling spec match status below min returns Provisioning",
			nodePool: func() *coreapi.NodePool {
				np := fixture.NewNodePool()
				np.Properties.AutoScaling = &coreapi.NodePoolAutoScaling{Min: 1, Max: 5}
				return np
			}(),
			readDesires: []*kubeapplierapi.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolCreateMatchingHypershiftNodePool()
					np.Spec.AutoScaling = &v1beta1.NodePoolAutoScaling{Min: ptr.To(int32(1)), Max: 5}
					np.Spec.Replicas = nil
					np.Status.Replicas = 0
					return np
				}()),
			},
			wantState:         coreapi.ProvisioningStateProvisioning,
			wantMessageSubstr: "status replicas is 0, want >= 1 (autoscaling min)",
		},
		{
			name: "autoscaling spec match status above max returns Provisioning",
			nodePool: func() *coreapi.NodePool {
				np := fixture.NewNodePool()
				np.Properties.AutoScaling = &coreapi.NodePoolAutoScaling{Min: 1, Max: 5}
				return np
			}(),
			readDesires: []*kubeapplierapi.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolCreateMatchingHypershiftNodePool()
					np.Spec.AutoScaling = &v1beta1.NodePoolAutoScaling{Min: ptr.To(int32(1)), Max: 5}
					np.Spec.Replicas = nil
					np.Status.Replicas = 6
					return np
				}()),
			},
			wantState:         coreapi.ProvisioningStateProvisioning,
			wantMessageSubstr: "status replicas is 6, want <= 5 (autoscaling max)",
		},
		{
			name: "labels mismatch returns Provisioning",
			nodePool: func() *coreapi.NodePool {
				np := fixture.NewNodePool()
				np.Properties.Labels = map[string]string{"env": "prod"}
				return np
			}(),
			readDesires: []*kubeapplierapi.ReadDesire{
				newHypershiftNodePoolReadDesire(t, testNodePoolCreateMatchingHypershiftNodePool()),
			},
			wantState:         coreapi.ProvisioningStateProvisioning,
			wantMessageSubstr: "nodeLabels are map[], want at least map[env:prod]",
		},
		{
			name: "labels exact match returns Succeeded",
			nodePool: func() *coreapi.NodePool {
				np := fixture.NewNodePool()
				np.Properties.Labels = map[string]string{"env": "prod"}
				return np
			}(),
			readDesires: []*kubeapplierapi.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolCreateMatchingHypershiftNodePool()
					np.Spec.NodeLabels = map[string]string{"env": "prod"}
					return np
				}()),
			},
			wantState: coreapi.ProvisioningStateSucceeded,
		},
		{
			name: "labels match with extra observed labels returns Succeeded",
			nodePool: func() *coreapi.NodePool {
				np := fixture.NewNodePool()
				np.Properties.Labels = map[string]string{"env": "prod"}
				return np
			}(),
			readDesires: []*kubeapplierapi.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolCreateMatchingHypershiftNodePool()
					np.Spec.NodeLabels = map[string]string{"env": "prod", "managed-by": "other"}
					return np
				}()),
			},
			wantState: coreapi.ProvisioningStateSucceeded,
		},
		{
			name: "taints mismatch returns Provisioning",
			nodePool: func() *coreapi.NodePool {
				np := fixture.NewNodePool()
				np.Properties.Taints = []coreapi.Taint{
					{Effect: metadataapi.EffectNoSchedule, Key: "key1", Value: "val1"},
				}
				return np
			}(),
			readDesires: []*kubeapplierapi.ReadDesire{
				newHypershiftNodePoolReadDesire(t, testNodePoolCreateMatchingHypershiftNodePool()),
			},
			wantState:         coreapi.ProvisioningStateProvisioning,
			wantMessageSubstr: "missing desired taint {effect:NoSchedule key:key1 value:val1}",
		},
		{
			name: "taints exact match returns Succeeded",
			nodePool: func() *coreapi.NodePool {
				np := fixture.NewNodePool()
				np.Properties.Taints = []coreapi.Taint{
					{Effect: metadataapi.EffectNoSchedule, Key: "key1", Value: "val1"},
				}
				return np
			}(),
			readDesires: []*kubeapplierapi.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolCreateMatchingHypershiftNodePool()
					np.Spec.Taints = []v1beta1.Taint{
						{Effect: corev1.TaintEffectNoSchedule, Key: "key1", Value: "val1"},
					}
					return np
				}()),
			},
			wantState: coreapi.ProvisioningStateSucceeded,
		},
		{
			name: "taints match with extra observed taints returns Succeeded",
			nodePool: func() *coreapi.NodePool {
				np := fixture.NewNodePool()
				np.Properties.Taints = []coreapi.Taint{
					{Effect: metadataapi.EffectNoSchedule, Key: "key1", Value: "val1"},
				}
				return np
			}(),
			readDesires: []*kubeapplierapi.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolCreateMatchingHypershiftNodePool()
					np.Spec.Taints = []v1beta1.Taint{
						{Effect: corev1.TaintEffectNoSchedule, Key: "key1", Value: "val1"},
						{Effect: corev1.TaintEffectNoExecute, Key: "internal", Value: "true"},
					}
					return np
				}()),
			},
			wantState: coreapi.ProvisioningStateSucceeded,
		},
		{
			name: "status replicas mismatch returns Provisioning",
			nodePool: func() *coreapi.NodePool {
				np := fixture.NewNodePool()
				np.Properties.Replicas = 3
				return np
			}(),
			readDesires: []*kubeapplierapi.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolCreateMatchingHypershiftNodePool()
					np.Spec.Replicas = ptr.To(int32(3))
					np.Status.Replicas = 1
					return np
				}()),
			},
			wantState:         coreapi.ProvisioningStateProvisioning,
			wantMessageSubstr: "status replicas is 1, want 3",
		},
		{
			name:     "scale to zero spec match status replicas nonzero returns Provisioning",
			nodePool: fixture.NewNodePool(),
			readDesires: []*kubeapplierapi.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolCreateMatchingHypershiftNodePool()
					np.Status.Replicas = 1
					return np
				}()),
			},
			wantState:         coreapi.ProvisioningStateProvisioning,
			wantMessageSubstr: "status replicas is 1, want 0",
		},
		{
			name: "AllMachinesReady false returns Provisioning",
			nodePool: func() *coreapi.NodePool {
				np := fixture.NewNodePool()
				np.Properties.Replicas = 3
				return np
			}(),
			readDesires: []*kubeapplierapi.ReadDesire{
				newHypershiftNodePoolReadDesire(t, func() *v1beta1.NodePool {
					np := testNodePoolCreateMatchingHypershiftNodePool()
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
		{
			name:     "scaling to zero skips AllMachinesReady check",
			nodePool: fixture.NewNodePool(),
			readDesires: []*kubeapplierapi.ReadDesire{
				newHypershiftNodePoolReadDesire(t, testNodePoolCreateMatchingHypershiftNodePool()),
			},
			wantState: coreapi.ProvisioningStateSucceeded,
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

			state, err := controller.hypershiftNodePoolOperationState(ctx, tt.nodePool)
			require.NoError(t, err)
			assert.Equal(t, tt.wantState, state.ProvisioningState)
			if tt.wantMessageSubstr != "" {
				assert.Contains(t, state.Message, tt.wantMessageSubstr)
			}
		})
	}
}

func TestCreateHypershiftNodePoolLabelsSpecMatchesDesired(t *testing.T) {
	t.Parallel()

	controller := &operationNodePoolCreate{}

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
			name:      "desired empty with extra observed labels",
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
			name:      "desired subset present with extra observed labels",
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

func TestCreateHypershiftNodePoolReplicasOrAutoscalingSpecMatchesDesired(t *testing.T) {
	t.Parallel()

	controller := &operationNodePoolCreate{}

	tests := []struct {
		name       string
		desired    *coreapi.NodePool
		observed   v1beta1.NodePoolSpec
		wantMatch  bool
		wantSubstr string
	}{
		{
			name: "replicas match",
			desired: &coreapi.NodePool{
				Properties: coreapi.NodePoolProperties{Replicas: 3},
			},
			observed:  v1beta1.NodePoolSpec{Replicas: ptr.To(int32(3))},
			wantMatch: true,
		},
		{
			name: "replicas mismatch",
			desired: &coreapi.NodePool{
				Properties: coreapi.NodePoolProperties{Replicas: 3},
			},
			observed:   v1beta1.NodePoolSpec{Replicas: ptr.To(int32(1))},
			wantMatch:  false,
			wantSubstr: "replicas is 1, want 3",
		},
		{
			name: "replicas desired observed unset",
			desired: &coreapi.NodePool{
				Properties: coreapi.NodePoolProperties{Replicas: 3},
			},
			observed:   v1beta1.NodePoolSpec{},
			wantMatch:  false,
			wantSubstr: "replicas is 0, want 3",
		},
		{
			name: "autoscaling match",
			desired: &coreapi.NodePool{
				Properties: coreapi.NodePoolProperties{
					AutoScaling: &coreapi.NodePoolAutoScaling{Min: 1, Max: 5},
				},
			},
			observed: v1beta1.NodePoolSpec{
				AutoScaling: &v1beta1.NodePoolAutoScaling{Min: ptr.To(int32(1)), Max: 5},
			},
			wantMatch: true,
		},
		{
			name: "autoscaling desired but observed unset",
			desired: &coreapi.NodePool{
				Properties: coreapi.NodePoolProperties{
					AutoScaling: &coreapi.NodePoolAutoScaling{Min: 1, Max: 5},
				},
			},
			observed:   v1beta1.NodePoolSpec{},
			wantMatch:  false,
			wantSubstr: "autoscaling is unset, want min=1 max=5",
		},
		{
			name: "autoscaling mismatch",
			desired: &coreapi.NodePool{
				Properties: coreapi.NodePoolProperties{
					AutoScaling: &coreapi.NodePoolAutoScaling{Min: 1, Max: 5},
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
			desired: &coreapi.NodePool{
				Properties: coreapi.NodePoolProperties{
					AutoScaling: &coreapi.NodePoolAutoScaling{Min: 1, Max: 5},
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
			desired: &coreapi.NodePool{
				Properties: coreapi.NodePoolProperties{Replicas: 3},
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

func TestCreateHypershiftNodePoolTaintsSpecMatchesDesired(t *testing.T) {
	t.Parallel()

	controller := &operationNodePoolCreate{}

	tests := []struct {
		name       string
		desired    []coreapi.Taint
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
			name:      "desired empty with extra observed taints",
			desired:   []coreapi.Taint{},
			observed:  []v1beta1.Taint{{Effect: corev1.TaintEffectNoSchedule, Key: "internal", Value: "true"}},
			wantMatch: true,
		},
		{
			name: "desired taint missing on observed",
			desired: []coreapi.Taint{
				{Effect: metadataapi.EffectNoSchedule, Key: "key1", Value: "val1"},
			},
			observed:   nil,
			wantMatch:  false,
			wantSubstr: "missing desired taint {effect:NoSchedule key:key1 value:val1}",
		},
		{
			name: "desired taint effect mismatch",
			desired: []coreapi.Taint{
				{Effect: metadataapi.EffectNoSchedule, Key: "key1", Value: "val1"},
			},
			observed: []v1beta1.Taint{
				{Effect: corev1.TaintEffectNoExecute, Key: "key1", Value: "val1"},
			},
			wantMatch:  false,
			wantSubstr: "missing desired taint {effect:NoSchedule key:key1 value:val1}",
		},
		{
			name: "desired taints match observed exactly",
			desired: []coreapi.Taint{
				{Effect: metadataapi.EffectNoSchedule, Key: "key1", Value: "val1"},
			},
			observed: []v1beta1.Taint{
				{Effect: corev1.TaintEffectNoSchedule, Key: "key1", Value: "val1"},
			},
			wantMatch: true,
		},
		{
			name: "desired subset present with extra observed taints",
			desired: []coreapi.Taint{
				{Effect: metadataapi.EffectNoSchedule, Key: "key1", Value: "val1"},
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

func TestCreateHypershiftNodePoolStatusReplicasMatchesDesired(t *testing.T) {
	t.Parallel()

	controller := &operationNodePoolCreate{}

	tests := []struct {
		name             string
		desired          *coreapi.NodePool
		observedReplicas int32
		wantMatch        bool
		wantSubstr       string
	}{
		{
			name: "fixed replicas match",
			desired: &coreapi.NodePool{
				Properties: coreapi.NodePoolProperties{Replicas: 3},
			},
			observedReplicas: 3,
			wantMatch:        true,
		},
		{
			name: "fixed replicas mismatch",
			desired: &coreapi.NodePool{
				Properties: coreapi.NodePoolProperties{Replicas: 3},
			},
			observedReplicas: 1,
			wantMatch:        false,
			wantSubstr:       "status replicas is 1, want 3",
		},
		{
			name: "autoscaling within range",
			desired: &coreapi.NodePool{
				Properties: coreapi.NodePoolProperties{
					AutoScaling: &coreapi.NodePoolAutoScaling{Min: 1, Max: 5},
				},
			},
			observedReplicas: 3,
			wantMatch:        true,
		},
		{
			name: "autoscaling below min",
			desired: &coreapi.NodePool{
				Properties: coreapi.NodePoolProperties{
					AutoScaling: &coreapi.NodePoolAutoScaling{Min: 2, Max: 5},
				},
			},
			observedReplicas: 1,
			wantMatch:        false,
			wantSubstr:       "status replicas is 1, want >= 2 (autoscaling min)",
		},
		{
			name: "autoscaling above max",
			desired: &coreapi.NodePool{
				Properties: coreapi.NodePoolProperties{
					AutoScaling: &coreapi.NodePoolAutoScaling{Min: 1, Max: 5},
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

func TestCreateHypershiftNodePoolConditionStatusMatchesDesired(t *testing.T) {
	t.Parallel()

	controller := &operationNodePoolCreate{}

	tests := []struct {
		name          string
		conditionType string
		conditions    []v1beta1.NodePoolCondition
		wantMatch     bool
		wantSubstr    string
	}{
		{
			name:          "AllMachinesReady condition true",
			conditionType: v1beta1.NodePoolAllMachinesReadyConditionType,
			conditions: []v1beta1.NodePoolCondition{
				{Type: v1beta1.NodePoolAllMachinesReadyConditionType, Status: corev1.ConditionTrue},
			},
			wantMatch: true,
		},
		{
			name:          "AllMachinesReady condition false",
			conditionType: v1beta1.NodePoolAllMachinesReadyConditionType,
			conditions: []v1beta1.NodePoolCondition{
				{Type: v1beta1.NodePoolAllMachinesReadyConditionType, Status: corev1.ConditionFalse, Message: "waiting"},
			},
			wantMatch:  false,
			wantSubstr: "condition AllMachinesReady is False: waiting",
		},
		{
			name:          "AllMachinesReady condition not yet reported",
			conditionType: v1beta1.NodePoolAllMachinesReadyConditionType,
			conditions:    nil,
			wantMatch:     false,
			wantSubstr:    "condition AllMachinesReady not yet reported",
		},
		{
			name:          "AllNodesHealthy condition true",
			conditionType: v1beta1.NodePoolAllNodesHealthyConditionType,
			conditions: []v1beta1.NodePoolCondition{
				{Type: v1beta1.NodePoolAllNodesHealthyConditionType, Status: corev1.ConditionTrue},
			},
			wantMatch: true,
		},
		{
			name:          "AllNodesHealthy condition false",
			conditionType: v1beta1.NodePoolAllNodesHealthyConditionType,
			conditions: []v1beta1.NodePoolCondition{
				{Type: v1beta1.NodePoolAllNodesHealthyConditionType, Status: corev1.ConditionFalse, Message: "1 of 2 machines are not healthy"},
			},
			wantMatch:  false,
			wantSubstr: "condition AllNodesHealthy is False: 1 of 2 machines are not healthy",
		},
		{
			name:          "AllNodesHealthy condition not yet reported",
			conditionType: v1beta1.NodePoolAllNodesHealthyConditionType,
			conditions:    nil,
			wantMatch:     false,
			wantSubstr:    "condition AllNodesHealthy not yet reported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			match, msg := controller.hypershiftNodePoolConditionStatusMatchesDesired(tt.conditions, tt.conditionType)
			assert.Equal(t, tt.wantMatch, match)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}

func TestCreateHypershiftNodePoolStatusMatchesDesired(t *testing.T) {
	t.Parallel()

	controller := &operationNodePoolCreate{}

	tests := []struct {
		name        string
		desired     *coreapi.NodePool
		observed    v1beta1.NodePoolStatus
		wantMatch   bool
		wantSubstrs []string
	}{
		{
			name: "fixed replicas with ready machines",
			desired: &coreapi.NodePool{
				Properties: coreapi.NodePoolProperties{Replicas: 3},
			},
			observed: v1beta1.NodePoolStatus{
				Replicas: 3,
				Conditions: []v1beta1.NodePoolCondition{
					{Type: v1beta1.NodePoolAllNodesHealthyConditionType, Status: corev1.ConditionTrue},
					{Type: v1beta1.NodePoolAllMachinesReadyConditionType, Status: corev1.ConditionTrue},
				},
			},
			wantMatch: true,
		},
		{
			name: "scaling to zero skips AllNodesHealthy and AllMachinesReady",
			desired: &coreapi.NodePool{
				Properties: coreapi.NodePoolProperties{Replicas: 0},
			},
			observed: v1beta1.NodePoolStatus{
				Replicas: 0,
			},
			wantMatch: true,
		},
		{
			name: "autoscaling at min with ready machines returns match",
			desired: &coreapi.NodePool{
				Properties: coreapi.NodePoolProperties{
					AutoScaling: &coreapi.NodePoolAutoScaling{Min: 1, Max: 5},
				},
			},
			observed: v1beta1.NodePoolStatus{
				Replicas: 1,
				Conditions: []v1beta1.NodePoolCondition{
					{Type: v1beta1.NodePoolAllNodesHealthyConditionType, Status: corev1.ConditionTrue},
					{Type: v1beta1.NodePoolAllMachinesReadyConditionType, Status: corev1.ConditionTrue},
				},
			},
			wantMatch: true,
		},
		{
			name: "autoscaling in range but AllMachinesReady false returns mismatch",
			desired: &coreapi.NodePool{
				Properties: coreapi.NodePoolProperties{
					AutoScaling: &coreapi.NodePoolAutoScaling{Min: 1, Max: 5},
				},
			},
			observed: v1beta1.NodePoolStatus{
				Replicas: 3,
				Conditions: []v1beta1.NodePoolCondition{
					{Type: v1beta1.NodePoolAllNodesHealthyConditionType, Status: corev1.ConditionTrue},
					{Type: v1beta1.NodePoolAllMachinesReadyConditionType, Status: corev1.ConditionFalse, Message: "waiting"},
				},
			},
			wantMatch:   false,
			wantSubstrs: []string{"condition AllMachinesReady is False: waiting"},
		},
		{
			name: "autoscaling in range but AllNodesHealthy false returns mismatch",
			desired: &coreapi.NodePool{
				Properties: coreapi.NodePoolProperties{
					AutoScaling: &coreapi.NodePoolAutoScaling{Min: 1, Max: 5},
				},
			},
			observed: v1beta1.NodePoolStatus{
				Replicas: 3,
				Conditions: []v1beta1.NodePoolCondition{
					{Type: v1beta1.NodePoolAllNodesHealthyConditionType, Status: corev1.ConditionFalse, Message: "1 of 2 machines are not healthy"},
					{Type: v1beta1.NodePoolAllMachinesReadyConditionType, Status: corev1.ConditionTrue},
				},
			},
			wantMatch:   false,
			wantSubstrs: []string{"condition AllNodesHealthy is False: 1 of 2 machines are not healthy"},
		},
		{
			name: "autoscaling in range but AllMachinesReady not reported returns mismatch",
			desired: &coreapi.NodePool{
				Properties: coreapi.NodePoolProperties{
					AutoScaling: &coreapi.NodePoolAutoScaling{Min: 1, Max: 5},
				},
			},
			observed: v1beta1.NodePoolStatus{
				Replicas: 3,
			},
			wantMatch:   false,
			wantSubstrs: []string{"condition AllMachinesReady not yet reported"},
		},
		{
			name: "autoscaling status below min",
			desired: &coreapi.NodePool{
				Properties: coreapi.NodePoolProperties{
					AutoScaling: &coreapi.NodePoolAutoScaling{Min: 2, Max: 5},
				},
			},
			observed: v1beta1.NodePoolStatus{
				Replicas: 1,
			},
			wantMatch:   false,
			wantSubstrs: []string{"status replicas is 1, want >= 2 (autoscaling min)"},
		},
		{
			name: "autoscaling status above max",
			desired: &coreapi.NodePool{
				Properties: coreapi.NodePoolProperties{
					AutoScaling: &coreapi.NodePoolAutoScaling{Min: 1, Max: 5},
				},
			},
			observed: v1beta1.NodePoolStatus{
				Replicas: 6,
			},
			wantMatch:   false,
			wantSubstrs: []string{"status replicas is 6, want <= 5 (autoscaling max)"},
		},
		{
			name: "replicas, AllNodesHealthy, and AllMachinesReady mismatches are all reported together",
			desired: &coreapi.NodePool{
				Properties: coreapi.NodePoolProperties{Replicas: 3},
			},
			observed: v1beta1.NodePoolStatus{
				Replicas: 1,
				Conditions: []v1beta1.NodePoolCondition{
					{Type: v1beta1.NodePoolAllNodesHealthyConditionType, Status: corev1.ConditionFalse, Message: "1 of 2 machines are not healthy"},
					{Type: v1beta1.NodePoolAllMachinesReadyConditionType, Status: corev1.ConditionFalse, Message: "waiting"},
				},
			},
			wantMatch: false,
			wantSubstrs: []string{
				"status replicas is 1, want 3",
				"condition AllNodesHealthy is False: 1 of 2 machines are not healthy",
				"condition AllMachinesReady is False: waiting",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			match, msg := controller.hypershiftNodePoolStatusMatchesDesired(tt.desired, tt.observed)
			assert.Equal(t, tt.wantMatch, match)
			for _, substr := range tt.wantSubstrs {
				assert.Contains(t, msg, substr)
			}
		})
	}
}

// testNodePoolCreateMatchingHypershiftNodePool returns a Hypershift NodePool that matches the
// default (zero-replica) node pool fixture for node pool create state calculation tests.
func testNodePoolCreateMatchingHypershiftNodePool() *v1beta1.NodePool {
	return &v1beta1.NodePool{
		Spec: v1beta1.NodePoolSpec{
			Replicas: ptr.To(int32(0)),
		},
		Status: v1beta1.NodePoolStatus{
			Replicas: 0,
		},
	}
}
