// Copyright 2025 Microsoft Corporation
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
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"dario.cat/mergo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	csarhcpv1alpha1 "github.com/openshift-online/ocm-api-model/clientapi/arohcp/v1alpha1"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

const (
	dummyURL = "https://redhat.com"
	dummyCA  = `-----BEGIN CERTIFICATE-----
MIICMzCCAZygAwIBAgIJALiPnVsvq8dsMA0GCSqGSIb3DQEBBQUAMFMxCzAJBgNV
BAYTAlVTMQwwCgYDVQQIEwNmb28xDDAKBgNVBAcTA2ZvbzEMMAoGA1UEChMDZm9v
MQwwCgYDVQQLEwNmb28xDDAKBgNVBAMTA2ZvbzAeFw0xMzAzMTkxNTQwMTlaFw0x
ODAzMTgxNTQwMTlaMFMxCzAJBgNVBAYTAlVTMQwwCgYDVQQIEwNmb28xDDAKBgNV
BAcTA2ZvbzEMMAoGA1UEChMDZm9vMQwwCgYDVQQLEwNmb28xDDAKBgNVBAMTA2Zv
bzCBnzANBgkqhkiG9w0BAQEFAAOBjQAwgYkCgYEAzdGfxi9CNbMf1UUcvDQh7MYB
OveIHyc0E0KIbhjK5FkCBU4CiZrbfHagaW7ZEcN0tt3EvpbOMxxc/ZQU2WN/s/wP
xph0pSfsfFsTKM4RhTWD2v4fgk+xZiKd1p0+L4hTtpwnEw0uXRVd0ki6muwV5y/P
+5FHUeldq+pgTcgzuK8CAwEAAaMPMA0wCwYDVR0PBAQDAgLkMA0GCSqGSIb3DQEB
BQUAA4GBAJiDAAtY0mQQeuxWdzLRzXmjvdSuL9GoyT3BF/jSnpxz5/58dba8pWen
v3pj4P3w5DoOso0rzkZy2jEsEitlVM2mLSbQpMM+MUVQCQoiG6W9xuCFuxSrwPIS
pAqEAuV4DNoxQKKWmhVv+J0ptMWD25Pnpxeq5sXzghfJnslJlQND
-----END CERTIFICATE-----
`
)

var dummyAudiences = []string{"audience1", "audience2"}

