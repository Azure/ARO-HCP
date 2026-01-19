package client

import (
	"context"
	"time"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/msi-dataplane/pkg/dataplane"
)

// IdentityStub represents the information of an Azure identity
// that will be returned when leveraging the mock Managed Identity Data Plane
// client. IdentityStub support is limited to the following Azure identities
// - Azure User-Assigned Managed Identity
// - Azure Service Principal
// It is often the case that what we really want to stub is the information
// of an Azure Service Principal instead of a User-Assigned Managed Identity.
type IdentityStub struct {
	// ClientID is the Client ID of the identity.
	ClientID string
	// ClientSecret is The base64 encoded bundle
	// certificate (public + private key) of the identity
	ClientSecret string
	// PrincipalID is the Principal ID of the identity.
	// For User-Assigned Managed identities this is the Object (principal) ID
	// of the Managed Identity. This is, the Object ID of the service principal
	// backing the Managed Identity.
	// For Service Principals it is the Object ID of the service principal.
	PrincipalID string
}

type managedIdentityClientStub struct {
	cloudConfiguration *cloud.Configuration

	// identityStub represents the part of the identity information that will
	// be stubbed and returned in all responses provided by the client stub.
	identityStub *IdentityStub

	// tenantID is the id of the tenant to be passed in to the stubbed managed identity data
	tenantID string
}

var _ MIDataplaneClient = (*managedIdentityClientStub)(nil)

// GetUserAssignedIdentities returns the User Assigned Managed Identities associated
// The returned results will have the stubbed data provided during construction of the client
// for the client id, client secret and principal id attributes.
func (c *managedIdentityClientStub) GetUserAssignedIdentitiesCredentials(ctx context.Context,
	request dataplane.UserAssignedIdentitiesRequest) (*dataplane.ManagedIdentityCredentials, error) {
	// TODO should we support certificate rotation for the Managed Identity
	// Data Plane client stub?

	// TODO if we need to deal with certificate expiration checking logic we
	// might need to update the CannotRenewAfter, NotAfter, NotBefore, RenewAfter
	// attributes to have values in the expected values for the client stub

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
			ClientID:                   ptr.To(c.identityStub.ClientID),
			ClientSecret:               ptr.To(c.identityStub.ClientSecret),
			TenantID:                   ptr.To(c.tenantID),
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
			ObjectID: ptr.To(c.identityStub.PrincipalID),
		}

		identities[i] = identity
	}

	managedIdentityCredentials.ExplicitIdentities = identities
	return &managedIdentityCredentials, nil
}

func NewManagedIdentityClientStub(
	cloudConfiguration *cloud.Configuration, identityStub *IdentityStub, tenantID string,
) MIDataplaneClient {
	return &managedIdentityClientStub{
		cloudConfiguration: cloudConfiguration,
		identityStub:       identityStub,
		tenantID:           tenantID,
	}
}
