// Copyright 2025 Microsoft Corporation
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

package api

import (
	"fmt"
	"net/http"
	"time"

	validator "github.com/go-playground/validator/v10"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// HCPOpenShiftClusterExternalAuth represents the external auth config resource for ARO HCP
// OpenShift clusters.
type HCPOpenShiftClusterExternalAuth struct {
	arm.ProxyResource
	Properties HCPOpenShiftClusterExternalAuthProperties `json:"properties" validate:"required"`
}

// HCPOpenShiftClusterNodePoolProperties represents the property bag of a
// HCPOpenShiftClusterNodePool resource.
type HCPOpenShiftClusterExternalAuthProperties struct {
	ProvisioningState arm.ExternalAuthProvisioningState `json:"provisioningState"       visibility:"read"                     validate:"omitempty"`
	Condition         ExternalAuthCondition             `json:"condition,omitzero"      visibility:"read"                     validate:"omitempty"`
	Issuer            TokenIssuerProfile                `json:"issuer"                  visibility:"read create update"       validate:"required"`
	Clients           []ExternalAuthClientProfile       `json:"clients"                 visibility:"read create update"       validate:"max=20,omitempty"`
	Claim             ExternalAuthClaimProfile          `json:"claim"                   visibility:"read create update"       validate:"required"`
}

// Condition defines an observation of the external auth state.
// Visibility for the entire struct is "read".
type ExternalAuthCondition struct {
	ConditionType      ExternalAuthConditionType `json:"type"               validate:"enum_externalauthconditiontype"`
	Status             ConditionStatusType       `json:"status"             validate:"enum_externalauthconditionstatustype"`
	LastTransitionTime time.Time                 `json:"lastTransitionTime"`
	Reason             string                    `json:"reason"`
	Message            string                    `json:"message"`
}

// Token issuer profile
// This configures how the platform interacts with the identity provider and
// how tokens issued from the identity provider are evaluated by the Kubernetes API server.
// Visbility for the entire struct is "read create update".
type TokenIssuerProfile struct {
	Url       string   `json:"url"       validate:"required,url,startswith=https://"`
	Audiences []string `json:"audiences" validate:"required,min=0,max=10"`
	Ca        *string  `json:"ca"        validate:"omitempty,pem_certificates"`
}

// External Auth client profile
// This configures how on-cluster, platform clients should request tokens from the identity provider.
// Visibility for the entire struct is "read create update".
type ExternalAuthClientProfile struct {
	Component                     ExternalAuthClientComponentProfile `json:"component"   validate:"required"`
	ClientId                      string                             `json:"clientId"    validate:"required"`
	ExtraScopes                   []string                           `json:"extraScopes" validate:"omitempty"`
	ExternalAuthClientProfileType ExternalAuthClientType             `json:"type"        validate:"required,enum_externalauthclienttype"`
}

// External Auth component profile
// Must have unique namespace/name pairs.
// Visibility for the entire struct is "read create update".
type ExternalAuthClientComponentProfile struct {
	Name                string `json:"name"                validate:"required,max=256"`
	AuthClientNamespace string `json:"authClientNamespace" validate:"required,max=63"`
}

// External Auth claim profile
// Visibility for the entire struct is "read create update".
type ExternalAuthClaimProfile struct {
	Mappings        TokenClaimMappingsProfile  `json:"mappings"        validate:"required"`
	ValidationRules []TokenClaimValidationRule `json:"validationRules" validate:"omitempty"`
}

// External Auth claim mappings profile.
// At a minimum username or groups must be defined.
// Visibility for the entire struct is "read create update".
type TokenClaimMappingsProfile struct {
	Username UsernameClaimProfile `json:"username" validate:"required"`
	Groups   *GroupClaimProfile   `json:"groups"   validate:"omitempty"`
}

// External Auth claim profile
// This configures how the groups of a cluster identity should be constructed
// from the claims in a JWT token issued by the identity provider. When
// referencing a claim, if the claim is present in the JWT token, its value
// must be a list of groups separated by a comma (',').
//
// For example - '"example"' and '"exampleOne", "exampleTwo", "exampleThree"' are valid claim values.
//
// Visibility for the entire struct is "read create update".
type GroupClaimProfile struct {
	Claim  string `json:"claim"  validate:"required,max=256"`
	Prefix string `json:"prefix" validate:"omitempty"`
}

// External Auth claim profile
// This configures how the username of a cluster identity should be constructed
// from the claims in a JWT token issued by the identity provider.
// Visibility for the entire struct is "read create update".
type UsernameClaimProfile struct {
	Claim        string                        `json:"claim"        validate:"required,max=256"`
	Prefix       string                        `json:"prefix"       validate:"omitempty"`
	PrefixPolicy UsernameClaimPrefixPolicyType `json:"prefixPolicy" validate:"omitempty,enum_usernameclaimprefixpolicytype"`
}

// External Auth claim validation rule
// Visibility for the entire struct is "read create update".
type TokenClaimValidationRule struct {
	TokenClaimValidationRuleType TokenValidationRuleType `json:"type"          validate:"required,enum_tokenvalidationruletyperequiredclaim"`
	RequiredClaim                TokenRequiredClaim      `json:"requiredClaim" validate:"omitempty"`
}

// Token required claim validation rule.
// Visibility for the entire struct is "read create update".
type TokenRequiredClaim struct {
	Claim         string `json:"claim"         validate:"required"`
	RequiredValue string `json:"requiredValue" validate:"required"`
}

func NewDefaultHCPOpenShiftClusterExternalAuth() *HCPOpenShiftClusterExternalAuth {
	return &HCPOpenShiftClusterExternalAuth{
		Properties: HCPOpenShiftClusterExternalAuthProperties{
			Claim: ExternalAuthClaimProfile{
				Mappings: TokenClaimMappingsProfile{
					Username: UsernameClaimProfile{
						PrefixPolicy: UsernameClaimPrefixPolicyTypeNone,
					},
				},
			},
		},
	}
}

// This combination is used later in the system as a unique identifier and as
// such we must ensure uniqueness.
func (externalAuth *HCPOpenShiftClusterExternalAuth) validateUniqueClientIdentifiers() []arm.CloudErrorBody {
	var errorDetails []arm.CloudErrorBody

	if len(externalAuth.Properties.Clients) > 1 {
		clientIdsMap := make(map[string][]string, len(externalAuth.Properties.Clients))
		for _, elem := range externalAuth.Properties.Clients {
			var uniqueKey = elem.generateUniqueIdentifier()
			if clientIds, ok := clientIdsMap[uniqueKey]; ok {
				clientIdsMap[uniqueKey] = append(clientIds, elem.ClientId)
			} else {
				clientIdsMap[uniqueKey] = []string{elem.ClientId}
			}
		}
		for uniqueKey, clientIds := range clientIdsMap {
			if len(clientIds) > 1 {
				errorDetails = append(errorDetails, arm.CloudErrorBody{
					Code: arm.CloudErrorCodeInvalidRequestContent,
					Message: fmt.Sprintf(
						("External Auth Clients must have a unique combination of component.Name & component.AuthClientNamespace. " +
							"The following clientIds share the same unique combination '%s' and are invalid: \n '%s' "),
						uniqueKey,
						clientIds),
					Target: "properties.clients",
				})

			}
		}

	}
	return errorDetails
}

// This combination is used later in the system as a unique identifier.
func (c ExternalAuthClientProfile) generateUniqueIdentifier() string {
	return c.Component.Name + c.Component.AuthClientNamespace
}

// validateClientIdInAudiences checks that each ClientId matches an audience in the TokenIssuerProfile.
func (externalAuth *HCPOpenShiftClusterExternalAuth) validateClientIdInAudiences() []arm.CloudErrorBody {
	var errorDetails []arm.CloudErrorBody

	if len(externalAuth.Properties.Clients) > 0 {
		audiencesSet := make(map[string]struct{}, len(externalAuth.Properties.Issuer.Audiences))
		for _, aud := range externalAuth.Properties.Issuer.Audiences {
			audiencesSet[aud] = struct{}{}
		}

		for i, client := range externalAuth.Properties.Clients {
			if _, found := audiencesSet[client.ClientId]; !found {
				errorDetails = append(errorDetails, arm.CloudErrorBody{
					Code:    arm.CloudErrorCodeInvalidRequestContent,
					Message: fmt.Sprintf("ClientId '%s' in clients[%d] must match an audience in TokenIssuerProfile", client.ClientId, i),
					Target:  "properties.clients",
				})
			}
		}
	}

	return errorDetails
}

// validateUsernamePrefixPolicy checks that a usernameClaimProfile obeys it's own type
func (externalAuth *HCPOpenShiftClusterExternalAuth) validateUsernamePrefixPolicy() []arm.CloudErrorBody {
	var errorDetails []arm.CloudErrorBody

	switch externalAuth.Properties.Claim.Mappings.Username.PrefixPolicy {
	case UsernameClaimPrefixPolicyTypePrefix:
		if len(externalAuth.Properties.Claim.Mappings.Username.Prefix) == 0 {
			errorDetails = append(errorDetails, arm.CloudErrorBody{
				Code:    arm.CloudErrorCodeInvalidRequestContent,
				Message: "UsernameClaimProfile has a PrefixPolicy of 'Prefix' but Username.Prefix is unset",
				Target:  "properties.claim.mappings.username.prefix",
			})
		}
	case UsernameClaimPrefixPolicyTypeNoPrefix:
		if len(externalAuth.Properties.Claim.Mappings.Username.Prefix) > 0 {
			errorDetails = append(errorDetails, arm.CloudErrorBody{
				Code: arm.CloudErrorCodeInvalidRequestContent,
				Message: fmt.Sprintf(
					"UsernameClaimProfile has a PrefixPolicy of 'NoPrefix' but Username.Prefix is set to %s",
					externalAuth.Properties.Claim.Mappings.Username.Prefix,
				),
				Target: "properties.claim.mappings.username.prefix",
			})
		}
	case UsernameClaimPrefixPolicyTypeNone:
	}

	return errorDetails
}

func (externalAuth *HCPOpenShiftClusterExternalAuth) Validate(validate *validator.Validate, request *http.Request) []arm.CloudErrorBody {
	errorDetails := ValidateRequest(validate, request, externalAuth)

	// Proceed with complex, multi-field validation only if single-field
	// validation has passed. This avoids running further checks on data
	// we already know to be invalid and prevents the response body from
	// becoming overwhelming.
	if len(errorDetails) == 0 {
		errorDetails = append(errorDetails, externalAuth.validateUniqueClientIdentifiers()...)
		errorDetails = append(errorDetails, externalAuth.validateClientIdInAudiences()...)
		errorDetails = append(errorDetails, externalAuth.validateUsernamePrefixPolicy()...)
	}

	return errorDetails
}
