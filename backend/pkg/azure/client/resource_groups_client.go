package client

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

type ResourceGroupsClient interface {
	CreateOrUpdate(ctx context.Context, resourceGroupName string, parameters armresources.ResourceGroup,
		options *armresources.ResourceGroupsClientCreateOrUpdateOptions) (
		armresources.ResourceGroupsClientCreateOrUpdateResponse, error)
	BeginDelete(ctx context.Context, resourceGroupName string,
		options *armresources.ResourceGroupsClientBeginDeleteOptions) (
		*runtime.Poller[armresources.ResourceGroupsClientDeleteResponse], error)
	Get(ctx context.Context, resourceGroupName string, options *armresources.ResourceGroupsClientGetOptions) (
		armresources.ResourceGroupsClientGetResponse, error)
}

var _ ResourceGroupsClient = (*armresources.ResourceGroupsClient)(nil)
