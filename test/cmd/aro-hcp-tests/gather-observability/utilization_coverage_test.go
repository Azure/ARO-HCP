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
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	promutil "github.com/Azure/ARO-HCP/test/util/prometheus"
)

func TestUtilizationCoverageGaps(t *testing.T) {
	interval := func(first, last int, status utilizationCoverageInterval) utilizationCoverageInterval {
		status.Start = utilizationTestTime.Add(time.Duration(first) * time.Minute)
		status.End = utilizationTestTime.Add(time.Duration(last) * time.Minute)
		return status
	}
	valid := utilizationCoverageInterval{Eligible: true, Nodes: 1}
	missingUsage := utilizationCoverageInterval{Nodes: 1, MissingUsage: 1}
	missingInventory := utilizationCoverageInterval{Nodes: 1, MissingInventory: 1}
	missingCapacity := utilizationCoverageInterval{Nodes: 1, MissingCapacity: 1}
	missingCluster := utilizationCoverageInterval{Nodes: 1, MissingClusters: 1}
	noNodes := utilizationCoverageInterval{MissingClusters: 1}
	complete := []utilizationCoverageInterval{interval(0, 2, valid)}
	for _, test := range []struct {
		name                string
		drop                func(string, map[string]string, int) bool
		cpu, memory         []utilizationCoverageInterval
		cpuPeak, memoryPeak int
	}{
		{
			name: "complete compresses and ties select earliest",
			cpu:  complete, memory: complete,
		},
		{
			name: "front missing usage",
			drop: func(query string, _ map[string]string, minute int) bool { return query == "cpu" && minute == 0 },
			cpu:  []utilizationCoverageInterval{interval(0, 0, missingUsage), interval(1, 2, valid)}, memory: complete,
			cpuPeak: 1,
		},
		{
			name: "back missing usage",
			drop: func(query string, _ map[string]string, minute int) bool { return query == "cpu" && minute == 2 },
			cpu:  []utilizationCoverageInterval{interval(0, 1, valid), interval(2, 2, missingUsage)}, memory: complete,
		},
		{
			name: "interior gap",
			drop: func(query string, _ map[string]string, minute int) bool { return query == "cpu" && minute == 1 },
			cpu:  []utilizationCoverageInterval{interval(0, 0, valid), interval(1, 1, missingUsage), interval(2, 2, valid)}, memory: complete,
		},
		{
			name: "all usage missing no peaks",
			drop: func(query string, _ map[string]string, _ int) bool { return query == "cpu" || query == "available" },
			cpu:  []utilizationCoverageInterval{interval(0, 2, missingUsage)}, memory: []utilizationCoverageInterval{interval(0, 2, missingUsage)},
			cpuPeak: -1, memoryPeak: -1,
		},
		{
			name: "missing node info with usage",
			drop: func(_ string, labels map[string]string, _ int) bool { return labels["__name__"] == "kube_node_info" },
			cpu:  []utilizationCoverageInterval{interval(0, 2, missingInventory)}, memory: []utilizationCoverageInterval{interval(0, 2, missingInventory)},
			cpuPeak: -1, memoryPeak: -1,
		},
		{
			name:    "usage only retains observed nodes",
			drop:    func(query string, _ map[string]string, _ int) bool { return query == "nodes" },
			cpu:     []utilizationCoverageInterval{interval(0, 2, utilizationCoverageInterval{Nodes: 1, MissingInventory: 1, MissingCapacity: 1})},
			memory:  []utilizationCoverageInterval{interval(0, 2, utilizationCoverageInterval{Nodes: 1, MissingInventory: 1, MissingCapacity: 1})},
			cpuPeak: -1, memoryPeak: -1,
		},
		{
			name: "overlapping missing categories",
			drop: func(query string, labels map[string]string, _ int) bool {
				return query == "cpu" || query == "available" || labels["__name__"] == "kube_node_info" || labels["__name__"] == "kube_node_status_capacity"
			},
			cpu:     []utilizationCoverageInterval{interval(0, 2, utilizationCoverageInterval{Nodes: 1, MissingInventory: 1, MissingUsage: 1, MissingCapacity: 1})},
			memory:  []utilizationCoverageInterval{interval(0, 2, utilizationCoverageInterval{Nodes: 1, MissingInventory: 1, MissingUsage: 1, MissingCapacity: 1})},
			cpuPeak: -1, memoryPeak: -1,
		},
		{
			name:   "absent expected cluster with observed nodes",
			drop:   func(query string, _ map[string]string, minute int) bool { return query == "inventory" && minute == 1 },
			cpu:    []utilizationCoverageInterval{interval(0, 0, valid), interval(1, 1, missingCluster), interval(2, 2, valid)},
			memory: []utilizationCoverageInterval{interval(0, 0, valid), interval(1, 1, missingCluster), interval(2, 2, valid)},
		},
		{
			name:   "expected cluster without observed nodes",
			drop:   func(query string, _ map[string]string, minute int) bool { return query != "inventory" && minute == 1 },
			cpu:    []utilizationCoverageInterval{interval(0, 0, valid), interval(1, 1, noNodes), interval(2, 2, valid)},
			memory: []utilizationCoverageInterval{interval(0, 0, valid), interval(1, 1, noNodes), interval(2, 2, valid)},
		},
		{
			name:   "absent cluster and nodes counted once",
			drop:   func(_ string, _ map[string]string, minute int) bool { return minute == 1 },
			cpu:    []utilizationCoverageInterval{interval(0, 0, valid), interval(1, 1, noNodes), interval(2, 2, valid)},
			memory: []utilizationCoverageInterval{interval(0, 0, valid), interval(1, 1, noNodes), interval(2, 2, valid)},
		},
		{
			name: "missing KSM memory capacity does not exclude CPU",
			drop: func(_ string, labels map[string]string, minute int) bool {
				return labels["__name__"] == "kube_node_status_capacity" && labels["resource"] == "memory" && minute == 0
			},
			cpu: complete, memory: []utilizationCoverageInterval{interval(0, 0, missingCapacity), interval(1, 2, valid)},
			memoryPeak: 1,
		},
		{
			name: "missing exporter total overlaps usage and capacity",
			drop: func(query string, _ map[string]string, minute int) bool { return query == "total" && minute == 0 },
			cpu:  complete, memory: []utilizationCoverageInterval{interval(0, 0, utilizationCoverageInterval{Nodes: 1, MissingUsage: 1, MissingCapacity: 1}), interval(1, 2, valid)},
			memoryPeak: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			results := utilizationTestHistory([]string{"mgmt", "Svc.Mixed"}, 3)
			for i := range results {
				results[i].series = slices.DeleteFunc(results[i].series, func(s promutil.Result) bool {
					ts, _, _ := utilizationValue(s.Values[0])
					return test.drop != nil && test.drop(results[i].query.name, s.Metric, int((ts-utilizationTestTime.Unix())/60))
				})
			}
			end := utilizationTestTime.Add(2 * time.Minute)
			report := collectUtilization(context.Background(), utilizationTestTime, end, end, utilizationTestQuery(results))
			var want []utilizationCoverage
			for _, scope := range []string{"Svc.Mixed", "mgmt", "overall"} {
				for i, resource := range []string{"cpu", "memory"} {
					intervals := slices.Clone([][]utilizationCoverageInterval{test.cpu, test.memory}[i])
					if scope == "overall" {
						for j := range intervals {
							intervals[j].Nodes *= 2
							intervals[j].MissingInventory *= 2
							intervals[j].MissingUsage *= 2
							intervals[j].MissingCapacity *= 2
							intervals[j].MissingClusters *= 2
						}
					}
					want = append(want, utilizationCoverage{Scope: scope, Resource: resource, Intervals: intervals})
				}
			}
			if !reflect.DeepEqual(report.Coverage, want) {
				t.Errorf("coverage = %+v, want %+v", report.Coverage, want)
			}
			wantPeaks := map[string]time.Time{}
			for _, scope := range []string{"Svc.Mixed", "mgmt", "overall"} {
				for i, resource := range []string{"CPU", "memory"} {
					if minute := []int{test.cpuPeak, test.memoryPeak}[i]; minute >= 0 {
						wantPeaks[scope+" "+resource+" peak"] = utilizationTestTime.Add(time.Duration(minute) * time.Minute)
					}
				}
			}
			peaks := map[string]time.Time{}
			for _, snapshot := range report.Snapshots {
				for _, reason := range snapshot.Reasons {
					peaks[reason] = snapshot.Time
				}
			}
			if !reflect.DeepEqual(peaks, wantPeaks) {
				t.Errorf("selected peaks changed: got %v, want %v", peaks, wantPeaks)
			}
		})
	}
}

