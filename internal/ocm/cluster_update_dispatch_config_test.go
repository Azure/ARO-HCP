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

func TestClusterUpdateDispatchConfigHash(t *testing.T) {
	base := &api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			NodeDrainTimeoutMinutes: 30,
			API: api.CustomerAPIProfile{
				AuthorizedCIDRs: []string{"10.0.0.0/8"},
			},
			Autoscaling: api.ClusterAutoscalingProfile{
				MaxNodesTotal:            10,
				MaxPodGracePeriodSeconds: 600,
			},
		},
	}

	hash1, err := ClusterUpdateDispatchConfigHashFromRP(base)
	require.NoError(t, err)
	require.NotEmpty(t, hash1)

	hash2, err := ClusterUpdateDispatchConfigHashFromRP(base)
	require.NoError(t, err)
	assert.Equal(t, hash1, hash2)

	differentDrain := &api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			NodeDrainTimeoutMinutes: 60,
			API: api.CustomerAPIProfile{
				AuthorizedCIDRs: []string{"10.0.0.0/8"},
			},
			Autoscaling: api.ClusterAutoscalingProfile{
				MaxNodesTotal:            10,
				MaxPodGracePeriodSeconds: 600,
			},
		},
	}
	hashDifferent, err := ClusterUpdateDispatchConfigHashFromRP(differentDrain)
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hashDifferent)

	differentCIDRs := &api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			NodeDrainTimeoutMinutes: 30,
			API: api.CustomerAPIProfile{
				AuthorizedCIDRs: []string{"192.168.0.0/16"},
			},
			Autoscaling: api.ClusterAutoscalingProfile{
				MaxNodesTotal:            10,
				MaxPodGracePeriodSeconds: 600,
			},
		},
	}
	hashCIDRs, err := ClusterUpdateDispatchConfigHashFromRP(differentCIDRs)
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
				MaxNodesTotal:            10,
				MaxPodGracePeriodSeconds: 600,
			},
		},
	}
	hashMirrors, err := ClusterUpdateDispatchConfigHashFromRP(withMirrors)
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hashMirrors)

	differentAutoscaling := &api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			NodeDrainTimeoutMinutes: 30,
			API: api.CustomerAPIProfile{
				AuthorizedCIDRs: []string{"10.0.0.0/8"},
			},
			Autoscaling: api.ClusterAutoscalingProfile{
				MaxNodesTotal:            20,
				MaxPodGracePeriodSeconds: 600,
			},
		},
	}
	hashAS, err := ClusterUpdateDispatchConfigHashFromRP(differentAutoscaling)
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
	hashSizeOverride, err := ClusterUpdateDispatchConfigHashFromRP(withSizeOverride)
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hashSizeOverride)

	withCPOImageOverride := &api.HCPOpenShiftCluster{
		CustomerProperties: base.CustomerProperties,
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ExperimentalFeatures: api.ExperimentalFeatures{
				ControlPlaneOperatorImage: "quay.io/openshift/cpo:test",
			},
		},
	}
	hashCPOImageOverride, err := ClusterUpdateDispatchConfigHashFromRP(withCPOImageOverride)
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hashCPOImageOverride)
}

func TestClusterUpdateDispatchConfigHashExcludesNonUpdatableFields(t *testing.T) {
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

	hash1, err := ClusterUpdateDispatchConfigHashFromRP(cluster1)
	require.NoError(t, err)
	hash2, err := ClusterUpdateDispatchConfigHashFromRP(cluster2)
	require.NoError(t, err)
	assert.Equal(t, hash1, hash2)
}

func TestClusterUpdateDispatchConfigHashExcludesTagsWithoutExperimentalFeatures(t *testing.T) {
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

	hash1, err := ClusterUpdateDispatchConfigHashFromRP(cluster1)
	require.NoError(t, err)
	hash2, err := ClusterUpdateDispatchConfigHashFromRP(cluster2)
	require.NoError(t, err)
	assert.Equal(t, hash1, hash2)
}

