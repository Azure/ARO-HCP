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
	"slices"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
)

// externalAuthUpdateDispatchConfig is the set of properties that are updatable by RP Backend's
// external auth cluster service update dispatch controller, against Cluster Service.
// The dispatch controller compares desired and actual configs and sends a CS PATCH
// only when they differ.
//
// This does not necessarily include all the fields that can be updated via the CS API,
// just the ones that are considered during an ARM ExternalAuth update call and processed by
// RP Backend's external auth cluster service update dispatch controller.
//
// Do not embed internal/api struct types in this struct or its nested field types.
// See clusterUpdateDispatchConfig for the full rationale.
type externalAuthUpdateDispatchConfig struct {
	Issuer  externalAuthUpdateDispatchConfigIssuer   `json:"issuer,omitempty"`
	Clients []externalAuthUpdateDispatchConfigClient `json:"clients,omitempty"`
	Claim   externalAuthUpdateDispatchConfigClaim    `json:"claim,omitempty"`
}

type externalAuthUpdateDispatchConfigIssuer struct {
	URL       string   `json:"url,omitempty"`
	Audiences []string `json:"audiences,omitempty"`
	CA        string   `json:"ca,omitempty"`
}

// externalAuthUpdateDispatchConfigClient is the curated client subset hashed and
// applied to CS. Type is stored as the CS string value (not the RP enum) so both
// fromRP() and fromCS() produce identical values without needing CS-to-RP reverse
// conversion.
type externalAuthUpdateDispatchConfigClient struct {
	ComponentName      string   `json:"componentName,omitempty"`
	ComponentNamespace string   `json:"componentNamespace,omitempty"`
	ClientID           string   `json:"clientId,omitempty"`
	ExtraScopes        []string `json:"extraScopes,omitempty"`
	Type               string   `json:"type,omitempty"`
}

type externalAuthUpdateDispatchConfigClaim struct {
	Mappings        externalAuthUpdateDispatchConfigClaimMappings    `json:"mappings,omitempty"`
	ValidationRules []externalAuthUpdateDispatchConfigValidationRule `json:"validationRules,omitempty"`
}

type externalAuthUpdateDispatchConfigClaimMappings struct {
	Username externalAuthUpdateDispatchConfigUsernameClaim `json:"username,omitempty"`
	Groups   *externalAuthUpdateDispatchConfigGroupsClaim  `json:"groups,omitempty"`
}

// externalAuthUpdateDispatchConfigUsernameClaim stores PrefixPolicy as the CS
// string value so both fromRP() and fromCS() produce identical values.
type externalAuthUpdateDispatchConfigUsernameClaim struct {
	Claim        string `json:"claim,omitempty"`
	Prefix       string `json:"prefix,omitempty"`
	PrefixPolicy string `json:"prefixPolicy,omitempty"`
}

type externalAuthUpdateDispatchConfigGroupsClaim struct {
	Claim  string `json:"claim,omitempty"`
	Prefix string `json:"prefix,omitempty"`
}

type externalAuthUpdateDispatchConfigValidationRule struct {
	Claim         string `json:"claim,omitempty"`
	RequiredValue string `json:"requiredValue,omitempty"`
}

// ExternalAuthUpdateDispatchConfigDiffers reports whether the dispatch-managed configuration
// derived from the RP external auth differs from the live Cluster Service external auth. The
// comparison uses a SHA-256 hash of each side's canonical JSON representation.
func ExternalAuthUpdateDispatchConfigDiffers(externalAuth *api.HCPOpenShiftClusterExternalAuth, csExternalAuth *arohcpv1alpha1.ExternalAuth) (bool, error) {
	desiredConfig, err := externalAuthUpdateDispatchConfigFromRP(externalAuth)
	if err != nil {
		return false, err
	}
	desiredHash, err := desiredConfig.hash()
	if err != nil {
		return false, err
	}

	actualConfig := externalAuthUpdateDispatchConfigFromCS(csExternalAuth)
	actualHash, err := actualConfig.hash()
	if err != nil {
		return false, err
	}

	return desiredHash != actualHash, nil
}