func TestUtilizationCoverageAutoscalingAndCounts(t *testing.T) {
	end := utilizationTestTime.Add(4 * time.Minute)
	history, clusters := utilizationBuildHistory(utilizationTestHistory([]string{"svc"}, 5), utilizationTestTime, end)
	// Replacements with identical counts coalesce; additions and removals do not.
	for minute := 1; minute < 5; minute++ {
		nodes := history[utilizationTestTime.Add(time.Duration(minute)*time.Minute).Unix()].nodes
		node := nodes[utilizationNodeKey{"svc", "node"}]
		delete(nodes, utilizationNodeKey{"svc", "node"})
		node.Name = "replacement"
		nodes[utilizationNodeKey{"svc", node.Name}] = node
		if minute == 2 || minute == 3 {
			added := *node
			added.Name = "added"
			nodes[utilizationNodeKey{"svc", added.Name}] = &added
		}
	}
	for _, incomplete := range []bool{false, true} {
		if incomplete {
			for _, minute := range history {
				for _, node := range minute.nodes {
					node.Usage.CPU = nil
				}
			}
		}
		snapshots, coverage, _ := utilizationSelectSnapshots(history, clusters, utilizationTestTime, end, nil)
		if len(snapshots) != 1 || !snapshots[0].Time.Equal(utilizationTestTime) {
			t.Fatalf("autoscaling must preserve earliest peak: %+v", snapshots)
		}
		for _, entry := range coverage {
			var want []utilizationCoverageInterval
			for _, span := range []struct{ first, last, nodes int }{{0, 1, 1}, {2, 3, 2}, {4, 4, 1}} {
				status := utilizationCoverageInterval{
					Start:    utilizationTestTime.Add(time.Duration(span.first) * time.Minute),
					End:      utilizationTestTime.Add(time.Duration(span.last) * time.Minute),
					Eligible: true, Nodes: span.nodes,
				}
				if incomplete && entry.Resource == "cpu" {
					status.Eligible, status.MissingUsage = false, span.nodes
				}
				want = append(want, status)
			}
			if !reflect.DeepEqual(entry.Intervals, want) {
				t.Errorf("%s/%s incomplete=%v: intervals = %+v, want %+v", entry.Scope, entry.Resource, incomplete, entry.Intervals, want)
			}
		}
	}
}