func TestClusterUpdateDispatchConfigCanonicalJSON(t *testing.T) {
	config := clusterUpdateDispatchConfigFromRP(&api.HCPOpenShiftCluster{
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

	raw, err := config.canonicalJSON()
	require.NoError(t, err)

	keys, err := topLevelJSONKeys([]byte(raw))
	require.NoError(t, err)
	assert.True(t, slices.IsSorted(keys), "top-level JSON keys must be sorted: %v", keys)
	assert.Equal(t, []string{"autoscaling", "experimentalFeatures", "imageDigestMirrors", "k8sAPIServerAuthorizedCIDRs", "nodeDrainTimeoutMinutes"}, keys)
}

func TestClusterUpdateDispatchConfigFromCSRoundTrip(t *testing.T) {
	hcpCluster := &api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			NodeDrainTimeoutMinutes: 45,
			API: api.CustomerAPIProfile{
				AuthorizedCIDRs: []string{"10.0.0.0/8", "192.168.0.0/16"},
			},
			ImageDigestMirrors: []api.ImageDigestMirror{
				{Source: "quay.io/openshift-release-dev", Mirrors: []string{"mirror.example.com"}},
			},
			Autoscaling: api.ClusterAutoscalingProfile{
				MaxNodesTotal:               12,
				MaxPodGracePeriodSeconds:    600,
				MaxNodeProvisionTimeSeconds: 900,
				PodPriorityThreshold:        -10,
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ExperimentalFeatures: api.ExperimentalFeatures{
				ControlPlaneAvailability:  api.SingleReplicaControlPlane,
				ControlPlanePodSizing:     api.MinimalControlPlanePodSizing,
				ControlPlaneOperatorImage: "quay.io/openshift/cpo:test",
			},
		},
	}

	oldClusterServiceCluster, err := arohcpv1alpha1.NewCluster().Build()
	require.NoError(t, err)

	clusterBuilder, autoscalerBuilder, err := BuildCSCluster(nil, "", hcpCluster, nil, oldClusterServiceCluster)
	require.NoError(t, err)

	csCluster, err := clusterBuilder.Autoscaler(autoscalerBuilder).Build()
	require.NoError(t, err)

	actualConfig, err := clusterUpdateDispatchConfigFromCS(csCluster)
	require.NoError(t, err)

	desiredHash, err := clusterUpdateDispatchConfigFromRP(hcpCluster).hash()
	require.NoError(t, err)
	actualHash, err := actualConfig.hash()
	require.NoError(t, err)
	assert.Equal(t, desiredHash, actualHash)
}

func TestClusterUpdateDispatchConfigDiffers(t *testing.T) {
	hcpCluster := &api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			NodeDrainTimeoutMinutes: 30,
		},
	}

	oldClusterServiceCluster, err := arohcpv1alpha1.NewCluster().Build()
	require.NoError(t, err)

	matchingBuilder, matchingAutoscalerBuilder, err := BuildCSCluster(nil, "", hcpCluster, nil, oldClusterServiceCluster)
	require.NoError(t, err)
	matchingCSCluster, err := matchingBuilder.Autoscaler(matchingAutoscalerBuilder).Build()
	require.NoError(t, err)

	differs, err := ClusterUpdateDispatchConfigDiffers(hcpCluster, matchingCSCluster)
	require.NoError(t, err)
	assert.False(t, differs)

	hcpCluster.CustomerProperties.NodeDrainTimeoutMinutes = 60
	differs, err = ClusterUpdateDispatchConfigDiffers(hcpCluster, matchingCSCluster)
	require.NoError(t, err)
	assert.True(t, differs)
}

func TestClusterUpdateDispatchConfigNodeDrainTimeoutFromCS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		csCluster *arohcpv1alpha1.Cluster
		want      int32
	}{
		{
			name:      "nil cluster",
			csCluster: nil,
			want:      0,
		},
		{
			name: "unset grace period",
			csCluster: func() *arohcpv1alpha1.Cluster {
				cluster, err := arohcpv1alpha1.NewCluster().Build()
				require.NoError(t, err)
				return cluster
			}(),
			want: 0,
		},
		{
			name: "minutes unit",
			csCluster: func() *arohcpv1alpha1.Cluster {
				cluster, err := arohcpv1alpha1.NewCluster().
					NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
						Unit(csNodeDrainGracePeriodUnit).
						Value(float64(45))).
					Build()
				require.NoError(t, err)
				return cluster
			}(),
			want: 45,
		},
		{
			name: "non-minutes unit treated as unset",
			csCluster: func() *arohcpv1alpha1.Cluster {
				cluster, err := arohcpv1alpha1.NewCluster().
					NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
						Unit("hours").
						Value(float64(1))).
					Build()
				require.NoError(t, err)
				return cluster
			}(),
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ClusterUpdateDispatchConfigNodeDrainTimeoutFromCS(tt.csCluster))
		})
	}
}

