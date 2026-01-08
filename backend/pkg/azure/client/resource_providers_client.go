package client

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// ResourceProvidersClient is an interface that defines the methods that
// we want to use from the ProvidersClient type in the Azure Go SDK
// (https://github.com/Azure/azure-sdk-for-go/tree/main/sdk/resourcemanager/resources/armresources).
// The aim is to only contain methods that are defined in the Azure Go SDK
// ProvidersClient client.
// For the cases where logic is desired to be implemented combining
// ProvidersClient calls and other logic use another client than
// this one.
// If you need to use a method provided by the Azure Go SDK ProvidersClient
// client but it is not defined in this interface then it has to be added here and all
// the types implementing this interface have to implement the new method.
// TODO now that the interface that we have always matches the methods of the SDK client, what if for example
// we would like to provide a higher abstracted interface because we consider this one too detailed in the area
// we are working on? imagine a method of the sdk that returns a pager. In some places we could consider iterating
// through the pager is too low level and we would like to provide a higher abstractd interface that hides those
// details. We could of course create a new interface that wraps this one but then how would we ensure that people
// are using the higher level one and not passing around the lower level one everywhere?
type ResourceProvidersClient interface {
	Get(ctx context.Context, resourceProviderNamespace string,
		options *armresources.ProvidersClientGetOptions) (armresources.ProvidersClientGetResponse, error)
}

// interface guard to ensure that all methods defined in the ResourceProvidersClient
// interface are implemented by the real Azure Go SDK ProvidersClient
// client. This interface guard should always compile
var _ ResourceProvidersClient = (*armresources.ProvidersClient)(nil)

// NewResourceProvidersClient instantiates a ResourceProvidersClient instance from the Azure Go SDK ProvidersClient
// client.
func NewResourceProvidersClient(subscriptionID string, credential azcore.TokenCredential, options *arm.ClientOptions) (ResourceProvidersClient, error) {
	return armresources.NewProvidersClient(subscriptionID, credential, options)
}

// ResourceProvidersClientRetriever allows you to retrieve a ResourceProvidersClient instance
type ResourceProvidersClientRetriever interface {
	Retrieve(subscriptionID string, credentials azcore.TokenCredential, options *arm.ClientOptions,
	) (ResourceProvidersClient, error)
}

var _ ResourceProvidersClient = (*armresources.ProvidersClient)(nil)

type resourceProvidersClientRetriever struct {
}

// NewResourceProvidersClientRetriever instantiates a ResourceProvidersClientRetriever instance that will
// instantiate a ResourceProvidersClient instance from the Azure Go SDK ProvidersClient client.
func NewResourceProvidersClientRetriever() ResourceProvidersClientRetriever {
	return &resourceProvidersClientRetriever{}
}

// TODO by now directly instantiating the SDK client and having the direct ResourceProvidersClient we
// simplify the code. However, what if for example we would like to add behavior on top of the SDK client? For
// example, adding some decoration on each call to its methods (logging, tracing, other stuff). With the previous
// approach I had with the extra type that wrapped it it was possible but if we always instantiate the SDK client
// directly it is not possible.
// TODO in the ARO Classic RP for example they also define an interface that matches the methods of the SDK client
// but they still have an extra type that implements the interface, that embeds the SDK client and where sometimes
// they add more information to the type or override some of the methods. For example, in
// https://github.com/Azure/ARO-RP/blob/master/pkg/util/azureclient/azuresdk/armauthorization/roledefinitions.go. Is
// that not desirable? This is semi-related to the first point too.
func (a *resourceProvidersClientRetriever) Retrieve(subscriptionID string,
	credential azcore.TokenCredential, options *arm.ClientOptions) (ResourceProvidersClient, error) {

	return NewResourceProvidersClient(subscriptionID, credential, options)
}
