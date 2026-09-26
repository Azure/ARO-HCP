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
	"strings"
	"testing"
	"time"
)

func TestReplicaPeakQueries(t *testing.T) {
	for _, tc := range []struct {
		name       string
		start, end int64
		window     string
	}{
		{"inclusive", 120, 360, "[241s:60s] @ 360"},
		{"unaligned", 121, 359, "[121s:60s] @ 300"},
		{"single", 120, 120, "[1s:60s] @ 120"},
		{"no grid", 121, 179, ""},
		{"reversed", 360, 120, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			zone := time.FixedZone("non-UTC", 90*60)
			queries := replicaPeakQueries("cluster.\"a\\b", time.Unix(tc.start, 0).In(zone), time.Unix(tc.end, 0).In(zone))
			if tc.window == "" {
				if len(queries) != 0 {
					t.Fatal("empty grid produced queries")
				}
				return
			}
			want := map[string]int{"cpu/" + workspaceSvc: 1, "cpuSustained/" + workspaceSvc: 1, "memory/" + workspaceSvc: 1}
			for _, ws := range []string{workspaceSvc, workspaceHcp} {
				for _, name := range []string{"requests", "initRequests", "metadata"} {
					want[name+"/"+ws] = 1
				}
			}
			for _, query := range queries {
				want[query.name+"/"+query.workspace]--
				for _, fragment := range []string{tc.window, `cluster="cluster.\"a\\b"`, `hostedcontrolplane=""`} {
					if !strings.Contains(query.expression, fragment) {
						t.Errorf("%s missing %s: %s", query.name, fragment, query.expression)
					}
				}
			}
			for key, remaining := range want {
				if remaining != 0 {
					t.Errorf("query multiplicity mismatch for %s: %d", key, remaining)
				}
			}
		})
	}
}

