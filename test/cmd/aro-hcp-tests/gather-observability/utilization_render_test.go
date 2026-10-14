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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func utilizationFixture(t *testing.T) utilizationReport {
	t.Helper()
	data, err := os.ReadFile("testdata/utilization-synthetic.json")
	if err != nil {
		t.Fatal(err)
	}
	var report utilizationReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("decode synthetic fixture: %v", err)
	}
	return report
}

func TestRenderUtilizationHTML(t *testing.T) {
	report := utilizationFixture(t)
	attack := "</script><img src=x onerror=alert(1)>&\u2028\u2029"
	report.Warnings = append(report.Warnings, attack)
	report.Snapshots[0].Nodes[0].Name = attack
	report.Snapshots[0].Workloads[0].Component = attack
	html, err := renderUtilizationHTML(report)
	if err != nil {
		t.Fatalf("render representative report: %v", err)
	}
	text := string(html)
	for _, want := range []string{"<!DOCTYPE html>", `name="viewport"`, `id="capacity-chart-cpu"`, `id="capacity-chart-memory"`, `id="composition-chart-cpu"`, `id="composition-chart-memory"`, `id="workloads"`, `id="snapshot"`, `\u003c/script\u003e`, `\u0026`, `\u2028`, `\u2029`, "https://go-echarts.github.io/go-echarts-assets/assets/echarts.min.js"} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered utilization missing %q", want)
		}
	}
	for _, forbidden := range []string{attack, "innerHTML", "tailwind", "#ZgotmplZ", `id="resource"`, `$('resource')`} {
		if strings.Contains(text, forbidden) {
			t.Errorf("unsafe or unexpected content in output: %q", forbidden)
		}
	}
	const marker = `<script id="utilization-data" type="application/json">`
	if strings.Count(text, marker) != 1 || strings.Count(text, `"schemaVersion"`) != 1 {
		t.Fatal("report must be embedded exactly once")
	}
	_, embedded, ok := strings.Cut(text, marker)
	if !ok {
		t.Fatal("JSON data block missing")
	}
	embedded, _, _ = strings.Cut(embedded, "</script>")
	var decoded utilizationReport
	if err := json.Unmarshal([]byte(embedded), &decoded); err != nil {
		t.Fatalf("embedded JSON is not valid JSON: %v", err)
	}
	if !reflect.DeepEqual(report, decoded) {
		t.Fatal("embedded JSON changed the report (including unknown values or HTML-like strings)")
	}
}

func TestRenderUtilizationValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*utilizationReport)
		want string
	}{
		{"schema", func(r *utilizationReport) { r.SchemaVersion++ }, "schemaVersion"},
		{"missing generated time", func(r *utilizationReport) { r.GeneratedAt = time.Time{} }, "timestamps"},
		{"missing start", func(r *utilizationReport) { r.Start = time.Time{} }, "timestamps"},
		{"missing end", func(r *utilizationReport) { r.End = time.Time{} }, "timestamps"},
		{"reversed window", func(r *utilizationReport) { r.End = r.Start.Add(-time.Second) }, "start <= end"},
		{"missing snapshot time", func(r *utilizationReport) { r.Snapshots[0].Time = time.Time{} }, "snapshot 0"},
		{"early snapshot", func(r *utilizationReport) { r.Snapshots[0].Time = r.Start.Add(-time.Second) }, "snapshot 0"},
		{"late snapshot", func(r *utilizationReport) { r.Snapshots[0].Time = r.End.Add(time.Second) }, "snapshot 0"},
		{"negative capacity", func(r *utilizationReport) { *r.Snapshots[0].Nodes[0].Capacity.CPU = -1 }, "node 0"},
		{"infinite allocatable", func(r *utilizationReport) { *r.Snapshots[0].Nodes[0].Allocatable.Memory = math.Inf(1) }, "node 0"},
		{"NaN usage", func(r *utilizationReport) { *r.Snapshots[0].Nodes[0].Usage.CPU = math.NaN() }, "node 0"},
		{"negative requests", func(r *utilizationReport) { *r.Snapshots[0].Workloads[0].Requests.CPU = -1 }, "workload 0"},
		{"negative limits", func(r *utilizationReport) { *r.Snapshots[0].Workloads[0].Limits.Memory = -1 }, "workload 0"},
		{"negative container usage", func(r *utilizationReport) { *r.Snapshots[0].Workloads[0].Containers[0].Usage.CPU = -1 }, "container 0"},
		{"negative pods", func(r *utilizationReport) { r.Snapshots[0].Workloads[0].Pods = -1 }, "pod counts"},
		{"negative pending", func(r *utilizationReport) { r.Snapshots[0].Workloads[0].PendingPods = -1 }, "pod counts"},
		{"excess pending", func(r *utilizationReport) { r.Snapshots[0].Workloads[0].PendingPods = 4 }, "pendingPods cannot exceed"},
		{"negative unlimited", func(r *utilizationReport) { *r.Snapshots[0].Workloads[0].Containers[0].UnlimitedMemory = -1 }, "unlimited counts"},
		{"excess unlimited", func(r *utilizationReport) { *r.Snapshots[0].Workloads[0].Containers[0].UnlimitedCPU = 4 }, "unlimited counts"},
		{"duplicate node", func(r *utilizationReport) {
			r.Snapshots[0].Nodes = append(r.Snapshots[0].Nodes, r.Snapshots[0].Nodes[0])
		}, "duplicate node"},
		{"duplicate cluster", func(r *utilizationReport) { r.Clusters = append(r.Clusters, r.Clusters[0]) }, "unique nonempty"},
		{"empty node name", func(r *utilizationReport) { r.Snapshots[0].Nodes[0].Name = "" }, "named node"},
		{"undeclared node cluster", func(r *utilizationReport) { r.Snapshots[0].Nodes[0].Cluster = "other" }, "declared cluster"},
		{"undeclared workload cluster", func(r *utilizationReport) { r.Snapshots[0].Workloads[0].Cluster = "other" }, "declared cluster"},
		{"unscheduled placed workload", func(r *utilizationReport) { r.Snapshots[0].Workloads[0].Unscheduled = true }, "unscheduled workload"},
		{"duplicate container", func(r *utilizationReport) {
			w := &r.Snapshots[0].Workloads[0]
			w.Containers = append(w.Containers, w.Containers[0])
		}, "container names"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := utilizationFixture(t)
			tc.edit(&report)
			if _, err := renderUtilizationHTML(report); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q validation error, got %v", tc.want, err)
			}
		})
	}
	t.Run("incomplete is valid", func(t *testing.T) {
		report := utilizationFixture(t)
		report.Clusters = []string{"unknown"}
		report.Snapshots[0].Nodes = []utilizationNode{{Cluster: "unknown", Name: "unknown"}}
		report.Snapshots[0].Workloads = []utilizationWorkload{{Cluster: "unknown"}}
		if _, err := renderUtilizationHTML(report); err != nil {
			t.Fatalf("nil resource data must be accepted: %v", err)
		}
		report.Snapshots = nil
		if _, err := renderUtilizationHTML(report); err != nil {
			t.Fatalf("nil snapshots must be accepted: %v", err)
		}
	})
}

// Artificial coverage on the existing synthetic measurement fixture, never on
// a collected report. Keep the example reproducible without changing real data.
func utilizationCoverageFixture(t *testing.T) utilizationReport {
	t.Helper()
	report := utilizationFixture(t)
	report.Warnings = append([]string{"SYNTHETIC COVERAGE EXAMPLE: artificial coverage intervals over synthetic measurements, not an actual collection."}, report.Warnings...)
	report.Snapshots[0].Reasons = []string{"synthetic-svc-1 CPU peak", "synthetic-mgmt-1 CPU peak", "overall CPU peak"}
	for _, scope := range append([]string{"overall"}, report.Clusters...) {
		for _, resource := range []string{"cpu", "memory"} {
			coverage := utilizationCoverage{Scope: scope, Resource: resource}
			for minute := range 31 {
				i := utilizationCoverageInterval{Start: report.Start.Add(time.Duration(minute) * time.Minute), Eligible: true, Nodes: 2}
				i.End = i.Start
				switch scope {
				case "overall", "synthetic-mgmt-1":
					i.Eligible = resource == "cpu" && minute != 19 && minute != 21
				case "synthetic-svc-1":
					i.Eligible = resource == "memory" || minute >= 4 && minute < 28
				case "synthetic-mgmt-2":
					i.Eligible = resource == "cpu" && minute < 25 || resource == "memory" && minute >= 6
				}
				if !i.Eligible {
					i.MissingUsage = 1
				}
				if n := len(coverage.Intervals); n > 0 && coverage.Intervals[n-1].Eligible == i.Eligible {
					coverage.Intervals[n-1].End = i.End
				} else {
					coverage.Intervals = append(coverage.Intervals, i)
				}
			}
			report.Coverage = append(report.Coverage, coverage)
		}
	}
	return report
}

func TestRenderUtilizationCoverageValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*utilizationReport)
		want string
	}{
		{"scope", func(r *utilizationReport) { r.Coverage[0].Scope = "other" }, "scope/resource"},
		{"resource", func(r *utilizationReport) { r.Coverage[0].Resource = "CPU" }, "scope/resource"},
		{"duplicate", func(r *utilizationReport) { r.Coverage = append(r.Coverage, r.Coverage[0]) }, "unique"},
		{"empty intervals", func(r *utilizationReport) { r.Coverage[0].Intervals = nil }, "cannot be empty"},
		{"non UTC", func(r *utilizationReport) {
			r.Coverage[0].Intervals[0].Start = r.Start.In(time.FixedZone("offset", 3600))
		}, "UTC-minute"},
		{"seconds", func(r *utilizationReport) { r.Coverage[0].Intervals[0].Start = r.Start.Add(time.Second) }, "UTC-minute"},
		{"nanoseconds", func(r *utilizationReport) { r.Coverage[0].Intervals[0].End = r.Start.Add(time.Nanosecond) }, "UTC-minute"},
		{"zero time", func(r *utilizationReport) { r.Coverage[0].Intervals[0].Start = time.Time{} }, "UTC-minute"},
		{"outside", func(r *utilizationReport) { r.Coverage[0].Intervals[0].Start = r.Start.Add(-time.Minute) }, "report grid"},
		{"late end", func(r *utilizationReport) { r.Coverage[1].Intervals[0].End = r.End.Add(time.Minute) }, "report grid"},
		{"missing first", func(r *utilizationReport) { r.Coverage[0].Intervals[0].Start = r.Start.Add(time.Minute) }, "contiguous"},
		{"missing last", func(r *utilizationReport) { r.Coverage[1].Intervals[0].End = r.End.Add(-time.Minute) }, "cover the evaluated"},
		{"gap", func(r *utilizationReport) { r.Coverage[0].Intervals[0].End = r.Start.Add(17 * time.Minute) }, "contiguous"},
		{"overlap", func(r *utilizationReport) { r.Coverage[0].Intervals[0].End = r.Start.Add(19 * time.Minute) }, "contiguous"},
		{"unordered", func(r *utilizationReport) {
			r.Coverage[0].Intervals[0], r.Coverage[0].Intervals[1] = r.Coverage[0].Intervals[1], r.Coverage[0].Intervals[0]
		}, "ordered"},
		{"reversed", func(r *utilizationReport) { r.Coverage[0].Intervals[1].End = r.Start }, "ordered"},
		{"negative nodes", func(r *utilizationReport) { r.Coverage[0].Intervals[0].Nodes = -1 }, "nonnegative"},
		{"negative inventory", func(r *utilizationReport) { r.Coverage[0].Intervals[1].MissingInventory = -1 }, "missing node counts"},
		{"excess inventory", func(r *utilizationReport) { r.Coverage[0].Intervals[1].MissingInventory = 3 }, "missing node counts"},
		{"negative usage", func(r *utilizationReport) { r.Coverage[0].Intervals[1].MissingUsage = -1 }, "missing node counts"},
		{"excess usage", func(r *utilizationReport) { r.Coverage[0].Intervals[1].MissingUsage = 3 }, "missing node counts"},
		{"negative capacity", func(r *utilizationReport) { r.Coverage[0].Intervals[1].MissingCapacity = -1 }, "missing node counts"},
		{"excess capacity", func(r *utilizationReport) { r.Coverage[0].Intervals[1].MissingCapacity = 3 }, "missing node counts"},
		{"negative clusters", func(r *utilizationReport) { r.Coverage[0].Intervals[1].MissingClusters = -1 }, "within scope"},
		{"excess overall clusters", func(r *utilizationReport) { r.Coverage[0].Intervals[1].MissingClusters = 4 }, "within scope"},
		{"excess cluster clusters", func(r *utilizationReport) { r.Coverage[2].Intervals[0].MissingClusters = 2 }, "within scope"},
		{"eligible inventory gap", func(r *utilizationReport) { r.Coverage[0].Intervals[0].MissingInventory = 1 }, "eligible samples"},
		{"eligible usage gap", func(r *utilizationReport) { r.Coverage[0].Intervals[0].MissingUsage = 1 }, "eligible samples"},
		{"eligible capacity gap", func(r *utilizationReport) { r.Coverage[0].Intervals[0].MissingCapacity = 1 }, "eligible samples"},
		{"eligible cluster gap", func(r *utilizationReport) { r.Coverage[0].Intervals[0].MissingClusters = 1 }, "eligible samples"},
		{"eligible no nodes", func(r *utilizationReport) { r.Coverage[0].Intervals[0].Nodes = 0 }, "eligible samples"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := utilizationCoverageFixture(t)
			tc.edit(&r)
			if _, err := renderUtilizationHTML(r); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
		})
	}
	r := utilizationCoverageFixture(t)
	if _, err := renderUtilizationHTML(r); err != nil {
		t.Fatalf("valid inclusive coverage: %v", err)
	}
	// Evaluation rounds the start up and end down, not to the nearest minute.
	r.Start = r.Start.Add(-30 * time.Second)
	r.End = r.End.Add(30 * time.Second)
	if _, err := renderUtilizationHTML(r); err != nil {
		t.Fatalf("non-minute report bounds: %v", err)
	}
	r.Snapshots = nil
	r.Start = r.End.Add(-time.Second)
	if _, err := renderUtilizationHTML(r); err == nil {
		t.Fatal("coverage cannot exist when there are no evaluated minutes")
	}
	r.Coverage, r.Clusters = nil, nil
	if _, err := renderUtilizationHTML(r); err != nil {
		t.Fatalf("no-grid/no-cluster error reports may omit coverage: %v", err)
	}
}

