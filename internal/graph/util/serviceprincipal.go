// Copyright 2025 Microsoft Corporation
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

package util

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/internal/graph/graphsdk/models"
	"github.com/Azure/ARO-HCP/internal/graph/graphsdk/serviceprincipals"
)

// ServicePrincipal represents a Microsoft Entra service principal
type ServicePrincipal struct {
	ID          string `json:"id"`
	AppID       string `json:"appId"`
	DisplayName string `json:"displayName"`
}

// CreateServicePrincipal creates a new Microsoft Entra service principal.
// Transient errors and eventual-consistency delays are retried by the
// transport-level graphRetryHandler middleware.
func (c *Client) CreateServicePrincipal(ctx context.Context, appId string) (*ServicePrincipal, error) {
	sp := models.NewServicePrincipal()
	sp.SetAppId(&appId)

	createdSp, err := c.graphClient.ServicePrincipals().Post(ctx, sp, nil)
	if err != nil {
		return nil, fmt.Errorf("create service principal for appId %q: %w", appId, odataErrorWithDiagnostics(err))
	}

	return &ServicePrincipal{
		ID:    *createdSp.GetId(),
		AppID: *createdSp.GetAppId(),
	}, nil
}

// DeleteServicePrincipal soft-deletes a service principal.
func (c *Client) DeleteServicePrincipal(ctx context.Context, servicePrincipalID string) error {
	err := c.graphClient.ServicePrincipals().ByServicePrincipalId(servicePrincipalID).Delete(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete service principal: %w", odataErrorWithDiagnostics(err))
	}
	return nil
}

// GetServicePrincipalByAppID returns the service principal associated with an
// application ID. A nil result means no active service principal exists.
func (c *Client) GetServicePrincipalByAppID(ctx context.Context, appID string) (*ServicePrincipal, error) {
	response, err := c.graphClient.ServicePrincipals().Get(ctx, &serviceprincipals.ServicePrincipalsRequestBuilderGetRequestConfiguration{
		QueryParameters: &serviceprincipals.ServicePrincipalsRequestBuilderGetQueryParameters{
			Filter: to.Ptr(fmt.Sprintf("appId eq '%s'", appID)),
			Select: []string{"id", "appId"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("get service principal for appId %q: %w", appID, odataErrorWithDiagnostics(err))
	}

	values := response.GetValue()
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) > 1 {
		return nil, fmt.Errorf("found %d service principals for appId %q", len(values), appID)
	}

	value := values[0]
	if value.GetId() == nil || value.GetAppId() == nil {
		return nil, fmt.Errorf("service principal for appId %q has an incomplete response", appID)
	}

	servicePrincipal := &ServicePrincipal{
		ID:    *value.GetId(),
		AppID: *value.GetAppId(),
	}
	return servicePrincipal, nil
}
