//go:build go1.18
// +build go1.18

// Code generated by Microsoft (R) AutoRest Code Generator (autorest: 3.10.3, generator: @autorest/go@4.0.0-preview.63)
// Changes may cause incorrect behavior and will be lost if the code is regenerated.
// Code generated by @autorest/go. DO NOT EDIT.

package generated

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// NodePoolsClient contains the methods for the NodePools group.
// Don't use this type directly, use NewNodePoolsClient() instead.
type NodePoolsClient struct {
	internal       *arm.Client
	subscriptionID string
}

// NewNodePoolsClient creates a new instance of NodePoolsClient with the specified values.
//   - subscriptionID - The ID of the target subscription. The value must be an UUID.
//   - credential - used to authorize requests. Usually a credential from azidentity.
//   - options - pass nil to accept the default values.
func NewNodePoolsClient(subscriptionID string, credential azcore.TokenCredential, options *arm.ClientOptions) (*NodePoolsClient, error) {
	cl, err := arm.NewClient(moduleName, moduleVersion, credential, options)
	if err != nil {
		return nil, err
	}
	client := &NodePoolsClient{
		subscriptionID: subscriptionID,
		internal:       cl,
	}
	return client, nil
}

// BeginCreateOrUpdate - Create a HcpOpenShiftClusterNodePoolResource
// If the operation fails it returns an *azcore.ResponseError type.
//
// Generated from API version 2024-06-10-preview
//   - resourceGroupName - The name of the resource group. The name is case insensitive.
//   - hcpOpenShiftClusterName - Name of HCP cluster
//   - nodePoolName - Name of HCP cluster *
//   - resource - Resource create parameters.
//   - options - NodePoolsClientBeginCreateOrUpdateOptions contains the optional parameters for the NodePoolsClient.BeginCreateOrUpdate
//     method.
func (client *NodePoolsClient) BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, nodePoolName string, resource HcpOpenShiftClusterNodePoolResource, options *NodePoolsClientBeginCreateOrUpdateOptions) (*runtime.Poller[NodePoolsClientCreateOrUpdateResponse], error) {
	if options == nil || options.ResumeToken == "" {
		resp, err := client.createOrUpdate(ctx, resourceGroupName, hcpOpenShiftClusterName, nodePoolName, resource, options)
		if err != nil {
			return nil, err
		}
		poller, err := runtime.NewPoller(resp, client.internal.Pipeline(), &runtime.NewPollerOptions[NodePoolsClientCreateOrUpdateResponse]{
			FinalStateVia: runtime.FinalStateViaAzureAsyncOp,
			Tracer:        client.internal.Tracer(),
		})
		return poller, err
	} else {
		return runtime.NewPollerFromResumeToken(options.ResumeToken, client.internal.Pipeline(), &runtime.NewPollerFromResumeTokenOptions[NodePoolsClientCreateOrUpdateResponse]{
			Tracer: client.internal.Tracer(),
		})
	}
}

// CreateOrUpdate - Create a HcpOpenShiftClusterNodePoolResource
// If the operation fails it returns an *azcore.ResponseError type.
//
// Generated from API version 2024-06-10-preview
func (client *NodePoolsClient) createOrUpdate(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, nodePoolName string, resource HcpOpenShiftClusterNodePoolResource, options *NodePoolsClientBeginCreateOrUpdateOptions) (*http.Response, error) {
	var err error
	const operationName = "NodePoolsClient.BeginCreateOrUpdate"
	ctx = context.WithValue(ctx, runtime.CtxAPINameKey{}, operationName)
	ctx, endSpan := runtime.StartSpan(ctx, operationName, client.internal.Tracer(), nil)
	defer func() { endSpan(err) }()
	req, err := client.createOrUpdateCreateRequest(ctx, resourceGroupName, hcpOpenShiftClusterName, nodePoolName, resource, options)
	if err != nil {
		return nil, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return nil, err
	}
	if !runtime.HasStatusCode(httpResp, http.StatusOK, http.StatusCreated) {
		err = runtime.NewResponseError(httpResp)
		return nil, err
	}
	return httpResp, nil
}

