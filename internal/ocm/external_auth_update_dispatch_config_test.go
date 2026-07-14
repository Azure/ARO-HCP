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

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
)

func newTestExternalAuth() *api.HCPOpenShiftClusterExternalAuth {
	return &api.HCPOpenShiftClusterExternalAuth{
		Properties: api.HCPOpenShiftClusterExternalAuthProperties{
			Issuer: api.TokenIssuerProfile{
				URL:       "https://issuer.example.com",
				Audiences: []string{"aud1", "aud2"},
				CA:        "test-ca-cert",
			},
			Clients: []api.ExternalAuthClientProfile{
				{
					Component: api.ExternalAuthClientComponentProfile{
						Name:                "console",
						AuthClientNamespace: "openshift-console",
					},
					ClientID:    "client-id-1",
					ExtraScopes: []string{"email", "profile"},
					Type:        api.ExternalAuthClientTypePublic,
				},
			},
			Claim: api.ExternalAuthClaimProfile{
				Mappings: api.TokenClaimMappingsProfile{
					Username: api.UsernameClaimProfile{
						Claim:        "email",
						PrefixPolicy: api.UsernameClaimPrefixPolicyNoPrefix,
					},
					Groups: &api.GroupClaimProfile{
						Claim:  "groups",
						Prefix: "oidc:",
					},
				},
				ValidationRules: []api.TokenClaimValidationRule{
					{
						Type: api.TokenValidationRuleTypeRequiredClaim,
						RequiredClaim: api.TokenRequiredClaim{
							Claim:         "hd",
							RequiredValue: "example.com",
						},
					},
				},
			},
		},
	}
}

func TestExternalAuthUpdateDispatchConfigHash(t *testing.T) {
	base := newTestExternalAuth()

	config1, err := externalAuthUpdateDispatchConfigFromRP(base)
	require.NoError(t, err)
	hash1, err := config1.hash()
	require.NoError(t, err)
	require.NotEmpty(t, hash1)

	config2, err := externalAuthUpdateDispatchConfigFromRP(base)
	require.NoError(t, err)
	hash2, err := config2.hash()
	require.NoError(t, err)
	assert.Equal(t, hash1, hash2)

	differentIssuer := newTestExternalAuth()
	differentIssuer.Properties.Issuer.URL = "https://other.example.com"
	configDiffIssuer, err := externalAuthUpdateDispatchConfigFromRP(differentIssuer)
	require.NoError(t, err)
	hashDiffIssuer, err := configDiffIssuer.hash()
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hashDiffIssuer)

	differentClient := newTestExternalAuth()
	differentClient.Properties.Clients[0].ClientID = "different-client-id"
	configDiffClient, err := externalAuthUpdateDispatchConfigFromRP(differentClient)
	require.NoError(t, err)
	hashDiffClient, err := configDiffClient.hash()
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hashDiffClient)

	differentClaim := newTestExternalAuth()
	differentClaim.Properties.Claim.Mappings.Username.Claim = "sub"
	configDiffClaim, err := externalAuthUpdateDispatchConfigFromRP(differentClaim)
	require.NoError(t, err)
	hashDiffClaim, err := configDiffClaim.hash()
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hashDiffClaim)

	noGroups := newTestExternalAuth()
	noGroups.Properties.Claim.Mappings.Groups = nil
	configNoGroups, err := externalAuthUpdateDispatchConfigFromRP(noGroups)
	require.NoError(t, err)
	hashNoGroups, err := configNoGroups.hash()
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hashNoGroups)

	differentValidationRule := newTestExternalAuth()
	differentValidationRule.Properties.Claim.ValidationRules[0].RequiredClaim.RequiredValue = "other.example.com"
	configDiffValidationRule, err := externalAuthUpdateDispatchConfigFromRP(differentValidationRule)
	require.NoError(t, err)
	hashDiffValidationRule, err := configDiffValidationRule.hash()
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hashDiffValidationRule)
}

