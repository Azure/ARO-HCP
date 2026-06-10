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
	"encoding/json"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestClusterUpdatableConfigHash(t *testing.T) {
	base := &api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			NodeDrainTimeoutMinutes: 30,
			API: api.CustomerAPIProfile{
				AuthorizedCIDRs: []string{"10.0.0.0/8"},
			},
			Autoscaling: api.ClusterAutoscalingProfile{
				MaxNodesTotal:    10,
				MaxPodGracePeriodSeconds: 600,
			},
		},
	}

	hash1, err := ClusterUpdatableConfigHash(base)
	require.NoError(t, err)
	require.NotEmpty(t, hash1)

	hash2, err := ClusterUpdatableConfigHash(base)
	require.NoError(t, err)
	assert.Equal(t, hash1, hash2)

	differentDrain := &api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			NodeDrainTimeoutMinutes: 60,
			API: api.CustomerAPIProfile{
				AuthorizedCIDRs: []string{"10.0.0.0/8"},
			},
			Autoscaling: api.ClusterAutoscalingProfile{
				MaxNodesTotal:    10,
				MaxPodGracePeriodSeconds: 600,
			},
		},
	}
	hashDifferent, err := ClusterUpdatableConfigHash(differentDrain)
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hashDifferent)

	differentCIDRs := &api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			NodeDrainTimeoutMinutes: 30,
			API: api.CustomerAPIProfile{
				AuthorizedCIDRs: []string{"192.168.0.0/16"},
			},
			Autoscaling: api.ClusterAutoscalingProfile{
				MaxNodesTotal:    10,
				MaxPodGracePeriodSeconds: 600,
			},
		},
	}
	hashCIDRs, err := ClusterUpdatableConfigHash(differentCIDRs)
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hashCIDRs)

	withMirrors := &api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			NodeDrainTimeoutMinutes: 30,
			API: api.CustomerAPIProfile{
				AuthorizedCIDRs: []string{"10.0.0.0/8"},
			},
			ImageDigestMirrors: []api.ImageDigestMirror{
				{Source: "quay.io/openshift-release-dev", Mirrors: []string{"mirror.example.com"}},
			},
			Autoscaling: api.ClusterAutoscalingProfile{
				MaxNodesTotal:    10,
				MaxPodGracePeriodSeconds: 600,
			},
		},
	}
	hashMirrors, err := ClusterUpdatableConfigHash(withMirrors)
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hashMirrors)

	differentAutoscaling := &api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			NodeDrainTimeoutMinutes: 30,
			API: api.CustomerAPIProfile{
				AuthorizedCIDRs: []string{"10.0.0.0/8"},
			},
			Autoscaling: api.ClusterAutoscalingProfile{
				MaxNodesTotal:    20,
				MaxPodGracePeriodSeconds: 600,
			},
		},
	}
	hashAS, err := ClusterUpdatableConfigHash(differentAutoscaling)
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hashAS)

	withSizeOverride := &api.HCPOpenShiftCluster{
		CustomerProperties: base.CustomerProperties,
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ExperimentalFeatures: api.ExperimentalFeatures{
				ControlPlanePodSizing: api.MinimalControlPlanePodSizing,
			},
		},
	}
	hashSizeOverride, err := ClusterUpdatableConfigHash(withSizeOverride)
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hashSizeOverride)
}

func TestClusterUpdatableConfigHashExcludesNonUpdatableFields(t *testing.T) {
	cluster1 := &api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			NodeDrainTimeoutMinutes: 30,
			Version:                 api.VersionProfile{ID: "4.19.1"},
			Network: api.NetworkProfile{
				PodCIDR: "10.128.0.0/14",
			},
		},
	}

	cluster2 := &api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			NodeDrainTimeoutMinutes: 30,
			Version:                 api.VersionProfile{ID: "4.19.2"},
			Network: api.NetworkProfile{
				PodCIDR: "10.200.0.0/14",
			},
		},
	}

	hash1, err := ClusterUpdatableConfigHash(cluster1)
	require.NoError(t, err)
	hash2, err := ClusterUpdatableConfigHash(cluster2)
	require.NoError(t, err)
	assert.Equal(t, hash1, hash2)
}

