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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/test/util/timing"
)

var utilizationTestTime = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func utilizationTestSeries(at time.Time, value float64, labels ...string) PrometheusResult {
	metric := map[string]string{}
	for i := 0; i < len(labels); i += 2 {
		metric[labels[i]] = labels[i+1]
	}
	return PrometheusResult{Metric: metric, Values: [][]any{{float64(at.Unix()), strconv.FormatFloat(value, 'g', -1, 64)}}}
}

func utilizationTestHistory(clusters []string, minutes int) []utilizationQueryResult {
	queries := utilizationHistoryQueries()
	results := make([]utilizationQueryResult, len(queries))
	for i, query := range queries {
		results[i].query = query
	}
	for minute := 0; minute < minutes; minute++ {
		at := utilizationTestTime.Add(time.Duration(minute) * time.Minute)
		for _, cluster := range clusters {
			results[0].series = append(results[0].series, utilizationTestSeries(at, 1, "cluster", cluster))
			for _, metric := range []string{"kube_node_info", "kube_node_labels", "kube_node_status_capacity", "kube_node_status_allocatable"} {
				if strings.Contains(metric, "status") {
					for _, resource := range []string{"cpu", "memory"} {
						value := 10.0
						if resource == "memory" {
							value = 100
						}
						results[1].series = append(results[1].series, utilizationTestSeries(at, value, "__name__", metric, "cluster", cluster, "node", "node", "resource", resource))
					}
				} else {
					results[1].series = append(results[1].series, utilizationTestSeries(at, 1, "__name__", metric, "cluster", cluster, "node", "node", "label_node_kubernetes_io_instance_type", "SKU", "label_kubernetes_azure_com_agentpool", "system"))
				}
			}
			results[2].series = append(results[2].series, utilizationTestSeries(at, 1, "cluster", cluster, "instance", "10.0.0.1:9100", "nodename", "node"))
			for i, value := range []float64{5, 100, 50} {
				results[3+i].series = append(results[3+i].series, utilizationTestSeries(at, value, "cluster", cluster, "instance", "10.0.0.1:9100"))
			}
		}
	}
	return results
}

func utilizationTestQuery(history []utilizationQueryResult) utilizationQueryFunc {
	return func(_ context.Context, ws, expression string, _, _ time.Time) ([]PrometheusResult, error) {
		for _, result := range history {
			if result.query.workspace == ws && result.query.expression == expression {
				return result.series, result.err
			}
		}
		return nil, nil
	}
}

func TestUtilizationHistoryAutoscalingAndPeakTies(t *testing.T) {
	results := utilizationTestHistory([]string{"svc", "mgmt"}, 3)
	// The later node is ten times larger; using current capacity would reverse
	// the CPU peak. Memory peaks independently in the second minute.
	for i := range results[1].series {
		s := &results[1].series[i]
		if s.Metric["resource"] == "cpu" && s.Values[0][0].(float64) > float64(utilizationTestTime.Unix()) {
			s.Values[0][1] = "100"
		}
	}
	for i := range results[3].series {
		s := &results[3].series[i]
		if s.Values[0][0].(float64) > float64(utilizationTestTime.Unix()) {
			s.Values[0][1] = "40"
		}
	}
	for i := range results[5].series {
		s := &results[5].series[i]
		if s.Values[0][0].(float64) > float64(utilizationTestTime.Unix()) {
			s.Values[0][1] = "20"
		}
	}
	// Duplicate HA series and a customer node must not inflate utilization.
	for i := range results {
		results[i].series = append(results[i].series, results[i].series...)
	}
	results[3].series = append(results[3].series, utilizationTestSeries(utilizationTestTime, 999, "cluster", "customer", "instance", "node"))
	end := utilizationTestTime.Add(2 * time.Minute)
	report := collectUtilization(context.Background(), utilizationTestTime, end, end, utilizationTestQuery(results))
	if len(report.Snapshots) != 2 {
		t.Fatalf("expected two deduplicated peak minutes, got %+v", report)
	}
	for i, snapshot := range report.Snapshots {
		if !snapshot.Time.Equal(utilizationTestTime.Add(time.Duration(i) * time.Minute)) {
			t.Errorf("earliest tied peak not selected: %v", snapshot.Time)
		}
		if len(snapshot.Reasons) != 3 {
			t.Errorf("expected cluster and overall reasons: %v", snapshot.Reasons)
		}
		if len(snapshot.Nodes) != 2 {
			t.Fatalf("expected two underlay nodes without HA duplication: %+v", snapshot.Nodes)
		}
		wantCapacity := 10.0
		if i == 1 {
			wantCapacity = 100
		}
		if got := *snapshot.Nodes[0].Capacity.CPU; got != wantCapacity {
			t.Errorf("historical capacity = %v, want %v", got, wantCapacity)
		}
	}
}

