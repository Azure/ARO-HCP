// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package framework

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	hcpsdk20251223preview "github.com/Azure/ARO-HCP/test/sdk/v20251223preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

type HCPClustersListByResourceGroupPager interface {
	More() bool
	NextPage(ctx context.Context) (hcpsdk20240610preview.HcpOpenShiftClustersClientListByResourceGroupResponse, error)
}

type HCPClustersCreateOrUpdatePoller interface {
	PollUntilDone(ctx context.Context, options *runtime.PollUntilDoneOptions) (hcpsdk20240610preview.HcpOpenShiftClustersClientCreateOrUpdateResponse, error)
	Poll(ctx context.Context) (*http.Response, error)
}

type HCPClustersUpdatePoller interface {
	PollUntilDone(ctx context.Context, options *runtime.PollUntilDoneOptions) (hcpsdk20240610preview.HcpOpenShiftClustersClientUpdateResponse, error)
}

type HCPClustersDeletePoller interface {
	PollUntilDone(ctx context.Context, options *runtime.PollUntilDoneOptions) (hcpsdk20240610preview.HcpOpenShiftClustersClientDeleteResponse, error)
}

type HCPClustersRequestAdminCredentialPoller interface {
	PollUntilDone(ctx context.Context, options *runtime.PollUntilDoneOptions) (hcpsdk20240610preview.HcpOpenShiftClustersClientRequestAdminCredentialResponse, error)
}

type HCPClustersClientFacade interface {
	Get(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, options *hcpsdk20240610preview.HcpOpenShiftClustersClientGetOptions) (hcpsdk20240610preview.HcpOpenShiftClustersClientGetResponse, error)
	BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, resource hcpsdk20240610preview.HcpOpenShiftCluster, options *hcpsdk20240610preview.HcpOpenShiftClustersClientBeginCreateOrUpdateOptions) (HCPClustersCreateOrUpdatePoller, error)
	BeginUpdate(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, properties hcpsdk20240610preview.HcpOpenShiftClusterUpdate, options *hcpsdk20240610preview.HcpOpenShiftClustersClientBeginUpdateOptions) (HCPClustersUpdatePoller, error)
	BeginDelete(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, options *hcpsdk20240610preview.HcpOpenShiftClustersClientBeginDeleteOptions) (HCPClustersDeletePoller, error)
	BeginRequestAdminCredential(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, options *hcpsdk20240610preview.HcpOpenShiftClustersClientBeginRequestAdminCredentialOptions) (HCPClustersRequestAdminCredentialPoller, error)
	NewListByResourceGroupPager(resourceGroupName string, options *hcpsdk20240610preview.HcpOpenShiftClustersClientListByResourceGroupOptions) HCPClustersListByResourceGroupPager
}

type NodePoolsCreateOrUpdatePoller interface {
	PollUntilDone(ctx context.Context, options *runtime.PollUntilDoneOptions) (hcpsdk20240610preview.NodePoolsClientCreateOrUpdateResponse, error)
}

type NodePoolsUpdatePoller interface {
	PollUntilDone(ctx context.Context, options *runtime.PollUntilDoneOptions) (hcpsdk20240610preview.NodePoolsClientUpdateResponse, error)
}

type NodePoolsDeletePoller interface {
	PollUntilDone(ctx context.Context, options *runtime.PollUntilDoneOptions) (hcpsdk20240610preview.NodePoolsClientDeleteResponse, error)
}

type NodePoolsListByParentPager interface {
	More() bool
	NextPage(ctx context.Context) (hcpsdk20240610preview.NodePoolsClientListByParentResponse, error)
}

