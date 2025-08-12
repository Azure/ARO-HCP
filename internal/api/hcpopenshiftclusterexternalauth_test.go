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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestExternalAuthRequiredForPut(t *testing.T) {
	tests := []struct {
		name         string
		resource     *HCPOpenShiftClusterExternalAuth
		expectErrors []arm.CloudErrorBody
	}{
		{
			name:     "Empty External Auth",
			resource: &HCPOpenShiftClusterExternalAuth{},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Missing required field 'properties'",
					Target:  "properties",
				},
			},
		},
		{
			name:     "Default external auth",
			resource: NewDefaultHCPOpenShiftClusterExternalAuth(),
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Missing required field 'properties'",
					Target:  "properties",
				},
			},
		},
		{
			name:     "Minimum valid external auth",
			resource: MinimumValidExternalAuthTestCase(),
		},
	}

	validate := NewTestValidator()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPut, "localhost", nil)
			require.NoError(t, err)

			actualErrors := ValidateRequest(validate, request, tt.resource)

			// from hcpopenshiftcluster_test.go
			diff := compareErrors(tt.expectErrors, actualErrors)
			if diff != "" {
				t.Fatalf("Expected error mismatch:\n%s", diff)
			}
		})
	}
}

func TestExternalAuthValidate(t *testing.T) {
	TooLongClaim := strings.Repeat("a", 257)
	ClientId1 := "clientId1"
	ClientId2 := "clientId2"
	ClientComponentName := "A"
	ClientComponentNamespace := "B"

	// Note "required_for_put" validation tests are above.
	// This function tests all the other validators in use.
	tests := []struct {
		name         string
		tweaks       *HCPOpenShiftClusterExternalAuth
		expectErrors []arm.CloudErrorBody
	}{
		{
			name:   "Minimum valid node pool",
			tweaks: &HCPOpenShiftClusterExternalAuth{},
		},
		{
			name: "Max not satisfied for properties.claim.mappings.username.claim",
			tweaks: &HCPOpenShiftClusterExternalAuth{
				Properties: HCPOpenShiftClusterExternalAuthProperties{
					Claim: ExternalAuthClaimProfile{
						Mappings: TokenClaimMappingsProfile{
							Username: UsernameClaimProfile{
								Claim: TooLongClaim,
							},
						},
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Code:    "InvalidRequestContent",
					Message: fmt.Sprintf("Invalid value '%s' for field 'claim' (maximum length is 256)", TooLongClaim),
					Target:  "properties.claim.mappings.username.claim",
				},
			},
		},
		{
			name: "Max not satisfied for properties.claim.mappings.groups.claim",
			tweaks: &HCPOpenShiftClusterExternalAuth{
				Properties: HCPOpenShiftClusterExternalAuthProperties{
					Claim: ExternalAuthClaimProfile{
						Mappings: TokenClaimMappingsProfile{
							Groups: &GroupClaimProfile{
								Claim: TooLongClaim,
							},
						},
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Code:    "InvalidRequestContent",
					Message: fmt.Sprintf("Invalid value '%s' for field 'claim' (maximum length is 256)", TooLongClaim),
					Target:  "properties.claim.mappings.groups.claim",
				},
			},
		},
		{
			name: "Bad properties.issuer.ca",
			tweaks: &HCPOpenShiftClusterExternalAuth{
				Properties: HCPOpenShiftClusterExternalAuthProperties{
					Issuer: TokenIssuerProfile{
						Ca: Ptr("NOT A PEM DOC"),
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid value 'NOT A PEM DOC' for field 'ca' (must provide PEM encoded certificates)",
					Target:  "properties.issuer.ca",
				},
			},
		},
		{
			name: "Bad properties.issuer.url - InvalidURL",
			tweaks: &HCPOpenShiftClusterExternalAuth{
				Properties: HCPOpenShiftClusterExternalAuthProperties{
					Issuer: TokenIssuerProfile{
						Url: Ptr("aaa"),
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid value 'aaa' for field 'url' (must be a URL)",
					Target:  "properties.issuer.url",
				},
			},
		},
		{
			name: "Bad properties.issuer.url - Not  starting with https://",
			tweaks: &HCPOpenShiftClusterExternalAuth{
				Properties: HCPOpenShiftClusterExternalAuthProperties{
					Issuer: TokenIssuerProfile{
						Url: Ptr("http://microsoft.com"),
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid value 'http://microsoft.com' for field 'url' (must start with 'https://')",
					Target:  "properties.issuer.url",
				},
			},
		},
		{
			name: "Bad properties.issuer.audiences",
			tweaks: &HCPOpenShiftClusterExternalAuth{
				Properties: HCPOpenShiftClusterExternalAuthProperties{
					Issuer: TokenIssuerProfile{
						Audiences: []string{"omitempty"},
					},
				},
			},
			expectErrors: nil,
		},
		//--------------------------------
		// Complex field validation
		//--------------------------------
		{
			name: "Valid ClientId in audiences",
			tweaks: &HCPOpenShiftClusterExternalAuth{
				Properties: HCPOpenShiftClusterExternalAuthProperties{
					Issuer: TokenIssuerProfile{
						Url:       Ptr("https://example.com"),
						Audiences: []string{ClientId1},
					},
					Clients: []ExternalAuthClientProfile{
						{
							ClientId: ClientId1,
							Component: ExternalAuthClientComponentProfile{
								Name:                ClientComponentName,
								AuthClientNamespace: ClientComponentNamespace,
							},
							ExternalAuthClientProfileType: "confidential",
						},
					},
					Claim: ExternalAuthClaimProfile{
						Mappings: TokenClaimMappingsProfile{
							Username: UsernameClaimProfile{Claim: "email"},
						},
					},
				},
			},
			expectErrors: nil,
		},
		{
			name: "Invalid ClientId not in audiences",
			tweaks: &HCPOpenShiftClusterExternalAuth{
				Properties: HCPOpenShiftClusterExternalAuthProperties{
					Issuer: TokenIssuerProfile{
						Url:       Ptr("https://example.com"),
						Audiences: []string{},
					},
					Clients: []ExternalAuthClientProfile{
						{
							ClientId: ClientId1,
							Component: ExternalAuthClientComponentProfile{
								Name:                ClientComponentName,
								AuthClientNamespace: ClientComponentNamespace,
							},
							ExternalAuthClientProfileType: "confidential",
						},
					},
					Claim: ExternalAuthClaimProfile{
						Mappings: TokenClaimMappingsProfile{
							Username: UsernameClaimProfile{Claim: "email"},
						},
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Code:    "InvalidRequestContent",
					Message: fmt.Sprintf("ClientId '%s' in clients[0] must match an audience in TokenIssuerProfile", ClientId1),
					Target:  "properties.clients",
				},
			},
		},
		{
			name: "External Auth with multiple clients that have the same Name/Namespace pair",
			tweaks: &HCPOpenShiftClusterExternalAuth{
				Properties: HCPOpenShiftClusterExternalAuthProperties{
					Issuer: TokenIssuerProfile{
						Url:       Ptr("https://example.com"),
						Audiences: []string{ClientId1, ClientId2},
					},
					Clients: []ExternalAuthClientProfile{
						{
							ClientId: ClientId1,
							Component: ExternalAuthClientComponentProfile{
								Name:                ClientComponentName,
								AuthClientNamespace: ClientComponentNamespace,
							},
							ExternalAuthClientProfileType: "confidential",
						},
						{
							ClientId: ClientId2,
							Component: ExternalAuthClientComponentProfile{
								Name:                ClientComponentName,
								AuthClientNamespace: ClientComponentNamespace,
							},
							ExternalAuthClientProfileType: "confidential",
						},
					},
					Claim: ExternalAuthClaimProfile{
						Mappings: TokenClaimMappingsProfile{
							Username: UsernameClaimProfile{Claim: "email"},
						},
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: fmt.Sprintf(
						"External Auth Clients must have a unique combination of component.Name & component.AuthClientNamespace. "+
							"The following clientIds share the same unique combination '%s%s' and are invalid: \n '[%s %s]' ",
						ClientComponentName, ClientComponentNamespace, ClientId1, ClientId2,
					),
					Target: "properties.clients",
				},
			},
		},
		{
			name: "Invalid UsernamePrefixPolicy - A Policy of Prefix but none is set",
			tweaks: &HCPOpenShiftClusterExternalAuth{
				Properties: HCPOpenShiftClusterExternalAuthProperties{
					Claim: ExternalAuthClaimProfile{
						Mappings: TokenClaimMappingsProfile{
							Username: UsernameClaimProfile{
								Claim:        "email",
								Prefix:       "",
								PrefixPolicy: "Prefix",
							},
						},
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Code:    "InvalidRequestContent",
					Message: "UsernameClaimProfile has a PrefixPolicy of 'Prefix' but Username.Prefix is unset",
					Target:  "properties.claim.mappings.username.prefix",
				},
			},
		},
	}

	validate := NewTestValidator()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource := ExternalAuthTestCase(t, tt.tweaks)

			actualErrors := resource.Validate(validate, nil)

			// from hcpopenshiftcluster_test.go
			diff := compareErrors(tt.expectErrors, actualErrors)
			if diff != "" {
				t.Fatalf("Expected error mismatch:\n%s", diff)
			}

			for _, e := range actualErrors {
				AssertJSONPath[HCPOpenShiftClusterExternalAuth](t, e.Target)
			}
		})
	}
}