func TestUtilizationMissingClusterAndIncompleteNodes(t *testing.T) {
	results := utilizationTestHistory([]string{"svc", "mgmt", "missing"}, 2)
	for i := 1; i < len(results); i++ {
		results[i].series = slices.DeleteFunc(results[i].series, func(s PrometheusResult) bool { return s.Metric["cluster"] == "missing" })
	}
	// A known mgmt node without CPU cannot enter the CPU argmax, but it must
	// still appear in a snapshot selected for another cluster/resource.
	results[3].series = slices.DeleteFunc(results[3].series, func(s PrometheusResult) bool { return s.Metric["cluster"] == "mgmt" })
	end := utilizationTestTime.Add(time.Minute)
	report := collectUtilization(context.Background(), utilizationTestTime, end, end, utilizationTestQuery(results))
	if len(report.Snapshots) != 1 {
		t.Fatalf("expected one snapshot, got %+v", report)
	}
	snapshot := report.Snapshots[0]
	for _, reason := range snapshot.Reasons {
		if strings.HasPrefix(reason, "overall") || reason == "mgmt CPU peak" {
			t.Errorf("selected incomplete peak: %s", reason)
		}
	}
	if len(snapshot.Nodes) != 2 || snapshot.Nodes[0].Cluster != "mgmt" || snapshot.Nodes[0].Usage.CPU != nil {
		t.Errorf("missing CPU must remain nil on historical node: %+v", snapshot.Nodes)
	}
	if !strings.Contains(strings.Join(report.Warnings, ";"), "incomplete history") || !strings.Contains(strings.Join(snapshot.Warnings, ";"), "missing: node inventory unavailable") {
		t.Errorf("missing data warnings not surfaced: %+v", report)
	}
}

func TestUtilizationWeightedOverallAndNoSnapshotCap(t *testing.T) {
	clusters := []string{"a", "b", "c", "d", "e"}
	results := utilizationTestHistory(clusters, len(clusters))
	for i := range results[3].series {
		s := &results[3].series[i]
		ts, _, _ := utilizationValue(s.Values[0])
		minute := int((ts - utilizationTestTime.Unix()) / 60)
		if s.Metric["cluster"] == clusters[minute] {
			s.Values[0][1] = "9"
		}
	}
	end := utilizationTestTime.Add(4 * time.Minute)
	report := collectUtilization(context.Background(), utilizationTestTime, end, end, utilizationTestQuery(results))
	if len(report.Snapshots) != 5 {
		t.Fatalf("all distinct cluster peaks must survive without a cap: %+v", report)
	}
	// Make a larger cluster dominate the overall ratio at a later minute.
	results = utilizationTestHistory([]string{"a", "b"}, 2)
	for i := range results[1].series {
		s := &results[1].series[i]
		if s.Metric["cluster"] == "b" && s.Metric["resource"] == "cpu" {
			s.Values[0][1] = "100"
		}
	}
	for i := range results[3].series {
		s := &results[3].series[i]
		later := s.Values[0][0].(float64) > float64(utilizationTestTime.Unix())
		v := "9"
		if later {
			v = "1"
		}
		if s.Metric["cluster"] == "b" {
			v = "10"
			if later {
				v = "70"
			}
		}
		s.Values[0][1] = v
	}
	end = utilizationTestTime.Add(time.Minute)
	report = collectUtilization(context.Background(), utilizationTestTime, end, end, utilizationTestQuery(results))
	if len(report.Snapshots) != 2 || !slices.Contains(report.Snapshots[1].Reasons, "overall CPU peak") {
		t.Fatalf("overall CPU must use capacity weighting, not mean percentages: %+v", report)
	}
}