type NodePoolsClientFacade interface {
	Get(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, nodePoolName string, options *hcpsdk20240610preview.NodePoolsClientGetOptions) (hcpsdk20240610preview.NodePoolsClientGetResponse, error)
	BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, nodePoolName string, resource hcpsdk20240610preview.NodePool, options *hcpsdk20240610preview.NodePoolsClientBeginCreateOrUpdateOptions) (NodePoolsCreateOrUpdatePoller, error)
	BeginUpdate(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, nodePoolName string, properties hcpsdk20240610preview.NodePoolUpdate, options *hcpsdk20240610preview.NodePoolsClientBeginUpdateOptions) (NodePoolsUpdatePoller, error)
	BeginDelete(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, nodePoolName string, options *hcpsdk20240610preview.NodePoolsClientBeginDeleteOptions) (NodePoolsDeletePoller, error)
	NewListByParentPager(resourceGroupName string, hcpOpenShiftClusterName string, options *hcpsdk20240610preview.NodePoolsClientListByParentOptions) NodePoolsListByParentPager
}

type hcpClustersClient20240610Adapter struct {
	client *hcpsdk20240610preview.HcpOpenShiftClustersClient
}

func newHCPClustersClient20240610Facade(client *hcpsdk20240610preview.HcpOpenShiftClustersClient) HCPClustersClientFacade {
	return &hcpClustersClient20240610Adapter{client: client}
}

func (a *hcpClustersClient20240610Adapter) Get(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, options *hcpsdk20240610preview.HcpOpenShiftClustersClientGetOptions) (hcpsdk20240610preview.HcpOpenShiftClustersClientGetResponse, error) {
	return a.client.Get(ctx, resourceGroupName, hcpOpenShiftClusterName, options)
}

func (a *hcpClustersClient20240610Adapter) BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, resource hcpsdk20240610preview.HcpOpenShiftCluster, options *hcpsdk20240610preview.HcpOpenShiftClustersClientBeginCreateOrUpdateOptions) (HCPClustersCreateOrUpdatePoller, error) {
	return a.client.BeginCreateOrUpdate(ctx, resourceGroupName, hcpOpenShiftClusterName, resource, options)
}

func (a *hcpClustersClient20240610Adapter) BeginUpdate(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, properties hcpsdk20240610preview.HcpOpenShiftClusterUpdate, options *hcpsdk20240610preview.HcpOpenShiftClustersClientBeginUpdateOptions) (HCPClustersUpdatePoller, error) {
	return a.client.BeginUpdate(ctx, resourceGroupName, hcpOpenShiftClusterName, properties, options)
}

func (a *hcpClustersClient20240610Adapter) BeginDelete(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, options *hcpsdk20240610preview.HcpOpenShiftClustersClientBeginDeleteOptions) (HCPClustersDeletePoller, error) {
	return a.client.BeginDelete(ctx, resourceGroupName, hcpOpenShiftClusterName, options)
}

func (a *hcpClustersClient20240610Adapter) BeginRequestAdminCredential(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, options *hcpsdk20240610preview.HcpOpenShiftClustersClientBeginRequestAdminCredentialOptions) (HCPClustersRequestAdminCredentialPoller, error) {
	return a.client.BeginRequestAdminCredential(ctx, resourceGroupName, hcpOpenShiftClusterName, options)
}

func (a *hcpClustersClient20240610Adapter) NewListByResourceGroupPager(resourceGroupName string, options *hcpsdk20240610preview.HcpOpenShiftClustersClientListByResourceGroupOptions) HCPClustersListByResourceGroupPager {
	return &hcpClustersPager20240610Adapter{pager: a.client.NewListByResourceGroupPager(resourceGroupName, options)}
}

type hcpClustersPager20240610Adapter struct {
	pager *runtime.Pager[hcpsdk20240610preview.HcpOpenShiftClustersClientListByResourceGroupResponse]
}

func (a *hcpClustersPager20240610Adapter) More() bool { return a.pager.More() }
func (a *hcpClustersPager20240610Adapter) NextPage(ctx context.Context) (hcpsdk20240610preview.HcpOpenShiftClustersClientListByResourceGroupResponse, error) {
	return a.pager.NextPage(ctx)
}

type hcpClustersClient20251223Adapter struct {
	client *hcpsdk20251223preview.HcpOpenShiftClustersClient
}