// Run with go test -run TestReplicaPeakQueriesPromtool. Fixtures are generated in
// t.TempDir so promtool exercises the actual expressions without checked-in YAML.
func TestReplicaPeakQueriesPromtool(t *testing.T) {
	promtool, err := exec.LookPath("promtool")
	if err != nil {
		t.Skip("promtool unavailable: ", err)
	}
	type inputSeries struct {
		Series string `json:"series"`
		Values string `json:"values"`
	}
	type sample struct {
		Labels string  `json:"labels"`
		Value  float64 `json:"value"`
	}
	const scope = `cluster="cluster.a",namespace="ns",container="main"`
	oldUsage := scope + `,pod="reused",node="node",instance="node:10250",id="/old"`
	newUsage := scope + `,pod="reused",node="node",instance="node:10250",id="/new"`
	otherUsage := scope + `,pod="other",node="node",instance="node:10250",id="/other"`
	oldRequest := scope + `,pod="reused",uid="old",resource="cpu"`
	newRequest := scope + `,pod="reused",uid="new",resource="cpu"`
	for _, query := range replicaPeakQueries("cluster.a", time.Unix(120, 0), time.Unix(360, 0)) {
		t.Run(query.name+"/"+query.workspace, func(t *testing.T) {
			var inputs []inputSeries
			var expected []sample
			add := func(metric, labels, values string) {
				inputs = append(inputs, inputSeries{metric + "{" + labels + "}", values})
			}
			summary := func(labels string, maximum, minimum, count, first, last float64) {
				values := map[string]float64{"max": maximum, "count": count, "first": first, "last": last}
				if query.name == "requests" || query.name == "initRequests" {
					values["min"] = minimum
				}
				for statistic, value := range values {
					expected = append(expected, sample{fmt.Sprintf(`{%s,statistic=%q}`, labels, statistic), value})
				}
			}
			var metric, labels, excludedValues string
			switch query.name {
			case "cpu", "cpuSustained":
				metric, labels, excludedValues = "container_cpu_usage_seconds_total", oldUsage, "0+3000x13"
				// 30-second counter scrapes: deleted incarnation, replacement, and
				// an unequal live pod. Duplicate exporters must not double peaks/counts.
				for _, replica := range []string{"a", "b"} {
					add(metric, oldUsage+fmt.Sprintf(`,prometheus_replica=%q`, replica), "0+30x4 stale _x7")
					add(metric, newUsage+fmt.Sprintf(`,prometheus_replica=%q`, replica), "_x5 600+60x6")
				}
				add(metric, otherUsage, "0+7.5x12")
				resetUsage := strings.ReplaceAll(otherUsage, "/other", "/reset")
				add(metric, resetUsage, "0 15 30 45 60 120 180 0 30 60 90 120 150")
				if query.name == "cpu" {
					summary(oldUsage, 1, 0, 2, 120, 180)
					summary(newUsage, 2, 0, 4, 180, 360)
					summary(otherUsage, .25, 0, 5, 120, 360)
					summary(resetUsage, 1.5, 0, 5, 120, 360)
				} else {
					// With less than 10m of history, rate extrapolates only near
					// the scrapes (and never before counter zero), then divides by 600s.
					summary(oldUsage, .225, 0, 5, 120, 360)
					summary(newUsage, .75, 0, 4, 180, 360)
					summary(otherUsage, .15, 0, 5, 120, 360)
					summary(resetUsage, .55, 0, 5, 120, 360)
				}
			case "memory":
				metric, labels, excludedValues = "container_memory_working_set_bytes", oldUsage, "99999+0x13"
				add(metric, oldUsage+`,prometheus_replica="a"`, "99999 99999 99999 900 100 stale _x7")
				add(metric, oldUsage+`,prometheus_replica="b"`, "99999 99999 99999 700 100 stale _x7")
				add(metric, newUsage, "_x5 200 800 200 200 200 200 200")
				add(metric, otherUsage, "50+0x12")
				summary(oldUsage, 900, 0, 1, 120, 120)
				summary(newUsage, 800, 0, 4, 180, 360)
				summary(otherUsage, 50, 0, 5, 120, 360)
			case "requests", "initRequests":
				metric = "kube_pod_container_resource_requests"
				otherMetric := "kube_pod_init_container_resource_requests"
				if query.name == "initRequests" {
					metric, otherMetric = otherMetric, metric
				}
				labels, excludedValues = oldRequest, "99999+0x13"
				add(metric, oldRequest+`,prometheus_replica="a"`, "99 99 99 1 1 1 2 2 stale _x4")
				add(metric, oldRequest+`,prometheus_replica="b"`, "99 99 99 .5 .5 .5 1 1 stale _x4")
				add(metric, newRequest, "_x7 0 0 4 4 4")
				add(metric, strings.ReplaceAll(newRequest, `resource="cpu"`, `resource="memory"`), "_x7 1024+0x4")
				add(otherMetric, newRequest, excludedValues)
				add(metric, strings.ReplaceAll(newRequest, `resource="cpu"`, `resource="other"`), excludedValues)
				summary(oldRequest, 2, 1, 2, 120, 180)
				summary(newRequest, 4, 0, 3, 240, 360)
				summary(strings.ReplaceAll(newRequest, `resource="cpu"`, `resource="memory"`), 1024, 1024, 3, 240, 360)
			case "metadata":
				metric, labels, excludedValues = "kube_pod_container_info", scope+`,pod="reused",uid="old",container_id="containerd://old"`, "1+0x13"
				for _, family := range []string{"kube_pod_container_info", "kube_pod_init_container_info", "kube_pod_owner", "kube_replicaset_owner", "kube_job_owner", "kube_replicationcontroller_owner"} {
					// Identical labels across families catch union suppression and
					// loss of __name__ during reduction; include all join labels.
					identity := labels + `,owner_kind="ReplicaSet",owner_name="rs",owner_is_controller="true",replicaset="rs",job_name="job",replicationcontroller="rc"`
					for _, replica := range []string{"a", "b"} {
						add(family, identity+fmt.Sprintf(`,prometheus_replica=%q`, replica), "1+0x4 stale _x7")
					}
					expected = append(expected, sample{fmt.Sprintf(`{%s,metric=%q}`, identity, family), 1})
					add(family, strings.ReplaceAll(identity, `uid="old"`, `uid="zero"`), "0+0x12")
					add(family, strings.ReplaceAll(identity, `uid="old"`, `uid="invalid"`), "2+0x12")
				}
				add(metric, strings.ReplaceAll(labels, "old", "new"), "_x7 1+0x4")
				expected = append(expected, sample{fmt.Sprintf(`{%s,metric=%q}`, strings.ReplaceAll(labels, "old", "new"), metric), 1})
			}
			add(metric, strings.ReplaceAll(labels, "cluster.a", "clusterXa"), excludedValues)
			add(metric, labels+`,hostedcontrolplane="customer"`, excludedValues)
			// Older promtool versions compare floats exactly. Round only in the
			// test harness to tolerate rate extrapolation's floating-point error.
			testExpression := func(expression string) string {
				if query.name == "cpu" || query.name == "cpuSustained" {
					return "round((" + expression + ") * 1e9) / 1e9"
				}
				return expression
			}
			fixtureTests := []any{map[string]any{
				"interval": "30s", "input_series": inputs,
				"promql_expr_test": []any{map[string]any{
					"expr": testExpression(query.expression), "eval_time": "6m", "exp_samples": expected,
				}},
			}}
			// Exercise actual subquery boundaries, including a one-point range
			// and fractional seconds, rather than just checking query strings.
			for _, window := range []struct {
				start, end         time.Time
				count, first, last float64
			}{
				{time.Unix(120, 0), time.Unix(120, 0), 1, 120, 120},
				{time.Unix(121, 0), time.Unix(359, 0), 3, 180, 300},
				{time.Unix(120, 1), time.Unix(360, 0), 4, 180, 360},
			} {
				values := "1+0x12"
				if query.name == "cpu" || query.name == "cpuSustained" {
					values = "0+30x12"
				}
				expected = nil
				switch query.name {
				case "metadata":
					expected = append(expected, sample{fmt.Sprintf(`{%s,metric=%q}`, labels, metric), 1})
				case "cpuSustained":
					summary(labels, window.last/600, 0, window.count, window.first, window.last)
				default:
					summary(labels, 1, 1, window.count, window.first, window.last)
				}
				for _, bounded := range replicaPeakQueries("cluster.a", window.start, window.end) {
					if bounded.name != query.name || bounded.workspace != query.workspace {
						continue
					}
					fixtureTests = append(fixtureTests, map[string]any{
						"interval": "30s", "input_series": []inputSeries{{metric + "{" + labels + "}", values}},
						"promql_expr_test": []any{map[string]any{
							"expr": testExpression(bounded.expression), "eval_time": fmt.Sprintf("%ds", window.end.Unix()), "exp_samples": expected,
						}},
					})
				}
			}
			if query.name == "cpu" || query.name == "cpuSustained" {
				// A 30s burst after a full 10m baseline has the same identity
				// in both queries, but a much lower sustained peak.
				for _, spike := range replicaPeakQueries("cluster.a", time.Unix(600, 0), time.Unix(720, 0)) {
					if spike.name != query.name {
						continue
					}
					maximum := 6.666666667 // 600 CPU seconds / 90 sampled seconds
					if query.name == "cpuSustained" {
						maximum = 1.052631579 // 600 CPU seconds / 570 sampled seconds
					}
					expected = nil
					summary(oldUsage, maximum, 0, 3, 600, 720)
					fixtureTests = append(fixtureTests, map[string]any{
						"interval": "30s", "input_series": []inputSeries{{metric + "{" + oldUsage + "}", "600+0x20 1200+0x3"}},
						"promql_expr_test": []any{map[string]any{
							"expr": testExpression(spike.expression), "eval_time": "12m", "exp_samples": expected,
						}},
					})
				}
			}
			fixture := map[string]any{
				"evaluation_interval": "1m", "tests": fixtureTests,
			}
			data, err := json.MarshalIndent(fixture, "", "  ")
			if err != nil {
				t.Fatal("marshal promtool fixture: ", err)
			}
			path := filepath.Join(t.TempDir(), "replica-peaks.json")
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
}
