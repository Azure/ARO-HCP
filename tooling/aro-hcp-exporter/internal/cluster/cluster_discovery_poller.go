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
	client       graphquery.Querier
	query        string
	resultMutex  sync.Mutex
	discovered   bool
	discoveredCh chan struct{}
	rows         []clusterRow
	sleepTime    time.Duration
}

func NewClusterDiscoveryPoller(client graphquery.Querier, region string, clusterTypes []string, sleepTime time.Duration) *ClusterDiscoveryPoller {
	return &ClusterDiscoveryPoller{
		client:       client,
		query:        BuildClusterQuery(region, clusterTypes),
		resultMutex:  sync.Mutex{},
		sleepTime:    sleepTime,
		discovered:   false,
		discoveredCh: make(chan struct{}),
	}
}

func (c *ClusterDiscoveryPoller) Poll(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	default:
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
		if !c.discovered {
			c.discovered = true
			close(c.discoveredCh)
			logger.Info("First discovery of clusters", "total", len(newRows))
		}
		oldNames := clusterNames(c.rows)
		c.rows = newRows
		c.resultMutex.Unlock()

		newNames := clusterNames(newRows)
		if !oldNames.Equal(newNames) {
			added := newNames.Difference(oldNames)
			removed := oldNames.Difference(newNames)
			logger.Info("Discovered clusters changed",
				"total", newNames.Len(),
				"added", added.UnsortedList(),
				"removed", removed.UnsortedList(),
			)
		}
	}
	select {
	case <-ctx.Done():
	case <-time.After(c.sleepTime):
	}
}

func (c *ClusterDiscoveryPoller) GetDiscoverResult(ctx context.Context) DiscoverResult {
	select {
	case <-c.discoveredCh:
	case <-ctx.Done():
		return DiscoverResult{}
	}

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

func clusterNames(rows []clusterRow) sets.Set[string] {
	s := sets.New[string]()
	for _, r := range rows {
		s.Insert(r.Name)
	}
	return s
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
