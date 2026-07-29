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

package ocm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
)

func TestNodePoolUpdateDispatchConfigHash(t *testing.T) {
	baseProperties := api.HCPOpenShiftClusterNodePoolProperties{
		Labels:   map[string]string{"env": "prod"},
		Replicas: 3,
		Taints: []api.Taint{
			{Effect: api.EffectNoSchedule, Key: "key1", Value: "val1"},
		},
		NodeDrainTimeoutMinutes: ptr.To(int32(30)),
	}

	base := &api.HCPOpenShiftClusterNodePool{Properties: baseProperties}

	baseHash, err := nodePoolUpdateDispatchConfigHash(base)
	require.NoError(t, err)
	require.NotEmpty(t, baseHash)

	hashAgain, err := nodePoolUpdateDispatchConfigHash(base)
	require.NoError(t, err)
	assert.Equal(t, baseHash, hashAgain)

	tests := []struct {
		name     string
		nodePool *api.HCPOpenShiftClusterNodePool
	}{
		{
			name: "different labels",
			nodePool: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					Labels:                  map[string]string{"env": "staging"},
					Replicas:                baseProperties.Replicas,
					Taints:                  baseProperties.Taints,
					NodeDrainTimeoutMinutes: baseProperties.NodeDrainTimeoutMinutes,
				},
			},
		},
		{
			name: "different replicas",
			nodePool: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					Labels:                  baseProperties.Labels,
					Replicas:                5,
					Taints:                  baseProperties.Taints,
					NodeDrainTimeoutMinutes: baseProperties.NodeDrainTimeoutMinutes,
				},
			},
		},
		{
			name: "autoscaling instead of replicas",
			nodePool: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					Labels:                  baseProperties.Labels,
					AutoScaling:             &api.NodePoolAutoScaling{Min: 1, Max: 10},
					Taints:                  baseProperties.Taints,
					NodeDrainTimeoutMinutes: baseProperties.NodeDrainTimeoutMinutes,
				},
			},
		},
		{
			name: "different taints",
			nodePool: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					Labels:   baseProperties.Labels,
					Replicas: baseProperties.Replicas,
					Taints: []api.Taint{
						{Effect: api.EffectNoExecute, Key: "key2", Value: "val2"},
					},
					NodeDrainTimeoutMinutes: baseProperties.NodeDrainTimeoutMinutes,
				},
			},
		},
		{
			name: "different node drain timeout",
			nodePool: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					Labels:                  baseProperties.Labels,
					Replicas:                baseProperties.Replicas,
					Taints:                  baseProperties.Taints,
					NodeDrainTimeoutMinutes: ptr.To(int32(60)),
				},
			},
		},
	}

	// Each row changes one dispatch-managed field from the baseline above. Comparing against
	// baseHash checks that the field is included in the canonical hash. It does not prove the
	// field mapped correctly. See FromCS round-trip and per-helper tests for that.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash, err := nodePoolUpdateDispatchConfigHash(tt.nodePool)
			require.NoError(t, err)
			assert.NotEqual(t, baseHash, hash, "changing %q should change the dispatch config hash", tt.name)
		})
	}
}

func TestNodePoolUpdateDispatchConfigHashExcludesNonUpdatableFields(t *testing.T) {
	np1 := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Replicas: 3,
			Version:  api.NodePoolVersionProfile{ID: "4.19.1", ChannelGroup: "stable"},
			Platform: api.NodePoolPlatformProfile{
				VMSize: "Standard_D4s_v3",
			},
		},
	}

	np2 := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Replicas: 3,
			Version:  api.NodePoolVersionProfile{ID: "4.19.2", ChannelGroup: "candidate"},
			Platform: api.NodePoolPlatformProfile{
				VMSize: "Standard_D8s_v3",
			},
		},
	}

	hash1, err := nodePoolUpdateDispatchConfigHash(np1)
	require.NoError(t, err)
	hash2, err := nodePoolUpdateDispatchConfigHash(np2)
	require.NoError(t, err)
	assert.Equal(t, hash1, hash2)
}

func TestNodePoolUpdateDispatchConfigHashExcludesAutoRepair(t *testing.T) {
	np1 := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Replicas:   3,
			AutoRepair: true,
		},
	}
	np2 := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Replicas:   3,
			AutoRepair: false,
		},
	}

	hash1, err := nodePoolUpdateDispatchConfigHash(np1)
	require.NoError(t, err)
	hash2, err := nodePoolUpdateDispatchConfigHash(np2)
	require.NoError(t, err)
	assert.Equal(t, hash1, hash2)
}

