//go:build go1.18
// +build go1.18

// Code generated by Microsoft (R) AutoRest Code Generator (autorest: 3.10.2, generator: @autorest/go@4.0.0-preview.63)
// Changes may cause incorrect behavior and will be lost if the code is regenerated.
// Code generated by @autorest/go. DO NOT EDIT.

package fake

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"

	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/fake/server"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
)

// HcpOpenShiftClustersServer is a fake server for instances of the generated.HcpOpenShiftClustersClient type.
type HcpOpenShiftClustersServer struct {
	// AdminCredentials is the fake for method HcpOpenShiftClustersClient.AdminCredentials
	// HTTP status codes to indicate success: http.StatusOK
	AdminCredentials func(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, options *generated.HcpOpenShiftClustersClientAdminCredentialsOptions) (resp azfake.Responder[generated.HcpOpenShiftClustersClientAdminCredentialsResponse], errResp azfake.ErrorResponder)

	// BeginCreateOrUpdate is the fake for method HcpOpenShiftClustersClient.BeginCreateOrUpdate
	// HTTP status codes to indicate success: http.StatusOK, http.StatusCreated
	BeginCreateOrUpdate func(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, resource generated.HcpOpenShiftClusterResource, options *generated.HcpOpenShiftClustersClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[generated.HcpOpenShiftClustersClientCreateOrUpdateResponse], errResp azfake.ErrorResponder)

	// BeginDelete is the fake for method HcpOpenShiftClustersClient.BeginDelete
	// HTTP status codes to indicate success: http.StatusAccepted, http.StatusNoContent
	BeginDelete func(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, options *generated.HcpOpenShiftClustersClientBeginDeleteOptions) (resp azfake.PollerResponder[generated.HcpOpenShiftClustersClientDeleteResponse], errResp azfake.ErrorResponder)

	// Get is the fake for method HcpOpenShiftClustersClient.Get
	// HTTP status codes to indicate success: http.StatusOK
	Get func(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, options *generated.HcpOpenShiftClustersClientGetOptions) (resp azfake.Responder[generated.HcpOpenShiftClustersClientGetResponse], errResp azfake.ErrorResponder)

	// KubeConfig is the fake for method HcpOpenShiftClustersClient.KubeConfig
	// HTTP status codes to indicate success: http.StatusOK
	KubeConfig func(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, options *generated.HcpOpenShiftClustersClientKubeConfigOptions) (resp azfake.Responder[generated.HcpOpenShiftClustersClientKubeConfigResponse], errResp azfake.ErrorResponder)

	// NewListByResourceGroupPager is the fake for method HcpOpenShiftClustersClient.NewListByResourceGroupPager
	// HTTP status codes to indicate success: http.StatusOK
	NewListByResourceGroupPager func(resourceGroupName string, options *generated.HcpOpenShiftClustersClientListByResourceGroupOptions) (resp azfake.PagerResponder[generated.HcpOpenShiftClustersClientListByResourceGroupResponse])

	// NewListBySubscriptionPager is the fake for method HcpOpenShiftClustersClient.NewListBySubscriptionPager
	// HTTP status codes to indicate success: http.StatusOK
	NewListBySubscriptionPager func(options *generated.HcpOpenShiftClustersClientListBySubscriptionOptions) (resp azfake.PagerResponder[generated.HcpOpenShiftClustersClientListBySubscriptionResponse])

	// BeginUpdate is the fake for method HcpOpenShiftClustersClient.BeginUpdate
	// HTTP status codes to indicate success: http.StatusOK, http.StatusAccepted
	BeginUpdate func(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, properties generated.HcpOpenShiftClusterPatch, options *generated.HcpOpenShiftClustersClientBeginUpdateOptions) (resp azfake.PollerResponder[generated.HcpOpenShiftClustersClientUpdateResponse], errResp azfake.ErrorResponder)
}

