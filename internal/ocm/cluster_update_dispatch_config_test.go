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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestClusterUpdateDispatchConfigHash(t *testing.T) {
	baseCustomerProperties := api.HCPOpenShiftClusterCustomerProperties{
		NodeDrainTimeoutMinutes: 30,
		API: api.CustomerAPIProfile{
			AuthorizedCIDRs: []string{"10.0.0.0/8"},
		},
		Autoscaling: api.ClusterAutoscalingProfile{
			MaxNodesTotal:            10,
			MaxPodGracePeriodSeconds: 600,
		},
	}

	base := &api.HCPOpenShiftCluster{
		CustomerProperties: baseCustomerProperties,
	}

	baseHash, err := clusterUpdateDispatchConfigHash(base, nil)
	require.NoError(t, err)
	require.NotEmpty(t, baseHash)

	hashAgain, err := clusterUpdateDispatchConfigHash(base, nil)
	require.NoError(t, err)
	assert.Equal(t, baseHash, hashAgain)

	tests := []struct {
		name    string
		cluster *api.HCPOpenShiftCluster
		spc     *api.ServiceProviderCluster
	}{
		{
			name: "different node drain timeout",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					NodeDrainTimeoutMinutes: 60,
					API:                     baseCustomerProperties.API,
					Autoscaling:             baseCustomerProperties.Autoscaling,
				},
			},
		},
		{
			name: "different authorized CIDRs",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					NodeDrainTimeoutMinutes: baseCustomerProperties.NodeDrainTimeoutMinutes,
					API: api.CustomerAPIProfile{
						AuthorizedCIDRs: []string{"192.168.0.0/16"},
					},
					Autoscaling: baseCustomerProperties.Autoscaling,
				},
			},
		},
		{
			name: "image digest mirrors",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					NodeDrainTimeoutMinutes: baseCustomerProperties.NodeDrainTimeoutMinutes,
					API:                     baseCustomerProperties.API,
					ImageDigestMirrors: []api.ImageDigestMirror{
						{Source: "quay.io/openshift-release-dev", Mirrors: []string{"mirror.example.com"}},
					},
					Autoscaling: baseCustomerProperties.Autoscaling,
				},
			},
		},
		{
			name: "different autoscaling",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					NodeDrainTimeoutMinutes: baseCustomerProperties.NodeDrainTimeoutMinutes,
					API:                     baseCustomerProperties.API,
					Autoscaling: api.ClusterAutoscalingProfile{
						MaxNodesTotal:            20,
						MaxPodGracePeriodSeconds: baseCustomerProperties.Autoscaling.MaxPodGracePeriodSeconds,
					},
				},
			},
		},
		{
			name: "control plane availability single replica",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: baseCustomerProperties,
				ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
					ExperimentalFeatures: api.ExperimentalFeatures{
						ControlPlaneAvailability: api.SingleReplicaControlPlane,
					},
				},
			},
		},
		{
			name: "control plane pod sizing",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: baseCustomerProperties,
				ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
					ExperimentalFeatures: api.ExperimentalFeatures{
						ControlPlanePodSizing: api.MinimalControlPlanePodSizing,
					},
				},
			},
		},
		{
			name: "control plane operator image",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: baseCustomerProperties,
				ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
					ExperimentalFeatures: api.ExperimentalFeatures{
						ControlPlaneOperatorImage: "quay.io/openshift/cpo:test",
					},
				},
			},
		},
		{
			name: "service provider cluster control plane size",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: baseCustomerProperties,
			},
			spc: &api.ServiceProviderCluster{
				Spec: api.ServiceProviderClusterSpec{
					DesiredHostedClusterControlPlaneSize: ptr.To("Large"),
				},
			},
		},
		{
			name: "different KMS key version",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					NodeDrainTimeoutMinutes: baseCustomerProperties.NodeDrainTimeoutMinutes,
					API:                     baseCustomerProperties.API,
					Autoscaling:             baseCustomerProperties.Autoscaling,
					Etcd: api.EtcdProfile{
						DataEncryption: api.EtcdDataEncryptionProfile{
							KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
							CustomerManaged: &api.CustomerManagedEncryptionProfile{
								EncryptionType: api.CustomerManagedEncryptionTypeKMS,
								Kms: &api.KmsEncryptionProfile{
									ActiveKey: api.KmsKey{
										Version: "v1",
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "container registry pull managed identity",
			cluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					NodeDrainTimeoutMinutes: baseCustomerProperties.NodeDrainTimeoutMinutes,
					API:                     baseCustomerProperties.API,
					Autoscaling:             baseCustomerProperties.Autoscaling,
					Platform: api.CustomerPlatformProfile{
						ContainerRegistryPullManagedIdentity: api.NewTestUserAssignedIdentity("cr-pull-mi"),
					},
				},
			},
		},
	}

	// Each row changes one dispatch-managed field from the baseline above. Comparing against
	// baseHash checks that the field is included in the canonical hash. It does not prove the
	// field mapped correctly. See FromCS round-trip and per-helper tests for that.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash, err := clusterUpdateDispatchConfigHash(tt.cluster, tt.spc)
			require.NoError(t, err)
			assert.NotEqual(t, baseHash, hash, "changing %q should change the dispatch config hash", tt.name)
		})
	}
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

	hash1, err := clusterUpdateDispatchConfigHash(cluster1, &api.ServiceProviderCluster{})
	require.NoError(t, err)
	hash2, err := clusterUpdateDispatchConfigHash(cluster2, &api.ServiceProviderCluster{})
	require.NoError(t, err)
	assert.Equal(t, hash1, hash2)

	// Immutable etcd fields (keyName, vaultName, visibility) should not affect the hash.
	// Only the key version is mutable and dispatch-managed.
	cluster3 := &api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			NodeDrainTimeoutMinutes: 30,
			Etcd: api.EtcdProfile{
				DataEncryption: api.EtcdDataEncryptionProfile{
					KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
					CustomerManaged: &api.CustomerManagedEncryptionProfile{
						EncryptionType: api.CustomerManagedEncryptionTypeKMS,
						Kms: &api.KmsEncryptionProfile{
							Visibility: api.KeyVaultVisibilityPublic,
							ActiveKey: api.KmsKey{
								Name:      "key-A",
								VaultName: "vault-A",
								Version:   "v1",
							},
						},
					},
				},
			},
		},
	}
	cluster4 := &api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			NodeDrainTimeoutMinutes: 30,
			Etcd: api.EtcdProfile{
				DataEncryption: api.EtcdDataEncryptionProfile{
					KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
					CustomerManaged: &api.CustomerManagedEncryptionProfile{
						EncryptionType: api.CustomerManagedEncryptionTypeKMS,
						Kms: &api.KmsEncryptionProfile{
							Visibility: api.KeyVaultVisibilityPrivate,
							ActiveKey: api.KmsKey{
								Name:      "key-B",
								VaultName: "vault-B",
								Version:   "v1",
							},
						},
					},
				},
			},
		},
	}
	hash3, err := clusterUpdateDispatchConfigHash(cluster3, &api.ServiceProviderCluster{})
	require.NoError(t, err)
	hash4, err := clusterUpdateDispatchConfigHash(cluster4, &api.ServiceProviderCluster{})
	require.NoError(t, err)
	assert.Equal(t, hash3, hash4, "changing immutable etcd fields (keyName, vaultName, visibility) should not change the dispatch config hash")
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

	hash1, err := clusterUpdateDispatchConfigHash(cluster1, &api.ServiceProviderCluster{})
	require.NoError(t, err)
	hash2, err := clusterUpdateDispatchConfigHash(cluster2, &api.ServiceProviderCluster{})
	require.NoError(t, err)
	assert.Equal(t, hash1, hash2)
}