// TestNodePoolUpdateDispatchConfigFromCSRoundTrip checks that RP and Cluster Service agree on
// dispatch-managed config after materializing RP desired state onto a Cluster Service node pool
// via BuildCSNodePool (update path: updating=true). nodePoolUpdateDispatchConfigFromCS and
// nodePoolUpdateDispatchConfigFromRP must then produce the same canonical hash.
func TestNodePoolUpdateDispatchConfigFromCSRoundTrip(t *testing.T) {
	nodePool := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Labels:   map[string]string{"env": "prod", "team": "platform"},
			Replicas: 5,
			Taints: []api.Taint{
				{Effect: api.EffectNoSchedule, Key: "dedicated", Value: "infra"},
			},
			NodeDrainTimeoutMinutes: ptr.To(int32(45)),
			AutoRepair:              true,
			Version:                 api.NodePoolVersionProfile{ID: "4.19.1", ChannelGroup: "stable"},
			Platform: api.NodePoolPlatformProfile{
				VMSize: "Standard_D4s_v3",
			},
		},
	}

	csNodePoolBuilder, err := BuildCSNodePool(context.Background(), nodePool, true)
	require.NoError(t, err)

	csNodePool, err := csNodePoolBuilder.Build()
	require.NoError(t, err)

	actualConfig := nodePoolUpdateDispatchConfigFromCS(csNodePool)

	desiredHash, err := nodePoolUpdateDispatchConfigFromRP(nodePool).hash()
	require.NoError(t, err)
	actualHash, err := actualConfig.hash()
	require.NoError(t, err)
	assert.Equal(t, desiredHash, actualHash)
}

// TestNodePoolUpdateDispatchConfigFromCSRoundTripAutoScaling checks that RP desired state with
// autoscaling (instead of fixed replicas) round-trips through BuildCSNodePool and
// nodePoolUpdateDispatchConfigFromCS with matching hash.
func TestNodePoolUpdateDispatchConfigFromCSRoundTripAutoScaling(t *testing.T) {
	nodePool := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			AutoScaling:             &api.NodePoolAutoScaling{Min: 2, Max: 10},
			NodeDrainTimeoutMinutes: ptr.To(int32(30)),
		},
	}

	csNodePoolBuilder, err := BuildCSNodePool(context.Background(), nodePool, true)
	require.NoError(t, err)

	csNodePool, err := csNodePoolBuilder.Build()
	require.NoError(t, err)

	actualConfig := nodePoolUpdateDispatchConfigFromCS(csNodePool)

	desiredHash, err := nodePoolUpdateDispatchConfigFromRP(nodePool).hash()
	require.NoError(t, err)
	actualHash, err := actualConfig.hash()
	require.NoError(t, err)
	assert.Equal(t, desiredHash, actualHash)
}

func TestNodePoolUpdateDispatchConfigFromCS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		csNodePool func(t *testing.T) *arohcpv1alpha1.NodePool
		want       *nodePoolUpdateDispatchConfig
	}{
		{
			name: "fixed replicas without autoscaling",
			csNodePool: func(t *testing.T) *arohcpv1alpha1.NodePool {
				t.Helper()
				csNodePool, err := arohcpv1alpha1.NewNodePool().Replicas(5).Build()
				require.NoError(t, err)
				return csNodePool
			},
			want: &nodePoolUpdateDispatchConfig{
				Replicas: 5,
			},
		},
		{
			name: "autoscaling maps min and max instead of replicas",
			csNodePool: func(t *testing.T) *arohcpv1alpha1.NodePool {
				t.Helper()
				csNodePool, err := arohcpv1alpha1.NewNodePool().
					Autoscaling(arohcpv1alpha1.NewNodePoolAutoscaling().
						MinReplica(2).
						MaxReplica(10)).
					Build()
				require.NoError(t, err)
				return csNodePool
			},
			want: &nodePoolUpdateDispatchConfig{
				AutoScaling: &nodePoolUpdateDispatchConfigAutoScaling{
					Min: 2,
					Max: 10,
				},
			},
		},
		{
			name: "labels are copied when set",
			csNodePool: func(t *testing.T) *arohcpv1alpha1.NodePool {
				t.Helper()
				csNodePool, err := arohcpv1alpha1.NewNodePool().
					Labels(map[string]string{"env": "prod"}).
					Build()
				require.NoError(t, err)
				return csNodePool
			},
			want: &nodePoolUpdateDispatchConfig{
				Labels: map[string]string{"env": "prod"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := nodePoolUpdateDispatchConfigFromCS(tt.csNodePool(t))
			if tt.want.Replicas != 0 {
				assert.Equal(t, tt.want.Replicas, got.Replicas)
			}
			if tt.want.AutoScaling != nil {
				require.NotNil(t, got.AutoScaling)
				assert.Equal(t, *tt.want.AutoScaling, *got.AutoScaling)
			}
			if tt.want.Labels != nil {
				assert.Equal(t, tt.want.Labels, got.Labels)
			}
		})
	}
}

