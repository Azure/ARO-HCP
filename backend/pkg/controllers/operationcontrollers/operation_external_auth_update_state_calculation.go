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

func (c *operationExternalAuthUpdate) clusterServiceExternalAuthSpecOperationState(
	desired *api.HCPOpenShiftClusterExternalAuth,
	csExternalAuth *arohcpv1alpha1.ExternalAuth,
) (*operationState, error) {
	differs, err := ocm.ExternalAuthUpdateDispatchConfigDiffers(desired, csExternalAuth)
	if err != nil {
		return nil, err
	}
	if differs {
		desiredJSON, err := ocm.ExternalAuthUpdateDispatchConfigJSONFromRP(desired)
		if err != nil {
			return nil, err
		}
		actualJSON, err := ocm.ExternalAuthUpdateDispatchConfigJSONFromCS(csExternalAuth)
		if err != nil {
			return nil, err
		}
		return newOperationState(
			arm.ProvisioningStateUpdating,
			fmt.Sprintf("Cluster Service external auth spec does not match desired: desired=%s, actual=%s", desiredJSON, actualJSON),
		), nil
	}
	return newOperationState(arm.ProvisioningStateSucceeded, ""), nil
}

func (c *operationExternalAuthUpdate) hypershiftExternalAuthOperationState(
	ctx context.Context,
	operation *api.Operation,
	externalAuth *api.HCPOpenShiftClusterExternalAuth,
) (*operationState, error) {
	logger := utils.LoggerFromContext(ctx)

	hostedCluster, err := maestrohelpers.GetCachedHostedClusterForCluster(
		ctx,
		c.readDesireLister,
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName,
		operation.ExternalID.Parent.Name,
	)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if hostedCluster == nil {
		return newOperationState(arm.ProvisioningStateUpdating, "HostedCluster has not been observed yet"), nil
	}

	if matches, message := hypershiftExternalAuthSpecMatchesDesired(externalAuth, hostedCluster); !matches {
		logger.Info("hypershift HostedCluster external auth spec does not match desired", "message", message)
		return newOperationState(arm.ProvisioningStateUpdating, message), nil
	}

	// TODO compare with Hypershift HostedCluster relevant parts of status.configuration.authentication when possible.
	// At the moment of writing this (2026-06-29) the status.configuration.authentication is only available on
	// HostedClusters >= 4.21.

	return newOperationState(arm.ProvisioningStateSucceeded, ""), nil
}

func hypershiftExternalAuthSpecMatchesDesired(externalAuth *api.HCPOpenShiftClusterExternalAuth, hostedCluster *v1beta1.HostedCluster) (bool, string) {
	if hostedCluster.Spec.Configuration == nil ||
		hostedCluster.Spec.Configuration.Authentication == nil ||
		len(hostedCluster.Spec.Configuration.Authentication.OIDCProviders) == 0 {
		return false, "HostedCluster has no OIDCProviders configured"
	}

	expectedName := strings.ToLower(externalAuth.Name)
	var observedProvider *configv1.OIDCProvider
	for i := range hostedCluster.Spec.Configuration.Authentication.OIDCProviders {
		if strings.EqualFold(hostedCluster.Spec.Configuration.Authentication.OIDCProviders[i].Name, expectedName) {
			observedProvider = &hostedCluster.Spec.Configuration.Authentication.OIDCProviders[i]
			break
		}
	}
	if observedProvider == nil {
		return false, fmt.Sprintf("HostedCluster OIDCProvider %q not found", expectedName)
	}

	if matches, message := externalAuthIssuerMatchesDesired(externalAuth.Properties.Issuer, *observedProvider); !matches {
		return false, message
	}
	if matches, message := externalAuthClientsMatchDesired(externalAuth.Properties.Clients, observedProvider.OIDCClients); !matches {
		return false, message
	}
	if matches, message := externalAuthClaimMappingsMatchDesired(externalAuth.Properties.Claim, *observedProvider); !matches {
		return false, message
	}

	return true, ""
}

