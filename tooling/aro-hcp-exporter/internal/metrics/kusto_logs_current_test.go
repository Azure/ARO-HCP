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

package metrics

import (
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/tooling/aro-hcp-exporter/internal/cluster"
)

func TestKustoLogsCurrentCollectorLabels(t *testing.T) {
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "dev")
	for _, testCase := range []struct {
		name        string
		clusterType string
		wantType    string
	}{
		{name: "service", clusterType: "svc-cluster", wantType: "svc-cluster"},
		{name: "management", clusterType: "mgmt-cluster", wantType: "mgmt-cluster"},
		{name: "raw tag", clusterType: "SVC-CLUSTER", wantType: "SVC-CLUSTER"},
		{name: "missing type", wantType: "unknown"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			collector, err := NewKustoLogsCurrentCollector("test-kusto", "eastus", nil, time.Minute, nil)
			require.NoError(t, err)
			clusterTypes := clusterTypesByName([]cluster.ClusterInfo{
				{Name: "test-cluster", SubscriptionId: "test-sub", ClusterType: testCase.clusterType},
			})
			collector.cacheLogAge("test-cluster", clusterTypes["test-cluster"], "containerLogs", time.Now().Add(-time.Minute))
			registry := prometheus.NewPedanticRegistry()
			require.NoError(t, registry.Register(collector))
			families, err := registry.Gather()
			require.NoError(t, err)
			require.Len(t, families, 1)
			assert.Equal(t, "kusto_logs_age_in_seconds", families[0].GetName())
			require.Len(t, families[0].Metric, 1)
			metric := families[0].Metric[0]
			labels := map[string]string{}
			for _, label := range metric.Label {
				labels[label.GetName()] = label.GetValue()
			}
			assert.Equal(t, map[string]string{
				"kusto_cluster": "test-kusto",
				"cluster":       "test-cluster",
				"table":         "containerLogs",
				"cluster_type":  testCase.wantType,
			}, labels)
			assert.InDelta(t, 60, metric.GetGauge().GetValue(), 5)
		})
	}
}

func TestClusterTypesByName(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		clusterTypes []string
		wantType     string
	}{
		{name: "matching types", clusterTypes: []string{"svc-cluster", "svc-cluster"}, wantType: "svc-cluster"},
		{name: "conflict service first", clusterTypes: []string{"svc-cluster", "mgmt-cluster", "svc-cluster"}, wantType: "unknown"},
		{name: "conflict management first", clusterTypes: []string{"mgmt-cluster", "svc-cluster", "mgmt-cluster"}, wantType: "unknown"},
		{name: "missing type", clusterTypes: []string{""}, wantType: "unknown"},
		{name: "missing type first", clusterTypes: []string{"", "svc-cluster", "svc-cluster"}, wantType: "unknown"},
		{name: "missing type last", clusterTypes: []string{"svc-cluster", ""}, wantType: "unknown"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			clusters := []cluster.ClusterInfo{
				{Name: "other-cluster", SubscriptionId: "other-sub", ClusterType: "mgmt-cluster"},
			}
			for index, clusterType := range testCase.clusterTypes {
				clusters = append(clusters, cluster.ClusterInfo{
					Name: "test-cluster", SubscriptionId: fmt.Sprintf("sub-%d", index), ClusterType: clusterType,
				})
				if index > 0 {
					assert.Equal(t, testCase.wantType, clusterTypesByName(clusters)["test-cluster"])
				}
			}
			assert.Equal(t, map[string]string{
				"test-cluster":  testCase.wantType,
				"other-cluster": "mgmt-cluster",
			}, clusterTypesByName(clusters))
		})
	}
	assert.Empty(t, clusterTypesByName(nil))
}
