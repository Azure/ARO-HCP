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
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
)

// externalAuthUpdateDispatchConfig is a dispatch-specific canonical model of the ExternalAuth's
// Cluster Service fields that are considered by the external auth's cluster service update dispatch
// controller. Its shape intentionally does not mirror RP resources or the Cluster Service API.
// Conversion functions project external state into this form and back out when dispatching to
// Cluster Service.
//
// The same struct is built from either RP desired state (HCPOpenShiftClusterExternalAuth) or from
// the live Cluster Service ExternalAuth. This applies only to ExternalAuth CS updates. Cluster and
// node pool updates use separate dispatch paths and update dispatch config structs. Drift between the
// two projections may trigger the external auth's cluster service update dispatch controller to
// PATCH Cluster Service.
//
// The external auth's cluster service update dispatch controller compares desired and actual
// configs in this canonical form and sends a CS PATCH only when they differ.
//
// Note: This does not include all fields updatable via the ExternalAuth Cluster Service API, only
// the subset that the external auth's cluster service update dispatch controller considers.
//
// Note: Do not embed internal/api struct types (for example api.ExternalAuthClientProfile,
// api.TokenClaimMappingsProfile, or api.TokenClaimValidationRule) in this struct or its nested
// field types. We want to make those internal/api struct types independent of this so they can
// evolve independently. For example, if a field here referenced an internal/api struct type
// directly, any new field added to that struct would be automatically considered as updatable
// automatically, but we might not want that field to be updatable and/or CS side doesn't really
// support updating it. Instead, define curated local structs with only the fields that dispatch
// should hash and sync, and copy values explicitly from api types at the conversion boundaries.
// Using api/internal enum or scalar types for individual curated fields is fine. Some fields are
// stored as Cluster Service string values (for example client Type and username PrefixPolicy) so
// both fromRP() and fromCS() produce identical values without needing CS-to-RP reverse conversion.
//
// IMPORTANT: how to add a new dispatch-managed config field:
//
// Dispatch and operation state are related but wired separately. You need both for a correct
// external auth update experience:
//   - Dispatch (this file): detects drift and PATCHes Cluster Service.
//   - Operation state (operation_external_auth_update_state_calculation.go): decides when the ARM
//     external auth update operation can leave Updating and report Succeeded.
//
// If you wire dispatch but skip operation state calculation, the update may be sent to CS but the
// operation can succeed too early (or stay Updating forever if dispatch is missing).
//
// Before you start:
//
//   - Confirm this is an ExternalAuth-level Cluster Service update (not cluster or node pool).
//   - Confirm Cluster Service supports updating the field on an existing external auth.
//   - Identify where RP desired state lives (HCPOpenShiftClusterExternalAuth).
//
// 1. Dispatch wiring (this file)
//
//   - Add the field to externalAuthUpdateDispatchConfig or a curated nested struct.
//     Do not embed internal/api struct types.
//   - Populate it in externalAuthUpdateDispatchConfigFromRP. This ensures RP projection works correctly
//   - Populate it in externalAuthUpdateDispatchConfigFromCS. This ensures CS projection works correctly
//   - Apply it in applyToCSBuilder. This ensures the CS builders work correctly.
//
// 2. Operation state wiring (backend/pkg/controllers/operationcontrollers/operation_external_auth_update.go)
//
//	determineOperationState aggregates several sources and picks the worst state. Your new
//	check must succeed along with CS spec and Hypershift checks.
//
//	Choose where to observe the change and implement it:
//	  - Prefer observing directly from the Management Cluster side when the field is visible there after propagation.
//	    Most dispatch fields today are checked here: issuer, clients, claim mappings, validation rules, etc.
//	  - Observe from Cluster Service when the Management Cluster side is not a reliable source of
//	    truth yet or simply can't be calculated from there
//	  - Sometimes you might want to observe it on both sides for extra validation
//
//	Return (false, message) with a clear message when observed != desired so Updating state
//	is actionable in logs.
//
// 3. Tests
//
//   - external_auth_update_dispatch_config_test.go: hash, FromCS, round-trip, apply payload.
//   - operation_external_auth_update_test.go: match/mismatch cases for your new helper and, if
//     useful, an end-to-end clusterServiceExternalAuthSpecOperationState or
//     hypershiftExternalAuthOperationState case.
//   - Consider both "not applied yet" (Updating) and "applied" (Succeeded) scenarios.
//
// 4. Sanity checks
//
//   - Create path: applyToCSBuilder is also used by BuildCSExternalAuth in internal/ocm/convert.go
//     when the frontend first creates a Cluster Service external auth (not only when the update
//     dispatch controller PATCHes an existing one). Verify the new field is present on create.
//   - Desired state must exist in Cosmos before dispatch can sync it. If customers set this
//     field via ARM, also wire the full ingest path: ARM API, frontend validation/conversion,
//     and persistence onto HCPOpenShiftClusterExternalAuth. Internal-only fields still need
//     whatever backend path writes the value Cosmos holds.
type externalAuthUpdateDispatchConfig struct {
	Issuer  externalAuthUpdateDispatchConfigIssuer   `json:"issuer"`
	Clients []externalAuthUpdateDispatchConfigClient `json:"clients"`
	Claim   externalAuthUpdateDispatchConfigClaim    `json:"claim"`
}

