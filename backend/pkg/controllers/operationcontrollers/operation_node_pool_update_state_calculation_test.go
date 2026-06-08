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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/api"
)

func TestNodePoolUpdateSpecMatchesDesired(t *testing.T) {
	tests := []struct {
		name     string
		desired  api.HCPOpenShiftClusterNodePoolProperties
		observed v1beta1.NodePoolSpec
		want     bool
		wantMsg  string
	}{
		{
			name: "fixed replicas match",
			desired: api.HCPOpenShiftClusterNodePoolProperties{
				Replicas: 3,
			},
			observed: v1beta1.NodePoolSpec{
				Replicas: ptr.To(int32(3)),
			},
			want: true,
		},
		{
			name: "fixed replicas mismatch",
			desired: api.HCPOpenShiftClusterNodePoolProperties{
				Replicas: 3,
			},
			observed: v1beta1.NodePoolSpec{
				Replicas: ptr.To(int32(2)),
			},
			want:    false,
			wantMsg: "hypershift NodePool replicas is 2, want 3",
		},
		{
			name: "autoscaling match",
			desired: api.HCPOpenShiftClusterNodePoolProperties{
				AutoScaling: &api.NodePoolAutoScaling{Min: 2, Max: 5},
			},
			observed: v1beta1.NodePoolSpec{
				AutoScaling: &v1beta1.NodePoolAutoScaling{
					Min: ptr.To(int32(2)),
					Max: 5,
				},
			},
			want: true,
		},
		{
			name: "autoscaling min mismatch",
			desired: api.HCPOpenShiftClusterNodePoolProperties{
				AutoScaling: &api.NodePoolAutoScaling{Min: 2, Max: 5},
			},
			observed: v1beta1.NodePoolSpec{
				AutoScaling: &v1beta1.NodePoolAutoScaling{
					Min: ptr.To(int32(1)),
					Max: 5,
				},
			},
			want:    false,
			wantMsg: "hypershift NodePool autoscaling min is 1, want 2",
		},
		{
			name: "desired autoscaling but observed fixed replicas",
			desired: api.HCPOpenShiftClusterNodePoolProperties{
				AutoScaling: &api.NodePoolAutoScaling{Min: 2, Max: 5},
			},
			observed: v1beta1.NodePoolSpec{
				Replicas: ptr.To(int32(0)),
			},
			want:    false,
			wantMsg: "hypershift NodePool has no autoscaling configuration",
		},
		{
			name: "desired fixed replicas but observed autoscaling remains",
			desired: api.HCPOpenShiftClusterNodePoolProperties{
				Replicas: 3,
			},
			observed: v1beta1.NodePoolSpec{
				AutoScaling: &v1beta1.NodePoolAutoScaling{
					Min: ptr.To(int32(2)),
					Max: 5,
				},
			},
			want:    false,
			wantMsg: "hypershift NodePool still has autoscaling configuration",
		},
		{
			name: "labels match",
			desired: api.HCPOpenShiftClusterNodePoolProperties{
				Replicas: 1,
				Labels:   map[string]string{"role": "worker", "env": "test"},
			},
			observed: v1beta1.NodePoolSpec{
				Replicas:   ptr.To(int32(1)),
				NodeLabels: map[string]string{"role": "worker", "env": "test"},
			},
			want: true,
		},
		{
			name: "labels mismatch",
			desired: api.HCPOpenShiftClusterNodePoolProperties{
				Replicas: 1,
				Labels:   map[string]string{"role": "worker"},
			},
			observed: v1beta1.NodePoolSpec{
				Replicas:   ptr.To(int32(1)),
				NodeLabels: map[string]string{"role": "infra"},
			},
			want:    false,
			wantMsg: "hypershift NodePool nodeLabels do not match desired labels",
		},
		{
			name: "taints match",
			desired: api.HCPOpenShiftClusterNodePoolProperties{
				Replicas: 1,
				Taints: []api.Taint{
					{Key: "k1", Value: "v1", Effect: api.EffectNoSchedule},
				},
			},
			observed: v1beta1.NodePoolSpec{
				Replicas: ptr.To(int32(1)),
				Taints: []v1beta1.Taint{
					{Key: "k1", Value: "v1", Effect: corev1.TaintEffectNoSchedule},
				},
			},
			want: true,
		},
		{
			name: "taints mismatch",
			desired: api.HCPOpenShiftClusterNodePoolProperties{
				Replicas: 1,
				Taints: []api.Taint{
					{Key: "k1", Value: "v1", Effect: api.EffectNoSchedule},
				},
			},
			observed: v1beta1.NodePoolSpec{
				Replicas: ptr.To(int32(1)),
				Taints:   []v1beta1.Taint{},
			},
			want:    false,
			wantMsg: "hypershift NodePool has 0 taints, want 1",
		},
		{
			name: "node drain timeout match",
			desired: api.HCPOpenShiftClusterNodePoolProperties{
				Replicas:                1,
				NodeDrainTimeoutMinutes: ptr.To(int32(30)),
			},
			observed: v1beta1.NodePoolSpec{
				Replicas:         ptr.To(int32(1)),
				NodeDrainTimeout: &metav1.Duration{Duration: 30 * time.Minute},
			},
			want: true,
		},
		{
			name: "node drain timeout mismatch",
			desired: api.HCPOpenShiftClusterNodePoolProperties{
				Replicas:                1,
				NodeDrainTimeoutMinutes: ptr.To(int32(30)),
			},
			observed: v1beta1.NodePoolSpec{
				Replicas:         ptr.To(int32(1)),
				NodeDrainTimeout: &metav1.Duration{Duration: 15 * time.Minute},
			},
			want:    false,
			wantMsg: "hypershift NodePool nodeDrainTimeout is 15m0s, want 30m0s",
		},
		{
			name: "node drain timeout both nil",
			desired: api.HCPOpenShiftClusterNodePoolProperties{
				Replicas: 1,
			},
			observed: v1beta1.NodePoolSpec{
				Replicas: ptr.To(int32(1)),
			},
			want: true,
		},
		{
			name: "node drain timeout desired nil but observed set",
			desired: api.HCPOpenShiftClusterNodePoolProperties{
				Replicas: 1,
			},
			observed: v1beta1.NodePoolSpec{
				Replicas:         ptr.To(int32(1)),
				NodeDrainTimeout: &metav1.Duration{Duration: 10 * time.Minute},
			},
			want:    false,
			wantMsg: "hypershift NodePool nodeDrainTimeout is 10m0s, want unset",
		},
		{
			name: "node drain timeout desired zero and observed nil treated as match",
			desired: api.HCPOpenShiftClusterNodePoolProperties{
				Replicas:                1,
				NodeDrainTimeoutMinutes: ptr.To(int32(0)),
			},
			observed: v1beta1.NodePoolSpec{
				Replicas: ptr.To(int32(1)),
			},
			want: true,
		},
		{
			name: "node drain timeout desired nonzero but observed nil",
			desired: api.HCPOpenShiftClusterNodePoolProperties{
				Replicas:                1,
				NodeDrainTimeoutMinutes: ptr.To(int32(15)),
			},
			observed: v1beta1.NodePoolSpec{
				Replicas: ptr.To(int32(1)),
			},
			want:    false,
			wantMsg: "hypershift NodePool nodeDrainTimeout is unset, want 15m0s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &operationNodePoolUpdate{}
			got, msg := c.hypershiftNodePoolSpecMatchesCosmosDesired(tt.desired, tt.observed)
			assert.Equal(t, tt.want, got)
			if !tt.want {
				assert.Equal(t, tt.wantMsg, msg)
			}
		})
	}
}

