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

package databasemutationhelpers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

type HTTPTestAccessor interface {
	Get(ctx context.Context, resourceIDString string) (any, error)
	List(ctx context.Context, parentResourceIDString string) ([]any, error)
	CreateOrUpdate(ctx context.Context, resourceIDString string, content []byte) error
	Patch(ctx context.Context, resourceIDString string, content []byte) error
	Delete(ctx context.Context, resourceIDString string) error
}

type frontendHTTPTestAccessor struct {
	frontEndURL    string
	frontendClient *hcpsdk20240610preview.ClientFactory
}

func newFrontendHTTPTestAccessor(frontEndURL string, frontendClient *hcpsdk20240610preview.ClientFactory) *frontendHTTPTestAccessor {
	return &frontendHTTPTestAccessor{
		frontEndURL:    frontEndURL,
		frontendClient: frontendClient,
	}
}

var _ HTTPTestAccessor = &frontendHTTPTestAccessor{}

func (c frontendHTTPTestAccessor) Get(ctx context.Context, resourceIDString string) (any, error) {
	resourceID, err := azcorearm.ParseResourceID(resourceIDString)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	switch strings.ToLower(resourceID.ResourceType.String()) {
	case strings.ToLower(api.ClusterResourceType.String()):
		return c.frontendClient.NewHcpOpenShiftClustersClient().Get(ctx, resourceID.ResourceGroupName, resourceID.Name, nil)

	case strings.ToLower(api.NodePoolResourceType.String()):
		return c.frontendClient.NewNodePoolsClient().Get(ctx, resourceID.ResourceGroupName, resourceID.Parent.Name, resourceID.Name, nil)

	case strings.ToLower(api.ExternalAuthResourceType.String()):
		return c.frontendClient.NewExternalAuthsClient().Get(ctx, resourceID.ResourceGroupName, resourceID.Parent.Name, resourceID.Name, nil)

	case strings.ToLower(api.OperationStatusResourceType.String()):
		parts := []string{
			"/subscriptions",
			resourceID.SubscriptionID,
			"providers",
			api.ProviderNamespace,
			"locations",
			"fake-location",
			api.OperationStatusResourceType.Type,
			resourceID.Name,
		}
		fullURL := c.frontEndURL + strings.Join(parts, "/") + "?api-version=2024-06-10-preview"
		req, err := http.NewRequest(http.MethodGet, fullURL, nil)
		if err != nil {
			return nil, utils.TrackError(err)
		}
		req = req.WithContext(ctx)
		req.Header.Set(arm.HeaderNameHomeTenantID, api.TestTenantID)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, utils.TrackError(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			return nil, utils.TrackError(fmt.Errorf("expected 200 status code, got %d", resp.StatusCode))
		}
		contentBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, utils.TrackError(err)
		}
		ret := &arm.Operation{}
		if err := json.Unmarshal(contentBytes, ret); err != nil {
			return nil, utils.TrackError(err)
		}

		return ret, nil

	default:
		return "", utils.TrackError(fmt.Errorf("unknown resource type: %s", resourceID.ResourceType.String()))
	}
}

func (c frontendHTTPTestAccessor) List(ctx context.Context, exemplarResourceIDString string) ([]any, error) {
	exemplarResourceID, err := azcorearm.ParseResourceID(exemplarResourceIDString)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	switch strings.ToLower(exemplarResourceID.ResourceType.String()) {
	case strings.ToLower(api.ClusterResourceType.String()):
		pager := c.frontendClient.NewHcpOpenShiftClustersClient().NewListByResourceGroupPager(exemplarResourceID.ResourceGroupName, nil)
		ret := []any{}
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, utils.TrackError(err)
			}
			for i := range page.Value {
				ret = append(ret, page.Value[i])
			}
		}
		return ret, nil

	case strings.ToLower(api.NodePoolResourceType.String()):
		pager := c.frontendClient.NewNodePoolsClient().NewListByParentPager(exemplarResourceID.ResourceGroupName, exemplarResourceID.Parent.Name, nil)
		ret := []any{}
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, utils.TrackError(err)
			}
			for i := range page.Value {
				ret = append(ret, page.Value[i])
			}
		}
		return ret, nil

	case strings.ToLower(api.ExternalAuthResourceType.String()):
		pager := c.frontendClient.NewExternalAuthsClient().NewListByParentPager(exemplarResourceID.ResourceGroupName, exemplarResourceID.Parent.Name, nil)
		ret := []any{}
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, utils.TrackError(err)
			}
			for i := range page.Value {
				ret = append(ret, page.Value[i])
			}
		}
		return ret, nil

	default:
		return nil, utils.TrackError(fmt.Errorf("unknown resource type: %s", exemplarResourceID.ResourceType.String()))
	}
}