func TestExternalAuthUpdateDispatchConfigHashExcludesNonUpdatableFields(t *testing.T) {
	ea1 := newTestExternalAuth()
	ea1.Properties.ProvisioningState = "Succeeded"
	ea1.Name = "auth1"

	ea2 := newTestExternalAuth()
	ea2.Properties.ProvisioningState = "Updating"
	ea2.Name = "auth2"

	config1, err := externalAuthUpdateDispatchConfigFromRP(ea1)
	require.NoError(t, err)
	hash1, err := config1.hash()
	require.NoError(t, err)

	config2, err := externalAuthUpdateDispatchConfigFromRP(ea2)
	require.NoError(t, err)
	hash2, err := config2.hash()
	require.NoError(t, err)

	assert.Equal(t, hash1, hash2)
}

func TestExternalAuthUpdateDispatchConfigFromCSRoundTrip(t *testing.T) {
	ea := newTestExternalAuth()

	csBuilder, err := BuildCSExternalAuth(context.Background(), ea, true)
	require.NoError(t, err)

	csExternalAuth, err := csBuilder.Build()
	require.NoError(t, err)

	actualConfig, err := externalAuthUpdateDispatchConfigFromCS(csExternalAuth)
	require.NoError(t, err)

	desiredConfig, err := externalAuthUpdateDispatchConfigFromRP(ea)
	require.NoError(t, err)
	desiredHash, err := desiredConfig.hash()
	require.NoError(t, err)
	actualHash, err := actualConfig.hash()
	require.NoError(t, err)
	assert.Equal(t, desiredHash, actualHash)

	require.Len(t, actualConfig.Claim.ValidationRules, 1)
	assert.Equal(t, api.TokenValidationRuleTypeRequiredClaim, actualConfig.Claim.ValidationRules[0].Type)
	assert.Equal(t, "hd", actualConfig.Claim.ValidationRules[0].RequiredClaim.Claim)
	assert.Equal(t, "example.com", actualConfig.Claim.ValidationRules[0].RequiredClaim.RequiredValue)
	require.Len(t, actualConfig.Clients, 1)
	assert.Equal(t, api.ExternalAuthClientTypePublic, actualConfig.Clients[0].Type)
}

func TestExternalAuthUpdateDispatchConfigFromCSRoundTripNoGroups(t *testing.T) {
	ea := newTestExternalAuth()
	ea.Properties.Claim.Mappings.Groups = nil

	csBuilder, err := BuildCSExternalAuth(context.Background(), ea, true)
	require.NoError(t, err)

	csExternalAuth, err := csBuilder.Build()
	require.NoError(t, err)

	actualConfig, err := externalAuthUpdateDispatchConfigFromCS(csExternalAuth)
	require.NoError(t, err)

	desiredConfig, err := externalAuthUpdateDispatchConfigFromRP(ea)
	require.NoError(t, err)
	desiredHash, err := desiredConfig.hash()
	require.NoError(t, err)
	actualHash, err := actualConfig.hash()
	require.NoError(t, err)
	assert.Equal(t, desiredHash, actualHash)
	assert.Nil(t, actualConfig.Claim.Mappings.Groups)
}

func TestExternalAuthUpdateDispatchConfigFromCSRoundTripMultipleClients(t *testing.T) {
	ea := newTestExternalAuth()
	ea.Properties.Clients = append(ea.Properties.Clients, api.ExternalAuthClientProfile{
		Component: api.ExternalAuthClientComponentProfile{
			Name:                "cli",
			AuthClientNamespace: "openshift-cli",
		},
		ClientID:    "aaa-first-alphabetically",
		ExtraScopes: []string{"openid"},
		Type:        api.ExternalAuthClientTypeConfidential,
	})

	csBuilder, err := BuildCSExternalAuth(context.Background(), ea, true)
	require.NoError(t, err)

	csExternalAuth, err := csBuilder.Build()
	require.NoError(t, err)

	actualConfig, err := externalAuthUpdateDispatchConfigFromCS(csExternalAuth)
	require.NoError(t, err)

	desiredConfig, err := externalAuthUpdateDispatchConfigFromRP(ea)
	require.NoError(t, err)
	desiredHash, err := desiredConfig.hash()
	require.NoError(t, err)
	actualHash, err := actualConfig.hash()
	require.NoError(t, err)
	assert.Equal(t, desiredHash, actualHash)
}

