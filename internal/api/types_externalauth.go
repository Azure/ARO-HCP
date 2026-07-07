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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// HCPOpenShiftClusterExternalAuth represents the external auth config resource for ARO HCP
// OpenShift clusters.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type HCPOpenShiftClusterExternalAuth struct {
	// PartitionKey holds the lowercased subscriptionID.
	CosmosMetadata `json:"cosmosMetadata" redact:"nonsecret"`

	arm.ProxyResource
	Properties                HCPOpenShiftClusterExternalAuthProperties                `json:"properties" redact:"nonsecret"`
	ServiceProviderProperties HCPOpenShiftClusterExternalAuthServiceProviderProperties `json:"serviceProviderProperties,omitempty" redact:"nonsecret"`
	Status                    HCPOpenShiftClusterExternalAuthStatus                    `json:"status" redact:"nonsecret"`
}

// HCPOpenShiftClusterExternalAuthStatus contains the observed state of the external auth.
type HCPOpenShiftClusterExternalAuthStatus struct {
	// Conditions are the top-level HCPOpenShiftClusterExternalAuth status conditions.
	// Each Condition Type represents a condition and it should be unique among all conditions.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" redact:"notraverse"`
}

// EnsureDefaults fills in default values for fields that may be absent in
// Cosmos documents created before the field was introduced, or on the create
// and preflight paths where the internal type is constructed from external input.
// Only fields where the zero value is never valid user input are safe to default
// here (string enums). See the DDR at docs/api-version-defaults-and-storage.md.
//
// This method should be treated as append-only. Avoid removing defaulting
// rules until all Cosmos documents have been verified to contain the field.
func (ea *HCPOpenShiftClusterExternalAuth) EnsureDefaults() {
	if len(ea.Properties.Claim.Mappings.Username.PrefixPolicy) == 0 {
		ea.Properties.Claim.Mappings.Username.PrefixPolicy = UsernameClaimPrefixPolicyNone
	}
}

var _ arm.CosmosPersistable = &HCPOpenShiftClusterExternalAuth{}

// HCPOpenShiftClusterNodePoolProperties represents the property bag of a
// HCPOpenShiftClusterNodePool resource.
type HCPOpenShiftClusterExternalAuthProperties struct {
	ProvisioningState arm.ProvisioningState       `json:"provisioningState" redact:"nonsecret"`
	Issuer            TokenIssuerProfile          `json:"issuer" redact:"nonsecret"`
	Clients           []ExternalAuthClientProfile `json:"clients" redact:"nonsecret"`
	Claim             ExternalAuthClaimProfile    `json:"claim" redact:"nonsecret"`
}

type HCPOpenShiftClusterExternalAuthServiceProviderProperties struct {
	ClusterServiceID  *InternalID `json:"clusterServiceID,omitempty" redact:"nonsecret"`
	ActiveOperationID string      `json:"activeOperationId,omitempty" redact:"nonsecret"`
	// DeletionTimestamp is the timestamp at which the ExternalAuth deletion was requested.
	// The timestamp is in UTC.
	// A nil value indicates that the ExternalAuth deletion has not been requested.
	DeletionTimestamp *metav1.Time `json:"deletionTimestamp,omitempty" redact:"nonsecret"`
	// ClusterServiceDeletionTimestamp is written when a dispatch of a Cluster
	// Service Delete ExternalAuth request against Cluster Service for this
	// external auth has been handled. It is set after a successful
	// DeleteExternalAuth call to Cluster Service, but also when it's
	// determined that no delete call is needed but we consider we should
	// behave as if the delete call was successfully issued (for example, if
	// the parent cluster of the external auth is already being uninstalled,
	// because cluster-service will already take care of deleting the
	// external auth as part of the cluster teardown).
	// A nil value indicates that the Cluster Service Deletion has not been requested.
	// The timestamp is in UTC.
	// TODO this attribute is not in use yet. Do not rely on it.
	ClusterServiceDeletionTimestamp *metav1.Time `json:"clusterServiceDeletionTimestamp,omitempty" redact:"nonsecret"`

	// TODO Temporary field to track whether the external auth operation is using the new deletion approach.
	// We are migrating from the external auth CS deletion synchronous in frontend to the backend, to be fully asynchronous.
	// This boolean is true for ExternalAuth delete operations that are created with new deletion approach.
	// This will be removed once all external auths whose deletion was triggered before the new approach is fully rolled out have been
	// fully deleted in all ARO-HCP permanent environments, for all regions.
	UsesNewExternalAuthDeletionApproach bool `json:"usesNewExternalAuthDeletionApproach" redact:"nonsecret"`
}

// Token issuer profile
// This configures how the platform interacts with the identity provider and
// how tokens issued from the identity provider are evaluated by the Kubernetes API server.
// Visbility for the entire struct is "read create update".
type TokenIssuerProfile struct {
	URL       string   `json:"url" redact:"nonsecret"`
	Audiences []string `json:"audiences" redact:"nonsecret"`
	CA        string   `json:"ca" redact:"nonsecret"`
}

// External Auth client profile
// This configures how on-cluster, platform clients should request tokens from the identity provider.
// Visibility for the entire struct is "read create update".
type ExternalAuthClientProfile struct {
	Component   ExternalAuthClientComponentProfile `json:"component" redact:"nonsecret"`
	ClientID    string                             `json:"clientId" redact:"nonsecret"`
	ExtraScopes []string                           `json:"extraScopes" redact:"nonsecret"`
	Type        ExternalAuthClientType             `json:"type" redact:"nonsecret"`
}

// External Auth component profile
// Must have unique namespace/name pairs.
// Visibility for the entire struct is "read create update".
type ExternalAuthClientComponentProfile struct {
	Name                string `json:"name" redact:"nonsecret"`
	AuthClientNamespace string `json:"authClientNamespace" redact:"nonsecret"`
}

// External Auth claim profile
// Visibility for the entire struct is "read create update".
type ExternalAuthClaimProfile struct {
	Mappings        TokenClaimMappingsProfile  `json:"mappings" redact:"nonsecret"`
	ValidationRules []TokenClaimValidationRule `json:"validationRules" redact:"nonsecret"`
}

// External Auth claim mappings profile.
// At a minimum username or groups must be defined.
// Visibility for the entire struct is "read create update".
type TokenClaimMappingsProfile struct {
	Username UsernameClaimProfile `json:"username" redact:"nonsecret"`
	Groups   *GroupClaimProfile   `json:"groups" redact:"nonsecret"`
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
	Claim  string `json:"claim" redact:"nonsecret"`
	Prefix string `json:"prefix" redact:"nonsecret"`
}

// External Auth claim profile
// This configures how the username of a cluster identity should be constructed
// from the claims in a JWT token issued by the identity provider.
// Visibility for the entire struct is "read create update".
type UsernameClaimProfile struct {
	Claim        string                    `json:"claim" redact:"nonsecret"`
	Prefix       string                    `json:"prefix" redact:"nonsecret"`
	PrefixPolicy UsernameClaimPrefixPolicy `json:"prefixPolicy" redact:"nonsecret"`
}

// External Auth claim validation rule
// Visibility for the entire struct is "read create update".
type TokenClaimValidationRule struct {
	Type          TokenValidationRuleType `json:"type" redact:"nonsecret"`
	RequiredClaim TokenRequiredClaim      `json:"requiredClaim" redact:"nonsecret"`
}

// Token required claim validation rule.
// Visibility for the entire struct is "read create update".
type TokenRequiredClaim struct {
	Claim         string `json:"claim" redact:"nonsecret"`
	RequiredValue string `json:"requiredValue" redact:"nonsecret"`
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