// TestClusterUpdateDispatchConfigFromCSRoundTrip checks that RP and Cluster Service agree on
// dispatch-managed config after materializing RP desired state onto an existing Cluster Service
// cluster via BuildCSCluster (update path: non-nil old cluster). clusterUpdateDispatchConfigFromCS
// and clusterUpdateDispatchConfigFromRP must then produce the same canonical hash.
func TestClusterUpdateDispatchConfigFromCSRoundTrip(t *testing.T) {
	resourceID, err := azcorearm.ParseResourceID("/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/testResourceGroup/providers/Microsoft.RedHatOpenShift/openShiftClusters/testCluster")
	require.NoError(t, err)

	oldClusterServiceCluster, err := arohcpv1alpha1.NewCluster().Build()
	require.NoError(t, err)

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
	spc := &api.ServiceProviderCluster{}

	clusterBuilder, err := BuildCSCluster(resourceID, api.TestTenantID, hcpCluster, nil, oldClusterServiceCluster, spc)
	require.NoError(t, err)

	csCluster, err := clusterBuilder.Build()
	require.NoError(t, err)

	actualConfig, err := clusterUpdateDispatchConfigFromCS(csCluster)
	require.NoError(t, err)

	desiredHash, err := clusterUpdateDispatchConfigFromRP(hcpCluster, spc).hash()
	require.NoError(t, err)
	actualHash, err := actualConfig.hash()
	require.NoError(t, err)
	assert.Equal(t, desiredHash, actualHash)
}

// TestClusterUpdateDispatchConfigFromCSRoundTripServiceProviderClusterSize checks that RP
// desired state with a ServiceProviderCluster-level size override round-trips through
// BuildCSCluster and clusterUpdateDispatchConfigFromCS with matching hash and field split.
func TestClusterUpdateDispatchConfigFromCSRoundTripServiceProviderClusterSize(t *testing.T) {
	oldClusterServiceCluster, err := arohcpv1alpha1.NewCluster().Build()
	require.NoError(t, err)

	hcpCluster := &api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			NodeDrainTimeoutMinutes: 30,
		},
	}
	spc := &api.ServiceProviderCluster{
		Spec: api.ServiceProviderClusterSpec{
			// Use lowercase to match the value CS stores after ConvertHostedClusterSizeOverrideToCS.
			DesiredHostedClusterControlPlaneSize: ptr.To("large"),
		},
	}

	clusterBuilder, err := BuildCSCluster(nil, "11111111-1111-1111-1111-111111111111", hcpCluster, nil, oldClusterServiceCluster, spc)
	require.NoError(t, err)

	csCluster, err := clusterBuilder.Build()
	require.NoError(t, err)

	actualConfig, err := clusterUpdateDispatchConfigFromCS(csCluster)
	require.NoError(t, err)

	assert.Equal(t, clusterUpdateDispatchConfigExperimentalFeatures{}, actualConfig.ExperimentalFeatures)
	require.NotNil(t, actualConfig.ServiceProviderClusterDispatch.DesiredHostedClusterControlPlaneSize)
	assert.Equal(t, "large", *actualConfig.ServiceProviderClusterDispatch.DesiredHostedClusterControlPlaneSize)

	desiredHash, err := clusterUpdateDispatchConfigFromRP(hcpCluster, spc).hash()
	require.NoError(t, err)
	actualHash, err := actualConfig.hash()
	require.NoError(t, err)
	assert.Equal(t, desiredHash, actualHash)
}

func TestClusterUpdateDispatchConfigFromCS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		csCluster func(t *testing.T) *arohcpv1alpha1.Cluster
		want      *clusterUpdateDispatchConfig
	}{
		{
			name: "custom size override maps to SPC dispatch not cluster pod sizing",
			csCluster: func(t *testing.T) *arohcpv1alpha1.Cluster {
				t.Helper()
				cluster, err := arohcpv1alpha1.NewCluster().Properties(map[string]string{
					CSPropertySizeOverride: "large",
				}).Build()
				require.NoError(t, err)
				return cluster
			},
			want: &clusterUpdateDispatchConfig{
				ExperimentalFeatures: clusterUpdateDispatchConfigExperimentalFeatures{},
				ServiceProviderClusterDispatch: clusterUpdateDispatchConfigServiceProviderClusterDispatch{
					DesiredHostedClusterControlPlaneSize: ptr.To("large"),
				},
			},
		},
		{
			name: "e2e_minimal size override maps to cluster pod sizing experimental feature and not to SPC dispatch",
			csCluster: func(t *testing.T) *arohcpv1alpha1.Cluster {
				t.Helper()
				cluster, err := arohcpv1alpha1.NewCluster().Properties(map[string]string{
					CSPropertySizeOverride: CSPropertyE2EMinimalControlPlaneSize,
				}).Build()
				require.NoError(t, err)
				return cluster
			},
			want: &clusterUpdateDispatchConfig{
				ExperimentalFeatures: clusterUpdateDispatchConfigExperimentalFeatures{
					ControlPlanePodSizing: api.MinimalControlPlanePodSizing,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := clusterUpdateDispatchConfigFromCS(tt.csCluster(t))
			require.NoError(t, err)
			assert.Equal(t, tt.want.ExperimentalFeatures, got.ExperimentalFeatures)
			assert.Equal(t, tt.want.ServiceProviderClusterDispatch, got.ServiceProviderClusterDispatch)
		})
	}
}

