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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// HCPOpenShiftClusterExternalAuth represents the external auth config resource for ARO HCP
// OpenShift clusters.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type HCPOpenShiftClusterExternalAuth struct {
	arm.ProxyResource
	Properties                HCPOpenShiftClusterExternalAuthProperties                `json:"properties"`
	ServiceProviderProperties HCPOpenShiftClusterExternalAuthServiceProviderProperties `json:"serviceProviderProperties,omitempty"`
}

var _ arm.CosmosPersistable = &HCPOpenShiftClusterExternalAuth{}

func (o *HCPOpenShiftClusterExternalAuth) GetCosmosData() *arm.CosmosMetadata {
	return &arm.CosmosMetadata{
		ResourceID:        o.ID,
		ExistingCosmosUID: o.ServiceProviderProperties.ExistingCosmosUID,
	}
}

// HCPOpenShiftClusterNodePoolProperties represents the property bag of a
// HCPOpenShiftClusterNodePool resource.
type HCPOpenShiftClusterExternalAuthProperties struct {
	ProvisioningState arm.ProvisioningState       `json:"provisioningState"`
	Condition         ExternalAuthCondition       `json:"condition,omitzero"`
	Issuer            TokenIssuerProfile          `json:"issuer"`
	Clients           []ExternalAuthClientProfile `json:"clients"`
	Claim             ExternalAuthClaimProfile    `json:"claim"`
}

type HCPOpenShiftClusterExternalAuthServiceProviderProperties struct {
	ExistingCosmosUID string     `json:"existingCosmosUID,omitempty"`
	ClusterServiceID  InternalID `json:"clusterServiceID,omitempty"`
	ActiveOperationID string     `json:"activeOperationId,omitempty"`
}

// Condition defines an observation of the external auth state.
// Visibility for the entire struct is "read".
type ExternalAuthCondition struct {
	Type               ExternalAuthConditionType `json:"type"`
	Status             ConditionStatusType       `json:"status"`
	LastTransitionTime time.Time                 `json:"lastTransitionTime"`
	Reason             string                    `json:"reason"`
	Message            string                    `json:"message"`
}

// Token issuer profile
// This configures how the platform interacts with the identity provider and
// how tokens issued from the identity provider are evaluated by the Kubernetes API server.
// Visbility for the entire struct is "read create update".
type TokenIssuerProfile struct {
	URL       string   `json:"url"`
	Audiences []string `json:"audiences"`
	CA        string   `json:"ca"`
}

// External Auth client profile
// This configures how on-cluster, platform clients should request tokens from the identity provider.
// Visibility for the entire struct is "read create update".
type ExternalAuthClientProfile struct {
	Component   ExternalAuthClientComponentProfile `json:"component"`
	ClientID    string                             `json:"clientId"`
	ExtraScopes []string                           `json:"extraScopes"`
	Type        ExternalAuthClientType             `json:"type"`
}

// External Auth component profile
// Must have unique namespace/name pairs.
// Visibility for the entire struct is "read create update".
type ExternalAuthClientComponentProfile struct {
	Name                string `json:"name"`
	AuthClientNamespace string `json:"authClientNamespace"`
}

// External Auth claim profile
// Visibility for the entire struct is "read create update".
type ExternalAuthClaimProfile struct {
	Mappings        TokenClaimMappingsProfile  `json:"mappings"`
	ValidationRules []TokenClaimValidationRule `json:"validationRules"`
}

// External Auth claim mappings profile.
// At a minimum username or groups must be defined.
// Visibility for the entire struct is "read create update".
type TokenClaimMappingsProfile struct {
	Username UsernameClaimProfile `json:"username"`
	Groups   *GroupClaimProfile   `json:"groups"`
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
	Claim  string `json:"claim"`
	Prefix string `json:"prefix"`
}

// External Auth claim profile
// This configures how the username of a cluster identity should be constructed
// from the claims in a JWT token issued by the identity provider.
// Visibility for the entire struct is "read create update".
type UsernameClaimProfile struct {
	Claim        string                    `json:"claim"`
	Prefix       string                    `json:"prefix"`
	PrefixPolicy UsernameClaimPrefixPolicy `json:"prefixPolicy"`
}

// External Auth claim validation rule
// Visibility for the entire struct is "read create update".
type TokenClaimValidationRule struct {
	Type          TokenValidationRuleType `json:"type"`
	RequiredClaim TokenRequiredClaim      `json:"requiredClaim"`
}

// Token required claim validation rule.
// Visibility for the entire struct is "read create update".
type TokenRequiredClaim struct {
	Claim         string `json:"claim"`
	RequiredValue string `json:"requiredValue"`
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