func externalAuthIssuerMatchesDesired(desired api.TokenIssuerProfile, observed configv1.OIDCProvider) (bool, string) {
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

func externalAuthClientsMatchDesired(desired []api.ExternalAuthClientProfile, observed []configv1.OIDCClientConfig) (bool, string) {
	if len(desired) != len(observed) {
		return false, fmt.Sprintf(
			"hypershift HostedCluster OIDCProvider has %d clients, want %d",
			len(observed), len(desired),
		)
	}

	sortedDesired := make([]api.ExternalAuthClientProfile, len(desired))
	copy(sortedDesired, desired)
	slices.SortFunc(sortedDesired, func(a, b api.ExternalAuthClientProfile) int {
		return strings.Compare(a.ClientID, b.ClientID)
	})

	sortedObserved := make([]configv1.OIDCClientConfig, len(observed))
	copy(sortedObserved, observed)
	slices.SortFunc(sortedObserved, func(a, b configv1.OIDCClientConfig) int {
		return strings.Compare(a.ClientID, b.ClientID)
	})

	for i := range sortedDesired {
		d := sortedDesired[i]
		o := sortedObserved[i]

		if d.ClientID != o.ClientID {
			return false, fmt.Sprintf(
				"hypershift HostedCluster OIDCProvider client[%d] clientID is %q, want %q",
				i, o.ClientID, d.ClientID,
			)
		}
		if d.Component.Name != o.ComponentName {
			return false, fmt.Sprintf(
				"hypershift HostedCluster OIDCProvider client %q componentName is %q, want %q",
				d.ClientID, o.ComponentName, d.Component.Name,
			)
		}
		if d.Component.AuthClientNamespace != o.ComponentNamespace {
			return false, fmt.Sprintf(
				"hypershift HostedCluster OIDCProvider client %q componentNamespace is %q, want %q",
				d.ClientID, o.ComponentNamespace, d.Component.AuthClientNamespace,
			)
		}
		if !slices.Equal(d.ExtraScopes, o.ExtraScopes) {
			return false, fmt.Sprintf(
				"hypershift HostedCluster OIDCProvider client %q extraScopes is %v, want %v",
				d.ClientID, o.ExtraScopes, d.ExtraScopes,
			)
		}
	}

	return true, ""
}

func externalAuthClaimMappingsMatchDesired(desired api.ExternalAuthClaimProfile, observed configv1.OIDCProvider) (bool, string) {
	if desired.Mappings.Username.Claim != observed.ClaimMappings.Username.Claim {
		return false, fmt.Sprintf(
			"hypershift HostedCluster OIDCProvider username claim is %q, want %q",
			observed.ClaimMappings.Username.Claim, desired.Mappings.Username.Claim,
		)
	}

	observedUsernamePrefix := ""
	if observed.ClaimMappings.Username.Prefix != nil {
		observedUsernamePrefix = observed.ClaimMappings.Username.Prefix.PrefixString
	}
	if desired.Mappings.Username.PrefixPolicy == api.UsernameClaimPrefixPolicyPrefix {
		if observedUsernamePrefix != desired.Mappings.Username.Prefix {
			return false, fmt.Sprintf(
				"hypershift HostedCluster OIDCProvider username prefix is %q, want %q",
				observedUsernamePrefix, desired.Mappings.Username.Prefix,
			)
		}
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
	}

	if matches, message := externalAuthValidationRulesMatchDesired(desired.ValidationRules, observed.ClaimValidationRules); !matches {
		return false, message
	}

	return true, ""
}

func externalAuthValidationRulesMatchDesired(desired []api.TokenClaimValidationRule, observed []configv1.TokenClaimValidationRule) (bool, string) {
	var observedRequiredClaim []configv1.TokenClaimValidationRule
	for _, r := range observed {
		if r.Type == configv1.TokenValidationRuleTypeRequiredClaim {
			observedRequiredClaim = append(observedRequiredClaim, r)
		}
	}

	if len(desired) != len(observedRequiredClaim) {
		return false, fmt.Sprintf(
			"hypershift HostedCluster OIDCProvider has %d RequiredClaim validation rules, want %d",
			len(observedRequiredClaim), len(desired),
		)
	}

	for i := range desired {
		d := desired[i]
		o := observedRequiredClaim[i]

		if o.RequiredClaim == nil {
			return false, fmt.Sprintf(
				"hypershift HostedCluster OIDCProvider validation rule[%d] has nil requiredClaim",
				i,
			)
		}
		if d.RequiredClaim.Claim != o.RequiredClaim.Claim {
			return false, fmt.Sprintf(
				"hypershift HostedCluster OIDCProvider validation rule[%d] claim is %q, want %q",
				i, o.RequiredClaim.Claim, d.RequiredClaim.Claim,
			)
		}
		if d.RequiredClaim.RequiredValue != o.RequiredClaim.RequiredValue {
			return false, fmt.Sprintf(
				"hypershift HostedCluster OIDCProvider validation rule[%d] requiredValue is %q, want %q",
				i, o.RequiredClaim.RequiredValue, d.RequiredClaim.RequiredValue,
			)
		}
	}

	return true, ""
}
