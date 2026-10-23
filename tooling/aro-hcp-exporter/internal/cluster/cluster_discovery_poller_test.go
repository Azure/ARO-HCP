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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
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

// TestClusterDiscoveryPoller_Poll_SleepsOnError is the regression test for the
// retry storm: Poll used to return before its sleep whenever the Resource Graph
// query failed, so the caller's `for { Poll(ctx) }` loop spun without backoff.
func TestClusterDiscoveryPoller_Poll_SleepsOnError(t *testing.T) {
	querier := &mockQuerier{err: fmt.Errorf("resource graph throttled")}
	poller := NewClusterDiscoveryPoller(querier, "eastus", []string{"svc-cluster"}, "", time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	poller.Poll(ctx)
	elapsed := time.Since(start)

	assert.GreaterOrEqual(t, elapsed, 100*time.Millisecond,
		"Poll returned without waiting after a failed query, which lets the caller hot-loop")
}

func TestClusterDiscoveryPoller_retryDelay(t *testing.T) {
	tests := []struct {
		name                string
		sleepTime           time.Duration
		consecutiveFailures int
		discovered          bool
		wantAtLeast         time.Duration
		wantAtMost          time.Duration
	}{
		{
			name:                "zero interval stays zero so tests do not sleep",
			sleepTime:           0,
			consecutiveFailures: 5,
			discovered:          true,
			wantAtLeast:         0,
			wantAtMost:          0,
		},
		{
			name:                "before first success the interval never escalates",
			sleepTime:           2 * time.Minute,
			consecutiveFailures: 5,
			discovered:          false,
			wantAtLeast:         2 * time.Minute,
			wantAtMost:          2*time.Minute + 24*time.Second,
		},
		{
			name:                "first failure waits one poll interval",
			sleepTime:           2 * time.Minute,
			consecutiveFailures: 1,
			discovered:          true,
			wantAtLeast:         2 * time.Minute,
			wantAtMost:          2*time.Minute + 24*time.Second,
		},
		{
			name:                "second failure doubles",
			sleepTime:           2 * time.Minute,
			consecutiveFailures: 2,
			discovered:          true,
			wantAtLeast:         4 * time.Minute,
			wantAtMost:          4*time.Minute + 48*time.Second,
		},
		{
			name:                "growth is capped",
			sleepTime:           2 * time.Minute,
			consecutiveFailures: 20,
			discovered:          true,
			wantAtLeast:         maxDiscoveryBackoff,
			wantAtMost:          maxDiscoveryBackoff + 2*time.Minute,
		},
		{
			name:                "poll interval longer than the cap is never shortened",
			sleepTime:           30 * time.Minute,
			consecutiveFailures: 3,
			discovered:          true,
			wantAtLeast:         30 * time.Minute,
			wantAtMost:          36 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			poller := NewClusterDiscoveryPoller(nil, "eastus", []string{"svc-cluster"}, "", tt.sleepTime)
			poller.consecutiveFailures = tt.consecutiveFailures
			poller.discovered = tt.discovered

			delay := poller.retryDelay()

			assert.GreaterOrEqual(t, delay, tt.wantAtLeast)
			assert.LessOrEqual(t, delay, tt.wantAtMost)
		})
	}
}

// TestClusterDiscoveryPoller_retryDelay_FlatUntilFirstSuccess pins the recovery
// window while the exporter has published nothing. Escalating here would widen
// the gap in which a recovered Resource Graph goes unnoticed, with no cached
// data to protect in exchange.
func TestClusterDiscoveryPoller_retryDelay_FlatUntilFirstSuccess(t *testing.T) {
	const pollInterval = 2 * time.Minute
	poller := NewClusterDiscoveryPoller(nil, "eastus", []string{"svc-cluster"}, "", pollInterval)

	elapsed := time.Duration(0)
	for attempt := 1; attempt <= 8; attempt++ {
		poller.consecutiveFailures = attempt
		delay := poller.retryDelay()
		require.LessOrEqual(t, delay, pollInterval+pollInterval/5,
			"attempt %d escalated beyond one jittered poll interval", attempt)
		elapsed += delay
	}

	assert.LessOrEqual(t, elapsed, 20*time.Minute,
		"eight failed attempts must still fit inside the e2e budget; got %s", elapsed)
}

func TestClusterDiscoveryPoller_Poll_ResetsBackoffAfterSuccess(t *testing.T) {
	querier := &mockQuerier{err: fmt.Errorf("resource graph throttled")}
	poller := NewClusterDiscoveryPoller(querier, "eastus", []string{"svc-cluster"}, "", 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	poller.Poll(ctx)
	poller.Poll(ctx)
	require.Equal(t, 2, poller.consecutiveFailures)

	querier.err = nil
	querier.rows = []ClusterInfo{{Name: "c1", SubscriptionId: "sub-1"}}
	poller.Poll(ctx)

	assert.Zero(t, poller.consecutiveFailures, "a successful poll must reset the backoff")
}

// TestClusterDiscoveryPoller_HealthMetrics covers the signal that makes a
// wedged exporter diagnosable: the gauges are emitted from process start, so
// "discovery never succeeded" is distinguishable from "nothing to report".
func TestClusterDiscoveryPoller_HealthMetrics(t *testing.T) {
	querier := &mockQuerier{err: fmt.Errorf("resource graph throttled")}
	poller := NewClusterDiscoveryPoller(querier, "eastus", []string{"svc-cluster"}, "", 0)

	registry := prometheus.NewRegistry()
	for _, collector := range poller.Collectors() {
		require.NoError(t, registry.Register(collector))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	assert.Zero(t, testutil.ToFloat64(poller.lastSuccess),
		"last success timestamp must stay 0 until a query succeeds")

	poller.Poll(ctx)
	assert.Equal(t, 1.0, testutil.ToFloat64(poller.errorsTotal))
	assert.Zero(t, testutil.ToFloat64(poller.lastSuccess))

	querier.err = nil
	querier.rows = []ClusterInfo{
		{Name: "c1", SubscriptionId: "sub-1"},
		{Name: "c2", SubscriptionId: "sub-1"},
	}
	poller.Poll(ctx)

	assert.Equal(t, 1.0, testutil.ToFloat64(poller.errorsTotal), "a success must not bump the error counter")
	assert.Equal(t, 2.0, testutil.ToFloat64(poller.clustersFound))
	assert.Positive(t, testutil.ToFloat64(poller.lastSuccess), "last success timestamp must be set after a successful query")
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