func TestRenderUtilizationBrowserTimeline(t *testing.T) {
	report := utilizationCoverageFixture(t)
	// Optional example artifact uses only this clearly labeled synthetic fixture.
	if output := os.Getenv("UTILIZATION_COVERAGE_EXAMPLE"); output != "" {
		html, err := renderUtilizationHTML(report)
		if err != nil {
			t.Fatal(err)
		}
		html = bytes.Replace(html, []byte("<h1>Cluster utilization</h1>"), []byte("<h1>Synthetic Coverage Example</h1><p class=\"warning\">Artificial coverage intervals over synthetic measurements. Not an actual collection.</p>"), 1)
		if err := os.WriteFile(output, html, 0600); err != nil {
			t.Fatal(err)
		}
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(strings.TrimSuffix(output, filepath.Ext(output))+".json", data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	checkUtilizationBrowser(t, report, `
  const stat = (scope, resource) => coverageStats(report.coverage.find(c => c.scope === scope && c.resource === resource));
  check(stat('synthetic-svc-1', 'cpu').leading === 4 && stat('synthetic-svc-1', 'cpu').trailing === 3 && stat('synthetic-svc-1', 'cpu').eligible === 24, 'inclusive leading and trailing counts');
  check(stat('synthetic-mgmt-2', 'cpu').trailing === 6 && !stat('synthetic-mgmt-2', 'cpu').leading, 'trailing only');
  check(stat('synthetic-mgmt-2', 'memory').leading === 6 && !stat('synthetic-mgmt-2', 'memory').trailing, 'leading only');
  check(stat('synthetic-mgmt-1', 'cpu').interior === 2 && stat('synthetic-mgmt-1', 'cpu').eligible === 29, 'interior excludes complete samples between gaps');
  check(stat('synthetic-mgmt-1', 'memory').allmissing === 31 && !stat('synthetic-mgmt-1', 'memory').leading, 'allmissing is not startup');
  check(stat('synthetic-svc-1', 'memory').eligible === 31, 'complete coverage');
  check($('timeline-summary').textContent.includes('1 complete, 3 edge-only gaps, 2 with interior gaps, 2 all missing'), 'report summary counts scope/resource timelines, not merged minutes');
  check($('timeline-summary').textContent.includes('not proof') && $('timeline-summary').textContent.includes('run peak is uncertain'), 'edge and interior caveats');
  check(!document.querySelector('.timeline, .coverage-resource'), 'timeline DOM lazy');
  const details = $('coverage-timelines'); details.querySelector('summary').click(); await tick();
  check(details.querySelectorAll('details').length === 4 && !details.querySelector('.timeline'), 'scopes grouped with lazy resource rows');
  const group = [...details.querySelectorAll('details')].find(d => d.querySelector('summary').textContent === 'synthetic-mgmt-1');
  group.querySelector('summary').click(); await tick();
  check(group.querySelectorAll('.timeline').length === 2, 'both resources grouped');
  check(group.textContent.includes('Adjacent incomplete sample within 2 minutes'), 'near peak gaps explicitly marked');
  check(!adjacentGap(stat('synthetic-svc-1', 'cpu'), report.snapshots[0].time), 'distant edge gaps not called adjacent');
  const raw = group.querySelector('textarea');
  check(raw.readOnly && raw.value.split('\n').length === 2 && raw.value.includes('2026-09-18T12:19:00Z to 2026-09-18T12:19:00Z | 1 minute samples | interior | nodes=2'), 'exact inclusive gap times, counts and classes');
  check(raw.value.includes('missing inventory=0 | missing usage=1 | missing capacity=0 | missing clusters=0'), 'per-node reason counts retained');
  check(group.textContent.includes('at EACH minute') && group.textContent.includes('reasons may overlap'), 'counts not interpreted as sums');
  check(group.querySelector('.timeline').style.background.includes('linear-gradient'), 'one compact gradient div per resource');
  check(document.documentElement.scrollWidth <= window.innerWidth, 'timeline fits mobile');
  startCharts(); for (const resource of resources) verifyCapacityPixels(capacityCharts[resource]);
  group.querySelector('summary').click(); await tick();
  check(!group.querySelector('.timeline, textarea'), 'collapse releases resource DOM');
  details.querySelector('summary').click(); await tick();
  check(!details.querySelector('details, .timeline, textarea'), 'collapse releases all scope DOM');
`)
}

func TestRenderUtilizationBrowserTimelineLarge(t *testing.T) {
	report := utilizationCoverageFixture(t)
	report.End = report.Start.Add(9999 * time.Minute)
	report.Coverage = report.Coverage[:1]
	report.Coverage[0].Intervals = nil
	for minute := range 10000 {
		timestamp := report.Start.Add(time.Duration(minute) * time.Minute)
		report.Coverage[0].Intervals = append(report.Coverage[0].Intervals, utilizationCoverageInterval{Start: timestamp, End: timestamp, Eligible: minute%2 == 0, Nodes: 2, MissingUsage: minute % 2})
	}
	for i := range 30 {
		scope := fmt.Sprintf("synthetic-extra-%d", i)
		report.Clusters = append(report.Clusters, scope)
		report.Coverage = append(report.Coverage, utilizationCoverage{Scope: scope, Resource: "cpu", Intervals: []utilizationCoverageInterval{{Start: report.Start, End: report.End, Eligible: true, Nodes: 1}}})
	}
	checkUtilizationBrowser(t, report, `
  const details = $('coverage-timelines');
  check(!details.querySelector('details, textarea, .timeline'), 'large timelines initially lazy');
  details.querySelector('summary').click(); await tick();
  check(details.querySelectorAll('details').length === 25, 'scope list paginated');
  const group = details.querySelector('details'); group.querySelector('summary').click(); await tick();
  check(group.querySelectorAll('*').length < 15, '10000 intervals do not expand DOM');
  check(group.querySelectorAll('.timeline').length === 1, 'one gradient for 10000 intervals');
  check(group.querySelector('textarea').value.split('\n').length === 5000, 'all gaps accessible in bounded readonly DOM');
  check(group.textContent.includes('Coverage timeline not saved for this resource'), 'partial resource coverage is explicitly unknown');
  details.querySelector('[aria-label="Next coverage scopes"]').click();
  check(details.querySelectorAll('details').length === 6 && !details.querySelector('textarea, .timeline'), 'pagination disposes expanded resources');
  check(details.textContent.includes('synthetic-extra-29'), 'last scope accessible');
  check($('warnings').querySelectorAll('*').length < 100, 'large coverage DOM stays bounded independently of cluster summary cards');
`)
}

func TestRenderUtilizationCommand(t *testing.T) {
	report := utilizationFixture(t)
	valid, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	withCoverage, err := json.Marshal(utilizationCoverageFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	withHistory, err := json.Marshal(resourceHistoryFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, input, want string
	}{
		{"valid", string(valid) + "\n \t", ""},
		{"coverage", string(withCoverage), ""},
		{"history", string(withHistory), ""},
		{"invalid history", strings.Replace(string(withHistory), `"step":"1m"`, `"step":"5m"`, 1), "history step"},
		{"invalid coverage", strings.Replace(string(withCoverage), `"resource":"cpu"`, `"resource":"disk"`, 1), "scope/resource"},
		{"null unlimited", strings.Replace(string(valid), `"unlimitedCPU":0`, `"unlimitedCPU":null`, 1), ""},
		{"empty report", `{"schemaVersion":1,"generatedAt":"2026-09-18T12:35:00Z","start":"2026-09-18T12:00:00Z","end":"2026-09-18T12:30:00Z","snapshots":null}`, ""},
		{"invalid JSON", "{", "decode utilization"},
		{"empty", "", "decode utilization"},
		{"null", "null", "schemaVersion"},
		{"unsupported schema", strings.Replace(string(valid), `"schemaVersion":1`, `"schemaVersion":99`, 1), "schemaVersion"},
		{"unknown field", strings.Replace(string(valid), `"schemaVersion":1`, `"schemaVersion":1,"typo":true`, 1), "unknown field"},
		{"invalid timestamp", strings.Replace(string(valid), "2026-09-18T12:35:00Z", "not-a-time", 1), "decode utilization"},
		{"invalid resource type", strings.Replace(string(valid), `"cpu":8`, `"cpu":"8"`, 1), "decode utilization"},
		{"negative resource", strings.Replace(string(valid), `"cpu":8`, `"cpu":-8`, 1), "nonnegative"},
		{"overflow resource", strings.Replace(string(valid), `"cpu":8`, `"cpu":1e999`, 1), "decode utilization"},
		{"missing pods", strings.Replace(string(valid), `"pods":3,`, ``, 1), "pods and pendingPods are required"},
		{"null pods", strings.Replace(string(valid), `"pods":3`, `"pods":null`, 1), "pods and pendingPods are required"},
		{"missing pending", strings.Replace(string(valid), `"pendingPods":0,`, ``, 1), "pods and pendingPods are required"},
		{"null pending", strings.Replace(string(valid), `"pendingPods":0`, `"pendingPods":null`, 1), "pods and pendingPods are required"},
		{"fractional pods", strings.Replace(string(valid), `"pods":3`, `"pods":3.5`, 1), "decode utilization"},
		{"missing unlimited CPU", strings.Replace(string(valid), `"unlimitedCPU":0,`, ``, 1), "unlimitedCPU and unlimitedMemory are required"},
		{"missing unlimited memory", strings.Replace(string(valid), `,"unlimitedMemory":0`, ``, 1), "unlimitedCPU and unlimitedMemory are required"},
		{"fractional unlimited", strings.Replace(string(valid), `"unlimitedCPU":0`, `"unlimitedCPU":0.5`, 1), "decode utilization"},
		{"trailing object", string(valid) + "{}", "trailing data"},
		{"trailing null", string(valid) + "null", "trailing data"},
		{"trailing garbage", string(valid) + "!", "trailing data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			input, output := filepath.Join(dir, "utilization.json"), filepath.Join(dir, "output")
			if err := os.WriteFile(input, []byte(tc.input), 0600); err != nil {
				t.Fatal(err)
			}
			cmd, err := NewCommand()
			if err != nil {
				t.Fatal(err)
			}
			// Exercise the actual parent/child dispatch, not only the child's RunE.
			// No rendered-config, subscription, timing input or Azure credentials.
			cmd.SetArgs([]string{"render-utilization", "--input", input, "--output", output})
			err = cmd.Execute()
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("expected %q, got %v", tc.want, err)
				}
				if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
					t.Fatalf("invalid input should not create output: %v", statErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("offline child unexpectedly requires live setup: %v", err)
			}
			actual, err := os.ReadFile(filepath.Join(output, "utilization-summary.html"))
			if err != nil {
				t.Fatal(err)
			}
			var expected utilizationReport
			if err := json.Unmarshal([]byte(tc.input), &expected); err != nil {
				t.Fatal(err)
			}
			want, err := renderUtilizationHTML(expected)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(actual, want) {
				t.Fatal("offline child did not use the shared renderer")
			}
			actual, err = os.ReadFile(filepath.Join(output, "resource-history-summary.html"))
			if err != nil {
				t.Fatal(err)
			}
			want, err = renderResourceHistoryHTML(expected)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(actual, want) {
				t.Fatal("offline child did not use the shared history renderer")
			}
		})
	}
	for _, args := range [][]string{nil, {"--input", "unused"}, {"--output", "unused"}, {"extra"}} {
		cmd := newRenderUtilizationCommand()
		cmd.SetArgs(args)
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		if err := cmd.Execute(); err == nil {
			t.Errorf("expected argument validation error for %v", args)
		}
	}
}

// Set UTILIZATION_ECHARTS_JS to a cached copy of the template's CDN asset to
// additionally verify actual ECharts pixel layouts, without network in the test.
func TestRenderUtilizationBrowser(t *testing.T) {
	report := utilizationFixture(t)
	report.Warnings = append(report.Warnings, `</script><img src=x onerror="window.injected=true">`)
	checkUtilizationBrowser(t, report, `
  check(!window.echarts, 'test must start without CDN');
  check(!window.injected && !document.querySelector('img'), 'untrusted warning must remain text');
  check(!$('warnings').querySelector('textarea'), 'raw diagnostics are not built initially');
  const diagnostics = [...$('warnings').querySelectorAll('details')].find(details => details.querySelector('summary').textContent.startsWith('Diagnostics:'));
  diagnostics.querySelector('summary').click(); await tick();
  const raw = diagnostics.querySelector('textarea');
  check(raw.readOnly && raw.value.includes(report.warnings[report.warnings.length - 1]), 'readonly diagnostics preserve literal untrusted text in value');
  check(!window.injected && !document.querySelector('img'), 'opening diagnostics does not interpret HTML');
  diagnostics.querySelector('summary').click(); await tick();
  check(!diagnostics.querySelector('textarea'), 'closing diagnostics releases raw text DOM');
  check(document.documentElement.scrollWidth <= window.innerWidth, 'no page-level horizontal overflow');
  check(resources.every(r => $('capacity-chart-' + r).hidden && $('composition-chart-' + r).hidden), 'no empty chart space when CDN unavailable');
  check(!$('resource'), 'no resource selector, including hidden controls');
  check(document.querySelectorAll('#cards .card').length === 3, 'three separate cluster cards');
  check(document.querySelectorAll('#cards .resource-section.cpu').length === 3 && document.querySelectorAll('#cards .resource-section.memory').length === 3, 'each unchanged cluster card separates CPU and memory');
  const savedReasons = report.snapshots[0].reasons;
  report.snapshots[0].reasons = ['synthetic-svc-1 CPU peak', 'overall memory peak']; render();
  for (const card of document.querySelectorAll('#cards .card')) {
    const svc = card.querySelector('h3').textContent === 'synthetic-svc-1';
    check(card.querySelector('.cpu').textContent.includes('Cluster peak') === svc, 'CPU cluster peak badge only marks the matching cluster');
    check(!card.querySelector('.memory').textContent.includes('Cluster peak'), 'fleet peak does not imply every cluster peaked');
    check(card.querySelector('.memory').textContent.includes('At fleet peak') && !card.querySelector('.cpu').textContent.includes('At fleet peak'), 'fleet peak badge matches its resource domain');
  }
  report.snapshots[0].reasons = savedReasons; render();
  check(!$('node-details').open && !$('nodes').children.length, 'node measurements are initially lazy');
  $('node-details').querySelector('summary').click(); await tick();
  check(document.querySelectorAll('#nodes tr').length === 6, 'all node measurements without CDN after opening');
  check($('snapshot').options[0].textContent.includes('Peak pending demand'), 'labelled snapshot reasons');
  check($('warnings').textContent.includes('4/6 observed nodes have usable cpu'), 'compact node coverage excludes unknown usage and missing capacity');
  check($('warnings').textContent.includes('8/10 observed workload records have cpu usage; 9/10 have requests'), 'compact workload counts distinguish usage and requests');
  check($('warnings').textContent.includes('Unknown usage is never shown as unused space'), 'unknown usage interpretation remains prominent');
  check($('cards').textContent.includes('partial:'), 'partial totals are not complete totals');
  function cardUtilization(cluster, resource) {
    const card = [...document.querySelectorAll('#cards .card')].find(card => card.querySelector('h3').textContent === cluster);
    return [...card.querySelectorAll('.metric')].find(line => line.querySelector('dt').textContent === (resource === 'cpu' ? 'CPU' : 'Memory') + ' whole-node utilization').querySelector('dd').textContent;
  }
  check(cardUtilization('synthetic-svc-1', 'cpu') === '44.2%', 'cluster CPU percentage is weighted by physical capacity');
  check(cardUtilization('synthetic-svc-1', 'memory') === '46.9%', 'memory percentage shown alongside CPU');
  check(cardUtilization('synthetic-mgmt-1', 'cpu').startsWith('Unknown'), 'missing usage prevents cluster percentage');
  check(cardUtilization('synthetic-mgmt-2', 'cpu').startsWith('Unknown'), 'missing capacity prevents cluster percentage');
  check(nodeUtilization([], 'cpu') === 'Unknown', 'empty nodes cannot imply zero utilization');
  check(nodeUtilization([{capacity: {cpu: 0}, usage: {cpu: 0}}], 'cpu').startsWith('Unknown'), 'zero capacity has no percentage');
  check(nodeUtilization([{capacity: {cpu: 8}, usage: {cpu: 0}}], 'cpu') === '0%', 'measured zero usage remains zero percent');
  check($('nodes').children[0].children[4].textContent === '2.4 cores (30%)', 'node usage includes percent');
  check(!$('nodes').children[3].children[4].textContent.includes('%'), 'unknown node usage has no percentage');
  check($('nodes').children[0].children[8].textContent === '14 GiB (43.8%)', 'node memory percentage alongside CPU');
  check(currentGroups.map(g => g.name).join(',') === 'mgmt,svc', 'root is exactly two roles, not three clusters');
  check(currentGroups[0].rows.length === 8, 'both management clusters grouped together');
  check(['ci01-j1862272-svc', 'synthetic-svc-1', 'svc'].every(c => clusterRole(c) === 'svc'), 'service role at either name boundary');
  check(['ci01-j1862272-mgmt', 'synthetic-mgmt-2', 'mgmt'].every(c => clusterRole(c) === 'mgmt'), 'management role at either name boundary');
  check(['other', 'notsvc', 'mgmtnot'].every(c => clusterRole(c) === 'Other'), 'no role guesses');
  function clickRow(id, name) { [...$(id).querySelectorAll('button')].find(b => b.textContent === name).click(); }
  clickRow('workload-rows', 'mgmt');
  const component = currentGroups.find(g => g.name === 'kube-apiserver');
  check(component.rows.length === 5 && Math.abs(component.cpu.usage.value - 14.8) < 1e-9, 'HCP component aggregates namespaces across both mgmt clusters');
  check($('workload-rows').textContent.includes('2 clusters / 4 namespaces'), 'aggregate scope is visible');
  clickRow('workload-rows', 'kube-apiserver');
  check(currentGroups.length === 2, 'component splits into per-cluster workload');
  clickRow('workload-rows', 'synthetic-mgmt-1');
  check(!document.querySelector('.detail'), 'details remain lazy at deepest path');
  $('workload-rows').querySelector('summary').click(); await tick();
  check($('workload-rows').textContent.includes('audit-sidecar'), 'container aggregates retained');
  check($('workload-rows').textContent.includes('Unscheduled (pending demand)'), 'pending demand visible');
  check(document.querySelector('.detail').textContent.includes('GiB') && document.querySelector('.detail').textContent.includes('cores'), 'details contain both resources');
  $('workload-rows').querySelector('summary').click(); await tick();
  check(!document.querySelector('.detail'), 'collapse disposes container DOM');
  $('workload-path').firstChild.click();
  check(currentGroups.length === 2 && !document.querySelector('.detail'), 'Back restores cluster workload level');
  for (const resource of resources) {
    const key = resource === 'cpu' ? 'unlimitedCPU' : 'unlimitedMemory';
    check(unlimited([{containers: [{[key]: null}]}], resource) === 'Unknown', 'failed limit query is not zero unlimited');
    check(unlimited([{containers: [{[key]: 0}]}], resource) === '0', 'confirmed finite has zero unlimited');
    check(unlimited([{containers: [{[key]: 2}]}], resource) === '2', 'confirmed unlimited count retained');
    check(unlimited([{containers: [{[key]: 2}, {[key]: null}]}], resource).includes('Unknown'), 'mixed count coverage explicit');
  }
  const unlimitedGroup = currentGroups.find(g => g.name === 'synthetic-mgmt-2');
  check(unlimitedGroup.cpu.limits.value === 0 && unlimited(unlimitedGroup.rows, 'cpu') === '3', 'zero finite sum and positive unlimited count retained');
  navigateWorkloads(['mgmt']); clickRow('workload-rows', 'kube-system / DaemonSet / node-agent'); clickRow('workload-rows', 'synthetic-mgmt-2');
  $('workload-rows').querySelector('summary').click(); await tick();
  check($('workload-rows').firstChild.lastElementChild.textContent === 'Unknown', 'unknown unlimited group cell');
  check(document.querySelector('.detail tbody tr').lastElementChild.textContent === 'Unknown', 'unknown unlimited container cell');
  navigateWorkloads([]);
  startCharts();
  check(area(capacityCharts.cpu) === 88, 'fleet physical CPU capacity');
  check(area(capacityCharts.memory) === 608 * 1073741824, 'fleet physical memory capacity');
  check(capacityCharts.cpu.getOption().series[0].data.length === 3, 'capacity root contains cluster tiles only');
  const mgmt = capacityGroups.find(g => g.name === 'synthetic-mgmt-1').cpu;
  check(mgmt.used === 11.2 && Math.abs(mgmt.unused - 4.8) < 1e-9 && mgmt.unknown === 16, 'aggregate unknown remainder is never unused');
  check($('coverage-cpu').textContent.includes('1 nodes excluded'), 'missing capacity excluded with visible coverage');
  check(capacityCharts.cpu.getOption().series[0].data.flatMap(g => g.children).some(n => n.name.startsWith('Unknown usage') && n.value === 16 && n.itemStyle.color === '#99752d'), 'unknown chart slice distinct from unused');
  check(capacityCharts.cpu.getOption().series[0].data.flatMap(g => g.children).some(n => n.name.startsWith('Known unused') && n.itemStyle.color === '#303944'), 'unused slice muted');
  for (const resource of resources) verifyCapacityPixels(capacityCharts[resource]);
  // Walk every cluster and pool using DOM controls, conserving both resources at each edge.
  const roots = [...capacityGroups];
  function checkScope(expected) {
    for (const resource of resources) for (const field of ['capacity', 'used', 'unused', 'unknown']) {
      const actual = capacityGroups.reduce((total, group) => total + group[resource][field], 0);
      check(Math.abs(actual - expected[resource][field]) <= Math.max(1, actual) * 1e-9, 'drill conserves ' + resource + ' ' + field);
    }
  }
  for (const root of roots) {
    clickRow('capacity-rows', root.name);
    checkScope(root);
    for (const resource of resources) check(area(capacityCharts[resource]) === root[resource].capacity, 'cluster drill conserves ' + resource);
    const pools = [...capacityGroups];
    for (const pool of pools) {
      clickRow('capacity-rows', pool.name);
      checkScope(pool);
      for (const resource of resources) { check(area(capacityCharts[resource]) === pool[resource].capacity, 'pool drill conserves ' + resource); verifyCapacityPixels(capacityCharts[resource]); }
      $('capacity-path').firstChild.click();
    }
    $('capacity-path').firstChild.click();
  }
  check(area(capacityCharts.cpu) === 88 && !capacityPath.length, 'Back restores fleet totals');
  // Real ECharts event handlers and table controls use the same shared path.
  clickChart(capacityCharts.memory, n => n.path[0] === 'synthetic-mgmt-1', true);
  check(capacityPath.join('/') === 'synthetic-mgmt-1' && capacityCharts.cpu.getOption().series[0].data[0].name === 'hcp', 'memory click synchronizes CPU hierarchy');
  clickChart(capacityCharts.cpu, () => true, true);
  clickChart(capacityCharts.memory, n => n.path[2] === 'mgmt1-hcp-02', true);
  check(selectedNode === nodeKey('synthetic-mgmt-1', 'mgmt1-hcp-02'), 'node click selects workload placement');
  check(currentWorkloads.length === 1 && currentWorkloads.every(w => w.node === 'mgmt1-hcp-02' && !w.unscheduled), 'node selection excludes other placements and pending');
  $('clear-node').click();
  check(!selectedNode && currentWorkloads.length === 10, 'clear node restores fleet workload scope');
  navigateCapacity([]);
  clickChart(compositionCharts.memory, n => n.groupKey === 'mgmt');
  check(workloadPath[0] === 'mgmt' && compositionCharts.cpu.getOption().series[0].data.some(n => n.name.startsWith('kube-apiserver')), 'role chart click synchronizes both resources');
  clickChart(compositionCharts.cpu, n => n.name.startsWith('kube-apiserver'));
  check(currentGroups.length === 2, 'workload chart click splits by cluster');
  clickChart(compositionCharts.memory, n => n.groupKey === 'synthetic-mgmt-1');
  clickChart(compositionCharts.cpu, () => true); await tick();
  check(document.querySelectorAll('.detail').length === 4, 'deepest chart click opens placement details');
  navigateWorkloads([]);
  if (window.cachedECharts) {
    const node = report.snapshots[0].nodes[1], saved = node.usage.cpu;
    node.usage.cpu = 0.00001; navigateCapacity([node.cluster, node.pool]);
    verifyCapacityPixels(capacityCharts.cpu);
    const tree = capacityCharts.cpu.getModel().getSeriesByIndex(0).getData().tree;
    const target = tree.root.children[0], tiny = target.children.find(n => n.name.startsWith('Known used'));
    const pixels = n => n.getLayout().width * n.getLayout().height;
    check(pixels(tiny) > 0 && !tiny.getLayout().invisible, 'tiny positive usage must not be pruned');
    check(Math.abs(pixels(tiny) / pixels(target) - 0.00001 / 16) < 1e-10, 'tiny usage exact ratio');
    node.usage.cpu = saved; navigateCapacity([]);
  }
  $('search').value = 'no-match'; renderWorkloads();
  check(area(capacityCharts.cpu) === 88, 'search does not change capacity');
  check(cardUtilization('synthetic-svc-1', 'cpu') === '44.2%', 'workload filters do not alter cluster summaries');
  check($('workload-rows').textContent.includes('No workload records'), 'empty filter result explicit');
  $('search').value = ''; renderWorkloads();
  const sort = document.querySelector('[data-sort="cpu.usage"]'); sort.click(); sort.click();
  check(sort.parentElement.getAttribute('aria-sort') === 'descending', 'sortable workload table');
  const left = $('capacity-chart-cpu').getBoundingClientRect(), right = $('capacity-chart-memory').getBoundingClientRect();
  check(window.innerWidth <= 800 ? right.top > left.top : right.top === left.top && right.left > left.left, 'responsive paired charts');
  $('snapshot').value = '1'; $('snapshot').onchange();
  check($('cards').textContent.includes('Unknown'), 'empty snapshot is unknown, not zero');
  check(cardUtilization('synthetic-svc-1', 'cpu') === 'Unknown', 'empty snapshot has no utilization percentage');
  check($('warnings').textContent.includes('No node measurements'), 'missing data warning');
  check($('warnings').textContent.includes('Capacity and node usage are Unknown') && $('warnings').textContent.includes('Workload demand is Unknown'), 'empty snapshot explicitly unknown without opening diagnostics');
  check(!$('warnings').textContent.includes('0/0'), 'empty coverage does not imply complete zero demand');
`)
}

func TestRenderUtilizationBrowserCoverage(t *testing.T) {
	report := utilizationFixture(t)
	report.Warnings = []string{
		"synthetic-svc-1 CPU peak: incomplete history at 5 minute(s)",
		"synthetic-mgmt-1 memory peak: incomplete history at 31 minute(s)",
		"synthetic-mgmt-2 query unavailable: usage",
		`</textarea><script>window.injected=true</script><img src=x onerror="window.injected=true">`,
	}
	for i := range 600 {
		report.Snapshots[0].Warnings = append(report.Snapshots[0].Warnings, fmt.Sprintf("namespace-%03d partial workload metrics", i))
	}
	report.Snapshots[0].Workloads[0].Kind = "ReplicaSet"
	report.Snapshots[0].Workloads[1].Kind = "replicaset"
	checkUtilizationBrowser(t, report, `
  const section = title => [...$('warnings').querySelectorAll('.coverage-grid > div')].find(item => item.querySelector('strong').textContent.startsWith(title));
  check($('timeline-summary').textContent === 'Missing timestamps were not saved in this artifact. Cannot distinguish startup/teardown gaps from intermittent loss.', 'old reports never infer gap timing');
  check(!$('coverage-timelines'), 'old reports cannot invent timelines');
  check(section('Node measurements').textContent.includes('4/6'), 'usable node count is distinct from observed nodes');
  check(section('Workload totals are partial').textContent.includes('8/10') && section('Workload totals').textContent.includes('9/10'), 'partial coverage counts are record counts, not consumption percentages');
  check(section('Workload totals').textContent.includes('not the percentage of resource consumption captured'), 'coverage denominator is disclosed');
  check(section('Owner grouping is incomplete').textContent.includes('2 records retain ReplicaSet owners'), 'case-insensitive unresolved ReplicaSet count');
  check(section('Owner grouping').textContent.includes('Their measured values are retained'), 'grouping gaps do not invalidate measurements');
  check(section('Collection errors').textContent.includes('1 query or collection diagnostics'), 'collection failures counted separately');
  check(section('Peak search is incomplete').textContent.includes('not proven maxima'), 'incomplete peak search limits are visible');
  check(!$('warnings').querySelector('table, textarea, li'), 'no hidden coverage table or warning list built initially');
  check($('warnings').querySelectorAll('*').length < 30, 'hundreds of warnings produce bounded initial coverage DOM');
  const disclosures = [...$('warnings').querySelectorAll('details')];
  const peaks = disclosures.find(details => details.querySelector('summary').textContent.startsWith('Peak-search'));
  peaks.querySelector('summary').click(); await tick();
  const rows = [...peaks.querySelectorAll('tr')].slice(1).map(row => [...row.children].map(cell => cell.textContent));
  check(JSON.stringify(rows) === JSON.stringify([['synthetic-svc-1', 'CPU', '26/31', '5'], ['synthetic-mgmt-1', 'memory', '0/31', '31']]), 'eligible minutes use inclusive collection minute bounds and resource-specific exclusions');
  peaks.querySelector('summary').click(); await tick();
  check(!peaks.querySelector('table'), 'closing peak disclosure releases table');
  const diagnostics = disclosures.find(details => details.querySelector('summary').textContent.startsWith('Diagnostics:'));
  check(diagnostics.querySelector('summary').textContent.includes('609 messages'), 'diagnostic count includes saved and derived warnings for both resources');
  diagnostics.querySelector('summary').click(); await tick();
  const raw = diagnostics.querySelector('textarea');
  check(raw.readOnly && raw.getAttribute('aria-label') === 'Original collection diagnostics', 'raw diagnostics are accessible and readonly');
  check(raw.value.includes(report.warnings[3]) && raw.value.includes('namespace-599 partial workload metrics'), 'every original diagnostic remains available literally');
  check(raw.value.split('\n').length === 609, 'raw diagnostics include every message without truncation');
  check(!window.injected && !document.querySelector('img') && !diagnostics.querySelector('script'), 'raw HTML-like diagnostics never execute or create elements');
  check(diagnostics.querySelectorAll('li').length === 1 && diagnostics.textContent.includes('600 messages'), 'repeated diagnostics aggregate into one category instead of hundreds of rows');
  check($('warnings').querySelectorAll('*').length < 40, 'expanded diagnostic DOM stays bounded');
  diagnostics.querySelector('summary').click(); await tick();
  check(!diagnostics.querySelector('textarea, li'), 'diagnostic collapse releases detail DOM');
  check(section('Node measurements').textContent.includes('4/6 observed nodes have usable memory'), 'coverage includes memory alongside CPU');
  const workloads = snapshot().workloads;
  workloads.forEach(w => { w.usage.memory = 0; });
  render();
  check(section('Workload totals are partial').textContent.includes('10/10 observed workload records have memory usage; 9/10 have requests'), 'missing requests alone still mark totals partial, measured zero is known');
  report.snapshots = []; render();
  check($('warnings').textContent.includes('No snapshots were collected. Measurements are Unknown.'), 'no snapshots explicitly unknown');
  check(section('Node measurements').textContent.includes('3 expected clusters have no node records'), 'missing expected clusters disclosed');
  check(section('Node measurements').textContent.includes('node usage are Unknown') && section('Workload totals').textContent.includes('demand is Unknown'), 'empty record sets are not zero measurements');
  check(!section('Owner grouping'), 'no invented owner gaps for empty data');
`)
}

func TestRenderUtilizationBrowserIdentities(t *testing.T) {
	report := utilizationFixture(t)
	checkUtilizationBrowser(t, report, `
  const workloads = snapshot().workloads;
  const platform = {...workloads[0], cluster: 'ci01-j1862272-svc', component: 'shared', namespace: 'platform-a', kind: 'Deployment', name: 'shared'};
  workloads.push(platform, {...platform, cluster: 'synthetic-svc-1'}, {...platform, namespace: 'platform-b'}, {...platform, kind: 'DaemonSet'}, {...platform, cluster: 'unrecognized'});
  const hcp = {...workloads[2], kind: 'ReplicaSet', name: 'api-123abc', component: 'ReplicaSet/api-123abc'};
  workloads.push(hcp, {...hcp, name: 'api-456def', component: 'ReplicaSet/api-456def'});
  const saved = JSON.stringify(snapshot());
  render(); startCharts();
  check(currentGroups.map(g => g.name).sort().join(',') === 'Other,mgmt,svc', 'unknown role has explicit Other category');
  check(compositionCharts.cpu.getOption().series[0].data.some(g => g.groupKey === 'Other'), 'Other is visible in chart, not omitted');
  navigateWorkloads(['svc']);
  const group = currentGroups.find(g => g.name === 'platform-a / Deployment / shared');
  check(group.rows.length === 2 && group.cpu.usage.value === 8.2, 'platform exact owner aggregates across service clusters');
  check(currentGroups.some(g => g.name === 'platform-b / Deployment / shared') && currentGroups.some(g => g.name === 'platform-a / DaemonSet / shared'), 'platform namespace and kind cannot be collapsed by component');
  navigateWorkloads(['mgmt']);
  check(currentGroups.some(g => g.name === 'ReplicaSet/api-123abc') && currentGroups.some(g => g.name === 'ReplicaSet/api-456def'), 'legacy ReplicaSets stay fragmented, no hash stripping or invented parent');
  check($('warnings').textContent.includes('Saved data cannot reconstruct missing parent mappings'), 'fragmented owner warning preserved');
  check(JSON.stringify(snapshot()) === saved, 'navigation and grouping never modify saved data');
  const node = snapshot().nodes[0];
  const capacity = node.capacity.cpu, usage = node.usage.cpu;
  node.usage.cpu = capacity + 1;
  navigateCapacity([node.cluster, node.pool]);
  check(capacityGroups[0].cpu.used === capacity && capacityGroups[0].cpu.unused === 0 && capacityGroups[0].cpu.unknown === 0, 'overflow caps area, never makes negative unused');
  check($('coverage-cpu').textContent.includes('exceed capacity'), 'overflow clearly disclosed');
  $('node-details').querySelector('summary').click(); await tick();
  check($('nodes').textContent.includes('9 cores') && $('nodes').textContent.includes('Inconsistent: usage > capacity'), 'exact overflow value remains accessible');
  node.usage.cpu = 0; renderCapacity();
  check(capacityGroups[0].cpu.used === 0 && capacityGroups[0].cpu.unused === capacity, 'measured zero is genuinely unused capacity');
  node.usage.cpu = null; renderCapacity();
  check(capacityGroups[0].cpu.unused === 0 && capacityGroups[0].cpu.unknown === capacity, 'missing usage is all unknown, not unused');
  node.capacity.cpu = null; renderCapacity();
  check(area(capacityCharts.cpu) === 0 && area(capacityCharts.memory) > 0, 'CPU missing capacity does not suppress known memory capacity');
  check($('capacity-total-cpu').textContent.startsWith('Unknown') && $('capacity-rows').children.length === 1, 'unsized scope retained in navigation table');
  node.capacity.cpu = capacity; node.usage.cpu = usage;
`)
}

func TestRenderUtilizationBrowserLarge(t *testing.T) {
	report := utilizationFixture(t)
	node, workload := report.Snapshots[0].Nodes[0], report.Snapshots[0].Workloads[0]
	report.Snapshots = report.Snapshots[:1]
	report.Snapshots[0].Nodes = nil
	report.Snapshots[0].Workloads = nil
	for i := range 251 {
		n := node
		n.Name = fmt.Sprintf("node-%03d", i)
		report.Snapshots[0].Nodes = append(report.Snapshots[0].Nodes, n)
	}
	for group := range 80 {
		for placement := range 63 {
			w := workload
			w.Component = fmt.Sprintf("component-%03d", group)
			w.Namespace = fmt.Sprintf("ocm-namespace-%03d", placement)
			w.Node = fmt.Sprintf("node-%03d", placement)
			report.Snapshots[0].Workloads = append(report.Snapshots[0].Workloads, w)
		}
	}
	checkUtilizationBrowser(t, report, `
  check(currentGroups.length === 1 && currentWorkloads.length === 5040, 'root aggregates all workloads by role');
  $('workload-rows').querySelector('button').click();
  check(currentGroups.length === 80 && currentWorkloads.length === 5040, 'role drill includes every workload');
  check($('workload-rows').children.length === 25, 'top-level DOM is paginated');
  check(!document.querySelector('.detail') && !$('nodes').children.length, 'initial tables are lazy');
  check(document.querySelectorAll('*').length < 1000, 'initial DOM stays bounded');
  startCharts();
  const counts = {capacity: 0, composition: 0};
  for (const [kind, charts] of [['capacity', capacityCharts], ['composition', compositionCharts]]) for (const chart of Object.values(charts)) {
    const original = chart.setOption.bind(chart); chart.setOption = (...args) => { counts[kind]++; return original(...args); };
  }
  const cards = $('cards').firstChild, warnings = $('warnings').firstChild;
  const chartData = (resource = 'cpu') => compositionCharts[resource].getOption().series[0].data;
  check(chartData().length === 80 && chartData().every(group => !group.children), 'composition uses aggregate leaves, not placements');
  const expectedArea = sum(currentWorkloads, 'usage', 'cpu').value;
  check(Math.abs(chartData().reduce((n, group) => n + group.value, 0) - expectedArea) < 1e-8, 'composition aggregate area is exact');
  $('workload-rows').querySelector('button').click();
  $('workload-rows').querySelector('button').click();
  check(!document.querySelector('.detail'), 'cluster workload drill does not eagerly build details');
  $('workload-rows').querySelector('summary').click(); await tick();
  check(document.querySelectorAll('.detail').length === 20, 'opening group renders only one detail page');
  check(document.querySelector('.detail tbody tr'), 'actual container rows built on open');
  const details = $('workload-rows').querySelector('details');
  details.querySelector('[aria-label="Next workload records"]').click();
  check(details.querySelector('.detail').textContent.includes('namespace-020'), 'detail next page reaches correct placement');
  details.querySelector('[aria-label="Next workload records"]').click();
  details.querySelector('[aria-label="Next workload records"]').click();
  check(document.querySelectorAll('.detail').length === 3 && details.textContent.includes('namespace-062'), 'last placement accessible');
  $('workload-path').firstChild.click(); $('workload-path').firstChild.click();
  check(!document.querySelector('.detail') && !$('workload-rows').querySelector('details'), 'Back disposes details and restores workload level');
  counts.capacity = counts.composition = 0;
  $('workload-pages').querySelector('[aria-label="Next groups"]').click();
  check($('workload-rows').firstChild.textContent.includes('component-025') && !document.querySelector('.detail'), 'group pagination disposes details');
  $('workload-pages').querySelector('[aria-label="Next groups"]').click();
  $('workload-pages').querySelector('[aria-label="Next groups"]').click();
  check($('workload-rows').children.length === 5 && $('workload-rows').textContent.includes('component-079'), 'last group accessible');
  $('node-details').querySelector('summary').click(); await tick();
  check($('nodes').children.length === 25, 'node page is bounded');
  for (let i = 0; i < 10; i++) $('node-pages').querySelector('[aria-label="Next nodes"]').click();
  check($('nodes').children.length === 1 && $('nodes').textContent.includes('node-250'), 'last node accessible');
  $('node-details').querySelector('summary').click(); await tick();
  check(!$('nodes').children.length, 'closing nodes releases DOM');
  document.querySelector('[data-sort="name"]').click();
  check($('workload-rows').firstChild.textContent.includes('component-079'), 'sorting applies across pages');
  check(counts.capacity === 0 && counts.composition === 0, 'pagination and sorting never redraw charts');
  clickChart(compositionCharts.memory, group => group.name.includes('component-007'));
  check(workloadPath[1].includes('component-007') && currentGroups.length === 1, 'chart drill finds correct group after sorting and across pages');
  clickChart(compositionCharts.cpu, () => true);
  clickChart(compositionCharts.memory, () => true);
  await tick();
  const opened = document.querySelector('#workload-rows details[open]');
  check(opened && workloadPath[1].includes('component-007'), 'chart selection opens correct workload details');
  check(document.querySelectorAll('.detail').length === 20, 'chart selection uses bounded lazy details');
  $('workload-path').firstChild.click(); $('workload-path').firstChild.click();
  counts.capacity = counts.composition = 0;
  $('search').value = 'component-007'; $('search').oninput();
  $('search').value = 'component-079'; $('search').oninput();
  check(currentGroups.length === 80, 'search is debounced');
  await new Promise(resolve => setTimeout(resolve, 160));
  check(currentGroups.length === 1 && currentGroups[0].name === 'component-079', 'debounced filter uses latest input');
  check(counts.capacity === 0 && counts.composition === 2, 'search only redraws each composition once');
  $('basis').value = 'requests'; $('basis').onchange();
  check(counts.capacity === 0 && counts.composition === 4, 'basis never redraws capacity');
  check($('cards').firstChild === cards && $('warnings').firstChild === warnings, 'workload interactions preserve static snapshot DOM');
  check(Math.abs(chartData()[0].value - sum(currentWorkloads, 'requests', 'cpu').value) < 1e-8, 'filtered CPU request area preserved');
  check(chartData('memory')[0].value === sum(currentWorkloads, 'requests', 'memory').value, 'memory composition preserves byte values simultaneously');
  check(document.querySelectorAll('*').length < 1000, 'filter resets to bounded collapsed DOM');
  $('capacity-rows').querySelector('button').click(); $('capacity-rows').querySelector('button').click();
  check(capacityGroups.length === 251 && $('capacity-rows').children.length === 25, 'large capacity level keeps table bounded without pruning chart nodes');
  const capacityArea = area(capacityCharts.cpu);
  counts.capacity = counts.composition = 0;
  for (let i = 0; i < 10; i++) $('capacity-pages').querySelector('[aria-label="Next capacity groups"]').click();
  check($('capacity-rows').children.length === 1 && $('capacity-rows').textContent.includes('node-250'), 'last capacity navigation row accessible');
  check(area(capacityCharts.cpu) === capacityArea && counts.capacity === 0 && counts.composition === 0, 'capacity pagination changes neither areas nor charts');
`)
}

// Optional local replay uses the same assertions and browser harness as fixtures.
func TestRenderUtilizationBrowserReplay(t *testing.T) {
	input := os.Getenv("UTILIZATION_REPORT_JSON")
	if input == "" {
		t.Skip("set UTILIZATION_REPORT_JSON to exercise a collected report offline")
	}
	data, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	var report utilizationReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	checkUtilizationBrowser(t, report, `
  startCharts();
  for (let i = 0; i < report.snapshots.length; i++) {
    $('snapshot').value = String(i); $('snapshot').onchange(); await tick();
    for (const resource of ['cpu', 'memory']) {
      check(currentWorkloads.length === report.snapshots[i].workloads.length, 'all real workloads retained');
      check($('workload-rows').children.length <= 25 && !document.querySelector('.detail'), 'real report has bounded initial DOM');
      check(document.querySelectorAll('*').length < 1500, 'real report initial DOM is small');
      if (window.echarts) {
        const data = compositionCharts[resource].getOption().series[0].data;
        check(data.every(group => !group.children), 'real chart omits placement children');
        const expected = sum(currentWorkloads, 'usage', resource).value;
        check(Math.abs(data.reduce((n, group) => n + group.value, 0) / expected - 1) < 1e-9, 'real composition area is preserved');
        verifyCapacityPixels(capacityCharts[resource]);
      }
    }
    check(currentGroups.map(g => g.name).join(',') === 'mgmt,svc', 'real cluster roles recognized at suffix');
    check(currentGroups.find(g => g.name === 'mgmt').rows.some(w => /mgmt-1$/.test(w.cluster)) && currentGroups.find(g => g.name === 'mgmt').rows.some(w => /mgmt-2$/.test(w.cluster)), 'both real management clusters are combined');
    for (let level = 0; level < 3; level++) { $('workload-rows').querySelector('button').click(); await tick(); }
    const details = $('workload-rows').querySelector('details');
    details.querySelector('summary').click(); await tick();
    check(document.querySelectorAll('.detail').length <= 20 && details.querySelector('.detail'), 'real details are lazy and bounded');
    $('workload-path').children[1].click(); await tick();
    check(!document.querySelector('.detail') && !workloadPath.length, 'Fleet breadcrumb resets workload path');
  }
`)
}

func checkUtilizationBrowser(t *testing.T, report utilizationReport, assertions string) {
	t.Helper()
	browser, err := exec.LookPath("google-chrome")
	if err != nil {
		t.Skip("google-chrome unavailable; skipping optional browser smoke test")
	}
	html, err := renderUtilizationHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(html)
	text = text[:strings.Index(text, "<script defer src=")]
	if cached := os.Getenv("UTILIZATION_ECHARTS_JS"); cached != "" {
		t.Logf("checking real ECharts layout using %s", cached)
		library, err := os.ReadFile(cached)
		if err != nil {
			t.Fatal(err)
		}
		text += "<script>" + string(library) + "\nwindow.cachedECharts = window.echarts; delete window.echarts;</script>"
	}
	text += `<script>
(async () => { try {
  function check(condition, message) { if (!condition) throw new Error(message); }
  const tick = () => new Promise(resolve => setTimeout(resolve, 10));
  function startCharts() {
    window.echarts = window.cachedECharts || {init: () => {
      let option; const handlers = {};
      return {setOption(value) { option = value; }, getOption() { return option; }, on(name, handler) { handlers[name] = handler; }, off(name) { delete handlers[name]; }, trigger(name, event) { handlers[name]?.(event); }, resize() {}};
    }};
    drawCharts();
    // These charts disable animation. Flush paints explicitly so ECharts' idle
    // RAF loop cannot starve Chrome --dump-dom's virtual-time timer on large data.
    if (window.cachedECharts) for (const chart of [...Object.values(capacityCharts), ...Object.values(compositionCharts)]) {
      const setOption = chart.setOption.bind(chart);
      chart.setOption = (...args) => { setOption(...args); chart.getZr().flush(); chart.getZr().animation.stop(); };
      chart.getZr().flush(); chart.getZr().animation.stop();
    }
  }
  function area(chart) { return chart.getOption().series[0].data.reduce((total, node) => total + node.value, 0); }
  function clickChart(chart, predicate, child = false) {
    const tile = chart.getOption().series[0].data.find(predicate);
    check(tile, 'chart click target exists');
    chart.trigger('click', {data: child ? tile.children[0] : tile});
  }
  function verifyCapacityPixels(chart) {
    const data = chart.getOption().series[0].data;
    for (const group of data) {
      check(group.children.every(child => !child.children), 'only current capacity level and accounting slices are built');
      check(Math.abs(group.children.reduce((total, child) => total + child.value, 0) / group.value - 1) < 1e-9, 'capacity slices conserve aggregate total');
    }
    if (!window.cachedECharts || !data.length) return;
    const tree = chart.getModel().getSeriesByIndex(0).getData().tree, nodes = [];
    tree.eachNode(node => { if (node.depth === 1) nodes.push(node); });
    const pixels = node => node.getLayout().width * node.getLayout().height;
    const unit = pixels(nodes[0]) / nodes[0].getValue();
    for (const node of nodes) {
      check(Math.abs(pixels(node) / node.getValue() / unit - 1) < 1e-9, 'aggregate capacities preserve exact pixel ratios');
      check(Math.abs(node.children.reduce((total, child) => total + pixels(child), 0) / pixels(node) - 1) < 1e-9, 'no area consumed by padding borders or labels');
      for (const child of node.children) check(Math.abs(pixels(child) / pixels(node) - child.getValue() / node.getValue()) < 1e-9, 'used unused and unknown pixel ratios exact');
    }
  }
` + assertions + `
  const result = document.createElement('output'); result.id = 'browser-result'; result.textContent = 'PASS'; document.body.append(result);
} catch (error) {
  const result = document.createElement('output'); result.id = 'browser-result'; result.textContent = 'FAIL: ' + error.message; document.body.append(result);
} })();
</script></body></html>`
	dir := t.TempDir()
	file := filepath.Join(dir, "report.html")
	if err := os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	for _, size := range []string{"1440,1000", "390,844"} {
		t.Run(size, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, browser, "--headless", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage", "--disable-background-networking", "--no-first-run", "--virtual-time-budget=5000", "--window-size="+size, "--user-data-dir="+filepath.Join(dir, "chrome-"+size), "--dump-dom", "file://"+file)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			dom, err := cmd.Output()
			if err != nil {
				t.Fatalf("browser smoke test: %v\n%s", err, stderr.String())
			}
			if !strings.Contains(string(dom), `<output id="browser-result">PASS</output>`) {
				_, result, _ := strings.Cut(string(dom), `<output id="browser-result">`)
				result, _, _ = strings.Cut(result, "</output>")
				t.Fatalf("browser assertions failed: %s", result)
			}
		})
	}
}