func utilizationTestWorkloads() []utilizationQueryResult {
	queries := utilizationSnapshotQueries([]string{"mgmt", "svc"})
	results := make([]utilizationQueryResult, len(queries))
	for i, q := range queries {
		results[i].query = q
	}
	for i, pod := range []string{"api-rs-1", "api-rs-2"} {
		uid := fmt.Sprintf("11111111-2222-3333-4444-%012d", i)
		base := []string{"cluster", "mgmt", "namespace", "ocm-tenant", "pod", pod, "uid", uid}
		add := func(index int, value float64, labels ...string) {
			results[index].series = append(results[index].series, utilizationTestSeries(utilizationTestTime, value, append(slices.Clone(base), labels...)...))
		}
		add(5, 1, "__name__", "kube_pod_info", "node", "node", "container", "kube-state-metrics")
		add(5, 1, "__name__", "kube_pod_status_phase", "phase", "Running", "container", "kube-state-metrics")
		add(5, 1, "__name__", "kube_pod_owner", "owner_kind", "ReplicaSet", "owner_name", "api-unchanged-hash", "owner_is_controller", "true", "container", "kube-state-metrics")
		for _, container := range []string{"api", "sidecar"} {
			add(5, 1, "__name__", "kube_pod_container_info", "container", container)
			add(0, 2, "container", container, "instance", "node", "id", "/kubepods/burstable/pod"+uid+"/"+container)
			add(1, 10, "container", container, "instance", "node", "id", "/kubepods/burstable/pod"+uid+"/"+container)
			add(6, 3, "container", container, "resource", "cpu")
			add(6, 20, "container", container, "resource", "memory")
			add(7, 5, "container", container, "resource", "cpu")
			add(7, 30, "container", container, "resource", "memory")
		}
	}
	// Owner is intentionally in the other workspace, and the same RS name in
	// another cluster must not contaminate this join.
	for _, cluster := range []string{"mgmt", "svc"} {
		name := "api-real-name"
		if cluster == "svc" {
			name = "wrong"
		}
		results[2].series = append(results[2].series, utilizationTestSeries(utilizationTestTime, 1, "__name__", "kube_replicaset_owner", "cluster", cluster, "namespace", "ocm-tenant", "replicaset", "api-unchanged-hash", "owner_kind", "Deployment", "owner_name", name))
	}
	return results
}

func TestUtilizationWorkloadsCrossWorkspaceOwnersAndContainers(t *testing.T) {
	results := utilizationTestWorkloads()
	for i := range results {
		results[i].series = append(results[i].series, results[i].series...)
	}
	var warnings []string
	rows := utilizationBuildWorkloads(results, []string{"mgmt"}, utilizationTestTime, &warnings)
	if len(rows) != 1 {
		t.Fatalf("expected one aggregate workload: %+v", rows)
	}
	row := rows[0]
	if row.Kind != "Deployment" || row.Name != "api-real-name" || row.Component != "Deployment/api-real-name" || row.Pods != 2 || len(row.Containers) != 2 {
		t.Fatalf("incorrect owner, identity, or replica aggregation: %+v", row)
	}
	if *row.Usage.CPU != 8 || *row.Usage.Memory != 40 || *row.Requests.CPU != 12 || *row.Limits.Memory != 120 {
		t.Errorf("multi-container sums inflated or incomplete: %+v", row)
	}
	if *row.Containers[0].Usage.CPU != 4 || *row.Containers[1].Usage.CPU != 4 {
		t.Errorf("per-container replica sums incorrect: %+v", row.Containers)
	}
	data, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "api-rs-") {
		t.Errorf("pod identities leaked into aggregate output: %s", data)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings for complete workload: %v", warnings)
	}
}

