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
	"testing"
)

func TestUtilizationPendingSpecWithoutRuntimeStatus(t *testing.T) {
	for _, failedLimits := range []bool{false, true} {
		results := utilizationTestWorkloads()
		results[0].series, results[1].series = nil, nil
		results[5].series = slices.DeleteFunc(results[5].series, func(s PrometheusResult) bool { return s.Metric["__name__"] == "kube_pod_container_info" })
		for i := range results[5].series {
			m := results[5].series[i].Metric
			if m["__name__"] == "kube_pod_info" {
				m["node"] = ""
			}
			if m["__name__"] == "kube_pod_status_phase" {
				m["phase"] = "Pending"
			}
		}
		if failedLimits {
			results[7].err = errors.New("limits unavailable")
		}
		var warnings []string
		rows := utilizationBuildWorkloads(results, []string{"mgmt"}, utilizationTestTime, &warnings)
		if len(rows) != 1 {
			t.Fatalf("expected pending aggregate: %+v", rows)
		}
		row := rows[0]
		if !row.Unscheduled || row.Node != "" || row.PendingPods != 2 || row.Requests.CPU == nil || *row.Requests.CPU != 12 || row.Requests.Memory == nil || *row.Requests.Memory != 80 || row.Usage.CPU != nil {
			t.Fatalf("pending spec demand lost or placement guessed: %+v", row)
		}
		if len(row.Containers) != 2 {
			t.Fatalf("exporter target container must not become workload container: %+v", row.Containers)
		}
		for _, c := range row.Containers {
			if c.Name == "kube-state-metrics" {
				t.Error("exporter container leaked into workload")
			}
			if failedLimits {
				if c.UnlimitedCPU != nil || c.Limits.CPU != nil {
					t.Errorf("failed query must leave limits and counts unknown: %+v", c)
				}
			} else if c.Limits.CPU == nil || *c.Limits.CPU != 10 || c.UnlimitedCPU == nil || *c.UnlimitedCPU != 0 {
				t.Errorf("finite pending limits lost: %+v", c)
			}
		}
		if !strings.Contains(strings.Join(warnings, ";"), "retaining observed spec-backed demand") {
			t.Errorf("partial inventory caveat absent: %v", warnings)
		}
	}
}

func TestUtilizationReusedPodUsageIncarnation(t *testing.T) {
	for _, test := range []struct {
		name, node, id string
		keep           bool
	}{
		{"old node", "old-node", "/kubepods/podaaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/old", true},
		{"same node old UID", "node", "/kubepods/podaaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/old", true},
		{"unknown incarnation", "node", "/unrecognized/old", true},
		{"two runtimes for same UID", "node", "/kubepods/pod11111111-2222-3333-4444-000000000000/other-runtime", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			results := utilizationTestWorkloads()
			for i := range results {
				results[i].series = slices.DeleteFunc(results[i].series, func(s PrometheusResult) bool { return s.Metric["pod"] == "api-rs-2" })
			}
			for i := range results[5].series {
				m := results[5].series[i].Metric
				if m["__name__"] == "kube_pod_owner" {
					m["owner_kind"], m["owner_name"] = "StatefulSet", "api"
				}
			}
			for _, index := range []int{0, 1} {
				old := results[index].series[0]
				old.Metric = maps.Clone(old.Metric)
				old.Metric["node"], old.Metric["instance"], old.Metric["id"] = test.node, test.node, test.id
				old.Values = [][]any{{float64(utilizationTestTime.Unix()), "999"}}
				results[index].series = append(results[index].series, old)
			}
			var warnings []string
			rows := utilizationBuildWorkloads(results, []string{"mgmt"}, utilizationTestTime, &warnings)
			if len(rows) != 1 || rows[0].Node != "node" {
				t.Fatalf("unexpected placement: %+v", rows)
			}
			if test.keep {
				if rows[0].Usage.CPU == nil || *rows[0].Usage.CPU != 4 || rows[0].Usage.Memory == nil || *rows[0].Usage.Memory != 20 {
					t.Errorf("stale high usage attributed to replacement: %+v", rows[0])
				}
			} else if rows[0].Usage.CPU != nil || rows[0].Usage.Memory != nil {
				t.Errorf("ambiguous runtimes must be unknown: %+v", rows[0])
			}
			if !strings.Contains(strings.Join(warnings, ";"), "usage excluded") {
				t.Errorf("excluded usage must warn: %v", warnings)
			}
		})
	}
}

func TestUtilizationSystemdCgroupAndAmbiguousKSM(t *testing.T) {
	results := utilizationTestWorkloads()
	for _, index := range []int{0, 1} {
		for i := range results[index].series {
			m := results[index].series[i].Metric
			m["id"] = "/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod" + strings.ReplaceAll(m["uid"], "-", "_") + ".slice/cri-containerd-runtime.scope"
		}
	}
	var warnings []string
	rows := utilizationBuildWorkloads(results, []string{"mgmt"}, utilizationTestTime, &warnings)
	if len(rows) != 1 || rows[0].Usage.CPU == nil || *rows[0].Usage.CPU != 8 {
		t.Fatalf("systemd pod UID must match: %+v warnings=%v", rows, warnings)
	}
	old := results[5].series[0]
	old.Metric = maps.Clone(old.Metric)
	old.Metric["uid"] = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	results[5].series = append(results[5].series, old)
	warnings = nil
	rows = utilizationBuildWorkloads(results, []string{"mgmt"}, utilizationTestTime, &warnings)
	if len(rows) != 1 || rows[0].Pods != 1 || *rows[0].Usage.CPU != 4 {
		t.Fatalf("ambiguous KSM incarnation must be excluded: %+v", rows)
	}
	if !strings.Contains(strings.Join(warnings, ";"), "conflicting pod incarnations") {
		t.Errorf("missing incarnation warning: %v", warnings)
	}
}