func TestConvertCStoHCPOpenShiftCluster(t *testing.T) {
	resourceID, err := azcorearm.ParseResourceID(api.TestClusterResourceID)
	require.NoError(t, err)

	testCases := []struct {
		name             string
		ocmClusterTweaks *arohcpv1alpha1.ClusterBuilder
		hcpClusterTweaks *api.HCPOpenShiftCluster
	}{
		{
			name:             "zero",
			ocmClusterTweaks: arohcpv1alpha1.NewCluster(),
			hcpClusterTweaks: &api.HCPOpenShiftCluster{},
		},
		{
			name: "converts nodeDrainGracePeriod to nodeDrainTimeoutMinutes",
			ocmClusterTweaks: arohcpv1alpha1.NewCluster().
				NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
					Unit(csNodeDrainGracePeriodUnit).
					Value(42),
				),
			hcpClusterTweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					NodeDrainTimeoutMinutes: 42,
				},
			},
		},
		{
			name: "converts EtcdEncryption for only default PlatformManaged",
			ocmClusterTweaks: arohcpv1alpha1.NewCluster().
				Azure(arohcpv1alpha1.NewAzure().
					EtcdEncryption(arohcpv1alpha1.NewAzureEtcdEncryption().
						DataEncryption(arohcpv1alpha1.NewAzureEtcdDataEncryption().
							KeyManagementMode(csKeyManagementModePlatformManaged),
						),
					),
				),
			hcpClusterTweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Etcd: api.EtcdProfile{
						DataEncryption: api.EtcdDataEncryptionProfile{
							KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypePlatformManaged,
							CustomerManaged:   nil,
						},
					},
				},
			},
		},
		{
			name: "converts EtcdEncryption for CustomerManaged",
			ocmClusterTweaks: arohcpv1alpha1.NewCluster().
				Azure(arohcpv1alpha1.NewAzure().
					EtcdEncryption(arohcpv1alpha1.NewAzureEtcdEncryption().
						DataEncryption(arohcpv1alpha1.NewAzureEtcdDataEncryption().
							KeyManagementMode(csKeyManagementModeCustomerManaged).
							CustomerManaged(arohcpv1alpha1.NewAzureEtcdDataEncryptionCustomerManaged().
								EncryptionType("kms").
								Kms(arohcpv1alpha1.NewAzureKmsEncryption().
									ActiveKey(arohcpv1alpha1.NewAzureKmsKey().
										KeyName("test").
										KeyVaultName("test").
										KeyVersion("test-version"),
									),
								),
							),
						),
					),
				),
			hcpClusterTweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Etcd: api.EtcdProfile{
						DataEncryption: api.EtcdDataEncryptionProfile{
							CustomerManaged: &api.CustomerManagedEncryptionProfile{
								EncryptionType: "KMS",
								Kms: &api.KmsEncryptionProfile{
									ActiveKey: api.KmsKey{
										Name:      "test",
										VaultName: "test",
										Version:   "test-version",
									},
								},
							},
							KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
						},
					},
				},
			},
		},
		{
			name: "converts CS ClusterImageRegistry to ClusterImageRegistryProfile",
			ocmClusterTweaks: arohcpv1alpha1.NewCluster().
				ImageRegistry(arohcpv1alpha1.NewClusterImageRegistry().
					State(string(csImageRegistryStateDisabled)),
				),
			hcpClusterTweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					ClusterImageRegistry: api.ClusterImageRegistryProfile{
						State: api.ClusterImageRegistryProfileStateDisabled,
					},
				},
			},
		},
		{
			name: "converts stable version from CS to RP (X.Y.Z to X.Y)",
			ocmClusterTweaks: arohcpv1alpha1.NewCluster().
				Version(arohcpv1alpha1.NewVersion().
					ID("openshift-v4.19.7").
					ChannelGroup("stable")),
			hcpClusterTweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Version: api.VersionProfile{
						ID:           "4.19",
						ChannelGroup: "stable",
					},
				},
			},
		},
		{
			name: "converts nightly version from CS to RP (strips channel suffix)",
			ocmClusterTweaks: arohcpv1alpha1.NewCluster().
				Version(arohcpv1alpha1.NewVersion().
					ID("openshift-v4.19.0-0.nightly-2025-01-01-nightly").
					ChannelGroup("nightly")),
			hcpClusterTweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Version: api.VersionProfile{
						ID:           "4.19",
						ChannelGroup: "nightly",
					},
				},
			},
		},
		{
			name: "converts candidate version from CS to RP",
			ocmClusterTweaks: arohcpv1alpha1.NewCluster().
				Version(arohcpv1alpha1.NewVersion().
					ID("openshift-v4.19.1-candidate").
					ChannelGroup("candidate")),
			hcpClusterTweaks: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Version: api.VersionProfile{
						ID:           "4.19",
						ChannelGroup: "candidate",
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			csCluster := ocmCluster(t, ocmClusterDefaults(api.TestLocation), tc.ocmClusterTweaks)
			expectHcpCluster := api.ClusterTestCase(t, tc.hcpClusterTweaks)

			actualHcpCluster, err := ConvertCStoHCPOpenShiftCluster(resourceID, api.TestLocation, csCluster)
			require.NoError(t, err)

			assert.Equal(t, expectHcpCluster, actualHcpCluster)
		})
	}
}