func TestClusterUpdateDispatchConfigAuthorizedCIDRsFromCS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		clusterAPI func() *arohcpv1alpha1.ClusterAPI
		want       []string
	}{
		{
			name: "not set CIDR block access section",
			clusterAPI: func() *arohcpv1alpha1.ClusterAPI {
				clusterAPI, err := arohcpv1alpha1.NewClusterAPI().Build()
				require.NoError(t, err)
				return clusterAPI
			},
			want: nil,
		},
		{
			name: "allow all mode",
			clusterAPI: func() *arohcpv1alpha1.ClusterAPI {
				clusterAPI, err := arohcpv1alpha1.NewClusterAPI().CIDRBlockAccess(arohcpv1alpha1.NewCIDRBlockAccess().
					Allow(arohcpv1alpha1.NewCIDRBlockAllowAccess().
						Mode(CSCIDRBlockAllowAccessModeAllowAll))).
					Build()
				require.NoError(t, err)
				return clusterAPI
			},
			want: nil,
		},
		{
			name: "allow all mode with stale values returns nil",
			clusterAPI: func() *arohcpv1alpha1.ClusterAPI {
				clusterAPI, err := arohcpv1alpha1.NewClusterAPI().CIDRBlockAccess(arohcpv1alpha1.NewCIDRBlockAccess().
					Allow(arohcpv1alpha1.NewCIDRBlockAllowAccess().
						Mode(CSCIDRBlockAllowAccessModeAllowAll).
						Values("10.0.0.0/8", "192.168.0.0/16"))).
					Build()
				require.NoError(t, err)
				return clusterAPI
			},
			want: nil,
		},
		{
			name: "CIDR block access section set without allow returns nil",
			clusterAPI: func() *arohcpv1alpha1.ClusterAPI {
				clusterAPI, err := arohcpv1alpha1.NewClusterAPI().CIDRBlockAccess(arohcpv1alpha1.NewCIDRBlockAccess()).Build()
				require.NoError(t, err)
				return clusterAPI
			},
			want: nil,
		},
		{
			name: "allow list mode",
			clusterAPI: func() *arohcpv1alpha1.ClusterAPI {
				clusterAPI, err := arohcpv1alpha1.NewClusterAPI().CIDRBlockAccess(arohcpv1alpha1.NewCIDRBlockAccess().
					Allow(arohcpv1alpha1.NewCIDRBlockAllowAccess().
						Mode(CSCIDRBlockAllowAccessModeAllowList).
						Values("10.0.0.0/8", "192.168.0.0/16"))).
					Build()
				require.NoError(t, err)
				return clusterAPI
			},
			want: []string{"10.0.0.0/8", "192.168.0.0/16"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ClusterUpdateDispatchConfigAuthorizedCIDRsFromCS(tt.clusterAPI()))
		})
	}
}

func TestClusterUpdateDispatchConfigNodeDrainTimeoutFromCS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		csCluster *arohcpv1alpha1.Cluster
		want      int32
	}{
		{
			name: "unset cs node drain grace period returns zero",
			csCluster: func() *arohcpv1alpha1.Cluster {
				cluster, err := arohcpv1alpha1.NewCluster().Build()
				require.NoError(t, err)
				return cluster
			}(),
			want: 0,
		},
		{
			name: "cs node drain grace period set in minutes unit returns the set value",
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
			name: "cs node drain grace period set in non-minutes unit returns zero",
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
			Autoscaling: api.ClusterAutoscalingProfile{
				MaxNodesTotal:               12,
				MaxPodGracePeriodSeconds:    600,
				MaxNodeProvisionTimeSeconds: 900,
				PodPriorityThreshold:        -10,
			},
		},
	}
	spc := &api.ServiceProviderCluster{}

	// We pass a non nil oldClusterServiceCluster so when we call BuildCSCluster, it will consider
	// it is an update, so it will not attempt to set the immutable attributes.
	oldClusterServiceCluster, err := arohcpv1alpha1.NewCluster().Build()
	require.NoError(t, err)

	clusterBuilder, err := BuildCSCluster(nil, "11111111-1111-1111-1111-111111111111", hcpCluster, nil, oldClusterServiceCluster, spc)

	require.NoError(t, err)

	csCluster, err := clusterBuilder.Build()
	require.NoError(t, err)

	desiredJSON, err := ClusterUpdateDispatchConfigJSONFromRP(hcpCluster, spc)
	require.NoError(t, err)
	actualJSON, err := ClusterUpdateDispatchConfigJSONFromCS(csCluster)
	require.NoError(t, err)

	// We assert both semantic and byte-for-byte JSON equality on purpose:
	//   - JSONEq checks that RP and CS projections represent the same config (values and structure).
	//   - Equal checks that canonicalJSON produces identical strings on both sides. The cluster
	//     service update dispatch controller uses string equality (==) for drift detection, so
	//     this must hold whenever the configs match; JSONEq alone would not catch encoding
	//     differences such as key ordering or whitespace that would cause a false drift signal.
	assert.JSONEq(t, desiredJSON, actualJSON)
	assert.Equal(t, desiredJSON, actualJSON)
	assert.Contains(t, desiredJSON, `"nodeDrainTimeoutMinutes": 45`)
	assert.Contains(t, desiredJSON, `"maxNodesTotal": 12`)

	hcpCluster.CustomerProperties.Autoscaling.MaxNodesTotal = 20
	desiredJSON, err = ClusterUpdateDispatchConfigJSONFromRP(hcpCluster, spc)
	require.NoError(t, err)
	assert.NotEqual(t, desiredJSON, actualJSON)
}