func TestUtilizationPendingTerminalUnknownAndOwnerChains(t *testing.T) {
	results := utilizationTestWorkloads()
	results[0].series, results[1].series = nil, nil
	results[5].series, results[6].series, results[7].series = nil, nil, nil
	for _, spec := range []struct {
		pod, phase, node, owner string
		placement               bool
	}{
		{"pending", "Pending", "", "Job", true},
		{"scheduled-pending", "Pending", "node", "StatefulSet", true},
		{"unknown", "Running", "", "DaemonSet", false},
		{"done", "Succeeded", "node", "Job", true},
		{"failed", "Failed", "node", "Job", true},
	} {
		base := []string{"cluster", "mgmt", "namespace", "any-namespace", "pod", spec.pod}
		add := func(metric string, labels ...string) {
			labels = append([]string{"__name__", metric}, labels...)
			results[5].series = append(results[5].series, utilizationTestSeries(utilizationTestTime, 1, append(slices.Clone(base), labels...)...))
		}
		add("kube_pod_status_phase", "phase", spec.phase)
		add("kube_pod_owner", "owner_kind", spec.owner, "owner_name", "true-name")
		add("kube_pod_container_info", "container", "main")
		if spec.placement {
			add("kube_pod_info", "node", spec.node)
		}
	}
	results[2].series = append(results[2].series, utilizationTestSeries(utilizationTestTime, 1, "__name__", "kube_job_owner", "cluster", "mgmt", "namespace", "any-namespace", "job_name", "true-name", "owner_kind", "CronJob", "owner_name", "cron-real"))
	var warnings []string
	rows := utilizationBuildWorkloads(results, []string{"mgmt"}, utilizationTestTime, &warnings)
	if len(rows) != 3 {
		t.Fatalf("terminal pods must be excluded: %+v", rows)
	}
	for _, row := range rows {
		if row.Pods != 1 || row.Usage.CPU != nil || row.Requests.CPU == nil || *row.Requests.CPU != 0 || row.Containers[0].UnlimitedCPU == nil || *row.Containers[0].UnlimitedCPU != 1 {
			t.Errorf("absent usage/requests/limits semantics wrong: %+v", row)
		}
		switch row.Kind {
		case "CronJob":
			if !row.Unscheduled || row.Node != "" || row.PendingPods != 1 || row.Name != "cron-real" {
				t.Errorf("pending demand or owner chain incorrect: %+v", row)
			}
		case "StatefulSet":
			if row.Unscheduled || row.Node != "node" || row.PendingPods != 1 {
				t.Errorf("scheduled pending placement incorrect: %+v", row)
			}
		case "DaemonSet":
			if row.Unscheduled || row.Node != "unknown" {
				t.Errorf("unknown placement falsely classified unscheduled: %+v", row)
			}
		default:
			t.Errorf("unexpected owner: %+v", row)
		}
	}
}

func TestUtilizationMissingCoverageAndQueryFailures(t *testing.T) {
	results := utilizationTestWorkloads()
	results[0].series = results[0].series[:3] // one replica's sidecar CPU missing
	results[6].err = errors.New("requests denied")
	results[7].err = errors.New("limits denied")
	var warnings []string
	rows := utilizationBuildWorkloads(results, []string{"mgmt"}, utilizationTestTime, &warnings)
	if len(rows) != 1 {
		t.Fatalf("expected partial workload, got %+v", rows)
	}
	row := rows[0]
	if row.Usage.CPU != nil || row.Requests.CPU != nil || row.Limits.Memory != nil || *row.Usage.Memory != 40 {
		t.Errorf("partial pointers must not produce complete totals: %+v", row)
	}
	if row.Containers[0].UnlimitedCPU != nil || row.Containers[1].UnlimitedMemory != nil {
		t.Errorf("failed limit query is not unlimited: %+v", row.Containers)
	}
	if !strings.Contains(strings.Join(warnings, ";"), "partial workload") {
		t.Errorf("expected partial coverage warning: %v", warnings)
	}
}

