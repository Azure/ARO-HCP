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
	"fmt"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"sync"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestValidateExternalAuth(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		newObj       *coreapi.HCPOpenShiftClusterExternalAuth
		oldObj       *coreapi.HCPOpenShiftClusterExternalAuth
		op           operation.Operation
		expectErrors []utils.ExpectedError
	}{
		{
			name:         "valid external auth create",
			newObj:       createValidExternalAuth(),
			op:           operation.Operation{Type: operation.Create},
			expectErrors: nil,
		},
		{
			name: "valid external auth with console confidential client",
			newObj: testExternalAuthWithClients(
				[]string{"console-client"},
				testExternalAuthClientProfile(
					coreapi.ExternalAuthConsoleClientComponentName,
					coreapi.ExternalAuthConsoleClientComponentNamespace,
					"console-client",
					metadataapi.ExternalAuthClientTypeConfidential,
				),
			),
			op:           operation.Operation{Type: operation.Create},
			expectErrors: nil,
		},
		{
			name: "valid external auth with multiple unique clients",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Issuer.Audiences = []string{"client1", "client2", "client3"}
				obj.Properties.Clients = []coreapi.ExternalAuthClientProfile{
					{
						Component: coreapi.ExternalAuthClientComponentProfile{
							Name:                "component1",
							AuthClientNamespace: "namespace1",
						},
						ClientID: "client1",
						Type:     metadataapi.ExternalAuthClientTypePublic,
					},
					{
						Component: coreapi.ExternalAuthClientComponentProfile{
							Name:                "component2",
							AuthClientNamespace: "namespace2",
						},
						ClientID: "client2",
						Type:     metadataapi.ExternalAuthClientTypePublic,
					},
					{
						Component: coreapi.ExternalAuthClientComponentProfile{
							Name:                "component1", // Same name but different namespace is OK
							AuthClientNamespace: "namespace3",
						},
						ClientID: "client3",
						Type:     metadataapi.ExternalAuthClientTypePublic,
					},
				}
				return obj
			}(),
			op:           operation.Operation{Type: operation.Create},
			expectErrors: nil,
		},
		{
			name: "valid external auth without CA certificate",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
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
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.issuer.url", Message: "Required value"},
				{FieldPath: "properties.issuer.audiences", Message: "Required value"},
			},
		},
		{
			name: "invalid issuer URL - not HTTPS",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createMinimalExternalAuth()
				obj.Properties.Issuer.URL = "http://insecure.example.com"
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.issuer.url", Message: "must be https URL"},
				{FieldPath: "properties.issuer.audiences", Message: "Required value"},
			},
		},
		{
			name: "missing issuer audiences",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createMinimalExternalAuth()
				obj.Properties.Issuer.URL = "https://valid.example.com"
				obj.Properties.Issuer.Audiences = []string{}
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.issuer.audiences", Message: "Required value"},
				{FieldPath: "properties.issuer.audiences", Message: "must have at least 1 items"},
			},
		},
		{
			name: "too many issuer audiences",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createMinimalExternalAuth()
				obj.Properties.Issuer.URL = "https://valid.example.com"
				obj.Properties.Issuer.Audiences = make([]string, 11)
				for i := range obj.Properties.Issuer.Audiences {
					obj.Properties.Issuer.Audiences[i] = "audience" + string(rune('0'+i))
				}
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.issuer.audiences", Message: "must have at most 10 items"},
			},
		},
		{
			name: "empty issuer audience",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createMinimalExternalAuth()
				obj.Properties.Issuer.URL = "https://valid.example.com"
				obj.Properties.Issuer.Audiences = []string{""}
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.issuer.audiences[0]", Message: "Required value"},
			},
		},
		{
			name: "empty issuer audience among valid audiences",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createMinimalExternalAuth()
				obj.Properties.Issuer.URL = "https://valid.example.com"
				obj.Properties.Issuer.Audiences = []string{"audience1", ""}
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.issuer.audiences[1]", Message: "Required value"},
			},
		},
		{
			name: "multiple empty issuer audiences",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createMinimalExternalAuth()
				obj.Properties.Issuer.URL = "https://valid.example.com"
				obj.Properties.Issuer.Audiences = []string{"", ""}
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.issuer.audiences[0]", Message: "Required value"},
				{FieldPath: "properties.issuer.audiences[1]", Message: "Required value"},
			},
		},
		{
			name: "invalid CA certificate",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createMinimalExternalAuth()
				obj.Properties.Issuer.URL = "https://valid.example.com"
				obj.Properties.Issuer.Audiences = []string{"audience1"}
				obj.Properties.Issuer.CA = "invalid-pem"
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.issuer.ca", Message: "not a valid PEM"},
			},
		},
		{
			name: "CA PEM with private key is rejected",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createMinimalExternalAuth()
				obj.Properties.Issuer.URL = "https://valid.example.com"
				obj.Properties.Issuer.Audiences = []string{"audience1"}
				obj.Properties.Issuer.CA = mustGenerateTestPrivateKeyPEM(t)
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.issuer.ca", Message: "not a valid PEM"},
			},
		},
		{
			name: "non-CA certificate is rejected",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createMinimalExternalAuth()
				obj.Properties.Issuer.URL = "https://valid.example.com"
				obj.Properties.Issuer.Audiences = []string{"audience1"}
				obj.Properties.Issuer.CA = mustGenerateTestCertPEM(t, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour), false)
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.issuer.ca", Message: "must be a CA certificate"},
			},
		},
		{
			name: "expired CA certificate is rejected",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createMinimalExternalAuth()
				obj.Properties.Issuer.URL = "https://valid.example.com"
				obj.Properties.Issuer.Audiences = []string{"audience1"}
				obj.Properties.Issuer.CA = mustGenerateTestCertPEM(t, time.Now().Add(-48*time.Hour), time.Now().Add(-time.Hour), true)
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.issuer.ca", Message: "has expired"},
			},
		},
		{
			name: "not-yet-valid CA certificate is rejected",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createMinimalExternalAuth()
				obj.Properties.Issuer.URL = "https://valid.example.com"
				obj.Properties.Issuer.Audiences = []string{"audience1"}
				obj.Properties.Issuer.CA = mustGenerateTestCertPEM(t, time.Now().Add(time.Hour), time.Now().Add(24*time.Hour), true)
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.issuer.ca", Message: "is not yet valid"},
			},
		},
		{
			name: "too many clients",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createMinimalExternalAuth()
				obj.Properties.Issuer.URL = "https://valid.example.com"
				obj.Properties.Issuer.Audiences = []string{"audience1"}
				obj.Properties.Clients = make([]coreapi.ExternalAuthClientProfile, 21)
				for i := range obj.Properties.Clients {
					obj.Properties.Clients[i] = coreapi.ExternalAuthClientProfile{
						Component: coreapi.ExternalAuthClientComponentProfile{
							Name:                "component" + string(rune('0'+i)),
							AuthClientNamespace: "namespace" + string(rune('0'+i)),
						},
						ClientID: "audience1",
						Type:     metadataapi.ExternalAuthClientTypePublic,
					}
				}
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.clients", Message: "must have at most 20 items"},
			},
		},
		{
			name: "missing client component name",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createMinimalExternalAuth()
				obj.Properties.Issuer.URL = "https://valid.example.com"
				obj.Properties.Issuer.Audiences = []string{"audience1"}
				obj.Properties.Issuer.CA = validCertPEM()
				obj.Properties.Clients = []coreapi.ExternalAuthClientProfile{
					{
						Component: coreapi.ExternalAuthClientComponentProfile{
							AuthClientNamespace: "test-namespace",
						},
						ClientID: "audience1",
						Type:     metadataapi.ExternalAuthClientTypePublic,
					},
				}
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.clients[0].component.name", Message: "Required value"},
			},
		},
		{
			name: "client component name too long",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				longName := make([]byte, 257)
				for i := range longName {
					longName[i] = 'a'
				}
				obj.Properties.Clients[0].Component.Name = string(longName)
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.clients[0].component.name", Message: "may not be more than 256 bytes"},
			},
		},
		{
			name: "username claim too long",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				longClaim := make([]byte, 257)
				for i := range longClaim {
					longClaim[i] = 'a'
				}
				obj.Properties.Claim.Mappings.Username.Claim = string(longClaim)
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.claim.mappings.username.claim", Message: "may not be more than 256 bytes"},
			},
		},
		{
			name: "group claim too long",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				longClaim := make([]byte, 257)
				for i := range longClaim {
					longClaim[i] = 'a'
				}
				obj.Properties.Claim.Mappings.Groups.Claim = string(longClaim)
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.claim.mappings.groups.claim", Message: "may not be more than 256 bytes"},
			},
		},
		{
			name: "missing username claim",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.Mappings.Username.Claim = ""
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.claim.mappings.username.claim", Message: "Required value"},
			},
		},
		{
			name: "duplicate client components (unique validation)",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Issuer.Audiences = []string{"client1", "client2"}
				obj.Properties.Clients = []coreapi.ExternalAuthClientProfile{
					{
						Component: coreapi.ExternalAuthClientComponentProfile{
							Name:                "same-component",
							AuthClientNamespace: "same-namespace",
						},
						ClientID: "client1",
						Type:     metadataapi.ExternalAuthClientTypePublic,
					},
					{
						Component: coreapi.ExternalAuthClientComponentProfile{
							Name:                "same-component", // Same component name and namespace
							AuthClientNamespace: "same-namespace",
						},
						ClientID: "client2",
						Type:     metadataapi.ExternalAuthClientTypePublic,
					},
				}
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.clients[1]", Message: "Duplicate value"},
			},
		},
		{
			name: "client ID not matching any issuer audience",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Issuer.Audiences = []string{"audience1", "audience2"}
				obj.Properties.Clients = []coreapi.ExternalAuthClientProfile{
					{
						Component: coreapi.ExternalAuthClientComponentProfile{
							Name:                "test-component",
							AuthClientNamespace: "test-namespace",
						},
						ClientID: "nonexistent-client", // This doesn't match any audience
						Type:     metadataapi.ExternalAuthClientTypePublic,
					},
				}
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.clients[0].clientId", Message: "must match an audience in issuer audiences"},
			},
		},
		{
			name: "multiple clients with mismatched audiences",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Issuer.Audiences = []string{"audience1"}
				obj.Properties.Clients = []coreapi.ExternalAuthClientProfile{
					{
						Component: coreapi.ExternalAuthClientComponentProfile{
							Name:                "component1",
							AuthClientNamespace: "namespace1",
						},
						ClientID: "audience1", // This matches
						Type:     metadataapi.ExternalAuthClientTypePublic,
					},
					{
						Component: coreapi.ExternalAuthClientComponentProfile{
							Name:                "component2",
							AuthClientNamespace: "namespace2",
						},
						ClientID: "bad-audience", // This doesn't match
						Type:     metadataapi.ExternalAuthClientTypePublic,
					},
				}
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.clients[1].clientId", Message: "must match an audience in issuer audiences"},
			},
		},
		{
			name: "invalid client type",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Clients[0].Type = "InvalidType"
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.clients[0].type", Message: "supported values"},
			},
		},
		{
			name: "valid client extraScopes",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Clients[0].ExtraScopes = []string{"email", "profile"}
				return obj
			}(),
			op:           operation.Operation{Type: operation.Create},
			expectErrors: nil,
		},
		{
			name: "empty client extraScope",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Clients[0].ExtraScopes = []string{""}
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.clients[0].extraScopes[0]", Message: "Required value"},
			},
		},
		{
			name: "empty client extraScope among valid extraScopes",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Clients[0].ExtraScopes = []string{"email", ""}
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.clients[0].extraScopes[1]", Message: "Required value"},
			},
		},
		{
			name: "multiple empty client extraScopes",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Clients[0].ExtraScopes = []string{"", ""}
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.clients[0].extraScopes[0]", Message: "Required value"},
				{FieldPath: "properties.clients[0].extraScopes[1]", Message: "Required value"},
			},
		},
		{
			name: "invalid external auth resource name - empty",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.ID.Name = ""
				obj.Name = ""
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "trackedResource.resource.id", Message: "resource name is required"},
				{FieldPath: "id", Message: "resource name is required"},
			},
		},
		{
			name: "invalid external auth resource name - special character",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.ID.Name = "$"
				obj.Name = "$"
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "id", Message: "must be a valid DNS RFC 1035 label"},
			},
		},
		{
			name: "invalid external auth resource name - starts with hyphen",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.ID.Name = "-abcde"
				obj.Name = "-abcde"
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "id", Message: "must be a valid DNS RFC 1035 label"},
			},
		},
		{
			name: "invalid external auth resource name - starts with number",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.ID.Name = "1externalauth"
				obj.Name = "1externalauth"
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "id", Message: "must be a valid DNS RFC 1035 label"},
			},
		},
		{
			name: "invalid external auth resource name - ends with hyphen",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.ID.Name = "my-auth-"
				obj.Name = "my-auth-"
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "id", Message: "must be a valid DNS RFC 1035 label"},
			},
		},
		{
			name: "invalid external auth resource name - too long",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				long := "07B4gc00vjA2C8KL3Ns4No9fi"
				obj.ID.Name = long
				obj.Name = long
				return obj
			}(),
			op: operation.Operation{Type: operation.Create},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "id", Message: "may not be more than 15 bytes"},
				{FieldPath: "id", Message: "must be a valid DNS RFC 1035 label"},
			},
		},
		{
			name: "valid external auth resource name - minimum length",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.ID.Name = "a"
				obj.Name = "a"
				return obj
			}(),
			op:           operation.Operation{Type: operation.Create},
			expectErrors: nil,
		},
		{
			name: "valid external auth resource name - with hyphens",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.ID.Name = "my-auth-1"
				obj.Name = "my-auth-1"
				return obj
			}(),
			op:           operation.Operation{Type: operation.Create},
			expectErrors: nil,
		},
		{
			name: "valid external auth resource name - maximum length",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.ID.Name = "myExternalAuth1" // 15 chars — max for this pattern
				obj.Name = "myExternalAuth1"
				return obj
			}(),
			op:           operation.Operation{Type: operation.Create},
			expectErrors: nil,
		},
		{
			name: "immutable provisioning state on update",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.ProvisioningState = coreapi.ProvisioningStateSucceeded
				// Set ValidationRules to empty to avoid nil pointer in discriminated union validation
				obj.Properties.Claim.ValidationRules = []coreapi.TokenClaimValidationRule{}
				return obj
			}(),
			oldObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.ProvisioningState = coreapi.ProvisioningStateProvisioning
				// Set ValidationRules to empty to avoid nil pointer in discriminated union validation
				obj.Properties.Claim.ValidationRules = []coreapi.TokenClaimValidationRule{}
				return obj
			}(),
			op: operation.Operation{Type: operation.Update},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.provisioningState", Message: "field is immutable"},
			},
		},
		{
			name: "update rejects changing console client type to Public",
			newObj: testExternalAuthWithClients(
				[]string{"console-client"},
				testExternalAuthClientProfile(
					coreapi.ExternalAuthConsoleClientComponentName,
					coreapi.ExternalAuthConsoleClientComponentNamespace,
					"console-client",
					metadataapi.ExternalAuthClientTypePublic,
				),
			),
			oldObj: testExternalAuthWithClients(
				[]string{"console-client"},
				testExternalAuthClientProfile(
					coreapi.ExternalAuthConsoleClientComponentName,
					coreapi.ExternalAuthConsoleClientComponentNamespace,
					"console-client",
					metadataapi.ExternalAuthClientTypeConfidential,
				),
			),
			op: operation.Operation{Type: operation.Update},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.clients[0].type", Message: fmt.Sprintf("must be %s when component name is %s and component namespace is %s",
					metadataapi.ExternalAuthClientTypeConfidential,
					coreapi.ExternalAuthConsoleClientComponentName,
					coreapi.ExternalAuthConsoleClientComponentNamespace,
				)},
			},
		},
		{
			name: "update accepts keeping console client Confidential",
			newObj: testExternalAuthWithClients(
				[]string{"console-client"},
				testExternalAuthClientProfile(
					coreapi.ExternalAuthConsoleClientComponentName,
					coreapi.ExternalAuthConsoleClientComponentNamespace,
					"console-client",
					metadataapi.ExternalAuthClientTypeConfidential,
				),
			),
			oldObj: testExternalAuthWithClients(
				[]string{"console-client"},
				testExternalAuthClientProfile(
					coreapi.ExternalAuthConsoleClientComponentName,
					coreapi.ExternalAuthConsoleClientComponentNamespace,
					"console-client",
					metadataapi.ExternalAuthClientTypeConfidential,
				),
			),
			op:           operation.Operation{Type: operation.Update},
			expectErrors: nil,
		},
		{
			name: "update rejects confidential on unsupported component",
			newObj: testExternalAuthWithClients(
				[]string{"other-client"},
				testExternalAuthClientProfile(
					"component1",
					"namespace1",
					"other-client",
					metadataapi.ExternalAuthClientTypeConfidential,
				),
			),
			oldObj: testExternalAuthWithClients(
				[]string{"other-client"},
				testExternalAuthClientProfile(
					"component1",
					"namespace1",
					"other-client",
					metadataapi.ExternalAuthClientTypePublic,
				),
			),
			op: operation.Operation{Type: operation.Update},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.clients[0].type", Message: "confidential client type is not allowed for this component"},
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

			utils.VerifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

func TestValidateExternalAuthDiscriminatedUnions(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		setupObject  func() *coreapi.HCPOpenShiftClusterExternalAuth
		expectErrors []utils.ExpectedError
	}{
		{
			name: "username prefix policy - valid None with no prefix",
			setupObject: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.Mappings.Username = coreapi.UsernameClaimProfile{
					Claim:        "sub",
					PrefixPolicy: metadataapi.UsernameClaimPrefixPolicyNone,
				}
				return obj
			},
			expectErrors: nil,
		},
		{
			name: "username prefix policy - valid NoPrefix with no prefix",
			setupObject: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.Mappings.Username = coreapi.UsernameClaimProfile{
					Claim:        "sub",
					PrefixPolicy: metadataapi.UsernameClaimPrefixPolicyNoPrefix,
				}
				return obj
			},
			expectErrors: nil,
		},
		{
			name: "username prefix policy - valid Prefix with prefix",
			setupObject: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.Mappings.Username = coreapi.UsernameClaimProfile{
					Claim:        "sub",
					Prefix:       "custom:",
					PrefixPolicy: metadataapi.UsernameClaimPrefixPolicyPrefix,
				}
				return obj
			},
			expectErrors: nil,
		},
		{
			name: "username prefix policy - invalid None with prefix (discriminated union violation)",
			setupObject: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.Mappings.Username = coreapi.UsernameClaimProfile{
					Claim:        "sub",
					Prefix:       "custom:",
					PrefixPolicy: metadataapi.UsernameClaimPrefixPolicyNone,
				}
				return obj
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.claim.mappings.username.prefix", Message: "may only be specified when `prefixPolicy` is \"Prefix\""},
			},
		},
		{
			name: "username prefix policy - invalid Prefix without prefix (discriminated union violation)",
			setupObject: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.Mappings.Username = coreapi.UsernameClaimProfile{
					Claim:        "sub",
					PrefixPolicy: metadataapi.UsernameClaimPrefixPolicyPrefix,
				}
				return obj
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.claim.mappings.username.prefix", Message: "must be specified when `prefixPolicy` is \"Prefix\""},
			},
		},
		{
			name: "token validation rule - valid RequiredClaim with claim",
			setupObject: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.ValidationRules = []coreapi.TokenClaimValidationRule{
					{
						Type: metadataapi.TokenValidationRuleTypeRequiredClaim,
						RequiredClaim: coreapi.TokenRequiredClaim{
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
			setupObject: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.ValidationRules = []coreapi.TokenClaimValidationRule{
					{
						Type: metadataapi.TokenValidationRuleTypeRequiredClaim,
					},
				}
				return obj
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.claim.validationRules[0].requiredClaim", Message: "must be specified when `type` is \"RequiredClaim\""},
				{FieldPath: "properties.claim.validationRules[0].requiredClaim.claim", Message: "Required value"},
				{FieldPath: "properties.claim.validationRules[0].requiredClaim.requiredValue", Message: "Required value"},
			},
		},
		{
			name: "token validation rule - invalid RequiredClaim with empty claim field",
			setupObject: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.ValidationRules = []coreapi.TokenClaimValidationRule{
					{
						Type: metadataapi.TokenValidationRuleTypeRequiredClaim,
						RequiredClaim: coreapi.TokenRequiredClaim{
							Claim:         "", // Empty claim should be rejected
							RequiredValue: "https://valid.example.com",
						},
					},
				}
				return obj
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.claim.validationRules[0].requiredClaim.claim", Message: "Required value"},
			},
		},
		{
			name: "token validation rule - invalid RequiredClaim with empty required value",
			setupObject: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.ValidationRules = []coreapi.TokenClaimValidationRule{
					{
						Type: metadataapi.TokenValidationRuleTypeRequiredClaim,
						RequiredClaim: coreapi.TokenRequiredClaim{
							Claim:         "iss",
							RequiredValue: "", // Empty required value should be rejected
						},
					},
				}
				return obj
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.claim.validationRules[0].requiredClaim.requiredValue", Message: "Required value"},
			},
		},
		{
			name: "username prefix policy - invalid NoPrefix with non-empty prefix (discriminated union violation)",
			setupObject: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.Mappings.Username = coreapi.UsernameClaimProfile{
					Claim:        "sub",
					Prefix:       "should-not-be-set:", // Prefix should not be set when PrefixPolicy is NoPrefix
					PrefixPolicy: metadataapi.UsernameClaimPrefixPolicyNoPrefix,
				}
				return obj
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.claim.mappings.username.prefix", Message: "may only be specified when `prefixPolicy` is \"Prefix\""},
			},
		},
		{
			name: "username prefix policy - invalid empty prefixPolicy",
			setupObject: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.Mappings.Username = coreapi.UsernameClaimProfile{
					Claim:        "sub",
					PrefixPolicy: "", // Empty prefixPolicy should be rejected
				}
				return obj
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.claim.mappings.username.prefixPolicy", Message: "supported values"},
			},
		},
		{
			name: "token validation rule - invalid empty type",
			setupObject: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Claim.ValidationRules = []coreapi.TokenClaimValidationRule{
					{
						Type: "", // Empty type should be rejected
						RequiredClaim: coreapi.TokenRequiredClaim{
							Claim:         "iss",
							RequiredValue: "https://valid.example.com",
						},
					},
				}
				return obj
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.claim.validationRules[0].type", Message: "supported values"},
				{FieldPath: "properties.claim.validationRules[0].requiredClaim", Message: "may only be specified when `type` is \"RequiredClaim\""},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := tt.setupObject()
			errs := ValidateExternalAuthCreate(ctx, obj)
			utils.VerifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

func TestValidateExternalAuthCustomValidation(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		newObj       *coreapi.HCPOpenShiftClusterExternalAuth
		expectErrors []utils.ExpectedError
	}{
		{
			name: "client ID matches audience",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Issuer.Audiences = []string{"client1", "client2"}
				obj.Properties.Clients = []coreapi.ExternalAuthClientProfile{
					{
						Component: coreapi.ExternalAuthClientComponentProfile{
							Name:                "component1",
							AuthClientNamespace: "namespace1",
						},
						ClientID: "client1",
						Type:     metadataapi.ExternalAuthClientTypePublic,
					},
				}
				return obj
			}(),
			expectErrors: nil,
		},
		{
			name: "client ID does not match any audience",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Issuer.Audiences = []string{"audience1", "audience2"}
				obj.Properties.Clients = []coreapi.ExternalAuthClientProfile{
					{
						Component: coreapi.ExternalAuthClientComponentProfile{
							Name:                "component1",
							AuthClientNamespace: "namespace1",
						},
						ClientID: "nonexistent-client",
						Type:     metadataapi.ExternalAuthClientTypePublic,
					},
				}
				return obj
			}(),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.clients[0]", Message: "must match an audience in issuer audiences"},
			},
		},
		{
			name: "unique client identifiers",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Issuer.Audiences = []string{"client1", "client2"}
				obj.Properties.Clients = []coreapi.ExternalAuthClientProfile{
					{
						Component: coreapi.ExternalAuthClientComponentProfile{
							Name:                "component1",
							AuthClientNamespace: "namespace1",
						},
						ClientID: "client1",
						Type:     metadataapi.ExternalAuthClientTypePublic,
					},
					{
						Component: coreapi.ExternalAuthClientComponentProfile{
							Name:                "component2",
							AuthClientNamespace: "namespace2",
						},
						ClientID: "client2",
						Type:     metadataapi.ExternalAuthClientTypePublic,
					},
				}
				return obj
			}(),
			expectErrors: nil,
		},
		{
			name: "console client must be Confidential",
			newObj: testExternalAuthWithClients(
				[]string{"console-client"},
				testExternalAuthClientProfile(
					coreapi.ExternalAuthConsoleClientComponentName,
					coreapi.ExternalAuthConsoleClientComponentNamespace,
					"console-client",
					metadataapi.ExternalAuthClientTypeConfidential,
				),
			),
			expectErrors: nil,
		},
		{
			name: "console client rejects Public type",
			newObj: testExternalAuthWithClients(
				[]string{"console-client"},
				testExternalAuthClientProfile(
					coreapi.ExternalAuthConsoleClientComponentName,
					coreapi.ExternalAuthConsoleClientComponentNamespace,
					"console-client",
					metadataapi.ExternalAuthClientTypePublic,
				),
			),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.clients[0].type", Message: fmt.Sprintf("must be %s when component name is %s and component namespace is %s",
					metadataapi.ExternalAuthClientTypeConfidential,
					coreapi.ExternalAuthConsoleClientComponentName,
					coreapi.ExternalAuthConsoleClientComponentNamespace,
				)},
			},
		},
		{
			name: "confidential client rejects unsupported component",
			newObj: testExternalAuthWithClients(
				[]string{"other-client"},
				testExternalAuthClientProfile(
					"component1",
					"namespace1",
					"other-client",
					metadataapi.ExternalAuthClientTypeConfidential,
				),
			),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.clients[0].type", Message: "confidential client type is not allowed for this component"},
			},
		},
		{
			name: "cli client in openshift-console namespace may be Public",
			newObj: testExternalAuthWithClients(
				[]string{"cli-client"},
				testExternalAuthClientProfile(
					"cli",
					coreapi.ExternalAuthConsoleClientComponentNamespace,
					"cli-client",
					metadataapi.ExternalAuthClientTypePublic,
				),
			),
			expectErrors: nil,
		},
		{
			name: "console name in different namespace may be Public",
			newObj: testExternalAuthWithClients(
				[]string{"console-client"},
				testExternalAuthClientProfile(
					coreapi.ExternalAuthConsoleClientComponentName,
					"other-namespace",
					"console-client",
					metadataapi.ExternalAuthClientTypePublic,
				),
			),
			expectErrors: nil,
		},
		{
			name: "openshift-console namespace with different name may be Public",
			newObj: testExternalAuthWithClients(
				[]string{"oauth-client"},
				testExternalAuthClientProfile(
					"oauth",
					coreapi.ExternalAuthConsoleClientComponentNamespace,
					"oauth-client",
					metadataapi.ExternalAuthClientTypePublic,
				),
			),
			expectErrors: nil,
		},
		{
			name: "default console and cli client pair",
			newObj: testExternalAuthWithClients(
				[]string{"shared-client-id"},
				testExternalAuthClientProfile(
					coreapi.ExternalAuthConsoleClientComponentName,
					coreapi.ExternalAuthConsoleClientComponentNamespace,
					"shared-client-id",
					metadataapi.ExternalAuthClientTypeConfidential,
				),
				testExternalAuthClientProfile(
					"cli",
					coreapi.ExternalAuthConsoleClientComponentNamespace,
					"shared-client-id",
					metadataapi.ExternalAuthClientTypePublic,
				),
			),
			expectErrors: nil,
		},
		{
			name: "duplicate client identifiers",
			newObj: func() *coreapi.HCPOpenShiftClusterExternalAuth {
				obj := createValidExternalAuth()
				obj.Properties.Issuer.Audiences = []string{"client1"}
				obj.Properties.Clients = []coreapi.ExternalAuthClientProfile{
					{
						Component: coreapi.ExternalAuthClientComponentProfile{
							Name:                "same-component",
							AuthClientNamespace: "same-namespace",
						},
						ClientID: "client1-a",
						Type:     metadataapi.ExternalAuthClientTypePublic,
					},
					{
						Component: coreapi.ExternalAuthClientComponentProfile{
							Name:                "same-component",
							AuthClientNamespace: "same-namespace",
						},
						ClientID: "client1-b",
						Type:     metadataapi.ExternalAuthClientTypePublic,
					},
				}
				return obj
			}(),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.clients[1]", Message: "Duplicate value"},
				{FieldPath: "properties.clients[0].clientId", Message: "must match an audience in issuer audiences"},
				{FieldPath: "properties.clients[1].clientId", Message: `Invalid value: "client1-b": must match an audience in issuer audiences`},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateExternalAuthCreate(ctx, tt.newObj)
			utils.VerifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}