func (a *hcpClustersClient20251223Adapter) Get(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, _ *hcpsdk20240610preview.HcpOpenShiftClustersClientGetOptions) (hcpsdk20240610preview.HcpOpenShiftClustersClientGetResponse, error) {
	resp, err := a.client.Get(ctx, resourceGroupName, hcpOpenShiftClusterName, nil)
	if err != nil {
		return hcpsdk20240610preview.HcpOpenShiftClustersClientGetResponse{}, err
	}
	converted, err := convertPermissive[hcpsdk20240610preview.HcpOpenShiftClustersClientGetResponse](resp)
	if err != nil {
		return hcpsdk20240610preview.HcpOpenShiftClustersClientGetResponse{}, err
	}
	return *converted, nil
}

func (a *hcpClustersClient20251223Adapter) BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, resource hcpsdk20240610preview.HcpOpenShiftCluster, options *hcpsdk20240610preview.HcpOpenShiftClustersClientBeginCreateOrUpdateOptions) (HCPClustersCreateOrUpdatePoller, error) {
	resource51223, err := convertPermissive[hcpsdk20251223preview.HcpOpenShiftCluster](resource)
	if err != nil {
		return nil, err
	}
	var options51223 *hcpsdk20251223preview.HcpOpenShiftClustersClientBeginCreateOrUpdateOptions
	if options != nil {
		options51223 = &hcpsdk20251223preview.HcpOpenShiftClustersClientBeginCreateOrUpdateOptions{ResumeToken: options.ResumeToken}
	}
	poller, err := a.client.BeginCreateOrUpdate(ctx, resourceGroupName, hcpOpenShiftClusterName, *resource51223, options51223)
	if err != nil {
		return nil, err
	}
	return &hcpClustersCreateOrUpdatePoller20251223Adapter{poller: poller}, nil
}

func (a *hcpClustersClient20251223Adapter) BeginUpdate(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, properties hcpsdk20240610preview.HcpOpenShiftClusterUpdate, options *hcpsdk20240610preview.HcpOpenShiftClustersClientBeginUpdateOptions) (HCPClustersUpdatePoller, error) {
	update51223, err := convertPermissive[hcpsdk20251223preview.HcpOpenShiftClusterUpdate](properties)
	if err != nil {
		return nil, err
	}
	var options51223 *hcpsdk20251223preview.HcpOpenShiftClustersClientBeginUpdateOptions
	if options != nil {
		options51223 = &hcpsdk20251223preview.HcpOpenShiftClustersClientBeginUpdateOptions{ResumeToken: options.ResumeToken}
	}
	poller, err := a.client.BeginUpdate(ctx, resourceGroupName, hcpOpenShiftClusterName, *update51223, options51223)
	if err != nil {
		return nil, err
	}
	return &hcpClustersUpdatePoller20251223Adapter{poller: poller}, nil
}

func (a *hcpClustersClient20251223Adapter) BeginDelete(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, options *hcpsdk20240610preview.HcpOpenShiftClustersClientBeginDeleteOptions) (HCPClustersDeletePoller, error) {
	var options51223 *hcpsdk20251223preview.HcpOpenShiftClustersClientBeginDeleteOptions
	if options != nil {
		options51223 = &hcpsdk20251223preview.HcpOpenShiftClustersClientBeginDeleteOptions{ResumeToken: options.ResumeToken}
	}
	poller, err := a.client.BeginDelete(ctx, resourceGroupName, hcpOpenShiftClusterName, options51223)
	if err != nil {
		return nil, err
	}
	return &hcpClustersDeletePoller20251223Adapter{poller: poller}, nil
}

func (a *hcpClustersClient20251223Adapter) BeginRequestAdminCredential(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, options *hcpsdk20240610preview.HcpOpenShiftClustersClientBeginRequestAdminCredentialOptions) (HCPClustersRequestAdminCredentialPoller, error) {
	var options51223 *hcpsdk20251223preview.HcpOpenShiftClustersClientBeginRequestAdminCredentialOptions
	if options != nil {
		options51223 = &hcpsdk20251223preview.HcpOpenShiftClustersClientBeginRequestAdminCredentialOptions{
			ResumeToken: options.ResumeToken,
		}
	}
	poller, err := a.client.BeginRequestAdminCredential(ctx, resourceGroupName, hcpOpenShiftClusterName, options51223)
	if err != nil {
		return nil, err
	}
	return &hcpClustersRequestAdminCredentialPoller20251223Adapter{poller: poller}, nil
}

