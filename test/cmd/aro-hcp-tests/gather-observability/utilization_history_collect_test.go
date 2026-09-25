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
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func utilizationTestRequestHistory(minutes int) ([]utilizationHistorySample, []utilizationQueryResult) {
	end := utilizationTestTime.Add(time.Duration(minutes-1) * time.Minute)
	history, _ := utilizationBuildHistory(utilizationTestHistory([]string{"mgmt"}, minutes), utilizationTestTime, end)
	samples := utilizationRetainHistory(history, utilizationTestTime, end, nil)
	var results []utilizationQueryResult
	for _, query := range utilizationRequestHistoryQueries([]string{"mgmt"}) {
		result := utilizationQueryResult{query: query}
		if query.name == "history metadata" && query.workspace == workspaceSvc {
			for _, sample := range samples {
				result.series = append(result.series, utilizationTestSeries(sample.Time, 1, "__name__", "kube_state_metrics_list_total", "cluster", "mgmt"))
			}
		}
		results = append(results, result)
	}
	return samples, results
}

func utilizationTestHistoryPod(results []utilizationQueryResult, ws int, at time.Time, pod, uid, node, phase string, cpu float64) {
	base := []string{"cluster", "mgmt", "namespace", "ns", "pod", pod, "uid", uid}
	for _, labels := range [][]string{
		{"__name__", "kube_pod_info", "node", node},
		{"__name__", "kube_pod_status_phase", "phase", phase},
		{"__name__", "kube_pod_container_info", "container", "main"},
	} {
		results[ws].series = append(results[ws].series, utilizationTestSeries(at, 1, append(slices.Clone(base), labels...)...))
	}
	for i, resource := range []string{"cpu", "memory", "aro_openshift_io_swift_nic"} {
		results[ws+1].series = append(results[ws+1].series, utilizationTestSeries(at, cpu*float64(i+1), append(slices.Clone(base), "container", "main", "resource", resource)...))
	}
}

func TestUtilizationRequestHistoryWorkspaceSumAndDedup(t *testing.T) {
	samples, results := utilizationTestRequestHistory(2)
	utilizationTestHistoryPod(results, 0, samples[0].Time, "svc-pod", "svc-uid", "node", "Running", 2)
	utilizationTestHistoryPod(results, 2, samples[0].Time, "hcp-pod", "hcp-uid", "node", "Running", 3)
	utilizationTestHistoryPod(results, 2, samples[0].Time, "svc-pod", "svc-uid", "node", "Running", 2)
	// A second regular container and duplicate replica series must sum only once.
	for _, s := range slices.Clone(results[1].series) {
		s.Metric = maps.Clone(s.Metric)
		s.Metric["container"] = "sidecar"
		results[1].series = append(results[1].series, s)
	}
	for i := range results {
		results[i].series = append(results[i].series, results[i].series...)
	}
	utilizationBuildRequestHistory(samples, results, []string{"mgmt"})
	got := samples[0].Nodes[0].Requests
	if got.CPU == nil || *got.CPU != 7 || got.Memory == nil || *got.Memory != 14 || got.SwiftNIC == nil || *got.SwiftNIC != 21 {
		t.Fatalf("cross-workspace/replica/container totals incorrect: %+v; warnings=%v", got, samples[0].Warnings)
	}
	if got := samples[1].Nodes[0].Requests; got.CPU == nil || *got.CPU != 0 {
		t.Errorf("heartbeat-backed empty minute should be zero, not carry previous demand: %+v", got)
	}
}

func TestUtilizationRequestHistoryUnknownCoverage(t *testing.T) {
	for _, test := range []string{"empty HTTP", "missing query", "failed query", "missing phase", "missing containers", "missing placement", "UID conflict", "missing UID", "node conflict", "request node conflict", "missing node"} {
		t.Run(test, func(t *testing.T) {
			samples, results := utilizationTestRequestHistory(1)
			utilizationTestHistoryPod(results, 0, samples[0].Time, "pod", "uid", "node", "Running", 2)
			switch test {
			case "empty HTTP":
				for i := range results {
					results[i].series = nil
				}
			case "missing query":
				results = results[:3]
			case "failed query":
				results[3].err = errors.New("denied")
			case "missing phase", "missing containers", "missing placement":
				metric := map[string]string{"missing phase": "kube_pod_status_phase", "missing containers": "kube_pod_container_info", "missing placement": "kube_pod_info"}[test]
				results[0].series = slices.DeleteFunc(results[0].series, func(s PrometheusResult) bool { return s.Metric["__name__"] == metric })
				if test == "missing containers" {
					results[1].series = nil
				}
			case "UID conflict":
				utilizationTestHistoryPod(results, 2, samples[0].Time, "pod", "old-uid", "node", "Running", 999)
			case "missing UID":
				delete(results[1].series[0].Metric, "uid")
			case "node conflict":
				utilizationTestHistoryPod(results, 2, samples[0].Time, "pod", "uid", "other", "Running", 999)
			case "request node conflict":
				results[1].series[0].Metric["node"] = "other"
			case "missing node":
				samples[0].Nodes[0].Name = "other"
			}
			utilizationBuildRequestHistory(samples, results, []string{"mgmt"})
			got := samples[0].Nodes[0].Requests
			if got.CPU != nil || got.Memory != nil || got.SwiftNIC != nil || len(samples[0].Warnings) == 0 {
				t.Errorf("unknown coverage fabricated totals: %+v warnings=%v", got, samples[0].Warnings)
			}
		})
	}
}

