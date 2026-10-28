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
	"net/http"
	"sort"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

const hcpOpenShiftClusterResourceType = "Microsoft.RedHatOpenShift/hcpOpenShiftClusters"

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
	if len(resolverData.ManagementClusterTargets) == 0 {
		return nil, nil
	}

	rows, err := g.executeQuery(ctx, managementClusterQuery, &resolverData, discoveryDir, input)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve management clusters: %w", err)
	}
	return managementClustersFromRows(rows, resolverData.ManagementClusterTargets)
}

func managementClusterResolverData(seedData queryData, requests []frontendRequest) (queryData, error) {
	targets := make([]managementClusterTarget, 0, len(requests))
	targetIndexes := make(map[string]int, len(requests))
	sawNeverHostedClusterRequest := false
	sawClusterCollectionRequest := false
	for _, request := range requests {
		if isNeverHostedClusterRequest(request) {
			sawNeverHostedClusterRequest = true
			continue
		}
		if isClusterCollectionRequest(request) {
			sawClusterCollectionRequest = true
			continue
		}
		clusterResourceID := clusterResourceIDFromRequest(request)
		subscriptionID := strings.ToLower(strings.TrimSpace(request.SubscriptionID))
		if clusterResourceID == "" || subscriptionID == "" {
			continue
		}
		target := managementClusterTarget{
			ClusterResourceID: clusterResourceID,
			SubscriptionID:    subscriptionID,
			Optional:          isRejectedClusterCreateRequest(request, clusterResourceID),
		}
		key := target.ClusterResourceID + "\x00" + target.SubscriptionID
		if index, ok := targetIndexes[key]; ok {
			// Any request that requires placement makes the deduplicated target
			// required, even if another request for the same cluster was rejected.
			targets[index].Optional = targets[index].Optional && target.Optional
			continue
		}
		targetIndexes[key] = len(targets)
		targets = append(targets, target)
	}
	if len(targets) == 0 {
		if sawNeverHostedClusterRequest || sawClusterCollectionRequest {
			return emptyManagementClusterResolverData(seedData), nil
		}
		return queryData{}, fmt.Errorf("cannot resolve management clusters: no frontend request provided a named HCP cluster resource ID and subscription ID")
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

func emptyManagementClusterResolverData(seedData queryData) queryData {
	resolverData := seedData
	resolverData.SubscriptionID = ""
	resolverData.ClusterResourceID = ""
	resolverData.ManagementClusterTargets = nil
	return resolverData
}

// isNeverHostedClusterRequest identifies the negative lookup for a cluster
// resource itself. Failed child-resource requests still require placement,
// since they may belong to a hosted cluster.
func isNeverHostedClusterRequest(request frontendRequest) bool {
	return strings.EqualFold(strings.TrimSpace(request.Method), http.MethodGet) &&
		request.Status == http.StatusNotFound &&
		strings.EqualFold(strings.TrimSpace(request.ResourceType), hcpOpenShiftClusterResourceType) &&
		clusterResourceIDFromARMID(request.ResourceID) == normalizeClusterResourceID(request.ResourceID)
}

// isClusterCollectionRequest identifies a list request that names the HCP
// cluster collection but no individual cluster.
func isClusterCollectionRequest(request frontendRequest) bool {
	return strings.EqualFold(strings.TrimSpace(request.Method), http.MethodGet) &&
		strings.EqualFold(strings.TrimSpace(request.ResourceType), hcpOpenShiftClusterResourceType) &&
		clusterResourceIDFromARMID(request.ResourceID) == ""
}

// isRejectedClusterCreateRequest marks a failed top-level write as optional:
// it may have been rejected before a ReadDesire existed. Keep it in the query
// so a placement is still used if the resource already exists.
func isRejectedClusterCreateRequest(request frontendRequest, clusterResourceID string) bool {
	method := strings.TrimSpace(request.Method)
	if !strings.EqualFold(method, http.MethodPut) && !strings.EqualFold(method, http.MethodPost) {
		return false
	}
	if request.Status < http.StatusBadRequest ||
		request.Status >= http.StatusInternalServerError ||
		request.Status == http.StatusConflict {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(request.ResourceType), hcpOpenShiftClusterResourceType) &&
		normalizeClusterResourceID(request.ResourceID) == clusterResourceID
}

// clusterResourceIDFromRequest returns the named HCP cluster in either the
// request path or its reported cluster ID. This prevents collection and
// resource-group parent IDs from becoming broad placement-query prefixes.
func clusterResourceIDFromRequest(request frontendRequest) string {
	if isClusterCollectionRequest(request) {
		return ""
	}
	for _, resourceID := range []string{request.ResourceID, request.ClusterResourceID} {
		if clusterResourceID := clusterResourceIDFromARMID(resourceID); clusterResourceID != "" {
			return clusterResourceID
		}
	}
	return ""
}

func clusterResourceIDFromARMID(resourceID string) string {
	parsed, err := azcorearm.ParseResourceID(strings.TrimSpace(resourceID))
	if err != nil {
		return ""
	}
	for current := &parsed; current != nil; current = current.Parent {
		if strings.EqualFold(current.ResourceType.String(), hcpOpenShiftClusterResourceType) && strings.TrimSpace(current.Name) != "" {
			return normalizeClusterResourceID(current.String())
		}
	}
	return ""
}

type managementClusterTarget struct {
	ClusterResourceID string
	SubscriptionID    string
	Optional          bool // Failed top-level writes may not have produced a ReadDesire.
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
		if len(targets) == 0 {
			return nil, fmt.Errorf("management cluster query returned no results")
		}
		for _, target := range targets {
			if !target.Optional {
				return nil, fmt.Errorf("management cluster query returned no results")
			}
		}
		return nil, nil
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
			if target.Optional {
				continue
			}
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