func TestNodePoolUpdateDispatchConfigNodeDrainTimeoutFromCS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		csNodePool *arohcpv1alpha1.NodePool
		want       *int32
	}{
		{
			name: "unset cs node drain grace period returns nil",
			csNodePool: func() *arohcpv1alpha1.NodePool {
				csNodePool, err := arohcpv1alpha1.NewNodePool().Build()
				require.NoError(t, err)
				return csNodePool
			}(),
			want: nil,
		},
		{
			name: "cs node drain grace period set in minutes unit returns the set value",
			csNodePool: func() *arohcpv1alpha1.NodePool {
				csNodePool, err := arohcpv1alpha1.NewNodePool().
					NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
						Unit(csNodeDrainGracePeriodUnit).
						Value(float64(45))).
					Build()
				require.NoError(t, err)
				return csNodePool
			}(),
			want: ptr.To(int32(45)),
		},
		{
			name: "cs node drain grace period set in non-minutes unit returns nil",
			csNodePool: func() *arohcpv1alpha1.NodePool {
				csNodePool, err := arohcpv1alpha1.NewNodePool().
					NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
						Unit("hours").
						Value(float64(1))).
					Build()
				require.NoError(t, err)
				return csNodePool
			}(),
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, NodePoolUpdateDispatchConfigNodeDrainTimeoutFromCS(tt.csNodePool))
		})
	}
}