func TestExternalAuthUpdateDispatchConfigJSONFromRPAndCS(t *testing.T) {
	ea := newTestExternalAuth()

	csBuilder, err := BuildCSExternalAuth(context.Background(), ea, true)
	require.NoError(t, err)
	csExternalAuth, err := csBuilder.Build()
	require.NoError(t, err)

	desiredJSON, err := ExternalAuthUpdateDispatchConfigJSONFromRP(ea)
	require.NoError(t, err)
	actualJSON, err := ExternalAuthUpdateDispatchConfigJSONFromCS(csExternalAuth)
	require.NoError(t, err)

	// We assert both semantic and byte-for-byte JSON equality on purpose:
	//   - JSONEq checks that RP and CS projections represent the same config (values and structure).
	//   - Equal checks that canonicalJSON produces identical strings on both sides. The external auth
	//     service update dispatch controller uses string equality (==) for drift detection, so
	//     this must hold whenever the configs match; JSONEq alone would not catch encoding
	//     differences such as key ordering or whitespace that would cause a false drift signal.
	assert.JSONEq(t, desiredJSON, actualJSON)
	assert.Equal(t, desiredJSON, actualJSON)
	assert.Contains(t, desiredJSON, `"url": "https://issuer.example.com"`)
	assert.Contains(t, desiredJSON, `"clientId": "client-id-1"`)
	assert.Contains(t, desiredJSON, `"type": "RequiredClaim"`)
	assert.Contains(t, desiredJSON, `"requiredValue": "example.com"`)
	assert.Contains(t, desiredJSON, `"type": "Public"`)

	ea.Properties.Issuer.URL = "https://changed.example.com"
	desiredJSON, err = ExternalAuthUpdateDispatchConfigJSONFromRP(ea)
	require.NoError(t, err)
	assert.NotEqual(t, desiredJSON, actualJSON)
}

func TestExternalAuthUpdateDispatchConfigClientPreservesOrderFromRP(t *testing.T) {
	ea := newTestExternalAuth()
	ea.Properties.Clients = []api.ExternalAuthClientProfile{
		{
			Component:   api.ExternalAuthClientComponentProfile{Name: "z-component", AuthClientNamespace: "ns-z"},
			ClientID:    "zzz-client",
			ExtraScopes: []string{"openid"},
			Type:        api.ExternalAuthClientTypePublic,
		},
		{
			Component:   api.ExternalAuthClientComponentProfile{Name: "a-component", AuthClientNamespace: "ns-a"},
			ClientID:    "aaa-client",
			ExtraScopes: []string{"email"},
			Type:        api.ExternalAuthClientTypePublic,
		},
	}

	config, err := externalAuthUpdateDispatchConfigFromRP(ea)
	require.NoError(t, err)

	require.Len(t, config.Clients, 2)
	assert.Equal(t, "zzz-client", config.Clients[0].ClientID)
	assert.Equal(t, "aaa-client", config.Clients[1].ClientID)
}

func TestExternalAuthUpdateDispatchConfigPrefixPolicyNone(t *testing.T) {
	ea := newTestExternalAuth()
	ea.Properties.Claim.Mappings.Username.PrefixPolicy = api.UsernameClaimPrefixPolicyNone

	config, err := externalAuthUpdateDispatchConfigFromRP(ea)
	require.NoError(t, err)
	assert.Equal(t, api.UsernameClaimPrefixPolicyNone, config.Claim.Mappings.Username.PrefixPolicy)
}

func TestExternalAuthUpdateDispatchConfigValidationRulesFromRP(t *testing.T) {
	ea := newTestExternalAuth()

	config, err := externalAuthUpdateDispatchConfigFromRP(ea)
	require.NoError(t, err)

	require.Len(t, config.Claim.ValidationRules, 1)
	assert.Equal(t, api.TokenValidationRuleTypeRequiredClaim, config.Claim.ValidationRules[0].Type)
	assert.Equal(t, "hd", config.Claim.ValidationRules[0].RequiredClaim.Claim)
	assert.Equal(t, "example.com", config.Claim.ValidationRules[0].RequiredClaim.RequiredValue)
}

