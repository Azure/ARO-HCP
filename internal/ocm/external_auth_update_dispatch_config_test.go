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

	actualConfig := externalAuthUpdateDispatchConfigFromCS(csExternalAuth)

	desiredConfig, err := externalAuthUpdateDispatchConfigFromRP(ea)
	require.NoError(t, err)
	desiredHash, err := desiredConfig.hash()
	require.NoError(t, err)
	actualHash, err := actualConfig.hash()
	require.NoError(t, err)
	assert.Equal(t, desiredHash, actualHash)
}

func TestExternalAuthUpdateDispatchConfigFromCSRoundTripNoGroups(t *testing.T) {
	ea := newTestExternalAuth()
	ea.Properties.Claim.Mappings.Groups = nil

	csBuilder, err := BuildCSExternalAuth(context.Background(), ea, true)
	require.NoError(t, err)

	csExternalAuth, err := csBuilder.Build()
	require.NoError(t, err)

	actualConfig := externalAuthUpdateDispatchConfigFromCS(csExternalAuth)

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

	actualConfig := externalAuthUpdateDispatchConfigFromCS(csExternalAuth)

	desiredConfig, err := externalAuthUpdateDispatchConfigFromRP(ea)
	require.NoError(t, err)
	desiredHash, err := desiredConfig.hash()
	require.NoError(t, err)
	actualHash, err := actualConfig.hash()
	require.NoError(t, err)
	assert.Equal(t, desiredHash, actualHash)
}

func TestExternalAuthUpdateDispatchConfigDiffers(t *testing.T) {
	ea := newTestExternalAuth()

	csBuilder, err := BuildCSExternalAuth(context.Background(), ea, true)
	require.NoError(t, err)
	csExternalAuth, err := csBuilder.Build()
	require.NoError(t, err)

	differs, err := ExternalAuthUpdateDispatchConfigDiffers(ea, csExternalAuth)
	require.NoError(t, err)
	assert.False(t, differs)

	ea.Properties.Issuer.URL = "https://changed.example.com"
	differs, err = ExternalAuthUpdateDispatchConfigDiffers(ea, csExternalAuth)
	require.NoError(t, err)
	assert.True(t, differs)
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
	assert.JSONEq(t, desiredJSON, actualJSON)
	assert.Contains(t, desiredJSON, `"url":"https://issuer.example.com"`)
	assert.Contains(t, desiredJSON, `"clientId":"client-id-1"`)
}

func TestExternalAuthUpdateDispatchConfigClientSortOrder(t *testing.T) {
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
	assert.Equal(t, "aaa-client", config.Clients[0].ClientID)
	assert.Equal(t, "zzz-client", config.Clients[1].ClientID)
}

func TestExternalAuthUpdateDispatchConfigPrefixPolicyNone(t *testing.T) {
	ea := newTestExternalAuth()
	ea.Properties.Claim.Mappings.Username.PrefixPolicy = api.UsernameClaimPrefixPolicyNone

	config, err := externalAuthUpdateDispatchConfigFromRP(ea)
	require.NoError(t, err)
	assert.Equal(t, "", config.Claim.Mappings.Username.PrefixPolicy)
}
