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
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api"
)

func TestNodePoolUpdatableConfigHash(t *testing.T) {
	base := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Replicas: 3,
			Labels:   map[string]string{"role": "worker"},
		},
	}

	hash1, err := NodePoolUpdatableConfigHash(base)
	require.NoError(t, err)
	require.NotEmpty(t, hash1)

	hash2, err := NodePoolUpdatableConfigHash(base)
	require.NoError(t, err)
	assert.Equal(t, hash1, hash2)

	withTaintOrder := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Replicas: 1,
			Taints: []api.Taint{
				{Key: "b", Value: "v2", Effect: api.EffectNoSchedule},
				{Key: "a", Value: "v1", Effect: api.EffectNoExecute},
			},
		},
	}
	reordered := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Replicas: 1,
			Taints: []api.Taint{
				{Key: "a", Value: "v1", Effect: api.EffectNoExecute},
				{Key: "b", Value: "v2", Effect: api.EffectNoSchedule},
			},
		},
	}

	hashA, err := NodePoolUpdatableConfigHash(withTaintOrder)
	require.NoError(t, err)
	hashB, err := NodePoolUpdatableConfigHash(reordered)
	require.NoError(t, err)
	assert.NotEqual(t, hashA, hashB)

	autoscaling := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Replicas:    99,
			AutoScaling: &api.NodePoolAutoScaling{Min: 2, Max: 5},
		},
	}
	autoscalingHash, err := NodePoolUpdatableConfigHash(autoscaling)
	require.NoError(t, err)

	fixedReplicas := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Replicas: 99,
		},
	}
	fixedReplicasHash, err := NodePoolUpdatableConfigHash(fixedReplicas)
	require.NoError(t, err)
	assert.NotEqual(t, autoscalingHash, fixedReplicasHash)

	withDrainTimeout := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Replicas:                1,
			NodeDrainTimeoutMinutes: ptr.To(int32(30)),
		},
	}
	drainHash, err := NodePoolUpdatableConfigHash(withDrainTimeout)
	require.NoError(t, err)
	assert.NotEqual(t, fixedReplicasHash, drainHash)
}

func TestNodePoolUpdatableConfigJSONForHashIsCanonical(t *testing.T) {
	config := NodePoolUpdatableConfigFromProperties(api.HCPOpenShiftClusterNodePoolProperties{
		Replicas:                2,
		Labels:                  map[string]string{"role": "worker"},
		NodeDrainTimeoutMinutes: ptr.To(int32(15)),
		Taints: []api.Taint{
			{Key: "k", Value: "v", Effect: api.EffectNoSchedule},
		},
	})

	raw, err := nodePoolUpdatableConfigJSONForHash(config)
	require.NoError(t, err)

	keys, err := topLevelJSONKeys(raw)
	require.NoError(t, err)
	assert.True(t, slices.IsSorted(keys), "top-level JSON keys must be sorted: %v", keys)
	assert.Equal(t, []string{"labels", "nodeDrainTimeoutMinutes", "replicas", "taints"}, keys)
}

func topLevelJSONKeys(raw []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("expected JSON object, got %v", tok)
	}

	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("expected object key, got %v", tok)
		}
		keys = append(keys, key)

		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, err
		}
	}
	return keys, nil
}
