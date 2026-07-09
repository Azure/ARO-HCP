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
	"fmt"
	"slices"
	"strings"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// External auth update operation state calculation for the external auth update operation controller.

// hypershiftHostedClusterExternalAuthOperationState contains the external auth update operation state calculation
// comparing desired state against Hypershift's HostedCluster in the management cluster.
func (c *operationExternalAuthUpdate) hypershiftHostedClusterExternalAuthOperationState(ctx context.Context, externalAuth *api.HCPOpenShiftClusterExternalAuth) (*operationState, error) {
	hostedCluster, err := maestrohelpers.GetCachedHostedClusterForCluster(
		ctx,
		c.readDesireLister,
		externalAuth.ID.SubscriptionID,
		externalAuth.ID.ResourceGroupName,
		externalAuth.ID.Parent.Name,
	)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if hostedCluster == nil {
		return newOperationState(arm.ProvisioningStateUpdating, "Hypershift HostedCluster has not been observed yet"), nil
	}

	if matches, message := c.hypershiftHostedClusterExternalAuthSpecMatchesDesired(externalAuth, hostedCluster); !matches {
		return newOperationState(arm.ProvisioningStateUpdating, message), nil
	}

	// TODO compare with Hypershift HostedCluster relevant parts of status.configuration.authentication when possible.
	// At the moment of writing this (2026-06-29) the status.configuration.authentication is only available on
	// HostedClusters >= 4.21.

	return newOperationState(arm.ProvisioningStateSucceeded, ""), nil
}

// hypershiftHostedClusterExternalAuthSpecMatchesDesired reports whether Hypershift HostedCluster .Spec fields
// and other non status configuration matches desired external auth state. Returns false and a diagnostic message
// when any leaf check fails. HostedCluster .status is not checked here.
func (c *operationExternalAuthUpdate) hypershiftHostedClusterExternalAuthSpecMatchesDesired(externalAuth *api.HCPOpenShiftClusterExternalAuth, hostedCluster *v1beta1.HostedCluster) (bool, string) {
	if hostedCluster.Spec.Configuration == nil ||
		hostedCluster.Spec.Configuration.Authentication == nil ||
		len(hostedCluster.Spec.Configuration.Authentication.OIDCProviders) == 0 {
		return false, "Hypershift HostedCluster has no OIDCProviders configured"
	}

	// We lowercase the external auth name to match the HostedCluster OIDCProvider name, because
	// we create the external auth in CS with its name in lowercase.
	expectedName := strings.ToLower(externalAuth.Name)
	var observedProvider *configv1.OIDCProvider
	for i := range hostedCluster.Spec.Configuration.Authentication.OIDCProviders {
		if strings.EqualFold(hostedCluster.Spec.Configuration.Authentication.OIDCProviders[i].Name, expectedName) {
			observedProvider = &hostedCluster.Spec.Configuration.Authentication.OIDCProviders[i]
			break
		}
	}
	if observedProvider == nil {
		return false, fmt.Sprintf("Hypershift HostedCluster OIDCProvider %q not found", expectedName)
	}

	if matches, message := c.hypershiftHostedClusterExternalAuthIssuerSpecMatchesDesired(externalAuth.Properties.Issuer, *observedProvider); !matches {
		return false, message
	}
	if matches, message := c.hypershiftHostedClusterExternalAuthClientsSpecMatchesDesired(externalAuth.Properties.Clients, observedProvider.OIDCClients); !matches {
		return false, message
	}
	if matches, message := c.hypershiftHostedClusterExternalAuthClaimMappingsSpecMatchesDesired(externalAuth.Properties.Claim, *observedProvider); !matches {
		return false, message
	}

	return true, ""
}

// hypershiftHostedClusterExternalAuthIssuerSpecMatchesDesired reports whether HostedCluster OIDCProvider issuer
// configuration matches desired external auth issuer profile.
func (c *operationExternalAuthUpdate) hypershiftHostedClusterExternalAuthIssuerSpecMatchesDesired(desired api.TokenIssuerProfile, observed configv1.OIDCProvider) (bool, string) {
	if desired.URL != observed.Issuer.URL {
		return false, fmt.Sprintf(
			"hypershift HostedCluster OIDCProvider issuer URL is %q, want %q",
			observed.Issuer.URL, desired.URL,
		)
	}

	desiredAudiences := desired.Audiences
	observedAudiences := make([]string, len(observed.Issuer.Audiences))
	for i, a := range observed.Issuer.Audiences {
		observedAudiences[i] = string(a)
	}
	if !slices.Equal(desiredAudiences, observedAudiences) {
		return false, fmt.Sprintf(
			"hypershift HostedCluster OIDCProvider issuer audiences is %v, want %v",
			observedAudiences, desiredAudiences,
		)
	}

	return true, ""
}

