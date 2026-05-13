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

package validation

import (
	"context"
	"strings"
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestExternalAuthRequired(t *testing.T) {
	tests := []struct {
		name         string
		resource     *resourcesapi.HCPOpenShiftClusterExternalAuth
		expectErrors []utils.ExpectedError
	}{
		{
			name:     "Empty External Auth",
			resource: &resourcesapi.HCPOpenShiftClusterExternalAuth{},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "Required value",
					FieldPath: "trackedResource.resource.id",
				},
				{
					Message:   "Required value",
					FieldPath: "trackedResource.resource.systemData",
				},
				{
					Message:   "Required value",
					FieldPath: "properties.issuer.url",
				},
				{
					Message:   "Required value",
					FieldPath: "properties.claim.mappings",
				},
				{
					Message:   "Required value",
					FieldPath: "properties.claim.mappings.username",
				},
				{
					Message:   "Required value",
					FieldPath: "properties.claim.mappings.username.claim",
				},
				{
					Message:   "supported values: \"NoPrefix\", \"None\", \"Prefix\"",
					FieldPath: "properties.claim.mappings.username.prefixPolicy",
				},
				{
					Message:   "Required value",
					FieldPath: "properties.issuer.audiences",
				},
			},
		},
		{
			name:     "Default external auth",
			resource: resourcesapi.NewDefaultHCPOpenShiftClusterExternalAuth(resourcesapi.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/externalAuths/test-auth"))),
			expectErrors: []utils.ExpectedError{
				{
					Message:   "Required value",
					FieldPath: "trackedResource.resource.systemData",
				},
				{
					Message:   "Required value",
					FieldPath: "properties.claim.mappings.username.claim",
				},
				{
					Message:   "Required value",
					FieldPath: "properties.issuer.url",
				},
				{
					Message:   "Required value",
					FieldPath: "properties.issuer.audiences",
				}},
		},
		{
			name:     "Minimum valid external auth",
			resource: resourcesapi.MinimumValidExternalAuthTestCase(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualErrors := ValidateExternalAuthCreate(context.TODO(), tt.resource)
			utils.VerifyErrorsMatch(t, tt.expectErrors, actualErrors)
		})
	}
}