func TestUtilizationFiniteLimitSumWithUnlimitedReplica(t *testing.T) {
	results := utilizationTestWorkloads()
	results[7].series = slices.DeleteFunc(results[7].series, func(s PrometheusResult) bool { return s.Metric["pod"] == "api-rs-2" })
	var warnings []string
	rows := utilizationBuildWorkloads(results, []string{"mgmt"}, utilizationTestTime, &warnings)
	if len(rows) != 1 || rows[0].Limits.CPU == nil || *rows[0].Limits.CPU != 10 || rows[0].Limits.Memory == nil || *rows[0].Limits.Memory != 60 {
		t.Fatalf("finite limits must sum without unlimited poisoning: %+v", rows)
	}
	for _, c := range rows[0].Containers {
		if c.UnlimitedCPU == nil || *c.UnlimitedCPU != 1 || c.UnlimitedMemory == nil || *c.UnlimitedMemory != 1 {
			t.Errorf("one unlimited replica expected: %+v", c)
		}
	}
}

func TestUtilizationRuntimeIDDisambiguatesRestart(t *testing.T) {
	results := utilizationTestWorkloads()
	for i := range results[5].series {
		m := results[5].series[i].Metric
		if m["__name__"] == "kube_pod_container_info" {
			m["container_id"] = "containerd://" + m["container"]
		}
	}
	for _, index := range []int{0, 1} {
		old := results[index].series[0]
		old.Metric = maps.Clone(old.Metric)
		old.Metric["id"] += "-previous-runtime"
		old.Values = [][]any{{float64(utilizationTestTime.Unix()), "999"}}
		results[index].series = append(results[index].series, old)
		results[index].series = append(results[index].series, results[index].series...)
	}
	var warnings []string
	rows := utilizationBuildWorkloads(results, []string{"mgmt"}, utilizationTestTime, &warnings)
	if len(rows) != 1 || rows[0].Usage.CPU == nil || *rows[0].Usage.CPU != 8 || *rows[0].Usage.Memory != 40 {
		t.Fatalf("runtime ID must reject restarted container and dedup HA: %+v", rows)
	}
	if !strings.Contains(strings.Join(warnings, ";"), "unmatched") {
		t.Errorf("rejected runtime must warn: %v", warnings)
	}
}

func TestUtilizationDemandMissingQueryDoesNotInventZero(t *testing.T) {
	results := utilizationTestWorkloads()
	results[0].series, results[1].series = nil, nil
	results[5].series = slices.DeleteFunc(results[5].series, func(s PrometheusResult) bool { return s.Metric["__name__"] == "kube_pod_container_info" })
	for i := range results[5].series {
		m := results[5].series[i].Metric
		if m["__name__"] == "kube_pod_info" {
			m["node"] = ""
		}
		if m["__name__"] == "kube_pod_status_phase" {
			m["phase"] = "Pending"
		}
	}
	results[6].err = errors.New("requests unavailable")
	var warnings []string
	rows := utilizationBuildWorkloads(results, []string{"mgmt"}, utilizationTestTime, &warnings)
	if len(rows) != 1 || rows[0].Requests.CPU != nil || rows[0].Requests.Memory != nil || rows[0].Limits.CPU == nil || *rows[0].Limits.CPU != 20 {
		t.Fatalf("known limits must survive, failed requests must not become zero: %+v", rows)
	}
}

func TestUtilizationMemoryDenominatorAndPhysicalCPU(t *testing.T) {
	results := utilizationTestHistory([]string{"svc"}, 1)
	for i := range results[1].series {
		s := &results[1].series[i]
		if s.Metric["__name__"] == "kube_node_status_capacity" && s.Metric["resource"] == "memory" {
			s.Values[0][1] = "200"
		}
	}
	report := collectUtilization(context.Background(), utilizationTestTime, utilizationTestTime, utilizationTestTime, utilizationTestQuery(results))
	if len(report.Snapshots) != 1 {
		t.Fatalf("expected peak: %+v", report)
	}
	node := report.Snapshots[0].Nodes[0]
	if node.Capacity.Memory == nil || *node.Capacity.Memory != 100 || *node.Usage.Memory / *node.Capacity.Memory != 0.5 {
		t.Errorf("snapshot denominator differs from peak denominator: %+v", node)
	}
	results[1].series = slices.DeleteFunc(results[1].series, func(s PrometheusResult) bool {
		return s.Metric["__name__"] == "kube_node_status_capacity" && s.Metric["resource"] == "memory"
	})
	report = collectUtilization(context.Background(), utilizationTestTime, utilizationTestTime, utilizationTestTime, utilizationTestQuery(results))
	for _, reason := range report.Snapshots[0].Reasons {
		if strings.Contains(reason, "memory") {
			t.Errorf("memory peak must require KSM coverage: %s", reason)
		}
	}
	if !strings.Contains(utilizationHistoryQueries()[3].expression, `mode!~"idle|guest|guest_nice"`) {
		t.Error("physical CPU must exclude idle and duplicate guest modes")
	}
	for _, query := range utilizationSnapshotQueries([]string{"svc"}) {
		if query.name == "cpu" || query.name == "memory" {
			if !strings.Contains(query.expression, "container, node, instance, id") {
				t.Errorf("usage query drops incarnation: %s", query.expression)
			}
		} else if !strings.Contains(query.expression, "pod, uid,") {
			t.Errorf("KSM query drops UID: %s", query.expression)
		}
	}
}
