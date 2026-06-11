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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ExternalAuthUpdatableConfigHashVersion is the current version of the field
// list used to compute the updatable config hash. Bump this constant and add a
// new entry to externalAuthUpdatableConfigFieldsInVersion whenever the set of hashed fields
// changes (field addition or removal). The minimum version is 1 and versions
// must be consecutive (1, 2, 3, ...).
const ExternalAuthUpdatableConfigHashVersion = 1

// externalAuthUpdatableConfigFieldsInVersion maps each version number to the list of JSON
// field paths included in that version's hash. Every version must have an
// explicit entry, including the current one. Versions must start at 1 and be
// consecutive; gaps are treated as data corruption by the hash function.
// Entries must never be removed; stored resources may reference any past version.
//
// Each path lists the leaf JSON keys to include. Arrays are auto-detected at
// runtime: {"clients", "clientId"} means "the clientId field inside each
// element of the clients array."
//
// externalAuthUpdatableConfig and all its inner types must keep all fields
// forever. When a field is excluded from a new version, it stays in the struct
// (with a Deprecated prefix) so that older version hashes can still be
// reproduced.
var externalAuthUpdatableConfigFieldsInVersion = map[int][][]string{
	1: {
		{"issuer", "url"},
		{"issuer", "audiences"},
		{"issuer", "ca"},
		{"clients", "component", "name"},
		{"clients", "component", "authClientNamespace"},
		{"clients", "clientId"},
		{"clients", "extraScopes"},
		{"clients", "type"},
		{"claim", "mappings", "username", "claim"},
		{"claim", "mappings", "username", "prefix"},
		{"claim", "mappings", "username", "prefixPolicy"},
		{"claim", "mappings", "groups", "claim"},
		{"claim", "mappings", "groups", "prefix"},
		{"claim", "validationRules", "type"},
		{"claim", "validationRules", "requiredClaim", "claim"},
		{"claim", "validationRules", "requiredClaim", "requiredValue"},
	},
}

// externalAuthUpdatableConfig is the canonical representation of external auth properties
// hashed by ExternalAuthUpdatableConfigHash and applied to Cluster Service by
// applyExternalAuthUpdatableConfig (via BuildCSExternalAuth). Add or remove fields here
// and update ExternalAuthUpdatableConfigFromProperties plus applyExternalAuthUpdatableConfig
// in the same change.
//
// The digest is stored on the ServiceProviderExternalAuth as ClusterServiceUpdatableConfigHashForUpdateDispatch and
// compared by the external auth update dispatch controller: a mismatch triggers a CS PATCH and
// hash replacement.
//
// Fields must never be removed from this struct. Older hash versions reference
// them and the dispatch controller needs to reproduce those hashes. Deprecated/Removed
// fields in newer version should still be kept with a Deprecated prefix in the name
// of the variable (without modifying the json struct tag), excluded from the current
// version's field list in externalAuthUpdatableConfigFieldsInVersion, and removed from
// applyExternalAuthUpdatableConfig. To eventually delete a deprecated field,
// first run a one-time migration that re-baselines all resources to the current
// hash version, so no stored version references the old field.
type externalAuthUpdatableConfig struct {
	Issuer  externalAuthUpdatableIssuer          `json:"issuer"`
	Clients []externalAuthUpdatableClientProfile `json:"clients"`
	Claim   externalAuthUpdatableClaim           `json:"claim"`
}

type externalAuthUpdatableIssuer struct {
	URL       string   `json:"url"`
	Audiences []string `json:"audiences"`
	CA        string   `json:"ca"`
}

type externalAuthUpdatableClientProfile struct {
	Component   externalAuthUpdatableClientComponent `json:"component"`
	ClientID    string                               `json:"clientId"`
	ExtraScopes []string                             `json:"extraScopes"`
	Type        api.ExternalAuthClientType           `json:"type"`
}

type externalAuthUpdatableClientComponent struct {
	Name                string `json:"name"`
	AuthClientNamespace string `json:"authClientNamespace"`
}

type externalAuthUpdatableClaim struct {
	Mappings        externalAuthUpdatableClaimMappings    `json:"mappings"`
	ValidationRules []externalAuthUpdatableValidationRule `json:"validationRules"`
}

type externalAuthUpdatableClaimMappings struct {
	Username externalAuthUpdatableUsernameClaim `json:"username"`
	Groups   *externalAuthUpdatableGroupClaim   `json:"groups"`
}

type externalAuthUpdatableUsernameClaim struct {
	Claim        string                        `json:"claim"`
	Prefix       string                        `json:"prefix"`
	PrefixPolicy api.UsernameClaimPrefixPolicy `json:"prefixPolicy"`
}

type externalAuthUpdatableGroupClaim struct {
	Claim  string `json:"claim"`
	Prefix string `json:"prefix"`
}

