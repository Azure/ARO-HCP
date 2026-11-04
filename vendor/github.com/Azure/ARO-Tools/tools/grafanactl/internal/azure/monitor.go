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
	"encoding/json"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
)

// MonitorWorkspaceClient provides operations for Azure Monitor Workspace (Prometheus) management
type MonitorWorkspaceClient struct {
	client         *armmonitor.AzureMonitorWorkspacesClient
	subscriptionID string
}

// NewMonitorWorkspaceClient creates a new MonitorWorkspaceClient with the provided credentials.
// The clientOptions parameter allows callers to specify cloud-specific configuration
// (e.g. cloud.AzureGovernment for Fairfax). Pass nil to use the default (public cloud).
func NewMonitorWorkspaceClient(subscriptionID string, cred azcore.TokenCredential, clientOptions *arm.ClientOptions) (*MonitorWorkspaceClient, error) {
	client, err := armmonitor.NewAzureMonitorWorkspacesClient(subscriptionID, cred, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure Monitor Workspaces client: %w", err)
	}

	return &MonitorWorkspaceClient{
		client:         client,
		subscriptionID: subscriptionID,
	}, nil
}

// GetAllMonitorWorkspaces returns all managed Prometheus instances in the subscription
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

// ResourceGraphDiscoveryClient discovers Azure resources across subscriptions using Azure Resource Graph.
type ResourceGraphDiscoveryClient struct {
	client *armresourcegraph.Client
	tagKey string
}

// KustoCluster identifies a managed Kusto cluster and its geography tag.
type KustoCluster struct {
	ResourceID        string
	Geography         string
	ProvisioningState string
}

// NewResourceGraphDiscoveryClient creates a new ResourceGraphDiscoveryClient.
// tagKey selects which resource tag marks an Azure Monitor Workspace as a
// discovery target
func NewResourceGraphDiscoveryClient(cred azcore.TokenCredential, clientOptions *arm.ClientOptions, tagKey string) (*ResourceGraphDiscoveryClient, error) {
	client, err := armresourcegraph.NewClient(cred, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create Resource Graph client: %w", err)
	}

	return &ResourceGraphDiscoveryClient{
		client: client,
		tagKey: tagKey,
	}, nil
}

// DiscoverMonitorWorkspaceIDs returns resource IDs of all Azure Monitor Workspaces
// across all accessible subscriptions that have the configured discovery tag set.
func (c *ResourceGraphDiscoveryClient) DiscoverMonitorWorkspaceIDs(ctx context.Context) ([]string, error) {
	query := fmt.Sprintf("resources | where type =~ 'microsoft.monitor/accounts' | where isnotempty(tags['%s']) and properties.provisioningState == 'Succeeded' | project id", c.tagKey)
	format := armresourcegraph.ResultFormatObjectArray

	var ids []string
	var skipToken *string
	for {
		result, err := c.client.Resources(ctx, armresourcegraph.QueryRequest{
			Query: &query,
			Options: &armresourcegraph.QueryRequestOptions{
				ResultFormat: &format,
				SkipToken:    skipToken,
			},
		}, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to query Resource Graph: %w", err)
		}

		rows, ok := result.Data.([]any)
		if !ok {
			raw, _ := json.Marshal(result.Data)
			return nil, fmt.Errorf("unexpected Resource Graph result type: %T (raw: %s)", result.Data, string(raw))
		}

		for _, row := range rows {
			m, ok := row.(map[string]any)
			if !ok {
				continue
			}
			if id, ok := m["id"].(string); ok {
				ids = append(ids, id)
			}
		}

		skipToken = result.SkipToken
		if skipToken == nil || *skipToken == "" {
			break
		}
	}

	return ids, nil
}

// DiscoverKustoClusters returns Kusto clusters tagged for ARO HCP logs in one environment.
func (c *ResourceGraphDiscoveryClient) DiscoverKustoClusters(ctx context.Context, subscriptionID, environment string) ([]KustoCluster, error) {
	format := armresourcegraph.ResultFormatObjectArray

	var clusters []KustoCluster
	var skipToken *string
	for {
		request, err := newKustoClusterQueryRequest(subscriptionID, environment, format, skipToken)
		if err != nil {
			return nil, err
		}
		result, err := c.client.Resources(ctx, request, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to query Resource Graph for Kusto clusters: %w", err)
		}

		rows, ok := result.Data.([]any)
		if !ok {
			raw, _ := json.Marshal(result.Data)
			return nil, fmt.Errorf("unexpected Resource Graph result type: %T (raw: %s)", result.Data, string(raw))
		}

		for _, row := range rows {
			values, ok := row.(map[string]any)
			if !ok {
				continue
			}
			id, ok := values["id"].(string)
			if !ok || id == "" {
				continue
			}
			geography, _ := values["geography"].(string)
			provisioningState, _ := values["provisioningState"].(string)
			clusters = append(clusters, KustoCluster{
				ResourceID:        id,
				Geography:         geography,
				ProvisioningState: provisioningState,
			})
		}

		skipToken = result.SkipToken
		if skipToken == nil || *skipToken == "" {
			break
		}
	}

	return clusters, nil
}

// ValidateKustoEnvironment validates a value before it is embedded in a KQL string literal.
func ValidateKustoEnvironment(environment string) error {
	if environment == "" {
		return fmt.Errorf("environment cannot be empty")
	}
	for _, character := range environment {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '-' {
			continue
		}
		return fmt.Errorf("environment must contain only ASCII letters, numbers, and hyphens")
	}
	return nil
}

func newKustoClusterQueryRequest(subscriptionID, environment string, format armresourcegraph.ResultFormat, skipToken *string) (armresourcegraph.QueryRequest, error) {
	if err := ValidateKustoEnvironment(environment); err != nil {
		return armresourcegraph.QueryRequest{}, err
	}
	query := fmt.Sprintf(`resources
| where type =~ 'microsoft.kusto/clusters'
| where tostring(tags['aroHCPPurpose']) =~ 'logs'
| where tostring(tags['aroHCPEnvironment']) =~ '%s'
| project id, geography = tostring(tags['aroHCPGeoShortId']), provisioningState = tostring(properties.provisioningState)
| order by id asc`, environment)

	return armresourcegraph.QueryRequest{
		Query:         &query,
		Subscriptions: []*string{&subscriptionID},
		Options: &armresourcegraph.QueryRequestOptions{
			ResultFormat: &format,
			SkipToken:    skipToken,
		},
	}, nil
}
