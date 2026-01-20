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

package mgmtinventory

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
)

type GraphInventory struct {
	azureCredentials azcore.TokenCredential
	location         string
}

func NewGraphInventory(azureCredentials azcore.TokenCredential, location string) *GraphInventory {
	return &GraphInventory{
		azureCredentials: azureCredentials,
		location:         location,
	}
}

func (i *GraphInventory) GetManagementClusters(ctx context.Context) ([]ManagementCluster, error) {
	// Create Resource Graph client
	client, err := armresourcegraph.NewClient(i.azureCredentials, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Resource Graph client: %w", err)
	}
	kqlQuery := buildMgmtClusterKQLQuery(i.location)
	queryRequest := &armresourcegraph.QueryRequest{
		Query: &kqlQuery,
	}
	result, err := client.Resources(ctx, *queryRequest, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to execute Resource Graph query: %w", err)
	}
	return i.parseResourceGraphResults(result)
}

func buildMgmtClusterKQLQuery(region string) string {
	var query strings.Builder

	// Start with AKS managed clusters
	query.WriteString("resources\n")
	query.WriteString("| where type =~ 'Microsoft.ContainerService/managedClusters'\n")
	query.WriteString(fmt.Sprintf("| where location =~ '%s'\n", strings.ToLower(region)))
	query.WriteString("| where tags['clusterType'] =~ 'mgmt-cluster'\n")

	// Project the fields we need
	query.WriteString("| project name, resourceGroup, subscriptionId, location, tags, properties, id")

	return query.String()
}

func (i *GraphInventory) parseResourceGraphResults(result armresourcegraph.ClientResourcesResponse) ([]ManagementCluster, error) {
	rows, err := parseResourceGraphResultData(result.Data)
	if err != nil {
		return nil, err
	}

	var clusters []ManagementCluster
	for _, rowMap := range rows {
		cluster := ManagementCluster{
			Name:           parseGraphStringField(rowMap, "name"),
			ResourceGroup:  parseGraphStringField(rowMap, "resourceGroup"),
			SubscriptionID: parseGraphStringField(rowMap, "subscriptionId"),
			Location:       parseGraphStringField(rowMap, "location"),
			ResourceID:     parseGraphStringField(rowMap, "id"),
			Tags:           parseGraphTagsMap(rowMap),
		}

		// Extract properties for state if available
		cluster.State = parseGraphPropertiesField(rowMap, "provisioningState")

		clusters = append(clusters, cluster)
	}

	return clusters, nil
}

// ParseResourceGraphResultData converts Resource Graph result data to a slice of maps
func parseResourceGraphResultData(data interface{}) ([]map[string]interface{}, error) {
	if data == nil {
		return nil, nil
	}

	rows, ok := data.([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected Resource Graph response format: expected []interface{}, got %T", data)
	}

	var results []map[string]interface{}
	for _, row := range rows {
		rowMap, ok := row.(map[string]interface{})
		if !ok {
			continue // Skip invalid rows
		}
		results = append(results, rowMap)
	}

	return results, nil
}

func parseGraphStringField(data map[string]interface{}, field string) string {
	if value, ok := data[field].(string); ok {
		return value
	}
	return ""
}

func parseGraphTagsMap(data map[string]interface{}) map[string]string {
	tags := make(map[string]string)
	if tagsInterface, ok := data["tags"].(map[string]interface{}); ok {
		for key, value := range tagsInterface {
			if strValue, ok := value.(string); ok {
				tags[key] = strValue
			}
		}
	}
	return tags
}

func parseGraphPropertiesField(rowMap map[string]interface{}, field string) string {
	if properties, ok := rowMap["properties"].(map[string]interface{}); ok {
		return parseGraphStringField(properties, field)
	}
	return ""
}
