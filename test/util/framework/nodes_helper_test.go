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

package framework

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestSelectNodesBelongingToNodePool(t *testing.T) {
	t.Parallel()

	nodeWithNodePoolLabel := func(labelValue string) corev1.Node {
		return corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: labelValue + "-" + uuid.NewString(),
				Labels: map[string]string{
					v1beta1.NodePoolLabel: labelValue,
				},
			},
		}
	}

	node1 := nodeWithNodePoolLabel("e2e-cluster-np")
	node2 := nodeWithNodePoolLabel("e2e-cluster-np")
	node3 := nodeWithNodePoolLabel("e2e-cluster-worker-np")
	node4 := nodeWithNodePoolLabel("e2e-cluster-infra-worker-np")

	tests := []struct {
		name                  string
		nodes                 []corev1.Node
		pool                  string
		expectError           bool
		expectedErrorContains string
		wantNodes             []corev1.Node
	}{
		{
			name:                  "empty nodePoolName",
			pool:                  "",
			expectError:           true,
			expectedErrorContains: "nodePoolName is required",
		},
		{
			name:      "empty node list",
			nodes:     nil,
			pool:      "np",
			wantNodes: nil,
		},
		{
			name: "missing label", // this should never happen
			nodes: []corev1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "a", Labels: map[string]string{"other": "v"}}},
			},
			pool:      "np",
			wantNodes: nil,
		},
		{
			name:      "suffix does not match",
			nodes:     []corev1.Node{node4, node3, node2, node1},
			pool:      "other-pool",
			wantNodes: nil,
		},
		{
			name:      "nodePoolName full hypershift label does not match suffix rule",
			nodes:     []corev1.Node{node1, node2},
			pool:      "e2e-cluster-np",
			wantNodes: nil,
		},
		{
			name:      "shortest matching label wins when suffix overlaps",
			nodes:     []corev1.Node{node4, node3, node1, node2},
			pool:      "np",
			wantNodes: []corev1.Node{node1, node2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			nodes, err := SelectNodesBelongingToNodePool(tt.nodes, tt.pool)

			if tt.expectError {
				assert.Error(t, err)
				assert.NotEmpty(t, tt.expectedErrorContains, "expectedErrorContains should be set when expectError is true")
				assert.ErrorContains(t, err, tt.expectedErrorContains)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.wantNodes, nodes, "returned nodes must match")
		})
	}
}
