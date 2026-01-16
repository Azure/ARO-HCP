package client

import (
	"github.com/Azure/ARO-HCP/internal/fpa"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/msi-dataplane/pkg/dataplane"
)

// TODO given that the MSI data plane service comes from a library that is
// not the azure go sdk but a different one and it has multiple types as well
// as usage of stub in lower environments, mock identities and so on, should
// we have a pkg/azure/midataplane package to contain all of that instead.
// pkg/azure/client would be only for azure go sdk based ones.

// FPAClientBuilderType is a type that represents the type of the MIDataplaneClientBuilder
// interface. It is used to ensure that that interface is incompatible
// with other client builder interfaces that might have the same set of
// methods
type FPAMIDataplaneClientBuilderType string

const (
	// FPAClientBuilderTypeValue is the value of the FPABuilderType type that
	// represents the FPA client builder.
	FPAMIDataplaneClientBuilderTypeValue FPAMIDataplaneClientBuilderType = "FPA-MIDP"
)

// TODO should we reuse FPAClientBuilder interface for this? It is a bit
// special because it is a service that is not part of azure go sdk and
// only available in some environments.
type FPAMIDataplaneClientBuilder interface {
	BuilderType() FPAMIDataplaneClientBuilderType
	MIDataplane(tenantID string, identityURL string) (MIDataplaneClient, error)
}

type fpaMIdataplaneClientBuilder struct {
	serviceTenantID       string
	audience              string
	fpaTokenCredRetriever fpa.FirstPartyApplicationTokenCredentialRetriever
	options               *azcore.ClientOptions
}

var _ FPAMIDataplaneClientBuilder = (*fpaMIdataplaneClientBuilder)(nil)

func (b *fpaMIdataplaneClientBuilder) BuilderType() FPAMIDataplaneClientBuilderType {
	return FPAMIDataplaneClientBuilderTypeValue
}

// MIDataplane returns a new Managed Identity Data Plane client using the given identity URL.
// The tenantID parameter is not used in the implementation that leverages the real MI dataplane client,
// because it does not need it. However, it is still part of the MIDataplane method signature because for the
// hardcoded identity implementation (hardcodedIdentityMIDataplaneClient) we need it to determine
// the tenant ID of the identies being requested. We need this because the mock msi in some environments
// is located in a different tenant than where the service is running.
// For the real implementation the tenantID parameter of MIDataplane is not needed. We still
// define it in the interface because for the stub we would not need it.
// The identity URL is used to retrieve the Managed Identity Data Plane client.
func (b *fpaMIdataplaneClientBuilder) MIDataplane(_ string, identityURL string) (MIDataplaneClient, error) {
	creds, err := b.fpaTokenCredRetriever.RetrieveCredential(
		b.serviceTenantID,
		// The MI dataplane client receives tenant from the bearer challenge, we use a widlcard * so as
		// to not limit the allowed tenants in the credential. This was taken from
		// https://github.com/Azure/ARO-RP/blob/9719391dd5d2213abb1b895e9b9471925f5aec0d/pkg/cluster/cluster.go#L329
		// which was added as part of needed fixes to make Managed Identity work in MSFT Canary env
		// in https://github.com/Azure/ARO-RP/pull/3957
		"*",
	)
	if err != nil {
		return nil, err
	}

	dpClientFactory := dataplane.NewClientFactory(creds, b.audience, b.options)
	return dpClientFactory.NewClient(identityURL)
}

func NewFPAMIDataplaneClientBuilder(
	serviceTenantID string,
	fpaTokenCredRetriever fpa.FirstPartyApplicationTokenCredentialRetriever,
	audience string, options *azcore.ClientOptions,
) FPAMIDataplaneClientBuilder {

	return &fpaMIdataplaneClientBuilder{
		serviceTenantID:       serviceTenantID,
		fpaTokenCredRetriever: fpaTokenCredRetriever,
		audience:              audience,
		options:               options,
	}
}