func TestUtilizationRequestHistorySharedCollectorCoverage(t *testing.T) {
	for _, test := range []struct {
		name                         string
		inventory, heartbeat, hcpPod bool
		failedQuery                  int
		known                        bool
	}{
		{"svc inventory with empty hcp", true, false, false, -1, true},
		{"svc heartbeat with empty hcp", false, true, false, -1, true},
		{"no inventory or heartbeat", false, false, false, -1, false},
		{"hcp inventory alone insufficient", false, false, true, -1, false},
		{"failed hcp metadata", true, false, false, 2, false},
		{"failed hcp requests", true, false, false, 3, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			samples, results := utilizationTestRequestHistory(1)
			if !test.heartbeat {
				results[0].series = nil
			}
			if test.inventory {
				utilizationTestHistoryPod(results, 0, samples[0].Time, "system-pod", "uid", "node", "Running", 2)
			}
			if test.hcpPod {
				utilizationTestHistoryPod(results, 2, samples[0].Time, "hcp-pod", "hcp-uid", "node", "Running", 3)
			}
			if test.failedQuery >= 0 {
				results[test.failedQuery].err = errors.New("denied")
			}
			utilizationBuildRequestHistory(samples, results, []string{"mgmt"})
			got := samples[0].Nodes[0].Requests
			if !test.known {
				if got.CPU != nil || got.Memory != nil || got.SwiftNIC != nil || len(samples[0].Warnings) == 0 {
					t.Fatalf("uncovered requests must remain unknown: %+v warnings=%v", got, samples[0].Warnings)
				}
				return
			}
			want := 0.0
			if test.inventory {
				want = 2
			}
			if got.CPU == nil || *got.CPU != want || got.Memory == nil || *got.Memory != 2*want || got.SwiftNIC == nil || *got.SwiftNIC != 3*want || len(samples[0].Warnings) != 0 {
				t.Fatalf("shared svc collector should cover empty hcp: %+v warnings=%v", got, samples[0].Warnings)
			}
		})
	}
}

func TestUtilizationRequestHistorySpecBackedContainers(t *testing.T) {
	samples, results := utilizationTestRequestHistory(1)
	utilizationTestHistoryPod(results, 0, samples[0].Time, "pod", "uid", "node", "Pending", 2)
	results[0].series = slices.DeleteFunc(results[0].series, func(s PrometheusResult) bool { return s.Metric["__name__"] == "kube_pod_container_info" })
	// Retain the existing convention: observed regular-container requests can
	// supply inventory before runtime status exists, without querying limits.
	utilizationBuildRequestHistory(samples, results, []string{"mgmt"})
	if got := samples[0].Nodes[0].Requests; got.CPU == nil || *got.CPU != 2 || got.Memory == nil || *got.Memory != 4 || got.SwiftNIC == nil || *got.SwiftNIC != 6 {
		t.Fatalf("spec-backed demand lost: %+v", got)
	}
}

