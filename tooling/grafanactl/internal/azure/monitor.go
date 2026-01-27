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

package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

// PrometheusInstance represents an Azure Monitor Workspace (managed Prometheus instance)
type PrometheusInstance struct {
	Name string
	ID   string
	Tags map[string]*string
}

// MonitorWorkspaceClient provides operations for Azure Monitor Workspace (Prometheus) management
type MonitorWorkspaceClient struct {
	client         *armmonitor.AzureMonitorWorkspacesClient
	subscriptionID string
}

// NewPrometheusClient creates a new PrometheusClient with the provided credentials
func NewMonitorWorkspaceClient(subscriptionID string, cred azcore.TokenCredential) (*MonitorWorkspaceClient, error) {
	client, err := armmonitor.NewAzureMonitorWorkspacesClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure Monitor Workspaces client: %w", err)
	}

	return &MonitorWorkspaceClient{
		client:         client,
		subscriptionID: subscriptionID,
	}, nil
}

// ListPrometheusInstances returns all managed Prometheus instances in the subscription
func (p *MonitorWorkspaceClient) GetAllMonitorWorkspaces(ctx context.Context) ([]armmonitor.AzureMonitorWorkspaceResource, error) {
	var workspaces []armmonitor.AzureMonitorWorkspaceResource

	pager := p.client.NewListBySubscriptionPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get next page of Prometheus instances: %w", err)
		}

		for _, workspace := range page.Value {
			workspaces = append(workspaces, *workspace)
		}
	}

	return workspaces, nil
}