func TestNodePoolUpdateStatusMatchesDesired(t *testing.T) {
	tests := []struct {
		name     string
		desired  api.HCPOpenShiftClusterNodePoolProperties
		observed v1beta1.NodePoolStatus
		want     bool
		wantMsg  string
	}{
		{
			name: "fixed replicas match",
			desired: api.HCPOpenShiftClusterNodePoolProperties{
				Replicas: 3,
			},
			observed: v1beta1.NodePoolStatus{
				Replicas: 3,
				Conditions: []v1beta1.NodePoolCondition{
					{Type: v1beta1.NodePoolReadyConditionType, Status: corev1.ConditionTrue},
				},
			},
			want: true,
		},
		{
			name: "fixed replicas mismatch",
			desired: api.HCPOpenShiftClusterNodePoolProperties{
				Replicas: 3,
			},
			observed: v1beta1.NodePoolStatus{Replicas: 2},
			want:     false,
			wantMsg:  "hypershift NodePool status replicas is 2, want 3",
		},
		{
			name: "autoscaling status within range",
			desired: api.HCPOpenShiftClusterNodePoolProperties{
				AutoScaling: &api.NodePoolAutoScaling{Min: 2, Max: 5},
			},
			observed: v1beta1.NodePoolStatus{
				Replicas: 3,
				Conditions: []v1beta1.NodePoolCondition{
					{Type: v1beta1.NodePoolAutoscalingEnabledConditionType, Status: corev1.ConditionTrue},
					{Type: v1beta1.NodePoolReadyConditionType, Status: corev1.ConditionTrue},
				},
			},
			want: true,
		},
		{
			name: "autoscaling enabled condition false",
			desired: api.HCPOpenShiftClusterNodePoolProperties{
				AutoScaling: &api.NodePoolAutoScaling{Min: 2, Max: 5},
			},
			observed: v1beta1.NodePoolStatus{
				Replicas: 3,
				Conditions: []v1beta1.NodePoolCondition{
					{Type: v1beta1.NodePoolAutoscalingEnabledConditionType, Status: corev1.ConditionFalse, Reason: "Disabled", Message: "waiting for autoscaler"},
				},
			},
			want:    false,
			wantMsg: "hypershift NodePool autoscaling is not enabled: Disabled: waiting for autoscaler",
		},
		{
			name: "autoscaling status below min",
			desired: api.HCPOpenShiftClusterNodePoolProperties{
				AutoScaling: &api.NodePoolAutoScaling{Min: 2, Max: 5},
			},
			observed: v1beta1.NodePoolStatus{Replicas: 1},
			want:     false,
			wantMsg:  "hypershift NodePool status replicas is 1, want at least 2",
		},
		{
			name: "autoscaling status above max",
			desired: api.HCPOpenShiftClusterNodePoolProperties{
				AutoScaling: &api.NodePoolAutoScaling{Min: 2, Max: 5},
			},
			observed: v1beta1.NodePoolStatus{Replicas: 6},
			want:     false,
			wantMsg:  "hypershift NodePool status replicas is 6, want at most 5",
		},
		{
			name: "replicas match but not ready",
			desired: api.HCPOpenShiftClusterNodePoolProperties{
				Replicas: 3,
			},
			observed: v1beta1.NodePoolStatus{
				Replicas: 3,
				Conditions: []v1beta1.NodePoolCondition{
					{Type: v1beta1.NodePoolReadyConditionType, Status: corev1.ConditionFalse, Reason: "NotReady", Message: "machines not ready"},
				},
			},
			want:    false,
			wantMsg: "hypershift NodePool is not ready: NotReady: machines not ready",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &operationNodePoolUpdate{}
			got, msg := c.hypershiftNodePoolStatusMatchesCosmosDesired(tt.desired, tt.observed)
			assert.Equal(t, tt.want, got)
			if !tt.want {
				assert.Equal(t, tt.wantMsg, msg)
			}
		})
	}
}
