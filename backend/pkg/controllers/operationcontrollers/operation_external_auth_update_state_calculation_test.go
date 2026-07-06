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

package operationcontrollers

import (
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	internallistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestHypershiftHostedClusterExternalAuthOperationState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		externalAuth      *api.HCPOpenShiftClusterExternalAuth
		readDesires       []*kubeapplier.ReadDesire
		wantState         arm.ProvisioningState
		wantMessageSubstr string
	}{
		{
			name:              "no ReadDesire returns Updating",
			externalAuth:      newExternalAuthUpdateTestExternalAuth(),
			readDesires:       nil,
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "Hypershift HostedCluster has not been observed yet",
		},
		{
			name:         "matching external auth returns Succeeded",
			externalAuth: newExternalAuthUpdateTestExternalAuth(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: testExternalAuthUpdateMatchingHostedClusterSpec(),
				}),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name:         "oidc provider name match is case insensitive",
			externalAuth: newExternalAuthUpdateTestExternalAuth(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: func() v1beta1.HostedClusterSpec {
						spec := testExternalAuthUpdateMatchingHostedClusterSpec()
						provider := testExternalAuthUpdateMatchingOIDCProvider()
						provider.Name = strings.ToUpper(testExternalAuthName)
						spec.Configuration.Authentication.OIDCProviders = []configv1.OIDCProvider{provider}
						return spec
					}(),
				}),
			},
			wantState: arm.ProvisioningStateSucceeded,
		},
		{
			name:         "missing OIDC providers returns Updating",
			externalAuth: newExternalAuthUpdateTestExternalAuth(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: v1beta1.HostedClusterSpec{
						Configuration: &v1beta1.ClusterConfiguration{
							Authentication: &configv1.AuthenticationSpec{},
						},
					},
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "Hypershift HostedCluster has no OIDCProviders configured",
		},
		{
			name:         "OIDC provider not found returns Updating",
			externalAuth: newExternalAuthUpdateTestExternalAuth(),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: func() v1beta1.HostedClusterSpec {
						spec := testExternalAuthUpdateMatchingHostedClusterSpec()
						provider := testExternalAuthUpdateMatchingOIDCProvider()
						provider.Name = "other-provider"
						spec.Configuration.Authentication.OIDCProviders = []configv1.OIDCProvider{provider}
						return spec
					}(),
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: `OIDCProvider "test-external-auth" not found`,
		},
		{
			name: "issuer URL mismatch returns Updating",
			externalAuth: newExternalAuthUpdateTestExternalAuth(func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.Properties.Issuer.URL = "https://changed.example.com"
			}),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: testExternalAuthUpdateMatchingHostedClusterSpec(),
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: `issuer URL is "https://issuer.example.com", want "https://changed.example.com"`,
		},
		{
			name: "client count mismatch returns Updating",
			externalAuth: newExternalAuthUpdateTestExternalAuth(func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.Properties.Clients = append(ea.Properties.Clients, api.ExternalAuthClientProfile{
					Component: api.ExternalAuthClientComponentProfile{
						Name:                "oauth",
						AuthClientNamespace: "openshift-authentication",
					},
					ClientID: "client-id-2",
				})
			}),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: testExternalAuthUpdateMatchingHostedClusterSpec(),
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "has 1 clients, want 2",
		},
		{
			name: "username claim mismatch returns Updating",
			externalAuth: newExternalAuthUpdateTestExternalAuth(func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.Properties.Claim.Mappings.Username.Claim = "sub"
			}),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: testExternalAuthUpdateMatchingHostedClusterSpec(),
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: `username claim is "email", want "sub"`,
		},
		{
			name: "validation rule mismatch returns Updating",
			externalAuth: newExternalAuthUpdateTestExternalAuth(func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.Properties.Claim.ValidationRules[0].RequiredClaim.RequiredValue = "other.example.com"
			}),
			readDesires: []*kubeapplier.ReadDesire{
				newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
					Spec: testExternalAuthUpdateMatchingHostedClusterSpec(),
				}),
			},
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: `requiredValue is "example.com", want "other.example.com"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))

			controller := &operationExternalAuthUpdate{
				readDesireLister: &internallistertesting.SliceReadDesireLister{
					Desires: tt.readDesires,
				},
			}

			state, err := controller.hypershiftHostedClusterExternalAuthOperationState(ctx, tt.externalAuth)
			require.NoError(t, err)
			assert.Equal(t, tt.wantState, state.ProvisioningState)
			if tt.wantMessageSubstr != "" {
				assert.Contains(t, state.Message, tt.wantMessageSubstr)
			}
		})
	}
}

func TestHypershiftHostedClusterExternalAuthIssuerSpecMatchesDesired(t *testing.T) {
	t.Parallel()

	controller := &operationExternalAuthUpdate{}
	matchingProvider := testExternalAuthUpdateMatchingOIDCProvider()

	tests := []struct {
		name       string
		desired    api.TokenIssuerProfile
		observed   configv1.OIDCProvider
		wantMatch  bool
		wantSubstr string
	}{
		{
			name: "matching issuer",
			desired: api.TokenIssuerProfile{
				URL:       "https://issuer.example.com",
				Audiences: []string{"aud1", "aud2"},
			},
			observed:  matchingProvider,
			wantMatch: true,
		},
		{
			name: "URL mismatch",
			desired: api.TokenIssuerProfile{
				URL: "https://other.example.com",
			},
			observed:   matchingProvider,
			wantMatch:  false,
			wantSubstr: `issuer URL is "https://issuer.example.com", want "https://other.example.com"`,
		},
		{
			name: "audiences mismatch",
			desired: api.TokenIssuerProfile{
				URL:       "https://issuer.example.com",
				Audiences: []string{"aud1"},
			},
			observed:   matchingProvider,
			wantMatch:  false,
			wantSubstr: `issuer audiences is [aud1 aud2], want [aud1]`,
		},
		{
			name:      "both empty audiences match",
			desired:   api.TokenIssuerProfile{},
			observed:  configv1.OIDCProvider{},
			wantMatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			matches, msg := controller.hypershiftHostedClusterExternalAuthIssuerSpecMatchesDesired(tt.desired, tt.observed)
			assert.Equal(t, tt.wantMatch, matches)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}

func TestHypershiftHostedClusterExternalAuthClientsSpecMatchesDesired(t *testing.T) {
	t.Parallel()

	controller := &operationExternalAuthUpdate{}
	matchingClients := testExternalAuthUpdateMatchingOIDCProvider().OIDCClients

	tests := []struct {
		name       string
		desired    []api.ExternalAuthClientProfile
		observed   []configv1.OIDCClientConfig
		wantMatch  bool
		wantSubstr string
	}{
		{
			name: "matching clients",
			desired: []api.ExternalAuthClientProfile{
				{
					Component: api.ExternalAuthClientComponentProfile{
						Name:                "console",
						AuthClientNamespace: "openshift-console",
					},
					ClientID:    "client-id-1",
					ExtraScopes: []string{"email", "profile"},
				},
			},
			observed:  matchingClients,
			wantMatch: true,
		},
		{
			name: "client order mismatch",
			desired: []api.ExternalAuthClientProfile{
				{
					Component: api.ExternalAuthClientComponentProfile{
						Name:                "oauth",
						AuthClientNamespace: "openshift-authentication",
					},
					ClientID: "client-id-2",
				},
				{
					Component: api.ExternalAuthClientComponentProfile{
						Name:                "console",
						AuthClientNamespace: "openshift-console",
					},
					ClientID:    "client-id-1",
					ExtraScopes: []string{"email", "profile"},
				},
			},
			observed: []configv1.OIDCClientConfig{
				matchingClients[0],
				{
					ComponentName:      "oauth",
					ComponentNamespace: "openshift-authentication",
					ClientID:           "client-id-2",
				},
			},
			wantMatch:  false,
			wantSubstr: `client[0] clientID is "client-id-1", want "client-id-2"`,
		},
		{
			name:       "count mismatch",
			desired:    []api.ExternalAuthClientProfile{{ClientID: "a"}},
			observed:   nil,
			wantMatch:  false,
			wantSubstr: "has 0 clients, want 1",
		},
		{
			name: "clientID mismatch",
			desired: []api.ExternalAuthClientProfile{
				{ClientID: "want-this"},
			},
			observed: []configv1.OIDCClientConfig{
				{ClientID: "got-that"},
			},
			wantMatch:  false,
			wantSubstr: `clientID is "got-that", want "want-this"`,
		},
		{
			name: "componentName mismatch",
			desired: []api.ExternalAuthClientProfile{
				{
					Component: api.ExternalAuthClientComponentProfile{Name: "console"},
					ClientID:  "client-id-1",
				},
			},
			observed: []configv1.OIDCClientConfig{
				{
					ComponentName: "oauth",
					ClientID:      "client-id-1",
				},
			},
			wantMatch:  false,
			wantSubstr: `componentName is "oauth", want "console"`,
		},
		{
			name: "componentNamespace mismatch",
			desired: []api.ExternalAuthClientProfile{
				{
					Component: api.ExternalAuthClientComponentProfile{
						Name:                "console",
						AuthClientNamespace: "openshift-console",
					},
					ClientID: "client-id-1",
				},
			},
			observed: []configv1.OIDCClientConfig{
				{
					ComponentName:      "console",
					ComponentNamespace: "other-namespace",
					ClientID:           "client-id-1",
				},
			},
			wantMatch:  false,
			wantSubstr: `componentNamespace is "other-namespace", want "openshift-console"`,
		},
		{
			name: "extraScopes mismatch",
			desired: []api.ExternalAuthClientProfile{
				{
					ClientID:    "client-id-1",
					ExtraScopes: []string{"email"},
				},
			},
			observed: []configv1.OIDCClientConfig{
				{
					ClientID:    "client-id-1",
					ExtraScopes: []string{"profile"},
				},
			},
			wantMatch:  false,
			wantSubstr: `extraScopes is [profile], want [email]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			matches, msg := controller.hypershiftHostedClusterExternalAuthClientsSpecMatchesDesired(tt.desired, tt.observed)
			assert.Equal(t, tt.wantMatch, matches)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}