// hypershiftHostedClusterExternalAuthClientsSpecMatchesDesired reports whether HostedCluster OIDCProvider clients
// match desired external auth client profiles.
func (c *operationExternalAuthUpdate) hypershiftHostedClusterExternalAuthClientsSpecMatchesDesired(desired []api.ExternalAuthClientProfile, observed []configv1.OIDCClientConfig) (bool, string) {
	if len(desired) != len(observed) {
		return false, fmt.Sprintf(
			"hypershift HostedCluster OIDCProvider has %d clients, want %d",
			len(observed), len(desired),
		)
	}

	for i := range desired {
		desiredExternalAuthClientProfile := desired[i]
		observedOIDCClientConfig := observed[i]

		if desiredExternalAuthClientProfile.ClientID != observedOIDCClientConfig.ClientID {
			return false, fmt.Sprintf(
				"hypershift HostedCluster OIDCProvider client[%d] clientID is %q, want %q",
				i, observedOIDCClientConfig.ClientID, desiredExternalAuthClientProfile.ClientID,
			)
		}
		if desiredExternalAuthClientProfile.Component.Name != observedOIDCClientConfig.ComponentName {
			return false, fmt.Sprintf(
				"hypershift HostedCluster OIDCProvider client %q componentName is %q, want %q",
				desiredExternalAuthClientProfile.ClientID, observedOIDCClientConfig.ComponentName, desiredExternalAuthClientProfile.Component.Name,
			)
		}
		if desiredExternalAuthClientProfile.Component.AuthClientNamespace != observedOIDCClientConfig.ComponentNamespace {
			return false, fmt.Sprintf(
				"hypershift HostedCluster OIDCProvider client %q componentNamespace is %q, want %q",
				desiredExternalAuthClientProfile.ClientID, observedOIDCClientConfig.ComponentNamespace, desiredExternalAuthClientProfile.Component.AuthClientNamespace,
			)
		}
		if !slices.Equal(desiredExternalAuthClientProfile.ExtraScopes, observedOIDCClientConfig.ExtraScopes) {
			return false, fmt.Sprintf(
				"hypershift HostedCluster OIDCProvider client %q extraScopes is %v, want %v",
				desiredExternalAuthClientProfile.ClientID, observedOIDCClientConfig.ExtraScopes, desiredExternalAuthClientProfile.ExtraScopes,
			)
		}
	}

	return true, ""
}

// hypershiftHostedClusterExternalAuthClaimMappingsSpecMatchesDesired reports whether HostedCluster OIDCProvider
// claim mappings match desired external auth claim profile.
func (c *operationExternalAuthUpdate) hypershiftHostedClusterExternalAuthClaimMappingsSpecMatchesDesired(desired api.ExternalAuthClaimProfile, observed configv1.OIDCProvider) (bool, string) {
	if desired.Mappings.Username.Claim != observed.ClaimMappings.Username.Claim {
		return false, fmt.Sprintf(
			"hypershift HostedCluster OIDCProvider username claim is %q, want %q",
			observed.ClaimMappings.Username.Claim, desired.Mappings.Username.Claim,
		)
	}

	var expectedPrefixPolicy configv1.UsernamePrefixPolicy
	switch desired.Mappings.Username.PrefixPolicy {
	case api.UsernameClaimPrefixPolicyPrefix:
		expectedPrefixPolicy = configv1.Prefix
	case api.UsernameClaimPrefixPolicyNoPrefix:
		expectedPrefixPolicy = configv1.NoPrefix
	case api.UsernameClaimPrefixPolicyNone:
		expectedPrefixPolicy = configv1.NoOpinion
	}
	if observed.ClaimMappings.Username.PrefixPolicy != expectedPrefixPolicy {
		return false, fmt.Sprintf(
			"hypershift HostedCluster OIDCProvider username prefixPolicy is %q, want %q",
			observed.ClaimMappings.Username.PrefixPolicy, expectedPrefixPolicy,
		)
	}

	expectedUsernamePrefix := ""
	if desired.Mappings.Username.PrefixPolicy == api.UsernameClaimPrefixPolicyPrefix {
		expectedUsernamePrefix = desired.Mappings.Username.Prefix
	}
	observedUsernamePrefix := ""
	if observed.ClaimMappings.Username.Prefix != nil {
		observedUsernamePrefix = observed.ClaimMappings.Username.Prefix.PrefixString
	}
	if observedUsernamePrefix != expectedUsernamePrefix {
		return false, fmt.Sprintf(
			"hypershift HostedCluster OIDCProvider username prefix is %q, want %q",
			observedUsernamePrefix, expectedUsernamePrefix,
		)
	}

	if desired.Mappings.Groups != nil {
		if observed.ClaimMappings.Groups.Claim != desired.Mappings.Groups.Claim {
			return false, fmt.Sprintf(
				"hypershift HostedCluster OIDCProvider groups claim is %q, want %q",
				observed.ClaimMappings.Groups.Claim, desired.Mappings.Groups.Claim,
			)
		}
		if observed.ClaimMappings.Groups.Prefix != desired.Mappings.Groups.Prefix {
			return false, fmt.Sprintf(
				"hypershift HostedCluster OIDCProvider groups prefix is %q, want %q",
				observed.ClaimMappings.Groups.Prefix, desired.Mappings.Groups.Prefix,
			)
		}
	} else if observed.ClaimMappings.Groups.Claim != "" || observed.ClaimMappings.Groups.Prefix != "" {
		return false, fmt.Sprintf(
			"hypershift HostedCluster OIDCProvider has groups claim %q prefix %q, want no groups",
			observed.ClaimMappings.Groups.Claim, observed.ClaimMappings.Groups.Prefix,
		)
	}

	if matches, message := c.hypershiftHostedClusterExternalAuthValidationRulesSpecMatchesDesired(desired.ValidationRules, observed.ClaimValidationRules); !matches {
		return false, message
	}

	return true, ""
}