func testExternalAuthClientProfile(name, namespace, clientID string, clientType metadataapi.ExternalAuthClientType) coreapi.ExternalAuthClientProfile {
	return coreapi.ExternalAuthClientProfile{
		Component: coreapi.ExternalAuthClientComponentProfile{
			Name:                name,
			AuthClientNamespace: namespace,
		},
		ClientID: clientID,
		Type:     clientType,
	}
}

func testExternalAuthWithClients(audiences []string, clients ...coreapi.ExternalAuthClientProfile) *coreapi.HCPOpenShiftClusterExternalAuth {
	obj := createValidExternalAuth()
	obj.Properties.Issuer.Audiences = audiences
	obj.Properties.Clients = clients
	// Avoid nil pointer issues in discriminated union validation during update tests.
	obj.Properties.Claim.ValidationRules = []coreapi.TokenClaimValidationRule{}
	return obj
}

func createMinimalExternalAuth() *coreapi.HCPOpenShiftClusterExternalAuth {
	resourceID, _ := azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/externalAuths/test-auth")
	obj := coreapi.NewDefaultHCPOpenShiftClusterExternalAuth(resourceID)
	obj.Properties.Claim.Mappings.Username.Claim = "sub"
	// Add required systemData fields
	createdAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	obj.SystemData = &coreapi.SystemData{
		CreatedBy:     "test-user",
		CreatedByType: coreapi.CreatedByTypeUser,
		CreatedAt:     &createdAt,
	}
	return obj
}