func TestWithImmutableAttributes(t *testing.T) {
	testCases := []struct {
		name       string
		hcpCluster *api.HCPOpenShiftCluster
		want       *arohcpv1alpha1.Cluster
	}{
		{
			name:       "simple default",
			hcpCluster: &api.HCPOpenShiftCluster{},
			want:       ocmCluster(t, ocmClusterDefaults(api.TestLocation)),
		},
		{
			name: "converts stable version from RP to CS (adds patch and prefix)",
			hcpCluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Version: api.VersionProfile{
						ID:           "4.19",
						ChannelGroup: "stable",
					},
				},
			},
			want: ocmCluster(t, ocmClusterDefaults(api.TestLocation).
				Version(arohcpv1alpha1.NewVersion().
					ID("openshift-v4.19.7").
					ChannelGroup("stable"))),
		},
		{
			name: "converts candidate version from RP to CS (preserves patch)",
			hcpCluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Version: api.VersionProfile{
						ID:           "4.19.19",
						ChannelGroup: "candidate",
					},
				},
			},
			want: ocmCluster(t, ocmClusterDefaults(api.TestLocation).
				Version(arohcpv1alpha1.NewVersion().
					ID("openshift-v4.19.19-candidate").
					ChannelGroup("candidate"))),
		},
		{
			name: "converts nightly version from RP to CS (preserves semver)",
			hcpCluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Version: api.VersionProfile{
						ID:           "4.19.0-0.nightly-2025-01-01",
						ChannelGroup: "nightly",
					},
				},
			},
			want: ocmCluster(t, ocmClusterDefaults(api.TestLocation).
				Version(arohcpv1alpha1.NewVersion().
					ID("openshift-v4.19.0-0.nightly-2025-01-01-nightly").
					ChannelGroup("nightly"))),
		},
		{
			name: "with version 4.19",
			hcpCluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Version: api.VersionProfile{ID: "4.19", ChannelGroup: "stable"},
				},
			},
			want: ocmCluster(t, ocmClusterDefaults(api.TestLocation).Version(
				arohcpv1alpha1.NewVersion().ID("openshift-v4.19.7").ChannelGroup("stable"))),
		},
		{
			name: "with version 4.20",
			hcpCluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					Version: api.VersionProfile{ID: "4.20", ChannelGroup: "stable"},
				},
			},
			want: ocmCluster(t, ocmClusterDefaults(api.TestLocation).Version(
				arohcpv1alpha1.NewVersion().ID("openshift-v4.20.8").ChannelGroup("stable"))),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, arohcpv1alpha1.MarshalCluster(tc.want, &buf))
			want := buf.String()
			builder, err := withImmutableAttributes(
				ocmClusterDefaults(api.TestLocation),
				api.ClusterTestCase(t, tc.hcpCluster),
				api.TestSubscriptionID,
				api.TestResourceGroupName,
				api.TestTenantID,
				"")
			require.NoError(t, err)
			result, err := builder.Build()
			require.NoError(t, err)
			buf.Reset()
			require.NoError(t, arohcpv1alpha1.MarshalCluster(result, &buf))
			got := buf.String()
			assert.JSONEq(t, want, got)
		})
	}
}

func testResourceID(t *testing.T) *azcorearm.ResourceID {
	resourceID, err := azcorearm.ParseResourceID(api.TestClusterResourceID)
	require.NoError(t, err)
	return resourceID
}

func ocmCluster(t *testing.T, builders ...*arohcpv1alpha1.ClusterBuilder) *arohcpv1alpha1.Cluster {
	var mergedCluster map[string]interface{}

	for _, builder := range builders {
		var rawCluster map[string]interface{}
		var buffer bytes.Buffer

		cluster, err := builder.Build()
		require.NoError(t, err)
		require.NoError(t, arohcpv1alpha1.MarshalCluster(cluster, &buffer))
		require.NoError(t, json.Unmarshal(buffer.Bytes(), &rawCluster))
		require.NoError(t, mergo.Merge(&mergedCluster, rawCluster, mergo.WithOverride))
	}

	data, err := arm.MarshalJSON(mergedCluster)
	require.NoError(t, err)
	cluster, err := arohcpv1alpha1.UnmarshalCluster(data)
	require.NoError(t, err)

	return cluster
}