func TestClusterUpdateDispatchConfigApplyToCSBuilders(t *testing.T) {
	clusterBuilder := arohcpv1alpha1.NewCluster()
	clusterAPIBuilder := arohcpv1alpha1.NewClusterAPI()
	azureKmsKeyBuilder := arohcpv1alpha1.NewAzureKmsKey()
	azureBuilder := arohcpv1alpha1.NewAzure()

	tests := []struct {
		name           string
		config         clusterUpdateDispatchConfig
		properties     map[string]string
		wantProperties map[string]string
	}{
		{
			name: "enables both experimental properties",
			config: clusterUpdateDispatchConfig{
				ExperimentalFeatures: clusterUpdateDispatchConfigExperimentalFeatures{
					ControlPlaneAvailability: api.SingleReplicaControlPlane,
					ControlPlanePodSizing:    api.MinimalControlPlanePodSizing,
				},
			},
			properties: map[string]string{},
			wantProperties: map[string]string{
				CSPropertySingleReplica: CSPropertyEnabled,
				CSPropertySizeOverride:  CSPropertyE2EMinimalControlPlaneSize,
			},
		},
		{
			name:   "deletes experimental properties when disabled",
			config: clusterUpdateDispatchConfig{},
			properties: map[string]string{
				CSPropertySingleReplica:    CSPropertyEnabled,
				CSPropertySizeOverride:     CSPropertyEnabled,
				CSPropertyCPOImageOverride: "quay.io/openshift/cpo:old",
				"other":                    "value",
			},
			wantProperties: map[string]string{"other": "value"},
		},
		{
			name: "nil properties is treated as empty map",
			config: clusterUpdateDispatchConfig{
				ExperimentalFeatures: clusterUpdateDispatchConfigExperimentalFeatures{
					ControlPlanePodSizing: api.MinimalControlPlanePodSizing,
				},
			},
			properties: nil,
		},
		{
			name: "overrides conflicting caller properties",
			config: clusterUpdateDispatchConfig{
				ExperimentalFeatures: clusterUpdateDispatchConfigExperimentalFeatures{
					ControlPlaneAvailability: api.SingleReplicaControlPlane,
					ControlPlanePodSizing:    api.MinimalControlPlanePodSizing,
				},
			},
			properties: map[string]string{
				CSPropertySingleReplica: "false",
				CSPropertySizeOverride:  "false",
			},
			wantProperties: map[string]string{
				CSPropertySingleReplica: CSPropertyEnabled,
				CSPropertySizeOverride:  CSPropertyE2EMinimalControlPlaneSize,
			},
		},
		{
			name: "sets CPO image override property",
			config: clusterUpdateDispatchConfig{
				ExperimentalFeatures: clusterUpdateDispatchConfigExperimentalFeatures{
					ControlPlaneOperatorImage: "quay.io/openshift/cpo:test",
				},
			},
			properties: map[string]string{},
			wantProperties: map[string]string{
				CSPropertyCPOImageOverride: "quay.io/openshift/cpo:test",
			},
		},
		{
			name: "overrides conflicting CPO image property",
			config: clusterUpdateDispatchConfig{
				ExperimentalFeatures: clusterUpdateDispatchConfigExperimentalFeatures{
					ControlPlaneOperatorImage: "quay.io/openshift/cpo:test",
				},
			},
			properties: map[string]string{
				CSPropertyCPOImageOverride: "quay.io/openshift/cpo:old",
			},
			wantProperties: map[string]string{
				CSPropertyCPOImageOverride: "quay.io/openshift/cpo:test",
			},
		},
		{
			name: "size override wins over cluster level experimental pod sizing",
			config: clusterUpdateDispatchConfig{
				ExperimentalFeatures: clusterUpdateDispatchConfigExperimentalFeatures{
					ControlPlanePodSizing: api.MinimalControlPlanePodSizing,
				},
				ServiceProviderClusterDispatch: clusterUpdateDispatchConfigServiceProviderClusterDispatch{
					DesiredHostedClusterControlPlaneSize: ptr.To("Large"),
				},
			},
			properties: map[string]string{},
			wantProperties: map[string]string{
				CSPropertySizeOverride: "large",
			},
		},
		{
			name: "size override alone sets the property",
			config: clusterUpdateDispatchConfig{
				ServiceProviderClusterDispatch: clusterUpdateDispatchConfigServiceProviderClusterDispatch{
					DesiredHostedClusterControlPlaneSize: ptr.To("Medium"),
				},
			},
			properties: map[string]string{},
			wantProperties: map[string]string{
				CSPropertySizeOverride: "medium",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.applyToCSBuilders(clusterBuilder, clusterAPIBuilder, azureBuilder, azureKmsKeyBuilder, tt.properties)
			require.NoError(t, err)
			if tt.wantProperties != nil {
				assert.Equal(t, tt.wantProperties, tt.properties)
			}
		})
	}
}

func TestClusterUpdateDispatchConfigExperimentalFeaturesFromCS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		csCluster *arohcpv1alpha1.Cluster
		want      clusterUpdateDispatchConfigExperimentalFeatures
	}{
		{
			name:      "nil cluster returns empty experimental features",
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
			name: "size override e2e_minimal maps to MinimalControlPlanePodSizing",
			csCluster: func() *arohcpv1alpha1.Cluster {
				cluster, err := arohcpv1alpha1.NewCluster().Properties(map[string]string{
					CSPropertySizeOverride: CSPropertyE2EMinimalControlPlaneSize,
				}).Build()
				require.NoError(t, err)
				return cluster
			}(),
			want: clusterUpdateDispatchConfigExperimentalFeatures{
				ControlPlanePodSizing: api.MinimalControlPlanePodSizing,
			},
		},
		{
			name: "size override non e2e_minimal value does not set cluster level pod sizing",
			csCluster: func() *arohcpv1alpha1.Cluster {
				cluster, err := arohcpv1alpha1.NewCluster().Properties(map[string]string{
					CSPropertySizeOverride: "large",
				}).Build()
				require.NoError(t, err)
				return cluster
			}(),
			want: clusterUpdateDispatchConfigExperimentalFeatures{},
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
					CSPropertySizeOverride:     CSPropertyE2EMinimalControlPlaneSize,
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

func TestClusterUpdateDispatchConfigServiceProviderClusterDispatchDesiredHostedClusterControlPlaneSizeFromCS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		csCluster *arohcpv1alpha1.Cluster
		want      *string
	}{
		{
			name:      "nil cluster returns nil",
			csCluster: nil,
			want:      nil,
		},
		{
			name: "no size override property returns nil",
			csCluster: func() *arohcpv1alpha1.Cluster {
				cluster, err := arohcpv1alpha1.NewCluster().Build()
				require.NoError(t, err)
				return cluster
			}(),
			want: nil,
		},
		{
			name: "empty size override property returns nil",
			csCluster: func() *arohcpv1alpha1.Cluster {
				cluster, err := arohcpv1alpha1.NewCluster().Properties(map[string]string{
					CSPropertySizeOverride: "",
				}).Build()
				require.NoError(t, err)
				return cluster
			}(),
			want: nil,
		},
		{
			name: "e2e_minimal size override returns nil",
			csCluster: func() *arohcpv1alpha1.Cluster {
				cluster, err := arohcpv1alpha1.NewCluster().Properties(map[string]string{
					CSPropertySizeOverride: CSPropertyE2EMinimalControlPlaneSize,
				}).Build()
				require.NoError(t, err)
				return cluster
			}(),
			want: nil,
		},
		{
			name: "custom size override returns the set value",
			csCluster: func() *arohcpv1alpha1.Cluster {
				cluster, err := arohcpv1alpha1.NewCluster().Properties(map[string]string{
					CSPropertySizeOverride: "large",
				}).Build()
				require.NoError(t, err)
				return cluster
			}(),
			want: ptr.To("large"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, clusterUpdateDispatchConfigServiceProviderClusterDispatchDesiredHostedClusterControlPlaneSizeFromCS(tt.csCluster))
		})
	}
}