func TestExternalAuthUpdateDispatchConfigValidationRulesFromCS(t *testing.T) {
	csExternalAuth, err := arohcpv1alpha1.NewExternalAuth().
		Claim(arohcpv1alpha1.NewExternalAuthClaim().
			ValidationRules(
				arohcpv1alpha1.NewTokenClaimValidationRule().
					Claim("hd").
					RequiredValue("example.com"),
			)).
		Build()
	require.NoError(t, err)

	config, err := externalAuthUpdateDispatchConfigFromCS(csExternalAuth)
	require.NoError(t, err)

	require.Len(t, config.Claim.ValidationRules, 1)
	assert.Equal(t, api.TokenValidationRuleTypeRequiredClaim, config.Claim.ValidationRules[0].Type)
	assert.Equal(t, "hd", config.Claim.ValidationRules[0].RequiredClaim.Claim)
	assert.Equal(t, "example.com", config.Claim.ValidationRules[0].RequiredClaim.RequiredValue)
}

func TestExternalAuthUpdateDispatchConfigClientTypeConversionFromCS(t *testing.T) {
	csExternalAuth, err := arohcpv1alpha1.NewExternalAuth().
		Clients(
			arohcpv1alpha1.NewExternalAuthClientConfig().
				ID("client-id-1").
				Type(arohcpv1alpha1.ExternalAuthClientTypePublic),
			arohcpv1alpha1.NewExternalAuthClientConfig().
				ID("client-id-2").
				Type(arohcpv1alpha1.ExternalAuthClientTypeConfidential),
		).
		Build()
	require.NoError(t, err)

	config, err := externalAuthUpdateDispatchConfigFromCS(csExternalAuth)
	require.NoError(t, err)

	require.Len(t, config.Clients, 2)
	assert.Equal(t, api.ExternalAuthClientTypePublic, config.Clients[0].Type)
	assert.Equal(t, api.ExternalAuthClientTypeConfidential, config.Clients[1].Type)
}

func TestExternalAuthUpdateDispatchConfigFromCSUnknownClientType(t *testing.T) {
	csExternalAuth, err := arohcpv1alpha1.NewExternalAuth().
		Clients(
			arohcpv1alpha1.NewExternalAuthClientConfig().
				ID("client-id-1").
				Type(arohcpv1alpha1.ExternalAuthClientType("unknown")),
		).
		Build()
	require.NoError(t, err)

	_, err = externalAuthUpdateDispatchConfigFromCS(csExternalAuth)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownValue)
}

func TestConvertTokenClaimValidationRuleRPToCS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		rule    externalAuthUpdateDispatchConfigValidationRule
		wantErr bool
	}{
		{
			name: "RequiredClaim maps to CS builder",
			rule: externalAuthUpdateDispatchConfigValidationRule{
				Type: api.TokenValidationRuleTypeRequiredClaim,
				RequiredClaim: externalAuthUpdateDispatchConfigRequiredClaim{
					Claim:         "hd",
					RequiredValue: "example.com",
				},
			},
		},
		{
			name: "unsupported type returns error",
			rule: externalAuthUpdateDispatchConfigValidationRule{
				Type: api.TokenValidationRuleType("Unsupported"),
				RequiredClaim: externalAuthUpdateDispatchConfigRequiredClaim{
					Claim:         "hd",
					RequiredValue: "example.com",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			builder, err := convertTokenClaimValidationRuleRPToCS(tt.rule)
			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrUnknownValue)
				assert.Nil(t, builder)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, builder)

			csRule, err := builder.Build()
			require.NoError(t, err)
			assert.Equal(t, "hd", csRule.Claim())
			assert.Equal(t, "example.com", csRule.RequiredValue())
		})
	}
}