func TestHypershiftHostedClusterExternalAuthClaimMappingsSpecMatchesDesired(t *testing.T) {
	t.Parallel()

	controller := &operationExternalAuthUpdate{}

	newMatchingClaim := func() api.ExternalAuthClaimProfile {
		return newExternalAuthUpdateTestExternalAuth().Properties.Claim
	}
	newMatchingProvider := func() configv1.OIDCProvider {
		return testExternalAuthUpdateMatchingOIDCProvider()
	}

	tests := []struct {
		name       string
		desired    api.ExternalAuthClaimProfile
		observed   configv1.OIDCProvider
		wantMatch  bool
		wantSubstr string
	}{
		{
			name:      "matching claim mappings",
			desired:   newMatchingClaim(),
			observed:  newMatchingProvider(),
			wantMatch: true,
		},
		{
			name: "username claim mismatch",
			desired: func() api.ExternalAuthClaimProfile {
				claim := newMatchingClaim()
				claim.Mappings.Username.Claim = "sub"
				return claim
			}(),
			observed:   newMatchingProvider(),
			wantMatch:  false,
			wantSubstr: `username claim is "email", want "sub"`,
		},
		{
			name: "username prefixPolicy mismatch",
			desired: func() api.ExternalAuthClaimProfile {
				claim := newMatchingClaim()
				claim.Mappings.Username.PrefixPolicy = api.UsernameClaimPrefixPolicyNone
				return claim
			}(),
			observed:   newMatchingProvider(),
			wantMatch:  false,
			wantSubstr: `username prefixPolicy is "NoPrefix", want ""`,
		},
		{
			name: "username prefix mismatch when policy is Prefix",
			desired: func() api.ExternalAuthClaimProfile {
				claim := newMatchingClaim()
				claim.Mappings.Username.PrefixPolicy = api.UsernameClaimPrefixPolicyPrefix
				claim.Mappings.Username.Prefix = "oidc:"
				return claim
			}(),
			observed: func() configv1.OIDCProvider {
				provider := newMatchingProvider()
				provider.ClaimMappings.Username.PrefixPolicy = configv1.Prefix
				provider.ClaimMappings.Username.Prefix = &configv1.UsernamePrefix{PrefixString: "other:"}
				return provider
			}(),
			wantMatch:  false,
			wantSubstr: `username prefix is "other:", want "oidc:"`,
		},
		{
			name: "username prefix policy Prefix matches",
			desired: func() api.ExternalAuthClaimProfile {
				claim := newMatchingClaim()
				claim.Mappings.Username.PrefixPolicy = api.UsernameClaimPrefixPolicyPrefix
				claim.Mappings.Username.Prefix = "oidc:"
				return claim
			}(),
			observed: func() configv1.OIDCProvider {
				provider := newMatchingProvider()
				provider.ClaimMappings.Username.PrefixPolicy = configv1.Prefix
				provider.ClaimMappings.Username.Prefix = &configv1.UsernamePrefix{PrefixString: "oidc:"}
				return provider
			}(),
			wantMatch: true,
		},
		{
			name: "groups claim mismatch",
			desired: func() api.ExternalAuthClaimProfile {
				claim := newMatchingClaim()
				claim.Mappings.Groups.Claim = "custom-groups"
				return claim
			}(),
			observed:   newMatchingProvider(),
			wantMatch:  false,
			wantSubstr: `groups claim is "groups", want "custom-groups"`,
		},
		{
			name: "groups prefix mismatch",
			desired: func() api.ExternalAuthClaimProfile {
				claim := newMatchingClaim()
				claim.Mappings.Groups.Prefix = "custom:"
				return claim
			}(),
			observed:   newMatchingProvider(),
			wantMatch:  false,
			wantSubstr: `groups prefix is "oidc:", want "custom:"`,
		},
		{
			name: "nil groups desired matches when observed has no groups",
			desired: func() api.ExternalAuthClaimProfile {
				claim := newMatchingClaim()
				claim.Mappings.Groups = nil
				return claim
			}(),
			observed: func() configv1.OIDCProvider {
				provider := newMatchingProvider()
				provider.ClaimMappings.Groups = configv1.PrefixedClaimMapping{}
				return provider
			}(),
			wantMatch: true,
		},
		{
			name: "nil groups desired rejects observed groups",
			desired: func() api.ExternalAuthClaimProfile {
				claim := newMatchingClaim()
				claim.Mappings.Groups = nil
				return claim
			}(),
			observed:   newMatchingProvider(),
			wantMatch:  false,
			wantSubstr: `has groups claim "groups" prefix "oidc:", want no groups`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			matches, msg := controller.hypershiftHostedClusterExternalAuthClaimMappingsSpecMatchesDesired(tt.desired, tt.observed)
			assert.Equal(t, tt.wantMatch, matches)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}

func TestHypershiftHostedClusterExternalAuthValidationRulesSpecMatchesDesired(t *testing.T) {
	t.Parallel()

	controller := &operationExternalAuthUpdate{}
	matchingRules := testExternalAuthUpdateMatchingOIDCProvider().ClaimValidationRules

	tests := []struct {
		name       string
		desired    []api.TokenClaimValidationRule
		observed   []configv1.TokenClaimValidationRule
		wantMatch  bool
		wantSubstr string
	}{
		{
			name: "matching required claim rule",
			desired: []api.TokenClaimValidationRule{
				{
					Type: api.TokenValidationRuleTypeRequiredClaim,
					RequiredClaim: api.TokenRequiredClaim{
						Claim:         "hd",
						RequiredValue: "example.com",
					},
				},
			},
			observed:  matchingRules,
			wantMatch: true,
		},
		{
			name:       "count mismatch",
			desired:    []api.TokenClaimValidationRule{{Type: api.TokenValidationRuleTypeRequiredClaim}},
			observed:   nil,
			wantMatch:  false,
			wantSubstr: "has 0 validation rules, want 1",
		},
		{
			name: "rule type mismatch",
			desired: []api.TokenClaimValidationRule{
				{Type: api.TokenValidationRuleTypeRequiredClaim},
			},
			observed: []configv1.TokenClaimValidationRule{
				{Type: configv1.TokenValidationRuleTypeCEL},
			},
			wantMatch:  false,
			wantSubstr: `validation rule[0] type is "CEL", want "RequiredClaim"`,
		},
		{
			name: "nil requiredClaim errors when token validation rule type is RequiredClaim",
			desired: []api.TokenClaimValidationRule{
				{Type: api.TokenValidationRuleTypeRequiredClaim},
			},
			observed: []configv1.TokenClaimValidationRule{
				{Type: configv1.TokenValidationRuleTypeRequiredClaim},
			},
			wantMatch:  false,
			wantSubstr: "validation rule[0] has nil requiredClaim but expects a requiredClaim",
		},
		{
			name: "claim mismatch",
			desired: []api.TokenClaimValidationRule{
				{
					Type: api.TokenValidationRuleTypeRequiredClaim,
					RequiredClaim: api.TokenRequiredClaim{
						Claim:         "iss",
						RequiredValue: "example.com",
					},
				},
			},
			observed:   matchingRules,
			wantMatch:  false,
			wantSubstr: `validation rule[0] claim is "hd", want "iss"`,
		},
		{
			name: "requiredValue mismatch",
			desired: []api.TokenClaimValidationRule{
				{
					Type: api.TokenValidationRuleTypeRequiredClaim,
					RequiredClaim: api.TokenRequiredClaim{
						Claim:         "hd",
						RequiredValue: "other.example.com",
					},
				},
			},
			observed:   matchingRules,
			wantMatch:  false,
			wantSubstr: `validation rule[0] requiredValue is "example.com", want "other.example.com"`,
		},
		{
			name: "unsupported desired type",
			desired: []api.TokenClaimValidationRule{
				{Type: api.TokenValidationRuleType("Unsupported")},
			},
			observed: []configv1.TokenClaimValidationRule{
				{Type: configv1.TokenValidationRuleTypeRequiredClaim},
			},
			wantMatch:  false,
			wantSubstr: `validation rule[0] has unsupported desired type "Unsupported"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			matches, msg := controller.hypershiftHostedClusterExternalAuthValidationRulesSpecMatchesDesired(tt.desired, tt.observed)
			assert.Equal(t, tt.wantMatch, matches)
			if tt.wantSubstr != "" {
				assert.Contains(t, msg, tt.wantSubstr)
			}
		})
	}
}

func TestClusterServiceExternalAuthSpecOperationState(t *testing.T) {
	t.Parallel()

	newCSExternalAuthFromRP := func(t *testing.T, externalAuth *api.HCPOpenShiftClusterExternalAuth) *arohcpv1alpha1.ExternalAuth {
		t.Helper()
		builder, err := ocm.BuildCSExternalAuth(context.Background(), externalAuth, true)
		require.NoError(t, err)
		csExternalAuth, err := builder.Build()
		require.NoError(t, err)
		return csExternalAuth
	}

	controller := &operationExternalAuthUpdate{}

	tests := []struct {
		name              string
		externalAuth      *api.HCPOpenShiftClusterExternalAuth
		csExternalAuth    *arohcpv1alpha1.ExternalAuth
		wantState         arm.ProvisioningState
		wantMessageSubstr string
	}{
		{
			name:           "matching external auth returns Succeeded",
			externalAuth:   newExternalAuthUpdateTestExternalAuth(),
			csExternalAuth: newCSExternalAuthFromRP(t, newExternalAuthUpdateTestExternalAuth()),
			wantState:      arm.ProvisioningStateSucceeded,
		},
		{
			name: "issuer URL mismatch returns Updating",
			externalAuth: newExternalAuthUpdateTestExternalAuth(func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.Properties.Issuer.URL = "https://changed.example.com"
			}),
			csExternalAuth:    newCSExternalAuthFromRP(t, newExternalAuthUpdateTestExternalAuth()),
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "Cluster Service external auth spec does not match desired",
		},
		{
			name:           "empty default external auth matches empty CS external auth",
			externalAuth:   newExternalAuthTestFixture().newExternalAuth(),
			csExternalAuth: mustBuildEmptyCSExternalAuth(t),
			wantState:      arm.ProvisioningStateSucceeded,
		},
		{
			name: "validation rule mismatch returns Updating",
			externalAuth: newExternalAuthUpdateTestExternalAuth(func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.Properties.Claim.ValidationRules[0].RequiredClaim.RequiredValue = "changed.example.com"
			}),
			csExternalAuth:    newCSExternalAuthFromRP(t, newExternalAuthUpdateTestExternalAuth()),
			wantState:         arm.ProvisioningStateUpdating,
			wantMessageSubstr: "changed.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			state, err := controller.clusterServiceExternalAuthSpecOperationState(tt.externalAuth, tt.csExternalAuth)
			require.NoError(t, err)
			assert.Equal(t, tt.wantState, state.ProvisioningState)
			if tt.wantMessageSubstr != "" {
				assert.Contains(t, state.Message, tt.wantMessageSubstr)
			}
		})
	}
}

func mustBuildEmptyCSExternalAuth(t *testing.T) *arohcpv1alpha1.ExternalAuth {
	t.Helper()
	csExternalAuth, err := arohcpv1alpha1.NewExternalAuth().Build()
	require.NoError(t, err)
	return csExternalAuth
}