func TestClusterUpdateDispatchConfigAutoscalingFromCS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		autoscaler func(t *testing.T) *arohcpv1alpha1.ClusterAutoscaler
		want       clusterUpdateDispatchConfigAutoscaling
		wantErr    bool
	}{
		{
			name: "nil autoscaler returns empty autoscaling",
			autoscaler: func(t *testing.T) *arohcpv1alpha1.ClusterAutoscaler {
				t.Helper()
				return nil
			},
			want: clusterUpdateDispatchConfigAutoscaling{},
		},
		{
			name: "populated autoscaler maps all fields",
			autoscaler: func(t *testing.T) *arohcpv1alpha1.ClusterAutoscaler {
				t.Helper()
				autoscaler, err := arohcpv1alpha1.NewClusterAutoscaler().
					MaxNodeProvisionTime("15m").
					MaxPodGracePeriod(600).
					PodPriorityThreshold(-10).
					ResourceLimits(arohcpv1alpha1.NewAutoscalerResourceLimits().MaxNodesTotal(12)).
					Build()
				require.NoError(t, err)
				return autoscaler
			},
			want: clusterUpdateDispatchConfigAutoscaling{
				MaxNodesTotal:               12,
				MaxPodGracePeriodSeconds:    600,
				MaxNodeProvisionTimeSeconds: 900,
				PodPriorityThreshold:        -10,
			},
		},
		{
			name: "non-minute-aligned max node provision time uses integer seconds",
			autoscaler: func(t *testing.T) *arohcpv1alpha1.ClusterAutoscaler {
				t.Helper()
				autoscaler, err := arohcpv1alpha1.NewClusterAutoscaler().
					MaxNodeProvisionTime("15m50s").
					Build()
				require.NoError(t, err)
				return autoscaler
			},
			want: clusterUpdateDispatchConfigAutoscaling{
				MaxNodeProvisionTimeSeconds: 950,
			},
		},
		{
			name: "invalid max node provision time returns error",
			autoscaler: func(t *testing.T) *arohcpv1alpha1.ClusterAutoscaler {
				t.Helper()
				autoscaler, err := arohcpv1alpha1.NewClusterAutoscaler().
					MaxNodeProvisionTime("not-a-duration").
					Build()
				require.NoError(t, err)
				return autoscaler
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := clusterUpdateDispatchConfigAutoscalingFromCS(tt.autoscaler(t))
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestClusterUpdateDispatchConfigImageDigestMirrorsFromCS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		registryConfig func(t *testing.T) *arohcpv1alpha1.ClusterRegistryConfig
		want           []clusterUpdateDispatchConfigImageDigestMirror
	}{
		{
			name: "nil registry config returns nil",
			registryConfig: func(t *testing.T) *arohcpv1alpha1.ClusterRegistryConfig {
				t.Helper()
				return nil
			},
			want: nil,
		},
		{
			name: "empty image digest mirrors returns nil",
			registryConfig: func(t *testing.T) *arohcpv1alpha1.ClusterRegistryConfig {
				t.Helper()
				registryConfig, err := arohcpv1alpha1.NewClusterRegistryConfig().ImageDigestMirrors().Build()
				require.NoError(t, err)
				return registryConfig
			},
			want: nil,
		},
		{
			name: "mirrors with source and targets are copied",
			registryConfig: func(t *testing.T) *arohcpv1alpha1.ClusterRegistryConfig {
				t.Helper()
				registryConfig, err := arohcpv1alpha1.NewClusterRegistryConfig().
					ImageDigestMirrors(
						arohcpv1alpha1.NewImageMirror().
							Source("quay.io/openshift-release-dev").
							Mirrors("mirror.example.com", "mirror2.example.com"),
					).Build()
				require.NoError(t, err)
				return registryConfig
			},
			want: []clusterUpdateDispatchConfigImageDigestMirror{
				{
					Source:  "quay.io/openshift-release-dev",
					Mirrors: []string{"mirror.example.com", "mirror2.example.com"},
				},
			},
		},
		{
			name: "mirror without source is skipped",
			registryConfig: func(t *testing.T) *arohcpv1alpha1.ClusterRegistryConfig {
				t.Helper()
				registryConfig, err := arohcpv1alpha1.NewClusterRegistryConfig().
					ImageDigestMirrors(
						arohcpv1alpha1.NewImageMirror().Mirrors("mirror.example.com"),
					).Build()
				require.NoError(t, err)
				return registryConfig
			},
			want: []clusterUpdateDispatchConfigImageDigestMirror{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, clusterUpdateDispatchConfigImageDigestMirrorsFromCS(tt.registryConfig(t)))
		})
	}
}

func TestClusterUpdateDispatchConfigApplyToCSBuildersAutoscaling(t *testing.T) {
	clusterBuilder := arohcpv1alpha1.NewCluster()
	clusterAPIBuilder := arohcpv1alpha1.NewClusterAPI()
	config := clusterUpdateDispatchConfig{
		Autoscaling: clusterUpdateDispatchConfigAutoscaling{
			MaxNodesTotal:               12,
			MaxPodGracePeriodSeconds:    600,
			MaxNodeProvisionTimeSeconds: 900,
			PodPriorityThreshold:        -10,
		},
	}

	err := config.applyToCSBuilders(clusterBuilder, clusterAPIBuilder, nil, nil, map[string]string{})
	require.NoError(t, err)

	csCluster, err := clusterBuilder.Build()
	require.NoError(t, err)

	got, err := clusterUpdateDispatchConfigAutoscalingFromCS(csCluster.Autoscaler())
	require.NoError(t, err)
	assert.Equal(t, config.Autoscaling, got)
}