func TestUtilizationReplicaWithoutContainerInventory(t *testing.T) {
	results := utilizationTestWorkloads()
	for i := range results {
		results[i].series = slices.DeleteFunc(results[i].series, func(s PrometheusResult) bool {
			return s.Metric["pod"] == "api-rs-2" && s.Metric["container"] != "" && s.Metric["container"] != "kube-state-metrics"
		})
	}
	var warnings []string
	rows := utilizationBuildWorkloads(results, []string{"mgmt"}, utilizationTestTime, &warnings)
	if len(rows) != 1 || rows[0].Pods != 2 || rows[0].Usage.CPU != nil || rows[0].Requests.Memory != nil || rows[0].Limits.CPU != nil {
		t.Fatalf("missing replica inventory cannot produce complete workload totals: %+v", rows)
	}
	for _, container := range rows[0].Containers {
		if container.Usage.CPU != nil || container.Requests.Memory != nil {
			t.Errorf("incomplete replica inventory cannot produce complete container totals: %+v", container)
		}
	}
	if !strings.Contains(strings.Join(warnings, ";"), "container inventory unavailable") {
		t.Errorf("missing inventory warning: %v", warnings)
	}
}

func TestUtilizationUnspecifiedRequestsAndUnlimitedLimits(t *testing.T) {
	results := utilizationTestWorkloads()
	results[6].series = nil
	for i := range results[7].series {
		results[7].series[i].Values[0][1] = "0"
	}
	var warnings []string
	rows := utilizationBuildWorkloads(results, []string{"mgmt"}, utilizationTestTime, &warnings)
	if len(rows) != 1 || rows[0].Requests.CPU == nil || *rows[0].Requests.CPU != 0 || rows[0].Limits.CPU == nil || *rows[0].Limits.CPU != 0 || rows[0].Limits.Memory == nil || *rows[0].Limits.Memory != 0 {
		t.Fatalf("unspecified requests are zero; unlimited limits contribute zero finite sum: %+v", rows)
	}
	for _, container := range rows[0].Containers {
		if container.UnlimitedCPU == nil || *container.UnlimitedCPU != 2 || container.UnlimitedMemory == nil || *container.UnlimitedMemory != 2 {
			t.Errorf("unlimited counts should count replicas once: %+v", container)
		}
	}
}

func TestUtilizationNodeAdditionRemovalAndUnknownLabels(t *testing.T) {
	results := utilizationTestHistory([]string{"svc"}, 3)
	// Replace the node at minute one, then add a second at minute two. No
	// interpolation may retain the old node or move the new capacity earlier.
	for i := 1; i < len(results); i++ {
		var extra []PrometheusResult
		for j := range results[i].series {
			s := &results[i].series[j]
			ts, _, _ := utilizationValue(s.Values[0])
			if ts == utilizationTestTime.Unix() {
				continue
			}
			delete(s.Metric, "label_node_kubernetes_io_instance_type")
			delete(s.Metric, "label_kubernetes_azure_com_agentpool")
			if s.Metric["node"] != "" {
				s.Metric["node"] = "replacement"
			}
			if s.Metric["nodename"] != "" {
				s.Metric["nodename"] = "replacement"
			}
			if ts == utilizationTestTime.Add(2*time.Minute).Unix() {
				labels := []string{}
				for k, v := range s.Metric {
					labels = append(labels, k, v)
				}
				_, v, _ := utilizationValue(s.Values[0])
				clone := utilizationTestSeries(time.Unix(ts, 0), v, labels...)
				if clone.Metric["node"] != "" {
					clone.Metric["node"] = "added"
				}
				if clone.Metric["nodename"] != "" {
					clone.Metric["nodename"] = "added"
				}
				if clone.Metric["instance"] != "" {
					clone.Metric["instance"] = "10.0.0.2:9100"
				}
				extra = append(extra, clone)
			}
		}
		results[i].series = append(results[i].series, extra...)
	}
	history, clusters := utilizationBuildHistory(results, utilizationTestTime, utilizationTestTime.Add(2*time.Minute))
	if len(clusters) != 1 {
		t.Fatalf("incorrect underlay discovery: %v", clusters)
	}
	for minute, count := range []int{1, 1, 2} {
		nodes := history[utilizationTestTime.Add(time.Duration(minute)*time.Minute).Unix()].nodes
		if len(nodes) != count {
			t.Errorf("minute %d: expected %d nodes, got %v", minute, count, nodes)
		}
		if minute > 0 {
			if nodes[utilizationNodeKey{"svc", "node"}] != nil {
				t.Errorf("deleted node carried into minute %d", minute)
			}
			node := nodes[utilizationNodeKey{"svc", "replacement"}]
			if node == nil || node.Pool != "unknown" || node.SKU != "unknown" {
				t.Errorf("missing labels must be explicit unknown: %+v", node)
			}
		}
	}
}