func (a *hcpClustersClient20251223Adapter) NewListByResourceGroupPager(resourceGroupName string, _ *hcpsdk20240610preview.HcpOpenShiftClustersClientListByResourceGroupOptions) HCPClustersListByResourceGroupPager {
	return &hcpClustersPager20251223Adapter{pager: a.client.NewListByResourceGroupPager(resourceGroupName, nil)}
}

type hcpClustersPager20251223Adapter struct {
	pager *runtime.Pager[hcpsdk20251223preview.HcpOpenShiftClustersClientListByResourceGroupResponse]
}

func (a *hcpClustersPager20251223Adapter) More() bool { return a.pager.More() }
func (a *hcpClustersPager20251223Adapter) NextPage(ctx context.Context) (hcpsdk20240610preview.HcpOpenShiftClustersClientListByResourceGroupResponse, error) {
	resp, err := a.pager.NextPage(ctx)
	if err != nil {
		return hcpsdk20240610preview.HcpOpenShiftClustersClientListByResourceGroupResponse{}, err
	}
	converted, err := convertPermissive[hcpsdk20240610preview.HcpOpenShiftClustersClientListByResourceGroupResponse](resp)
	if err != nil {
		return hcpsdk20240610preview.HcpOpenShiftClustersClientListByResourceGroupResponse{}, err
	}
	return *converted, nil
}

type hcpClustersCreateOrUpdatePoller20251223Adapter struct {
	poller *runtime.Poller[hcpsdk20251223preview.HcpOpenShiftClustersClientCreateOrUpdateResponse]
}

func (a *hcpClustersCreateOrUpdatePoller20251223Adapter) PollUntilDone(ctx context.Context, options *runtime.PollUntilDoneOptions) (hcpsdk20240610preview.HcpOpenShiftClustersClientCreateOrUpdateResponse, error) {
	resp, err := a.poller.PollUntilDone(ctx, options)
	if err != nil {
		return hcpsdk20240610preview.HcpOpenShiftClustersClientCreateOrUpdateResponse{}, err
	}
	converted, err := convertPermissive[hcpsdk20240610preview.HcpOpenShiftClustersClientCreateOrUpdateResponse](resp)
	if err != nil {
		return hcpsdk20240610preview.HcpOpenShiftClustersClientCreateOrUpdateResponse{}, err
	}
	return *converted, nil
}

func (a *hcpClustersCreateOrUpdatePoller20251223Adapter) Poll(ctx context.Context) (*http.Response, error) {
	return a.poller.Poll(ctx)
}

type hcpClustersUpdatePoller20251223Adapter struct {
	poller *runtime.Poller[hcpsdk20251223preview.HcpOpenShiftClustersClientUpdateResponse]
}

func (a *hcpClustersUpdatePoller20251223Adapter) PollUntilDone(ctx context.Context, options *runtime.PollUntilDoneOptions) (hcpsdk20240610preview.HcpOpenShiftClustersClientUpdateResponse, error) {
	resp, err := a.poller.PollUntilDone(ctx, options)
	if err != nil {
		return hcpsdk20240610preview.HcpOpenShiftClustersClientUpdateResponse{}, err
	}
	converted, err := convertPermissive[hcpsdk20240610preview.HcpOpenShiftClustersClientUpdateResponse](resp)
	if err != nil {
		return hcpsdk20240610preview.HcpOpenShiftClustersClientUpdateResponse{}, err
	}
	return *converted, nil
}

type hcpClustersDeletePoller20251223Adapter struct {
	poller *runtime.Poller[hcpsdk20251223preview.HcpOpenShiftClustersClientDeleteResponse]
}