func TestClusterUpdateDispatchEtcdFromRP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		etcd api.EtcdProfile
		want clusterUpdateDispatchConfigEtcd
	}{
		{
			name: "empty mode returns zero value",
			etcd: api.EtcdProfile{},
			want: clusterUpdateDispatchConfigEtcd{},
		},
		{
			name: "platform-managed returns zero value",
			etcd: api.EtcdProfile{
				DataEncryption: api.EtcdDataEncryptionProfile{
					KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypePlatformManaged,
				},
			},
			want: clusterUpdateDispatchConfigEtcd{},
		},
		{
			name: "customer managed KMS returns version",
			etcd: api.EtcdProfile{
				DataEncryption: api.EtcdDataEncryptionProfile{
					KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
					CustomerManaged: &api.CustomerManagedEncryptionProfile{
						EncryptionType: api.CustomerManagedEncryptionTypeKMS,
						Kms: &api.KmsEncryptionProfile{
							Visibility: api.KeyVaultVisibilityPublic,
							ActiveKey: api.KmsKey{
								Name:      "test-key",
								VaultName: "test-vault",
								Version:   "v1",
							},
						},
					},
				},
			},
			want: clusterUpdateDispatchConfigEtcd{
				DataEncryption: clusterUpdateDispatchConfigEtcdDataEncryption{
					CustomerManaged: &clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManaged{
						Kms: &clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManagedKms{
							ActiveKey: clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManagedKmsActiveKey{
								Version: "v1",
							},
						},
					},
				},
			},
		},
		{
			name: "customer managed KMS with different version",
			etcd: api.EtcdProfile{
				DataEncryption: api.EtcdDataEncryptionProfile{
					KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
					CustomerManaged: &api.CustomerManagedEncryptionProfile{
						EncryptionType: api.CustomerManagedEncryptionTypeKMS,
						Kms: &api.KmsEncryptionProfile{
							ActiveKey: api.KmsKey{
								Version: "v2",
							},
						},
					},
				},
			},
			want: clusterUpdateDispatchConfigEtcd{
				DataEncryption: clusterUpdateDispatchConfigEtcdDataEncryption{
					CustomerManaged: &clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManaged{
						Kms: &clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManagedKms{
							ActiveKey: clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManagedKmsActiveKey{
								Version: "v2",
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, clusterUpdateDispatchEtcdFromRP(tt.etcd))
		})
	}
}

func TestClusterUpdateDispatchConfigEtcdFromCS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		azure func(t *testing.T) *arohcpv1alpha1.Azure
		want  clusterUpdateDispatchConfigEtcd
	}{
		{
			name: "no etcd encryption returns zero value",
			azure: func(t *testing.T) *arohcpv1alpha1.Azure {
				t.Helper()
				cluster, err := arohcpv1alpha1.NewCluster().Azure(arohcpv1alpha1.NewAzure()).Build()
				require.NoError(t, err)
				return cluster.Azure()
			},
			want: clusterUpdateDispatchConfigEtcd{},
		},
		{
			name: "platform-managed mode returns zero value",
			azure: func(t *testing.T) *arohcpv1alpha1.Azure {
				t.Helper()
				cluster, err := arohcpv1alpha1.NewCluster().Azure(arohcpv1alpha1.NewAzure().
					EtcdEncryption(arohcpv1alpha1.NewAzureEtcdEncryption().
						DataEncryption(arohcpv1alpha1.NewAzureEtcdDataEncryption().
							KeyManagementMode(csKeyManagementModePlatformManaged)))).Build()
				require.NoError(t, err)
				return cluster.Azure()
			},
			want: clusterUpdateDispatchConfigEtcd{},
		},
		{
			name: "customer managed KMS returns version",
			azure: func(t *testing.T) *arohcpv1alpha1.Azure {
				t.Helper()
				cluster, err := arohcpv1alpha1.NewCluster().Azure(arohcpv1alpha1.NewAzure().
					EtcdEncryption(arohcpv1alpha1.NewAzureEtcdEncryption().
						DataEncryption(arohcpv1alpha1.NewAzureEtcdDataEncryption().
							KeyManagementMode(csKeyManagementModeCustomerManaged).
							CustomerManaged(arohcpv1alpha1.NewAzureEtcdDataEncryptionCustomerManaged().
								EncryptionType(csCustomerManagedEncryptionTypeKms).
								Kms(arohcpv1alpha1.NewAzureKmsEncryption().
									ActiveKey(arohcpv1alpha1.NewAzureKmsKey().
										KeyVersion("v1"))))))).Build()
				require.NoError(t, err)
				return cluster.Azure()
			},
			want: clusterUpdateDispatchConfigEtcd{
				DataEncryption: clusterUpdateDispatchConfigEtcdDataEncryption{
					CustomerManaged: &clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManaged{
						Kms: &clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManagedKms{
							ActiveKey: clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManagedKmsActiveKey{
								Version: "v1",
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, clusterUpdateDispatchConfigEtcdFromCS(tt.azure(t)))
		})
	}
}

func TestClusterUpdateDispatchConfigFromCSRoundTripWithKMS(t *testing.T) {
	// oldClusterServiceCluster represents a KMS cluster that already exists in CS
	// with all immutable fields set at creation time.
	oldClusterServiceCluster, err := arohcpv1alpha1.NewCluster().
		Azure(arohcpv1alpha1.NewAzure().
			EtcdEncryption(arohcpv1alpha1.NewAzureEtcdEncryption().
				DataEncryption(arohcpv1alpha1.NewAzureEtcdDataEncryption().
					KeyManagementMode(csKeyManagementModeCustomerManaged).
					CustomerManaged(arohcpv1alpha1.NewAzureEtcdDataEncryptionCustomerManaged().
						EncryptionType(csCustomerManagedEncryptionTypeKms).
						Kms(arohcpv1alpha1.NewAzureKmsEncryption().
							ActiveKey(arohcpv1alpha1.NewAzureKmsKey().
								KeyVersion("v0").KeyName("test-key").KeyVaultName("test-vault"))))))).Build()
	require.NoError(t, err)

	hcpCluster := &api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			Etcd: api.EtcdProfile{
				DataEncryption: api.EtcdDataEncryptionProfile{
					KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
					CustomerManaged: &api.CustomerManagedEncryptionProfile{
						EncryptionType: api.CustomerManagedEncryptionTypeKMS,
						Kms: &api.KmsEncryptionProfile{
							Visibility: api.KeyVaultVisibilityPublic,
							ActiveKey: api.KmsKey{
								Name:      "test-key",
								VaultName: "test-vault",
								Version:   "v1",
							},
						},
					},
				},
			},
		},
	}
	spc := &api.ServiceProviderCluster{}

	// BuildCSCluster in the update case (oldClusterServiceCluster != nil) produces
	// a PATCH payload — it only contains mutable fields. Simulate what CS returns
	// after applying the PATCH by building a full cluster with immutable fields
	// from the old cluster plus the updated key version.
	_, err = BuildCSCluster(nil, "11111111-1111-1111-1111-111111111111", hcpCluster, nil, oldClusterServiceCluster, spc)
	require.NoError(t, err)

	fullCSCluster, err := arohcpv1alpha1.NewCluster().
		Azure(arohcpv1alpha1.NewAzure().
			EtcdEncryption(arohcpv1alpha1.NewAzureEtcdEncryption().
				DataEncryption(arohcpv1alpha1.NewAzureEtcdDataEncryption().
					KeyManagementMode(csKeyManagementModeCustomerManaged).
					CustomerManaged(arohcpv1alpha1.NewAzureEtcdDataEncryptionCustomerManaged().
						EncryptionType(csCustomerManagedEncryptionTypeKms).
						Kms(arohcpv1alpha1.NewAzureKmsEncryption().
							ActiveKey(arohcpv1alpha1.NewAzureKmsKey().
								KeyVersion("v1").KeyName("test-key").KeyVaultName("test-vault"))))))).Build()
	require.NoError(t, err)

	actualConfig, err := clusterUpdateDispatchConfigFromCS(fullCSCluster)
	require.NoError(t, err)

	require.NotNil(t, actualConfig.Etcd.DataEncryption.CustomerManaged)
	require.NotNil(t, actualConfig.Etcd.DataEncryption.CustomerManaged.Kms)
	assert.Equal(t, "v1", actualConfig.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version)

	desiredHash, err := clusterUpdateDispatchConfigFromRP(hcpCluster, spc).hash()
	require.NoError(t, err)
	actualHash, err := actualConfig.hash()
	require.NoError(t, err)
	assert.Equal(t, desiredHash, actualHash)
}

func TestClusterUpdateDispatchConfigJSONFromRPAndCSWithKMS(t *testing.T) {
	hcpCluster := &api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			Etcd: api.EtcdProfile{
				DataEncryption: api.EtcdDataEncryptionProfile{
					KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
					CustomerManaged: &api.CustomerManagedEncryptionProfile{
						EncryptionType: api.CustomerManagedEncryptionTypeKMS,
						Kms: &api.KmsEncryptionProfile{
							ActiveKey: api.KmsKey{
								Version: "v1",
							},
						},
					},
				},
			},
		},
	}
	spc := &api.ServiceProviderCluster{}

	// Simulate the full CS cluster that a GET would return after the key
	// version update has been applied — immutable fields are already present.
	fullCSCluster, err := arohcpv1alpha1.NewCluster().
		Azure(arohcpv1alpha1.NewAzure().
			EtcdEncryption(arohcpv1alpha1.NewAzureEtcdEncryption().
				DataEncryption(arohcpv1alpha1.NewAzureEtcdDataEncryption().
					KeyManagementMode(csKeyManagementModeCustomerManaged).
					CustomerManaged(arohcpv1alpha1.NewAzureEtcdDataEncryptionCustomerManaged().
						EncryptionType(csCustomerManagedEncryptionTypeKms).
						Kms(arohcpv1alpha1.NewAzureKmsEncryption().
							ActiveKey(arohcpv1alpha1.NewAzureKmsKey().
								KeyVersion("v1"))))))).Build()
	require.NoError(t, err)

	desiredJSON, err := ClusterUpdateDispatchConfigJSONFromRP(hcpCluster, spc)
	require.NoError(t, err)
	actualJSON, err := ClusterUpdateDispatchConfigJSONFromCS(fullCSCluster)
	require.NoError(t, err)

	assert.JSONEq(t, desiredJSON, actualJSON)
	assert.Equal(t, desiredJSON, actualJSON)
	assert.Contains(t, desiredJSON, `"version": "v1"`)
}

