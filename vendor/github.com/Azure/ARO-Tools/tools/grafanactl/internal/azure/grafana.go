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
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dashboard/armdashboard/v2"
)

// ManagedGrafanaClient provides operations for Managed Grafana Resources
// In order to communicate with the Grafana API, you can use ../grafana/client.go
type ManagedGrafanaClient struct {
	client *armdashboard.GrafanaClient
}

// NewManagedGrafanaClient creates a new ManagedGrafanaClient with the provided credentials.
// The clientOptions parameter allows callers to specify cloud-specific configuration
// (e.g. cloud.AzureGovernment for Fairfax). Pass nil to use the default (public cloud).
func NewManagedGrafanaClient(subscriptionID string, cred azcore.TokenCredential, clientOptions *arm.ClientOptions) (*ManagedGrafanaClient, error) {
	grafanaClient, err := armdashboard.NewGrafanaClient(subscriptionID, cred, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create Managed Grafana client: %w", err)
	}

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

// UpdateGrafanaIntegrations updates the Azure Monitor Workspace integrations for the Grafana instance
func (p *ManagedGrafanaClient) UpdateGrafanaIntegrations(ctx context.Context, resourceGroup, grafanaName string, integrations []string) error {
	err := p.waitForReadyGrafana(ctx, resourceGroup, grafanaName)
	if err != nil {
		return fmt.Errorf("failed to wait for Grafana to be ready: %w", err)
	}

	azureMonitorWorkspaceIntegrations := make([]*armdashboard.AzureMonitorWorkspaceIntegration, 0)
	for _, integration := range integrations {
		azureMonitorWorkspaceIntegrations = append(azureMonitorWorkspaceIntegrations, &armdashboard.AzureMonitorWorkspaceIntegration{
			AzureMonitorWorkspaceResourceID: &integration,
		})
	}

	poller, err := p.client.BeginUpdate(ctx, resourceGroup, grafanaName, armdashboard.ManagedGrafanaUpdateParameters{
		Properties: &armdashboard.ManagedGrafanaPropertiesUpdateParameters{
			GrafanaIntegrations: &armdashboard.GrafanaIntegrations{
				AzureMonitorWorkspaceIntegrations: azureMonitorWorkspaceIntegrations,
			},
		},
	}, nil)
	if err != nil {
		return fmt.Errorf("failed to update Grafana instance: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
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

// CreateOrUpdateGrafanaInstance creates or updates an Azure Managed Grafana resource.
func (c *ManagedGrafanaClient) CreateOrUpdateGrafanaInstance(ctx context.Context, resourceGroup, grafanaName string, grafana armdashboard.ManagedGrafana) (*armdashboard.ManagedGrafana, error) {
	poller, err := c.client.BeginCreate(ctx, resourceGroup, grafanaName, grafana, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin create/update Grafana instance: %w", err)
	}

	result, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to poll create/update Grafana instance: %w", err)
	}

	return &result.ManagedGrafana, nil
}

func (c *ManagedGrafanaClient) waitForReadyGrafana(ctx context.Context, resourceGroup, grafanaName string) error {
	logger := logr.FromContextOrDiscard(ctx)
	for {
		grafana, err := c.GetGrafanaInstance(ctx, resourceGroup, grafanaName)
		if err != nil {
			return fmt.Errorf("failed to get Grafana instance: %w", err)
		}
		if grafana.Properties == nil || grafana.Properties.ProvisioningState == nil {
			logger.Info("Grafana properties or provisioning state not available yet, retrying...")
		} else {
			state := *grafana.Properties.ProvisioningState
			switch state {
			case armdashboard.ProvisioningStateSucceeded:
				return nil
			case armdashboard.ProvisioningStateFailed, armdashboard.ProvisioningStateCanceled, armdashboard.ProvisioningStateDeleted:
				return fmt.Errorf("grafana instance provisioning ended in non-ready state: %s", state)
			default:
				logger.Info("Waiting for Grafana to be ready", "provisioningState", string(state))
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for Grafana to be ready: %w", ctx.Err())
		case <-time.After(10 * time.Second):
		}
	}
}