func createValidExternalAuth() *coreapi.HCPOpenShiftClusterExternalAuth {
	createdAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	return &coreapi.HCPOpenShiftClusterExternalAuth{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/externalAuths/test-auth"))},
		ProxyResource: coreapi.ProxyResource{
			Resource: coreapi.Resource{
				ID:   metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/externalAuths/test-auth")),
				Name: "test-auth",
				Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters/externalAuths",
				SystemData: &coreapi.SystemData{
					CreatedBy:     "test-user",
					CreatedByType: coreapi.CreatedByTypeUser,
					CreatedAt:     &createdAt,
				},
			},
		},
		Properties: coreapi.HCPOpenShiftClusterExternalAuthProperties{
			Issuer: coreapi.TokenIssuerProfile{
				URL:       "https://issuer.example.com",
				Audiences: []string{"audience1", "audience2"},
				CA:        validCertPEM(),
			},
			Clients: []coreapi.ExternalAuthClientProfile{
				{
					Component: coreapi.ExternalAuthClientComponentProfile{
						Name:                "test-component",
						AuthClientNamespace: "test-namespace",
					},
					ClientID: "audience1",
					Type:     metadataapi.ExternalAuthClientTypePublic,
				},
			},
			Claim: coreapi.ExternalAuthClaimProfile{
				Mappings: coreapi.TokenClaimMappingsProfile{
					Username: coreapi.UsernameClaimProfile{
						Claim:        "sub",
						PrefixPolicy: metadataapi.UsernameClaimPrefixPolicyNone,
					},
					Groups: &coreapi.GroupClaimProfile{
						Claim: "groups",
					},
				},
				ValidationRules: []coreapi.TokenClaimValidationRule{
					{
						Type: metadataapi.TokenValidationRuleTypeRequiredClaim,
						RequiredClaim: coreapi.TokenRequiredClaim{
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
	return mustValidCAPEM()
}

func TestValidateCACertificatePEM(t *testing.T) {
	fldPath := field.NewPath("properties", "issuer", "ca")
	now := time.Now()

	validCA := mustGenerateTestCertPEM(t, now.Add(-time.Hour), now.Add(24*time.Hour), true)
	secondValidCA := mustGenerateTestCertPEM(t, now.Add(-time.Hour), now.Add(24*time.Hour), true)
	nonCA := mustGenerateTestCertPEM(t, now.Add(-time.Hour), now.Add(24*time.Hour), false)
	expiredCA := mustGenerateTestCertPEM(t, now.Add(-48*time.Hour), now.Add(-time.Hour), true)
	notYetValidCA := mustGenerateTestCertPEM(t, now.Add(time.Hour), now.Add(24*time.Hour), true)
	privateKey := mustGenerateTestPrivateKeyPEM(t)

	tests := []struct {
		name         string
		value        *string
		expectErrors []utils.ExpectedError
	}{
		{
			name:  "nil is accepted",
			value: nil,
		},
		{
			name:  "empty is accepted",
			value: ptr.To(""),
		},
		{
			name:  "valid CA certificate",
			value: ptr.To(validCA),
		},
		{
			name:  "valid CA bundle",
			value: ptr.To(validCA + secondValidCA),
		},
		{
			name:  "not a PEM is rejected by ValidatePEM",
			value: ptr.To("NOT A PEM DOC"),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.issuer.ca", Message: "not a valid PEM"},
			},
		},
		{
			name:  "private key rejected",
			value: ptr.To(privateKey),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.issuer.ca", Message: "not a valid PEM"},
			},
		},
		{
			name:  "mixed certificate and private key rejected",
			value: ptr.To(validCA + privateKey),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.issuer.ca", Message: `PEM block 2 has type "EC PRIVATE KEY"; must be CERTIFICATE`},
			},
		},
		{
			name:  "empty certificate block is not a valid PEM",
			value: ptr.To("-----BEGIN CERTIFICATE-----\n-----END CERTIFICATE-----\n"),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.issuer.ca", Message: "not a valid PEM"},
			},
		},
		{
			name:  "non-CA certificate rejected",
			value: ptr.To(nonCA),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.issuer.ca", Message: "certificate 1 (CN=test-cert) must be a CA certificate"},
			},
		},
		{
			name:  "expired CA rejected",
			value: ptr.To(expiredCA),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.issuer.ca", Message: "certificate 1 (CN=test-ca) has expired"},
			},
		},
		{
			name:  "not-yet-valid CA rejected",
			value: ptr.To(notYetValidCA),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.issuer.ca", Message: "certificate 1 (CN=test-ca) is not yet valid"},
			},
		},
		{
			name:  "trailing non-PEM data rejected",
			value: ptr.To(validCA + "not-pem-data\n"),
			expectErrors: []utils.ExpectedError{
				{FieldPath: "properties.issuer.ca", Message: "contains trailing non-PEM data"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidatePEM(context.Background(), operation.Operation{}, fldPath, tt.value, nil)
			utils.VerifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}

	t.Run("ValidatePEM uses current time", func(t *testing.T) {
		errs := ValidatePEM(context.Background(), operation.Operation{}, fldPath, ptr.To(validCA), nil)
		if len(errs) != 0 {
			t.Fatalf("expected valid current CA to pass, got %v", errs)
		}
	})
}

func TestValidateCACertificatePEM_diagnostics(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	fldPath := field.NewPath("ca")
	nonCA := mustGenerateTestCertPEM(t, now.Add(-time.Hour), now.Add(24*time.Hour), false)
	privateKey := mustGenerateTestPrivateKeyPEM(t)
	validCA := mustGenerateTestCertPEM(t, now.Add(-time.Hour), now.Add(24*time.Hour), true)

	t.Run("non-CA uses block index and subject", func(t *testing.T) {
		errs := validateCACertificatePEM(fldPath, ptr.To(nonCA), now)
		if len(errs) != 1 {
			t.Fatalf("expected 1 error, got %v", errs)
		}
		if got := fmt.Sprint(errs[0].BadValue); got != "block 1: CN=test-cert" {
			t.Errorf("BadValue = %q, want %q", got, "block 1: CN=test-cert")
		}
		if !strings.Contains(errs[0].Detail, "certificate 1 (CN=test-cert) must be a CA certificate") {
			t.Errorf("Detail = %q", errs[0].Detail)
		}
	})

	t.Run("non-CERTIFICATE block uses block index", func(t *testing.T) {
		errs := validateCACertificatePEM(fldPath, ptr.To(validCA+privateKey), now)
		if len(errs) != 1 {
			t.Fatalf("expected 1 error, got %v", errs)
		}
		if got := fmt.Sprint(errs[0].BadValue); got != "block 2" {
			t.Errorf("BadValue = %q, want %q", got, "block 2")
		}
		if !strings.Contains(errs[0].Detail, `PEM block 2 has type "EC PRIVATE KEY"`) {
			t.Errorf("Detail = %q", errs[0].Detail)
		}
	})

	t.Run("trailing garbage is a single dedicated error", func(t *testing.T) {
		errs := validateCACertificatePEM(fldPath, ptr.To(validCA+"not-pem-data\n"), now)
		if len(errs) != 1 {
			t.Fatalf("expected 1 error, got %v", errs)
		}
		if got := fmt.Sprint(errs[0].BadValue); got != "" {
			t.Errorf("BadValue = %q, want empty", got)
		}
		if errs[0].Detail != "contains trailing non-PEM data" {
			t.Errorf("Detail = %q", errs[0].Detail)
		}
	})

	t.Run("trailing whitespace after PEM is accepted", func(t *testing.T) {
		if errs := validateCACertificatePEM(fldPath, ptr.To(validCA+"\n  \n"), now); len(errs) != 0 {
			t.Fatalf("expected trailing whitespace to pass, got %v", errs)
		}
	})
}

func mustGenerateTestCertPEM(t *testing.T, notBefore, notAfter time.Time, isCA bool) string {
	t.Helper()
	pemBytes, err := generateTestCertPEM(notBefore, notAfter, isCA)
	if err != nil {
		t.Fatalf("failed to generate test certificate: %v", err)
	}
	return pemBytes
}

func mustGenerateTestPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate test private key: %v", err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("failed to marshal test private key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))
}

