package client

import (
	"github.com/Azure/ARO-HCP/internal/fpa"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

type ClientBuilder interface {
	NewProvidersClient(subscriptionID string, tenantID string, clientOptions *arm.ClientOptions) (ResourceProvidersClient, error)
}

type FPAClientBuilder struct {
	fpaTokenCredRetriever fpa.FirstPartyApplicationTokenCredentialRetriever
}

func NewFPAClientBuilder(fpaTokenCredRetriever fpa.FirstPartyApplicationTokenCredentialRetriever) *FPAClientBuilder {
	return &FPAClientBuilder{fpaTokenCredRetriever: fpaTokenCredRetriever}
}

func (f FPAClientBuilder) NewProvidersClient(subscriptionID string, tenantID string, clientOptions *arm.ClientOptions) (ResourceProvidersClient, error) {
	credential, err := f.fpaTokenCredRetriever.RetrieveCredential(tenantID)
	if err != nil {
		return nil, err
	}
	return armresources.NewProvidersClient(subscriptionID, credential, clientOptions)
}

var _ ClientBuilder = &FPAClientBuilder{}

type MockClientBuilder struct {
	ResourceProvidersClient ResourceProvidersClient
}

var _ ClientBuilder = &MockClientBuilder{}

func (m MockClientBuilder) NewProvidersClient(subscriptionID string, tenantID string, clientOptions *arm.ClientOptions) (ResourceProvidersClient, error) {
	return m.ResourceProvidersClient, nil
}
