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

package grafana

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/grafana-tools/sdk"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/tooling/grafanactl/internal/azure"
)

const (
	grafanaAPITimeout = 60 * time.Second
)

// Client provides methods to interact with Azure Managed Grafana instances.
type Client struct {
	credential    azcore.TokenCredential
	grafanaClient *sdk.Client
}

// NewClient creates a new authenticated Grafana client for the specified Azure Managed Grafana instance.
// It retrieves the Grafana endpoint, obtains an Azure AD token, and initializes the SDK client.
func NewClient(ctx context.Context, credential azcore.TokenCredential, managedGrafanaClient *azure.ManagedGrafanaClient, subscriptionID, resourceGroup, grafanaName string) (*Client, error) {
	endpoint, err := managedGrafanaClient.GetGrafanaEndpoint(ctx, subscriptionID, resourceGroup, grafanaName)
	if err != nil {
		return nil, fmt.Errorf("failed to get Grafana endpoint: %w", err)
	}

	token, err := getGrafanaAPIToken(ctx, credential)
	if err != nil {
		return nil, fmt.Errorf("failed to get API token: %w", err)
	}

	grafanaClient, err := sdk.NewClient(endpoint, token, &http.Client{
		Timeout: grafanaAPITimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Grafana SDK client: %w", err)
	}

	return &Client{
		credential:    credential,
		grafanaClient: grafanaClient,
	}, nil
}

func getGrafanaAPIToken(ctx context.Context, credential azcore.TokenCredential) (string, error) {
	// ce34e7e5-485f-4d76-964f-b3d2b16d1e4f is the well-known Azure Managed Grafana service application ID
	scope := "ce34e7e5-485f-4d76-964f-b3d2b16d1e4f/.default"

	token, err := credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{scope},
	})
	if err != nil {
		return "", fmt.Errorf("failed to get token for Grafana API (scope: %s): %w", scope, err)
	}

	return token.Token, nil
}

// ListDataSources returns all datasources configured in the Grafana instance.
func (c *Client) ListDataSources(ctx context.Context) ([]sdk.Datasource, error) {
	datasources, err := c.grafanaClient.GetAllDatasources(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get datasources: %w", err)
	}

	return datasources, nil
}

// DeleteDataSource removes a datasource from the Grafana instance by name.
func (c *Client) DeleteDataSource(ctx context.Context, dataSourceName string) error {
	_, err := c.grafanaClient.DeleteDatasourceByName(ctx, dataSourceName)
	if err != nil {
		return fmt.Errorf("failed to delete datasource: %w", err)
	}

	return nil
}