// ExternalAuthUpdateDispatchConfigJSONFromRP returns the canonical JSON representation of the
// dispatch-managed configuration derived from the RP external auth.
func ExternalAuthUpdateDispatchConfigJSONFromRP(externalAuth *api.HCPOpenShiftClusterExternalAuth) (string, error) {
	config, err := externalAuthUpdateDispatchConfigFromRP(externalAuth)
	if err != nil {
		return "", err
	}
	raw, err := config.canonicalJSON()
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// ExternalAuthUpdateDispatchConfigJSONFromCS returns the canonical JSON representation of the
// dispatch-managed configuration derived from a Cluster Service external auth.
func ExternalAuthUpdateDispatchConfigJSONFromCS(csExternalAuth *arohcpv1alpha1.ExternalAuth) (string, error) {
	raw, err := externalAuthUpdateDispatchConfigFromCS(csExternalAuth).canonicalJSON()
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func externalAuthUpdateDispatchConfigFromRP(ea *api.HCPOpenShiftClusterExternalAuth) (*externalAuthUpdateDispatchConfig, error) {
	prefixPolicy, err := convertUsernameClaimPrefixPolicyRPToCS(ea.Properties.Claim.Mappings.Username.PrefixPolicy)
	if err != nil {
		return nil, err
	}

	clients, err := externalAuthUpdateDispatchConfigClientsFromRP(ea.Properties.Clients)
	if err != nil {
		return nil, err
	}

	config := &externalAuthUpdateDispatchConfig{
		Issuer: externalAuthUpdateDispatchConfigIssuer{
			URL:       ea.Properties.Issuer.URL,
			Audiences: ea.Properties.Issuer.Audiences,
			CA:        ea.Properties.Issuer.CA,
		},
		Clients: clients,
		Claim: externalAuthUpdateDispatchConfigClaim{
			Mappings: externalAuthUpdateDispatchConfigClaimMappings{
				Username: externalAuthUpdateDispatchConfigUsernameClaim{
					Claim:        ea.Properties.Claim.Mappings.Username.Claim,
					Prefix:       ea.Properties.Claim.Mappings.Username.Prefix,
					PrefixPolicy: prefixPolicy,
				},
			},
			ValidationRules: externalAuthUpdateDispatchConfigValidationRulesFromRP(ea.Properties.Claim.ValidationRules),
		},
	}

	if ea.Properties.Claim.Mappings.Groups != nil {
		config.Claim.Mappings.Groups = &externalAuthUpdateDispatchConfigGroupsClaim{
			Claim:  ea.Properties.Claim.Mappings.Groups.Claim,
			Prefix: ea.Properties.Claim.Mappings.Groups.Prefix,
		}
	}

	return config, nil
}

func externalAuthUpdateDispatchConfigClientsFromRP(clients []api.ExternalAuthClientProfile) ([]externalAuthUpdateDispatchConfigClient, error) {
	if len(clients) == 0 {
		return nil, nil
	}

	out := make([]externalAuthUpdateDispatchConfigClient, 0, len(clients))
	for _, c := range clients {
		clientType, err := convertExternalAuthClientTypeRPToCS(c.Type)
		if err != nil {
			return nil, err
		}
		out = append(out, externalAuthUpdateDispatchConfigClient{
			ComponentName:      c.Component.Name,
			ComponentNamespace: c.Component.AuthClientNamespace,
			ClientID:           c.ClientID,
			ExtraScopes:        c.ExtraScopes,
			Type:               string(clientType),
		})
	}

	slices.SortFunc(out, func(a, b externalAuthUpdateDispatchConfigClient) int {
		if a.ClientID < b.ClientID {
			return -1
		}
		if a.ClientID > b.ClientID {
			return 1
		}
		return 0
	})

	return out, nil
}

func externalAuthUpdateDispatchConfigValidationRulesFromRP(rules []api.TokenClaimValidationRule) []externalAuthUpdateDispatchConfigValidationRule {
	if len(rules) == 0 {
		return nil
	}

	out := make([]externalAuthUpdateDispatchConfigValidationRule, 0, len(rules))
	for _, r := range rules {
		out = append(out, externalAuthUpdateDispatchConfigValidationRule{
			Claim:         r.RequiredClaim.Claim,
			RequiredValue: r.RequiredClaim.RequiredValue,
		})
	}
	return out
}

func externalAuthUpdateDispatchConfigFromCS(csEA *arohcpv1alpha1.ExternalAuth) *externalAuthUpdateDispatchConfig {
	config := &externalAuthUpdateDispatchConfig{}

	if issuer, ok := csEA.GetIssuer(); ok && issuer != nil {
		config.Issuer.URL = issuer.URL()
		if audiences, ok := issuer.GetAudiences(); ok {
			config.Issuer.Audiences = audiences
		}
		config.Issuer.CA = issuer.CA()
	}

	config.Clients = externalAuthUpdateDispatchConfigClientsFromCS(csEA)

	if claim, ok := csEA.GetClaim(); ok && claim != nil {
		if mappings, ok := claim.GetMappings(); ok && mappings != nil {
			if userName, ok := mappings.GetUserName(); ok && userName != nil {
				config.Claim.Mappings.Username = externalAuthUpdateDispatchConfigUsernameClaim{
					Claim:        userName.Claim(),
					Prefix:       userName.Prefix(),
					PrefixPolicy: userName.PrefixPolicy(),
				}
			}

			if groups, ok := mappings.GetGroups(); ok && groups != nil && !groups.Empty() {
				config.Claim.Mappings.Groups = &externalAuthUpdateDispatchConfigGroupsClaim{
					Claim:  groups.Claim(),
					Prefix: groups.Prefix(),
				}
			}
		}

		if validationRules, ok := claim.GetValidationRules(); ok {
			config.Claim.ValidationRules = externalAuthUpdateDispatchConfigValidationRulesFromCS(validationRules)
		}
	}

	return config
}

func externalAuthUpdateDispatchConfigClientsFromCS(csEA *arohcpv1alpha1.ExternalAuth) []externalAuthUpdateDispatchConfigClient {
	csClients, ok := csEA.GetClients()
	if !ok || len(csClients) == 0 {
		return nil
	}

	out := make([]externalAuthUpdateDispatchConfigClient, 0, len(csClients))
	for _, c := range csClients {
		client := externalAuthUpdateDispatchConfigClient{
			ClientID: c.ID(),
			Type:     string(c.Type()),
		}

		if component, ok := c.GetComponent(); ok && component != nil {
			client.ComponentName = component.Name()
			client.ComponentNamespace = component.Namespace()
		}

		if extraScopes, ok := c.GetExtraScopes(); ok {
			client.ExtraScopes = extraScopes
		}

		out = append(out, client)
	}

	slices.SortFunc(out, func(a, b externalAuthUpdateDispatchConfigClient) int {
		if a.ClientID < b.ClientID {
			return -1
		}
		if a.ClientID > b.ClientID {
			return 1
		}
		return 0
	})

	return out
}

func externalAuthUpdateDispatchConfigValidationRulesFromCS(rules []*arohcpv1alpha1.TokenClaimValidationRule) []externalAuthUpdateDispatchConfigValidationRule {
	if len(rules) == 0 {
		return nil
	}

	out := make([]externalAuthUpdateDispatchConfigValidationRule, 0, len(rules))
	for _, r := range rules {
		out = append(out, externalAuthUpdateDispatchConfigValidationRule{
			Claim:         r.Claim(),
			RequiredValue: r.RequiredValue(),
		})
	}
	return out
}

func (c *externalAuthUpdateDispatchConfig) hash() (string, error) {
	return hashUpdateDispatchConfig(c)
}

func (c *externalAuthUpdateDispatchConfig) canonicalJSON() ([]byte, error) {
	return canonicalJSONForUpdateDispatchConfig(c)
}

func (c *externalAuthUpdateDispatchConfig) applyToCSBuilder(builder *arohcpv1alpha1.ExternalAuthBuilder) {
	builder.Issuer(arohcpv1alpha1.NewTokenIssuer().
		URL(c.Issuer.URL).
		CA(c.Issuer.CA).
		Audiences(c.Issuer.Audiences...))

	clientBuilders := make([]*arohcpv1alpha1.ExternalAuthClientConfigBuilder, 0, len(c.Clients))
	for _, client := range c.Clients {
		clientBuilders = append(clientBuilders, arohcpv1alpha1.NewExternalAuthClientConfig().
			ID(client.ClientID).
			Component(arohcpv1alpha1.NewClientComponent().
				Name(client.ComponentName).
				Namespace(client.ComponentNamespace)).
			ExtraScopes(client.ExtraScopes...).
			Type(arohcpv1alpha1.ExternalAuthClientType(client.Type)))
	}
	builder.Clients(clientBuilders...)

	tokenClaimMappingsBuilder := arohcpv1alpha1.NewTokenClaimMappings().
		UserName(arohcpv1alpha1.NewUsernameClaim().
			Claim(c.Claim.Mappings.Username.Claim).
			Prefix(c.Claim.Mappings.Username.Prefix).
			PrefixPolicy(c.Claim.Mappings.Username.PrefixPolicy))

	if c.Claim.Mappings.Groups != nil {
		tokenClaimMappingsBuilder = tokenClaimMappingsBuilder.Groups(
			arohcpv1alpha1.NewGroupsClaim().
				Claim(c.Claim.Mappings.Groups.Claim).
				Prefix(c.Claim.Mappings.Groups.Prefix))
	}

	validationRuleBuilders := make([]*arohcpv1alpha1.TokenClaimValidationRuleBuilder, 0, len(c.Claim.ValidationRules))
	for _, rule := range c.Claim.ValidationRules {
		validationRuleBuilders = append(validationRuleBuilders, arohcpv1alpha1.NewTokenClaimValidationRule().
			Claim(rule.Claim).
			RequiredValue(rule.RequiredValue))
	}

	builder.Claim(arohcpv1alpha1.NewExternalAuthClaim().
		Mappings(tokenClaimMappingsBuilder).
		ValidationRules(validationRuleBuilders...))
}
