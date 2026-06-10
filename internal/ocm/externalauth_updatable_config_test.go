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

	"github.com/Azure/ARO-HCP/internal/api"
)

func TestExternalAuthUpdatableConfigHash(t *testing.T) {
	base := &api.HCPOpenShiftClusterExternalAuth{
		Properties: api.HCPOpenShiftClusterExternalAuthProperties{
			Issuer: api.TokenIssuerProfile{
				URL:       "https://issuer.example.com",
				Audiences: []string{"aud1"},
				CA:        "ca-cert-data",
			},
			Clients: []api.ExternalAuthClientProfile{
				{
					Component: api.ExternalAuthClientComponentProfile{
						Name:                "my-component",
						AuthClientNamespace: "kube-system",
					},
					ClientID:    "client-id-1",
					ExtraScopes: []string{"email"},
				},
			},
			Claim: api.ExternalAuthClaimProfile{
				Mappings: api.TokenClaimMappingsProfile{
					Username: api.UsernameClaimProfile{
						Claim:        "email",
						Prefix:       "oidc:",
						PrefixPolicy: api.UsernameClaimPrefixPolicyPrefix,
					},
				},
			},
		},
	}

	hash1, err := ExternalAuthUpdatableConfigHash(base)
	require.NoError(t, err)
	require.NotEmpty(t, hash1)

	hash2, err := ExternalAuthUpdatableConfigHash(base)
	require.NoError(t, err)
	assert.Equal(t, hash1, hash2, "same input must produce stable hash")

	modified := &api.HCPOpenShiftClusterExternalAuth{
		Properties: api.HCPOpenShiftClusterExternalAuthProperties{
			Issuer: api.TokenIssuerProfile{
				URL:       "https://other-issuer.example.com",
				Audiences: []string{"aud1"},
				CA:        "ca-cert-data",
			},
			Clients: base.Properties.Clients,
			Claim:   base.Properties.Claim,
		},
	}

	hash3, err := ExternalAuthUpdatableConfigHash(modified)
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hash3, "different issuer URL must produce different hash")

	withGroups := &api.HCPOpenShiftClusterExternalAuth{
		Properties: api.HCPOpenShiftClusterExternalAuthProperties{
			Issuer:  base.Properties.Issuer,
			Clients: base.Properties.Clients,
			Claim: api.ExternalAuthClaimProfile{
				Mappings: api.TokenClaimMappingsProfile{
					Username: base.Properties.Claim.Mappings.Username,
					Groups: &api.GroupClaimProfile{
						Claim:  "groups",
						Prefix: "oidc:",
					},
				},
			},
		},
	}

	hash4, err := ExternalAuthUpdatableConfigHash(withGroups)
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hash4, "adding groups claim must change hash")
}

func TestExternalAuthUpdatableConfigJSONForHashIsCanonical(t *testing.T) {
	config := ExternalAuthUpdatableConfigFromProperties(api.HCPOpenShiftClusterExternalAuthProperties{
		Issuer: api.TokenIssuerProfile{
			URL:       "https://issuer.example.com",
			Audiences: []string{"aud1"},
			CA:        "ca-data",
		},
		Clients: []api.ExternalAuthClientProfile{
			{
				Component: api.ExternalAuthClientComponentProfile{
					Name:                "comp",
					AuthClientNamespace: "ns",
				},
				ClientID: "cid",
			},
		},
		Claim: api.ExternalAuthClaimProfile{
			Mappings: api.TokenClaimMappingsProfile{
				Username: api.UsernameClaimProfile{
					Claim:        "email",
					PrefixPolicy: api.UsernameClaimPrefixPolicyNone,
				},
			},
		},
	})

	raw, err := externalAuthUpdatableConfigJSONForHash(config)
	require.NoError(t, err)

	keys, err := topLevelExternalAuthJSONKeys(raw)
	require.NoError(t, err)
	assert.True(t, slices.IsSorted(keys), "top-level JSON keys must be sorted: %v", keys)
	assert.Equal(t, []string{"claim", "clients", "issuer"}, keys)
}

func topLevelExternalAuthJSONKeys(raw []byte) ([]string, error) {
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