func ocmClusterDefaults(azureLocation string) *arohcpv1alpha1.ClusterBuilder {
	// This reflects how the immutable attributes get set when passed a minimally
	// valid RP cluster, using constants from internal/api/testhelpers.go.
	return arohcpv1alpha1.NewCluster().
		API(arohcpv1alpha1.NewClusterAPI().
			Listening(arohcpv1alpha1.ListeningMethodExternal)).
		Azure(arohcpv1alpha1.NewAzure().
			EtcdEncryption(arohcpv1alpha1.NewAzureEtcdEncryption().
				DataEncryption(arohcpv1alpha1.NewAzureEtcdDataEncryption().
					KeyManagementMode(csKeyManagementModePlatformManaged))).
			ManagedResourceGroupName(api.TestManagedResourceGroupName).
			NetworkSecurityGroupResourceID(api.TestNetworkSecurityGroupResourceID).
			NodesOutboundConnectivity(arohcpv1alpha1.NewAzureNodesOutboundConnectivity().
				OutboundType(csOutboundType)).
			OperatorsAuthentication(arohcpv1alpha1.NewAzureOperatorsAuthentication().
				ManagedIdentities(arohcpv1alpha1.NewAzureOperatorsAuthenticationManagedIdentities().
					ControlPlaneOperatorsManagedIdentities(make(map[string]*csarhcpv1alpha1.AzureControlPlaneManagedIdentityBuilder)).
					DataPlaneOperatorsManagedIdentities(make(map[string]*csarhcpv1alpha1.AzureDataPlaneManagedIdentityBuilder)).
					ManagedIdentitiesDataPlaneIdentityUrl(""))).
			ResourceGroupName(strings.ToLower(api.TestResourceGroupName)).
			ResourceName(strings.ToLower(api.TestClusterName)).
			SubnetResourceID(api.TestSubnetResourceID).
			SubscriptionID(strings.ToLower(api.TestSubscriptionID)).
			TenantID(api.TestTenantID),
		).
		CCS(arohcpv1alpha1.NewCCS().Enabled(true)).
		CloudProvider(arohcpv1alpha1.NewCloudProvider().
			ID("azure")).
		Flavour(arohcpv1alpha1.NewFlavour().
			ID("osd-4")).
		Hypershift(arohcpv1alpha1.NewHypershift().
			Enabled(true)).
		Name(strings.ToLower(api.TestClusterName)).
		Network(arohcpv1alpha1.NewNetwork().
			HostPrefix(23).
			MachineCIDR("10.0.0.0/16").
			PodCIDR("10.128.0.0/14").
			ServiceCIDR("172.30.0.0/16").
			Type("OVNKubernetes")).
		Autoscaler(arohcpv1alpha1.NewClusterAutoscaler().
			PodPriorityThreshold(-10).
			MaxNodeProvisionTime("15m").
			MaxPodGracePeriod(600)).
		Product(arohcpv1alpha1.NewProduct().
			ID("aro")).
		Region(arohcpv1alpha1.NewCloudRegion().
			ID(azureLocation)).
		Version(arohcpv1alpha1.NewVersion().
			ID("").
			ChannelGroup("stable")).
		ImageRegistry(arohcpv1alpha1.NewClusterImageRegistry().
			State(csImageRegistryStateEnabled))
}

func getHCPNodePoolResource(opts ...func(*api.HCPOpenShiftClusterNodePool)) *api.HCPOpenShiftClusterNodePool {
	nodePool := &api.HCPOpenShiftClusterNodePool{
		Properties: api.HCPOpenShiftClusterNodePoolProperties{},
	}

	for _, opt := range opts {
		opt(nodePool)
	}
	return nodePool
}

// Because we don't distinguish between unset and empty values in our JSON parsing
// we will get the resulting CS object from an empty HCPOpenShiftClusterNodePool object.
func getBaseCSNodePoolBuilder() *arohcpv1alpha1.NodePoolBuilder {
	return arohcpv1alpha1.NewNodePool().
		ID("").
		AvailabilityZone("").
		AzureNodePool(arohcpv1alpha1.NewAzureNodePool().
			ResourceName("").
			VMSize("").
			EncryptionAtHost(
				arohcpv1alpha1.NewAzureNodePoolEncryptionAtHost().
					State(csEncryptionAtHostStateDisabled),
			).
			OsDisk(arohcpv1alpha1.NewAzureNodePoolOsDisk().
				SizeGibibytes(0).
				StorageAccountType(""),
			),
		).
		Subnet("").
		Version(arohcpv1alpha1.NewVersion().
			ID("").
			ChannelGroup(""),
		).
		Replicas(0).
		AutoRepair(false)
}

