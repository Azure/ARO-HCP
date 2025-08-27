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

package frontend

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"dario.cat/mergo"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestRequestIDPropagator(t *testing.T) {
	const testRequestID = "00000000-0000-0000-0000-000000000000"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Header.Get(clusterServiceRequestIDHeader)))
	}))
	defer ts.Close()

	do := func(c *http.Client) string {
		t.Helper()

		ctx := context.Background()
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
		require.NoError(t, err)
		correlationData := arm.NewCorrelationData(r)
		correlationData.RequestID = uuid.MustParse(testRequestID)
		r = r.WithContext(ContextWithCorrelationData(ctx, correlationData))

		rs, err := c.Do(r)
		require.NoError(t, err)

		require.Equal(t, http.StatusOK, rs.StatusCode)

		b, err := io.ReadAll(rs.Body)
		require.NoError(t, err)

		return string(b)
	}

	// Without the transport wrapper, the request ID isn't echoed.
	c := ts.Client()
	assert.Empty(t, do(c))

	// With the transport wrapper, the request ID is echoed.
	c.Transport = RequestIDPropagator(c.Transport)
	assert.Equal(t, testRequestID, do(c))
}

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
				Properties: api.HCPOpenShiftClusterProperties{
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
				Properties: api.HCPOpenShiftClusterProperties{
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
				Properties: api.HCPOpenShiftClusterProperties{
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
				Properties: api.HCPOpenShiftClusterProperties{
					ClusterImageRegistry: api.ClusterImageRegistryProfile{
						State: api.ClusterImageRegistryProfileStateDisabled,
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			csCluster := ocmCluster(t, ocmClusterDefaults(), tc.ocmClusterTweaks)
			expectHcpCluster := api.ClusterTestCase(t, tc.hcpClusterTweaks)

			// FIXME Temporary hack until we pass cluster autoscaling values to CS.
			expectHcpCluster.Properties.Autoscaling.MaxPodGracePeriodSeconds = 0
			expectHcpCluster.Properties.Autoscaling.MaxNodeProvisionTimeSeconds = 0
			expectHcpCluster.Properties.Autoscaling.PodPriorityThreshold = 0

			actualHcpCluster, err := ConvertCStoHCPOpenShiftCluster(resourceID, csCluster)
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
			want:       ocmCluster(t, ocmClusterDefaults()),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, arohcpv1alpha1.MarshalCluster(tc.want, &buf))
			want := buf.String()
			builder, err := withImmutableAttributes(
				ocmClusterDefaults(),
				api.ClusterTestCase(t, tc.hcpCluster),
				api.TestSubscriptionID,
				api.TestResourceGroupName,
				api.TestLocation,
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

func ocmClusterDefaults() *arohcpv1alpha1.ClusterBuilder {
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
			ResourceGroupName(api.TestResourceGroupName).
			ResourceName(api.TestClusterName).
			SubnetResourceID(api.TestSubnetResourceID).
			SubscriptionID(api.TestSubscriptionID).
			TenantID(api.TestTenantID),
		).
		CCS(arohcpv1alpha1.NewCCS().Enabled(true)).
		CloudProvider(cmv1.NewCloudProvider().
			ID("azure")).
		Flavour(cmv1.NewFlavour().
			ID("osd-4")).
		Hypershift(arohcpv1alpha1.NewHypershift().
			Enabled(true)).
		Name(api.TestClusterName).
		Network(arohcpv1alpha1.NewNetwork().
			HostPrefix(23).
			MachineCIDR("10.0.0.0/16").
			PodCIDR("10.128.0.0/14").
			ServiceCIDR("172.30.0.0/16").
			Type("OVNKubernetes")).
		Product(cmv1.NewProduct().
			ID("aro")).
		Region(cmv1.NewCloudRegion().
			ID(api.TestLocation)).
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
			OSDiskSizeGibibytes(0).
			OSDiskStorageAccountType(""),
		).
		Subnet("").
		Version(arohcpv1alpha1.NewVersion().
			ID("openshift-v").
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
	}
	for _, tc := range testCases {
		f := NewTestFrontend(t)
		t.Run(tc.name, func(t *testing.T) {
			ctx := ContextWithLogger(context.Background(), api.NewTestLogger())
			expected, err := tc.expectedCSNodePool.Build()
			require.NoError(t, err)
			generatedCSNodePool, _ := f.BuildCSNodePool(ctx, tc.hcpNodePool, false)
			assert.Equalf(t, expected, generatedCSNodePool, "BuildCSNodePool(%v, %v)", resourceID, expected)
		})
	}
}

func externalAuthResource(opts ...func(*api.HCPOpenShiftClusterExternalAuth)) *api.HCPOpenShiftClusterExternalAuth {
	externalAuth := api.NewDefaultHCPOpenShiftClusterExternalAuth()

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
			),
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
			name: "correctly parse PrefixPolicyType",
			hcpExternalAuth: externalAuthResource(
				func(hsc *api.HCPOpenShiftClusterExternalAuth) {
					hsc.Properties.Claim.Mappings.Username.PrefixPolicy = api.UsernameClaimPrefixPolicyTypePrefix
				},
			),
			expectedCSExternalAuth: getBaseCSExternalAuthBuilder().Claim(arohcpv1alpha1.NewExternalAuthClaim().
				Mappings(arohcpv1alpha1.NewTokenClaimMappings().
					UserName(arohcpv1alpha1.NewUsernameClaim().
						Claim("").
						Prefix("").
						PrefixPolicy(string(api.UsernameClaimPrefixPolicyTypePrefix)),
					),
				)),
		},
		{
			name: "correctly parse Issuer",
			hcpExternalAuth: externalAuthResource(
				func(hsc *api.HCPOpenShiftClusterExternalAuth) {
					hsc.Properties.Issuer = api.TokenIssuerProfile{
						Ca:        dummyCA,
						Url:       dummyURL,
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
								TokenClaimValidationRuleType: api.TokenValidationRuleTypeRequiredClaim,
								RequiredClaim: api.TokenRequiredClaim{
									Claim:         "A",
									RequiredValue: "B",
								},
							},
							{
								TokenClaimValidationRuleType: api.TokenValidationRuleTypeRequiredClaim,
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
						{ClientId: "a"},
						{ClientId: "b"},
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
						Type(""),
					arohcpv1alpha1.NewExternalAuthClientConfig().
						ID("b").
						Component(arohcpv1alpha1.NewClientComponent().
							Name("").
							Namespace(""),
						).
						ExtraScopes().
						Type(""),
				}...),
		},
	}
	for _, tc := range testCases {
		f := NewTestFrontend(t)
		t.Run(tc.name, func(t *testing.T) {
			ctx := ContextWithLogger(context.Background(), api.NewTestLogger())
			expected, err := tc.expectedCSExternalAuth.Build()
			require.NoError(t, err)
			generatedCSExternalAuth, err := f.BuildCSExternalAuth(ctx, tc.hcpExternalAuth, false)
			require.NoError(t, err)
			assert.Equalf(t, expected, generatedCSExternalAuth, "BuildCSExternalAuth(%v, %v)", resourceID, expected)
		})
	}
}
