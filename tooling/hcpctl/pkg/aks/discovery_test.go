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
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildKQLQuery(t *testing.T) {
	tests := []struct {
		name     string
		filter   *AKSFilter
		contains []string
	}{
		{
			name:   "no filter returns basic query",
			filter: nil,
			contains: []string{
				"resources",
				"where type =~ 'Microsoft.ContainerService/managedClusters'",
				"join kind=leftouter",
				"project name, resourceGroup, subscriptionId",
			},
		},
		{
			name: "region filter includes location clause",
			filter: &AKSFilter{
				Region: "eastus",
			},
			contains: []string{
				"where location =~ 'eastus'",
			},
		},
		{
			name: "tag filter includes tag clause",
			filter: &AKSFilter{
				TagKey:   "clusterType",
				TagValue: "svc-cluster",
			},
			contains: []string{
				"where tags['clusterType'] =~ 'svc-cluster'",
			},
		},
		{
			name: "cluster name filter includes name clause",
			filter: &AKSFilter{
				ClusterName: "my-test-cluster",
			},
			contains: []string{
				"where name =~ 'my-test-cluster'",
			},
		},
		{
			name: "both region and tag filters includes both clauses",
			filter: &AKSFilter{
				TagKey:   "clusterType",
				TagValue: "mgmt-cluster",
				Region:   "westus2",
			},
			contains: []string{
				"where location =~ 'westus2'",
				"where tags['clusterType'] =~ 'mgmt-cluster'",
			},
		},
		{
			name: "all filters includes all clauses",
			filter: &AKSFilter{
				ClusterName: "production-cluster",
				TagKey:      "environment",
				TagValue:    "prod",
				Region:      "eastus",
			},
			contains: []string{
				"where name =~ 'production-cluster'",
				"where location =~ 'eastus'",
				"where tags['environment'] =~ 'prod'",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := buildKQLQuery(tt.filter)
			for _, expected := range tt.contains {
				assert.Contains(t, query, expected)
			}
		})
	}
}

func TestParseResourceGraphResults(t *testing.T) {
	discovery := &AKSDiscovery{}

	t.Run("nil data returns empty slice", func(t *testing.T) {
		result := armresourcegraph.ClientResourcesResponse{
			QueryResponse: armresourcegraph.QueryResponse{
				Data: nil,
			},
		}
		clusters, err := discovery.parseResourceGraphResults(result)
		require.NoError(t, err)
		assert.Empty(t, clusters)
	})

	t.Run("empty data returns empty slice", func(t *testing.T) {
		result := armresourcegraph.ClientResourcesResponse{
			QueryResponse: armresourcegraph.QueryResponse{
				Data: []interface{}{},
			},
		}
		clusters, err := discovery.parseResourceGraphResults(result)
		require.NoError(t, err)
		assert.Empty(t, clusters)
	})

	t.Run("invalid data format returns error", func(t *testing.T) {
		result := armresourcegraph.ClientResourcesResponse{
			QueryResponse: armresourcegraph.QueryResponse{
				Data: "invalid-format",
			},
		}
		clusters, err := discovery.parseResourceGraphResults(result)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected Resource Graph response format")
		assert.Nil(t, clusters)
	})

	t.Run("valid cluster data parses correctly", func(t *testing.T) {
		result := armresourcegraph.ClientResourcesResponse{
			QueryResponse: armresourcegraph.QueryResponse{
				Data: []interface{}{
					map[string]interface{}{
						"name":                    "test-cluster",
						"resourceGroup":           "test-rg",
						"subscriptionId":          "sub-123",
						"subscriptionDisplayName": "Test Subscription",
						"location":                "eastus",
						"tags": map[string]interface{}{
							"environment": "test",
							"owner":       "team-a",
						},
						"properties": map[string]interface{}{
							"provisioningState": "Succeeded",
						},
					},
				},
			},
		}

		clusters, err := discovery.parseResourceGraphResults(result)
		require.NoError(t, err)
		require.Len(t, clusters, 1)

		cluster := clusters[0]
		assert.Equal(t, "test-cluster", cluster.Name)
		assert.Equal(t, "test-rg", cluster.ResourceGroup)
		assert.Equal(t, "sub-123", cluster.SubscriptionID)
		assert.Equal(t, "Test Subscription", cluster.Subscription)
		assert.Equal(t, "eastus", cluster.Location)
		assert.Equal(t, "Succeeded", cluster.State)
		assert.Equal(t, map[string]string{
			"environment": "test",
			"owner":       "team-a",
		}, cluster.Tags)
	})
}
