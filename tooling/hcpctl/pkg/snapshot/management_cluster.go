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
	"sort"
	"strings"
)

var managementClusterQuery = querySpec{
	component:    "backend",
	queryName:    "managementCluster",
	templatePath: "queries/backend/managementCluster/query.kql",
	database:     "service",
	category:     categoryResourceDiscovery,
}

// resolveManagementCluster maps each hosted cluster's latest ReadDesire to
// the AKS management cluster that hosts its control plane.
func (g *Gatherer) resolveManagementCluster(
	ctx context.Context,
	input GatherInput,
	seedData queryData,
	requests []frontendRequest,
	discoveryDir string,
) ([]string, error) {
	resolverData, err := managementClusterResolverData(seedData, requests)
	if err != nil {
		return nil, err
	}

	rows, err := g.executeQuery(ctx, managementClusterQuery, &resolverData, discoveryDir, input)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve management clusters: %w", err)
	}
	return managementClustersFromRows(rows, resolverData.ManagementClusterTargets)
}

func managementClusterResolverData(seedData queryData, requests []frontendRequest) (queryData, error) {
	targets := make([]managementClusterTarget, 0, len(requests))
	seen := make(map[string]struct{}, len(requests))
	for _, request := range requests {
		clusterResourceID := normalizeClusterResourceID(request.ClusterResourceID)
		subscriptionID := strings.ToLower(strings.TrimSpace(request.SubscriptionID))
		if clusterResourceID == "" || subscriptionID == "" {
			continue
		}
		target := managementClusterTarget{
			ClusterResourceID: clusterResourceID,
			SubscriptionID:    subscriptionID,
		}
		key := target.ClusterResourceID + "\x00" + target.SubscriptionID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		targets = append(targets, target)
	}
	if len(targets) == 0 {
		return queryData{}, fmt.Errorf("cannot resolve management clusters: no frontend request provided a cluster resource ID and subscription ID")
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].ClusterResourceID != targets[j].ClusterResourceID {
			return targets[i].ClusterResourceID < targets[j].ClusterResourceID
		}
		return targets[i].SubscriptionID < targets[j].SubscriptionID
	})

	resolverData := seedData
	resolverData.SubscriptionID = ""
	resolverData.ClusterResourceID = ""
	resolverData.ManagementClusterTargets = targets
	return resolverData, nil
}

type managementClusterTarget struct {
	ClusterResourceID string
	SubscriptionID    string
}

func managementClusterNames(input GatherInput) []string {
	unique := make(map[string]struct{}, len(input.ManagementClusterNames)+1)
	for _, name := range input.ManagementClusterNames {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "" {
			unique[name] = struct{}{}
		}
	}
	if name := strings.ToLower(strings.TrimSpace(input.ManagementClusterName)); name != "" {
		unique[name] = struct{}{}
	}

	names := make([]string, 0, len(unique))
	for name := range unique {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func normalizeClusterResourceID(resourceID string) string {
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(resourceID)), "/")
}

func managementClustersFromRows(rows []resultRow, targets []managementClusterTarget) ([]string, error) {
	if len(rows) == 0 {
		return nil, fmt.Errorf("management cluster query returned no results")
	}

	clusterResourceIDIndex, managementClusterIndex := -1, -1
	for index, column := range rows[0].columns {
		switch column {
		case "clusterResourceID":
			clusterResourceIDIndex = index
		case "managementCluster":
			managementClusterIndex = index
		}
	}
	if clusterResourceIDIndex < 0 {
		return nil, fmt.Errorf("management cluster query result is missing the clusterResourceID column")
	}
	if managementClusterIndex < 0 {
		return nil, fmt.Errorf("management cluster query result is missing the managementCluster column")
	}

	placementsByCluster := make(map[string]map[string]struct{}, len(targets))
	for _, row := range rows {
		if clusterResourceIDIndex >= len(row.values) || managementClusterIndex >= len(row.values) {
			return nil, fmt.Errorf("management cluster query returned a malformed row")
		}
		clusterResourceID := normalizeClusterResourceID(row.values[clusterResourceIDIndex])
		managementCluster := strings.ToLower(strings.TrimSpace(row.values[managementClusterIndex]))
		if clusterResourceID == "" || managementCluster == "" {
			return nil, fmt.Errorf("management cluster query returned an empty clusterResourceID or managementCluster value")
		}
		placements := placementsByCluster[clusterResourceID]
		if placements == nil {
			placements = make(map[string]struct{})
			placementsByCluster[clusterResourceID] = placements
		}
		placements[managementCluster] = struct{}{}
	}

	managementClusters := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		clusterResourceID := normalizeClusterResourceID(target.ClusterResourceID)
		placements := placementsByCluster[clusterResourceID]
		if len(placements) == 0 {
			return nil, fmt.Errorf("management cluster query returned no placement for cluster resource ID %q", target.ClusterResourceID)
		}
		if len(placements) > 1 {
			return nil, fmt.Errorf("management cluster query returned %d distinct placements for cluster resource ID %q, expected 1", len(placements), target.ClusterResourceID)
		}
		for managementCluster := range placements {
			managementClusters[managementCluster] = struct{}{}
		}
	}

	result := make([]string, 0, len(managementClusters))
	for managementCluster := range managementClusters {
		result = append(result, managementCluster)
	}
	sort.Strings(result)
	return result, nil
}
