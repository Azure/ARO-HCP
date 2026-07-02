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
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api"
)

func TestNodePoolUpdateDispatchConfigHash(t *testing.T) {
	base := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Labels:   map[string]string{"env": "prod"},
			Replicas: 3,
			Taints: []api.Taint{
				{Effect: api.EffectNoSchedule, Key: "key1", Value: "val1"},
			},
			NodeDrainTimeoutMinutes: ptr.To(int32(30)),
		},
	}

	hash1, err := nodePoolUpdateDispatchConfigHash(base)
	require.NoError(t, err)
	require.NotEmpty(t, hash1)

	hash2, err := nodePoolUpdateDispatchConfigHash(base)
	require.NoError(t, err)
	assert.Equal(t, hash1, hash2)

	differentLabels := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Labels:                  map[string]string{"env": "staging"},
			Replicas:                3,
			Taints:                  base.Properties.Taints,
			NodeDrainTimeoutMinutes: ptr.To(int32(30)),
		},
	}
	hashDifferentLabels, err := nodePoolUpdateDispatchConfigHash(differentLabels)
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hashDifferentLabels)

	differentReplicas := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Labels:                  map[string]string{"env": "prod"},
			Replicas:                5,
			Taints:                  base.Properties.Taints,
			NodeDrainTimeoutMinutes: ptr.To(int32(30)),
		},
	}
	hashDifferentReplicas, err := nodePoolUpdateDispatchConfigHash(differentReplicas)
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hashDifferentReplicas)

	withAutoScaling := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Labels:                  map[string]string{"env": "prod"},
			AutoScaling:             &api.NodePoolAutoScaling{Min: 1, Max: 10},
			Taints:                  base.Properties.Taints,
			NodeDrainTimeoutMinutes: ptr.To(int32(30)),
		},
	}
	hashAutoScaling, err := nodePoolUpdateDispatchConfigHash(withAutoScaling)
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hashAutoScaling)

	differentTaints := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Labels:   map[string]string{"env": "prod"},
			Replicas: 3,
			Taints: []api.Taint{
				{Effect: api.EffectNoExecute, Key: "key2", Value: "val2"},
			},
			NodeDrainTimeoutMinutes: ptr.To(int32(30)),
		},
	}
	hashDifferentTaints, err := nodePoolUpdateDispatchConfigHash(differentTaints)
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hashDifferentTaints)

	differentDrain := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Labels:                  map[string]string{"env": "prod"},
			Replicas:                3,
			Taints:                  base.Properties.Taints,
			NodeDrainTimeoutMinutes: ptr.To(int32(60)),
		},
	}
	hashDifferentDrain, err := nodePoolUpdateDispatchConfigHash(differentDrain)
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hashDifferentDrain)
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

func TestNodePoolUpdateDispatchConfigCanonicalJSON(t *testing.T) {
	config := nodePoolUpdateDispatchConfigFromRP(&api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Labels:                  map[string]string{"env": "prod"},
			Replicas:                3,
			AutoRepair:              true,
			Taints:                  []api.Taint{{Effect: api.EffectNoSchedule, Key: "k", Value: "v"}},
			NodeDrainTimeoutMinutes: ptr.To(int32(30)),
		},
	})

	raw, err := config.canonicalJSON()
	require.NoError(t, err)

	keys, err := topLevelJSONKeys(raw)
	require.NoError(t, err)
	assert.True(t, slices.IsSorted(keys), "top-level JSON keys must be sorted: %v", keys)
	assert.Equal(t, []string{"labels", "nodeDrainTimeoutMinutes", "replicas", "taints"}, keys)
}

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

func TestNodePoolUpdateDispatchConfigDiffers(t *testing.T) {
	nodePool := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Replicas:                3,
			NodeDrainTimeoutMinutes: ptr.To(int32(30)),
		},
	}

	csNodePoolBuilder, err := BuildCSNodePool(context.Background(), nodePool, true)
	require.NoError(t, err)
	csNodePool, err := csNodePoolBuilder.Build()
	require.NoError(t, err)

	differs, err := NodePoolUpdateDispatchConfigDiffers(nodePool, csNodePool)
	require.NoError(t, err)
	assert.False(t, differs)

	nodePool.Properties.Replicas = 5
	differs, err = NodePoolUpdateDispatchConfigDiffers(nodePool, csNodePool)
	require.NoError(t, err)
	assert.True(t, differs)
}

func TestNodePoolUpdateDispatchConfigDiffersIgnoresCSDrainTimeoutWhenRPUnset(t *testing.T) {
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

	differs, err := NodePoolUpdateDispatchConfigDiffers(nodePool, csNodePoolBuilt)
	require.NoError(t, err)
	assert.False(t, differs, "RP nil drain timeout must not fight CS frozen value")
}

func TestNodePoolUpdateDispatchConfigEffectiveNodeDrainTimeoutMinutes(t *testing.T) {
	t.Run("explicit RP override", func(t *testing.T) {
		nodePool := &api.HCPOpenShiftClusterNodePool{
			Properties: api.HCPOpenShiftClusterNodePoolProperties{
				NodeDrainTimeoutMinutes: ptr.To(int32(15)),
			},
		}
		csNodePoolBuilder, err := BuildCSNodePool(context.Background(), &api.HCPOpenShiftClusterNodePool{
			Properties: api.HCPOpenShiftClusterNodePoolProperties{
				NodeDrainTimeoutMinutes: ptr.To(int32(4)),
			},
		}, true)
		require.NoError(t, err)
		csNodePool, err := csNodePoolBuilder.Build()
		require.NoError(t, err)

		got := NodePoolUpdateDispatchConfigEffectiveNodeDrainTimeoutMinutes(nodePool, csNodePool)
		require.NotNil(t, got)
		assert.Equal(t, int32(15), *got)
	})

	t.Run("RP unset uses CS", func(t *testing.T) {
		nodePool := &api.HCPOpenShiftClusterNodePool{}
		csNodePoolBuilder, err := BuildCSNodePool(context.Background(), &api.HCPOpenShiftClusterNodePool{
			Properties: api.HCPOpenShiftClusterNodePoolProperties{
				NodeDrainTimeoutMinutes: ptr.To(int32(4)),
			},
		}, true)
		require.NoError(t, err)
		csNodePool, err := csNodePoolBuilder.Build()
		require.NoError(t, err)

		got := NodePoolUpdateDispatchConfigEffectiveNodeDrainTimeoutMinutes(nodePool, csNodePool)
		require.NotNil(t, got)
		assert.Equal(t, int32(4), *got)
	})
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
	assert.JSONEq(t, desiredJSON, actualJSON)
	assert.Contains(t, desiredJSON, `"nodeDrainTimeoutMinutes":45`)
	assert.Contains(t, desiredJSON, `"replicas":3`)
}