func TestClusterUpdateDispatchConfigJSONFromRPAndCS(t *testing.T) {
	hcpCluster := &api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			NodeDrainTimeoutMinutes: 45,
			API: api.CustomerAPIProfile{
				AuthorizedCIDRs: []string{"10.0.0.0/8"},
			},
		},
	}

	oldClusterServiceCluster, err := arohcpv1alpha1.NewCluster().Build()
	require.NoError(t, err)

	clusterBuilder, autoscalerBuilder, err := BuildCSCluster(nil, "", hcpCluster, nil, oldClusterServiceCluster)
	require.NoError(t, err)
	csCluster, err := clusterBuilder.Autoscaler(autoscalerBuilder).Build()
	require.NoError(t, err)

	desiredJSON, err := ClusterUpdateDispatchConfigJSONFromRP(hcpCluster)
	require.NoError(t, err)
	actualJSON, err := ClusterUpdateDispatchConfigJSONFromCS(csCluster)
	require.NoError(t, err)
	assert.JSONEq(t, desiredJSON, actualJSON)
	assert.Contains(t, desiredJSON, `"nodeDrainTimeoutMinutes":45`)
}

func TestApplyClusterUpdateDispatchConfigExperimentalProperties(t *testing.T) {
	clusterBuilder := arohcpv1alpha1.NewCluster()
	clusterAPIBuilder := arohcpv1alpha1.NewClusterAPI()

	t.Run("enables both experimental properties", func(t *testing.T) {
		properties := map[string]string{}
		err := (&clusterUpdateDispatchConfig{
			ExperimentalFeatures: clusterUpdateDispatchConfigExperimentalFeatures{
				ControlPlaneAvailability: api.SingleReplicaControlPlane,
				ControlPlanePodSizing:    api.MinimalControlPlanePodSizing,
			},
		}).applyToCSBuilders(clusterBuilder, clusterAPIBuilder, properties)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{
			CSPropertySingleReplica: CSPropertyEnabled,
			CSPropertySizeOverride:  CSPropertyEnabled,
		}, properties)
	})

	t.Run("deletes experimental properties when disabled", func(t *testing.T) {
		properties := map[string]string{
			CSPropertySingleReplica:    CSPropertyEnabled,
			CSPropertySizeOverride:     CSPropertyEnabled,
			CSPropertyCPOImageOverride: "quay.io/openshift/cpo:old",
			"other":                    "value",
		}
		err := (&clusterUpdateDispatchConfig{}).applyToCSBuilders(clusterBuilder, clusterAPIBuilder, properties)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"other": "value"}, properties)
	})

	t.Run("nil properties is treated as empty map", func(t *testing.T) {
		err := (&clusterUpdateDispatchConfig{
			ExperimentalFeatures: clusterUpdateDispatchConfigExperimentalFeatures{
				ControlPlanePodSizing: api.MinimalControlPlanePodSizing,
			},
		}).applyToCSBuilders(clusterBuilder, clusterAPIBuilder, nil)
		require.NoError(t, err)
	})

	t.Run("overrides conflicting caller properties", func(t *testing.T) {
		properties := map[string]string{
			CSPropertySingleReplica: "false",
			CSPropertySizeOverride:  "false",
		}
		err := (&clusterUpdateDispatchConfig{
			ExperimentalFeatures: clusterUpdateDispatchConfigExperimentalFeatures{
				ControlPlaneAvailability: api.SingleReplicaControlPlane,
				ControlPlanePodSizing:    api.MinimalControlPlanePodSizing,
			},
		}).applyToCSBuilders(clusterBuilder, clusterAPIBuilder, properties)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{
			CSPropertySingleReplica: CSPropertyEnabled,
			CSPropertySizeOverride:  CSPropertyEnabled,
		}, properties)
	})

	t.Run("sets CPO image override property", func(t *testing.T) {
		properties := map[string]string{}
		err := (&clusterUpdateDispatchConfig{
			ExperimentalFeatures: clusterUpdateDispatchConfigExperimentalFeatures{
				ControlPlaneOperatorImage: "quay.io/openshift/cpo:test",
			},
		}).applyToCSBuilders(clusterBuilder, clusterAPIBuilder, properties)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{
			CSPropertyCPOImageOverride: "quay.io/openshift/cpo:test",
		}, properties)
	})

	t.Run("overrides conflicting CPO image property", func(t *testing.T) {
		properties := map[string]string{
			CSPropertyCPOImageOverride: "quay.io/openshift/cpo:old",
		}
		err := (&clusterUpdateDispatchConfig{
			ExperimentalFeatures: clusterUpdateDispatchConfigExperimentalFeatures{
				ControlPlaneOperatorImage: "quay.io/openshift/cpo:test",
			},
		}).applyToCSBuilders(clusterBuilder, clusterAPIBuilder, properties)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{
			CSPropertyCPOImageOverride: "quay.io/openshift/cpo:test",
		}, properties)
	})
}

