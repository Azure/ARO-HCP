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
// Changing this struct has deploy-time effects:
//   - Removing a field changes the digest for every external auth that had that field marshalled.
//   - Adding a field changes the digest for every external auth that would start marshalling the field.
//   - Renaming a json tag changes the digest for every external auth that had the field marshalled.
//
// In all of those cases, a change of digest implies a CS PATCH and hash replacement.
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
// externalAuthUpdatableConfig built from the external auth properties marshaled as a json map.
func ExternalAuthUpdatableConfigHash(externalAuth *api.HCPOpenShiftClusterExternalAuth) (string, error) {
	config := ExternalAuthUpdatableConfigFromProperties(externalAuth.Properties)

	raw, err := externalAuthUpdatableConfigJSONForHash(config)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// externalAuthUpdatableConfigJSONForHash returns canonical JSON for hashing. The struct
// is marshaled first so json tags and omitempty apply, then round-tripped through
// map[string]any so object keys are emitted in sorted order at every level.
func externalAuthUpdatableConfigJSONForHash(config *externalAuthUpdatableConfig) ([]byte, error) {
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to marshal external auth updatable config: %w", err))
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to unmarshal external auth updatable config: %w", err))
	}

	raw, err = json.Marshal(payload)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to marshal external auth updatable config payload: %w", err))
	}
	return raw, nil
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
