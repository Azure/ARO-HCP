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
	"strings"
	"testing"

	"dario.cat/mergo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	fleetapi "github.com/Azure/ARO-HCP/internal/apis/fleet"
	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
	armresourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources/arm"
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

func TestWithImmutableAttributes(t *testing.T) {
	testCases := []struct {
		name       string
		hcpCluster *resourcesapi.HCPOpenShiftCluster
		want       *arohcpv1alpha1.Cluster
	}{
		{
			name:       "simple default",
			hcpCluster: &resourcesapi.HCPOpenShiftCluster{},
			want:       ocmCluster(t, ocmClusterDefaults(resourcesapi.TestLocation)),
		},
		{
			name: "converts stable version from RP to CS (adds patch and prefix)",
			hcpCluster: &resourcesapi.HCPOpenShiftCluster{
				CustomerProperties: resourcesapi.HCPOpenShiftClusterCustomerProperties{
					Version: resourcesapi.VersionProfile{
						ID:           "4.20",
						ChannelGroup: "stable",
					},
				},
			},
			want: ocmCluster(t, ocmClusterDefaults(resourcesapi.TestLocation).
				Version(arohcpv1alpha1.NewVersion().
					ID("openshift-v4.20.20").
					ChannelGroup("stable"))),
		},
		{
			name: "converts candidate version from RP to CS (preserves patch)",
			hcpCluster: &resourcesapi.HCPOpenShiftCluster{
				CustomerProperties: resourcesapi.HCPOpenShiftClusterCustomerProperties{
					Version: resourcesapi.VersionProfile{
						ID:           "4.21.19",
						ChannelGroup: "candidate",
					},
				},
			},
			want: ocmCluster(t, ocmClusterDefaults(resourcesapi.TestLocation).
				Version(arohcpv1alpha1.NewVersion().
					ID("openshift-v4.21.19-candidate").
					ChannelGroup("candidate"))),
		},
		{
			name: "converts nightly version from RP to CS (preserves semver)",
			hcpCluster: &resourcesapi.HCPOpenShiftCluster{
				CustomerProperties: resourcesapi.HCPOpenShiftClusterCustomerProperties{
					Version: resourcesapi.VersionProfile{
						ID:           "4.21.0-0.nightly-2025-01-01",
						ChannelGroup: "nightly",
					},
				},
			},
			want: ocmCluster(t, ocmClusterDefaults(resourcesapi.TestLocation).
				Version(arohcpv1alpha1.NewVersion().
					ID("openshift-v4.21.0-0.nightly-2025-01-01-nightly").
					ChannelGroup("nightly"))),
		},
		{
			name: "with version 4.19",
			hcpCluster: &resourcesapi.HCPOpenShiftCluster{
				CustomerProperties: resourcesapi.HCPOpenShiftClusterCustomerProperties{
					Version: resourcesapi.VersionProfile{ID: "4.19", ChannelGroup: "stable"},
				},
			},
			want: ocmCluster(t, ocmClusterDefaults(resourcesapi.TestLocation).Version(
				arohcpv1alpha1.NewVersion().ID("openshift-v4.19.30").ChannelGroup("stable"))),
		},
		{
			name: "with version 4.21",
			hcpCluster: &resourcesapi.HCPOpenShiftCluster{
				CustomerProperties: resourcesapi.HCPOpenShiftClusterCustomerProperties{
					Version: resourcesapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"},
				},
			},
			want: ocmCluster(t, ocmClusterDefaults(resourcesapi.TestLocation).Version(
				arohcpv1alpha1.NewVersion().ID("openshift-v4.21.14").ChannelGroup("stable"))),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, arohcpv1alpha1.MarshalCluster(tc.want, &buf))
			want := buf.String()
			builder, err := withImmutableAttributes(
				ocmClusterDefaults(resourcesapi.TestLocation),
				resourcesapi.ClusterTestCase(t, tc.hcpCluster),
				resourcesapi.TestSubscriptionID,
				resourcesapi.TestResourceGroupName,
				resourcesapi.TestTenantID,
				resourcesapi.TestManagedIdentitiesDataPlaneIdentityURL,
			)
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
	resourceID, err := azcorearm.ParseResourceID(resourcesapi.TestClusterResourceID)
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

	data, err := armresourcesapi.MarshalJSON(mergedCluster)
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
			ManagedResourceGroupName(resourcesapi.TestManagedResourceGroupName).
			NetworkSecurityGroupResourceID(resourcesapi.TestNetworkSecurityGroupResourceID).
			NodesOutboundConnectivity(arohcpv1alpha1.NewAzureNodesOutboundConnectivity().
				OutboundType(csOutboundType)).
			OperatorsAuthentication(arohcpv1alpha1.NewAzureOperatorsAuthentication().
				ManagedIdentities(arohcpv1alpha1.NewAzureOperatorsAuthenticationManagedIdentities().
					ControlPlaneOperatorsManagedIdentities(make(map[string]*arohcpv1alpha1.AzureControlPlaneManagedIdentityBuilder)).
					DataPlaneOperatorsManagedIdentities(make(map[string]*arohcpv1alpha1.AzureDataPlaneManagedIdentityBuilder)).
					ManagedIdentitiesDataPlaneIdentityUrl(resourcesapi.TestManagedIdentitiesDataPlaneIdentityURL))).
			ResourceGroupName(strings.ToLower(resourcesapi.TestResourceGroupName)).
			ResourceName(strings.ToLower(resourcesapi.TestClusterName)).
			SubnetResourceID(resourcesapi.TestSubnetResourceID).
			VnetIntegrationSubnetResourceID(resourcesapi.TestVnetIntegrationSubnetResourceID).
			SubscriptionID(strings.ToLower(resourcesapi.TestSubscriptionID)).
			TenantID(resourcesapi.TestTenantID),
		).
		CCS(arohcpv1alpha1.NewCCS().Enabled(true)).
		CloudProvider(arohcpv1alpha1.NewCloudProvider().
			ID("azure")).
		DomainPrefix("testcluster").
		Hypershift(arohcpv1alpha1.NewHypershift().
			Enabled(true)).
		Name(strings.ToLower(resourcesapi.TestClusterName)).
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
			ID("openshift-v4.20.20").
			ChannelGroup("stable")).
		ImageRegistry(arohcpv1alpha1.NewClusterImageRegistry().
			State(csImageRegistryStateEnabled)).
		RegistryConfig(arohcpv1alpha1.NewClusterRegistryConfig().
			ImageDigestMirrors())
}

