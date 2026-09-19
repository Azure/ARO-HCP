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
	for _, want := range []string{"<!DOCTYPE html>", `name="viewport"`, `id="capacity-chart"`, `id="composition-chart"`, `id="workloads"`, `id="snapshot"`, `\u003c/script\u003e`, `\u0026`, `\u2028`, `\u2029`, "https://go-echarts.github.io/go-echarts-assets/assets/echarts.min.js"} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered utilization missing %q", want)
		}
	}
	for _, forbidden := range []string{attack, "innerHTML", "tailwind", "#ZgotmplZ"} {
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

func TestRenderUtilizationCommand(t *testing.T) {
	report := utilizationFixture(t)
	valid, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, input, want string
	}{
		{"valid", string(valid) + "\n \t", ""},
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
	browser, err := exec.LookPath("google-chrome")
	if err != nil {
		t.Skip("google-chrome unavailable; skipping optional browser smoke test")
	}
	report := utilizationFixture(t)
	report.Warnings = append(report.Warnings, `</script><img src=x onerror="window.injected=true">`)
	html, err := renderUtilizationHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(html)
	start := strings.Index(text, "<script defer src=")
	prefix := text[:start]
	if cached := os.Getenv("UTILIZATION_ECHARTS_JS"); cached != "" {
		t.Logf("checking real ECharts layout using %s", cached)
		library, err := os.ReadFile(cached)
		if err != nil {
			t.Fatal(err)
		}
		prefix += "<script>" + string(library) + "\nwindow.cachedECharts = window.echarts; delete window.echarts;</script>"
	}
	text = prefix + `
<script>
try {
  function check(condition, message) { if (!condition) throw new Error(message); }
  check(!window.echarts, 'test must start without CDN');
  check(!window.injected && !document.querySelector('img'), 'untrusted warning must remain text');
  check($('warnings').textContent.includes('<img src=x'), 'untrusted warning is still readable');
  check(document.documentElement.scrollWidth <= window.innerWidth, 'no page-level horizontal overflow');
  check($('capacity-chart').hidden, 'no empty chart space when CDN unavailable');
  check(document.querySelectorAll('#cards .card').length === 3, 'three separate cluster cards');
  check(document.querySelectorAll('#nodes tr').length === 6, 'all node measurements without CDN');
  check($('snapshot').options[0].textContent.includes('Peak pending demand'), 'labelled snapshot reasons');
  check($('warnings').textContent.includes('unknown usage'), 'prominent unknown warning');
  check($('cards').textContent.includes('partial:'), 'partial totals are not complete totals');
  function cardUtilization(cluster) {
    const card = [...document.querySelectorAll('#cards .card')].find(card => card.querySelector('h3').textContent === cluster);
    return [...card.querySelectorAll('.metric')].find(line => line.querySelector('dt').textContent === 'Whole-node utilization').querySelector('dd').textContent;
  }
  check(cardUtilization('synthetic-svc-1') === '44.2%', 'cluster CPU percentage is weighted by physical capacity');
  check(cardUtilization('synthetic-mgmt-1').startsWith('Unknown'), 'missing usage prevents cluster percentage');
  check(cardUtilization('synthetic-mgmt-2').startsWith('Unknown'), 'missing capacity prevents cluster percentage');
  check(nodeUtilization([]) === 'Unknown', 'empty nodes cannot imply zero utilization');
  check(nodeUtilization([{capacity: {cpu: 0}, usage: {cpu: 0}}]).startsWith('Unknown'), 'zero capacity has no percentage');
  check(nodeUtilization([{capacity: {cpu: 8}, usage: {cpu: 0}}]) === '0%', 'measured zero usage remains zero percent');
  check($('nodes').children[0].children[4].textContent === '2.4 cores (30%)', 'node usage includes percent');
  check(!$('nodes').children[3].children[4].textContent.includes('%'), 'unknown node usage has no percentage');
  check($('workload-rows').textContent.includes('3 namespaces / 3 owners / 3 placements'), 'component grouping and node attribution');
  check($('workload-rows').textContent.includes('audit-sidecar'), 'container aggregates retained');
  check($('workload-rows').textContent.includes('Unscheduled (pending demand)'), 'pending demand visible');
  check(unlimited([{containers: [{unlimitedCPU: null}]}]) === 'Unknown', 'failed limit query is not zero unlimited');
  check(unlimited([{containers: [{unlimitedCPU: 0}]}]) === '0', 'confirmed finite has zero unlimited');
  check(unlimited([{containers: [{unlimitedCPU: 2}]}]) === '2', 'confirmed unlimited count retained');
  check(unlimited([{containers: [{unlimitedCPU: 2}, {unlimitedCPU: null}]}]).includes('Unknown'), 'mixed count coverage explicit');
  const unknownRow = [...$('workload-rows').children].find(row => row.textContent.includes('node-agent'));
  check(unknownRow.lastElementChild.textContent === 'Unknown', 'unknown unlimited group cell');
  check(unknownRow.querySelector('.detail tbody tr').lastElementChild.textContent === 'Unknown', 'unknown unlimited container cell');
  const unlimitedGroup = currentGroups.find(group => group.cluster === 'synthetic-mgmt-2' && group.name === 'kube-apiserver');
  check(unlimitedGroup.limits.value === 0 && unlimited(unlimitedGroup.rows) === '3', 'confirmed unlimited retains zero finite sum and positive count');
  const chartOptions = {};
  window.echarts = window.cachedECharts || {init: element => ({setOption: option => {chartOptions[element.id] = option;}, on() {}, off() {}, resize() {}})};
  drawCharts();
  function leaves(data) { return data.flatMap(node => node.children ? leaves(node.children) : [node]); }
  function capacityData() { return (window.cachedECharts ? capacityChart.getOption() : chartOptions['capacity-chart']).series[0].data; }
  function area() { return leaves(capacityData()).reduce((total, node) => total + node.value, 0); }
  check(area() === 88, 'physical capacity sizes');
  check(leaves(capacityData()).some(node => node.name === 'Unknown usage (not free)' && node.value === 16), 'unknown tile is not unused');
  if (window.cachedECharts) {
    const nodes = report.snapshots[0].nodes;
    const saved = nodes[1].usage.cpu;
    nodes[1].usage.cpu = 0.00001;
    render();
    const tree = capacityChart.getModel().getSeriesByIndex(0).getData().tree;
    const layouts = [];
    tree.eachNode(node => { if (node.depth === 3) layouts.push(node); });
    const pixels = node => node.getLayout().width * node.getLayout().height;
    const equal = layouts.filter(node => node.getValue() === 16);
    check(equal.length === 3, 'equal nodes span different clusters and pools');
    check(equal.every(node => Math.abs(pixels(node) / pixels(equal[0]) - 1) < 1e-9), 'equal capacities must have equal pixel area across hierarchy');
    const target = layouts.find(node => node.name.includes('svc-services-01'));
    const tiny = target.children.find(node => node.name.startsWith('Used CPU'));
    const layout = tiny.getLayout();
    check(layout && !layout.invisible && layout.isInView !== false && pixels(tiny) > 0, 'tiny positive usage must not be pruned');
    check(Math.abs(pixels(tiny) / pixels(target) - 0.00001 / 16) < 1e-10, 'tiny usage area must preserve ratio');
    check(Math.abs(target.children.reduce((total, child) => total + pixels(child), 0) / pixels(target) - 1) < 1e-9, 'labels borders and gaps must not consume area');
    nodes[1].usage.cpu = saved; render();
  }
  $('cluster').value = 'synthetic-mgmt-1'; $('cluster').onchange();
  $('pool').value = 'hcp'; $('pool').onchange();
  check(area() === 88, 'cluster and pool filters must not resize physical capacity');
  chooseNode(nodeKey('synthetic-mgmt-1', 'mgmt1-hcp-02'));
  check($('workload-rows').textContent.includes('mgmt1-hcp-02'), 'node selection retains matching placement');
  check(!$('workload-rows').textContent.includes('mgmt1-hcp-01'), 'node selection excludes other placements');
  check(!$('workload-rows').textContent.includes('Unscheduled (pending demand)'), 'pending is not assigned to node');
  $('search').value = 'no-match'; render();
  check(area() === 88, 'search must not resize capacity');
  check(cardUtilization('synthetic-svc-1') === '44.2%', 'filters must not change whole-cluster utilization');
  check($('workload-rows').textContent.includes('No workload records'), 'empty filter result is explicit');
  $('search').value = ''; $('cluster').value = ''; $('cluster').onchange();
  const sort = document.querySelector('[data-sort="usage"]'); sort.click(); sort.click();
  check(sort.parentElement.getAttribute('aria-sort') === 'descending', 'sortable workload table');
  $('resource').value = 'memory'; $('resource').onchange();
  check($('unused-label').textContent === 'Available memory', 'memory availability label');
  check($('nodes').textContent.includes('32 GiB'), 'memory units');
  check(cardUtilization('synthetic-svc-1') === '46.9%', 'memory percentage uses memory capacity and usage');
  check($('nodes').children[0].children[4].textContent === '14 GiB (43.8%)', 'node memory percentage');
  check(area() === 608 * 1073741824, 'memory physical capacity area');
  $('snapshot').value = '1'; $('snapshot').onchange();
  check($('cards').textContent.includes('Unknown'), 'empty snapshot is unknown, not zero');
  check(cardUtilization('synthetic-svc-1') === 'Unknown', 'empty snapshot has no utilization percentage');
  check($('warnings').textContent.includes('No node measurements'), 'missing data warning');
  const result = document.createElement('output'); result.id = 'browser-result'; result.textContent = 'PASS'; document.body.append(result);
} catch (error) {
  const result = document.createElement('output'); result.id = 'browser-result'; result.textContent = 'FAIL: ' + error.message; document.body.append(result);
}
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
			cmd := exec.CommandContext(ctx, browser, "--headless", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage", "--no-first-run", "--window-size="+size, "--user-data-dir="+filepath.Join(dir, "chrome-"+size), "--dump-dom", "file://"+file)
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