func TestBuildCSNodePool(t *testing.T) {
	resourceID := testResourceID(t)
	testCases := []struct {
		name               string
		hcpNodePool        *api.HCPOpenShiftClusterNodePool
		expectedCSNodePool *arohcpv1alpha1.NodePoolBuilder
	}{
		{
			name:               "zero",
			hcpNodePool:        getHCPNodePoolResource(),
			expectedCSNodePool: getBaseCSNodePoolBuilder(),
		},
		{
			name: "handle multiple taints",
			hcpNodePool: getHCPNodePoolResource(
				func(hsc *api.HCPOpenShiftClusterNodePool) {
					hsc.Properties.Taints = []api.Taint{
						{Effect: "a"},
						{Effect: "b"},
					}
				},
			),
			expectedCSNodePool: getBaseCSNodePoolBuilder().Taints(
				[]*arohcpv1alpha1.TaintBuilder{
					arohcpv1alpha1.NewTaint().
						Effect("a").
						Key("").
						Value(""),
					arohcpv1alpha1.NewTaint().Effect("b").
						Key("").
						Value(""),
				}...),
		},
		{
			name: "converts stable version from RP to CS (adds patch and prefix)",
			hcpNodePool: getHCPNodePoolResource(
				func(hsc *api.HCPOpenShiftClusterNodePool) {
					hsc.Properties.Version = api.NodePoolVersionProfile{
						ID:           "4.19",
						ChannelGroup: "stable",
					}
				},
			),
			expectedCSNodePool: getBaseCSNodePoolBuilder().
				Version(arohcpv1alpha1.NewVersion().
					ID("openshift-v4.19.7").
					ChannelGroup("stable")),
		},
		{
			name: "converts candidate version from RP to CS (adds channel suffix)",
			hcpNodePool: getHCPNodePoolResource(
				func(hsc *api.HCPOpenShiftClusterNodePool) {
					hsc.Properties.Version = api.NodePoolVersionProfile{
						ID:           "4.19.19",
						ChannelGroup: "candidate",
					}
				},
			),
			expectedCSNodePool: getBaseCSNodePoolBuilder().
				Version(arohcpv1alpha1.NewVersion().
					ID("openshift-v4.19.19-candidate").
					ChannelGroup("candidate")),
		},
		{
			name: "converts nightly version from RP to CS with semver",
			hcpNodePool: getHCPNodePoolResource(
				func(hsc *api.HCPOpenShiftClusterNodePool) {
					hsc.Properties.Version = api.NodePoolVersionProfile{
						ID:           "4.19.0-0.nightly-2025-01-01",
						ChannelGroup: "nightly",
					}
				},
			),
			expectedCSNodePool: getBaseCSNodePoolBuilder().
				Version(arohcpv1alpha1.NewVersion().
					ID("openshift-v4.19.0-0.nightly-2025-01-01-nightly").
					ChannelGroup("nightly")),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			expected, err := tc.expectedCSNodePool.Build()
			require.NoError(t, err)
			generatedCSNodePoolBuilder, err := BuildCSNodePool(ctx, tc.hcpNodePool, false)
			require.NoError(t, err)
			generatedCSNodePool, err := generatedCSNodePoolBuilder.Build()
			require.NoError(t, err)
			assert.Equalf(t, expected, generatedCSNodePool, "BuildCSNodePool(%v, %v)", resourceID, expected)
		})
	}
}

func externalAuthResource(opts ...func(*api.HCPOpenShiftClusterExternalAuth)) *api.HCPOpenShiftClusterExternalAuth {
	externalAuth := api.NewDefaultHCPOpenShiftClusterExternalAuth(nil)

	for _, opt := range opts {
		opt(externalAuth)
	}
	return externalAuth
}

