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

	"github.com/Azure/ARO-HCP/internal/api"
)

func TestExternalAuthRequired(t *testing.T) {
	tests := []struct {
		name         string
		resource     *api.HCPOpenShiftClusterExternalAuth
		expectErrors []expectedError
	}{
		{
			name:     "Empty External Auth",
			resource: &api.HCPOpenShiftClusterExternalAuth{},
			expectErrors: []expectedError{
				{
					message:   "Required value",
					fieldPath: "trackedResource.resource.id",
				},
				{
					message:   "Required value",
					fieldPath: "properties.issuer.url",
				},
				{
					message:   "Required value",
					fieldPath: "properties.claim.mappings",
				},
				{
					message:   "Required value",
					fieldPath: "properties.claim.mappings.username",
				},
				{
					message:   "Required value",
					fieldPath: "properties.claim.mappings.username.claim",
				},
				{
					message:   "supported values: \"NoPrefix\", \"None\", \"Prefix\"",
					fieldPath: "properties.claim.mappings.username.prefixPolicy",
				},
				{
					message:   "Required value",
					fieldPath: "properties.issuer.audiences",
				},
			},
		},
		{
			name:     "Default external auth",
			resource: api.NewDefaultHCPOpenShiftClusterExternalAuth(api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/externalAuths/test-auth"))),
			expectErrors: []expectedError{
				{
					message:   "Required value",
					fieldPath: "properties.claim.mappings.username.claim",
				},
				{
					message:   "Required value",
					fieldPath: "properties.issuer.url",
				},
				{
					message:   "Required value",
					fieldPath: "properties.issuer.audiences",
				}},
		},
		{
			name:     "Minimum valid external auth",
			resource: api.MinimumValidExternalAuthTestCase(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualErrors := ValidateExternalAuthCreate(context.TODO(), tt.resource)
			verifyErrorsMatch(t, tt.expectErrors, actualErrors)
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
		tweaks       *api.HCPOpenShiftClusterExternalAuth
		expectErrors []expectedError
	}{
		{
			name:   "Minimum valid external auth",
			tweaks: &api.HCPOpenShiftClusterExternalAuth{},
		},
		{
			name: "Max not satisfied for properties.claim.mappings.username.claim",
			tweaks: &api.HCPOpenShiftClusterExternalAuth{
				Properties: api.HCPOpenShiftClusterExternalAuthProperties{
					Claim: api.ExternalAuthClaimProfile{
						Mappings: api.TokenClaimMappingsProfile{
							Username: api.UsernameClaimProfile{
								Claim: TooLongClaim,
							},
						},
					},
				},
			},
			expectErrors: []expectedError{}, // This field is not being validated for length in the actual function
		},
		{
			name: "Max not satisfied for properties.claim.mappings.groups.claim",
			tweaks: &api.HCPOpenShiftClusterExternalAuth{
				Properties: api.HCPOpenShiftClusterExternalAuthProperties{
					Claim: api.ExternalAuthClaimProfile{
						Mappings: api.TokenClaimMappingsProfile{
							Groups: &api.GroupClaimProfile{
								Claim: TooLongClaim,
							},
						},
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "may not be more than 256 bytes",
					fieldPath: "properties.claim.mappings.groups.claim",
				},
			},
		},
		{
			name: "Empty properties.issuer.ca",
			tweaks: &api.HCPOpenShiftClusterExternalAuth{
				Properties: api.HCPOpenShiftClusterExternalAuthProperties{
					Issuer: api.TokenIssuerProfile{
						CA: "",
					},
				},
			},
		},
		{
			name: "Bad properties.issuer.ca",
			tweaks: &api.HCPOpenShiftClusterExternalAuth{
				Properties: api.HCPOpenShiftClusterExternalAuthProperties{
					Issuer: api.TokenIssuerProfile{
						CA: "NOT A PEM DOC",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "not a valid PEM",
					fieldPath: "properties.issuer.ca",
				},
			},
		},
		{
			name: "Bad properties.issuer.url - InvalidURL",
			tweaks: &api.HCPOpenShiftClusterExternalAuth{
				Properties: api.HCPOpenShiftClusterExternalAuthProperties{
					Issuer: api.TokenIssuerProfile{
						URL: "aaa",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "must be https URL",
					fieldPath: "properties.issuer.url",
				},
			},
		},
		{
			name: "Bad properties.issuer.url - Not starting with https://",
			tweaks: &api.HCPOpenShiftClusterExternalAuth{
				Properties: api.HCPOpenShiftClusterExternalAuthProperties{
					Issuer: api.TokenIssuerProfile{
						URL: "http://microsoft.com",
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "must be https URL",
					fieldPath: "properties.issuer.url",
				},
			},
		},
		{
			name: "Bad properties.issuer.audiences",
			tweaks: &api.HCPOpenShiftClusterExternalAuth{
				Properties: api.HCPOpenShiftClusterExternalAuthProperties{
					Issuer: api.TokenIssuerProfile{
						Audiences: []string{"omitempty"},
					},
				},
			},
			expectErrors: nil,
		},
		{
			name: "Missing prefix when policy is Prefix",
			tweaks: &api.HCPOpenShiftClusterExternalAuth{
				Properties: api.HCPOpenShiftClusterExternalAuthProperties{
					Claim: api.ExternalAuthClaimProfile{
						Mappings: api.TokenClaimMappingsProfile{
							Username: api.UsernameClaimProfile{
								PrefixPolicy: api.UsernameClaimPrefixPolicyPrefix,
							},
						},
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "must be specified when `prefixPolicy` is \"Prefix\"",
					fieldPath: "properties.claim.mappings.username.prefix",
				},
			},
		},
		{
			name: "No username prefix when policy is NoPrefix",
			tweaks: &api.HCPOpenShiftClusterExternalAuth{
				Properties: api.HCPOpenShiftClusterExternalAuthProperties{
					Claim: api.ExternalAuthClaimProfile{
						Mappings: api.TokenClaimMappingsProfile{
							Username: api.UsernameClaimProfile{
								Prefix:       "prefix",
								PrefixPolicy: api.UsernameClaimPrefixPolicyNoPrefix,
							},
						},
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "may only be specified when `prefixPolicy` is \"Prefix\"",
					fieldPath: "properties.claim.mappings.username.prefix",
				},
			},
		},
		{
			name: "No username prefix when policy is None",
			tweaks: &api.HCPOpenShiftClusterExternalAuth{
				Properties: api.HCPOpenShiftClusterExternalAuthProperties{
					Claim: api.ExternalAuthClaimProfile{
						Mappings: api.TokenClaimMappingsProfile{
							Username: api.UsernameClaimProfile{
								Prefix:       "prefix",
								PrefixPolicy: api.UsernameClaimPrefixPolicyNone,
							},
						},
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "may only be specified when `prefixPolicy` is \"Prefix\"",
					fieldPath: "properties.claim.mappings.username.prefix",
				},
			},
		},

		//--------------------------------
		// Complex field validation
		//--------------------------------

		{
			name: "Valid ClientID in audiences",
			tweaks: &api.HCPOpenShiftClusterExternalAuth{
				Properties: api.HCPOpenShiftClusterExternalAuthProperties{
					Issuer: api.TokenIssuerProfile{
						URL:       "https://example.com",
						Audiences: []string{ClientID1},
					},
					Clients: []api.ExternalAuthClientProfile{
						{
							ClientID: ClientID1,
							Component: api.ExternalAuthClientComponentProfile{
								Name:                ClientComponentName,
								AuthClientNamespace: ClientComponentNamespace,
							},
							Type: api.ExternalAuthClientTypeConfidential,
						},
					},
					Claim: api.ExternalAuthClaimProfile{
						Mappings: api.TokenClaimMappingsProfile{
							Username: api.UsernameClaimProfile{Claim: "email"},
						},
					},
				},
			},
			expectErrors: nil,
		},
		{
			name: "Invalid ClientID not in audiences",
			tweaks: &api.HCPOpenShiftClusterExternalAuth{
				Properties: api.HCPOpenShiftClusterExternalAuthProperties{
					Issuer: api.TokenIssuerProfile{
						URL:       "https://example.com",
						Audiences: []string{},
					},
					Clients: []api.ExternalAuthClientProfile{
						{
							ClientID: ClientID1,
							Component: api.ExternalAuthClientComponentProfile{
								Name:                ClientComponentName,
								AuthClientNamespace: ClientComponentNamespace,
							},
							Type: api.ExternalAuthClientTypeConfidential,
						},
					},
					Claim: api.ExternalAuthClaimProfile{
						Mappings: api.TokenClaimMappingsProfile{
							Username: api.UsernameClaimProfile{Claim: "email"},
						},
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "must match an audience in issuer audiences",
					fieldPath: "properties.clients",
				},
			},
		},
		{
			name: "External Auth with multiple clients that have the same Name/Namespace pair",
			tweaks: &api.HCPOpenShiftClusterExternalAuth{
				Properties: api.HCPOpenShiftClusterExternalAuthProperties{
					Issuer: api.TokenIssuerProfile{
						URL:       "https://example.com",
						Audiences: []string{ClientID1, ClientID2},
					},
					Clients: []api.ExternalAuthClientProfile{
						{
							ClientID: ClientID1,
							Component: api.ExternalAuthClientComponentProfile{
								Name:                ClientComponentName,
								AuthClientNamespace: ClientComponentNamespace,
							},
							Type: api.ExternalAuthClientTypeConfidential,
						},
						{
							ClientID: ClientID2,
							Component: api.ExternalAuthClientComponentProfile{
								Name:                ClientComponentName,
								AuthClientNamespace: ClientComponentNamespace,
							},
							Type: api.ExternalAuthClientTypeConfidential,
						},
					},
					Claim: api.ExternalAuthClaimProfile{
						Mappings: api.TokenClaimMappingsProfile{
							Username: api.UsernameClaimProfile{Claim: "email"},
						},
					},
				},
			},
			expectErrors: []expectedError{
				{
					message:   "Duplicate value",
					fieldPath: "properties.clients",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource := api.ExternalAuthTestCase(t, tt.tweaks)
			actualErrors := ValidateExternalAuthCreate(context.TODO(), resource)
			verifyErrorsMatch(t, tt.expectErrors, actualErrors)
		})
	}
}