func TestUtilizationCoverageMixedClusterDiagnostics(t *testing.T) {
	end := utilizationTestTime.Add(2 * time.Minute)
	history, clusters := utilizationBuildHistory(utilizationTestHistory([]string{"a", "b", "c"}, 3), utilizationTestTime, end)
	for offset := range 3 {
		minute := history[utilizationTestTime.Add(time.Duration(offset)*time.Minute).Unix()]
		minute.nodes[utilizationNodeKey{"a", "node"}].Usage.CPU = nil
		minute.nodes[utilizationNodeKey{"b", "node"}].inventory = false
		delete(minute.nodes, utilizationNodeKey{"c", "node"})
		if offset > 0 {
			minute.nodes[utilizationNodeKey{"b", "node"}].Usage.CPU = nil
		}
	}
	snapshots, coverage, _ := utilizationSelectSnapshots(history, clusters, utilizationTestTime, end, nil)
	if len(snapshots) != 1 || !slices.Equal(snapshots[0].Reasons, []string{"a memory peak"}) {
		t.Fatalf("only the complete cluster/resource may select a peak: %+v", snapshots)
	}
	for _, entry := range coverage {
		if entry.Scope != "overall" {
			continue
		}
		want := []utilizationCoverageInterval{{Start: utilizationTestTime, End: end, Nodes: 2, MissingInventory: 1, MissingClusters: 1}}
		if entry.Resource == "cpu" {
			want[0].End = utilizationTestTime
			want[0].MissingUsage = 1
			later := want[0]
			later.Start, later.End = utilizationTestTime.Add(time.Minute), end
			later.MissingUsage = 2
			want = append(want, later)
		}
		if !reflect.DeepEqual(entry.Intervals, want) {
			t.Errorf("overall must sum mixed diagnostics and split changing counts: %+v, want %+v", entry, want)
		}
	}
}