func TestClusterUpdateDispatchConfigApplyToCSBuildersEtcd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		config            clusterUpdateDispatchConfig
		kmsKeyBuilder     *arohcpv1alpha1.AzureKmsKeyBuilder
		wantKeyVersion    string
		wantKeyVersionSet bool
	}{
		{
			name: "customer managed KMS sets key version on builder",
			config: clusterUpdateDispatchConfig{
				Etcd: clusterUpdateDispatchConfigEtcd{
					DataEncryption: clusterUpdateDispatchConfigEtcdDataEncryption{
						CustomerManaged: &clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManaged{
							Kms: &clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManagedKms{
								ActiveKey: clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManagedKmsActiveKey{
									Version: "v2",
								},
							},
						},
					},
				},
			},
			kmsKeyBuilder:     arohcpv1alpha1.NewAzureKmsKey(),
			wantKeyVersion:    "v2",
			wantKeyVersionSet: true,
		},
		{
			name:              "empty etcd does not set key version",
			config:            clusterUpdateDispatchConfig{},
			kmsKeyBuilder:     arohcpv1alpha1.NewAzureKmsKey(),
			wantKeyVersionSet: false,
		},
		{
			name: "nil builder is safely skipped",
			config: clusterUpdateDispatchConfig{
				Etcd: clusterUpdateDispatchConfigEtcd{
					DataEncryption: clusterUpdateDispatchConfigEtcdDataEncryption{
						CustomerManaged: &clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManaged{
							Kms: &clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManagedKms{
								ActiveKey: clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManagedKmsActiveKey{
									Version: "v2",
								},
							},
						},
					},
				},
			},
			kmsKeyBuilder:     nil,
			wantKeyVersionSet: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clusterBuilder := arohcpv1alpha1.NewCluster()
			clusterAPIBuilder := arohcpv1alpha1.NewClusterAPI()
			properties := map[string]string{}

			err := tt.config.applyToCSBuilders(clusterBuilder, clusterAPIBuilder, nil, tt.kmsKeyBuilder, properties)
			require.NoError(t, err)

			if tt.wantKeyVersionSet && tt.kmsKeyBuilder != nil {
				key, err := tt.kmsKeyBuilder.Build()
				require.NoError(t, err)
				assert.Equal(t, tt.wantKeyVersion, key.KeyVersion())
			}
		})
	}
}

