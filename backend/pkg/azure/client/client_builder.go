package client

import (
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/fpa"
)

// FPAClientBuilderType is a type that represents the type of the FPAClientBuilder
// interface. It is used to ensure that that interface is incompatible
// with other client builder interfaces that might have the same set of
// methods
type FPAClientBuilderType string

const (
	// FPAClientBuilderTypeValue is the value of the FPABuilderType type that
	// represents the FPA client builder.
	FPAClientBuilderTypeValue FPAClientBuilderType = "FPA"
)

type FPAClientBuilder interface {
	// BuilderType returns the type of the client builder. Its only
	// purpose is to ensure that this interface is incompatible
	// with other client builder interfaces that might have the same
	// set of methods. In that way we ensure that they cannot be used
	// interchangeably.
	BuilderType() FPAClientBuilderType
	ResourceProvidersClient(tenantID string, subscriptionID string) (ResourceProvidersClient, error)
}

type fpaClientBuilder struct {
	fpaTokenCredRetriever fpa.FirstPartyApplicationTokenCredentialRetriever
	options               *azcorearm.ClientOptions
}

var _ FPAClientBuilder = (*fpaClientBuilder)(nil)

// NewFPAClientBuilder instantiates a FPAClientBuilder. When clients are instantiated with it the FPA token credential
// retriever is leveraged to get a FPA Token Credential, and the provided ARM client options.
func NewFPAClientBuilder(tokenCredRetriever fpa.FirstPartyApplicationTokenCredentialRetriever, options *azcorearm.ClientOptions) FPAClientBuilder {
	return &fpaClientBuilder{
		fpaTokenCredRetriever: tokenCredRetriever,
		options:               options,
	}
}

func (b *fpaClientBuilder) ResourceProvidersClient(tenantID string, subscriptionID string) (ResourceProvidersClient, error) {
	creds, err := b.fpaTokenCredRetriever.RetrieveCredential(tenantID)
	if err != nil {
		return nil, err
	}

	return NewResourceProvidersClient(subscriptionID, creds, b.options)
}

func (b *fpaClientBuilder) BuilderType() FPAClientBuilderType {
	return FPAClientBuilderTypeValue
}