func TestNodePoolUpdateDispatchConfigEffectiveNodeDrainTimeoutMinutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		nodePool   *api.HCPOpenShiftClusterNodePool
		csNodePool func(t *testing.T) *arohcpv1alpha1.NodePool
		want       *int32
	}{
		{
			name: "explicit RP override",
			nodePool: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					NodeDrainTimeoutMinutes: ptr.To(int32(15)),
				},
			},
			csNodePool: func(t *testing.T) *arohcpv1alpha1.NodePool {
				t.Helper()
				csNodePoolBuilder, err := BuildCSNodePool(context.Background(), &api.HCPOpenShiftClusterNodePool{
					Properties: api.HCPOpenShiftClusterNodePoolProperties{
						NodeDrainTimeoutMinutes: ptr.To(int32(4)),
					},
				}, true)
				require.NoError(t, err)
				csNodePool, err := csNodePoolBuilder.Build()
				require.NoError(t, err)
				return csNodePool
			},
			want: ptr.To(int32(15)),
		},
		{
			name: "RP set uses RP when CS unset",
			nodePool: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					NodeDrainTimeoutMinutes: ptr.To(int32(15)),
				},
			},
			csNodePool: func(t *testing.T) *arohcpv1alpha1.NodePool {
				t.Helper()
				csNodePool, err := arohcpv1alpha1.NewNodePool().Build()
				require.NoError(t, err)
				return csNodePool
			},
			want: ptr.To(int32(15)),
		},
		{
			name:     "RP unset uses CS",
			nodePool: &api.HCPOpenShiftClusterNodePool{},
			csNodePool: func(t *testing.T) *arohcpv1alpha1.NodePool {
				t.Helper()
				csNodePoolBuilder, err := BuildCSNodePool(context.Background(), &api.HCPOpenShiftClusterNodePool{
					Properties: api.HCPOpenShiftClusterNodePoolProperties{
						NodeDrainTimeoutMinutes: ptr.To(int32(4)),
					},
				}, true)
				require.NoError(t, err)
				csNodePool, err := csNodePoolBuilder.Build()
				require.NoError(t, err)
				return csNodePool
			},
			want: ptr.To(int32(4)),
		},
		{
			name:     "RP unset and CS unset returns nil",
			nodePool: &api.HCPOpenShiftClusterNodePool{},
			csNodePool: func(t *testing.T) *arohcpv1alpha1.NodePool {
				t.Helper()
				csNodePool, err := arohcpv1alpha1.NewNodePool().Build()
				require.NoError(t, err)
				return csNodePool
			},
			want: nil,
		},
		{
			name: "RP explicit zero overrides CS",
			nodePool: &api.HCPOpenShiftClusterNodePool{
				Properties: api.HCPOpenShiftClusterNodePoolProperties{
					NodeDrainTimeoutMinutes: ptr.To(int32(0)),
				},
			},
			csNodePool: func(t *testing.T) *arohcpv1alpha1.NodePool {
				t.Helper()
				csNodePoolBuilder, err := BuildCSNodePool(context.Background(), &api.HCPOpenShiftClusterNodePool{
					Properties: api.HCPOpenShiftClusterNodePoolProperties{
						NodeDrainTimeoutMinutes: ptr.To(int32(4)),
					},
				}, true)
				require.NoError(t, err)
				csNodePool, err := csNodePoolBuilder.Build()
				require.NoError(t, err)
				return csNodePool
			},
			want: ptr.To(int32(0)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := NodePoolUpdateDispatchConfigEffectiveNodeDrainTimeoutMinutes(tt.nodePool, tt.csNodePool(t))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNodePoolUpdateDispatchConfigDiffJSONIgnoresCSDrainTimeoutWhenRPUnset(t *testing.T) {
	nodePool := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Replicas: 3,
		},
	}

	csNodePool := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Replicas:                3,
			NodeDrainTimeoutMinutes: ptr.To(int32(4)),
		},
	}
	csNodePoolBuilder, err := BuildCSNodePool(context.Background(), csNodePool, true)
	require.NoError(t, err)
	csNodePoolBuilt, err := csNodePoolBuilder.Build()
	require.NoError(t, err)

	desiredJSON, actualJSON, err := NodePoolUpdateDispatchConfigDiffJSON(nodePool, csNodePoolBuilt)
	require.NoError(t, err)
	assert.JSONEq(t, desiredJSON, actualJSON)
	assert.Equal(t, desiredJSON, actualJSON)
	assert.NotContains(t, actualJSON, "nodeDrainTimeoutMinutes")
}

func TestNodePoolUpdateDispatchConfigJSONFromRPAndCS(t *testing.T) {
	nodePool := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Labels:                  map[string]string{"env": "prod"},
			Replicas:                3,
			NodeDrainTimeoutMinutes: ptr.To(int32(45)),
		},
	}

	csNodePoolBuilder, err := BuildCSNodePool(context.Background(), nodePool, true)
	require.NoError(t, err)
	csNodePool, err := csNodePoolBuilder.Build()
	require.NoError(t, err)

	desiredJSON, err := NodePoolUpdateDispatchConfigJSONFromRP(nodePool)
	require.NoError(t, err)
	actualJSON, err := NodePoolUpdateDispatchConfigJSONFromCS(csNodePool)
	require.NoError(t, err)

	// We assert both semantic and byte-for-byte JSON equality on purpose:
	//   - JSONEq checks that RP and CS projections represent the same config (values and structure).
	//   - Equal checks that canonicalJSON produces identical strings on both sides. The node pool
	//     service update dispatch controller uses string equality (==) for drift detection, so
	//     this must hold whenever the configs match; JSONEq alone would not catch encoding
	//     differences such as key ordering or whitespace that would cause a false drift signal.
	assert.JSONEq(t, desiredJSON, actualJSON)
	assert.Equal(t, desiredJSON, actualJSON)
	assert.Contains(t, desiredJSON, `"nodeDrainTimeoutMinutes": 45`)
	assert.Contains(t, desiredJSON, `"replicas": 3`)

	nodePool.Properties.Replicas = 5
	desiredJSON, actualJSON, err = NodePoolUpdateDispatchConfigDiffJSON(nodePool, csNodePool)
	require.NoError(t, err)
	assert.NotEqual(t, desiredJSON, actualJSON)
}