func TestUtilizationRequestHistoryPhasePlacementAndZero(t *testing.T) {
	for _, test := range []struct {
		phase, node string
		want        float64
	}{
		{"Succeeded", "node", 0}, {"FAILED", "node", 0}, {"Pending", "", 0}, {"pending", "node", 2}, {"Running", "node", 2},
	} {
		t.Run(test.phase+test.node, func(t *testing.T) {
			samples, results := utilizationTestRequestHistory(1)
			utilizationTestHistoryPod(results, 0, samples[0].Time, "pod", "uid", test.node, test.phase, 2)
			utilizationBuildRequestHistory(samples, results, []string{"mgmt"})
			if got := samples[0].Nodes[0].Requests.CPU; got == nil || *got != test.want {
				t.Errorf("assigned nonterminal request = %v, want %v", got, test.want)
			}
		})
	}
	samples, results := utilizationTestRequestHistory(1)
	for _, ws := range []int{0, 2} {
		results[ws].series = nil // Inventory, rather than a heartbeat, establishes coverage.
		utilizationTestHistoryPod(results, ws, samples[0].Time, "pod", "uid", "node", "Running", 2)
		results[ws+1].series = nil
	}
	utilizationBuildRequestHistory(samples, results, []string{"mgmt"})
	got := samples[0].Nodes[0].Requests
	if got.CPU == nil || *got.CPU != 0 || got.Memory == nil || *got.Memory != 0 || got.SwiftNIC == nil || *got.SwiftNIC != 0 {
		t.Fatalf("inventoried containers with no requests must be zero: %+v", got)
	}
}

func TestUtilizationRequestHistoryHistoricalPlacement(t *testing.T) {
	samples, results := utilizationTestRequestHistory(3)
	for i := range samples {
		other := samples[i].Nodes[0]
		other.Name = "other"
		samples[i].Nodes = append(samples[i].Nodes, other)
		node, uid := "node", "old-uid"
		if i > 0 {
			node, uid = "other", "new-uid"
		}
		utilizationTestHistoryPod(results, 0, samples[i].Time, "reused-name", uid, node, "Running", float64(i+1))
	}
	// An unrelated cluster's UID and placement must not contaminate this join.
	for _, series := range slices.Clone(results[0].series) {
		series.Metric = maps.Clone(series.Metric)
		series.Metric["cluster"], series.Metric["uid"] = "customer", "conflicting-uid"
		results[0].series = append(results[0].series, series)
	}
	// Return a real matrix with multiple points per series, not just one-point fixtures.
	last := results[1].series[len(results[1].series)-1]
	last.Values = append(slices.Clone(last.Values), []any{float64(samples[1].Time.Unix()), "6"})
	results[1].series[len(results[1].series)-1] = last
	utilizationBuildRequestHistory(samples, results, []string{"mgmt"})
	for i, sample := range samples {
		for j, node := range sample.Nodes {
			want := 0.0
			if (i == 0 && j == 0) || (i > 0 && j == 1) {
				want = float64(i + 1)
			}
			if node.Requests.CPU == nil || *node.Requests.CPU != want {
				t.Errorf("minute %d node %s: request=%v want=%v warnings=%v", i, node.Name, node.Requests.CPU, want, sample.Warnings)
			}
		}
	}
}

func TestUtilizationRequestHistoryQueries(t *testing.T) {
	queries := utilizationRequestHistoryQueries([]string{`cluster.a`, `cluster"b`})
	if len(queries) != 4 {
		t.Fatalf("expected metadata and requests in both workspaces: %+v", queries)
	}
	for _, query := range queries {
		if strings.Contains(query.expression, "kube_state_metrics_list_total") && query.workspace != workspaceSvc {
			t.Error("collector self metrics route only to svc")
		}
		for _, forbidden := range []string{"__name__=~", "container_cpu_usage", "working_set", "kube_pod_owner", "resource_limits", "init_container"} {
			if strings.Contains(query.expression, forbidden) {
				t.Errorf("history query contains unnecessary/unsupported %s: %s", forbidden, query.expression)
			}
		}
		for _, required := range []string{`hostedcontrolplane=""`, "max by (", "pod, uid,", `cluster=~"cluster\\.a|cluster\"b"`} {
			if !strings.Contains(query.expression, required) {
				t.Errorf("history query missing %s: %s", required, query.expression)
			}
		}
	}
}

