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
}

type hardcodedIdentityMIDataplaneClient struct {
	cloudConfiguration *cloud.Configuration

	// hardcodedIdentity represents the part of the identity information that will
	// be stubbed and returned in all responses provided by the client stub.
	hardcodedIdentity *HardcodedIdentity

	// tenantID is the id of the tenant to be passed in to the stubbed managed identity data
	tenantID string
}

var _ MIDataplaneClient = (*hardcodedIdentityMIDataplaneClient)(nil)

// GetUserAssignedIdentities returns the User Assigned Managed Identities associated
// The returned results will have the stubbed data provided during construction of the client
// for the client id, client secret and principal id attributes.
func (c *hardcodedIdentityMIDataplaneClient) GetUserAssignedIdentitiesCredentials(ctx context.Context,
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
			ClientID:                   ptr.To(c.hardcodedIdentity.ClientID),
			ClientSecret:               ptr.To(c.hardcodedIdentity.ClientSecret),
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
			ObjectID: ptr.To(c.hardcodedIdentity.PrincipalID),
		}

		identities[i] = identity
	}

	managedIdentityCredentials.ExplicitIdentities = identities
	return &managedIdentityCredentials, nil
}

func NewHardcodedIdentityMIDataPlaneClient(
	cloudConfiguration *cloud.Configuration, identityStub *HardcodedIdentity, tenantID string,
) MIDataplaneClient {
	return &hardcodedIdentityMIDataplaneClient{
		cloudConfiguration: cloudConfiguration,
		hardcodedIdentity:  identityStub,
		tenantID:           tenantID,
	}
}