func TestUtilizationFailedAndCancelledHistory(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelled {
				cancel()
			}
			query := func(context.Context, string, string, time.Time, time.Time) ([]PrometheusResult, error) {
				if cancelled {
					t.Error("must not call query after cancellation")
				}
				return nil, errors.New("history forbidden")
			}
			report := collectUtilization(ctx, utilizationTestTime, utilizationTestTime, utilizationTestTime, query)
			if len(report.Snapshots) != 0 || len(report.Warnings) < 6 {
				t.Errorf("failed history should return warnings and no fabricated snapshots: %+v", report)
			}
		})
	}
}

func TestUtilizationCancellationPreservesSelectionAndBoundsConcurrency(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	history := utilizationTestHistory([]string{"svc"}, 2)
	history[5].series[1].Values[0][1] = "10"
	base := utilizationTestQuery(history)
	var active, maximum atomic.Int32
	var once sync.Once
	query := func(ctx context.Context, ws, expression string, start, end time.Time) ([]PrometheusResult, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); current > old && !maximum.CompareAndSwap(old, current); old = maximum.Load() {
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > utilizationTimeout {
			t.Errorf("collector timeout must be ten minutes")
		}
		for _, result := range history {
			if expression == result.query.expression {
				time.Sleep(time.Millisecond)
				return base(ctx, ws, expression, start, end)
			}
		}
		once.Do(cancel)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	end := utilizationTestTime.Add(time.Minute)
	report := collectUtilization(ctx, utilizationTestTime, end, end, query)
	if maximum.Load() != 2 {
		t.Errorf("expected bounded two-worker batch, maximum = %d", maximum.Load())
	}
	if len(report.Snapshots) != 2 {
		t.Fatalf("cancellation discarded selected snapshots: %+v", report)
	}
	for _, snapshot := range report.Snapshots {
		if len(snapshot.Nodes) == 0 || len(snapshot.Warnings) == 0 {
			t.Errorf("snapshot must retain nodes and cancellation warning: %+v", snapshot)
		}
	}
	if !strings.Contains(strings.Join(report.Warnings, ";"), "context canceled") {
		t.Errorf("cancellation warning missing: %+v", report)
	}
}

func TestUtilizationFixedWindowAndInvalidSamples(t *testing.T) {
	now := utilizationTestTime.Add(95 * time.Second)
	var mu sync.Mutex
	var calls int
	query := func(_ context.Context, _, _ string, start, end time.Time) ([]PrometheusResult, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		if !start.Equal(utilizationTestTime.Add(time.Minute)) || !end.Equal(start) || start.Location() != time.UTC {
			t.Errorf("query outside fixed UTC minute bounds: %v..%v", start, end)
		}
		return nil, nil
	}
	report := collectUtilization(context.Background(), utilizationTestTime.Add(10*time.Second), now.Add(time.Hour), now, query)
	if !report.End.Equal(now) || calls != 6 {
		t.Errorf("end must freeze before queries: %+v calls=%d", report, calls)
	}
	for _, sample := range [][]any{nil, {float64(0)}, {float64(0), "NaN"}, {float64(0), "+Inf"}, {float64(0), "garbage"}, {float64(0), "-1"}, {1.5, "1"}} {
		if _, _, ok := utilizationValue(sample); ok {
			t.Errorf("accepted invalid sample: %v", sample)
		}
	}
}

type utilizationTestCredential struct{}

func (utilizationTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test"}, nil
}

func TestUtilizationHTTPIntegrationAndUnavailableWorkspace(t *testing.T) {
	history := utilizationTestHistory([]string{"svc"}, 1)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		// Reproduce the managed endpoint's restriction observed in PR 7021:
		// local mocks must not silently accept unsupported metric-name regexes.
		if strings.Contains(r.URL.Query().Get("query"), "__name__=~") {
			http.Error(w, `{"status":"error","errorType":"Not Implemented","error":"Not implemented: Metric name only support equality(=) filter."}`, http.StatusNotImplemented)
			return
		}
		if r.URL.Path != "/api/v1/query_range" || r.Header.Get("Authorization") != "Bearer test" || r.URL.Query().Get("step") != "60s" {
			t.Errorf("invalid query_range request: %s", r.URL)
		}
		var series []PrometheusResult
		for _, result := range history {
			if result.query.expression == r.URL.Query().Get("query") {
				series = result.series
			}
		}
		if strings.Contains(r.URL.Query().Get("query"), "container_memory_working_set_bytes") {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		if err := json.NewEncoder(w).Encode(PrometheusResponse{Status: "success", Data: PrometheusData{ResultType: "matrix", Result: series}}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	o := Options{completedOptions: &completedOptions{TimeWindow: timing.TimeWindow{Start: utilizationTestTime, End: utilizationTestTime}, cred: utilizationTestCredential{}}}
	report := o.collectUtilization(context.Background(), map[string]*workspaceData{workspaceSvc: {PromEndpoint: server.URL}, workspaceHcp: {}})
	if len(report.Snapshots) != 1 {
		t.Fatalf("expected HTTP-selected snapshot: %+v", report)
	}
	warnings := strings.Join(report.Snapshots[0].Warnings, ";")
	if !strings.Contains(warnings, "403") || !strings.Contains(warnings, "hcp metadata query unavailable") {
		t.Errorf("query/workspace failures must not discard snapshot: %v", warnings)
	}
	if got := requests.Load(); got != 13 {
		t.Errorf("expected bounded 6 node history + 2 svc request history + 5 svc detail queries, got %d", got)
	}
	report = o.collectUtilization(context.Background(), nil)
	if len(report.Snapshots) != 0 || !strings.Contains(strings.Join(report.Warnings, ";"), "endpoint unavailable") {
		t.Errorf("missing svc endpoint must be reported: %+v", report)
	}
}

func TestUtilizationQueriesScopeAndDedup(t *testing.T) {
	for _, query := range append(utilizationHistoryQueries(), utilizationSnapshotQueries([]string{`cluster.a`, `cluster"b`})...) {
		if !strings.Contains(query.expression, "max by (") {
			t.Errorf("query missing HA dedup: %s", query.expression)
		}
		if strings.Contains(query.expression, "__name__=~") || strings.Contains(query.expression, "__name__!=") || strings.Contains(query.expression, "__name__!~") {
			t.Errorf("Azure requires exact metric-name selectors: %s", query.expression)
		}
		if strings.Contains(strings.ReplaceAll(query.expression, "or on (__name__)", "or"), " on (") && !strings.Contains(query.expression, "on (cluster,") {
			t.Errorf("join is not cluster-qualified: %s", query.expression)
		}
		if strings.Contains(query.expression, "kube_") && !strings.Contains(query.expression, `hostedcontrolplane=""`) {
			t.Errorf("customer KSM must be excluded: %s", query.expression)
		}
		if strings.Contains(query.expression, "container_cpu") && !strings.Contains(query.expression, "[2m]") {
			t.Error("container CPU must use rate2m")
		}
		if strings.Contains(query.expression, "working_set") && !strings.Contains(query.expression, "[1m]") {
			t.Error("memory must use avg1m")
		}
		if strings.Contains(query.expression, "aks-system") || strings.Contains(query.expression, "namespace=~") {
			t.Errorf("pool/namespace filtering would omit underlay workloads: %s", query.expression)
		}
	}
	if utilizationTimeout != 10*time.Minute {
		t.Fatalf("collector timeout is %v, not ten minutes", utilizationTimeout)
	}
}