func (a *hcpClustersDeletePoller20251223Adapter) PollUntilDone(ctx context.Context, options *runtime.PollUntilDoneOptions) (hcpsdk20240610preview.HcpOpenShiftClustersClientDeleteResponse, error) {
	resp, err := a.poller.PollUntilDone(ctx, options)
	if err != nil {
		return hcpsdk20240610preview.HcpOpenShiftClustersClientDeleteResponse{}, err
	}
	converted, err := convertPermissive[hcpsdk20240610preview.HcpOpenShiftClustersClientDeleteResponse](resp)
	if err != nil {
		return hcpsdk20240610preview.HcpOpenShiftClustersClientDeleteResponse{}, err
	}
	return *converted, nil
}

type hcpClustersRequestAdminCredentialPoller20251223Adapter struct {
	poller *runtime.Poller[hcpsdk20251223preview.HcpOpenShiftClustersClientRequestAdminCredentialResponse]
}

func (a *hcpClustersRequestAdminCredentialPoller20251223Adapter) PollUntilDone(ctx context.Context, options *runtime.PollUntilDoneOptions) (hcpsdk20240610preview.HcpOpenShiftClustersClientRequestAdminCredentialResponse, error) {
	resp, err := a.poller.PollUntilDone(ctx, options)
	if err != nil {
		return hcpsdk20240610preview.HcpOpenShiftClustersClientRequestAdminCredentialResponse{}, err
	}
	converted, err := convertPermissive[hcpsdk20240610preview.HcpOpenShiftClustersClientRequestAdminCredentialResponse](resp)
	if err != nil {
		return hcpsdk20240610preview.HcpOpenShiftClustersClientRequestAdminCredentialResponse{}, err
	}
	return *converted, nil
}

type nodePoolsClient20240610Adapter struct {
	client *hcpsdk20240610preview.NodePoolsClient
}

func newNodePoolsClient20240610Facade(client *hcpsdk20240610preview.NodePoolsClient) NodePoolsClientFacade {
	return &nodePoolsClient20240610Adapter{client: client}
}

func (a *nodePoolsClient20240610Adapter) Get(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, nodePoolName string, options *hcpsdk20240610preview.NodePoolsClientGetOptions) (hcpsdk20240610preview.NodePoolsClientGetResponse, error) {
	return a.client.Get(ctx, resourceGroupName, hcpOpenShiftClusterName, nodePoolName, options)
}

func (a *nodePoolsClient20240610Adapter) BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, nodePoolName string, resource hcpsdk20240610preview.NodePool, options *hcpsdk20240610preview.NodePoolsClientBeginCreateOrUpdateOptions) (NodePoolsCreateOrUpdatePoller, error) {
	return a.client.BeginCreateOrUpdate(ctx, resourceGroupName, hcpOpenShiftClusterName, nodePoolName, resource, options)
}

func (a *nodePoolsClient20240610Adapter) BeginUpdate(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, nodePoolName string, properties hcpsdk20240610preview.NodePoolUpdate, options *hcpsdk20240610preview.NodePoolsClientBeginUpdateOptions) (NodePoolsUpdatePoller, error) {
	return a.client.BeginUpdate(ctx, resourceGroupName, hcpOpenShiftClusterName, nodePoolName, properties, options)
}

func (a *nodePoolsClient20240610Adapter) BeginDelete(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, nodePoolName string, options *hcpsdk20240610preview.NodePoolsClientBeginDeleteOptions) (NodePoolsDeletePoller, error) {
	return a.client.BeginDelete(ctx, resourceGroupName, hcpOpenShiftClusterName, nodePoolName, options)
}

func (a *nodePoolsClient20240610Adapter) NewListByParentPager(resourceGroupName string, hcpOpenShiftClusterName string, options *hcpsdk20240610preview.NodePoolsClientListByParentOptions) NodePoolsListByParentPager {
	return &nodePoolsListByParentPager20240610Adapter{pager: a.client.NewListByParentPager(resourceGroupName, hcpOpenShiftClusterName, options)}
}

type nodePoolsListByParentPager20240610Adapter struct {
	pager *runtime.Pager[hcpsdk20240610preview.NodePoolsClientListByParentResponse]
}

