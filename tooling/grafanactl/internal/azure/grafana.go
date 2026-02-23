// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package azure

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dashboard/armdashboard"
)

// ManagedGrafanaClient provides operations for Managed Grafana Resources
// In order to communicate with the Grafana API, you can use ../grafana/client.go
type ManagedGrafanaClient struct {
	client *armdashboard.GrafanaClient
}

// NewManagedGrafanaClient creates a new ManagedGrafanaClient with the provided credentials
func NewManagedGrafanaClient(subscriptionID string, cred azcore.TokenCredential) (*ManagedGrafanaClient, error) {
	client, err := armdashboard.NewClientFactory(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure Monitor Workspaces client: %w", err)
	}

	grafanaClient := client.NewGrafanaClient()

	return &ManagedGrafanaClient{
		client: grafanaClient,
	}, nil
}

func (p *ManagedGrafanaClient) GetGrafanaInstance(ctx context.Context, resourceGroup, grafanaName string) (*armdashboard.ManagedGrafana, error) {
	instance, err := p.client.Get(ctx, resourceGroup, grafanaName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get Grafana instance: %w", err)
	}

	return &instance.ManagedGrafana, nil

}

// ListPrometheusInstances returns all managed Prometheus instances in the subscription
func (p *ManagedGrafanaClient) UpdateGrafanaIntegrations(ctx context.Context, resourceGroup, grafanaName string, integrations []string) error {
	azureMonitorWorkspaceIntegrations := make([]*armdashboard.AzureMonitorWorkspaceIntegration, 0)
	for _, integration := range integrations {
		azureMonitorWorkspaceIntegrations = append(azureMonitorWorkspaceIntegrations, &armdashboard.AzureMonitorWorkspaceIntegration{
			AzureMonitorWorkspaceResourceID: &integration,
		})
	}

	_, err := p.client.Update(ctx, resourceGroup, grafanaName, armdashboard.ManagedGrafanaUpdateParameters{
		Properties: &armdashboard.ManagedGrafanaPropertiesUpdateParameters{
			GrafanaIntegrations: &armdashboard.GrafanaIntegrations{
				AzureMonitorWorkspaceIntegrations: azureMonitorWorkspaceIntegrations,
			},
		},
	}, nil)
	if err != nil {
		return fmt.Errorf("failed to update Grafana instance: %w", err)
	}

	return nil
}

// GetGrafanaEndpoint retrieves the endpoint URL for an Azure Managed Grafana instance
// using the Azure Dashboard API.
func (c *ManagedGrafanaClient) GetGrafanaEndpoint(ctx context.Context, subscriptionID, resourceGroup, grafanaName string) (string, error) {
	grafanaResource, err := c.client.Get(ctx, resourceGroup, grafanaName, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get Grafana resource: %w", err)
	}

	if grafanaResource.Properties == nil || grafanaResource.Properties.Endpoint == nil {
		return "", fmt.Errorf("grafana instance endpoint not found in response")
	}

	endpoint := *grafanaResource.Properties.Endpoint
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "https://" + endpoint
	}

	return endpoint, nil
}