func TestClusterUpdateDispatchConfigExperimentalFeaturesFromCS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		csCluster *arohcpv1alpha1.Cluster
		want      clusterUpdateDispatchConfigExperimentalFeatures
	}{
		{
			name:      "nil cluster",
			csCluster: nil,
			want:      clusterUpdateDispatchConfigExperimentalFeatures{},
		},
		{
			name: "single replica enabled",
			csCluster: func() *arohcpv1alpha1.Cluster {
				cluster, err := arohcpv1alpha1.NewCluster().Properties(map[string]string{
					CSPropertySingleReplica: CSPropertyEnabled,
				}).Build()
				require.NoError(t, err)
				return cluster
			}(),
			want: clusterUpdateDispatchConfigExperimentalFeatures{
				ControlPlaneAvailability: api.SingleReplicaControlPlane,
			},
		},
		{
			name: "single replica disabled value ignored",
			csCluster: func() *arohcpv1alpha1.Cluster {
				cluster, err := arohcpv1alpha1.NewCluster().Properties(map[string]string{
					CSPropertySingleReplica: "false",
				}).Build()
				require.NoError(t, err)
				return cluster
			}(),
			want: clusterUpdateDispatchConfigExperimentalFeatures{},
		},
		{
			name: "size override enabled",
			csCluster: func() *arohcpv1alpha1.Cluster {
				cluster, err := arohcpv1alpha1.NewCluster().Properties(map[string]string{
					CSPropertySizeOverride: CSPropertyEnabled,
				}).Build()
				require.NoError(t, err)
				return cluster
			}(),
			want: clusterUpdateDispatchConfigExperimentalFeatures{
				ControlPlanePodSizing: api.MinimalControlPlanePodSizing,
			},
		},
		{
			name: "size override disabled value ignored",
			csCluster: func() *arohcpv1alpha1.Cluster {
				cluster, err := arohcpv1alpha1.NewCluster().Properties(map[string]string{
					CSPropertySizeOverride: "false",
				}).Build()
				require.NoError(t, err)
				return cluster
			}(),
			want: clusterUpdateDispatchConfigExperimentalFeatures{},
		},
		{
			name: "CPO image override",
			csCluster: func() *arohcpv1alpha1.Cluster {
				cluster, err := arohcpv1alpha1.NewCluster().Properties(map[string]string{
					CSPropertyCPOImageOverride: "quay.io/openshift/cpo:test",
				}).Build()
				require.NoError(t, err)
				return cluster
			}(),
			want: clusterUpdateDispatchConfigExperimentalFeatures{
				ControlPlaneOperatorImage: "quay.io/openshift/cpo:test",
			},
		},
		{
			name: "empty CPO image override ignored",
			csCluster: func() *arohcpv1alpha1.Cluster {
				cluster, err := arohcpv1alpha1.NewCluster().Properties(map[string]string{
					CSPropertyCPOImageOverride: "",
				}).Build()
				require.NoError(t, err)
				return cluster
			}(),
			want: clusterUpdateDispatchConfigExperimentalFeatures{},
		},
		{
			name: "all experimental properties enabled",
			csCluster: func() *arohcpv1alpha1.Cluster {
				cluster, err := arohcpv1alpha1.NewCluster().Properties(map[string]string{
					CSPropertySingleReplica:    CSPropertyEnabled,
					CSPropertySizeOverride:     CSPropertyEnabled,
					CSPropertyCPOImageOverride: "quay.io/openshift/cpo:test",
				}).Build()
				require.NoError(t, err)
				return cluster
			}(),
			want: clusterUpdateDispatchConfigExperimentalFeatures{
				ControlPlaneAvailability:  api.SingleReplicaControlPlane,
				ControlPlanePodSizing:     api.MinimalControlPlanePodSizing,
				ControlPlaneOperatorImage: "quay.io/openshift/cpo:test",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, clusterUpdateDispatchConfigExperimentalFeaturesFromCS(tt.csCluster))
		})
	}
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