// createOrUpdateCreateRequest creates the CreateOrUpdate request.
func (client *NodePoolsClient) createOrUpdateCreateRequest(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, nodePoolName string, resource HcpOpenShiftClusterNodePoolResource, options *NodePoolsClientBeginCreateOrUpdateOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/{hcpOpenShiftClusterName}/nodePools/{nodePoolName}"
	if client.subscriptionID == "" {
		return nil, errors.New("parameter client.subscriptionID cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{subscriptionId}", url.PathEscape(client.subscriptionID))
	if resourceGroupName == "" {
		return nil, errors.New("parameter resourceGroupName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{resourceGroupName}", url.PathEscape(resourceGroupName))
	if hcpOpenShiftClusterName == "" {
		return nil, errors.New("parameter hcpOpenShiftClusterName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{hcpOpenShiftClusterName}", url.PathEscape(hcpOpenShiftClusterName))
	if nodePoolName == "" {
		return nil, errors.New("parameter nodePoolName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{nodePoolName}", url.PathEscape(nodePoolName))
	req, err := runtime.NewRequest(ctx, http.MethodPut, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	if err != nil {
		return nil, err
	}
	reqQP := req.Raw().URL.Query()
	reqQP.Set("api-version", "2024-06-10-preview")
	req.Raw().URL.RawQuery = reqQP.Encode()
	req.Raw().Header["Accept"] = []string{"application/json"}
	if err := runtime.MarshalAsJSON(req, resource); err != nil {
		return nil, err
	}
	return req, nil
}

// BeginDelete - Delete a HcpOpenShiftClusterNodePoolResource
// If the operation fails it returns an *azcore.ResponseError type.
//
// Generated from API version 2024-06-10-preview
//   - resourceGroupName - The name of the resource group. The name is case insensitive.
//   - hcpOpenShiftClusterName - Name of HCP cluster
//   - nodePoolName - Name of HCP cluster *
//   - options - NodePoolsClientBeginDeleteOptions contains the optional parameters for the NodePoolsClient.BeginDelete method.
func (client *NodePoolsClient) BeginDelete(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, nodePoolName string, options *NodePoolsClientBeginDeleteOptions) (*runtime.Poller[NodePoolsClientDeleteResponse], error) {
	if options == nil || options.ResumeToken == "" {
		resp, err := client.deleteOperation(ctx, resourceGroupName, hcpOpenShiftClusterName, nodePoolName, options)
		if err != nil {
			return nil, err
		}
		poller, err := runtime.NewPoller(resp, client.internal.Pipeline(), &runtime.NewPollerOptions[NodePoolsClientDeleteResponse]{
			FinalStateVia: runtime.FinalStateViaLocation,
			Tracer:        client.internal.Tracer(),
		})
		return poller, err
	} else {
		return runtime.NewPollerFromResumeToken(options.ResumeToken, client.internal.Pipeline(), &runtime.NewPollerFromResumeTokenOptions[NodePoolsClientDeleteResponse]{
			Tracer: client.internal.Tracer(),
		})
	}
}

// Delete - Delete a HcpOpenShiftClusterNodePoolResource
// If the operation fails it returns an *azcore.ResponseError type.
//
// Generated from API version 2024-06-10-preview
func (client *NodePoolsClient) deleteOperation(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, nodePoolName string, options *NodePoolsClientBeginDeleteOptions) (*http.Response, error) {
	var err error
	const operationName = "NodePoolsClient.BeginDelete"
	ctx = context.WithValue(ctx, runtime.CtxAPINameKey{}, operationName)
	ctx, endSpan := runtime.StartSpan(ctx, operationName, client.internal.Tracer(), nil)
	defer func() { endSpan(err) }()
	req, err := client.deleteCreateRequest(ctx, resourceGroupName, hcpOpenShiftClusterName, nodePoolName, options)
	if err != nil {
		return nil, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return nil, err
	}
	if !runtime.HasStatusCode(httpResp, http.StatusAccepted, http.StatusNoContent) {
		err = runtime.NewResponseError(httpResp)
		return nil, err
	}
	return httpResp, nil
}

// deleteCreateRequest creates the Delete request.
func (client *NodePoolsClient) deleteCreateRequest(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, nodePoolName string, options *NodePoolsClientBeginDeleteOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/{hcpOpenShiftClusterName}/nodePools/{nodePoolName}"
	if client.subscriptionID == "" {
		return nil, errors.New("parameter client.subscriptionID cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{subscriptionId}", url.PathEscape(client.subscriptionID))
	if resourceGroupName == "" {
		return nil, errors.New("parameter resourceGroupName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{resourceGroupName}", url.PathEscape(resourceGroupName))
	if hcpOpenShiftClusterName == "" {
		return nil, errors.New("parameter hcpOpenShiftClusterName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{hcpOpenShiftClusterName}", url.PathEscape(hcpOpenShiftClusterName))
	if nodePoolName == "" {
		return nil, errors.New("parameter nodePoolName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{nodePoolName}", url.PathEscape(nodePoolName))
	req, err := runtime.NewRequest(ctx, http.MethodDelete, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	if err != nil {
		return nil, err
	}
	reqQP := req.Raw().URL.Query()
	reqQP.Set("api-version", "2024-06-10-preview")
	req.Raw().URL.RawQuery = reqQP.Encode()
	req.Raw().Header["Accept"] = []string{"application/json"}
	return req, nil
}

// Get - Get a HcpOpenShiftClusterNodePoolResource
// If the operation fails it returns an *azcore.ResponseError type.
//
// Generated from API version 2024-06-10-preview
//   - resourceGroupName - The name of the resource group. The name is case insensitive.
//   - hcpOpenShiftClusterName - Name of HCP cluster
//   - nodePoolName - Name of HCP cluster *
//   - options - NodePoolsClientGetOptions contains the optional parameters for the NodePoolsClient.Get method.
func (client *NodePoolsClient) Get(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, nodePoolName string, options *NodePoolsClientGetOptions) (NodePoolsClientGetResponse, error) {
	var err error
	const operationName = "NodePoolsClient.Get"
	ctx = context.WithValue(ctx, runtime.CtxAPINameKey{}, operationName)
	ctx, endSpan := runtime.StartSpan(ctx, operationName, client.internal.Tracer(), nil)
	defer func() { endSpan(err) }()
	req, err := client.getCreateRequest(ctx, resourceGroupName, hcpOpenShiftClusterName, nodePoolName, options)
	if err != nil {
		return NodePoolsClientGetResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return NodePoolsClientGetResponse{}, err
	}
	if !runtime.HasStatusCode(httpResp, http.StatusOK) {
		err = runtime.NewResponseError(httpResp)
		return NodePoolsClientGetResponse{}, err
	}
	resp, err := client.getHandleResponse(httpResp)
	return resp, err
}

// getCreateRequest creates the Get request.
func (client *NodePoolsClient) getCreateRequest(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, nodePoolName string, options *NodePoolsClientGetOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/{hcpOpenShiftClusterName}/nodePools/{nodePoolName}"
	if client.subscriptionID == "" {
		return nil, errors.New("parameter client.subscriptionID cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{subscriptionId}", url.PathEscape(client.subscriptionID))
	if resourceGroupName == "" {
		return nil, errors.New("parameter resourceGroupName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{resourceGroupName}", url.PathEscape(resourceGroupName))
	if hcpOpenShiftClusterName == "" {
		return nil, errors.New("parameter hcpOpenShiftClusterName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{hcpOpenShiftClusterName}", url.PathEscape(hcpOpenShiftClusterName))
	if nodePoolName == "" {
		return nil, errors.New("parameter nodePoolName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{nodePoolName}", url.PathEscape(nodePoolName))
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	if err != nil {
		return nil, err
	}
	reqQP := req.Raw().URL.Query()
	reqQP.Set("api-version", "2024-06-10-preview")
	req.Raw().URL.RawQuery = reqQP.Encode()
	req.Raw().Header["Accept"] = []string{"application/json"}
	return req, nil
}

// getHandleResponse handles the Get response.
func (client *NodePoolsClient) getHandleResponse(resp *http.Response) (NodePoolsClientGetResponse, error) {
	result := NodePoolsClientGetResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.HcpOpenShiftClusterNodePoolResource); err != nil {
		return NodePoolsClientGetResponse{}, err
	}
	return result, nil
}

// NewListByParentPager - List HcpOpenShiftClusterNodePoolResource resources by HcpOpenShiftClusterResource
//
// Generated from API version 2024-06-10-preview
//   - resourceGroupName - The name of the resource group. The name is case insensitive.
//   - hcpOpenShiftClusterName - Name of HCP cluster
//   - options - NodePoolsClientListByParentOptions contains the optional parameters for the NodePoolsClient.NewListByParentPager
//     method.
func (client *NodePoolsClient) NewListByParentPager(resourceGroupName string, hcpOpenShiftClusterName string, options *NodePoolsClientListByParentOptions) *runtime.Pager[NodePoolsClientListByParentResponse] {
	return runtime.NewPager(runtime.PagingHandler[NodePoolsClientListByParentResponse]{
		More: func(page NodePoolsClientListByParentResponse) bool {
			return page.NextLink != nil && len(*page.NextLink) > 0
		},
		Fetcher: func(ctx context.Context, page *NodePoolsClientListByParentResponse) (NodePoolsClientListByParentResponse, error) {
			ctx = context.WithValue(ctx, runtime.CtxAPINameKey{}, "NodePoolsClient.NewListByParentPager")
			nextLink := ""
			if page != nil {
				nextLink = *page.NextLink
			}
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), nextLink, func(ctx context.Context) (*policy.Request, error) {
				return client.listByParentCreateRequest(ctx, resourceGroupName, hcpOpenShiftClusterName, options)
			}, nil)
			if err != nil {
				return NodePoolsClientListByParentResponse{}, err
			}
			return client.listByParentHandleResponse(resp)
		},
		Tracer: client.internal.Tracer(),
	})
}

// listByParentCreateRequest creates the ListByParent request.
func (client *NodePoolsClient) listByParentCreateRequest(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, options *NodePoolsClientListByParentOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/{hcpOpenShiftClusterName}/nodePools"
	if client.subscriptionID == "" {
		return nil, errors.New("parameter client.subscriptionID cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{subscriptionId}", url.PathEscape(client.subscriptionID))
	if resourceGroupName == "" {
		return nil, errors.New("parameter resourceGroupName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{resourceGroupName}", url.PathEscape(resourceGroupName))
	if hcpOpenShiftClusterName == "" {
		return nil, errors.New("parameter hcpOpenShiftClusterName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{hcpOpenShiftClusterName}", url.PathEscape(hcpOpenShiftClusterName))
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	if err != nil {
		return nil, err
	}
	reqQP := req.Raw().URL.Query()
	reqQP.Set("api-version", "2024-06-10-preview")
	req.Raw().URL.RawQuery = reqQP.Encode()
	req.Raw().Header["Accept"] = []string{"application/json"}
	return req, nil
}

// listByParentHandleResponse handles the ListByParent response.
func (client *NodePoolsClient) listByParentHandleResponse(resp *http.Response) (NodePoolsClientListByParentResponse, error) {
	result := NodePoolsClientListByParentResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.HcpOpenShiftClusterNodePoolResourceListResult); err != nil {
		return NodePoolsClientListByParentResponse{}, err
	}
	return result, nil
}

// BeginUpdate - Update a HcpOpenShiftClusterNodePoolResource
// If the operation fails it returns an *azcore.ResponseError type.
//
// Generated from API version 2024-06-10-preview
//   - resourceGroupName - The name of the resource group. The name is case insensitive.
//   - hcpOpenShiftClusterName - Name of HCP cluster
//   - nodePoolName - Name of HCP cluster *
//   - properties - The resource properties to be updated.
//   - options - NodePoolsClientBeginUpdateOptions contains the optional parameters for the NodePoolsClient.BeginUpdate method.
func (client *NodePoolsClient) BeginUpdate(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, nodePoolName string, properties HcpOpenShiftClusterNodePoolPatch, options *NodePoolsClientBeginUpdateOptions) (*runtime.Poller[NodePoolsClientUpdateResponse], error) {
	if options == nil || options.ResumeToken == "" {
		resp, err := client.update(ctx, resourceGroupName, hcpOpenShiftClusterName, nodePoolName, properties, options)
		if err != nil {
			return nil, err
		}
		poller, err := runtime.NewPoller(resp, client.internal.Pipeline(), &runtime.NewPollerOptions[NodePoolsClientUpdateResponse]{
			FinalStateVia: runtime.FinalStateViaLocation,
			Tracer:        client.internal.Tracer(),
		})
		return poller, err
	} else {
		return runtime.NewPollerFromResumeToken(options.ResumeToken, client.internal.Pipeline(), &runtime.NewPollerFromResumeTokenOptions[NodePoolsClientUpdateResponse]{
			Tracer: client.internal.Tracer(),
		})
	}
}

// Update - Update a HcpOpenShiftClusterNodePoolResource
// If the operation fails it returns an *azcore.ResponseError type.
//
// Generated from API version 2024-06-10-preview
func (client *NodePoolsClient) update(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, nodePoolName string, properties HcpOpenShiftClusterNodePoolPatch, options *NodePoolsClientBeginUpdateOptions) (*http.Response, error) {
	var err error
	const operationName = "NodePoolsClient.BeginUpdate"
	ctx = context.WithValue(ctx, runtime.CtxAPINameKey{}, operationName)
	ctx, endSpan := runtime.StartSpan(ctx, operationName, client.internal.Tracer(), nil)
	defer func() { endSpan(err) }()
	req, err := client.updateCreateRequest(ctx, resourceGroupName, hcpOpenShiftClusterName, nodePoolName, properties, options)
	if err != nil {
		return nil, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return nil, err
	}
	if !runtime.HasStatusCode(httpResp, http.StatusOK, http.StatusAccepted) {
		err = runtime.NewResponseError(httpResp)
		return nil, err
	}
	return httpResp, nil
}

// updateCreateRequest creates the Update request.
func (client *NodePoolsClient) updateCreateRequest(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, nodePoolName string, properties HcpOpenShiftClusterNodePoolPatch, options *NodePoolsClientBeginUpdateOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/{hcpOpenShiftClusterName}/nodePools/{nodePoolName}"
	if client.subscriptionID == "" {
		return nil, errors.New("parameter client.subscriptionID cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{subscriptionId}", url.PathEscape(client.subscriptionID))
	if resourceGroupName == "" {
		return nil, errors.New("parameter resourceGroupName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{resourceGroupName}", url.PathEscape(resourceGroupName))
	if hcpOpenShiftClusterName == "" {
		return nil, errors.New("parameter hcpOpenShiftClusterName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{hcpOpenShiftClusterName}", url.PathEscape(hcpOpenShiftClusterName))
	if nodePoolName == "" {
		return nil, errors.New("parameter nodePoolName cannot be empty")
	}
	urlPath = strings.ReplaceAll(urlPath, "{nodePoolName}", url.PathEscape(nodePoolName))
	req, err := runtime.NewRequest(ctx, http.MethodPatch, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	if err != nil {
		return nil, err
	}
	reqQP := req.Raw().URL.Query()
	reqQP.Set("api-version", "2024-06-10-preview")
	req.Raw().URL.RawQuery = reqQP.Encode()
	req.Raw().Header["Accept"] = []string{"application/json"}
	if err := runtime.MarshalAsJSON(req, properties); err != nil {
		return nil, err
	}
	return req, nil
}
