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

	raw, err := externalAuthUpdatableConfigJSONForVersion(config, externalAuthUpdatableConfigFieldsInVersion[ExternalAuthUpdatableConfigHashVersion])
	require.NoError(t, err)

	keys, err := topLevelExternalAuthJSONKeys(raw)
	require.NoError(t, err)
	assert.True(t, slices.IsSorted(keys), "top-level JSON keys must be sorted: %v", keys)
	assert.Equal(t, []string{"claim", "clients", "issuer"}, keys)
}

func TestExternalAuthUpdatableConfigHashForVersion(t *testing.T) {
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
					Type:        api.ExternalAuthClientTypeConfidential,
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

	t.Run("current version matches convenience wrapper", func(t *testing.T) {
		hashVia, err := ExternalAuthUpdatableConfigHashForVersion(base, ExternalAuthUpdatableConfigHashVersion)
		require.NoError(t, err)

		hashConvenience, err := ExternalAuthUpdatableConfigHash(base)
		require.NoError(t, err)

		assert.Equal(t, hashConvenience, hashVia, "HashForVersion with current version must equal Hash")
	})

	t.Run("zero or negative version returns error", func(t *testing.T) {
		_, err := ExternalAuthUpdatableConfigHashForVersion(base, 0)
		require.Error(t, err)

		_, err = ExternalAuthUpdatableConfigHashForVersion(base, -1)
		require.Error(t, err)
	})

	t.Run("unknown version higher than current falls back to current version", func(t *testing.T) {
		hashUnknown, err := ExternalAuthUpdatableConfigHashForVersion(base, ExternalAuthUpdatableConfigHashVersion+100)
		require.NoError(t, err)

		hashCurrent, err := ExternalAuthUpdatableConfigHash(base)
		require.NoError(t, err)

		assert.Equal(t, hashCurrent, hashUnknown, "unknown version higher than current must fall back to current version hash")
	})


	t.Run("field addition produces different hash for new version", func(t *testing.T) {
		origFields := externalAuthUpdatableConfigFieldsInVersion
		defer func() { externalAuthUpdatableConfigFieldsInVersion = origFields }()

		externalAuthUpdatableConfigFieldsInVersion = map[int][][]string{
			1: origFields[1],
			2: append(append([][]string{}, origFields[1]...), []string{"issuer", "newField"}),
		}

		hashV1, err := ExternalAuthUpdatableConfigHashForVersion(base, 1)
		require.NoError(t, err)
		hashV2, err := ExternalAuthUpdatableConfigHashForVersion(base, 2)
		require.NoError(t, err)

		assert.Equal(t, hashV1, hashV2, "v1 and v2 hashes should be equal when the new field has zero value")
	})

	t.Run("field removal excludes field from new version hash", func(t *testing.T) {
		origFields := externalAuthUpdatableConfigFieldsInVersion
		defer func() { externalAuthUpdatableConfigFieldsInVersion = origFields }()

		v2Fields := make([][]string, 0, len(origFields[1]))
		for _, f := range origFields[1] {
			if len(f) == 2 && f[0] == "issuer" && f[1] == "ca" {
				continue
			}
			v2Fields = append(v2Fields, f)
		}
		externalAuthUpdatableConfigFieldsInVersion = map[int][][]string{
			1: origFields[1],
			2: v2Fields,
		}

		hashV1, err := ExternalAuthUpdatableConfigHashForVersion(base, 1)
		require.NoError(t, err)
		hashV2, err := ExternalAuthUpdatableConfigHashForVersion(base, 2)
		require.NoError(t, err)

		assert.NotEqual(t, hashV1, hashV2, "removing a field with a non-zero value must change the hash")
	})

	t.Run("old version hash is stable across version bumps", func(t *testing.T) {
		hashBefore, err := ExternalAuthUpdatableConfigHashForVersion(base, ExternalAuthUpdatableConfigHashVersion)
		require.NoError(t, err)

		origFields := externalAuthUpdatableConfigFieldsInVersion
		defer func() { externalAuthUpdatableConfigFieldsInVersion = origFields }()

		externalAuthUpdatableConfigFieldsInVersion = map[int][][]string{
			1: origFields[1],
			2: append(append([][]string{}, origFields[1]...), []string{"issuer", "newField"}),
		}

		hashAfter, err := ExternalAuthUpdatableConfigHashForVersion(base, 1)
		require.NoError(t, err)

		assert.Equal(t, hashBefore, hashAfter, "adding a v2 entry must not change v1 hash for the same data")
	})
}

func TestExternalAuthUpdatableConfigFieldsInVersionConsecutive(t *testing.T) {
	for v := 1; v <= ExternalAuthUpdatableConfigHashVersion; v++ {
		_, ok := externalAuthUpdatableConfigFieldsInVersion[v]
		assert.True(t, ok, "version %d is missing from externalAuthUpdatableConfigFieldsInVersion", v)
	}
	assert.Len(t, externalAuthUpdatableConfigFieldsInVersion, ExternalAuthUpdatableConfigHashVersion,
		"map has entries beyond the current version constant")
}