type externalAuthUpdatableValidationRule struct {
	Type          api.TokenValidationRuleType        `json:"type"`
	RequiredClaim externalAuthUpdatableRequiredClaim `json:"requiredClaim"`
}

type externalAuthUpdatableRequiredClaim struct {
	Claim         string `json:"claim"`
	RequiredValue string `json:"requiredValue"`
}

// ExternalAuthUpdatableConfigFromProperties extracts the canonical updatable external auth
// configuration from internal API properties.
func ExternalAuthUpdatableConfigFromProperties(properties api.HCPOpenShiftClusterExternalAuthProperties) *externalAuthUpdatableConfig {
	config := &externalAuthUpdatableConfig{
		Issuer: externalAuthUpdatableIssuer{
			URL:       properties.Issuer.URL,
			Audiences: properties.Issuer.Audiences,
			CA:        properties.Issuer.CA,
		},
	}

	for _, c := range properties.Clients {
		config.Clients = append(config.Clients, externalAuthUpdatableClientProfile{
			Component: externalAuthUpdatableClientComponent{
				Name:                c.Component.Name,
				AuthClientNamespace: c.Component.AuthClientNamespace,
			},
			ClientID:    c.ClientID,
			ExtraScopes: c.ExtraScopes,
			Type:        c.Type,
		})
	}

	config.Claim.Mappings.Username = externalAuthUpdatableUsernameClaim{
		Claim:        properties.Claim.Mappings.Username.Claim,
		Prefix:       properties.Claim.Mappings.Username.Prefix,
		PrefixPolicy: properties.Claim.Mappings.Username.PrefixPolicy,
	}

	if properties.Claim.Mappings.Groups != nil {
		config.Claim.Mappings.Groups = &externalAuthUpdatableGroupClaim{
			Claim:  properties.Claim.Mappings.Groups.Claim,
			Prefix: properties.Claim.Mappings.Groups.Prefix,
		}
	}

	for _, r := range properties.Claim.ValidationRules {
		config.Claim.ValidationRules = append(config.Claim.ValidationRules, externalAuthUpdatableValidationRule{
			Type: r.Type,
			RequiredClaim: externalAuthUpdatableRequiredClaim{
				Claim:         r.RequiredClaim.Claim,
				RequiredValue: r.RequiredClaim.RequiredValue,
			},
		})
	}

	return config
}

// ExternalAuthUpdatableConfigFromExternalAuth is a convenience wrapper around
// ExternalAuthUpdatableConfigFromProperties that accepts the full document.
func ExternalAuthUpdatableConfigFromExternalAuth(externalAuth *api.HCPOpenShiftClusterExternalAuth) *externalAuthUpdatableConfig {
	return ExternalAuthUpdatableConfigFromProperties(externalAuth.Properties)
}

// ExternalAuthUpdatableConfigHash returns a SHA-256 hex digest of
// externalAuthUpdatableConfig built from the external auth properties marshaled
// as a json map using the current hash version.
func ExternalAuthUpdatableConfigHash(externalAuth *api.HCPOpenShiftClusterExternalAuth) (string, error) {
	return ExternalAuthUpdatableConfigHashForVersion(externalAuth, ExternalAuthUpdatableConfigHashVersion)
}

// ExternalAuthUpdatableConfigHashForVersion returns a SHA-256 hex digest using
// the field list associated with the given version. This allows reproducing
// hashes that were computed with an older (or newer) set of fields. If the
// version is unknown (e.g. after a rollback), it falls back to the current
// version's field list.
func ExternalAuthUpdatableConfigHashForVersion(externalAuth *api.HCPOpenShiftClusterExternalAuth, version int) (string, error) {
	if version <= 0 {
		return "", utils.TrackError(fmt.Errorf("invalid external auth updatable config hash version: %d", version))
	}
	// TODO if version is not found, should we fallback to the current version, or should we error?
	// This is related to concerns about rollbacks
	fields, ok := externalAuthUpdatableConfigFieldsInVersion[version]
	if !ok && version <= ExternalAuthUpdatableConfigHashVersion {
		return "", utils.TrackError(fmt.Errorf("unknown external auth updatable config hash version: %d", version))
	}
	if !ok && version > ExternalAuthUpdatableConfigHashVersion {
		fields = externalAuthUpdatableConfigFieldsInVersion[ExternalAuthUpdatableConfigHashVersion]
	}

	config := ExternalAuthUpdatableConfigFromProperties(externalAuth.Properties)

	raw, err := externalAuthUpdatableConfigJSONForVersion(config, fields)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// externalAuthUpdatableConfigJSONForVersion returns canonical JSON for hashing.
// The struct is marshaled, round-tripped through map[string]any for sorted keys,
// then any fields not in the version's field list are removed.
func externalAuthUpdatableConfigJSONForVersion(config *externalAuthUpdatableConfig, fields [][]string) ([]byte, error) {
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to marshal external auth updatable config: %w", err))
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to unmarshal external auth updatable config: %w", err))
	}

	keepOnlyFields(payload, fields, nil)

	raw, err = json.Marshal(payload)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to marshal external auth updatable config payload: %w", err))
	}
	return raw, nil
}