func getHCPNodePoolResource(opts ...func(*resourcesapi.HCPOpenShiftClusterNodePool)) *resourcesapi.HCPOpenShiftClusterNodePool {
	nodePool := &resourcesapi.HCPOpenShiftClusterNodePool{
		Properties: resourcesapi.HCPOpenShiftClusterNodePoolProperties{
			Platform: resourcesapi.NodePoolPlatformProfile{
				OSDisk: resourcesapi.OSDiskProfile{
					// SizeGiB is initialized to 64 to reflect the default value set by SetDefaultValuesNodePool
					// in the real API flow. This ensures tests match production behavior where SizeGiB is never nil.
					SizeGiB:                ptr.To(int32(64)),
					DiskStorageAccountType: resourcesapi.DiskStorageAccountTypePremium_LRS,
					DiskType:               resourcesapi.OsDiskTypeManaged,
				},
			},
		},
	}

	for _, opt := range opts {
		opt(nodePool)
	}
	return nodePool
}

// Base CS nodepool builder that reflects the defaults set in getHCPNodePoolResource.
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
				SizeGibibytes(64).
				StorageAccountType(string(resourcesapi.DiskStorageAccountTypePremium_LRS)).
				Persistence("persistent"),
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
		hcpNodePool        *resourcesapi.HCPOpenShiftClusterNodePool
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
				func(hsc *resourcesapi.HCPOpenShiftClusterNodePool) {
					hsc.Properties.Taints = []resourcesapi.Taint{
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
				func(hsc *resourcesapi.HCPOpenShiftClusterNodePool) {
					hsc.Properties.Version = resourcesapi.NodePoolVersionProfile{
						ID:           "4.20",
						ChannelGroup: "stable",
					}
				},
			),
			expectedCSNodePool: getBaseCSNodePoolBuilder().
				Version(arohcpv1alpha1.NewVersion().
					ID("openshift-v4.20.20").
					ChannelGroup("stable")),
		},
		{
			name: "converts candidate version from RP to CS (adds channel suffix)",
			hcpNodePool: getHCPNodePoolResource(
				func(hsc *resourcesapi.HCPOpenShiftClusterNodePool) {
					hsc.Properties.Version = resourcesapi.NodePoolVersionProfile{
						ID:           "4.21.19",
						ChannelGroup: "candidate",
					}
				},
			),
			expectedCSNodePool: getBaseCSNodePoolBuilder().
				Version(arohcpv1alpha1.NewVersion().
					ID("openshift-v4.21.19-candidate").
					ChannelGroup("candidate")),
		},
		{
			name: "converts nightly version from RP to CS with semver",
			hcpNodePool: getHCPNodePoolResource(
				func(hsc *resourcesapi.HCPOpenShiftClusterNodePool) {
					hsc.Properties.Version = resourcesapi.NodePoolVersionProfile{
						ID:           "4.21.0-0.nightly-2025-01-01",
						ChannelGroup: "nightly",
					}
				},
			),
			expectedCSNodePool: getBaseCSNodePoolBuilder().
				Version(arohcpv1alpha1.NewVersion().
					ID("openshift-v4.21.0-0.nightly-2025-01-01-nightly").
					ChannelGroup("nightly")),
		},
		{
			name: "converts ephemeral disk type from RP to CS",
			hcpNodePool: getHCPNodePoolResource(
				func(hsc *resourcesapi.HCPOpenShiftClusterNodePool) {
					hsc.Properties.Platform.OSDisk.DiskType = resourcesapi.OsDiskTypeEphemeral
				},
			),
			expectedCSNodePool: getBaseCSNodePoolBuilder().
				AzureNodePool(arohcpv1alpha1.NewAzureNodePool().
					ResourceName("").
					VMSize("").
					EncryptionAtHost(
						arohcpv1alpha1.NewAzureNodePoolEncryptionAtHost().
							State(csEncryptionAtHostStateDisabled),
					).
					OsDisk(arohcpv1alpha1.NewAzureNodePoolOsDisk().
						SizeGibibytes(64).
						StorageAccountType(string(resourcesapi.DiskStorageAccountTypePremium_LRS)).
						Persistence("ephemeral"),
					),
				),
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

func externalAuthResource(opts ...func(*resourcesapi.HCPOpenShiftClusterExternalAuth)) *resourcesapi.HCPOpenShiftClusterExternalAuth {
	externalAuth := resourcesapi.NewDefaultHCPOpenShiftClusterExternalAuth(nil)

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
		hcpExternalAuth        *resourcesapi.HCPOpenShiftClusterExternalAuth
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
				func(hsc *resourcesapi.HCPOpenShiftClusterExternalAuth) {
					hsc.Properties.Claim.Mappings.Username.PrefixPolicy = resourcesapi.UsernameClaimPrefixPolicyPrefix
				},
			),
			expectedCSExternalAuth: getBaseCSExternalAuthBuilder().Claim(arohcpv1alpha1.NewExternalAuthClaim().
				Mappings(arohcpv1alpha1.NewTokenClaimMappings().
					UserName(arohcpv1alpha1.NewUsernameClaim().
						Claim("").
						Prefix("").
						PrefixPolicy(string(resourcesapi.UsernameClaimPrefixPolicyPrefix)),
					),
				).
				ValidationRules(),
			),
		},
		{
			name: "correctly parse Issuer",
			hcpExternalAuth: externalAuthResource(
				func(hsc *resourcesapi.HCPOpenShiftClusterExternalAuth) {
					hsc.Properties.Issuer = resourcesapi.TokenIssuerProfile{
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
				func(hsc *resourcesapi.HCPOpenShiftClusterExternalAuth) {
					hsc.Properties.Claim = resourcesapi.ExternalAuthClaimProfile{
						Mappings: resourcesapi.TokenClaimMappingsProfile{
							Username: resourcesapi.UsernameClaimProfile{
								Claim:        "a",
								Prefix:       "",
								PrefixPolicy: "None",
							},
							Groups: &resourcesapi.GroupClaimProfile{
								Claim:  "b",
								Prefix: "",
							},
						},
						ValidationRules: []resourcesapi.TokenClaimValidationRule{
							{
								Type: resourcesapi.TokenValidationRuleTypeRequiredClaim,
								RequiredClaim: resourcesapi.TokenRequiredClaim{
									Claim:         "A",
									RequiredValue: "B",
								},
							},
							{
								Type: resourcesapi.TokenValidationRuleTypeRequiredClaim,
								RequiredClaim: resourcesapi.TokenRequiredClaim{
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
				func(hsc *resourcesapi.HCPOpenShiftClusterExternalAuth) {
					hsc.Properties.Clients = []resourcesapi.ExternalAuthClientProfile{
						{
							ClientID: "a",
							Type:     resourcesapi.ExternalAuthClientTypeConfidential,
						},
						{
							ClientID: "b",
							Type:     resourcesapi.ExternalAuthClientTypeConfidential,
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
		builder = ocmClusterDefaults(resourcesapi.TestLocation)
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
		Properties(map[string]string{}).
		API(clusterAPIBuilder.CIDRBlockAccess(arohcpv1alpha1.NewCIDRBlockAccess().
			Allow(arohcpv1alpha1.NewCIDRBlockAllowAccess().
				Mode(csCIDRBlockAllowAccessModeAllowAll)))).
		RegistryConfig(arohcpv1alpha1.NewClusterRegistryConfig().ImageDigestMirrors())
}

func TestBuildCSCluster(t *testing.T) {
	testCases := []struct {
		name                     string
		hcpCluster               *resourcesapi.HCPOpenShiftCluster
		requiredProperties       map[string]string
		oldClusterServiceCluster *arohcpv1alpha1.Cluster
		expectedCSCluster        *arohcpv1alpha1.ClusterBuilder
		expectedError            string
	}{
		{
			name: "CREATE - sets CIDRBlockAccess with nil AuthorizedCIDRs",
			hcpCluster: &resourcesapi.HCPOpenShiftCluster{
				CustomerProperties: resourcesapi.HCPOpenShiftClusterCustomerProperties{
					API: resourcesapi.CustomerAPIProfile{
						AuthorizedCIDRs: nil,
					},
				},
			},
			expectedCSCluster: getBaseCSClusterBuilder(false),
		},
		{
			name: "CREATE - rejects empty AuthorizedCIDRs",
			hcpCluster: func() *resourcesapi.HCPOpenShiftCluster {
				cluster := resourcesapi.MinimumValidClusterTestCase()
				cluster.CustomerProperties.API.AuthorizedCIDRs = make([]string, 0)
				return cluster
			}(),
			expectedError: "AuthorizedCIDRs cannot be an empty list",
		},
		{
			name: "CREATE - sets CIDRBlockAccess with non-empty AuthorizedCIDRs",
			hcpCluster: &resourcesapi.HCPOpenShiftCluster{
				CustomerProperties: resourcesapi.HCPOpenShiftClusterCustomerProperties{
					API: resourcesapi.CustomerAPIProfile{
						Visibility:      resourcesapi.VisibilityPrivate,
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
			name: "UPDATE - sets CIDRBlockAccess with nil AuthorizedCIDRs",
			oldClusterServiceCluster: func() *arohcpv1alpha1.Cluster {
				c, err := arohcpv1alpha1.NewCluster().Build()
				if err != nil {
					panic(err)
				}
				return c
			}(),
			hcpCluster: &resourcesapi.HCPOpenShiftCluster{
				CustomerProperties: resourcesapi.HCPOpenShiftClusterCustomerProperties{
					API: resourcesapi.CustomerAPIProfile{
						AuthorizedCIDRs: nil,
					},
				},
			},
			expectedCSCluster: getBaseCSClusterBuilder(true),
		},
		{
			name: "UPDATE - rejects empty AuthorizedCIDRs",
			oldClusterServiceCluster: func() *arohcpv1alpha1.Cluster {
				c, err := arohcpv1alpha1.NewCluster().Build()
				if err != nil {
					panic(err)
				}
				return c
			}(),
			hcpCluster: func() *resourcesapi.HCPOpenShiftCluster {
				cluster := resourcesapi.MinimumValidClusterTestCase()
				cluster.CustomerProperties.API.AuthorizedCIDRs = make([]string, 0)
				return cluster
			}(),
			expectedError: "AuthorizedCIDRs cannot be an empty list",
		},
		{
			name: "UPDATE - sets only CIDRBlockAccess with non-empty AuthorizedCIDRs",
			oldClusterServiceCluster: func() *arohcpv1alpha1.Cluster {
				c, err := arohcpv1alpha1.NewCluster().Build()
				if err != nil {
					panic(err)
				}
				return c
			}(),
			hcpCluster: &resourcesapi.HCPOpenShiftCluster{
				CustomerProperties: resourcesapi.HCPOpenShiftClusterCustomerProperties{
					API: resourcesapi.CustomerAPIProfile{
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
		{
			name: "CREATE - sets experimental feature properties to true",
			hcpCluster: &resourcesapi.HCPOpenShiftCluster{
				ServiceProviderProperties: resourcesapi.HCPOpenShiftClusterServiceProviderProperties{
					ExperimentalFeatures: resourcesapi.ExperimentalFeatures{
						ControlPlaneAvailability: resourcesapi.SingleReplicaControlPlane,
						ControlPlanePodSizing:    resourcesapi.MinimalControlPlanePodSizing,
					},
				},
			},
			expectedCSCluster: getBaseCSClusterBuilder(false).
				Properties(map[string]string{
					"hosted_cluster_single_replica": "true",
					"hosted_cluster_size_override":  "true",
				}),
		},
		{
			name: "CREATE - sets only single-replica",
			hcpCluster: &resourcesapi.HCPOpenShiftCluster{
				ServiceProviderProperties: resourcesapi.HCPOpenShiftClusterServiceProviderProperties{
					ExperimentalFeatures: resourcesapi.ExperimentalFeatures{
						ControlPlaneAvailability: resourcesapi.SingleReplicaControlPlane,
					},
				},
			},
			expectedCSCluster: getBaseCSClusterBuilder(false).
				Properties(map[string]string{
					"hosted_cluster_single_replica": "true",
				}),
		},
		{
			name: "UPDATE - tag removal clears previously set properties",
			oldClusterServiceCluster: func() *arohcpv1alpha1.Cluster {
				c, err := arohcpv1alpha1.NewCluster().Properties(map[string]string{
					"hosted_cluster_single_replica": "true",
					"hosted_cluster_size_override":  "true",
				}).Build()
				if err != nil {
					panic(err)
				}
				return c
			}(),
			hcpCluster: &resourcesapi.HCPOpenShiftCluster{},
			expectedCSCluster: getBaseCSClusterBuilder(true).
				Properties(map[string]string{}),
		},
		{
			name: "UPDATE - partial feature disablement keeps remaining feature",
			oldClusterServiceCluster: func() *arohcpv1alpha1.Cluster {
				c, err := arohcpv1alpha1.NewCluster().Properties(map[string]string{
					"hosted_cluster_single_replica": "true",
					"hosted_cluster_size_override":  "true",
				}).Build()
				if err != nil {
					panic(err)
				}
				return c
			}(),
			hcpCluster: &resourcesapi.HCPOpenShiftCluster{
				ServiceProviderProperties: resourcesapi.HCPOpenShiftClusterServiceProviderProperties{
					ExperimentalFeatures: resourcesapi.ExperimentalFeatures{
						ControlPlanePodSizing: resourcesapi.MinimalControlPlanePodSizing,
					},
				},
			},
			expectedCSCluster: getBaseCSClusterBuilder(true).
				Properties(map[string]string{
					"hosted_cluster_size_override": "true",
				}),
		},
		{
			name: "UPDATE - preserves non-experimental old properties",
			oldClusterServiceCluster: func() *arohcpv1alpha1.Cluster {
				c, err := arohcpv1alpha1.NewCluster().Properties(map[string]string{
					"provisioner_noop_provision":    "true",
					"provisioner_noop_deprovision":  "true",
					"hosted_cluster_single_replica": "true",
				}).Build()
				if err != nil {
					panic(err)
				}
				return c
			}(),
			hcpCluster: &resourcesapi.HCPOpenShiftCluster{
				ServiceProviderProperties: resourcesapi.HCPOpenShiftClusterServiceProviderProperties{
					ExperimentalFeatures: resourcesapi.ExperimentalFeatures{
						ControlPlaneAvailability: resourcesapi.SingleReplicaControlPlane,
					},
				},
			},
			expectedCSCluster: getBaseCSClusterBuilder(true).
				Properties(map[string]string{
					"provisioner_noop_provision":    "true",
					"provisioner_noop_deprovision":  "true",
					"hosted_cluster_single_replica": "true",
				}),
		},
		{
			name: "CREATE - required properties merged with experimental features",
			requiredProperties: map[string]string{
				"provision_shard_id":           "test-shard",
				"provisioner_noop_provision":   "true",
				"provisioner_noop_deprovision": "true",
			},
			hcpCluster: &resourcesapi.HCPOpenShiftCluster{
				ServiceProviderProperties: resourcesapi.HCPOpenShiftClusterServiceProviderProperties{
					ExperimentalFeatures: resourcesapi.ExperimentalFeatures{
						ControlPlaneAvailability: resourcesapi.SingleReplicaControlPlane,
						ControlPlanePodSizing:    resourcesapi.MinimalControlPlanePodSizing,
					},
				},
			},
			expectedCSCluster: getBaseCSClusterBuilder(false).
				Properties(map[string]string{
					"provision_shard_id":            "test-shard",
					"provisioner_noop_provision":    "true",
					"provisioner_noop_deprovision":  "true",
					"hosted_cluster_single_replica": "true",
					"hosted_cluster_size_override":  "true",
				}),
		},
		{
			name: "CREATE - experimental features override conflicting required properties",
			requiredProperties: map[string]string{
				"hosted_cluster_single_replica": "false",
				"hosted_cluster_size_override":  "false",
			},
			hcpCluster: &resourcesapi.HCPOpenShiftCluster{
				ServiceProviderProperties: resourcesapi.HCPOpenShiftClusterServiceProviderProperties{
					ExperimentalFeatures: resourcesapi.ExperimentalFeatures{
						ControlPlaneAvailability: resourcesapi.SingleReplicaControlPlane,
						ControlPlanePodSizing:    resourcesapi.MinimalControlPlanePodSizing,
					},
				},
			},
			expectedCSCluster: getBaseCSClusterBuilder(false).
				Properties(map[string]string{
					"hosted_cluster_single_replica": "true",
					"hosted_cluster_size_override":  "true",
				}),
		},
		{
			name: "CREATE - sets some image digest mirrors",
			hcpCluster: &resourcesapi.HCPOpenShiftCluster{
				CustomerProperties: resourcesapi.HCPOpenShiftClusterCustomerProperties{
					ImageDigestMirrors: []resourcesapi.ImageDigestMirror{
						{
							Source:  "sourceRegistry1",
							Mirrors: []string{"mirrorRegistry1a", "mirrorRegistry1b"},
						},
						{
							Source:  "sourceRegistry2",
							Mirrors: []string{"mirrorRegistry2a", "mirrorRegistry2b"},
						},
					},
				},
			},
			expectedCSCluster: getBaseCSClusterBuilder(false).
				RegistryConfig(arohcpv1alpha1.NewClusterRegistryConfig().
					ImageDigestMirrors(
						arohcpv1alpha1.NewImageMirror().
							Source("sourceRegistry1").
							Mirrors("mirrorRegistry1a", "mirrorRegistry1b"),
						arohcpv1alpha1.NewImageMirror().
							Source("sourceRegistry2").
							Mirrors("mirrorRegistry2a", "mirrorRegistry2b"),
					),
				),
		},
		{
			name:       "UPDATE - clears all image digest mirrors",
			hcpCluster: &resourcesapi.HCPOpenShiftCluster{},
			oldClusterServiceCluster: func() *arohcpv1alpha1.Cluster {
				c, err := getBaseCSClusterBuilder(false).
					RegistryConfig(arohcpv1alpha1.NewClusterRegistryConfig().
						ImageDigestMirrors(
							arohcpv1alpha1.NewImageMirror().
								Source("sourceRegistry1").
								Mirrors("mirrorRegistry1a", "mirrorRegistry1b"),
							arohcpv1alpha1.NewImageMirror().
								Source("sourceRegistry2").
								Mirrors("mirrorRegistry2a", "mirrorRegistry2b"),
						),
					).Build()
				if err != nil {
					panic(err)
				}
				return c
			}(),
			expectedCSCluster: getBaseCSClusterBuilder(true),
		},
		{
			name: "CREATE - converts KMS encryption with Public visibility",
			hcpCluster: func() *resourcesapi.HCPOpenShiftCluster {
				cluster := resourcesapi.MinimumValidClusterTestCase()
				cluster.CustomerProperties.Etcd.DataEncryption.KeyManagementMode = resourcesapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged
				cluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged = &resourcesapi.CustomerManagedEncryptionProfile{
					EncryptionType: resourcesapi.CustomerManagedEncryptionTypeKMS,
					Kms: &resourcesapi.KmsEncryptionProfile{
						Visibility: resourcesapi.KeyVaultVisibilityPublic,
						ActiveKey: resourcesapi.KmsKey{
							Name:      "test-key",
							VaultName: "test-vault",
							Version:   "v1",
						},
					},
				}
				return cluster
			}(),
			expectedCSCluster: ocmClusterDefaults(resourcesapi.TestLocation).
				NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
					Unit(csNodeDrainGracePeriodUnit).
					Value(float64(0))).
				Autoscaler(arohcpv1alpha1.NewClusterAutoscaler().
					PodPriorityThreshold(-10).
					MaxNodeProvisionTime("15m").
					MaxPodGracePeriod(600).
					ResourceLimits(arohcpv1alpha1.NewAutoscalerResourceLimits().
						MaxNodesTotal(0))).
				Properties(map[string]string{}).
				API(arohcpv1alpha1.NewClusterAPI().
					Listening(arohcpv1alpha1.ListeningMethodExternal).
					CIDRBlockAccess(arohcpv1alpha1.NewCIDRBlockAccess().
						Allow(arohcpv1alpha1.NewCIDRBlockAllowAccess().
							Mode(csCIDRBlockAllowAccessModeAllowAll)))).
				Azure(arohcpv1alpha1.NewAzure().
					EtcdEncryption(arohcpv1alpha1.NewAzureEtcdEncryption().
						DataEncryption(arohcpv1alpha1.NewAzureEtcdDataEncryption().
							KeyManagementMode(csKeyManagementModeCustomerManaged).
							CustomerManaged(arohcpv1alpha1.NewAzureEtcdDataEncryptionCustomerManaged().
								EncryptionType("kms").
								Kms(arohcpv1alpha1.NewAzureKmsEncryption().
									Visibility(arohcpv1alpha1.AzureKmsEncryptionVisibilityPublic).
									ActiveKey(arohcpv1alpha1.NewAzureKmsKey().
										KeyName("test-key").
										KeyVaultName("test-vault").
										KeyVersion("v1"),
									),
								),
							),
						)).
					ManagedResourceGroupName(resourcesapi.TestManagedResourceGroupName).
					NetworkSecurityGroupResourceID(resourcesapi.TestNetworkSecurityGroupResourceID).
					NodesOutboundConnectivity(arohcpv1alpha1.NewAzureNodesOutboundConnectivity().
						OutboundType(csOutboundType)).
					OperatorsAuthentication(arohcpv1alpha1.NewAzureOperatorsAuthentication().
						ManagedIdentities(arohcpv1alpha1.NewAzureOperatorsAuthenticationManagedIdentities().
							ControlPlaneOperatorsManagedIdentities(make(map[string]*arohcpv1alpha1.AzureControlPlaneManagedIdentityBuilder)).
							DataPlaneOperatorsManagedIdentities(make(map[string]*arohcpv1alpha1.AzureDataPlaneManagedIdentityBuilder)).
							ManagedIdentitiesDataPlaneIdentityUrl(resourcesapi.TestManagedIdentitiesDataPlaneIdentityURL))).
					ResourceGroupName(strings.ToLower(resourcesapi.TestResourceGroupName)).
					ResourceName(strings.ToLower(resourcesapi.TestClusterName)).
					SubnetResourceID(resourcesapi.TestSubnetResourceID).
					VnetIntegrationSubnetResourceID(resourcesapi.TestVnetIntegrationSubnetResourceID).
					SubscriptionID(strings.ToLower(resourcesapi.TestSubscriptionID)).
					TenantID(resourcesapi.TestTenantID),
				),
		},
		{
			name: "CREATE - converts KMS encryption with Private visibility",
			hcpCluster: func() *resourcesapi.HCPOpenShiftCluster {
				cluster := resourcesapi.MinimumValidClusterTestCase()
				cluster.CustomerProperties.Etcd.DataEncryption.KeyManagementMode = resourcesapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged
				cluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged = &resourcesapi.CustomerManagedEncryptionProfile{
					EncryptionType: resourcesapi.CustomerManagedEncryptionTypeKMS,
					Kms: &resourcesapi.KmsEncryptionProfile{
						Visibility: resourcesapi.KeyVaultVisibilityPrivate,
						ActiveKey: resourcesapi.KmsKey{
							Name:      "test-key",
							VaultName: "test-vault",
							Version:   "v1",
						},
					},
				}
				return cluster
			}(),
			expectedCSCluster: ocmClusterDefaults(resourcesapi.TestLocation).
				NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
					Unit(csNodeDrainGracePeriodUnit).
					Value(float64(0))).
				Autoscaler(arohcpv1alpha1.NewClusterAutoscaler().
					PodPriorityThreshold(-10).
					MaxNodeProvisionTime("15m").
					MaxPodGracePeriod(600).
					ResourceLimits(arohcpv1alpha1.NewAutoscalerResourceLimits().
						MaxNodesTotal(0))).
				Properties(map[string]string{}).
				API(arohcpv1alpha1.NewClusterAPI().
					Listening(arohcpv1alpha1.ListeningMethodExternal).
					CIDRBlockAccess(arohcpv1alpha1.NewCIDRBlockAccess().
						Allow(arohcpv1alpha1.NewCIDRBlockAllowAccess().
							Mode(csCIDRBlockAllowAccessModeAllowAll)))).
				Azure(arohcpv1alpha1.NewAzure().
					EtcdEncryption(arohcpv1alpha1.NewAzureEtcdEncryption().
						DataEncryption(arohcpv1alpha1.NewAzureEtcdDataEncryption().
							KeyManagementMode(csKeyManagementModeCustomerManaged).
							CustomerManaged(arohcpv1alpha1.NewAzureEtcdDataEncryptionCustomerManaged().
								EncryptionType("kms").
								Kms(arohcpv1alpha1.NewAzureKmsEncryption().
									Visibility(arohcpv1alpha1.AzureKmsEncryptionVisibilityPrivate).
									ActiveKey(arohcpv1alpha1.NewAzureKmsKey().
										KeyName("test-key").
										KeyVaultName("test-vault").
										KeyVersion("v1"),
									),
								),
							),
						)).
					ManagedResourceGroupName(resourcesapi.TestManagedResourceGroupName).
					NetworkSecurityGroupResourceID(resourcesapi.TestNetworkSecurityGroupResourceID).
					NodesOutboundConnectivity(arohcpv1alpha1.NewAzureNodesOutboundConnectivity().
						OutboundType(csOutboundType)).
					OperatorsAuthentication(arohcpv1alpha1.NewAzureOperatorsAuthentication().
						ManagedIdentities(arohcpv1alpha1.NewAzureOperatorsAuthenticationManagedIdentities().
							ControlPlaneOperatorsManagedIdentities(make(map[string]*arohcpv1alpha1.AzureControlPlaneManagedIdentityBuilder)).
							DataPlaneOperatorsManagedIdentities(make(map[string]*arohcpv1alpha1.AzureDataPlaneManagedIdentityBuilder)).
							ManagedIdentitiesDataPlaneIdentityUrl(resourcesapi.TestManagedIdentitiesDataPlaneIdentityURL))).
					ResourceGroupName(strings.ToLower(resourcesapi.TestResourceGroupName)).
					ResourceName(strings.ToLower(resourcesapi.TestClusterName)).
					SubnetResourceID(resourcesapi.TestSubnetResourceID).
					VnetIntegrationSubnetResourceID(resourcesapi.TestVnetIntegrationSubnetResourceID).
					SubscriptionID(strings.ToLower(resourcesapi.TestSubscriptionID)).
					TenantID(resourcesapi.TestTenantID),
				),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a complete minimal cluster for testing
			// For error test cases with expected errors, use the cluster as-is to preserve empty slices
			var hcpCluster *resourcesapi.HCPOpenShiftCluster
			if tc.expectedError != "" {
				hcpCluster = tc.hcpCluster
			} else {
				hcpCluster = resourcesapi.ClusterTestCase(t, tc.hcpCluster)
			}

			hcpCluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL = resourcesapi.TestManagedIdentitiesDataPlaneIdentityURL

			resourceID, err := azcorearm.ParseResourceID(resourcesapi.TestClusterResourceID)
			require.NoError(t, err)

			// Build actual CS cluster
			actualClusterBuilder, actualAutoscalerBuilder, err := BuildCSCluster(resourceID, resourcesapi.TestTenantID, hcpCluster, tc.requiredProperties, tc.oldClusterServiceCluster)

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

// validProvisionShardBuilder returns a builder pre-populated with all required fields
// for a valid management cluster conversion. Tests can override individual fields.
func validProvisionShardBuilder(t *testing.T) *arohcpv1alpha1.ProvisionShardBuilder {
	t.Helper()
	return arohcpv1alpha1.NewProvisionShard().
		ID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee").
		HREF("/api/aro_hcp/v1alpha1/provision_shards/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee").
		Status(csProvisioningShardStatusActive).
		Topology("shared").
		AzureShard(arohcpv1alpha1.NewAzureShard().
			AksManagementClusterResourceId("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/test-westus3-mgmt-1").
			PublicDnsZoneResourceId("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dns-rg/providers/Microsoft.Network/dnszones/test.example.com").
			CxSecretsKeyVaultUrl("https://cx-kv.vault.azure.net/").
			CxManagedIdentitiesKeyVaultUrl("https://mi-kv.vault.azure.net/").
			CxSecretsKeyVaultManagedIdentityClientId("c2bde1aa-d904-48cd-a728-9de33e3ddca9"),
		).
		MaestroConfig(
			arohcpv1alpha1.NewProvisionShardMaestroConfig().
				ConsumerName("test-consumer").
				RestApiConfig(arohcpv1alpha1.NewProvisionShardMaestroRestApiConfig().
					Url("http://maestro.maestro.svc.cluster.local:8000")).
				GrpcApiConfig(arohcpv1alpha1.NewProvisionShardMaestroGrpcApiConfig().
					Url("maestro-grpc.maestro.svc.cluster.local:8090")),
		)
}

func TestConvertCSManagementClusterToInternal(t *testing.T) {
	tests := []struct {
		name                string
		build               func(t *testing.T) *arohcpv1alpha1.ProvisionShard
		expectedErrorSubstr string
		validate            func(t *testing.T, mc *fleetapi.ManagementCluster)
	}{
		{
			name: "nil shard",
			build: func(t *testing.T) *arohcpv1alpha1.ProvisionShard {
				return nil
			},
			expectedErrorSubstr: "provision shard is nil",
		},
		{
			name: "empty shard HREF",
			build: func(t *testing.T) *arohcpv1alpha1.ProvisionShard {
				shard, err := arohcpv1alpha1.NewProvisionShard().Build()
				require.NoError(t, err)
				return shard
			},
			expectedErrorSubstr: "provision shard has empty HREF",
		},
		{
			name: "invalid AKS resource ID",
			build: func(t *testing.T) *arohcpv1alpha1.ProvisionShard {
				shard, err := arohcpv1alpha1.NewProvisionShard().
					ID("11111111-2222-3333-4444-555555555555").
					HREF("/api/aro_hcp/v1alpha1/provision_shards/11111111-2222-3333-4444-555555555555").
					AzureShard(arohcpv1alpha1.NewAzureShard().
						AksManagementClusterResourceId("not-a-valid-resource-id")).
					Build()
				require.NoError(t, err)
				return shard
			},
			expectedErrorSubstr: "failed to parse management cluster AKS resource ID",
		},
		{
			name: "invalid public DNS zone resource ID",
			build: func(t *testing.T) *arohcpv1alpha1.ProvisionShard {
				shard, err := arohcpv1alpha1.NewProvisionShard().
					ID("11111111-2222-3333-4444-555555555555").
					HREF("/api/aro_hcp/v1alpha1/provision_shards/11111111-2222-3333-4444-555555555555").
					AzureShard(arohcpv1alpha1.NewAzureShard().
						AksManagementClusterResourceId("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/test-westus3-mgmt-1").
						PublicDnsZoneResourceId("not-valid")).
					Build()
				require.NoError(t, err)
				return shard
			},
			expectedErrorSubstr: "failed to parse public DNS zone resource ID",
		},
		{
			name: "missing maestro config",
			build: func(t *testing.T) *arohcpv1alpha1.ProvisionShard {
				shard, err := arohcpv1alpha1.NewProvisionShard().
					ID("11111111-2222-3333-4444-555555555555").
					HREF("/api/aro_hcp/v1alpha1/provision_shards/11111111-2222-3333-4444-555555555555").
					AzureShard(arohcpv1alpha1.NewAzureShard().
						AksManagementClusterResourceId("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/test-westus3-mgmt-1").
						PublicDnsZoneResourceId("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dns-rg/providers/Microsoft.Network/dnszones/test.example.com").
						CxSecretsKeyVaultUrl("https://cx-kv.vault.azure.net/").
						CxManagedIdentitiesKeyVaultUrl("https://mi-kv.vault.azure.net/").
						CxSecretsKeyVaultManagedIdentityClientId("c2bde1aa-d904-48cd-a728-9de33e3ddca9"),
					).
					Build()
				require.NoError(t, err)
				return shard
			},
			expectedErrorSubstr: "no maestro config",
		},
		{
			name: "successful conversion populates all fields",
			build: func(t *testing.T) *arohcpv1alpha1.ProvisionShard {
				shard, err := validProvisionShardBuilder(t).Build()
				require.NoError(t, err)
				return shard
			},
			validate: func(t *testing.T, mc *fleetapi.ManagementCluster) {
				// ResourceID
				expectedResourceID := resourcesapi.Must(fleetapi.ToManagementClusterResourceID("1"))
				require.NotNil(t, mc.ResourceID)
				assert.Equal(t, expectedResourceID.String(), mc.ResourceID.String())
				assert.Equal(t, mc.ResourceID, mc.CosmosMetadata.ResourceID)

				assert.Equal(t, "1", mc.GetStampIdentifier(), "stamp identifier should be suffix after last '-' in AKS cluster name")
				assert.Equal(t, fleetapi.ManagementClusterSchedulingPolicySchedulable, mc.Spec.SchedulingPolicy, "active shard should be schedulable")

				// Status
				require.NotNil(t, mc.Status.AKSResourceID)
				assert.Equal(t, "test-westus3-mgmt-1", mc.Status.AKSResourceID.Name)
				require.NotNil(t, mc.Status.PublicDNSZoneResourceID)
				assert.Equal(t, "https://cx-kv.vault.azure.net/", mc.Status.HostedClustersSecretsKeyVaultURL)
				assert.Equal(t, "https://mi-kv.vault.azure.net/", mc.Status.HostedClustersManagedIdentitiesKeyVaultURL)
				assert.Equal(t, "c2bde1aa-d904-48cd-a728-9de33e3ddca9", mc.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID)
				require.NotNil(t, mc.Status.ClusterServiceProvisionShardID)
				assert.Equal(t, resourcesapi.Must(resourcesapi.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")), *mc.Status.ClusterServiceProvisionShardID)

				// Maestro config
				assert.Equal(t, "test-consumer", mc.Status.MaestroConsumerName)
				assert.Equal(t, "http://maestro.maestro.svc.cluster.local:8000", mc.Status.MaestroRESTAPIURL)
				assert.Equal(t, "maestro-grpc.maestro.svc.cluster.local:8090", mc.Status.MaestroGRPCTarget)
			},
		},
		{
			name: "maintenance shard is unschedulable",
			build: func(t *testing.T) *arohcpv1alpha1.ProvisionShard {
				shard, err := validProvisionShardBuilder(t).Status("maintenance").Build()
				require.NoError(t, err)
				return shard
			},
			validate: func(t *testing.T, mc *fleetapi.ManagementCluster) {
				assert.Equal(t, fleetapi.ManagementClusterSchedulingPolicyUnschedulable, mc.Spec.SchedulingPolicy, "maintenance shard should be unschedulable")
				require.Len(t, mc.Status.Conditions, 1)
				assert.Equal(t, string(fleetapi.ManagementClusterConditionReady), mc.Status.Conditions[0].Type)
				assert.Equal(t, metav1.ConditionFalse, mc.Status.Conditions[0].Status)
				assert.Equal(t, string(fleetapi.ManagementClusterConditionReasonProvisionShardMaintenance), mc.Status.Conditions[0].Reason)
				assert.Contains(t, mc.Status.Conditions[0].Message, "maintenance")
			},
		},
		{
			name: "offline shard is unschedulable",
			build: func(t *testing.T) *arohcpv1alpha1.ProvisionShard {
				shard, err := validProvisionShardBuilder(t).Status("offline").Build()
				require.NoError(t, err)
				return shard
			},
			validate: func(t *testing.T, mc *fleetapi.ManagementCluster) {
				assert.Equal(t, fleetapi.ManagementClusterSchedulingPolicyUnschedulable, mc.Spec.SchedulingPolicy, "offline shard should be unschedulable")
				require.Len(t, mc.Status.Conditions, 1)
				assert.Equal(t, string(fleetapi.ManagementClusterConditionReady), mc.Status.Conditions[0].Type)
				assert.Equal(t, metav1.ConditionFalse, mc.Status.Conditions[0].Status)
				assert.Equal(t, string(fleetapi.ManagementClusterConditionReasonProvisionShardOffline), mc.Status.Conditions[0].Reason)
				assert.Contains(t, mc.Status.Conditions[0].Message, "offline")
			},
		},
		{
			name: "unknown shard status produces ConditionUnknown",
			build: func(t *testing.T) *arohcpv1alpha1.ProvisionShard {
				shard, err := validProvisionShardBuilder(t).Status("some-new-status").Build()
				require.NoError(t, err)
				return shard
			},
			validate: func(t *testing.T, mc *fleetapi.ManagementCluster) {
				assert.Equal(t, fleetapi.ManagementClusterSchedulingPolicyUnschedulable, mc.Spec.SchedulingPolicy, "unknown status shard should be unschedulable")
				require.Len(t, mc.Status.Conditions, 1)
				assert.Equal(t, string(fleetapi.ManagementClusterConditionReady), mc.Status.Conditions[0].Type)
				assert.Equal(t, metav1.ConditionUnknown, mc.Status.Conditions[0].Status)
				assert.Equal(t, string(fleetapi.ManagementClusterConditionReasonProvisionShardStatusUnknown), mc.Status.Conditions[0].Reason)
				assert.Contains(t, mc.Status.Conditions[0].Message, "some-new-status")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			shard := tt.build(t)
			mc, err := ConvertCSManagementClusterToInternal(shard)
			if len(tt.expectedErrorSubstr) > 0 {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErrorSubstr)
				assert.Nil(t, mc)
			} else {
				require.NoError(t, err)
				require.NotNil(t, mc)
				if tt.validate != nil {
					tt.validate(t, mc)
				}
			}
		})
	}
}