// externalAuthUpdateDispatchConfigIssuer is the curated issuer subset used for dispatch hash and
// sync. See externalAuthUpdateDispatchConfig: do not embed api.TokenIssuerProfile.
type externalAuthUpdateDispatchConfigIssuer struct {
	URL       string   `json:"url"`
	Audiences []string `json:"audiences"`
	CA        string   `json:"ca"`
}

// externalAuthUpdateDispatchConfigClient is the curated client subset used for dispatch hash and
// sync. See externalAuthUpdateDispatchConfig: do not embed api.ExternalAuthClientProfile.
// Type uses the RP enum in the dispatch canonical form. fromCS() and applyToCSBuilder() convert
// through convertExternalAuthClientTypeCSToRP and convertExternalAuthClientTypeRPToCS because CS
// uses lowercase values ("public", "confidential") while RP uses PascalCase ("Public", "Confidential").
type externalAuthUpdateDispatchConfigClient struct {
	ComponentName      string                     `json:"componentName"`
	ComponentNamespace string                     `json:"componentNamespace"`
	ClientID           string                     `json:"clientId"`
	ExtraScopes        []string                   `json:"extraScopes"`
	Type               api.ExternalAuthClientType `json:"type"`
}

// externalAuthUpdateDispatchConfigClaim is the curated claim subset used for dispatch hash and
// sync. See externalAuthUpdateDispatchConfig: do not embed api.ExternalAuthClaimProfile.
type externalAuthUpdateDispatchConfigClaim struct {
	Mappings        externalAuthUpdateDispatchConfigClaimMappings    `json:"mappings"`
	ValidationRules []externalAuthUpdateDispatchConfigValidationRule `json:"validationRules"`
}

// externalAuthUpdateDispatchConfigClaimMappings is the curated claim mappings subset used for
// dispatch hash and sync. See externalAuthUpdateDispatchConfig: do not embed api.TokenClaimMappingsProfile.
type externalAuthUpdateDispatchConfigClaimMappings struct {
	Username externalAuthUpdateDispatchConfigUsernameClaim `json:"username"`
	Groups   *externalAuthUpdateDispatchConfigGroupsClaim  `json:"groups"`
}

// externalAuthUpdateDispatchConfigUsernameClaim is the curated username claim subset used for
// dispatch hash and sync. See externalAuthUpdateDispatchConfig: do not embed api.UsernameClaimProfile.
type externalAuthUpdateDispatchConfigUsernameClaim struct {
	Claim        string                        `json:"claim"`
	Prefix       string                        `json:"prefix"`
	PrefixPolicy api.UsernameClaimPrefixPolicy `json:"prefixPolicy"`
}

// externalAuthUpdateDispatchConfigGroupsClaim is the curated groups claim subset used for dispatch
// hash and sync. See externalAuthUpdateDispatchConfig: do not embed api.GroupClaimProfile.
type externalAuthUpdateDispatchConfigGroupsClaim struct {
	Claim  string `json:"claim"`
	Prefix string `json:"prefix"`
}

// externalAuthUpdateDispatchConfigValidationRule is the curated validation rule subset used for
// dispatch hash and sync. See externalAuthUpdateDispatchConfig: do not embed api.TokenClaimValidationRule.
type externalAuthUpdateDispatchConfigValidationRule struct {
	Type          api.TokenValidationRuleType                   `json:"type"`
	RequiredClaim externalAuthUpdateDispatchConfigRequiredClaim `json:"requiredClaim"`
}

// externalAuthUpdateDispatchConfigRequiredClaim is the curated required-claim subset used for
// dispatch hash and sync. See externalAuthUpdateDispatchConfig: do not embed api.TokenRequiredClaim.
type externalAuthUpdateDispatchConfigRequiredClaim struct {
	Claim         string `json:"claim"`
	RequiredValue string `json:"requiredValue"`
}

