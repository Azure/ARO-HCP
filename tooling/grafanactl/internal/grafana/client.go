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
	"time"

	"github.com/go-logr/logr"
	"github.com/grafana-tools/sdk"
	"github.com/hashicorp/go-retryablehttp"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/tooling/grafanactl/internal/azure"
)

const (
	grafanaAPITimeout  = 60 * time.Second
	grafanaAPIRetryMax = 5
)

// Client provides methods to interact with Azure Managed Grafana instances.
type Client struct {
	grafanaClient *sdk.Client
}

type retryableLogger struct {
	logger logr.Logger
}

func (l *retryableLogger) Printf(format string, v ...interface{}) {
	l.logger.V(2).Info(fmt.Sprintf(format, v...))
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

	httpClient := retryablehttp.NewClient()
	httpClient.RetryMax = grafanaAPIRetryMax
	httpClient.Logger = &retryableLogger{logger: logr.FromContextOrDiscard(ctx).WithName("grafana").WithValues("endpoint", endpoint)}
	httpClient.HTTPClient.Timeout = grafanaAPITimeout

	grafanaClient, err := sdk.NewClient(endpoint, token, httpClient.StandardClient())
	if err != nil {
		return nil, fmt.Errorf("failed to create Grafana SDK client: %w", err)
	}

	return &Client{
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

// ListFolders returns all folders in the Grafana instance.
func (c *Client) ListFolders(ctx context.Context) ([]sdk.Folder, error) {
	folders, err := c.grafanaClient.GetAllFolders(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get folders: %w", err)
	}

	return folders, nil
}

// ListDashboards returns all dashboards in the Grafana instance.
func (c *Client) ListDashboards(ctx context.Context) ([]sdk.FoundBoard, error) {
	boards, err := c.grafanaClient.SearchDashboards(ctx, "", false)
	if err != nil {
		return nil, fmt.Errorf("failed to search dashboards: %w", err)
	}

	return boards, nil
}

// CreateFolder creates a new folder in Grafana.
func (c *Client) CreateFolder(ctx context.Context, title string) (sdk.Folder, error) {
	folder := sdk.Folder{Title: title}
	createdFolder, err := c.grafanaClient.CreateFolder(ctx, folder)
	if err != nil {
		return sdk.Folder{}, fmt.Errorf("failed to create folder %q: %w", title, err)
	}

	return createdFolder, nil
}

// GetDashboardByUID retrieves a dashboard by its UID.
func (c *Client) GetDashboardByUID(ctx context.Context, uid string) (sdk.Board, sdk.BoardProperties, error) {
	board, props, err := c.grafanaClient.GetDashboardByUID(ctx, uid)
	if err != nil {
		return sdk.Board{}, sdk.BoardProperties{}, fmt.Errorf("failed to get dashboard %q: %w", uid, err)
	}

	return board, props, nil
}

// SetDashboard creates or updates a dashboard.
func (c *Client) SetDashboard(ctx context.Context, board sdk.Board, folderID int, overwrite bool) error {
	params := sdk.SetDashboardParams{
		FolderID:  folderID,
		Overwrite: overwrite,
	}

	_, err := c.grafanaClient.SetDashboard(ctx, board, params)
	if err != nil {
		return fmt.Errorf("failed to set dashboard: %w", err)
	}

	return nil
}

// DeleteDashboardByUID removes a dashboard by its UID.
func (c *Client) DeleteDashboardByUID(ctx context.Context, uid string) error {
	_, err := c.grafanaClient.DeleteDashboardByUID(ctx, uid)
	if err != nil {
		return fmt.Errorf("failed to delete dashboard %q: %w", uid, err)
	}

	return nil
}