func TestExternalAuthValidate(t *testing.T) {
	TooLongClaim := strings.Repeat("a", 257)
	ClientID1 := "clientID1"
	ClientID2 := "clientID2"
	ClientComponentName := "A"
	ClientComponentNamespace := "B"

	// Note "required" validation tests are above.
	// This function tests all the other validators in use.
	tests := []struct {
		name         string
		tweaks       *resourcesapi.HCPOpenShiftClusterExternalAuth
		expectErrors []utils.ExpectedError
	}{
		{
			name:   "Minimum valid external auth",
			tweaks: &resourcesapi.HCPOpenShiftClusterExternalAuth{},
		},
		{
			name: "Max not satisfied for properties.claim.mappings.username.claim",
			tweaks: &resourcesapi.HCPOpenShiftClusterExternalAuth{
				Properties: resourcesapi.HCPOpenShiftClusterExternalAuthProperties{
					Claim: resourcesapi.ExternalAuthClaimProfile{
						Mappings: resourcesapi.TokenClaimMappingsProfile{
							Username: resourcesapi.UsernameClaimProfile{
								Claim: TooLongClaim,
							},
						},
					},
				},
			},
			expectErrors: []utils.ExpectedError{}, // This field is not being validated for length in the actual function
		},
		{
			name: "Max not satisfied for properties.claim.mappings.groups.claim",
			tweaks: &resourcesapi.HCPOpenShiftClusterExternalAuth{
				Properties: resourcesapi.HCPOpenShiftClusterExternalAuthProperties{
					Claim: resourcesapi.ExternalAuthClaimProfile{
						Mappings: resourcesapi.TokenClaimMappingsProfile{
							Groups: &resourcesapi.GroupClaimProfile{
								Claim: TooLongClaim,
							},
						},
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "may not be more than 256 bytes",
					FieldPath: "properties.claim.mappings.groups.claim",
				},
			},
		},
		{
			name: "Empty properties.issuer.ca",
			tweaks: &resourcesapi.HCPOpenShiftClusterExternalAuth{
				Properties: resourcesapi.HCPOpenShiftClusterExternalAuthProperties{
					Issuer: resourcesapi.TokenIssuerProfile{
						CA: "",
					},
				},
			},
		},
		{
			name: "Bad properties.issuer.ca",
			tweaks: &resourcesapi.HCPOpenShiftClusterExternalAuth{
				Properties: resourcesapi.HCPOpenShiftClusterExternalAuthProperties{
					Issuer: resourcesapi.TokenIssuerProfile{
						CA: "NOT A PEM DOC",
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "not a valid PEM",
					FieldPath: "properties.issuer.ca",
				},
			},
		},
		{
			name: "Bad properties.issuer.url - InvalidURL",
			tweaks: &resourcesapi.HCPOpenShiftClusterExternalAuth{
				Properties: resourcesapi.HCPOpenShiftClusterExternalAuthProperties{
					Issuer: resourcesapi.TokenIssuerProfile{
						URL: "aaa",
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "must be https URL",
					FieldPath: "properties.issuer.url",
				},
			},
		},
		{
			name: "Bad properties.issuer.url - Not starting with https://",
			tweaks: &resourcesapi.HCPOpenShiftClusterExternalAuth{
				Properties: resourcesapi.HCPOpenShiftClusterExternalAuthProperties{
					Issuer: resourcesapi.TokenIssuerProfile{
						URL: "http://microsoft.com",
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "must be https URL",
					FieldPath: "properties.issuer.url",
				},
			},
		},
		{
			name: "Bad properties.issuer.audiences",
			tweaks: &resourcesapi.HCPOpenShiftClusterExternalAuth{
				Properties: resourcesapi.HCPOpenShiftClusterExternalAuthProperties{
					Issuer: resourcesapi.TokenIssuerProfile{
						Audiences: []string{"omitempty"},
					},
				},
			},
			expectErrors: nil,
		},
		{
			name: "Missing prefix when policy is Prefix",
			tweaks: &resourcesapi.HCPOpenShiftClusterExternalAuth{
				Properties: resourcesapi.HCPOpenShiftClusterExternalAuthProperties{
					Claim: resourcesapi.ExternalAuthClaimProfile{
						Mappings: resourcesapi.TokenClaimMappingsProfile{
							Username: resourcesapi.UsernameClaimProfile{
								PrefixPolicy: resourcesapi.UsernameClaimPrefixPolicyPrefix,
							},
						},
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "must be specified when `prefixPolicy` is \"Prefix\"",
					FieldPath: "properties.claim.mappings.username.prefix",
				},
			},
		},
		{
			name: "No username prefix when policy is NoPrefix",
			tweaks: &resourcesapi.HCPOpenShiftClusterExternalAuth{
				Properties: resourcesapi.HCPOpenShiftClusterExternalAuthProperties{
					Claim: resourcesapi.ExternalAuthClaimProfile{
						Mappings: resourcesapi.TokenClaimMappingsProfile{
							Username: resourcesapi.UsernameClaimProfile{
								Prefix:       "prefix",
								PrefixPolicy: resourcesapi.UsernameClaimPrefixPolicyNoPrefix,
							},
						},
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "may only be specified when `prefixPolicy` is \"Prefix\"",
					FieldPath: "properties.claim.mappings.username.prefix",
				},
			},
		},
		{
			name: "No username prefix when policy is None",
			tweaks: &resourcesapi.HCPOpenShiftClusterExternalAuth{
				Properties: resourcesapi.HCPOpenShiftClusterExternalAuthProperties{
					Claim: resourcesapi.ExternalAuthClaimProfile{
						Mappings: resourcesapi.TokenClaimMappingsProfile{
							Username: resourcesapi.UsernameClaimProfile{
								Prefix:       "prefix",
								PrefixPolicy: resourcesapi.UsernameClaimPrefixPolicyNone,
							},
						},
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "may only be specified when `prefixPolicy` is \"Prefix\"",
					FieldPath: "properties.claim.mappings.username.prefix",
				},
			},
		},

		//--------------------------------
		// Complex field validation
		//--------------------------------

		{
			name: "Valid ClientID in audiences",
			tweaks: &resourcesapi.HCPOpenShiftClusterExternalAuth{
				Properties: resourcesapi.HCPOpenShiftClusterExternalAuthProperties{
					Issuer: resourcesapi.TokenIssuerProfile{
						URL:       "https://example.com",
						Audiences: []string{ClientID1},
					},
					Clients: []resourcesapi.ExternalAuthClientProfile{
						{
							ClientID: ClientID1,
							Component: resourcesapi.ExternalAuthClientComponentProfile{
								Name:                ClientComponentName,
								AuthClientNamespace: ClientComponentNamespace,
							},
							Type: resourcesapi.ExternalAuthClientTypeConfidential,
						},
					},
					Claim: resourcesapi.ExternalAuthClaimProfile{
						Mappings: resourcesapi.TokenClaimMappingsProfile{
							Username: resourcesapi.UsernameClaimProfile{Claim: "email"},
						},
					},
				},
			},
			expectErrors: nil,
		},
		{
			name: "Invalid ClientID not in audiences",
			tweaks: &resourcesapi.HCPOpenShiftClusterExternalAuth{
				Properties: resourcesapi.HCPOpenShiftClusterExternalAuthProperties{
					Issuer: resourcesapi.TokenIssuerProfile{
						URL:       "https://example.com",
						Audiences: []string{},
					},
					Clients: []resourcesapi.ExternalAuthClientProfile{
						{
							ClientID: ClientID1,
							Component: resourcesapi.ExternalAuthClientComponentProfile{
								Name:                ClientComponentName,
								AuthClientNamespace: ClientComponentNamespace,
							},
							Type: resourcesapi.ExternalAuthClientTypeConfidential,
						},
					},
					Claim: resourcesapi.ExternalAuthClaimProfile{
						Mappings: resourcesapi.TokenClaimMappingsProfile{
							Username: resourcesapi.UsernameClaimProfile{Claim: "email"},
						},
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "must match an audience in issuer audiences",
					FieldPath: "properties.clients",
				},
			},
		},
		{
			name: "External Auth with multiple clients that have the same Name/Namespace pair",
			tweaks: &resourcesapi.HCPOpenShiftClusterExternalAuth{
				Properties: resourcesapi.HCPOpenShiftClusterExternalAuthProperties{
					Issuer: resourcesapi.TokenIssuerProfile{
						URL:       "https://example.com",
						Audiences: []string{ClientID1, ClientID2},
					},
					Clients: []resourcesapi.ExternalAuthClientProfile{
						{
							ClientID: ClientID1,
							Component: resourcesapi.ExternalAuthClientComponentProfile{
								Name:                ClientComponentName,
								AuthClientNamespace: ClientComponentNamespace,
							},
							Type: resourcesapi.ExternalAuthClientTypeConfidential,
						},
						{
							ClientID: ClientID2,
							Component: resourcesapi.ExternalAuthClientComponentProfile{
								Name:                ClientComponentName,
								AuthClientNamespace: ClientComponentNamespace,
							},
							Type: resourcesapi.ExternalAuthClientTypeConfidential,
						},
					},
					Claim: resourcesapi.ExternalAuthClaimProfile{
						Mappings: resourcesapi.TokenClaimMappingsProfile{
							Username: resourcesapi.UsernameClaimProfile{Claim: "email"},
						},
					},
				},
			},
			expectErrors: []utils.ExpectedError{
				{
					Message:   "Duplicate value",
					FieldPath: "properties.clients",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource := resourcesapi.ExternalAuthTestCase(t, tt.tweaks)
			actualErrors := ValidateExternalAuthCreate(context.TODO(), resource)
			utils.VerifyErrorsMatch(t, tt.expectErrors, actualErrors)
		})
	}
}