// Because we don't distinguish between unset and empty values in our JSON parsing
// we will get the resulting CS object from an empty HCPOpenShiftClusterExternalAuth object.
func getBaseCSExternalAuthBuilder() *arohcpv1alpha1.ExternalAuthBuilder {
	return arohcpv1alpha1.NewExternalAuth().
		ID("").
		Issuer(arohcpv1alpha1.NewTokenIssuer().
			Audiences().
			URL("").
			CA("")).
		Claim(arohcpv1alpha1.NewExternalAuthClaim().
			Mappings(arohcpv1alpha1.NewTokenClaimMappings().
				UserName(arohcpv1alpha1.NewUsernameClaim().
					Claim("").
					Prefix("").
					PrefixPolicy(""),
				),
			).
			ValidationRules(),
		).
		Clients()
}

func TestBuildCSExternalAuth(t *testing.T) {
	resourceID := testResourceID(t)
	testCases := []struct {
		name                   string
		hcpExternalAuth        *api.HCPOpenShiftClusterExternalAuth
		expectedCSExternalAuth *arohcpv1alpha1.ExternalAuthBuilder
	}{
		{
			name:                   "zero",
			hcpExternalAuth:        externalAuthResource(),
			expectedCSExternalAuth: getBaseCSExternalAuthBuilder(),
		},
		{
			name: "correctly parse PrefixPolicy",
			hcpExternalAuth: externalAuthResource(
				func(hsc *api.HCPOpenShiftClusterExternalAuth) {
					hsc.Properties.Claim.Mappings.Username.PrefixPolicy = api.UsernameClaimPrefixPolicyPrefix
				},
			),
			expectedCSExternalAuth: getBaseCSExternalAuthBuilder().Claim(arohcpv1alpha1.NewExternalAuthClaim().
				Mappings(arohcpv1alpha1.NewTokenClaimMappings().
					UserName(arohcpv1alpha1.NewUsernameClaim().
						Claim("").
						Prefix("").
						PrefixPolicy(string(api.UsernameClaimPrefixPolicyPrefix)),
					),
				).
				ValidationRules(),
			),
		},
		{
			name: "correctly parse Issuer",
			hcpExternalAuth: externalAuthResource(
				func(hsc *api.HCPOpenShiftClusterExternalAuth) {
					hsc.Properties.Issuer = api.TokenIssuerProfile{
						CA:        dummyCA,
						URL:       dummyURL,
						Audiences: dummyAudiences,
					}
				},
			),
			expectedCSExternalAuth: getBaseCSExternalAuthBuilder().Issuer(
				arohcpv1alpha1.NewTokenIssuer().
					CA(dummyCA).
					URL(dummyURL).
					Audiences(dummyAudiences...),
			),
		},
		{
			name: "correctly parse Claim",
			hcpExternalAuth: externalAuthResource(
				func(hsc *api.HCPOpenShiftClusterExternalAuth) {
					hsc.Properties.Claim = api.ExternalAuthClaimProfile{
						Mappings: api.TokenClaimMappingsProfile{
							Username: api.UsernameClaimProfile{
								Claim:        "a",
								Prefix:       "",
								PrefixPolicy: "None",
							},
							Groups: &api.GroupClaimProfile{
								Claim:  "b",
								Prefix: "",
							},
						},
						ValidationRules: []api.TokenClaimValidationRule{
							{
								Type: api.TokenValidationRuleTypeRequiredClaim,
								RequiredClaim: api.TokenRequiredClaim{
									Claim:         "A",
									RequiredValue: "B",
								},
							},
							{
								Type: api.TokenValidationRuleTypeRequiredClaim,
								RequiredClaim: api.TokenRequiredClaim{
									Claim:         "C",
									RequiredValue: "D",
								},
							},
						},
					}
				},
			),
			expectedCSExternalAuth: getBaseCSExternalAuthBuilder().Claim(
				arohcpv1alpha1.NewExternalAuthClaim().
					Mappings(arohcpv1alpha1.NewTokenClaimMappings().
						UserName(arohcpv1alpha1.NewUsernameClaim().
							Claim("a").
							Prefix("").
							PrefixPolicy(""),
						).
						Groups(arohcpv1alpha1.NewGroupsClaim().
							Claim("b").
							Prefix(""),
						),
					).
					ValidationRules([]*arohcpv1alpha1.TokenClaimValidationRuleBuilder{
						arohcpv1alpha1.NewTokenClaimValidationRule().
							Claim("A").
							RequiredValue("B"),
						arohcpv1alpha1.NewTokenClaimValidationRule().
							Claim("C").
							RequiredValue("D"),
					}...),
			),
		},
		{
			name: "handle multiple clients",
			hcpExternalAuth: externalAuthResource(
				func(hsc *api.HCPOpenShiftClusterExternalAuth) {
					hsc.Properties.Clients = []api.ExternalAuthClientProfile{
						{
							ClientID: "a",
							Type:     api.ExternalAuthClientTypeConfidential,
						},
						{
							ClientID: "b",
							Type:     api.ExternalAuthClientTypeConfidential,
						},
					}
				},
			),
			expectedCSExternalAuth: getBaseCSExternalAuthBuilder().Clients(
				[]*arohcpv1alpha1.ExternalAuthClientConfigBuilder{
					arohcpv1alpha1.NewExternalAuthClientConfig().
						ID("a").
						Component(arohcpv1alpha1.NewClientComponent().
							Name("").
							Namespace(""),
						).
						ExtraScopes().
						Type(arohcpv1alpha1.ExternalAuthClientTypeConfidential),
					arohcpv1alpha1.NewExternalAuthClientConfig().
						ID("b").
						Component(arohcpv1alpha1.NewClientComponent().
							Name("").
							Namespace(""),
						).
						ExtraScopes().
						Type(arohcpv1alpha1.ExternalAuthClientTypeConfidential),
				}...),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			expected, err := tc.expectedCSExternalAuth.Build()
			require.NoError(t, err)
			generatedCSExternalAuthBuilder, err := BuildCSExternalAuth(ctx, tc.hcpExternalAuth, false)
			require.NoError(t, err)
			generatedCSExternalAuth, err := generatedCSExternalAuthBuilder.Build()
			require.NoError(t, err)
			assert.Equalf(t, expected, generatedCSExternalAuth, "BuildCSExternalAuth(%v, %v)", resourceID, expected)
		})
	}
}

