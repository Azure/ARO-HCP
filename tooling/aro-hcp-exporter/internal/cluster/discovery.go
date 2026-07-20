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

package cluster

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-HCP/tooling/aro-hcp-exporter/pkg/graphquery"
)

// DiscoverResult holds the cluster names and subscription IDs found by
// querying Azure Resource Graph for AKS clusters tagged with the
// requested clusterType values.
type DiscoverResult struct {
	ClusterNames    []string
	SubscriptionIDs []string
}

type clusterRow struct {
	Name           string `mapstructure:"name"`
	SubscriptionId string `mapstructure:"subscriptionId"`
}

// Discover queries Azure Resource Graph for AKS managed clusters in the
// given region whose "clusterType" tag matches one of the provided
// clusterTypes. It returns the discovered cluster names and the
// deduplicated set of subscription IDs that contain them.
func Discover(ctx context.Context, client graphquery.Querier, region string, clusterTypes []string) (DiscoverResult, error) {
	query := buildKQLQuery(region, clusterTypes)

	var rows []clusterRow
	err := client.ExecuteConvertRequest(ctx, graphquery.ResourceGraphRequest{
		Query:  &query,
		Output: &rows,
	})
	if err != nil {
		return DiscoverResult{}, fmt.Errorf("failed to execute Resource Graph query: %w", err)
	}

	var result DiscoverResult
	seenSubs := sets.New[string]()
	seenClusterNames := sets.New[string]()
	for _, row := range rows {
		seenClusterNames.Insert(row.Name)
		seenSubs.Insert(row.SubscriptionId)
	}

	if seenClusterNames.Len() == 0 {
		return DiscoverResult{}, fmt.Errorf("no clusters found in region %q matching clusterTypes %v", region, clusterTypes)
	}

	result.ClusterNames = seenClusterNames.UnsortedList()
	result.SubscriptionIDs = seenSubs.UnsortedList()

	return result, nil
}

// buildKQLQuery constructs a KQL query that finds AKS managed clusters
// in the specified region with a clusterType tag matching any of the
// given types.
func buildKQLQuery(region string, clusterTypes []string) string {
	quoted := make([]string, len(clusterTypes))
	for i, ct := range clusterTypes {
		quoted[i] = fmt.Sprintf("'%s'", graphquery.EscapeKQL(ct))
	}

	return fmt.Sprintf(
		"resources\n"+
			"| where type =~ 'Microsoft.ContainerService/managedClusters'\n"+
			"| where location =~ '%s'\n"+
			"| where tags['clusterType'] in~ (%s)\n"+
			"| project name, subscriptionId",
		graphquery.EscapeKQL(region),
		strings.Join(quoted, ", "),
	)
}