func (a *nodePoolsListByParentPager20240610Adapter) More() bool { return a.pager.More() }
func (a *nodePoolsListByParentPager20240610Adapter) NextPage(ctx context.Context) (hcpsdk20240610preview.NodePoolsClientListByParentResponse, error) {
	return a.pager.NextPage(ctx)
}

type nodePoolsClient20251223Adapter struct {
	client *hcpsdk20251223preview.NodePoolsClient
}

func (a *nodePoolsClient20251223Adapter) Get(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, nodePoolName string, _ *hcpsdk20240610preview.NodePoolsClientGetOptions) (hcpsdk20240610preview.NodePoolsClientGetResponse, error) {
	resp, err := a.client.Get(ctx, resourceGroupName, hcpOpenShiftClusterName, nodePoolName, nil)
	if err != nil {
		return hcpsdk20240610preview.NodePoolsClientGetResponse{}, err
	}
	converted, err := convertPermissive[hcpsdk20240610preview.NodePoolsClientGetResponse](resp)
	if err != nil {
		return hcpsdk20240610preview.NodePoolsClientGetResponse{}, err
	}
	return *converted, nil
}

func (a *nodePoolsClient20251223Adapter) BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, nodePoolName string, resource hcpsdk20240610preview.NodePool, options *hcpsdk20240610preview.NodePoolsClientBeginCreateOrUpdateOptions) (NodePoolsCreateOrUpdatePoller, error) {
	resource51223, err := convertPermissive[hcpsdk20251223preview.NodePool](resource)
	if err != nil {
		return nil, err
	}
	var options51223 *hcpsdk20251223preview.NodePoolsClientBeginCreateOrUpdateOptions
	if options != nil {
		options51223 = &hcpsdk20251223preview.NodePoolsClientBeginCreateOrUpdateOptions{ResumeToken: options.ResumeToken}
	}
	poller, err := a.client.BeginCreateOrUpdate(ctx, resourceGroupName, hcpOpenShiftClusterName, nodePoolName, *resource51223, options51223)
	if err != nil {
		return nil, err
	}
	return &nodePoolsCreateOrUpdatePoller20251223Adapter{poller: poller}, nil
}

func (a *nodePoolsClient20251223Adapter) BeginUpdate(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, nodePoolName string, properties hcpsdk20240610preview.NodePoolUpdate, options *hcpsdk20240610preview.NodePoolsClientBeginUpdateOptions) (NodePoolsUpdatePoller, error) {
	update51223, err := convertPermissive[hcpsdk20251223preview.NodePoolUpdate](properties)
	if err != nil {
		return nil, err
	}
	var options51223 *hcpsdk20251223preview.NodePoolsClientBeginUpdateOptions
	if options != nil {
		options51223 = &hcpsdk20251223preview.NodePoolsClientBeginUpdateOptions{ResumeToken: options.ResumeToken}
	}
	poller, err := a.client.BeginUpdate(ctx, resourceGroupName, hcpOpenShiftClusterName, nodePoolName, *update51223, options51223)
	if err != nil {
		return nil, err
	}
	return &nodePoolsUpdatePoller20251223Adapter{poller: poller}, nil
}

func (a *nodePoolsClient20251223Adapter) BeginDelete(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, nodePoolName string, options *hcpsdk20240610preview.NodePoolsClientBeginDeleteOptions) (NodePoolsDeletePoller, error) {
	var options51223 *hcpsdk20251223preview.NodePoolsClientBeginDeleteOptions
	if options != nil {
		options51223 = &hcpsdk20251223preview.NodePoolsClientBeginDeleteOptions{ResumeToken: options.ResumeToken}
	}
	poller, err := a.client.BeginDelete(ctx, resourceGroupName, hcpOpenShiftClusterName, nodePoolName, options51223)
	if err != nil {
		return nil, err
	}
	return &nodePoolsDeletePoller20251223Adapter{poller: poller}, nil
}