func TestUtilizationHistoryGridMemorySwiftAndChurn(t *testing.T) {
	results := utilizationTestHistory([]string{"mgmt"}, 3)
	for i := range results[1].series {
		s := &results[1].series[i]
		if s.Metric["__name__"] == "kube_node_status_capacity" && s.Metric["resource"] == "memory" {
			s.Values[0][1] = "200"
		}
		if s.Values[0][0] == float64(utilizationTestTime.Add(time.Minute).Unix()) {
			s.Metric["node"] = "replacement"
		}
	}
	for _, metric := range []string{"kube_node_status_capacity", "kube_node_status_allocatable"} {
		results[1].series = append(results[1].series, utilizationTestSeries(utilizationTestTime, 8, "__name__", metric, "cluster", "mgmt", "node", "node", "resource", "aro_openshift_io_swift_nic"))
	}
	end := utilizationTestTime.Add(3 * time.Minute)
	// Keep only exporter metrics in minute two and no inventory in minute three.
	results[1].series = slices.DeleteFunc(results[1].series, func(s PrometheusResult) bool {
		return s.Values[0][0] == float64(utilizationTestTime.Add(2*time.Minute).Unix())
	})
	history, _ := utilizationBuildHistory(results, utilizationTestTime, end)
	samples := utilizationRetainHistory(history, utilizationTestTime, end, nil)
	if len(samples) != 4 || len(samples[3].Nodes) != 0 || len(samples[3].Expected) != 0 {
		t.Fatalf("empty minutes or historical expected clusters lost: %+v", samples)
	}
	node := samples[0].Nodes[0]
	if node.Capacity.Memory == nil || *node.Capacity.Memory != 200 || *history[utilizationTestTime.Unix()].nodes[utilizationNodeKey{"mgmt", "node"}].Capacity.Memory != 100 {
		t.Fatal("history must use KSM memory without changing peak exporter denominator")
	}
	if node.SwiftAdvertised == nil || !*node.SwiftAdvertised || *node.Capacity.SwiftNIC != 8 || *node.Allocatable.SwiftNIC != 8 || node.Usage.SwiftNIC != nil {
		t.Fatalf("SWIFT advertisement/capacity/usage incorrect: %+v", node)
	}
	for _, node := range samples[1].Nodes {
		if node.Name == "replacement" && (node.SwiftAdvertised == nil || *node.SwiftAdvertised) {
			t.Errorf("known KSM node without SWIFT must be not advertised: %+v", node)
		}
	}
	if samples[2].Nodes[0].SwiftAdvertised != nil {
		t.Error("exporter-only node must not imply SWIFT is absent")
	}
}

func TestUtilizationRequestHistoryChunksBeforePeaksAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	history := utilizationTestHistory([]string{"mgmt"}, 122)
	base := utilizationTestQuery(history)
	queries := utilizationRequestHistoryQueries([]string{"mgmt"})
	var mu sync.Mutex
	chunks := map[int64]int{}
	query := func(ctx context.Context, ws, expression string, first, last time.Time) ([]PrometheusResult, error) {
		for _, q := range queries {
			if q.expression != expression || q.workspace != ws {
				continue
			}
			mu.Lock()
			chunks[first.Unix()]++
			mu.Unlock()
			if last.Sub(first) > 29*time.Minute {
				t.Error("request matrix exceeds 30 minute samples")
			}
			if first.After(utilizationTestTime) {
				cancel()
				return nil, ctx.Err()
			}
			var series []PrometheusResult
			if q.name == "history metadata" {
				for at := first; !at.After(last); at = at.Add(time.Minute) {
					series = append(series, utilizationTestSeries(at, 1, "__name__", "kube_state_metrics_list_total", "cluster", "mgmt"))
				}
			}
			return series, nil
		}
		for _, q := range utilizationSnapshotQueries([]string{"mgmt"}) {
			if q.expression == expression {
				t.Error("peak workload details called before history finished/cancellation")
			}
		}
		return base(ctx, ws, expression, first, last)
	}
	end := utilizationTestTime.Add(121 * time.Minute)
	report := collectUtilization(ctx, utilizationTestTime, end, end, query)
	if len(report.History) != 122 || len(report.Snapshots) == 0 {
		t.Fatalf("partial results lost: history=%d peaks=%d", len(report.History), len(report.Snapshots))
	}
	if got := report.History[0].Nodes[0].Requests.CPU; got == nil || *got != 0 {
		t.Error("successful first chunk lost after cancellation")
	}
	if report.History[30].Nodes[0].Requests.CPU != nil {
		t.Error("cancelled chunk fabricated zero requests")
	}
	warnings := strings.Join(report.Warnings, ";")
	if !strings.Contains(warnings, "2026-01-01T12:30:00Z..2026-01-01T12:59:00Z") || !strings.Contains(warnings, "context canceled") {
		t.Errorf("chunk interval/cancellation diagnostics missing: %s", warnings)
	}
	if chunks[utilizationTestTime.Unix()] != 4 {
		t.Errorf("expected four first-chunk queries, got %v", chunks)
	}
	if len(chunks) != 2 || strings.Count(warnings, "remaining collection skipped") != 1 {
		t.Errorf("cancellation must stop batches and summarize remaining range once: chunks=%v warnings=%s", chunks, warnings)
	}
	for _, sample := range report.History[60:] {
		if sample.Nodes[0].Requests.CPU != nil || len(sample.Warnings) != 1 || !strings.Contains(sample.Warnings[0], "remaining collection skipped") {
			t.Errorf("unqueried minute must retain unknown requests and concise warning: %+v", sample)
		}
	}
}
