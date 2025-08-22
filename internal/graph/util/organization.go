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

	"github.com/Azure/ARO-HCP/internal/graph/graphsdk/organization"
)

// Organization represents a Microsoft Entra organization
type Organization struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

// GetOrganization retrieves the current organization
func (c *Client) GetOrganization(ctx context.Context) (*Organization, error) {
	queryParams := &organization.OrganizationRequestBuilderGetQueryParameters{
		Select: []string{"id", "displayName"},
	}
	config := &organization.OrganizationRequestBuilderGetRequestConfiguration{
		QueryParameters: queryParams,
	}

	orgResponse, err := c.graphClient.Organization().Get(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("get organization: %w", err)
	}

	if len(orgResponse.GetValue()) == 0 {
		return nil, fmt.Errorf("no organizations returned")
	}

	org := orgResponse.GetValue()[0]
	return &Organization{
		ID:          *org.GetId(),
		DisplayName: *org.GetDisplayName(),
	}, nil
}
