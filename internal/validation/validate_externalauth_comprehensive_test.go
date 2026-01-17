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
	"testing"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestValidateExternalAuth(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		newObj       *api.HCPOpenShiftClusterExternalAuth
		oldObj       *api.HCPOpenShiftClusterExternalAuth
		op           operation.Operation
		expectErrors []expectedError
	}{
		{
			name:         "valid external auth create",
			newObj:       createValidExternalAuth(),
			op:           operation.Operation{Type: operation.Create},
			expectErrors: nil,
		},
		{
			name: "valid external auth with multiple unique clients",
			newObj: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Issuer.Audiences = []string{"client1", "client2", "client3"}
				obj.Properties.Clients = []api.ExternalAuthClientProfile{
					{
						Component: api.ExternalAuthClientComponentProfile{
							Name:                "component1",
							AuthClientNamespace: "namespace1",
						},
						ClientID: "client1",
						Type:     api.ExternalAuthClientTypeConfidential,
					},
					{
						Component: api.ExternalAuthClientComponentProfile{
							Name:                "component2",
							AuthClientNamespace: "namespace2",
						},
						ClientID: "client2",
						Type:     api.ExternalAuthClientTypePublic,
					},
					{
						Component: api.ExternalAuthClientComponentProfile{
							Name:                "component1", // Same name but different namespace is OK
							AuthClientNamespace: "namespace3",
						},
						ClientID: "client3",
						Type:     api.ExternalAuthClientTypeConfidential,
					},
				}
				return obj
			}(),
			op:           operation.Operation{Type: operation.Create},
			expectErrors: nil,
		},
		{
			name: "valid external auth without CA certificate",
			newObj: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Issuer.CA = "" // CA is optional
				return obj
			}(),
			op:           operation.Operation{Type: operation.Create},
			expectErrors: nil,
		},
		{
			name:   "missing required issuer URL",
			newObj: createMinimalExternalAuth(),
			op:     operation.Operation{Type: operation.Create},
			expectErrors: []expectedError{
				{fieldPath: "properties.issuer.url", message: "Required value"},
				{fieldPath: "properties.issuer.audiences", message: "Required value"},
			},
		},
		{
			name: "invalid issuer URL - not HTTPS",
			newObj: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createMinimalExternalAuth()
				obj.Properties.Issuer.URL = "http://insecure.example.com"
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []expectedError{
				{fieldPath: "properties.issuer.url", message: "must be https URL"},
				{fieldPath: "properties.issuer.audiences", message: "Required value"},
			},
		},
		{
			name: "missing issuer audiences",
			newObj: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createMinimalExternalAuth()
				obj.Properties.Issuer.URL = "https://valid.example.com"
				obj.Properties.Issuer.Audiences = []string{}
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []expectedError{
				{fieldPath: "properties.issuer.audiences", message: "Required value"},
				{fieldPath: "properties.issuer.audiences", message: "must have at least 1 items"},
			},
		},
		{
			name: "too many issuer audiences",
			newObj: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createMinimalExternalAuth()
				obj.Properties.Issuer.URL = "https://valid.example.com"
				obj.Properties.Issuer.Audiences = make([]string, 11)
				for i := range obj.Properties.Issuer.Audiences {
					obj.Properties.Issuer.Audiences[i] = "audience" + string(rune('0'+i))
				}
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []expectedError{
				{fieldPath: "properties.issuer.audiences", message: "must have at most 10 items"},
			},
		},
		{
			name: "invalid CA certificate",
			newObj: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createMinimalExternalAuth()
				obj.Properties.Issuer.URL = "https://valid.example.com"
				obj.Properties.Issuer.Audiences = []string{"audience1"}
				obj.Properties.Issuer.CA = "invalid-pem"
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []expectedError{
				{fieldPath: "properties.issuer.ca", message: "not a valid PEM"},
			},
		},
		{
			name: "too many clients",
			newObj: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createMinimalExternalAuth()
				obj.Properties.Issuer.URL = "https://valid.example.com"
				obj.Properties.Issuer.Audiences = []string{"audience1"}
				obj.Properties.Clients = make([]api.ExternalAuthClientProfile, 21)
				for i := range obj.Properties.Clients {
					obj.Properties.Clients[i] = api.ExternalAuthClientProfile{
						Component: api.ExternalAuthClientComponentProfile{
							Name:                "component" + string(rune('0'+i)),
							AuthClientNamespace: "namespace" + string(rune('0'+i)),
						},
						ClientID: "audience1",
						Type:     api.ExternalAuthClientTypeConfidential,
					}
				}
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []expectedError{
				{fieldPath: "properties.clients", message: "must have at most 20 items"},
			},
		},
		{
			name: "missing client component name",
			newObj: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createMinimalExternalAuth()
				obj.Properties.Issuer.URL = "https://valid.example.com"
				obj.Properties.Issuer.Audiences = []string{"audience1"}
				obj.Properties.Issuer.CA = validCertPEM()
				obj.Properties.Clients = []api.ExternalAuthClientProfile{
					{
						Component: api.ExternalAuthClientComponentProfile{
							AuthClientNamespace: "test-namespace",
						},
						ClientID: "audience1",
						Type:     api.ExternalAuthClientTypeConfidential,
					},
				}
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []expectedError{
				{fieldPath: "properties.clients[0].component.name", message: "Required value"},
			},
		},
		{
			name: "client component name too long",
			newObj: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				longName := make([]byte, 257)
				for i := range longName {
					longName[i] = 'a'
				}
				obj.Properties.Clients[0].Component.Name = string(longName)
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []expectedError{
				{fieldPath: "properties.clients[0].component.name", message: "may not be more than 256 bytes"},
			},
		},
		{
			name: "group claim too long",
			newObj: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				longClaim := make([]byte, 257)
				for i := range longClaim {
					longClaim[i] = 'a'
				}
				obj.Properties.Claim.Mappings.Groups.Claim = string(longClaim)
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []expectedError{
				{fieldPath: "properties.claim.mappings.groups.claim", message: "may not be more than 256 bytes"},
			},
		},
		{
			name: "missing username claim",
			newObj: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.Mappings.Username.Claim = ""
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []expectedError{
				{fieldPath: "properties.claim.mappings.username.claim", message: "Required value"},
			},
		},
		{
			name: "duplicate client components (unique validation)",
			newObj: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Issuer.Audiences = []string{"client1", "client2"}
				obj.Properties.Clients = []api.ExternalAuthClientProfile{
					{
						Component: api.ExternalAuthClientComponentProfile{
							Name:                "same-component",
							AuthClientNamespace: "same-namespace",
						},
						ClientID: "client1",
						Type:     api.ExternalAuthClientTypeConfidential,
					},
					{
						Component: api.ExternalAuthClientComponentProfile{
							Name:                "same-component", // Same component name and namespace
							AuthClientNamespace: "same-namespace",
						},
						ClientID: "client2",
						Type:     api.ExternalAuthClientTypePublic,
					},
				}
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []expectedError{
				{fieldPath: "properties.clients[1]", message: "Duplicate value"},
			},
		},
		{
			name: "client ID not matching any issuer audience",
			newObj: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Issuer.Audiences = []string{"audience1", "audience2"}
				obj.Properties.Clients = []api.ExternalAuthClientProfile{
					{
						Component: api.ExternalAuthClientComponentProfile{
							Name:                "test-component",
							AuthClientNamespace: "test-namespace",
						},
						ClientID: "nonexistent-client", // This doesn't match any audience
						Type:     api.ExternalAuthClientTypeConfidential,
					},
				}
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []expectedError{
				{fieldPath: "properties.clients[0].clientId", message: "must match an audience in issuer audiences"},
			},
		},
		{
			name: "multiple clients with mismatched audiences",
			newObj: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Issuer.Audiences = []string{"audience1"}
				obj.Properties.Clients = []api.ExternalAuthClientProfile{
					{
						Component: api.ExternalAuthClientComponentProfile{
							Name:                "component1",
							AuthClientNamespace: "namespace1",
						},
						ClientID: "audience1", // This matches
						Type:     api.ExternalAuthClientTypeConfidential,
					},
					{
						Component: api.ExternalAuthClientComponentProfile{
							Name:                "component2",
							AuthClientNamespace: "namespace2",
						},
						ClientID: "bad-audience", // This doesn't match
						Type:     api.ExternalAuthClientTypePublic,
					},
				}
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []expectedError{
				{fieldPath: "properties.clients[1].clientId", message: "must match an audience in issuer audiences"},
			},
		},
		{
			name: "invalid client type",
			newObj: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Clients[0].Type = "InvalidType"
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []expectedError{
				{fieldPath: "properties.clients[0].type", message: "supported values"},
			},
		},
		{
			name: "immutable provisioning state on update",
			newObj: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.ProvisioningState = arm.ProvisioningStateSucceeded
				// Set ValidationRules to empty to avoid nil pointer in discriminated union validation
				obj.Properties.Claim.ValidationRules = []api.TokenClaimValidationRule{}
				return obj
			}(),
			oldObj: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.ProvisioningState = arm.ProvisioningStateProvisioning
				// Set ValidationRules to empty to avoid nil pointer in discriminated union validation
				obj.Properties.Claim.ValidationRules = []api.TokenClaimValidationRule{}
				return obj
			}(),
			op: operation.Operation{Type: operation.Update},
			expectErrors: []expectedError{
				{fieldPath: "properties.provisioningState", message: "field is immutable"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var errs field.ErrorList
			if tt.op.Type == operation.Create {
				errs = ValidateExternalAuthCreate(ctx, tt.newObj)
			} else {
				errs = ValidateExternalAuthUpdate(ctx, tt.newObj, tt.oldObj)
			}

			verifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

func TestValidateExternalAuthDiscriminatedUnions(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		setupObject  func() *api.HCPOpenShiftClusterExternalAuth
		expectErrors []expectedError
	}{
		{
			name: "username prefix policy - valid None with no prefix",
			setupObject: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.Mappings.Username = api.UsernameClaimProfile{
					Claim:        "sub",
					PrefixPolicy: api.UsernameClaimPrefixPolicyNone,
				}
				return obj
			},
			expectErrors: nil,
		},
		{
			name: "username prefix policy - valid NoPrefix with no prefix",
			setupObject: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.Mappings.Username = api.UsernameClaimProfile{
					Claim:        "sub",
					PrefixPolicy: api.UsernameClaimPrefixPolicyNoPrefix,
				}
				return obj
			},
			expectErrors: nil,
		},
		{
			name: "username prefix policy - valid Prefix with prefix",
			setupObject: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.Mappings.Username = api.UsernameClaimProfile{
					Claim:        "sub",
					Prefix:       "custom:",
					PrefixPolicy: api.UsernameClaimPrefixPolicyPrefix,
				}
				return obj
			},
			expectErrors: nil,
		},
		{
			name: "username prefix policy - invalid None with prefix (discriminated union violation)",
			setupObject: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.Mappings.Username = api.UsernameClaimProfile{
					Claim:        "sub",
					Prefix:       "custom:",
					PrefixPolicy: api.UsernameClaimPrefixPolicyNone,
				}
				return obj
			},
			expectErrors: []expectedError{
				{fieldPath: "properties.claim.mappings.username.prefix", message: "may only be specified when `prefixPolicy` is \"Prefix\""},
			},
		},
		{
			name: "username prefix policy - invalid Prefix without prefix (discriminated union violation)",
			setupObject: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.Mappings.Username = api.UsernameClaimProfile{
					Claim:        "sub",
					PrefixPolicy: api.UsernameClaimPrefixPolicyPrefix,
				}
				return obj
			},
			expectErrors: []expectedError{
				{fieldPath: "properties.claim.mappings.username.prefix", message: "must be specified when `prefixPolicy` is \"Prefix\""},
			},
		},
		{
			name: "token validation rule - valid RequiredClaim with claim",
			setupObject: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.ValidationRules = []api.TokenClaimValidationRule{
					{
						Type: api.TokenValidationRuleTypeRequiredClaim,
						RequiredClaim: api.TokenRequiredClaim{
							Claim:         "iss",
							RequiredValue: "https://valid.example.com",
						},
					},
				}
				return obj
			},
			expectErrors: nil,
		},
		{
			name: "token validation rule - invalid RequiredClaim without claim (discriminated union violation)",
			setupObject: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.ValidationRules = []api.TokenClaimValidationRule{
					{
						Type: api.TokenValidationRuleTypeRequiredClaim,
					},
				}
				return obj
			},
			expectErrors: []expectedError{
				{fieldPath: "properties.claim.validationRules[0].requiredClaim", message: "must be specified when `type` is \"RequiredClaim\""},
				{fieldPath: "properties.claim.validationRules[0].requiredClaim.claim", message: "Required value"},
				{fieldPath: "properties.claim.validationRules[0].requiredClaim.requiredValue", message: "Required value"},
			},
		},
		{
			name: "token validation rule - invalid RequiredClaim with empty claim field",
			setupObject: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.ValidationRules = []api.TokenClaimValidationRule{
					{
						Type: api.TokenValidationRuleTypeRequiredClaim,
						RequiredClaim: api.TokenRequiredClaim{
							Claim:         "", // Empty claim should be rejected
							RequiredValue: "https://valid.example.com",
						},
					},
				}
				return obj
			},
			expectErrors: []expectedError{
				{fieldPath: "properties.claim.validationRules[0].requiredClaim.claim", message: "Required value"},
			},
		},
		{
			name: "token validation rule - invalid RequiredClaim with empty required value",
			setupObject: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.ValidationRules = []api.TokenClaimValidationRule{
					{
						Type: api.TokenValidationRuleTypeRequiredClaim,
						RequiredClaim: api.TokenRequiredClaim{
							Claim:         "iss",
							RequiredValue: "", // Empty required value should be rejected
						},
					},
				}
				return obj
			},
			expectErrors: []expectedError{
				{fieldPath: "properties.claim.validationRules[0].requiredClaim.requiredValue", message: "Required value"},
			},
		},
		{
			name: "username prefix policy - invalid NoPrefix with non-empty prefix (discriminated union violation)",
			setupObject: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.Mappings.Username = api.UsernameClaimProfile{
					Claim:        "sub",
					Prefix:       "should-not-be-set:", // Prefix should not be set when PrefixPolicy is NoPrefix
					PrefixPolicy: api.UsernameClaimPrefixPolicyNoPrefix,
				}
				return obj
			},
			expectErrors: []expectedError{
				{fieldPath: "properties.claim.mappings.username.prefix", message: "may only be specified when `prefixPolicy` is \"Prefix\""},
			},
		},
		{
			name: "username prefix policy - invalid empty prefixPolicy",
			setupObject: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.Mappings.Username = api.UsernameClaimProfile{
					Claim:        "sub",
					PrefixPolicy: "", // Empty prefixPolicy should be rejected
				}
				return obj
			},
			expectErrors: []expectedError{
				{fieldPath: "properties.claim.mappings.username.prefixPolicy", message: "supported values"},
			},
		},
		{
			name: "token validation rule - invalid empty type",
			setupObject: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.ValidationRules = []api.TokenClaimValidationRule{
					{
						Type: "", // Empty type should be rejected
						RequiredClaim: api.TokenRequiredClaim{
							Claim:         "iss",
							RequiredValue: "https://valid.example.com",
						},
					},
				}
				return obj
			},
			expectErrors: []expectedError{
				{fieldPath: "properties.claim.validationRules[0].type", message: "supported values"},
				{fieldPath: "properties.claim.validationRules[0].requiredClaim", message: "may only be specified when `type` is \"RequiredClaim\""},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := tt.setupObject()
			errs := ValidateExternalAuthCreate(ctx, obj)
			verifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

func TestValidateExternalAuthCustomValidation(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		newObj       *api.HCPOpenShiftClusterExternalAuth
		expectErrors []expectedError
	}{
		{
			name: "client ID matches audience",
			newObj: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Issuer.Audiences = []string{"client1", "client2"}
				obj.Properties.Clients = []api.ExternalAuthClientProfile{
					{
						Component: api.ExternalAuthClientComponentProfile{
							Name:                "component1",
							AuthClientNamespace: "namespace1",
						},
						ClientID: "client1",
						Type:     api.ExternalAuthClientTypeConfidential,
					},
				}
				return obj
			}(),
			expectErrors: nil,
		},
		{
			name: "client ID does not match any audience",
			newObj: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Issuer.Audiences = []string{"audience1", "audience2"}
				obj.Properties.Clients = []api.ExternalAuthClientProfile{
					{
						Component: api.ExternalAuthClientComponentProfile{
							Name:                "component1",
							AuthClientNamespace: "namespace1",
						},
						ClientID: "nonexistent-client",
						Type:     api.ExternalAuthClientTypeConfidential,
					},
				}
				return obj
			}(),
			expectErrors: []expectedError{
				{fieldPath: "properties.clients[0]", message: "must match an audience in issuer audiences"},
			},
		},
		{
			name: "unique client identifiers",
			newObj: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Issuer.Audiences = []string{"client1", "client2"}
				obj.Properties.Clients = []api.ExternalAuthClientProfile{
					{
						Component: api.ExternalAuthClientComponentProfile{
							Name:                "component1",
							AuthClientNamespace: "namespace1",
						},
						ClientID: "client1",
						Type:     api.ExternalAuthClientTypeConfidential,
					},
					{
						Component: api.ExternalAuthClientComponentProfile{
							Name:                "component2",
							AuthClientNamespace: "namespace2",
						},
						ClientID: "client2",
						Type:     api.ExternalAuthClientTypePublic,
					},
				}
				return obj
			}(),
			expectErrors: nil,
		},
		{
			name: "duplicate client identifiers",
			newObj: func() *api.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Issuer.Audiences = []string{"client1"}
				obj.Properties.Clients = []api.ExternalAuthClientProfile{
					{
						Component: api.ExternalAuthClientComponentProfile{
							Name:                "same-component",
							AuthClientNamespace: "same-namespace",
						},
						ClientID: "client1-a",
						Type:     api.ExternalAuthClientTypeConfidential,
					},
					{
						Component: api.ExternalAuthClientComponentProfile{
							Name:                "same-component",
							AuthClientNamespace: "same-namespace",
						},
						ClientID: "client1-b",
						Type:     api.ExternalAuthClientTypePublic,
					},
				}
				return obj
			}(),
			expectErrors: []expectedError{
				{fieldPath: "properties.clients[1]", message: "Duplicate value"},
				{fieldPath: "properties.clients[0].clientId", message: "must match an audience in issuer audiences"},
				{fieldPath: "properties.clients[1].clientId", message: `Invalid value: "client1-b": must match an audience in issuer audiences`},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateExternalAuthCreate(ctx, tt.newObj)
			verifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

func createMinimalExternalAuth() *api.HCPOpenShiftClusterExternalAuth {
	resourceID, _ := azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/externalAuths/test-auth")
	obj := api.NewDefaultHCPOpenShiftClusterExternalAuth(resourceID)
	obj.Properties.Claim.Mappings.Username.Claim = "sub"
	return obj
}

func createValidExternalAuth() *api.HCPOpenShiftClusterExternalAuth {
	return &api.HCPOpenShiftClusterExternalAuth{
		ProxyResource: arm.ProxyResource{
			Resource: arm.Resource{
				ID:   api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/externalAuths/test-auth")),
				Name: "test-auth",
				Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters/externalAuths",
			},
		},
		Properties: api.HCPOpenShiftClusterExternalAuthProperties{
			Issuer: api.TokenIssuerProfile{
				URL:       "https://issuer.example.com",
				Audiences: []string{"audience1", "audience2"},
				CA:        validCertPEM(),
			},
			Clients: []api.ExternalAuthClientProfile{
				{
					Component: api.ExternalAuthClientComponentProfile{
						Name:                "test-component",
						AuthClientNamespace: "test-namespace",
					},
					ClientID: "audience1",
					Type:     api.ExternalAuthClientTypeConfidential,
				},
			},
			Claim: api.ExternalAuthClaimProfile{
				Mappings: api.TokenClaimMappingsProfile{
					Username: api.UsernameClaimProfile{
						Claim:        "sub",
						PrefixPolicy: api.UsernameClaimPrefixPolicyNone,
					},
					Groups: &api.GroupClaimProfile{
						Claim: "groups",
					},
				},
				ValidationRules: []api.TokenClaimValidationRule{
					{
						Type: api.TokenValidationRuleTypeRequiredClaim,
						RequiredClaim: api.TokenRequiredClaim{
							Claim:         "iss",
							RequiredValue: "https://issuer.example.com",
						},
					},
				},
			},
		},
	}
}

func validCertPEM() string {
	return `-----BEGIN CERTIFICATE-----
MIICMzCCAZygAwIBAgIJALiPnVsvq8dsMA0GCSqGSIb3DQEBBQUAMFMxCzAJBgNV
BAYTAlVTMQwwCgYDVQQIEwNmb28xDDAKBgNVBAcTA2ZvbzEMMAoGA1UEChMDZm9v
MQwwCgYDVQQLEwNmb28xDDAKBgNVBAMTA2ZvbzAeFw0xMzAzMTkxNTQwMTlaFw0x
ODAzMTgxNTQwMTlaMFMxCzAJBgNVBAYTAlVTMQwwCgYDVQQIEwNmb28xDDAKBgNV
BAcTA2ZvbzEMMAoGA1UEChMDZm9vMQwwCgYDVQQLEwNmb28xDDAKBgNVBAMTA2Zv
bzCBnzANBgkqhkiG9w0BAQEFAAOBjQAwgYkCgYEAzdGfxi9CNbMf1UUcvDQh7MYB
OveIHyc0E0KIbhjK5FkCBU4CiZrbfHagaW7ZEcN0tt3EvpbOMxxc/ZQU2WN/s/wP
xph0pSfsfFsTKM4RhTWD2v4fgk+xZiKd1p0+L4hTtpwnEw0uXRVd0ki6muwV5y/P
+5FHUeldq+pgTcgzuK8CAwEAAaMPMA0wCwYDVR0PBAQDAgLkMA0GCSqGSIb3DQEB
BQUAA4GBAJiDAAtY0mQQeuxWdzLRzXmjvdSuL9GoyT3BF/jSnpxz5/58dba8pWen
v3pj4P3w5DoOso0rzkZy2jEsEitlVM2mLSbQpMM+MUVQCQoiG6W9xuCFuxSrwPIS
pAqEAuV4DNoxQKKWmhVv+J0ptMWD25Pnpxeq5sXzghfJnslJlQND
-----END CERTIFICATE-----`
}