func getBaseCSClusterBuilder(updating bool) *arohcpv1alpha1.ClusterBuilder {
	var builder *arohcpv1alpha1.ClusterBuilder
	clusterAPIBuilder := arohcpv1alpha1.NewClusterAPI()

	if updating {
		builder = arohcpv1alpha1.NewCluster()
	} else {
		builder = ocmClusterDefaults(api.TestLocation)
		clusterAPIBuilder = clusterAPIBuilder.Listening(arohcpv1alpha1.ListeningMethodExternal)
	}

	// Add common mutable fields that BuildCSCluster always sets
	return builder.
		NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
			Unit(csNodeDrainGracePeriodUnit).
			Value(float64(0))).
		Autoscaler(arohcpv1alpha1.NewClusterAutoscaler().
			PodPriorityThreshold(-10).
			MaxNodeProvisionTime("15m").
			MaxPodGracePeriod(600).
			ResourceLimits(arohcpv1alpha1.NewAutoscalerResourceLimits().
				MaxNodesTotal(0))).
		API(clusterAPIBuilder.CIDRBlockAccess(arohcpv1alpha1.NewCIDRBlockAccess().
			Allow(arohcpv1alpha1.NewCIDRBlockAllowAccess().
				Mode(csCIDRBlockAllowAccessModeAllowAll))))
}

