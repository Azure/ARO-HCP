// Copyright 2026 Microsoft Corporation
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

package client

import (
	"context"
	"time"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/msi-dataplane/pkg/dataplane"
)

// HardcodedIdentity represents the information of an Azure identity
// that will be returned when leveraging the hardcodedIdentityManagedIdentitiesDataPlaneClient
// Data Plane client. HardcodedIdentity support is limited to the following
// Azure identities
// - Azure User-Assigned Managed Identity
// - Azure Service Principal
// It is often the case that what we really want to hardcode is the information
// of an Azure Service Principal instead of a User-Assigned Managed Identity.
type HardcodedIdentity struct {
	// ClientID is the Client ID of a valid identity.
	ClientID string
	// ClientSecret is The base64 encoded bundle
	// certificate (public + private key) of the identity.
	// The identity is a valid identity credential associated to the identity
	// identified by ClientID.
	ClientSecret string
	// PrincipalID is the Principal ID of the identity identified by ClientID.
	// For User-Assigned Managed identities this is the Object (principal) ID
	// of the Managed Identity. This is, the Object ID of the service principal
	// backing the Managed Identity.
	// For Service Principals it is the Object ID of the service principal.
	PrincipalID string
	// TenantID is the Tenant ID of the identity identified by ClientID.
	TenantID string
}

// hardcodedIdentityManagedIdentitiesDataplaneClient is a mock implementation of the
// ManagedIdentitiesDataplaneClient interface. The Managed Identities Data
// Plane service is only available in Azure tenants where Microsoft's
// First Party Application (FPA) integration is available. For the environments
// where the FPA integration is not enabled we cannot communicate with the
// Managed Identities Data Plane service so instead we use this mock implementation
// of the client, where all requests made with it return a single
// Azure Service Principal identity, disguised as a Managed Identity from the
// point of view of the consumers of the client. We commonly refer to this
// identity as the "mock MSI" (also known as mi mock) identity.
type hardcodedIdentityManagedIdentitiesDataplaneClient struct {
	cloudConfiguration *cloud.Configuration

	// hardcodedIdentity represents part of the identity information that will
	// be hardcoded and returned in all responses provided by the client.
	hardcodedIdentity *HardcodedIdentity
}

var _ ManagedIdentitiesDataplaneClient = (*hardcodedIdentityManagedIdentitiesDataplaneClient)(nil)

// GetUserAssignedIdentities returns the User Assigned Managed Identities associated
// The returned results will have the stubbed data provided during construction of the client
// for the client id, client secret and principal id attributes.
func (c *hardcodedIdentityManagedIdentitiesDataplaneClient) GetUserAssignedIdentitiesCredentials(ctx context.Context,
	request dataplane.UserAssignedIdentitiesRequest) (*dataplane.ManagedIdentityCredentials, error) {
	now := time.Now().UTC()
	aHundredYearsFromNow := now.AddDate(100, 0, 0).Format(time.RFC3339)
	aDayAgo := now.AddDate(0, 0, -1).Format(time.RFC3339)
	managedIdentityCredentials := dataplane.ManagedIdentityCredentials{
		AuthenticationEndpoint: ptr.To(c.cloudConfiguration.ActiveDirectoryAuthorityHost),
		NotBefore:              ptr.To(aDayAgo),
		CannotRenewAfter:       ptr.To(aHundredYearsFromNow),
		RenewAfter:             ptr.To(aHundredYearsFromNow),
		NotAfter:               ptr.To(aHundredYearsFromNow),
	}

	placeholder := "placeholder"
	identities := make([]dataplane.UserAssignedIdentityCredentials, len(request.IdentityIDs))
	for i, miResourceID := range request.IdentityIDs {
		identity := dataplane.UserAssignedIdentityCredentials{
			ClientID:                   ptr.To(c.hardcodedIdentity.ClientID),
			ClientSecret:               ptr.To(c.hardcodedIdentity.ClientSecret),
			TenantID:                   ptr.To(c.hardcodedIdentity.TenantID),
			ResourceID:                 ptr.To(miResourceID),
			AuthenticationEndpoint:     ptr.To(c.cloudConfiguration.ActiveDirectoryAuthorityHost),
			ClientSecretURL:            &placeholder,
			MtlsAuthenticationEndpoint: &placeholder,
			NotBefore:                  ptr.To(aDayAgo),
			CannotRenewAfter:           ptr.To(aHundredYearsFromNow),
			RenewAfter:                 ptr.To(aHundredYearsFromNow),
			NotAfter:                   ptr.To(aHundredYearsFromNow),
			CustomClaims: &dataplane.CustomClaims{
				XMSAzNwperimid: []string{placeholder},
				XMSAzTm:        &placeholder,
			},
			// In this specific context Object ID is equivalent to Principal ID
			ObjectID: ptr.To(c.hardcodedIdentity.PrincipalID),
		}

		identities[i] = identity
	}

	managedIdentityCredentials.ExplicitIdentities = identities
	return &managedIdentityCredentials, nil
}

// newHardcodedIdentityManagedIdentitiesDataPlaneClient provides a new instance of
// ManagedIdentitiesDataplaneClient based on the hardcoded identity implementation
// of the Managed Identities Data Plane client hardcodedIdentityManagedIdentitiesDataplaneClient.
func newHardcodedIdentityManagedIdentitiesDataPlaneClient(
	cloudConfiguration *cloud.Configuration, hardcodedIdentity *HardcodedIdentity,
) ManagedIdentitiesDataplaneClient {
	return &hardcodedIdentityManagedIdentitiesDataplaneClient{
		cloudConfiguration: cloudConfiguration,
		hardcodedIdentity:  hardcodedIdentity,
	}
}