func (c frontendHTTPTestAccessor) CreateOrUpdate(ctx context.Context, resourceIDString string, content []byte) error {
	resourceID, err := azcorearm.ParseResourceID(resourceIDString)
	if err != nil {
		return utils.TrackError(err)
	}

	switch strings.ToLower(resourceID.ResourceType.String()) {
	case strings.ToLower(api.ClusterResourceType.String()):
		obj := hcpsdk20240610preview.HcpOpenShiftCluster{}
		if err := json.Unmarshal(content, &obj); err != nil {
			return utils.TrackError(err)
		}
		_, err := c.frontendClient.NewHcpOpenShiftClustersClient().BeginCreateOrUpdate(ctx, resourceID.ResourceGroupName, resourceID.Name, obj, nil)
		if err != nil {
			return utils.TrackError(err)
		}
		return nil

	case strings.ToLower(api.NodePoolResourceType.String()):
		obj := hcpsdk20240610preview.NodePool{}
		if err := json.Unmarshal(content, &obj); err != nil {
			return utils.TrackError(err)
		}
		_, err := c.frontendClient.NewNodePoolsClient().BeginCreateOrUpdate(ctx, resourceID.ResourceGroupName, resourceID.Parent.Name, resourceID.Name, obj, nil)
		if err != nil {
			return utils.TrackError(err)
		}
		return nil

	case strings.ToLower(api.ExternalAuthResourceType.String()):
		obj := hcpsdk20240610preview.ExternalAuth{}
		if err := json.Unmarshal(content, &obj); err != nil {
			return utils.TrackError(err)
		}
		_, err := c.frontendClient.NewExternalAuthsClient().BeginCreateOrUpdate(ctx, resourceID.ResourceGroupName, resourceID.Parent.Name, resourceID.Name, obj, nil)
		if err != nil {
			return utils.TrackError(err)
		}
		return nil

	case strings.ToLower(azcorearm.SubscriptionResourceType.String()):
		fullURL := c.frontEndURL + path.Join("/subscriptions", resourceID.Name)
		req, err := http.NewRequest("PUT", fullURL, bytes.NewReader(content))
		if err != nil {
			return utils.TrackError(err)
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			return utils.TrackError(err)
		}
		if response.StatusCode != 200 {
			return utils.TrackError(fmt.Errorf("expected 200 status code, got %d", response.StatusCode))
		}
		//responseBytes, err := httputil.DumpResponse(response, true)
		//if err != nil {
		//	return utils.TrackError(err)
		//}
		//fmt.Printf("%s", string(responseBytes))

		return nil

	default:
		return utils.TrackError(fmt.Errorf("unknown resource type: %s", resourceID.ResourceType.String()))
	}
}

func (c frontendHTTPTestAccessor) Patch(ctx context.Context, resourceIDString string, content []byte) error {
	resourceID, err := azcorearm.ParseResourceID(resourceIDString)
	if err != nil {
		return utils.TrackError(err)
	}

	switch strings.ToLower(resourceID.ResourceType.String()) {
	case strings.ToLower(api.ClusterResourceType.String()):
		obj := hcpsdk20240610preview.HcpOpenShiftClusterUpdate{}
		if err := json.Unmarshal(content, &obj); err != nil {
			return utils.TrackError(err)
		}
		_, err := c.frontendClient.NewHcpOpenShiftClustersClient().BeginUpdate(ctx, resourceID.ResourceGroupName, resourceID.Name, obj, nil)
		if err != nil {
			return utils.TrackError(err)
		}
		return nil

	case strings.ToLower(api.NodePoolResourceType.String()):
		obj := hcpsdk20240610preview.NodePoolUpdate{}
		if err := json.Unmarshal(content, &obj); err != nil {
			return utils.TrackError(err)
		}
		_, err := c.frontendClient.NewNodePoolsClient().BeginUpdate(ctx, resourceID.ResourceGroupName, resourceID.Parent.Name, resourceID.Name, obj, nil)
		if err != nil {
			return utils.TrackError(err)
		}
		return nil

	case strings.ToLower(api.ExternalAuthResourceType.String()):
		obj := hcpsdk20240610preview.ExternalAuthUpdate{}
		if err := json.Unmarshal(content, &obj); err != nil {
			return utils.TrackError(err)
		}
		_, err := c.frontendClient.NewExternalAuthsClient().BeginUpdate(ctx, resourceID.ResourceGroupName, resourceID.Parent.Name, resourceID.Name, obj, nil)
		if err != nil {
			return utils.TrackError(err)
		}
		return nil

	default:
		return utils.TrackError(fmt.Errorf("unknown resource type: %s", resourceID.ResourceType.String()))
	}
}

func (c frontendHTTPTestAccessor) Delete(ctx context.Context, resourceIDString string) error {
	resourceID, err := azcorearm.ParseResourceID(resourceIDString)
	if err != nil {
		return utils.TrackError(err)
	}

	switch strings.ToLower(resourceID.ResourceType.String()) {
	case strings.ToLower(api.ClusterResourceType.String()):
		_, err := c.frontendClient.NewHcpOpenShiftClustersClient().BeginDelete(ctx, resourceID.ResourceGroupName, resourceID.Name, nil)
		return utils.TrackError(err)

	case strings.ToLower(api.NodePoolResourceType.String()):
		_, err := c.frontendClient.NewNodePoolsClient().BeginDelete(ctx, resourceID.ResourceGroupName, resourceID.Parent.Name, resourceID.Name, nil)
		return utils.TrackError(err)

	case strings.ToLower(api.ExternalAuthResourceType.String()):
		_, err := c.frontendClient.NewExternalAuthsClient().BeginDelete(ctx, resourceID.ResourceGroupName, resourceID.Parent.Name, resourceID.Name, nil)
		return utils.TrackError(err)

	default:
		return utils.TrackError(fmt.Errorf("unknown resource type: %s", resourceID.ResourceType.String()))
	}
}