func TestNodePoolUpdateDispatchConfigTaintsFromCS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		csNodePool func(t *testing.T) *arohcpv1alpha1.NodePool
		want       []NodePoolUpdateDispatchConfigTaint
	}{
		{
			name: "no taints returns nil",
			csNodePool: func(t *testing.T) *arohcpv1alpha1.NodePool {
				t.Helper()
				csNodePool, err := arohcpv1alpha1.NewNodePool().Build()
				require.NoError(t, err)
				return csNodePool
			},
			want: nil,
		},
		{
			name: "taints with effect key and value are copied",
			csNodePool: func(t *testing.T) *arohcpv1alpha1.NodePool {
				t.Helper()
				csNodePool, err := arohcpv1alpha1.NewNodePool().
					Taints(
						arohcpv1alpha1.NewTaint().
							Effect(string(api.EffectNoSchedule)).
							Key("dedicated").
							Value("infra"),
					).
					Build()
				require.NoError(t, err)
				return csNodePool
			},
			want: []NodePoolUpdateDispatchConfigTaint{
				{Effect: string(api.EffectNoSchedule), Key: "dedicated", Value: "infra"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, NodePoolUpdateDispatchConfigTaintsFromCS(tt.csNodePool(t)))
		})
	}
}

func TestNodePoolUpdateDispatchConfigApplyToCSBuilder(t *testing.T) {
	tests := []struct {
		name   string
		config nodePoolUpdateDispatchConfig
		verify func(t *testing.T, csNodePool *arohcpv1alpha1.NodePool)
	}{
		{
			name: "sets labels replicas and taints",
			config: nodePoolUpdateDispatchConfig{
				Labels:   map[string]string{"env": "prod"},
				Replicas: 5,
				Taints: []NodePoolUpdateDispatchConfigTaint{
					{Effect: string(api.EffectNoSchedule), Key: "dedicated", Value: "infra"},
				},
			},
			verify: func(t *testing.T, csNodePool *arohcpv1alpha1.NodePool) {
				t.Helper()
				got := nodePoolUpdateDispatchConfigFromCS(csNodePool)
				assert.Equal(t, map[string]string{"env": "prod"}, got.Labels)
				assert.Equal(t, int32(5), got.Replicas)
				require.Len(t, got.Taints, 1)
				assert.Equal(t, NodePoolUpdateDispatchConfigTaint{
					Effect: string(api.EffectNoSchedule),
					Key:    "dedicated",
					Value:  "infra",
				}, got.Taints[0])
			},
		},
		{
			name: "sets autoscaling instead of replicas",
			config: nodePoolUpdateDispatchConfig{
				AutoScaling: &nodePoolUpdateDispatchConfigAutoScaling{
					Min: 2,
					Max: 10,
				},
			},
			verify: func(t *testing.T, csNodePool *arohcpv1alpha1.NodePool) {
				t.Helper()
				got := nodePoolUpdateDispatchConfigFromCS(csNodePool)
				require.NotNil(t, got.AutoScaling)
				assert.Equal(t, nodePoolUpdateDispatchConfigAutoScaling{Min: 2, Max: 10}, *got.AutoScaling)
			},
		},
		{
			name: "sets node drain grace period in minutes",
			config: nodePoolUpdateDispatchConfig{
				NodeDrainTimeoutMinutes: ptr.To(int32(45)),
			},
			verify: func(t *testing.T, csNodePool *arohcpv1alpha1.NodePool) {
				t.Helper()
				got := NodePoolUpdateDispatchConfigNodeDrainTimeoutFromCS(csNodePool)
				require.NotNil(t, got)
				assert.Equal(t, int32(45), *got)
			},
		},
		{
			name:   "nil taints does not set taints on builder",
			config: nodePoolUpdateDispatchConfig{Replicas: 3},
			verify: func(t *testing.T, csNodePool *arohcpv1alpha1.NodePool) {
				t.Helper()
				assert.Nil(t, NodePoolUpdateDispatchConfigTaintsFromCS(csNodePool))
			},
		},
		{
			name: "nil drain timeout does not set node drain grace period",
			config: nodePoolUpdateDispatchConfig{
				Replicas: 3,
			},
			verify: func(t *testing.T, csNodePool *arohcpv1alpha1.NodePool) {
				t.Helper()
				assert.Nil(t, NodePoolUpdateDispatchConfigNodeDrainTimeoutFromCS(csNodePool))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodePoolBuilder := arohcpv1alpha1.NewNodePool()
			tt.config.applyToCSBuilder(nodePoolBuilder)

			csNodePool, err := nodePoolBuilder.Build()
			require.NoError(t, err)
			tt.verify(t, csNodePool)
		})
	}
}
