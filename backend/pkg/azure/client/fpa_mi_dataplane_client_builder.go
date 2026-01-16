package client

import (
	"github.com/Azure/ARO-HCP/internal/fpa"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/msi-dataplane/pkg/dataplane"
)

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

// FPAMIDataplaneClientBuilder offers the ability to create Managed Identity Data Plane clients
// authenticating as the the First Party Application (FPA) identity.
type FPAMIDataplaneClientBuilder interface {
	BuilderType() FPAMIDataplaneClientBuilderType
	// ManagedIdentitiesDataplane returns a new Managed Identity Data Plane client using the given identity URL.
	ManagedIdentitiesDataplane(identityURL string) (ManagedIdentitiesDataplaneClient, error)
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

func (b *fpaMIdataplaneClientBuilder) ManagedIdentitiesDataplane(identityURL string) (ManagedIdentitiesDataplaneClient, error) {
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

// NewFPAMIDataplaneClientBuilder provides a new instance of
// FPAMIDataplaneClientBuilder that allows to retrieve Managed Identities Data Plane clients
// authenticating as the the First Party Application (FPA) identity.
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
