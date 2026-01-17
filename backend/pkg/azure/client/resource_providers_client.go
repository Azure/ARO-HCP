package client

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// ResourceProvidersClient is an interface that defines the methods that
// we want to use from the ProvidersClient type in the Azure Go SDK
// (https://github.com/Azure/azure-sdk-for-go/tree/main/sdk/resourcemanager/resources/armresources).
// The aim is to only contain methods that are defined in the Azure Go SDK
// ProvidersClient client.
// If you need to use a method provided by the Azure Go SDK ProvidersClient
// client but it is not defined in this interface then it has to be added here and all
// the types implementing this interface have to implement the new method.
type ResourceProvidersClient interface {
	Get(ctx context.Context, resourceProviderNamespace string,
		options *armresources.ProvidersClientGetOptions) (armresources.ProvidersClientGetResponse, error)
}

// interface guard to ensure that all methods defined in the ResourceProvidersClient
// interface are implemented by the real Azure Go SDK ProvidersClient
// client. This interface guard should always compile
var _ ResourceProvidersClient = (*armresources.ProvidersClient)(nil)
