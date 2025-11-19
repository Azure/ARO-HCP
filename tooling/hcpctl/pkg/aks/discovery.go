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

package aks

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/common"
)

// AKSCluster represents an AKS cluster with its metadata
type AKSCluster struct {
	Name           string            `json:"name" yaml:"name"`
	ResourceGroup  string            `json:"resourcegroup" yaml:"resourcegroup"`
	SubscriptionID string            `json:"subscriptionid" yaml:"subscriptionid"`
	Subscription   string            `json:"subscription" yaml:"subscription"` // Subscription display name
	Location       string            `json:"location" yaml:"location"`
	ResourceID     string            `json:"resourceid" yaml:"resourceid"`
	State          string            `json:"state" yaml:"state"`
	Tags           map[string]string `json:"tags" yaml:"tags"` // Full tag map for flexibility
}

// AKSFilter defines filtering criteria for AKS cluster discovery
type AKSFilter struct {
	TagKey      string // e.g., "clusterType"
	TagValue    string // e.g., "mgmt-cluster" or "svc-cluster"
	Region      string // e.g., "eastus" (optional)
	ClusterName string // e.g., "my-cluster" (optional)
}

const ManagementClusterType = "mgmt-cluster"
const ServiceClusterType = "svc-cluster"

func NewAKSFilter(clusterType, region, name string) *AKSFilter {
	return &AKSFilter{
		TagKey:      "clusterType",
		TagValue:    clusterType,
		Region:      region,
		ClusterName: name,
	}
}

func NewMgmtClusterFilter(region string, name string) *AKSFilter {
	return &AKSFilter{
		TagKey:      "clusterType",
		TagValue:    ManagementClusterType,
		Region:      region,
		ClusterName: name,
	}
}

func NewSvcClusterFilter(region string, name string) *AKSFilter {
	return &AKSFilter{
		TagKey:      "clusterType",
		TagValue:    ServiceClusterType,
		Region:      region,
		ClusterName: name,
	}
}

// AKSDiscovery handles AKS cluster discovery operations using Azure Resource Graph
type AKSDiscovery struct {
	credential azcore.TokenCredential
}

// NewAKSDiscovery creates a new AKS discovery instance
func NewAKSDiscovery(cred azcore.TokenCredential) *AKSDiscovery {
	return &AKSDiscovery{
		credential: cred,
	}
}

// DiscoverClusters discovers AKS clusters using Azure Resource Graph
func (d *AKSDiscovery) DiscoverClusters(ctx context.Context, filter *AKSFilter) ([]AKSCluster, error) {
	// Create Resource Graph client
	client, err := armresourcegraph.NewClient(d.credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Resource Graph client: %w", err)
	}
	kqlQuery := buildKQLQuery(filter)
	queryRequest := &armresourcegraph.QueryRequest{
		Query: &kqlQuery,
	}
	result, err := client.Resources(ctx, *queryRequest, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to execute Resource Graph query: %w", err)
	}
	return d.parseResourceGraphResults(result)
}

// FindSingleCluster finds exactly one AKS cluster using the provided filter
// Returns an error if zero clusters or multiple clusters are found
func (d *AKSDiscovery) FindSingleCluster(ctx context.Context, filter *AKSFilter) (*AKSCluster, error) {
	clusters, err := d.DiscoverClusters(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to discover clusters: %w", err)
	}

	if len(clusters) == 0 {
		return nil, fmt.Errorf("no clusters not found matching filter criteria")
	}

	if len(clusters) > 1 {
		return nil, fmt.Errorf("multiple AKS clusters found. filter criteria too broad")
	}

	// Return the single match
	return &clusters[0], nil
}

// buildKQLQuery builds a KQL query for Azure Resource Graph
func buildKQLQuery(filter *AKSFilter) string {
	var query strings.Builder

	// Start with AKS managed clusters
	query.WriteString("resources\n")
	query.WriteString("| where type =~ 'Microsoft.ContainerService/managedClusters'\n")

	// Apply optional filters
	if filter != nil {
		if filter.ClusterName != "" {
			query.WriteString(fmt.Sprintf("| where name =~ '%s'\n", filter.ClusterName))
		}

		if filter.Region != "" {
			query.WriteString(fmt.Sprintf("| where location =~ '%s'\n", strings.ToLower(filter.Region)))
		}

		if filter.TagKey != "" && filter.TagValue != "" {
			query.WriteString(fmt.Sprintf("| where tags['%s'] =~ '%s'\n", filter.TagKey, filter.TagValue))
		}
	}

	// Join with subscriptions to get display names
	query.WriteString("| join kind=leftouter (\n")
	query.WriteString("    resourcecontainers\n")
	query.WriteString("    | where type == 'microsoft.resources/subscriptions'\n")
	query.WriteString("    | project subscriptionId, subscriptionDisplayName = name\n")
	query.WriteString(") on subscriptionId\n")

	// Project the fields we need
	query.WriteString("| project name, resourceGroup, subscriptionId, subscriptionDisplayName, location, tags, properties, id")

	return query.String()
}

// parseResourceGraphResults parses the Resource Graph response into AKSCluster structs
func (d *AKSDiscovery) parseResourceGraphResults(result armresourcegraph.ClientResourcesResponse) ([]AKSCluster, error) {
	rows, err := common.ParseResourceGraphResultData(result.Data)
	if err != nil {
		return nil, err
	}

	var clusters []AKSCluster
	for _, rowMap := range rows {
		cluster := AKSCluster{
			Name:           common.ParseStringField(rowMap, "name"),
			ResourceGroup:  common.ParseStringField(rowMap, "resourceGroup"),
			SubscriptionID: common.ParseStringField(rowMap, "subscriptionId"),
			Subscription:   common.ParseStringField(rowMap, "subscriptionDisplayName"),
			Location:       common.ParseStringField(rowMap, "location"),
			ResourceID:     common.ParseStringField(rowMap, "id"),
			Tags:           common.ParseTagsMap(rowMap),
		}

		// Extract properties for state if available
		cluster.State = common.ParsePropertiesField(rowMap, "provisioningState")

		clusters = append(clusters, cluster)
	}

	return clusters, nil
}
