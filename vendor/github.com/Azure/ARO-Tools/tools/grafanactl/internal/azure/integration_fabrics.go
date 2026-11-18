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

package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dashboard/armdashboard/v2"
)

// IntegrationFabricsClient provides operations for Managed Grafana integration fabrics.
type IntegrationFabricsClient struct {
	client *armdashboard.IntegrationFabricsClient
}

// NewIntegrationFabricsClient creates an IntegrationFabricsClient.
func NewIntegrationFabricsClient(subscriptionID string, cred azcore.TokenCredential, clientOptions *arm.ClientOptions) (*IntegrationFabricsClient, error) {
	client, err := armdashboard.NewIntegrationFabricsClient(subscriptionID, cred, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create integration fabrics client: %w", err)
	}

	return &IntegrationFabricsClient{client: client}, nil
}

// List returns all integration fabrics under a Managed Grafana resource.
func (c *IntegrationFabricsClient) List(ctx context.Context, resourceGroup, grafanaName string) ([]armdashboard.IntegrationFabric, error) {
	var fabrics []armdashboard.IntegrationFabric
	pager := c.client.NewListPager(resourceGroup, grafanaName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list integration fabrics: %w", err)
		}
		for _, fabric := range page.Value {
			if fabric != nil {
				fabrics = append(fabrics, *fabric)
			}
		}
	}
	return fabrics, nil
}

// Get returns one integration fabric.
func (c *IntegrationFabricsClient) Get(ctx context.Context, resourceGroup, grafanaName, integrationFabricName string) (*armdashboard.IntegrationFabric, error) {
	result, err := c.client.Get(ctx, resourceGroup, grafanaName, integrationFabricName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get integration fabric %q: %w", integrationFabricName, err)
	}
	return &result.IntegrationFabric, nil
}

// Create creates an integration fabric.
func (c *IntegrationFabricsClient) Create(ctx context.Context, resourceGroup, grafanaName, integrationFabricName string, fabric armdashboard.IntegrationFabric) (*armdashboard.IntegrationFabric, error) {
	poller, err := c.client.BeginCreate(ctx, resourceGroup, grafanaName, integrationFabricName, fabric, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin creating integration fabric %q: %w", integrationFabricName, err)
	}
	result, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create integration fabric %q: %w", integrationFabricName, err)
	}
	return &result.IntegrationFabric, nil
}

// Update updates the supported properties of an integration fabric.
func (c *IntegrationFabricsClient) Update(ctx context.Context, resourceGroup, grafanaName, integrationFabricName string, parameters armdashboard.IntegrationFabricUpdateParameters) (*armdashboard.IntegrationFabric, error) {
	poller, err := c.client.BeginUpdate(ctx, resourceGroup, grafanaName, integrationFabricName, parameters, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin updating integration fabric %q: %w", integrationFabricName, err)
	}
	result, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to update integration fabric %q: %w", integrationFabricName, err)
	}
	return &result.IntegrationFabric, nil
}

// Delete deletes an integration fabric.
func (c *IntegrationFabricsClient) Delete(ctx context.Context, resourceGroup, grafanaName, integrationFabricName string) error {
	poller, err := c.client.BeginDelete(ctx, resourceGroup, grafanaName, integrationFabricName, nil)
	if err != nil {
		return fmt.Errorf("failed to begin deleting integration fabric %q: %w", integrationFabricName, err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("failed to delete integration fabric %q: %w", integrationFabricName, err)
	}
	return nil
}