func TestBuildCSCluster(t *testing.T) {
	testCases := []struct {
		name              string
		hcpCluster        *api.HCPOpenShiftCluster
		updating          bool
		expectedCSCluster *arohcpv1alpha1.ClusterBuilder
		expectedError     string
	}{
		{
			name:     "CREATE - sets CIDRBlockAccess with nil AuthorizedCIDRs",
			updating: false,
			hcpCluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					API: api.CustomerAPIProfile{
						AuthorizedCIDRs: nil,
					},
				},
			},
			expectedCSCluster: getBaseCSClusterBuilder(false),
		},
		{
			name:     "CREATE - rejects empty AuthorizedCIDRs",
			updating: false,
			hcpCluster: func() *api.HCPOpenShiftCluster {
				cluster := api.MinimumValidClusterTestCase()
				cluster.CustomerProperties.API.AuthorizedCIDRs = make([]string, 0)
				return cluster
			}(),
			expectedError: "AuthorizedCIDRs cannot be an empty list",
		},
		{
			name:     "CREATE - sets CIDRBlockAccess with non-empty AuthorizedCIDRs",
			updating: false,
			hcpCluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					API: api.CustomerAPIProfile{
						Visibility:      api.VisibilityPrivate,
						AuthorizedCIDRs: []string{"10.0.0.0/8", "192.168.0.0/16"},
					},
				},
			},
			expectedCSCluster: getBaseCSClusterBuilder(false).
				API(arohcpv1alpha1.NewClusterAPI().
					Listening(arohcpv1alpha1.ListeningMethodInternal).
					CIDRBlockAccess(arohcpv1alpha1.NewCIDRBlockAccess().
						Allow(arohcpv1alpha1.NewCIDRBlockAllowAccess().
							Mode(csCIDRBlockAllowAccessModeAllowList).
							Values("10.0.0.0/8", "192.168.0.0/16")))),
		},
		{
			name:     "UPDATE - sets CIDRBlockAccess with nil AuthorizedCIDRs",
			updating: true,
			hcpCluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					API: api.CustomerAPIProfile{
						AuthorizedCIDRs: nil,
					},
				},
			},
			expectedCSCluster: getBaseCSClusterBuilder(true),
		},
		{
			name:     "UPDATE - rejects empty AuthorizedCIDRs",
			updating: true,
			hcpCluster: func() *api.HCPOpenShiftCluster {
				cluster := api.MinimumValidClusterTestCase()
				cluster.CustomerProperties.API.AuthorizedCIDRs = make([]string, 0)
				return cluster
			}(),
			expectedError: "AuthorizedCIDRs cannot be an empty list",
		},
		{
			name:     "UPDATE - sets only CIDRBlockAccess with non-empty AuthorizedCIDRs",
			updating: true,
			hcpCluster: &api.HCPOpenShiftCluster{
				CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
					API: api.CustomerAPIProfile{
						AuthorizedCIDRs: []string{"172.16.0.0/12", "203.0.113.0/24"},
					},
				},
			},
			expectedCSCluster: getBaseCSClusterBuilder(true).
				API(arohcpv1alpha1.NewClusterAPI().
					CIDRBlockAccess(arohcpv1alpha1.NewCIDRBlockAccess().
						Allow(arohcpv1alpha1.NewCIDRBlockAllowAccess().
							Mode(csCIDRBlockAllowAccessModeAllowList).
							Values("172.16.0.0/12", "203.0.113.0/24")))),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a complete minimal cluster for testing
			// For error test cases with expected errors, use the cluster as-is to preserve empty slices
			var hcpCluster *api.HCPOpenShiftCluster
			if tc.expectedError != "" {
				hcpCluster = tc.hcpCluster
			} else {
				hcpCluster = api.ClusterTestCase(t, tc.hcpCluster)
			}

			// Create request headers
			requestHeader := http.Header{}
			requestHeader.Set(arm.HeaderNameHomeTenantID, api.TestTenantID)
			requestHeader.Set(arm.HeaderNameIdentityURL, "")

			resourceID, err := azcorearm.ParseResourceID(api.TestClusterResourceID)
			require.NoError(t, err)

			// Build actual CS cluster
			actualClusterBuilder, actualAutoscalerBuilder, err := BuildCSCluster(resourceID, requestHeader, hcpCluster, tc.updating)

			if tc.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)
				return
			}

			require.NoError(t, err)

			// Build expected CS cluster
			expected, err := tc.expectedCSCluster.Build()
			require.NoError(t, err)

			actual, err := actualClusterBuilder.Autoscaler(actualAutoscalerBuilder).Build()
			require.NoError(t, err)

			// Compare
			assert.Equal(t, expected, actual)
		})
	}
}