func TestUtilizationCoverageZeroCapacity(t *testing.T) {
	for _, metric := range []string{"cpu", "memory", "total"} {
		t.Run(metric, func(t *testing.T) {
			results := utilizationTestHistory([]string{"svc"}, 1)
			for i := range results {
				for j := range results[i].series {
					s := &results[i].series[j]
					if (s.Metric["__name__"] == "kube_node_status_capacity" && s.Metric["resource"] == metric) || (metric == "total" && (results[i].query.name == "total" || results[i].query.name == "available")) {
						s.Values[0][1] = "0"
					}
				}
			}
			report := collectUtilization(context.Background(), utilizationTestTime, utilizationTestTime, utilizationTestTime, utilizationTestQuery(results))
			for _, entry := range report.Coverage {
				missing := entry.Resource == metric || (metric == "total" && entry.Resource == "memory")
				want := utilizationCoverageInterval{Start: utilizationTestTime, End: utilizationTestTime, Eligible: !missing, Nodes: 1}
				if missing {
					want.MissingCapacity = 1
				}
				if !reflect.DeepEqual(entry.Intervals, []utilizationCoverageInterval{want}) {
					t.Errorf("zero capacity: %+v, want %+v", entry, want)
				}
			}
		})
	}
}

func TestUtilizationCoverageEarlyReturns(t *testing.T) {
	end := utilizationTestTime.Add(2 * time.Minute)
	report := collectUtilization(context.Background(), utilizationTestTime, end, end, utilizationTestQuery(nil))
	want := []utilizationCoverage{
		{Scope: "overall", Resource: "cpu", Intervals: []utilizationCoverageInterval{{Start: utilizationTestTime, End: end}}},
		{Scope: "overall", Resource: "memory", Intervals: []utilizationCoverageInterval{{Start: utilizationTestTime, End: end}}},
	}
	if !reflect.DeepEqual(report.Coverage, want) || len(report.Snapshots) != 0 || !slices.Contains(report.Warnings, "expected underlay cluster inventory unavailable; no peaks selected") {
		t.Errorf("missing cluster inventory must retain excluded overall intervals and warning: %+v", report)
	}
	report = collectUtilization(context.Background(), utilizationTestTime.Add(time.Second), utilizationTestTime.Add(2*time.Second), end, func(context.Context, string, string, time.Time, time.Time) ([]promutil.Result, error) {
		t.Error("empty minute window must not query")
		return nil, nil
	})
	if len(report.Coverage) != 0 || !slices.Contains(report.Warnings, "time window contains no UTC minute samples") {
		t.Errorf("empty minute window must not invent coverage: %+v", report)
	}
}

func TestUtilizationCoverageJSONOptional(t *testing.T) {
	report := collectUtilization(context.Background(), utilizationTestTime, utilizationTestTime, utilizationTestTime, utilizationTestQuery(utilizationTestHistory([]string{"svc"}, 1)))
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var replay utilizationReport
	if err := json.Unmarshal(data, &replay); err != nil {
		t.Fatal(err)
	}
	if replay.SchemaVersion != 1 || !reflect.DeepEqual(replay.Coverage, report.Coverage) {
		t.Errorf("coverage must round trip in schema 1: %+v", replay)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	// Only snapshots retain node identities; coverage contains aggregate counts.
	if strings.Contains(string(fields["coverage"]), `"node"`) || strings.Contains(string(fields["coverage"]), `"name"`) {
		t.Errorf("node histories leaked into coverage: %s", fields["coverage"])
	}
	delete(fields, "coverage")
	legacy, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	replay = utilizationReport{}
	if err := json.Unmarshal(legacy, &replay); err != nil {
		t.Fatal(err)
	}
	if replay.SchemaVersion != 1 || replay.Coverage != nil || !reflect.DeepEqual(replay.Snapshots, report.Snapshots) {
		t.Errorf("legacy schema 1 reports must remain valid without coverage: %+v", replay)
	}
}