func TestClusterUpdatableConfigHashExcludesTagsWithoutExperimentalFeatures(t *testing.T) {
	cluster1 := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Tags: map[string]string{api.TagClusterSizeOverride: string(api.MinimalControlPlanePodSizing)},
		},
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			NodeDrainTimeoutMinutes: 30,
		},
	}
	cluster2 := &api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			NodeDrainTimeoutMinutes: 30,
		},
	}

	hash1, err := ClusterUpdatableConfigHash(cluster1)
	require.NoError(t, err)
	hash2, err := ClusterUpdatableConfigHash(cluster2)
	require.NoError(t, err)
	assert.Equal(t, hash1, hash2)
}

func TestClusterUpdatableConfigJSONForHashIsCanonical(t *testing.T) {
	config := ClusterUpdatableConfigFromCluster(&api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			NodeDrainTimeoutMinutes: 30,
			API: api.CustomerAPIProfile{
				AuthorizedCIDRs: []string{"10.0.0.0/8"},
			},
			ImageDigestMirrors: []api.ImageDigestMirror{
				{Source: "quay.io", Mirrors: []string{"mirror.example.com"}},
			},
			Autoscaling: api.ClusterAutoscalingProfile{
				MaxNodesTotal: 10,
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ExperimentalFeatures: api.ExperimentalFeatures{
				ControlPlaneAvailability: api.SingleReplicaControlPlane,
			},
		},
	})

	raw, err := clusterUpdatableConfigJSONForHash(config)
	require.NoError(t, err)

	keys, err := topLevelJSONKeys(raw)
	require.NoError(t, err)
	assert.True(t, slices.IsSorted(keys), "top-level JSON keys must be sorted: %v", keys)
	assert.Equal(t, []string{"authorizedCidrs", "autoscaling", "experimentalFeatures", "imageDigestMirrors", "nodeDrainTimeoutMinutes"}, keys)
}

func TestApplyClusterUpdatableConfigExperimentalProperties(t *testing.T) {
	clusterBuilder := arohcpv1alpha1.NewCluster()
	clusterAPIBuilder := arohcpv1alpha1.NewClusterAPI()

	t.Run("enables both experimental properties", func(t *testing.T) {
		properties := map[string]string{}
		err := applyClusterUpdatableConfig(clusterBuilder, clusterAPIBuilder, properties, &clusterUpdatableConfig{
			ExperimentalFeatures: clusterUpdatableExperimentalFeatures{
				ControlPlaneAvailability: api.SingleReplicaControlPlane,
				ControlPlanePodSizing:    api.MinimalControlPlanePodSizing,
			},
		})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{
			CSPropertySingleReplica: CSPropertyEnabled,
			CSPropertySizeOverride:  CSPropertyEnabled,
		}, properties)
	})

	t.Run("deletes experimental properties when disabled", func(t *testing.T) {
		properties := map[string]string{
			CSPropertySingleReplica: CSPropertyEnabled,
			CSPropertySizeOverride:  CSPropertyEnabled,
			"other":                 "value",
		}
		err := applyClusterUpdatableConfig(clusterBuilder, clusterAPIBuilder, properties, &clusterUpdatableConfig{})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"other": "value"}, properties)
	})

	t.Run("nil properties is treated as empty map", func(t *testing.T) {
		err := applyClusterUpdatableConfig(clusterBuilder, clusterAPIBuilder, nil, &clusterUpdatableConfig{
			ExperimentalFeatures: clusterUpdatableExperimentalFeatures{
				ControlPlanePodSizing: api.MinimalControlPlanePodSizing,
			},
		})
		require.NoError(t, err)
	})

	t.Run("overrides conflicting caller properties", func(t *testing.T) {
		properties := map[string]string{
			CSPropertySingleReplica: "false",
			CSPropertySizeOverride:  "false",
		}
		err := applyClusterUpdatableConfig(clusterBuilder, clusterAPIBuilder, properties, &clusterUpdatableConfig{
			ExperimentalFeatures: clusterUpdatableExperimentalFeatures{
				ControlPlaneAvailability: api.SingleReplicaControlPlane,
				ControlPlanePodSizing:    api.MinimalControlPlanePodSizing,
			},
		})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{
			CSPropertySingleReplica: CSPropertyEnabled,
			CSPropertySizeOverride:  CSPropertyEnabled,
		}, properties)
	})
}

func topLevelJSONKeys(raw []byte) ([]string, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(payload))
	for k := range payload {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys, nil
}
