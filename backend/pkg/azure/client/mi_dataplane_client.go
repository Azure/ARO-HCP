package client

import (
	"context"

	"github.com/Azure/msi-dataplane/pkg/dataplane"
)

// MIDataplaneClient is the interface to interact with Azure's Managed Identity
// Data Plane service. The Managed Identities Data Plane service is only
// available in Azure tenants where Microsoft's First Party Application (FPA)
// is enabled. For the environments where the FPA integration is not enabled
// we use a "mock" FPA identity, which is a common Azure Service Principal
// identity,and we instantiate a "mock" MIDataPlaneClient that always returns
// a single Azure Service Principal identity representing a managed identity.
// We commonly refer to this identity as the "mock MSI" identity. In that case,
// all requests to the client will return the same Azure Service Principal
// identity.
// This client is different than the Azure's Managed Identity Client, which is
// used to interact with the control plane side of the Managed Identities service.
type MIDataplaneClient interface {
	GetUserAssignedIdentitiesCredentials(
		ctx context.Context, request dataplane.UserAssignedIdentitiesRequest,
	) (*dataplane.ManagedIdentityCredentials, error)
}

var _ MIDataplaneClient = (dataplane.Client)(nil)
