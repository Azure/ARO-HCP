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
	Properties HCPOpenShiftClusterExternalAuthProperties `json:"properties" validate:"required_for_put"`
}

// HCPOpenShiftClusterNodePoolProperties represents the property bag of a
// HCPOpenShiftClusterNodePool resource.
type HCPOpenShiftClusterExternalAuthProperties struct {
	ProvisioningState arm.ExternalAuthProvisioningState `json:"provisioningState"       visibility:"read"                     validate:"omitempty"`
	Condition         ExternalAuthCondition             `json:"condition,omitzero"      visibility:"read"                     validate:"omitempty"`
	Issuer            TokenIssuerProfile                `json:"issuer"                  visibility:"read create update"       validate:"required_for_put"`
	Clients           []ExternalAuthClientProfile       `json:"clients"                 visibility:"read create update"       validate:"max=20,omitempty"`
	Claim             ExternalAuthClaimProfile          `json:"claim"                   visibility:"read create update"       validate:"required_for_put"`
}

/** Condition defines an observation of the external auth state. */
type ExternalAuthCondition struct {
	ConditionType      ExternalAuthConditionType `json:"type"                   validate:"enum_externalauthconditiontype"`
	Status             ConditionStatusType       `json:"status"                 validate:"enum_externalauthconditionstatustype"`
	LastTransitionTime time.Time                 `json:"lastTransitionTime"`
	Reason             string                    `json:"reason"`
	Message            string                    `json:"message"`
}

/** Token issuer profile
 * This configures how the platform interacts with the identity provider and
 * how tokens issued from the identity provider are evaluated by the Kubernetes API server.
 */
type TokenIssuerProfile struct {
	// TODO: validate https url
	Url string `json:"url"                      visibility:"read create update"       validate:"required,url,startswith=https://"`
	// TODO: validate at least one of the entries must match the 'aud' claim in the JWT token.
	Audiences []string `json:"audiences"        visibility:"read create update"       validate:"required,min=1,max=10,dive,required"`
	Ca        string   `json:"ca"               visibility:"read create update"       validate:"omitempty,pem_certificates"`
}

/** External Auth client profile
 * This configures how on-cluster, platform clients should request tokens from the identity provider.
 */
type ExternalAuthClientProfile struct {
	Component ExternalAuthClientComponentProfile `json:"component"                visibility:"read create update"       validate:"required_for_put"`
	// TODO: The clientId must appear in the audience field of the TokenIssuerProfile.
	ClientId                      string                 `json:"clientId"                       visibility:"read create update"       validate:"required_for_put"`
	ExtraScopes                   []string               `json:"extraScopes"                    visibility:"read create update"       validate:"omitempty"`
	ExternalAuthClientProfileType ExternalAuthClientType `json:"enum_externalauthclienttype"    visibility:"read create update"       validate:"required_for_put"`
}

/** External Auth component profile
 * Must have unique namespace/name pairs.
 */
type ExternalAuthClientComponentProfile struct {
	Name                string `json:"name"                   visibility:"read create update"     validate:"required_for_put,max=256"`
	AuthClientNamespace string `json:"authClientNamespace"    visibility:"read create update"     validate:"required_for_put,max=63"`
}

/** External Auth claim profile */
type ExternalAuthClaimProfile struct {
	Mappings        TokenClaimMappingsProfile  `json:"mappings"           visibility:"read create update"        validate:"required_for_put"`
	ValidationRules []TokenClaimValidationRule `json:"validationRules"    visibility:"read create update"        validate:"omitempty"`
}

/** External Auth claim mappings profile.
 * At a minimum username or groups must be defined.
 */
type TokenClaimMappingsProfile struct {
	Username UsernameClaimProfile `json:"username"        visibility:"read create update"        validate:"required_for_put"`
	Groups   *GroupClaimProfile   `json:"groups"          visibility:"read create update"        validate:"omitempty"`
}

/** External Auth claim profile
 * This configures how the groups of a cluster identity should be constructed
 * from the claims in a JWT token issued by the identity provider. When
 * referencing a claim, if the claim is present in the JWT token, its value
 * must be a list of groups separated by a comma (',').
 *
 * For example - '"example"' and '"exampleOne", "exampleTwo", "exampleThree"' are valid claim values.
 */
type GroupClaimProfile struct {
	Claim  string `json:"claim"         visibility:"read create update"      validate:"required_for_put,max=256"`
	Prefix string `json:"prefix"        visibility:"read create update"      validate:"omitempty"`
}

/** External Auth claim profile
 * This configures how the username of a cluster identity should be constructed
 * from the claims in a JWT token issued by the identity provider.
 */
type UsernameClaimProfile struct {
	Claim        string `json:"claim"             visibility:"read create update"      validate:"required_for_put,max=256"`
	Prefix       string `json:"prefix"            visibility:"read create update"      validate:"omitempty"`
	PrefixPolicy string `json:"prefixPolicy"      visibility:"read create update"      validate:"omitempty"`
}

/** External Auth claim validation rule */
type TokenClaimValidationRule struct {
	TokenClaimValidationRuleType TokenValidationRuleType `json:"type"                visibility:"read create update"      validate:"required_for_put,enum_tokenvalidationruletyperequiredclaim"`
	RequiredClaim                TokenRequiredClaim      `json:"requiredClaim"       visibility:"read create update"      validate:"omitempty"`
}

/** Token required claim validation rule. */
type TokenRequiredClaim struct {
	Claim         string `json:"claim"             visibility:"read create update"      validate:"required_for_put"`
	RequiredValue string `json:"requiredValue"     visibility:"read create update"      validate:"required_for_put"`
}

func NewDefaultHCPOpenShiftClusterExternalAuth() *HCPOpenShiftClusterExternalAuth {
	// Currently the only defaults in External Auth is for TokenValidationRuleType but as
	// there are no TokenValidationRules by default the object is just empty.
	return &HCPOpenShiftClusterExternalAuth{}
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

func (externalAuth *HCPOpenShiftClusterExternalAuth) Validate(validate *validator.Validate, request *http.Request) []arm.CloudErrorBody {
	errorDetails := ValidateRequest(validate, request, externalAuth)

	// Proceed with complex, multi-field validation only if single-field
	// validation has passed. This avoids running further checks on data
	// we already know to be invalid and prevents the response body from
	// becoming overwhelming.
	if len(errorDetails) == 0 {
		errorDetails = append(errorDetails, externalAuth.validateUniqueClientIdentifiers()...)
		errorDetails = append(errorDetails, externalAuth.validateClientIdInAudiences()...)
	}

	return errorDetails
}