// NewHcpOpenShiftClustersServerTransport creates a new instance of HcpOpenShiftClustersServerTransport with the provided implementation.
// The returned HcpOpenShiftClustersServerTransport instance is connected to an instance of generated.HcpOpenShiftClustersClient via the
// azcore.ClientOptions.Transporter field in the client's constructor parameters.
func NewHcpOpenShiftClustersServerTransport(srv *HcpOpenShiftClustersServer) *HcpOpenShiftClustersServerTransport {
	return &HcpOpenShiftClustersServerTransport{
		srv:                         srv,
		beginCreateOrUpdate:         newTracker[azfake.PollerResponder[generated.HcpOpenShiftClustersClientCreateOrUpdateResponse]](),
		beginDelete:                 newTracker[azfake.PollerResponder[generated.HcpOpenShiftClustersClientDeleteResponse]](),
		newListByResourceGroupPager: newTracker[azfake.PagerResponder[generated.HcpOpenShiftClustersClientListByResourceGroupResponse]](),
		newListBySubscriptionPager:  newTracker[azfake.PagerResponder[generated.HcpOpenShiftClustersClientListBySubscriptionResponse]](),
		beginUpdate:                 newTracker[azfake.PollerResponder[generated.HcpOpenShiftClustersClientUpdateResponse]](),
	}
}

// HcpOpenShiftClustersServerTransport connects instances of generated.HcpOpenShiftClustersClient to instances of HcpOpenShiftClustersServer.
// Don't use this type directly, use NewHcpOpenShiftClustersServerTransport instead.
type HcpOpenShiftClustersServerTransport struct {
	srv                         *HcpOpenShiftClustersServer
	beginCreateOrUpdate         *tracker[azfake.PollerResponder[generated.HcpOpenShiftClustersClientCreateOrUpdateResponse]]
	beginDelete                 *tracker[azfake.PollerResponder[generated.HcpOpenShiftClustersClientDeleteResponse]]
	newListByResourceGroupPager *tracker[azfake.PagerResponder[generated.HcpOpenShiftClustersClientListByResourceGroupResponse]]
	newListBySubscriptionPager  *tracker[azfake.PagerResponder[generated.HcpOpenShiftClustersClientListBySubscriptionResponse]]
	beginUpdate                 *tracker[azfake.PollerResponder[generated.HcpOpenShiftClustersClientUpdateResponse]]
}

// Do implements the policy.Transporter interface for HcpOpenShiftClustersServerTransport.
func (h *HcpOpenShiftClustersServerTransport) Do(req *http.Request) (*http.Response, error) {
	rawMethod := req.Context().Value(runtime.CtxAPINameKey{})
	method, ok := rawMethod.(string)
	if !ok {
		return nil, nonRetriableError{errors.New("unable to dispatch request, missing value for CtxAPINameKey")}
	}

	var resp *http.Response
	var err error

	switch method {
	case "HcpOpenShiftClustersClient.AdminCredentials":
		resp, err = h.dispatchAdminCredentials(req)
	case "HcpOpenShiftClustersClient.BeginCreateOrUpdate":
		resp, err = h.dispatchBeginCreateOrUpdate(req)
	case "HcpOpenShiftClustersClient.BeginDelete":
		resp, err = h.dispatchBeginDelete(req)
	case "HcpOpenShiftClustersClient.Get":
		resp, err = h.dispatchGet(req)
	case "HcpOpenShiftClustersClient.KubeConfig":
		resp, err = h.dispatchKubeConfig(req)
	case "HcpOpenShiftClustersClient.NewListByResourceGroupPager":
		resp, err = h.dispatchNewListByResourceGroupPager(req)
	case "HcpOpenShiftClustersClient.NewListBySubscriptionPager":
		resp, err = h.dispatchNewListBySubscriptionPager(req)
	case "HcpOpenShiftClustersClient.BeginUpdate":
		resp, err = h.dispatchBeginUpdate(req)
	default:
		err = fmt.Errorf("unhandled API %s", method)
	}

	if err != nil {
		return nil, err
	}

	return resp, nil
}

