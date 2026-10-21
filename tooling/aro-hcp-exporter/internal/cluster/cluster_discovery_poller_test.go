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
	"testing"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/tooling/aro-hcp-exporter/pkg/graphquery"
)

type mockQuerier struct {
	rows any
	err  error
}

func (m *mockQuerier) ExecuteConvertRequest(_ context.Context, request graphquery.ResourceGraphRequest) error {
	if m.err != nil {
		return m.err
	}
	return mapstructure.Decode(m.rows, request.Output)
}

func TestClusterDiscoveryPoller_GetDiscoverResult_BeforePoll(t *testing.T) {
	poller := NewClusterDiscoveryPoller(nil, "eastus", []string{"svc-cluster"}, "", time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := poller.GetDiscoverResult(ctx)
	assert.Empty(t, result.Clusters)
}

func TestClusterDiscoveryPoller_Poll_UpdatesResults(t *testing.T) {
	querier := &mockQuerier{
		rows: []map[string]any{
			{"name": "svc-1", "subscriptionId": "sub-a", "clusterType": "svc-cluster"},
			{"name": "mgmt-1", "subscriptionId": "sub-b", "clusterType": "mgmt-cluster"},
		},
	}
	poller := NewClusterDiscoveryPoller(querier, "eastus", []string{"svc-cluster"}, "", 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	poller.Poll(ctx)

	result := poller.GetDiscoverResult(ctx)
	assert.Equal(t, []ClusterInfo{
		{Name: "svc-1", SubscriptionId: "sub-a", ClusterType: "svc-cluster"},
		{Name: "mgmt-1", SubscriptionId: "sub-b", ClusterType: "mgmt-cluster"},
	}, result.Clusters)

	querier.rows = []map[string]any{
		{"name": "svc-1", "subscriptionId": "sub-a", "clusterType": "SVC-CLUSTER"},
		{"name": "untagged", "subscriptionId": "sub-a"},
	}
	poller.Poll(ctx)
	result = poller.GetDiscoverResult(ctx)
	assert.Equal(t, []ClusterInfo{
		{Name: "svc-1", SubscriptionId: "sub-a", ClusterType: "SVC-CLUSTER"},
		{Name: "untagged", SubscriptionId: "sub-a"},
	}, result.Clusters)
}

func TestClusterDiscoveryPoller_Poll_PreservesClusterInfo(t *testing.T) {
	querier := &mockQuerier{
		rows: []ClusterInfo{
			{Name: "cluster-a", SubscriptionId: "sub-1", ClusterType: "svc-cluster"},
			{Name: "cluster-a", SubscriptionId: "sub-2", ClusterType: "mgmt-cluster"},
			{Name: "cluster-c", SubscriptionId: "sub-1", ClusterType: "svc-cluster"},
		},
	}
	poller := NewClusterDiscoveryPoller(querier, "eastus", []string{"svc-cluster"}, "", 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	poller.Poll(ctx)

	result := poller.GetDiscoverResult(ctx)
	assert.Equal(t, querier.rows, result.Clusters)
	result.Clusters[0].ClusterType = "changed"
	assert.Equal(t, querier.rows, poller.GetDiscoverResult(ctx).Clusters)
}

func TestClusterDiscoveryPoller_Poll_PreservesResultsAcrossMultiplePolls(t *testing.T) {
	querier := &mockQuerier{
		rows: []ClusterInfo{{Name: "c1", SubscriptionId: "s1"}},
	}
	poller := NewClusterDiscoveryPoller(querier, "eastus", []string{"svc-cluster"}, "", 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for i := range 3 {
		poller.Poll(ctx)
		result := poller.GetDiscoverResult(ctx)
		require.Equal(t, querier.rows, result.Clusters, "poll %d", i)
	}
}

func TestClusterDiscoveryPoller_Poll_ErrorKeepsPreviousResults(t *testing.T) {
	querier := &mockQuerier{
		rows: []ClusterInfo{{Name: "existing", SubscriptionId: "sub-1"}},
	}
	poller := NewClusterDiscoveryPoller(querier, "eastus", []string{"svc-cluster"}, "", 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	poller.Poll(ctx)
	result := poller.GetDiscoverResult(ctx)
	require.Equal(t, querier.rows, result.Clusters)

	querier.err = fmt.Errorf("network timeout")
	poller.Poll(ctx)

	result = poller.GetDiscoverResult(ctx)
	assert.Equal(t, querier.rows, result.Clusters)
}

func TestClusterDiscoveryPoller_Poll_RespectsContextCancellation(t *testing.T) {
	querier := &mockQuerier{
		rows: []ClusterInfo{{Name: "new", SubscriptionId: "sub-1"}},
	}
	poller := NewClusterDiscoveryPoller(querier, "eastus", []string{"svc-cluster"}, "", time.Hour)

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	poller.Poll(cancelledCtx)

	result := poller.GetDiscoverResult(cancelledCtx)
	assert.Empty(t, result.Clusters)
}

func TestBuildClusterQuery(t *testing.T) {
	tests := []struct {
		name            string
		region          string
		clusterTypes    []string
		clusterFilter   string
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:         "single cluster type",
			region:       "eastus",
			clusterTypes: []string{"svc-cluster"},
			wantContains: []string{
				"| where location =~ 'eastus'",
				"| where tags['clusterType'] in~ ('svc-cluster')",
				"| project name, subscriptionId",
			},
			wantNotContains: []string{"| where name contains"},
		},
		{
			name:         "multiple cluster types",
			region:       "westus2",
			clusterTypes: []string{"svc-cluster", "mgmt-cluster"},
			wantContains: []string{
				"| where location =~ 'westus2'",
				"| where tags['clusterType'] in~ ('svc-cluster', 'mgmt-cluster')",
			},
		},
		{
			name:         "region is passed through as-is",
			region:       "eastus",
			clusterTypes: []string{"svc-cluster"},
			wantContains: []string{
				"| where location =~ 'eastus'",
			},
		},
		{
			name:          "with cluster name filter",
			region:        "westus3",
			clusterTypes:  []string{"svc-cluster", "mgmt-cluster"},
			clusterFilter: "cspr-westus3",
			wantContains: []string{
				"| where name contains 'cspr-westus3'",
				"| project name, subscriptionId",
			},
		},
		{
			name:            "empty cluster name filter omits clause",
			region:          "eastus",
			clusterTypes:    []string{"svc-cluster"},
			clusterFilter:   "",
			wantNotContains: []string{"| where name contains"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := BuildClusterQuery(tt.region, tt.clusterTypes, tt.clusterFilter)

			assert.Contains(t, query, "| where type =~ 'Microsoft.ContainerService/managedClusters'")
			assert.Contains(t, query, "| project name, subscriptionId, clusterType = tostring(tags['clusterType'])")
			for _, want := range tt.wantContains {
				assert.Contains(t, query, want)
			}
			for _, notWant := range tt.wantNotContains {
				assert.NotContains(t, query, notWant)
			}
		})
	}
}