func TestClusterUpdateDispatchConfigFromCSEtcdExtraction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		csCluster func(t *testing.T) *arohcpv1alpha1.Cluster
		wantEtcd  clusterUpdateDispatchConfigEtcd
	}{
		{
			name: "no etcd encryption returns zero value",
			csCluster: func(t *testing.T) *arohcpv1alpha1.Cluster {
				t.Helper()
				cluster, err := arohcpv1alpha1.NewCluster().Build()
				require.NoError(t, err)
				return cluster
			},
			wantEtcd: clusterUpdateDispatchConfigEtcd{},
		},
		{
			name: "customer managed KMS extracts version",
			csCluster: func(t *testing.T) *arohcpv1alpha1.Cluster {
				t.Helper()
				cluster, err := arohcpv1alpha1.NewCluster().
					Azure(arohcpv1alpha1.NewAzure().
						EtcdEncryption(arohcpv1alpha1.NewAzureEtcdEncryption().
							DataEncryption(arohcpv1alpha1.NewAzureEtcdDataEncryption().
								KeyManagementMode(csKeyManagementModeCustomerManaged).
								CustomerManaged(arohcpv1alpha1.NewAzureEtcdDataEncryptionCustomerManaged().
									EncryptionType(csCustomerManagedEncryptionTypeKms).
									Kms(arohcpv1alpha1.NewAzureKmsEncryption().
										ActiveKey(arohcpv1alpha1.NewAzureKmsKey().
											KeyVersion("v3"))))))).Build()
				require.NoError(t, err)
				return cluster
			},
			wantEtcd: clusterUpdateDispatchConfigEtcd{
				DataEncryption: clusterUpdateDispatchConfigEtcdDataEncryption{
					CustomerManaged: &clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManaged{
						Kms: &clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManagedKms{
							ActiveKey: clusterUpdateDispatchConfigEtcdDataEncryptionCustomerManagedKmsActiveKey{
								Version: "v3",
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := clusterUpdateDispatchConfigFromCS(tt.csCluster(t))
			require.NoError(t, err)
			assert.Equal(t, tt.wantEtcd, got.Etcd)
		})
	}
}

func TestClusterUpdateDispatchConfigContainerRegistryPullMIFromCS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		csAzure  *arohcpv1alpha1.Azure
		wantMIID string
	}{
		{
			name:     "nil azure returns empty",
			csAzure:  nil,
			wantMIID: "",
		},
		{
			name: "azure without container registry returns empty",
			csAzure: func() *arohcpv1alpha1.Azure {
				a, err := arohcpv1alpha1.NewAzure().TenantID("t").Build()
				require.NoError(t, err)
				return a
			}(),
			wantMIID: "",
		},
		{
			name: "extracts managed identity resource ID",
			csAzure: func() *arohcpv1alpha1.Azure {
				a, err := arohcpv1alpha1.NewAzure().
					ContainerRegistry(arohcpv1alpha1.NewAzureContainerRegistry().
						Credentials(arohcpv1alpha1.NewAzureContainerRegistryCredentials().
							Type(arohcpv1alpha1.AzureContainerRegistryCredentialTypeManagedIdentity).
							ManagedIdentity(arohcpv1alpha1.NewAzureUserAssignedManagedIdentity().
								ResourceID(api.NewTestUserAssignedIdentity("cr-pull-mi").String())))).
					Build()
				require.NoError(t, err)
				return a
			}(),
			wantMIID: api.NewTestUserAssignedIdentity("cr-pull-mi").String(),
		},
		{
			name: "empty resource ID returns empty (clearing signal)",
			csAzure: func() *arohcpv1alpha1.Azure {
				a, err := arohcpv1alpha1.NewAzure().
					ContainerRegistry(arohcpv1alpha1.NewAzureContainerRegistry().
						Credentials(arohcpv1alpha1.NewAzureContainerRegistryCredentials().
							Type(arohcpv1alpha1.AzureContainerRegistryCredentialTypeManagedIdentity).
							ManagedIdentity(arohcpv1alpha1.NewAzureUserAssignedManagedIdentity().
								ResourceID("")))).
					Build()
				require.NoError(t, err)
				return a
			}(),
			wantMIID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ClusterUpdateDispatchConfigContainerRegistryPullMIFromCS(tt.csAzure)
			assert.Equal(t, tt.wantMIID, got)
		})
	}
}

func TestClusterUpdateDispatchConfigFromCSContainerRegistryRoundTrip(t *testing.T) {
	t.Parallel()

	miID := api.NewTestUserAssignedIdentity("cr-pull-mi")

	rpCluster := &api.HCPOpenShiftCluster{
		CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
			Platform: api.CustomerPlatformProfile{
				ContainerRegistryPullManagedIdentity: miID,
			},
		},
	}

	rpConfig := clusterUpdateDispatchConfigFromRP(rpCluster, nil)
	assert.Equal(t, miID.String(), rpConfig.ContainerRegistryPullManagedIdentityResourceID)

	csCluster, err := arohcpv1alpha1.NewCluster().
		Azure(arohcpv1alpha1.NewAzure().
			ContainerRegistry(arohcpv1alpha1.NewAzureContainerRegistry().
				Credentials(arohcpv1alpha1.NewAzureContainerRegistryCredentials().
					Type(arohcpv1alpha1.AzureContainerRegistryCredentialTypeManagedIdentity).
					ManagedIdentity(arohcpv1alpha1.NewAzureUserAssignedManagedIdentity().
						ResourceID(miID.String()))))).
		Build()
	require.NoError(t, err)

	csConfig, err := clusterUpdateDispatchConfigFromCS(csCluster)
	require.NoError(t, err)
	assert.Equal(t, rpConfig.ContainerRegistryPullManagedIdentityResourceID, csConfig.ContainerRegistryPullManagedIdentityResourceID)
}

func TestClusterUpdateDispatchConfigApplyToCSBuildersContainerRegistry(t *testing.T) {
	t.Parallel()

	miID := api.NewTestUserAssignedIdentity("cr-pull-mi")

	tests := []struct {
		name      string
		config    clusterUpdateDispatchConfig
		wantMISet bool
		wantMIRID string
	}{
		{
			name: "sets container registry pull MI on azure builder",
			config: clusterUpdateDispatchConfig{
				ContainerRegistryPullManagedIdentityResourceID: miID.String(),
			},
			wantMISet: true,
			wantMIRID: miID.String(),
		},
		{
			name:      "empty container registry does not set azure builder",
			config:    clusterUpdateDispatchConfig{},
			wantMISet: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clusterBuilder := arohcpv1alpha1.NewCluster()
			clusterAPIBuilder := arohcpv1alpha1.NewClusterAPI()
			properties := map[string]string{}

			err := tt.config.applyToCSBuilders(clusterBuilder, clusterAPIBuilder, nil, nil, properties)
			require.NoError(t, err)

			cluster, err := clusterBuilder.Build()
			require.NoError(t, err)

			if tt.wantMISet {
				require.NotNil(t, cluster.Azure(), "azure should be set")
				cr := cluster.Azure().ContainerRegistry()
				require.NotNil(t, cr, "container registry should be set")
				creds := cr.Credentials()
				require.NotNil(t, creds, "credentials should be set")
				mi := creds.ManagedIdentity()
				require.NotNil(t, mi, "managed identity should be set")
				assert.Equal(t, tt.wantMIRID, mi.ResourceID())
			} else {
				if cluster.Azure() != nil {
					assert.Nil(t, cluster.Azure().ContainerRegistry(), "container registry should not be set")
				}
			}
		})
	}
}