func (h *HcpOpenShiftClustersServerTransport) dispatchAdminCredentials(req *http.Request) (*http.Response, error) {
	if h.srv.AdminCredentials == nil {
		return nil, &nonRetriableError{errors.New("fake for method AdminCredentials not implemented")}
	}
	const regexStr = `/subscriptions/(?P<subscriptionId>[!#&$-;=?-\[\]_a-zA-Z0-9~%@]+)/resourceGroups/(?P<resourceGroupName>[!#&$-;=?-\[\]_a-zA-Z0-9~%@]+)/providers/Microsoft\.RedHatOpenShift/hcpOpenShiftClusters/(?P<hcpOpenShiftClusterName>[!#&$-;=?-\[\]_a-zA-Z0-9~%@]+)/adminCredentials`
	regex := regexp.MustCompile(regexStr)
	matches := regex.FindStringSubmatch(req.URL.EscapedPath())
	if matches == nil || len(matches) < 3 {
		return nil, fmt.Errorf("failed to parse path %s", req.URL.Path)
	}
	resourceGroupNameParam, err := url.PathUnescape(matches[regex.SubexpIndex("resourceGroupName")])
	if err != nil {
		return nil, err
	}
	hcpOpenShiftClusterNameParam, err := url.PathUnescape(matches[regex.SubexpIndex("hcpOpenShiftClusterName")])
	if err != nil {
		return nil, err
	}
	respr, errRespr := h.srv.AdminCredentials(req.Context(), resourceGroupNameParam, hcpOpenShiftClusterNameParam, nil)
	if respErr := server.GetError(errRespr, req); respErr != nil {
		return nil, respErr
	}
	respContent := server.GetResponseContent(respr)
	if !contains([]int{http.StatusOK}, respContent.HTTPStatus) {
		return nil, &nonRetriableError{fmt.Errorf("unexpected status code %d. acceptable values are http.StatusOK", respContent.HTTPStatus)}
	}
	resp, err := server.MarshalResponseAsJSON(respContent, server.GetResponse(respr).HcpOpenShiftClusterCredentials, req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (h *HcpOpenShiftClustersServerTransport) dispatchBeginCreateOrUpdate(req *http.Request) (*http.Response, error) {
	if h.srv.BeginCreateOrUpdate == nil {
		return nil, &nonRetriableError{errors.New("fake for method BeginCreateOrUpdate not implemented")}
	}
	beginCreateOrUpdate := h.beginCreateOrUpdate.get(req)
	if beginCreateOrUpdate == nil {
		const regexStr = `/subscriptions/(?P<subscriptionId>[!#&$-;=?-\[\]_a-zA-Z0-9~%@]+)/resourceGroups/(?P<resourceGroupName>[!#&$-;=?-\[\]_a-zA-Z0-9~%@]+)/providers/Microsoft\.RedHatOpenShift/hcpOpenShiftClusters/(?P<hcpOpenShiftClusterName>[!#&$-;=?-\[\]_a-zA-Z0-9~%@]+)`
		regex := regexp.MustCompile(regexStr)
		matches := regex.FindStringSubmatch(req.URL.EscapedPath())
		if matches == nil || len(matches) < 3 {
			return nil, fmt.Errorf("failed to parse path %s", req.URL.Path)
		}
		body, err := server.UnmarshalRequestAsJSON[generated.HcpOpenShiftClusterResource](req)
		if err != nil {
			return nil, err
		}
		resourceGroupNameParam, err := url.PathUnescape(matches[regex.SubexpIndex("resourceGroupName")])
		if err != nil {
			return nil, err
		}
		hcpOpenShiftClusterNameParam, err := url.PathUnescape(matches[regex.SubexpIndex("hcpOpenShiftClusterName")])
		if err != nil {
			return nil, err
		}
		respr, errRespr := h.srv.BeginCreateOrUpdate(req.Context(), resourceGroupNameParam, hcpOpenShiftClusterNameParam, body, nil)
		if respErr := server.GetError(errRespr, req); respErr != nil {
			return nil, respErr
		}
		beginCreateOrUpdate = &respr
		h.beginCreateOrUpdate.add(req, beginCreateOrUpdate)
	}

	resp, err := server.PollerResponderNext(beginCreateOrUpdate, req)
	if err != nil {
		return nil, err
	}

	if !contains([]int{http.StatusOK, http.StatusCreated}, resp.StatusCode) {
		h.beginCreateOrUpdate.remove(req)
		return nil, &nonRetriableError{fmt.Errorf("unexpected status code %d. acceptable values are http.StatusOK, http.StatusCreated", resp.StatusCode)}
	}
	if !server.PollerResponderMore(beginCreateOrUpdate) {
		h.beginCreateOrUpdate.remove(req)
	}

	return resp, nil
}

func (h *HcpOpenShiftClustersServerTransport) dispatchBeginDelete(req *http.Request) (*http.Response, error) {
	if h.srv.BeginDelete == nil {
		return nil, &nonRetriableError{errors.New("fake for method BeginDelete not implemented")}
	}
	beginDelete := h.beginDelete.get(req)
	if beginDelete == nil {
		const regexStr = `/subscriptions/(?P<subscriptionId>[!#&$-;=?-\[\]_a-zA-Z0-9~%@]+)/resourceGroups/(?P<resourceGroupName>[!#&$-;=?-\[\]_a-zA-Z0-9~%@]+)/providers/Microsoft\.RedHatOpenShift/hcpOpenShiftClusters/(?P<hcpOpenShiftClusterName>[!#&$-;=?-\[\]_a-zA-Z0-9~%@]+)`
		regex := regexp.MustCompile(regexStr)
		matches := regex.FindStringSubmatch(req.URL.EscapedPath())
		if matches == nil || len(matches) < 3 {
			return nil, fmt.Errorf("failed to parse path %s", req.URL.Path)
		}
		resourceGroupNameParam, err := url.PathUnescape(matches[regex.SubexpIndex("resourceGroupName")])
		if err != nil {
			return nil, err
		}
		hcpOpenShiftClusterNameParam, err := url.PathUnescape(matches[regex.SubexpIndex("hcpOpenShiftClusterName")])
		if err != nil {
			return nil, err
		}
		respr, errRespr := h.srv.BeginDelete(req.Context(), resourceGroupNameParam, hcpOpenShiftClusterNameParam, nil)
		if respErr := server.GetError(errRespr, req); respErr != nil {
			return nil, respErr
		}
		beginDelete = &respr
		h.beginDelete.add(req, beginDelete)
	}

	resp, err := server.PollerResponderNext(beginDelete, req)
	if err != nil {
		return nil, err
	}

	if !contains([]int{http.StatusAccepted, http.StatusNoContent}, resp.StatusCode) {
		h.beginDelete.remove(req)
		return nil, &nonRetriableError{fmt.Errorf("unexpected status code %d. acceptable values are http.StatusAccepted, http.StatusNoContent", resp.StatusCode)}
	}
	if !server.PollerResponderMore(beginDelete) {
		h.beginDelete.remove(req)
	}

	return resp, nil
}

func (h *HcpOpenShiftClustersServerTransport) dispatchGet(req *http.Request) (*http.Response, error) {
	if h.srv.Get == nil {
		return nil, &nonRetriableError{errors.New("fake for method Get not implemented")}
	}
	const regexStr = `/subscriptions/(?P<subscriptionId>[!#&$-;=?-\[\]_a-zA-Z0-9~%@]+)/resourceGroups/(?P<resourceGroupName>[!#&$-;=?-\[\]_a-zA-Z0-9~%@]+)/providers/Microsoft\.RedHatOpenShift/hcpOpenShiftClusters/(?P<hcpOpenShiftClusterName>[!#&$-;=?-\[\]_a-zA-Z0-9~%@]+)`
	regex := regexp.MustCompile(regexStr)
	matches := regex.FindStringSubmatch(req.URL.EscapedPath())
	if matches == nil || len(matches) < 3 {
		return nil, fmt.Errorf("failed to parse path %s", req.URL.Path)
	}
	resourceGroupNameParam, err := url.PathUnescape(matches[regex.SubexpIndex("resourceGroupName")])
	if err != nil {
		return nil, err
	}
	hcpOpenShiftClusterNameParam, err := url.PathUnescape(matches[regex.SubexpIndex("hcpOpenShiftClusterName")])
	if err != nil {
		return nil, err
	}
	respr, errRespr := h.srv.Get(req.Context(), resourceGroupNameParam, hcpOpenShiftClusterNameParam, nil)
	if respErr := server.GetError(errRespr, req); respErr != nil {
		return nil, respErr
	}
	respContent := server.GetResponseContent(respr)
	if !contains([]int{http.StatusOK}, respContent.HTTPStatus) {
		return nil, &nonRetriableError{fmt.Errorf("unexpected status code %d. acceptable values are http.StatusOK", respContent.HTTPStatus)}
	}
	resp, err := server.MarshalResponseAsJSON(respContent, server.GetResponse(respr).HcpOpenShiftClusterResource, req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (h *HcpOpenShiftClustersServerTransport) dispatchKubeConfig(req *http.Request) (*http.Response, error) {
	if h.srv.KubeConfig == nil {
		return nil, &nonRetriableError{errors.New("fake for method KubeConfig not implemented")}
	}
	const regexStr = `/subscriptions/(?P<subscriptionId>[!#&$-;=?-\[\]_a-zA-Z0-9~%@]+)/resourceGroups/(?P<resourceGroupName>[!#&$-;=?-\[\]_a-zA-Z0-9~%@]+)/providers/Microsoft\.RedHatOpenShift/hcpOpenShiftClusters/(?P<hcpOpenShiftClusterName>[!#&$-;=?-\[\]_a-zA-Z0-9~%@]+)/kubeConfig`
	regex := regexp.MustCompile(regexStr)
	matches := regex.FindStringSubmatch(req.URL.EscapedPath())
	if matches == nil || len(matches) < 3 {
		return nil, fmt.Errorf("failed to parse path %s", req.URL.Path)
	}
	resourceGroupNameParam, err := url.PathUnescape(matches[regex.SubexpIndex("resourceGroupName")])
	if err != nil {
		return nil, err
	}
	hcpOpenShiftClusterNameParam, err := url.PathUnescape(matches[regex.SubexpIndex("hcpOpenShiftClusterName")])
	if err != nil {
		return nil, err
	}
	respr, errRespr := h.srv.KubeConfig(req.Context(), resourceGroupNameParam, hcpOpenShiftClusterNameParam, nil)
	if respErr := server.GetError(errRespr, req); respErr != nil {
		return nil, respErr
	}
	respContent := server.GetResponseContent(respr)
	if !contains([]int{http.StatusOK}, respContent.HTTPStatus) {
		return nil, &nonRetriableError{fmt.Errorf("unexpected status code %d. acceptable values are http.StatusOK", respContent.HTTPStatus)}
	}
	resp, err := server.MarshalResponseAsJSON(respContent, server.GetResponse(respr).HcpOpenShiftClusterKubeconfig, req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (h *HcpOpenShiftClustersServerTransport) dispatchNewListByResourceGroupPager(req *http.Request) (*http.Response, error) {
	if h.srv.NewListByResourceGroupPager == nil {
		return nil, &nonRetriableError{errors.New("fake for method NewListByResourceGroupPager not implemented")}
	}
	newListByResourceGroupPager := h.newListByResourceGroupPager.get(req)
	if newListByResourceGroupPager == nil {
		const regexStr = `/subscriptions/(?P<subscriptionId>[!#&$-;=?-\[\]_a-zA-Z0-9~%@]+)/resourceGroups/(?P<resourceGroupName>[!#&$-;=?-\[\]_a-zA-Z0-9~%@]+)/providers/Microsoft\.RedHatOpenShift/hcpOpenShiftClusters`
		regex := regexp.MustCompile(regexStr)
		matches := regex.FindStringSubmatch(req.URL.EscapedPath())
		if matches == nil || len(matches) < 2 {
			return nil, fmt.Errorf("failed to parse path %s", req.URL.Path)
		}
		resourceGroupNameParam, err := url.PathUnescape(matches[regex.SubexpIndex("resourceGroupName")])
		if err != nil {
			return nil, err
		}
		resp := h.srv.NewListByResourceGroupPager(resourceGroupNameParam, nil)
		newListByResourceGroupPager = &resp
		h.newListByResourceGroupPager.add(req, newListByResourceGroupPager)
		server.PagerResponderInjectNextLinks(newListByResourceGroupPager, req, func(page *generated.HcpOpenShiftClustersClientListByResourceGroupResponse, createLink func() string) {
			page.NextLink = to.Ptr(createLink())
		})
	}
	resp, err := server.PagerResponderNext(newListByResourceGroupPager, req)
	if err != nil {
		return nil, err
	}
	if !contains([]int{http.StatusOK}, resp.StatusCode) {
		h.newListByResourceGroupPager.remove(req)
		return nil, &nonRetriableError{fmt.Errorf("unexpected status code %d. acceptable values are http.StatusOK", resp.StatusCode)}
	}
	if !server.PagerResponderMore(newListByResourceGroupPager) {
		h.newListByResourceGroupPager.remove(req)
	}
	return resp, nil
}

func (h *HcpOpenShiftClustersServerTransport) dispatchNewListBySubscriptionPager(req *http.Request) (*http.Response, error) {
	if h.srv.NewListBySubscriptionPager == nil {
		return nil, &nonRetriableError{errors.New("fake for method NewListBySubscriptionPager not implemented")}
	}
	newListBySubscriptionPager := h.newListBySubscriptionPager.get(req)
	if newListBySubscriptionPager == nil {
		const regexStr = `/subscriptions/(?P<subscriptionId>[!#&$-;=?-\[\]_a-zA-Z0-9~%@]+)/providers/Microsoft\.RedHatOpenShift/hcpOpenShiftClusters`
		regex := regexp.MustCompile(regexStr)
		matches := regex.FindStringSubmatch(req.URL.EscapedPath())
		if matches == nil || len(matches) < 1 {
			return nil, fmt.Errorf("failed to parse path %s", req.URL.Path)
		}
		resp := h.srv.NewListBySubscriptionPager(nil)
		newListBySubscriptionPager = &resp
		h.newListBySubscriptionPager.add(req, newListBySubscriptionPager)
		server.PagerResponderInjectNextLinks(newListBySubscriptionPager, req, func(page *generated.HcpOpenShiftClustersClientListBySubscriptionResponse, createLink func() string) {
			page.NextLink = to.Ptr(createLink())
		})
	}
	resp, err := server.PagerResponderNext(newListBySubscriptionPager, req)
	if err != nil {
		return nil, err
	}
	if !contains([]int{http.StatusOK}, resp.StatusCode) {
		h.newListBySubscriptionPager.remove(req)
		return nil, &nonRetriableError{fmt.Errorf("unexpected status code %d. acceptable values are http.StatusOK", resp.StatusCode)}
	}
	if !server.PagerResponderMore(newListBySubscriptionPager) {
		h.newListBySubscriptionPager.remove(req)
	}
	return resp, nil
}

func (h *HcpOpenShiftClustersServerTransport) dispatchBeginUpdate(req *http.Request) (*http.Response, error) {
	if h.srv.BeginUpdate == nil {
		return nil, &nonRetriableError{errors.New("fake for method BeginUpdate not implemented")}
	}
	beginUpdate := h.beginUpdate.get(req)
	if beginUpdate == nil {
		const regexStr = `/subscriptions/(?P<subscriptionId>[!#&$-;=?-\[\]_a-zA-Z0-9~%@]+)/resourceGroups/(?P<resourceGroupName>[!#&$-;=?-\[\]_a-zA-Z0-9~%@]+)/providers/Microsoft\.RedHatOpenShift/hcpOpenShiftClusters/(?P<hcpOpenShiftClusterName>[!#&$-;=?-\[\]_a-zA-Z0-9~%@]+)`
		regex := regexp.MustCompile(regexStr)
		matches := regex.FindStringSubmatch(req.URL.EscapedPath())
		if matches == nil || len(matches) < 3 {
			return nil, fmt.Errorf("failed to parse path %s", req.URL.Path)
		}
		body, err := server.UnmarshalRequestAsJSON[generated.HcpOpenShiftClusterPatch](req)
		if err != nil {
			return nil, err
		}
		resourceGroupNameParam, err := url.PathUnescape(matches[regex.SubexpIndex("resourceGroupName")])
		if err != nil {
			return nil, err
		}
		hcpOpenShiftClusterNameParam, err := url.PathUnescape(matches[regex.SubexpIndex("hcpOpenShiftClusterName")])
		if err != nil {
			return nil, err
		}
		respr, errRespr := h.srv.BeginUpdate(req.Context(), resourceGroupNameParam, hcpOpenShiftClusterNameParam, body, nil)
		if respErr := server.GetError(errRespr, req); respErr != nil {
			return nil, respErr
		}
		beginUpdate = &respr
		h.beginUpdate.add(req, beginUpdate)
	}

	resp, err := server.PollerResponderNext(beginUpdate, req)
	if err != nil {
		return nil, err
	}

	if !contains([]int{http.StatusOK, http.StatusAccepted}, resp.StatusCode) {
		h.beginUpdate.remove(req)
		return nil, &nonRetriableError{fmt.Errorf("unexpected status code %d. acceptable values are http.StatusOK, http.StatusAccepted", resp.StatusCode)}
	}
	if !server.PollerResponderMore(beginUpdate) {
		h.beginUpdate.remove(req)
	}

	return resp, nil
}