// ExternalAuthUpdateDispatchConfigJSONFromRP returns the canonical JSON of the dispatch config
// projected from RP desired state.
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

// ExternalAuthUpdateDispatchConfigJSONFromCS returns the canonical JSON of the dispatch config
// projected from a Cluster Service external auth.
func ExternalAuthUpdateDispatchConfigJSONFromCS(csExternalAuth *arohcpv1alpha1.ExternalAuth) (string, error) {
	raw, err := externalAuthUpdateDispatchConfigFromCS(csExternalAuth)
	if err != nil {
		return "", err
	}
	canonicalJSON, err := raw.canonicalJSON()
	if err != nil {
		return "", err
	}
	return string(canonicalJSON), nil
}

// externalAuthUpdateDispatchConfigFromRP projects RP desired state into the dispatch canonical form.
func externalAuthUpdateDispatchConfigFromRP(ea *api.HCPOpenShiftClusterExternalAuth) (*externalAuthUpdateDispatchConfig, error) {
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
					PrefixPolicy: ea.Properties.Claim.Mappings.Username.PrefixPolicy,
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

// externalAuthUpdateDispatchConfigClientsFromRP copies clients from RP into the dispatch canonical form.
func externalAuthUpdateDispatchConfigClientsFromRP(clients []api.ExternalAuthClientProfile) ([]externalAuthUpdateDispatchConfigClient, error) {
	if len(clients) == 0 {
		return nil, nil
	}

	out := make([]externalAuthUpdateDispatchConfigClient, 0, len(clients))
	for _, c := range clients {
		out = append(out, externalAuthUpdateDispatchConfigClient{
			ComponentName:      c.Component.Name,
			ComponentNamespace: c.Component.AuthClientNamespace,
			ClientID:           c.ClientID,
			ExtraScopes:        c.ExtraScopes,
			Type:               c.Type,
		})
	}

	return out, nil
}

// externalAuthUpdateDispatchConfigValidationRulesFromRP copies validation rules from RP into the
// dispatch canonical form.
func externalAuthUpdateDispatchConfigValidationRulesFromRP(rules []api.TokenClaimValidationRule) []externalAuthUpdateDispatchConfigValidationRule {
	if len(rules) == 0 {
		return nil
	}

	out := make([]externalAuthUpdateDispatchConfigValidationRule, 0, len(rules))
	for _, r := range rules {
		out = append(out, externalAuthUpdateDispatchConfigValidationRule{
			Type: r.Type,
			RequiredClaim: externalAuthUpdateDispatchConfigRequiredClaim{
				Claim:         r.RequiredClaim.Claim,
				RequiredValue: r.RequiredClaim.RequiredValue,
			},
		})
	}
	return out
}

// externalAuthUpdateDispatchConfigFromCS projects a Cluster Service external auth into the dispatch
// canonical form.
func externalAuthUpdateDispatchConfigFromCS(csEA *arohcpv1alpha1.ExternalAuth) (*externalAuthUpdateDispatchConfig, error) {
	config := &externalAuthUpdateDispatchConfig{}

	if issuer, ok := csEA.GetIssuer(); ok && issuer != nil {
		config.Issuer.URL = issuer.URL()
		if audiences, ok := issuer.GetAudiences(); ok {
			config.Issuer.Audiences = audiences
		}
		config.Issuer.CA = issuer.CA()
	}

	clients, err := externalAuthUpdateDispatchConfigClientsFromCS(csEA)
	if err != nil {
		return nil, err
	}
	config.Clients = clients

	if claim, ok := csEA.GetClaim(); ok && claim != nil {
		if mappings, ok := claim.GetMappings(); ok && mappings != nil {
			if userName, ok := mappings.GetUserName(); ok && userName != nil {
				prefixPolicy, err := convertUsernameClaimPrefixPolicyCSToRP(userName.PrefixPolicy())
				if err != nil {
					return nil, err
				}
				config.Claim.Mappings.Username = externalAuthUpdateDispatchConfigUsernameClaim{
					Claim:        userName.Claim(),
					Prefix:       userName.Prefix(),
					PrefixPolicy: prefixPolicy,
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

	return config, nil
}

// externalAuthUpdateDispatchConfigClientsFromCS extracts clients from a Cluster Service external
// auth into the dispatch canonical form.
func externalAuthUpdateDispatchConfigClientsFromCS(csEA *arohcpv1alpha1.ExternalAuth) ([]externalAuthUpdateDispatchConfigClient, error) {
	csClients, ok := csEA.GetClients()
	if !ok || len(csClients) == 0 {
		return nil, nil
	}

	out := make([]externalAuthUpdateDispatchConfigClient, 0, len(csClients))
	for _, c := range csClients {
		clientType, err := convertExternalAuthClientTypeCSToRP(c.Type())
		if err != nil {
			return nil, err
		}
		client := externalAuthUpdateDispatchConfigClient{
			ClientID: c.ID(),
			Type:     clientType,
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

	return out, nil
}

// externalAuthUpdateDispatchConfigValidationRulesFromCS extracts validation rules from a Cluster
// Service external auth into the dispatch canonical form.
func externalAuthUpdateDispatchConfigValidationRulesFromCS(rules []*arohcpv1alpha1.TokenClaimValidationRule) []externalAuthUpdateDispatchConfigValidationRule {
	if len(rules) == 0 {
		return nil
	}

	out := make([]externalAuthUpdateDispatchConfigValidationRule, 0, len(rules))
	for _, r := range rules {
		out = append(out, externalAuthUpdateDispatchConfigValidationRule{
			Type: api.TokenValidationRuleTypeRequiredClaim,
			RequiredClaim: externalAuthUpdateDispatchConfigRequiredClaim{
				Claim:         r.Claim(),
				RequiredValue: r.RequiredValue(),
			},
		})
	}
	return out
}

// hash returns a SHA-256 hex digest of c's canonical JSON.
func (c *externalAuthUpdateDispatchConfig) hash() (string, error) {
	return hashUpdateDispatchConfig(c)
}

// canonicalJSON returns the deterministic JSON encoding of c used for hashing and comparison.
// Keys are sorted at every object level; see canonicalJSONForUpdateDispatchConfig.
func (c *externalAuthUpdateDispatchConfig) canonicalJSON() ([]byte, error) {
	return canonicalJSONForUpdateDispatchConfig(c)
}

// applyToCSBuilder maps the dispatch config onto a Cluster Service external auth builder.
func (c *externalAuthUpdateDispatchConfig) applyToCSBuilder(builder *arohcpv1alpha1.ExternalAuthBuilder) error {
	builder.Issuer(arohcpv1alpha1.NewTokenIssuer().
		URL(c.Issuer.URL).
		CA(c.Issuer.CA).
		Audiences(c.Issuer.Audiences...))

	clientConfigBuilders := make([]*arohcpv1alpha1.ExternalAuthClientConfigBuilder, 0, len(c.Clients))
	for _, client := range c.Clients {
		clientType, err := convertExternalAuthClientTypeRPToCS(client.Type)
		if err != nil {
			return err
		}
		clientConfigBuilders = append(clientConfigBuilders, arohcpv1alpha1.NewExternalAuthClientConfig().
			ID(client.ClientID).
			Component(arohcpv1alpha1.NewClientComponent().
				Name(client.ComponentName).
				Namespace(client.ComponentNamespace)).
			ExtraScopes(client.ExtraScopes...).
			Type(clientType))
	}
	builder.Clients(clientConfigBuilders...)

	prefixPolicy, err := convertUsernameClaimPrefixPolicyRPToCS(c.Claim.Mappings.Username.PrefixPolicy)
	if err != nil {
		return err
	}
	tokenClaimMappingsBuilder := arohcpv1alpha1.NewTokenClaimMappings().
		UserName(arohcpv1alpha1.NewUsernameClaim().
			Claim(c.Claim.Mappings.Username.Claim).
			Prefix(c.Claim.Mappings.Username.Prefix).
			PrefixPolicy(prefixPolicy))

	if c.Claim.Mappings.Groups != nil {
		tokenClaimMappingsBuilder = tokenClaimMappingsBuilder.Groups(
			arohcpv1alpha1.NewGroupsClaim().
				Claim(c.Claim.Mappings.Groups.Claim).
				Prefix(c.Claim.Mappings.Groups.Prefix))
	}

	validationRuleBuilders := make([]*arohcpv1alpha1.TokenClaimValidationRuleBuilder, 0, len(c.Claim.ValidationRules))
	for _, rule := range c.Claim.ValidationRules {
		ruleBuilder, err := convertTokenClaimValidationRuleRPToCS(rule)
		if err != nil {
			return err
		}
		validationRuleBuilders = append(validationRuleBuilders, ruleBuilder)
	}

	builder.Claim(arohcpv1alpha1.NewExternalAuthClaim().
		Mappings(tokenClaimMappingsBuilder).
		ValidationRules(validationRuleBuilders...))
	return nil
}
