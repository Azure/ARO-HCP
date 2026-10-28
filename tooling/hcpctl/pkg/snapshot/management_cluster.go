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

package snapshot

import (
	"context"
	"fmt"
)

var managementClusterQuery = querySpec{
	component:    "backend",
	queryName:    "managementCluster",
	templatePath: "queries/backend/managementCluster/query.kql",
	database:     "service",
	category:     categoryResourceDiscovery,
}

// resolveManagementCluster maps the cluster's live ReadDesire to the AKS
// management cluster that actually hosts its control plane.
func (g *Gatherer) resolveManagementCluster(
	ctx context.Context,
	input GatherInput,
	seedData queryData,
	requests []frontendRequest,
	discoveryDir string,
) (string, error) {
	resolverData, err := managementClusterResolverData(seedData, requests)
	if err != nil {
		return "", err
	}

	rows, err := g.executeQuery(ctx, managementClusterQuery, &resolverData, discoveryDir, input)
	if err != nil {
		return "", fmt.Errorf("failed to resolve management cluster: %w", err)
	}
	return managementClusterFromRows(rows)
}

func managementClusterResolverData(seedData queryData, requests []frontendRequest) (queryData, error) {
	for _, request := range requests {
		if request.ClusterResourceID == "" || request.SubscriptionID == "" {
			continue
		}
		resolverData := seedData
		resolverData.SubscriptionID = request.SubscriptionID
		resolverData.ClusterResourceID = request.ClusterResourceID
		return resolverData, nil
	}
	return queryData{}, fmt.Errorf("cannot resolve management cluster: no frontend request provided a cluster resource ID and subscription ID")
}

func managementClusterFromRows(rows []resultRow) (string, error) {
	if len(rows) == 0 {
		return "", fmt.Errorf("management cluster query returned no results")
	}
	if len(rows) > 1 {
		return "", fmt.Errorf("management cluster query returned %d distinct results, expected 1", len(rows))
	}
	for index, column := range rows[0].columns {
		if column != "managementCluster" {
			continue
		}
		if index >= len(rows[0].values) || rows[0].values[index] == "" {
			return "", fmt.Errorf("management cluster query returned an empty managementCluster value")
		}
		return rows[0].values[index], nil
	}
	return "", fmt.Errorf("management cluster query result is missing the managementCluster column")
}