// keepOnlyFields walks the JSON map and removes any key whose path is not
// covered by the allowlist. A path is covered if any entry in the allowlist is
// a prefix of (or equal to) it. Arrays are auto-detected and each element is
// walked independently.
func keepOnlyFields(obj map[string]any, allowlist [][]string, prefix []string) {
	for key := range obj {
		path := make([]string, len(prefix)+1)
		copy(path, prefix)
		path[len(prefix)] = key
		if !pathCovered(path, allowlist) {
			delete(obj, key)
			continue
		}
		switch v := obj[key].(type) {
		case map[string]any:
			keepOnlyFields(v, allowlist, path)
		case []any:
			stripSliceFieldsNotIn(v, allowlist, path)
		}
	}
}

// stripSliceFieldsNotIn recurses into each element of a JSON array, applying
// keepOnlyFields to map elements and recursing into nested arrays.
func stripSliceFieldsNotIn(arr []any, allowlist [][]string, prefix []string) {
	for _, elem := range arr {
		switch v := elem.(type) {
		case map[string]any:
			keepOnlyFields(v, allowlist, prefix)
		case []any:
			stripSliceFieldsNotIn(v, allowlist, prefix)
		}
	}
}

// pathCovered returns true if any entry in the allowlist covers path. An
// allowlist entry covers a path if the entry is a prefix of (or equal to) the
// path, or if the path is a prefix of the entry (meaning a parent of an
// allowed leaf).
func pathCovered(path []string, allowlist [][]string) bool {
	for _, allowed := range allowlist {
		if isPrefix(allowed, path) || isPrefix(path, allowed) {
			return true
		}
	}
	return false
}

// isPrefix returns true if prefix is a prefix of (or equal to) path.
func isPrefix(prefix, path []string) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i := range prefix {
		if prefix[i] != path[i] {
			return false
		}
	}
	return true
}

func applyExternalAuthUpdatableConfig(externalAuthBuilder *arohcpv1alpha1.ExternalAuthBuilder, config *externalAuthUpdatableConfig) error {
	externalAuthBuilder.Issuer(arohcpv1alpha1.NewTokenIssuer().
		URL(config.Issuer.URL).
		CA(config.Issuer.CA).
		Audiences(config.Issuer.Audiences...),
	)

	clientConfigs := []*arohcpv1alpha1.ExternalAuthClientConfigBuilder{}
	for _, t := range config.Clients {
		clientType, err := convertExternalAuthClientTypeRPToCS(t.Type)
		if err != nil {
			return err
		}

		newClientConfig := arohcpv1alpha1.NewExternalAuthClientConfig().
			ID(t.ClientID).
			Component(arohcpv1alpha1.NewClientComponent().
				Name(t.Component.Name).
				Namespace(t.Component.AuthClientNamespace),
			).
			ExtraScopes(t.ExtraScopes...).
			Type(clientType)
		clientConfigs = append(clientConfigs, newClientConfig)
	}
	externalAuthBuilder.Clients(clientConfigs...)

	usernameClaimPrefixPolicy, err := convertUsernameClaimPrefixPolicyRPToCS(config.Claim.Mappings.Username.PrefixPolicy)
	if err != nil {
		return err
	}

	tokenClaimMappingsBuilder := arohcpv1alpha1.NewTokenClaimMappings().
		UserName(arohcpv1alpha1.NewUsernameClaim().
			Claim(config.Claim.Mappings.Username.Claim).
			Prefix(config.Claim.Mappings.Username.Prefix).
			PrefixPolicy(usernameClaimPrefixPolicy),
		)
	if config.Claim.Mappings.Groups != nil {
		tokenClaimMappingsBuilder = tokenClaimMappingsBuilder.Groups(
			arohcpv1alpha1.NewGroupsClaim().
				Claim(config.Claim.Mappings.Groups.Claim).
				Prefix(config.Claim.Mappings.Groups.Prefix),
		)
	}

	validationRules := []*arohcpv1alpha1.TokenClaimValidationRuleBuilder{}
	for _, t := range config.Claim.ValidationRules {
		newValidationRule := arohcpv1alpha1.NewTokenClaimValidationRule().
			Claim(t.RequiredClaim.Claim).
			RequiredValue(t.RequiredClaim.RequiredValue)
		validationRules = append(validationRules, newValidationRule)
	}

	externalAuthBuilder.
		Claim(arohcpv1alpha1.NewExternalAuthClaim().
			Mappings(tokenClaimMappingsBuilder).
			ValidationRules(validationRules...),
		)

	return nil
}