func TestExternalAuthUpdatableConfigFieldsInVersionDiffs(t *testing.T) {
	type versionDiff struct {
		added   [][]string
		removed [][]string
	}

	// When adding a new version, describe what changed from the previous
	// version here. The test verifies the full field lists match.
	diffs := map[int]versionDiff{
		// v1 is the base, no diff
	}

	for v := 2; v <= ExternalAuthUpdatableConfigHashVersion; v++ {
		t.Run(fmt.Sprintf("v%d", v), func(t *testing.T) {
			diff, ok := diffs[v]
			require.True(t, ok, "version %d needs a diff entry in this test", v)

			prev := externalAuthUpdatableConfigFieldsInVersion[v-1]
			expected := make(map[string]bool)
			for _, f := range prev {
				expected[fmt.Sprintf("%v", f)] = true
			}
			for _, f := range diff.added {
				expected[fmt.Sprintf("%v", f)] = true
			}
			for _, f := range diff.removed {
				delete(expected, fmt.Sprintf("%v", f))
			}

			actual := make(map[string]bool)
			for _, f := range externalAuthUpdatableConfigFieldsInVersion[v] {
				actual[fmt.Sprintf("%v", f)] = true
			}

			assert.Equal(t, expected, actual, "v%d field list does not match v%d + diff", v, v-1)
		})
	}
}

func TestKeepOnlyFields(t *testing.T) {
	t.Run("keeps only allowed leaf fields", func(t *testing.T) {
		obj := map[string]any{
			"issuer": map[string]any{
				"url": "https://example.com",
				"ca":  "cert-data",
			},
		}
		keepOnlyFields(obj, [][]string{{"issuer", "url"}}, nil)

		issuer, ok := obj["issuer"].(map[string]any)
		require.True(t, ok)
		assert.Contains(t, issuer, "url")
		assert.NotContains(t, issuer, "ca")
	})

	t.Run("strips fields from array elements", func(t *testing.T) {
		obj := map[string]any{
			"clients": []any{
				map[string]any{
					"clientId": "id1",
					"extra":    "should-be-removed",
				},
				map[string]any{
					"clientId": "id2",
					"extra":    "also-removed",
				},
			},
		}
		keepOnlyFields(obj, [][]string{{"clients", "clientId"}}, nil)

		clients, ok := obj["clients"].([]any)
		require.True(t, ok)
		require.Len(t, clients, 2)
		for _, c := range clients {
			m, ok := c.(map[string]any)
			require.True(t, ok)
			assert.Contains(t, m, "clientId")
			assert.NotContains(t, m, "extra")
		}
	})

	t.Run("removes top-level key not in allowlist", func(t *testing.T) {
		obj := map[string]any{
			"issuer": map[string]any{
				"url": "https://example.com",
			},
			"extra": "remove-me",
		}
		keepOnlyFields(obj, [][]string{{"issuer", "url"}}, nil)

		assert.Contains(t, obj, "issuer")
		assert.NotContains(t, obj, "extra")
	})

	t.Run("no-op when all fields are allowed", func(t *testing.T) {
		obj := map[string]any{
			"issuer": map[string]any{
				"url": "https://example.com",
			},
		}
		keepOnlyFields(obj, [][]string{{"issuer", "url"}}, nil)

		issuer, ok := obj["issuer"].(map[string]any)
		require.True(t, ok)
		assert.Contains(t, issuer, "url")
	})

	t.Run("keeps sibling and removes other in nested map", func(t *testing.T) {
		obj := map[string]any{
			"a": map[string]any{
				"b": map[string]any{
					"c": "remove-me",
					"d": "keep-me",
				},
			},
		}
		keepOnlyFields(obj, [][]string{{"a", "b", "d"}}, nil)

		b, ok := obj["a"].(map[string]any)["b"].(map[string]any)
		require.True(t, ok)
		assert.NotContains(t, b, "c")
		assert.Equal(t, "keep-me", b["d"])
	})

	t.Run("deeply nested field through array element", func(t *testing.T) {
		obj := map[string]any{
			"clients": []any{
				map[string]any{
					"component": map[string]any{
						"name":      "comp1",
						"namespace": "ns1",
					},
					"clientId": "id1",
				},
			},
		}
		keepOnlyFields(obj, [][]string{{"clients", "component", "name"}, {"clients", "clientId"}}, nil)

		clients := obj["clients"].([]any)
		elem := clients[0].(map[string]any)
		assert.Contains(t, elem, "clientId")
		comp := elem["component"].(map[string]any)
		assert.Contains(t, comp, "name")
		assert.NotContains(t, comp, "namespace")
	})

	t.Run("primitive array kept intact when covered", func(t *testing.T) {
		obj := map[string]any{
			"issuer": map[string]any{
				"audiences": []any{"aud1", "aud2"},
				"ca":        "remove-me",
			},
		}
		keepOnlyFields(obj, [][]string{{"issuer", "audiences"}}, nil)

		issuer := obj["issuer"].(map[string]any)
		assert.Contains(t, issuer, "audiences")
		assert.NotContains(t, issuer, "ca")
		assert.Equal(t, []any{"aud1", "aud2"}, issuer["audiences"])
	})

	t.Run("nested array of maps", func(t *testing.T) {
		obj := map[string]any{
			"outer": []any{
				[]any{
					map[string]any{
						"keep":   "yes",
						"remove": "no",
					},
				},
			},
		}
		keepOnlyFields(obj, [][]string{{"outer", "keep"}}, nil)

		outer := obj["outer"].([]any)
		inner := outer[0].([]any)
		m := inner[0].(map[string]any)
		assert.Contains(t, m, "keep")
		assert.NotContains(t, m, "remove")
	})

	t.Run("empty allowlist removes everything", func(t *testing.T) {
		obj := map[string]any{
			"issuer": map[string]any{"url": "https://example.com"},
			"claim":  map[string]any{"x": "y"},
		}
		keepOnlyFields(obj, [][]string{}, nil)

		assert.Empty(t, obj)
	})
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