// hypershiftHostedClusterExternalAuthValidationRulesSpecMatchesDesired reports whether HostedCluster OIDCProvider
// validation rules match desired external auth validation rules.
func (c *operationExternalAuthUpdate) hypershiftHostedClusterExternalAuthValidationRulesSpecMatchesDesired(desired []api.TokenClaimValidationRule, observed []configv1.TokenClaimValidationRule) (bool, string) {
	if len(desired) != len(observed) {
		return false, fmt.Sprintf(
			"hypershift HostedCluster OIDCProvider has %d validation rules, want %d",
			len(observed), len(desired),
		)
	}

	for i := range desired {
		desiredRule := desired[i]
		observedRule := observed[i]

		switch desiredRule.Type {
		case api.TokenValidationRuleTypeRequiredClaim:
			if observedRule.Type != configv1.TokenValidationRuleTypeRequiredClaim {
				return false, fmt.Sprintf(
					"hypershift HostedCluster OIDCProvider validation rule[%d] type is %q, want %q",
					i, observedRule.Type, configv1.TokenValidationRuleTypeRequiredClaim,
				)
			}
			if observedRule.RequiredClaim == nil {
				return false, fmt.Sprintf(
					"hypershift HostedCluster OIDCProvider validation rule[%d] has nil requiredClaim but expects a requiredClaim",
					i,
				)
			}
			if desiredRule.RequiredClaim.Claim != observedRule.RequiredClaim.Claim {
				return false, fmt.Sprintf(
					"hypershift HostedCluster OIDCProvider validation rule[%d] claim is %q, want %q",
					i, observedRule.RequiredClaim.Claim, desiredRule.RequiredClaim.Claim,
				)
			}
			if desiredRule.RequiredClaim.RequiredValue != observedRule.RequiredClaim.RequiredValue {
				return false, fmt.Sprintf(
					"hypershift HostedCluster OIDCProvider validation rule[%d] requiredValue is %q, want %q",
					i, observedRule.RequiredClaim.RequiredValue, desiredRule.RequiredClaim.RequiredValue,
				)
			}
		default:
			return false, fmt.Sprintf(
				"hypershift HostedCluster OIDCProvider validation rule[%d] has unsupported desired type %q",
				i, desiredRule.Type,
			)
		}
	}

	return true, ""
}

// clusterServiceExternalAuthSpecOperationState reports whether Cluster Service external auth spec fields
// match desired state intent for the external auth update operation. Only checks outside CS .status.
func (c *operationExternalAuthUpdate) clusterServiceExternalAuthSpecOperationState(externalAuth *api.HCPOpenShiftClusterExternalAuth, csExternalAuth *arohcpv1alpha1.ExternalAuth) (*operationState, error) {
	if matches, message, err := c.clusterServiceExternalAuthSpecMatchesDesired(externalAuth, csExternalAuth); err != nil {
		return nil, err
	} else if !matches {
		return newOperationState(arm.ProvisioningStateUpdating, message), nil
	}
	return newOperationState(arm.ProvisioningStateSucceeded, ""), nil
}

// clusterServiceExternalAuthSpecMatchesDesired reports whether Cluster Service external auth spec fields
// relevant to the external auth update operation match desired state. Returns false and a diagnostic
// message when any leaf check fails.
func (c *operationExternalAuthUpdate) clusterServiceExternalAuthSpecMatchesDesired(externalAuth *api.HCPOpenShiftClusterExternalAuth, csExternalAuth *arohcpv1alpha1.ExternalAuth) (bool, string, error) {
	desiredJSON, err := ocm.ExternalAuthUpdateDispatchConfigJSONFromRP(externalAuth)
	if err != nil {
		return false, "", err
	}
	observedJSON, err := ocm.ExternalAuthUpdateDispatchConfigJSONFromCS(csExternalAuth)
	if err != nil {
		return false, "", err
	}
	// We check if the desired config coming from cosmos differs from the actual config coming from cluster service.
	// Comparison uses canonical JSON (sorted object keys at every level) so we can compare them
	// using direct string equality.
	if desiredJSON == observedJSON {
		return true, "", nil
	}

	return false, fmt.Sprintf("Cluster Service external auth spec does not match desired: desired=%s, actual=%s", desiredJSON, observedJSON), nil
}