func generateTestCertPEM(notBefore, notAfter time.Time, isCA bool) (string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-cert"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  isCA,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature,
	}
	if isCA {
		template.Subject.CommonName = "test-ca"
		template.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), nil
}

func mustValidCAPEM() string {
	validCAPEMOnce.Do(func() {
		pemBytes, err := generateTestCertPEM(time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour), true)
		if err != nil {
			panic(err)
		}
		validCAPEMForTests = pemBytes
	})
	return validCAPEMForTests
}

var (
	validCAPEMOnce     sync.Once
	validCAPEMForTests string
)

func TestValidateCACertificatePEM_boundaryTimes(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	fldPath := field.NewPath("ca")

	atNotBefore := mustGenerateTestCertPEM(t, now, now.Add(time.Hour), true)
	atNotAfter := mustGenerateTestCertPEM(t, now.Add(-time.Hour), now, true)

	if errs := validateCACertificatePEM(fldPath, &atNotBefore, now); len(errs) != 0 {
		t.Fatalf("certificate valid starting exactly now should pass, got %v", errs)
	}
	if errs := validateCACertificatePEM(fldPath, &atNotAfter, now); len(errs) != 0 {
		t.Fatalf("certificate valid until exactly now should pass, got %v", errs)
	}
}

func TestValidateCACertificatePEM_doesNotEchoPrivateKey(t *testing.T) {
	fldPath := field.NewPath("ca")
	privateKey := mustGenerateTestPrivateKeyPEM(t)
	errs := validateCACertificatePEM(fldPath, &privateKey, time.Now())
	if len(errs) == 0 {
		t.Fatal("expected errors for a private key PEM")
	}
	for _, err := range errs {
		if strings.Contains(err.Error(), "BEGIN") || strings.Contains(err.Error(), privateKey) {
			t.Fatalf("error leaked PEM contents: %v", err)
		}
	}
}
