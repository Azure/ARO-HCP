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
	"sync"
	"time"

	"github.com/go-logr/logr"

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

type ClusterDiscoveryPoller struct {
	client      graphquery.Querier
	query       string
	resultMutex sync.Mutex
	rows        []clusterRow
	sleepTime   time.Duration
}

func NewClusterDiscoveryPoller(client graphquery.Querier, region string, clusterTypes []string, sleepTime time.Duration) *ClusterDiscoveryPoller {
	return &ClusterDiscoveryPoller{
		client:    client,
		query:     BuildClusterQuery(region, clusterTypes),
		sleepTime: sleepTime,
	}
}

func (c *ClusterDiscoveryPoller) Poll(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(c.sleepTime):
		logger := logr.FromContextOrDiscard(ctx)
		var newRows []clusterRow
		err := c.client.ExecuteConvertRequest(ctx, graphquery.ResourceGraphRequest{
			Query:  &c.query,
			Output: &newRows,
		})
		if err != nil {
			logger.Error(err, "failed to execute Resource Graph query")
			return
		}
		c.resultMutex.Lock()
		c.rows = newRows
		c.resultMutex.Unlock()
	}
}

func (c *ClusterDiscoveryPoller) GetDiscoverResult() DiscoverResult {
	c.resultMutex.Lock()
	rows := c.rows
	c.resultMutex.Unlock()

	var result DiscoverResult
	seenSubs := sets.New[string]()
	seenClusterNames := sets.New[string]()
	for _, row := range rows {
		seenClusterNames.Insert(row.Name)
		seenSubs.Insert(row.SubscriptionId)
	}
	result.ClusterNames = seenClusterNames.UnsortedList()
	result.SubscriptionIDs = seenSubs.UnsortedList()
	return result
}

// BuildClusterQuery constructs a KQL query that finds AKS managed clusters
// in the specified region with a clusterType tag matching the given type.
func BuildClusterQuery(region string, clusterTypes []string) string {
	quoted := make([]string, 0, len(clusterTypes))
	for _, clusterType := range clusterTypes {
		quoted = append(quoted, fmt.Sprintf("'%s'", graphquery.EscapeKQL(clusterType)))
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