func (a *nodePoolsClient20251223Adapter) NewListByParentPager(resourceGroupName string, hcpOpenShiftClusterName string, _ *hcpsdk20240610preview.NodePoolsClientListByParentOptions) NodePoolsListByParentPager {
	return &nodePoolsListByParentPager20251223Adapter{pager: a.client.NewListByParentPager(resourceGroupName, hcpOpenShiftClusterName, nil)}
}

type nodePoolsListByParentPager20251223Adapter struct {
	pager *runtime.Pager[hcpsdk20251223preview.NodePoolsClientListByParentResponse]
}

func (a *nodePoolsListByParentPager20251223Adapter) More() bool { return a.pager.More() }
func (a *nodePoolsListByParentPager20251223Adapter) NextPage(ctx context.Context) (hcpsdk20240610preview.NodePoolsClientListByParentResponse, error) {
	resp, err := a.pager.NextPage(ctx)
	if err != nil {
		return hcpsdk20240610preview.NodePoolsClientListByParentResponse{}, err
	}
	converted, err := convertPermissive[hcpsdk20240610preview.NodePoolsClientListByParentResponse](resp)
	if err != nil {
		return hcpsdk20240610preview.NodePoolsClientListByParentResponse{}, err
	}
	return *converted, nil
}

type nodePoolsCreateOrUpdatePoller20251223Adapter struct {
	poller *runtime.Poller[hcpsdk20251223preview.NodePoolsClientCreateOrUpdateResponse]
}

func (a *nodePoolsCreateOrUpdatePoller20251223Adapter) PollUntilDone(ctx context.Context, options *runtime.PollUntilDoneOptions) (hcpsdk20240610preview.NodePoolsClientCreateOrUpdateResponse, error) {
	resp, err := a.poller.PollUntilDone(ctx, options)
	if err != nil {
		return hcpsdk20240610preview.NodePoolsClientCreateOrUpdateResponse{}, err
	}
	converted, err := convertPermissive[hcpsdk20240610preview.NodePoolsClientCreateOrUpdateResponse](resp)
	if err != nil {
		return hcpsdk20240610preview.NodePoolsClientCreateOrUpdateResponse{}, err
	}
	return *converted, nil
}

type nodePoolsUpdatePoller20251223Adapter struct {
	poller *runtime.Poller[hcpsdk20251223preview.NodePoolsClientUpdateResponse]
}

func (a *nodePoolsUpdatePoller20251223Adapter) PollUntilDone(ctx context.Context, options *runtime.PollUntilDoneOptions) (hcpsdk20240610preview.NodePoolsClientUpdateResponse, error) {
	resp, err := a.poller.PollUntilDone(ctx, options)
	if err != nil {
		return hcpsdk20240610preview.NodePoolsClientUpdateResponse{}, err
	}
	converted, err := convertPermissive[hcpsdk20240610preview.NodePoolsClientUpdateResponse](resp)
	if err != nil {
		return hcpsdk20240610preview.NodePoolsClientUpdateResponse{}, err
	}
	return *converted, nil
}

type nodePoolsDeletePoller20251223Adapter struct {
	poller *runtime.Poller[hcpsdk20251223preview.NodePoolsClientDeleteResponse]
}

func (a *nodePoolsDeletePoller20251223Adapter) PollUntilDone(ctx context.Context, options *runtime.PollUntilDoneOptions) (hcpsdk20240610preview.NodePoolsClientDeleteResponse, error) {
	resp, err := a.poller.PollUntilDone(ctx, options)
	if err != nil {
		return hcpsdk20240610preview.NodePoolsClientDeleteResponse{}, err
	}
	converted, err := convertPermissive[hcpsdk20240610preview.NodePoolsClientDeleteResponse](resp)
	if err != nil {
		return hcpsdk20240610preview.NodePoolsClientDeleteResponse{}, err
	}
	return *converted, nil
}

func convertPermissive[T any](src any) (*T, error) {
	b, err := json.Marshal(src)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal for type conversion: %w", err)
	}
	var dst T
	decoder := json.NewDecoder(bytes.NewReader(b))
	if err = decoder.Decode(&dst); err != nil {
		return nil, fmt.Errorf("failed to unmarshal for type conversion: %w", err)
	}
	return &dst, nil
}
