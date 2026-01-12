package client

import (
	"github.com/Azure/ARO-HCP/internal/fpa"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// TODO decide what to do with the case where multiple additional allowed tenants are specified
// in some cases for the FPA identity
// TODO decide if we have different interfaces for each type of client or just different
// implementations
// TODO should we have arm.ClientOptions as parameters of the methods? This would allow us
// to instantiate different clients with different sets of options instead of relying on the implementation
// to set them at instantiation time. What would be the use case? Different clients could set different explicit API
// versions as it is part of the options. Moving it to the methods means we would need to pass around the client
// options and/or create different ones depending on specific needs. Right now we don't explicitly set apiversions.
type ClientBuilder interface {
	ResourceProvidersClient(tenantID string, subscriptionID string) (ResourceProvidersClient, error)
}

type FPAClientBuilder struct {
	fpaTokenCredRetriever fpa.FirstPartyApplicationTokenCredentialRetriever
	options               *arm.ClientOptions
}

var _ ClientBuilder = (*FPAClientBuilder)(nil)

// NewFpaClientBuilder instantiates a FPAClientBuilder. When clients are instantiated with it the FPA token credential
// retriever is leveraged to get a FPA Token Credential, and the provided ARM client options.
func NewFpaClientBuilder(tokenCredRetriever fpa.FirstPartyApplicationTokenCredentialRetriever, options *arm.ClientOptions) *FPAClientBuilder {
	return &FPAClientBuilder{
		fpaTokenCredRetriever: tokenCredRetriever,
		options:               options,
	}
}

func (b *FPAClientBuilder) ResourceProvidersClient(tenantID string, subscriptionID string) (ResourceProvidersClient, error) {
	creds, err := b.fpaTokenCredRetriever.RetrieveCredential(tenantID)
	if err != nil {
		return nil, err
	}

	return NewResourceProvidersClient(subscriptionID, creds, b.options)
}
