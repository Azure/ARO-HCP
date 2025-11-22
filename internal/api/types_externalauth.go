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
	"time"

	"github.com/google/uuid"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// HCPOpenShiftClusterExternalAuth represents the external auth config resource for ARO HCP
// OpenShift clusters.
type HCPOpenShiftClusterExternalAuth struct {
	arm.ProxyResource
	Properties                HCPOpenShiftClusterExternalAuthProperties                `json:"properties" validate:"required"`
	ServiceProviderProperties HCPOpenShiftClusterExternalAuthServiceProviderProperties `json:"serviceProviderProperties,omitempty" validate:"required"`
}

var _ CosmosPersistable = &HCPOpenShiftClusterExternalAuth{}

func (o *HCPOpenShiftClusterExternalAuth) GetCosmosData() CosmosData {
	return CosmosData{
		ID:                o.ID,
		ProvisioningState: o.Properties.ProvisioningState,
		ClusterServiceID:  o.ServiceProviderProperties.ClusterServiceID,
	}
}

func (o *HCPOpenShiftClusterExternalAuth) SetCosmosDocumentData(cosmosUID uuid.UUID) {
	o.ServiceProviderProperties.CosmosUID = cosmosUID.String()
}

// HCPOpenShiftClusterNodePoolProperties represents the property bag of a
// HCPOpenShiftClusterNodePool resource.
type HCPOpenShiftClusterExternalAuthProperties struct {
	ProvisioningState arm.ProvisioningState       `json:"provisioningState"       visibility:"read"                     validate:"omitempty"`
	Condition         ExternalAuthCondition       `json:"condition,omitzero"      visibility:"read"                     validate:"omitempty"`
	Issuer            TokenIssuerProfile          `json:"issuer"                  visibility:"read create update"       validate:"required"`
	Clients           []ExternalAuthClientProfile `json:"clients"                 visibility:"read create update"       validate:"omitempty,max=20,dive"`
	Claim             ExternalAuthClaimProfile    `json:"claim"                   visibility:"read create update"       validate:"required"`
}

type HCPOpenShiftClusterExternalAuthServiceProviderProperties struct {
	CosmosUID        string     `json:"cosmosUID,omitempty"`
	ClusterServiceID InternalID `json:"clusterServiceID,omitempty"                visibility:"read"`
}

// Condition defines an observation of the external auth state.
// Visibility for the entire struct is "read".
type ExternalAuthCondition struct {
	Type               ExternalAuthConditionType `json:"type"               validate:"enum_externalauthconditiontype"`
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
	URL       string   `json:"url"       validate:"required,url,startswith=https://"`
	Audiences []string `json:"audiences" validate:"required,max=10"`
	CA        string   `json:"ca"        validate:"omitempty,pem_certificates"`
}

// External Auth client profile
// This configures how on-cluster, platform clients should request tokens from the identity provider.
// Visibility for the entire struct is "read create update".
type ExternalAuthClientProfile struct {
	Component   ExternalAuthClientComponentProfile `json:"component"   validate:"required"`
	ClientID    string                             `json:"clientId"    validate:"required"`
	ExtraScopes []string                           `json:"extraScopes" validate:"omitempty"`
	Type        ExternalAuthClientType             `json:"type"        validate:"required,enum_externalauthclienttype"`
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
	ValidationRules []TokenClaimValidationRule `json:"validationRules" validate:"omitempty,dive"`
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
	Claim        string                    `json:"claim"        validate:"required,max=256"`
	Prefix       string                    `json:"prefix"       validate:"required_if=PrefixPolicy Prefix,excluded_unless=PrefixPolicy Prefix"`
	PrefixPolicy UsernameClaimPrefixPolicy `json:"prefixPolicy" validate:"enum_usernameclaimprefixpolicy"`
}

// External Auth claim validation rule
// Visibility for the entire struct is "read create update".
type TokenClaimValidationRule struct {
	Type          TokenValidationRuleType `json:"type"          validate:"required,enum_tokenvalidationruletyperequiredclaim"`
	RequiredClaim TokenRequiredClaim      `json:"requiredClaim" validate:"omitempty"`
}

// Token required claim validation rule.
// Visibility for the entire struct is "read create update".
type TokenRequiredClaim struct {
	Claim         string `json:"claim"         validate:"required"`
	RequiredValue string `json:"requiredValue" validate:"required"`
}

func NewDefaultHCPOpenShiftClusterExternalAuth(resourceID *azcorearm.ResourceID) *HCPOpenShiftClusterExternalAuth {
	return &HCPOpenShiftClusterExternalAuth{
		ProxyResource: arm.NewProxyResource(resourceID),
		Properties: HCPOpenShiftClusterExternalAuthProperties{
			Claim: ExternalAuthClaimProfile{
				Mappings: TokenClaimMappingsProfile{
					Username: UsernameClaimProfile{
						PrefixPolicy: UsernameClaimPrefixPolicyNone,
					},
				},
			},
		},
	}
}

func (o *HCPOpenShiftClusterExternalAuth) Validate() []arm.CloudErrorBody {
	return nil
}