func TestExternalAuthUpdateDispatchConfigApplyToCSBuilder(t *testing.T) {
	tests := []struct {
		name               string
		config             externalAuthUpdateDispatchConfig
		wantCSExternalAuth *arohcpv1alpha1.ExternalAuthBuilder
		wantErr            bool
		errContains        string
	}{
		{
			name: "maps issuer clients claim and validation rules",
			config: externalAuthUpdateDispatchConfig{
				Issuer: externalAuthUpdateDispatchConfigIssuer{
					URL:       "https://issuer.example.com",
					Audiences: []string{"aud1", "aud2"},
					CA:        "test-ca-cert",
				},
				Clients: []externalAuthUpdateDispatchConfigClient{
					{
						ComponentName:      "console",
						ComponentNamespace: "openshift-console",
						ClientID:           "client-id-1",
						ExtraScopes:        []string{"email", "profile"},
						Type:               api.ExternalAuthClientTypePublic,
					},
				},
				Claim: externalAuthUpdateDispatchConfigClaim{
					Mappings: externalAuthUpdateDispatchConfigClaimMappings{
						Username: externalAuthUpdateDispatchConfigUsernameClaim{
							Claim:        "email",
							Prefix:       "",
							PrefixPolicy: api.UsernameClaimPrefixPolicyNoPrefix,
						},
						Groups: &externalAuthUpdateDispatchConfigGroupsClaim{
							Claim:  "groups",
							Prefix: "oidc:",
						},
					},
					ValidationRules: []externalAuthUpdateDispatchConfigValidationRule{
						{
							Type: api.TokenValidationRuleTypeRequiredClaim,
							RequiredClaim: externalAuthUpdateDispatchConfigRequiredClaim{
								Claim:         "hd",
								RequiredValue: "example.com",
							},
						},
					},
				},
			},
			wantCSExternalAuth: arohcpv1alpha1.NewExternalAuth().
				Issuer(arohcpv1alpha1.NewTokenIssuer().
					URL("https://issuer.example.com").
					CA("test-ca-cert").
					Audiences("aud1", "aud2")).
				Clients(
					arohcpv1alpha1.NewExternalAuthClientConfig().
						ID("client-id-1").
						Component(arohcpv1alpha1.NewClientComponent().
							Name("console").
							Namespace("openshift-console")).
						ExtraScopes("email", "profile").
						Type(arohcpv1alpha1.ExternalAuthClientTypePublic),
				).
				Claim(arohcpv1alpha1.NewExternalAuthClaim().
					Mappings(arohcpv1alpha1.NewTokenClaimMappings().
						UserName(arohcpv1alpha1.NewUsernameClaim().
							Claim("email").
							Prefix("").
							PrefixPolicy("NoPrefix")).
						Groups(arohcpv1alpha1.NewGroupsClaim().
							Claim("groups").
							Prefix("oidc:"))).
					ValidationRules(
						arohcpv1alpha1.NewTokenClaimValidationRule().
							Claim("hd").
							RequiredValue("example.com"),
					)),
		},
		{
			name: "omits groups when unset",
			config: externalAuthUpdateDispatchConfig{
				Claim: externalAuthUpdateDispatchConfigClaim{
					Mappings: externalAuthUpdateDispatchConfigClaimMappings{
						Username: externalAuthUpdateDispatchConfigUsernameClaim{
							Claim:        "sub",
							PrefixPolicy: api.UsernameClaimPrefixPolicyNone,
						},
					},
				},
			},
			wantCSExternalAuth: arohcpv1alpha1.NewExternalAuth().
				Issuer(arohcpv1alpha1.NewTokenIssuer().URL("").CA("").Audiences()).
				Clients().
				Claim(arohcpv1alpha1.NewExternalAuthClaim().
					Mappings(arohcpv1alpha1.NewTokenClaimMappings().
						UserName(arohcpv1alpha1.NewUsernameClaim().
							Claim("sub").
							Prefix("").
							PrefixPolicy(""))).
					ValidationRules()),
		},
		{
			name: "converts RP client types to CS lowercase values",
			config: externalAuthUpdateDispatchConfig{
				Clients: []externalAuthUpdateDispatchConfigClient{
					{
						ClientID: "public-client",
						Type:     api.ExternalAuthClientTypePublic,
					},
					{
						ClientID: "confidential-client",
						Type:     api.ExternalAuthClientTypeConfidential,
					},
				},
				Claim: externalAuthUpdateDispatchConfigClaim{
					Mappings: externalAuthUpdateDispatchConfigClaimMappings{
						Username: externalAuthUpdateDispatchConfigUsernameClaim{
							PrefixPolicy: api.UsernameClaimPrefixPolicyNone,
						},
					},
				},
			},
			wantCSExternalAuth: arohcpv1alpha1.NewExternalAuth().
				Issuer(arohcpv1alpha1.NewTokenIssuer().URL("").CA("").Audiences()).
				Clients(
					arohcpv1alpha1.NewExternalAuthClientConfig().
						ID("public-client").
						Component(arohcpv1alpha1.NewClientComponent().Name("").Namespace("")).
						ExtraScopes().
						Type(arohcpv1alpha1.ExternalAuthClientTypePublic),
					arohcpv1alpha1.NewExternalAuthClientConfig().
						ID("confidential-client").
						Component(arohcpv1alpha1.NewClientComponent().Name("").Namespace("")).
						ExtraScopes().
						Type(arohcpv1alpha1.ExternalAuthClientTypeConfidential),
				).
				Claim(arohcpv1alpha1.NewExternalAuthClaim().
					Mappings(arohcpv1alpha1.NewTokenClaimMappings().
						UserName(arohcpv1alpha1.NewUsernameClaim().
							Claim("").
							Prefix("").
							PrefixPolicy(""))).
					ValidationRules()),
		},
		{
			name: "unknown client type returns error",
			config: externalAuthUpdateDispatchConfig{
				Clients: []externalAuthUpdateDispatchConfigClient{
					{
						ClientID: "client-id-1",
						Type:     api.ExternalAuthClientType("Unknown"),
					},
				},
			},
			wantErr:     true,
			errContains: "ExternalAuthClientType",
		},
		{
			name: "unsupported validation rule type returns error",
			config: externalAuthUpdateDispatchConfig{
				Claim: externalAuthUpdateDispatchConfigClaim{
					Mappings: externalAuthUpdateDispatchConfigClaimMappings{
						Username: externalAuthUpdateDispatchConfigUsernameClaim{
							PrefixPolicy: api.UsernameClaimPrefixPolicyNone,
						},
					},
					ValidationRules: []externalAuthUpdateDispatchConfigValidationRule{
						{
							Type: api.TokenValidationRuleType("Unsupported"),
							RequiredClaim: externalAuthUpdateDispatchConfigRequiredClaim{
								Claim:         "hd",
								RequiredValue: "example.com",
							},
						},
					},
				},
			},
			wantErr:     true,
			errContains: "TokenValidationRuleType",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := arohcpv1alpha1.NewExternalAuth()
			err := tt.config.applyToCSBuilder(builder)
			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrUnknownValue)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}
			require.NoError(t, err)

			got, err := builder.Build()
			require.NoError(t, err)

			want, err := tt.wantCSExternalAuth.Build()
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
}

func TestConvertExternalAuthClientTypeRPToCSAndCSToRP(t *testing.T) {
	t.Parallel()

	rpToCS, err := convertExternalAuthClientTypeRPToCS(api.ExternalAuthClientTypePublic)
	require.NoError(t, err)
	assert.Equal(t, arohcpv1alpha1.ExternalAuthClientTypePublic, rpToCS)

	csToRP, err := convertExternalAuthClientTypeCSToRP(arohcpv1alpha1.ExternalAuthClientTypeConfidential)
	require.NoError(t, err)
	assert.Equal(t, api.ExternalAuthClientTypeConfidential, csToRP)

	_, err = convertExternalAuthClientTypeRPToCS(api.ExternalAuthClientType("Unknown"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownValue)

	_, err = convertExternalAuthClientTypeCSToRP(arohcpv1alpha1.ExternalAuthClientType("unknown"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownValue)
}
