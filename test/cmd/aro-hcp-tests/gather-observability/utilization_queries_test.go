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

package gatherobservability

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestUtilizationQueriesPromtool(t *testing.T) {
	promtool, err := exec.LookPath("promtool")
	if err != nil {
		t.Skip("promtool unavailable: ", err)
	}
	type inputSeries struct {
		Series string `json:"series"`
		Values string `json:"values"`
	}
	type sample struct {
		Labels string `json:"labels"`
		Value  int    `json:"value"`
	}
	clusters := []string{"cluster.a", "cluster-b"}
	for _, tc := range []struct {
		name     string
		queries  []utilizationQuery
		families []string
		labels   string
		count    int
	}{
		{
			name: "nodes", queries: utilizationHistoryQueries(), count: 1,
			families: []string{"kube_node_info", "kube_node_labels", "kube_node_status_capacity", "kube_node_status_allocatable"},
			labels:   `node="same",resource="cpu"`,
		},
		{
			name: "metadata", queries: utilizationSnapshotQueries(clusters), count: 2,
			families: []string{
				"kube_pod_info", "kube_pod_container_info", "kube_pod_status_phase", "kube_pod_status_scheduled",
				"kube_pod_owner", "kube_replicaset_owner", "kube_job_owner", "kube_replicationcontroller_owner",
			},
			labels: `namespace="ns",pod="same",uid="uid",node="same"`,
		},
	} {
		matched := 0
		for _, query := range tc.queries {
			if query.name != tc.name {
				continue
			}
			matched++
			t.Run(query.name+"/"+query.workspace, func(t *testing.T) {
				var inputs []inputSeries
				var expected []sample
				// Deliberately identical labels across families expose plain-or suppression.
				// Repeated object names across clusters expose accidental cross-cluster aggregation.
				for i, metric := range tc.families {
					for j, cluster := range clusters {
						labels := fmt.Sprintf("cluster=%q,%s", cluster, tc.labels)
						value := (i+1)*10 + j
						inputs = append(inputs,
							inputSeries{fmt.Sprintf(`%s{%s,replica="a"}`, metric, labels), fmt.Sprint(value - 1)},
							inputSeries{fmt.Sprintf(`%s{%s,replica="b"}`, metric, labels), fmt.Sprint(value)},
							inputSeries{fmt.Sprintf(`%s{%s,hostedcontrolplane="customer"}`, metric, labels), "999"},
						)
						expected = append(expected, sample{fmt.Sprintf("%s{%s}", metric, labels), value})
					}
					if tc.name == "metadata" {
						// Also catch failure to escape the dot in cluster.a's regex selector.
						inputs = append(inputs, inputSeries{fmt.Sprintf(`%s{cluster="clusterXa",%s}`, metric, tc.labels), "999"})
					}
				}
				fixture := map[string]any{
					"evaluation_interval": "1m",
					"tests": []any{map[string]any{
						"interval": "1m", "input_series": inputs,
						"promql_expr_test": []any{map[string]any{
							"expr": query.expression, "eval_time": "0m", "exp_samples": expected,
						}},
					}},
				}
				data, err := json.MarshalIndent(fixture, "", "  ")
				if err != nil {
					t.Fatal("marshal promtool fixture: ", err)
				}
				path := filepath.Join(t.TempDir(), "queries.json")
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal("write promtool fixture: ", err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if output, err := exec.CommandContext(ctx, promtool, "test", "rules", path).CombinedOutput(); err != nil {
					t.Fatalf("promtool failed: %v\n%s\nexpression: %s", err, output, query.expression)
				}
			})
		}
		if matched != tc.count {
			t.Errorf("expected %d %s queries, tested %d", tc.count, tc.name, matched)
		}
	}
}
