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
	rows []clusterRow
	err  error
}

func (m *mockQuerier) ExecuteConvertRequest(_ context.Context, request graphquery.ResourceGraphRequest) error {
	if m.err != nil {
		return m.err
	}
	return mapstructure.Decode(m.rows, request.Output)
}

func TestClusterDiscoveryPoller_GetDiscoverResult_BeforePoll(t *testing.T) {
	poller := NewClusterDiscoveryPoller(nil, "eastus", []string{"svc-cluster"}, time.Minute)

	result := poller.GetDiscoverResult()
	assert.Empty(t, result.ClusterNames)
	assert.Empty(t, result.SubscriptionIDs)
}

func TestClusterDiscoveryPoller_Poll_UpdatesResults(t *testing.T) {
	querier := &mockQuerier{
		rows: []clusterRow{
			{Name: "svc-1", SubscriptionId: "sub-a"},
			{Name: "mgmt-1", SubscriptionId: "sub-b"},
		},
	}
	poller := NewClusterDiscoveryPoller(querier, "eastus", []string{"svc-cluster"}, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	poller.Poll(ctx)

	result := poller.GetDiscoverResult()
	assert.ElementsMatch(t, []string{"svc-1", "mgmt-1"}, result.ClusterNames)
	assert.ElementsMatch(t, []string{"sub-a", "sub-b"}, result.SubscriptionIDs)
}

func TestClusterDiscoveryPoller_Poll_DeduplicatesSubscriptionIDs(t *testing.T) {
	querier := &mockQuerier{
		rows: []clusterRow{
			{Name: "cluster-a", SubscriptionId: "sub-1"},
			{Name: "cluster-b", SubscriptionId: "sub-2"},
			{Name: "cluster-c", SubscriptionId: "sub-1"},
		},
	}
	poller := NewClusterDiscoveryPoller(querier, "eastus", []string{"svc-cluster"}, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	poller.Poll(ctx)

	result := poller.GetDiscoverResult()
	assert.ElementsMatch(t, []string{"cluster-a", "cluster-b", "cluster-c"}, result.ClusterNames)
	assert.ElementsMatch(t, []string{"sub-1", "sub-2"}, result.SubscriptionIDs)
}

func TestClusterDiscoveryPoller_Poll_PreservesResultsAcrossMultiplePolls(t *testing.T) {
	querier := &mockQuerier{
		rows: []clusterRow{{Name: "c1", SubscriptionId: "s1"}},
	}
	poller := NewClusterDiscoveryPoller(querier, "eastus", []string{"svc-cluster"}, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for i := range 3 {
		poller.Poll(ctx)
		result := poller.GetDiscoverResult()
		require.Len(t, result.ClusterNames, 1, "poll %d: expected 1 cluster name", i)
		require.Len(t, result.SubscriptionIDs, 1, "poll %d: expected 1 subscription ID", i)
	}
}

func TestClusterDiscoveryPoller_Poll_ErrorKeepsPreviousResults(t *testing.T) {
	querier := &mockQuerier{
		rows: []clusterRow{{Name: "existing", SubscriptionId: "sub-1"}},
	}
	poller := NewClusterDiscoveryPoller(querier, "eastus", []string{"svc-cluster"}, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	poller.Poll(ctx)
	result := poller.GetDiscoverResult()
	require.ElementsMatch(t, []string{"existing"}, result.ClusterNames)

	querier.err = fmt.Errorf("network timeout")
	poller.Poll(ctx)

	result = poller.GetDiscoverResult()
	assert.ElementsMatch(t, []string{"existing"}, result.ClusterNames)
	assert.ElementsMatch(t, []string{"sub-1"}, result.SubscriptionIDs)
}

func TestClusterDiscoveryPoller_Poll_RespectsContextCancellation(t *testing.T) {
	querier := &mockQuerier{
		rows: []clusterRow{{Name: "new", SubscriptionId: "sub-1"}},
	}
	poller := NewClusterDiscoveryPoller(querier, "eastus", []string{"svc-cluster"}, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	poller.Poll(ctx)

	result := poller.GetDiscoverResult()
	assert.Empty(t, result.ClusterNames)
	assert.Empty(t, result.SubscriptionIDs)
}

func TestBuildClusterQuery(t *testing.T) {
	tests := []struct {
		name         string
		region       string
		clusterTypes []string
		wantContains []string
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := BuildClusterQuery(tt.region, tt.clusterTypes)

			assert.Contains(t, query, "| where type =~ 'Microsoft.ContainerService/managedClusters'")
			for _, want := range tt.wantContains {
				assert.Contains(t, query, want)
			}
		})
	}
}
