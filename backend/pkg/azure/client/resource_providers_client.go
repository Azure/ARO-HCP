package client

import (
	"context"
	"log/slog"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// ResourceProvidersClient provides operations to interact with
// Azure Resource Providers API
type ResourceProvidersClient interface {
	Get(ctx context.Context, resourceProviderNamespace string,
		options *armresources.ProvidersClientGetOptions) (
		armresources.ProvidersClientGetResponse, error)
}

type resourceProvidersClient struct {
	client azureResourceProvidersApiClient
	logger *slog.Logger
}

func NewResourceProvidersClient(logger *slog.Logger, subscriptionID string, credential azcore.TokenCredential,
	options *arm.ClientOptions) (ResourceProvidersClient, error) {
	client, err := armresources.NewProvidersClient(subscriptionID, credential, options)
	if err != nil {
		return nil, err
	}
	return &resourceProvidersClient{
		client: client,
		logger: logger,
	}, nil
}

func (c *resourceProvidersClient) Get(ctx context.Context, resourceProviderNamespace string,
	options *armresources.ProvidersClientGetOptions) (armresources.ProvidersClientGetResponse, error) {
	c.logger.Debug("Getting RP", "resource_provider_namespace", resourceProviderNamespace)
	return c.client.Get(ctx, resourceProviderNamespace, options)
}

// ResourceProvidersClientRetriever allows you to retrieve a ResourceProvidersClient instance
type ResourceProvidersClientRetriever interface {
	Retrieve(subscriptionID string, credentials azcore.TokenCredential, options *arm.ClientOptions,
	) (ResourceProvidersClient, error)
}

type resourceProvidersClientRetriever struct {
	logger *slog.Logger
}

func NewResourceProvidersClientRetriever(logger *slog.Logger) ResourceProvidersClientRetriever {
	return &resourceProvidersClientRetriever{
		logger: logger,
	}
}

func (a *resourceProvidersClientRetriever) Retrieve(subscriptionID string,
	credentials azcore.TokenCredential, options *arm.ClientOptions) (ResourceProvidersClient, error) {
	return NewResourceProvidersClient(
		a.logger,
		subscriptionID,
		credentials,
		options,
	)
}
