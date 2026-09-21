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

func resourceHistoryFixture(t *testing.T) utilizationReport {
	t.Helper()
	data, err := os.ReadFile("testdata/utilization-history-synthetic.json")
	if err != nil {
		t.Fatal(err)
	}
	var report utilizationReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	return report
}

func TestRenderResourceHistoryHTML(t *testing.T) {
	report := resourceHistoryFixture(t)
	attack := "</script><img src=x onerror=alert(1)>&\u2028\u2029"
	report.Warnings = append(report.Warnings, attack)
	report.History[0].Warnings = append(report.History[0].Warnings, attack)
	report.History[0].Nodes[0].Name = attack
	report.History[0].Nodes[0].Pool = attack
	report.History[0].Nodes[0].SKU = attack
	html, err := renderResourceHistoryHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(html)
	for _, want := range []string{"<!DOCTYPE html>", `name="viewport"`, `id="chart-cpu"`, `id="chart-memory"`, `id="chart-swiftNIC"`, `id="samples"`, `\u003c/script\u003e`, `\u0026`, `\u2028`, `\u2029`, "https://go-echarts.github.io/go-echarts-assets/assets/echarts.min.js"} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered history missing %q", want)
		}
	}
	for _, forbidden := range []string{attack, "innerHTML", "#ZgotmplZ"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("unsafe history output: %q", forbidden)
		}
	}
	const marker = `<script id="resource-history-data" type="application/json">`
	if strings.Count(text, marker) != 1 || strings.Count(text, `"schemaVersion"`) != 1 {
		t.Fatal("report must be embedded exactly once")
	}
	_, embedded, _ := strings.Cut(text, marker)
	embedded, _, _ = strings.Cut(embedded, "</script>")
	var decoded utilizationReport
	if err := json.Unmarshal([]byte(embedded), &decoded); err != nil {
		t.Fatal(err)
	}
	report.Snapshots = nil
	report.Coverage = nil
	if !reflect.DeepEqual(report, decoded) {
		t.Fatal("embedded JSON changed nullable measurements or untrusted strings")
	}
}

func TestRenderResourceHistoryValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*utilizationReport)
		want string
	}{
		{"schema", func(r *utilizationReport) { r.SchemaVersion++ }, "schemaVersion"},
		{"generated", func(r *utilizationReport) { r.GeneratedAt = time.Time{} }, "timestamps"},
		{"reversed", func(r *utilizationReport) { r.End = r.Start.Add(-time.Minute) }, "start <= end"},
		{"step", func(r *utilizationReport) { r.Step = "5m" }, "step"},
		{"empty step", func(r *utilizationReport) { r.Step = "" }, "step"},
		{"duplicate cluster", func(r *utilizationReport) { r.Clusters = append(r.Clusters, r.Clusters[0]) }, "unique nonempty"},
		{"blank cluster", func(r *utilizationReport) { r.Clusters[0] = " " }, "unique nonempty"},
		{"duplicate expected", func(r *utilizationReport) {
			r.History[0].Expected = append(r.History[0].Expected, r.History[0].Expected[0])
		}, "expected clusters"},
		{"unknown expected", func(r *utilizationReport) { r.History[0].Expected[0] = "other" }, "expected clusters"},
		{"unknown node cluster", func(r *utilizationReport) { r.History[0].Nodes[0].Cluster = "other" }, "identities"},
		{"blank node", func(r *utilizationReport) { r.History[0].Nodes[0].Name = " " }, "identities"},
		{"duplicate node", func(r *utilizationReport) { r.History[0].Nodes = append(r.History[0].Nodes, r.History[0].Nodes[0]) }, "identities"},
		{"non UTC", func(r *utilizationReport) { r.History[0].Time = r.Start.In(time.FixedZone("offset", 3600)) }, "UTC-minute"},
		{"seconds", func(r *utilizationReport) { r.History[0].Time = r.Start.Add(time.Second) }, "UTC-minute"},
		{"nanoseconds", func(r *utilizationReport) { r.History[0].Time = r.Start.Add(time.Nanosecond) }, "UTC-minute"},
		{"zero time", func(r *utilizationReport) { r.History[0].Time = time.Time{} }, "UTC-minute"},
		{"early", func(r *utilizationReport) { r.History[0].Time = r.Start.Add(-time.Minute) }, "report grid"},
		{"late", func(r *utilizationReport) { r.History[4].Time = r.End.Add(time.Minute) }, "report grid"},
		{"missing first", func(r *utilizationReport) { r.History = r.History[1:] }, "contiguous"},
		{"missing last", func(r *utilizationReport) { r.History = r.History[:4] }, "cover the evaluated"},
		{"missing interior", func(r *utilizationReport) { r.History = append(r.History[:2], r.History[3:]...) }, "contiguous"},
		{"duplicate minute", func(r *utilizationReport) { r.History[1].Time = r.History[0].Time }, "contiguous"},
		{"unordered", func(r *utilizationReport) { r.History[1], r.History[2] = r.History[2], r.History[1] }, "ordered"},
		{"negative cpu", func(r *utilizationReport) { *r.History[0].Nodes[0].Capacity.CPU = -1 }, "nonnegative"},
		{"infinite memory", func(r *utilizationReport) { *r.History[0].Nodes[0].Allocatable.Memory = math.Inf(1) }, "finite"},
		{"NaN usage", func(r *utilizationReport) { *r.History[0].Nodes[0].Usage.CPU = math.NaN() }, "finite"},
		{"negative requests", func(r *utilizationReport) { *r.History[0].Nodes[1].Requests.SwiftNIC = -1 }, "nonnegative"},
		{"NIC usage", func(r *utilizationReport) {
			r.History[0].Nodes[1].Usage.SwiftNIC = r.History[0].Nodes[1].Capacity.SwiftNIC
		}, "no usage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := resourceHistoryFixture(t)
			tc.edit(&r)
			if _, err := renderResourceHistoryHTML(r); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
		})
	}
	r := resourceHistoryFixture(t)
	r.Start, r.End = r.Start.Add(-30*time.Second), r.End.Add(30*time.Second)
	if err := validateUtilizationHistory(r); err != nil {
		t.Fatalf("rounded minute grid must be valid: %v", err)
	}
	r.History[0].Nodes, r.History[0].Expected = nil, nil
	r.History[1].Nodes[0] = utilizationHistoryEntry{Cluster: r.Clusters[0], Name: "unknown"}
	if _, err := renderResourceHistoryHTML(r); err != nil {
		t.Fatalf("empty minutes and unknown values must remain valid gaps: %v", err)
	}
	r.History = nil
	r.Step = ""
	if _, err := renderResourceHistoryHTML(r); err != nil {
		t.Fatalf("legacy reports without history remain valid: %v", err)
	}
}

func TestRenderResourceHistoryBrowser(t *testing.T) {
	r := resourceHistoryFixture(t)
	r.Warnings = append(r.Warnings, `</script><img src=x onerror="window.injected=true">`)
	checkResourceHistoryBrowser(t, r, `
  const fleet = {cluster: '', pool: '', node: ''};
  const mgmt = {cluster: 'synthetic-mgmt', pool: '', node: ''};
  const svc = {cluster: 'synthetic-svc', pool: '', node: ''};
  const node = (name, pool = '') => ({...mgmt, pool, node: JSON.stringify(['synthetic-mgmt', name])});
  const a = (i, resource = 'cpu', scope = fleet) => aggregate(history[i], resource, scope);
  check(a(0).lines.capacity.value === 28 && a(0).lines.usage.value === 15, 'fleet sums all observed nodes');
  check(Math.abs(a(0).lines.usage.percent - 1500 / 28) < 1e-9, 'ratio of sums, not average of node percentages');
  check(a(0, 'memory').lines.capacity.value === 112 * 1073741824, 'memory preserved in bytes before display');
  check(a(0).requestRatio === 56 && a(0).estimate === 11, 'requests / allocatable and allocatable minus requests');
  check(a(1).lines.usage.value === null && a(1).lines.capacity.value === null && a(1).lines.requests.value === 15, 'missing metric gaps only affected absolute lines');
  check(a(1).lines.requests.percent === null, 'denominator incomplete despite complete numerator');
  check(a(1, 'cpu', node('mgmt-b')).lines.usage.value === 12 && a(1, 'cpu', node('mgmt-b')).lines.usage.percent === null, 'known absolute usage retained without capacity');
  check(a(1, 'cpu', svc).lines.usage.value === 0 && a(1, 'cpu', svc).lines.usage.percent === 0, 'measured zero is zero');
  check(a(2).lines.capacity.value === null && a(2, 'cpu', svc).missingInventory === 1, 'inventory false gaps all lines');
  check(a(2, 'cpu', mgmt).lines.capacity.value === 20, 'unknown pool does not invalidate all-pool aggregate');
  const hcp = {...mgmt, pool: JSON.stringify('hcp')};
  check(a(0, 'cpu', hcp).lines.capacity.value === 24 && a(1, 'cpu', hcp).lines.capacity.value === 12, 'membership changes at minute, not latest label');
  check(a(2, 'cpu', hcp).lines.capacity.value === null && a(2, 'cpu', hcp).ambiguousPool === 1, 'unknown pool prevents an understated pool total');
  const unknown = {...mgmt, pool: JSON.stringify('')};
  check(a(2, 'cpu', unknown).lines.capacity.value === 16 && a(2, 'cpu', unknown).ambiguousPool === 0, 'selected unknown pool measures its observed nodes');
  check(a(2, 'cpu', node('mgmt-b', unknown.pool)).lines.usage.value === 12, 'exact node in unknown pool retains known measurements');
  check(a(2, 'cpu', node('mgmt-b', hcp.pool)).lines.usage.value === null, 'unknown node membership still blocks exact node in named pool');
  const unknownLabels = JSON.parse(JSON.stringify(history[2])); unknownLabels.nodes[2].pool = 'unknown';
  check(aggregate(unknownLabels, 'cpu', unknown).lines.capacity.value === 20, 'empty and unknown labels share one unknown bucket');
  check(a(2, 'cpu', node('mgmt-new', JSON.stringify('hcp'))).lines.capacity.value === 4, 'unrelated unknown pool does not block selected known node');
  check(a(2, 'cpu', node('mgmt-old')).lines.capacity.value === null, 'deleted node is a gap, not zero or forward filled');
  check(a(3).missingClusters === 1 && a(3).lines.capacity.value === null, 'missing cluster inventory gaps fleet even with metrics');
  check(a(3, 'cpu', svc).lines.capacity.value === 4, 'another missing cluster does not invalidate selected cluster');
  const missing = {...history[0], nodes: history[0].nodes.filter(n => n.cluster === 'synthetic-svc')};
  check(aggregate(missing, 'cpu', fleet).missingClusters === 1 && aggregate(missing, 'cpu', fleet).lines.usage.value === null, 'expected cluster with no nodes gaps fleet');
  check(a(0, 'swiftNIC').lines.capacity.value === 24 && a(0, 'swiftNIC').excluded === 1, 'non-advertising nodes excluded from resource applicability');
  check(a(0, 'swiftNIC').lines.requests.value === 7 && a(0, 'swiftNIC').lines.requests.count === 3 && a(4, 'swiftNIC').lines.requests.value === 0, 'fixture demonstrates complete fleet SWIFT requests including measured zero');
  check(a(1, 'swiftNIC').lines.requests.value === null, 'fixture retains request gap at minute one');
  const missingRequests = JSON.parse(JSON.stringify(history[0])); missingRequests.nodes[0].requests.swiftNIC = null;
  const incompleteNIC = aggregate(missingRequests, 'swiftNIC', fleet);
  check(incompleteNIC.lines.requests.value === null && incompleteNIC.lines.requests.count === 2 && incompleteNIC.lines.requests.total === 3 && incompleteNIC.lines.capacity.value === 24, 'unknown requests on non-advertising node block only request total');
  check(a(0, 'swiftNIC', svc).noResource && a(0, 'swiftNIC', svc).lines.capacity.value === null, 'explicit no SWIFT is not zero');
  check(!a(2, 'swiftNIC', svc).noResource, 'inventory failure cannot claim absence of SWIFT resource');
  check(a(1, 'swiftNIC').unknownSwift === 1 && a(1, 'swiftNIC').lines.capacity.value === null, 'nil advertisement unknown, not excluded');
  check(a(1, 'swiftNIC', node('mgmt-b')).lines.capacity.value === 16 && a(1, 'swiftNIC', node('mgmt-b')).lines.requests.value === null, 'missing SWIFT requests do not erase capacity');
  check(!('usage' in a(0, 'swiftNIC').lines), 'never invent NIC usage');
  const swift = JSON.parse(JSON.stringify(history[0]));
  swift.nodes[0].requests.swiftNIC = 2;
  let nic = aggregate(swift, 'swiftNIC', svc);
  check(nic.noResource && nic.lines.capacity.value === null && nic.lines.allocatable.value === null && nic.lines.requests.value === 2, 'positive assigned requests survive absent advertised capacity');
  check(nic.lines.requests.percent === null && nic.requestRatio === null && nic.estimate === null, 'missing capacity and allocatable leave derived ratios and estimate unknown');
  check(lineText(nic, 'requests', 'swiftNIC').startsWith('2 slots (1/1 nodes)') && lineText(nic, 'capacity', 'swiftNIC') === 'No SWIFT-NIC resource advertised', 'absence notice never masks assigned request value');
  check(diagnostics(nic).includes('1 nodes with positive assigned SWIFT requests without confirmed advertisement'), 'requests without advertisement explicitly diagnosed');
  nic = aggregate(swift, 'swiftNIC', fleet);
  check(nic.lines.requests.value === 9 && nic.lines.capacity.value === 24 && nic.lines.requests.percent === 37.5, 'requests include non-advertising nodes while capacity excludes them');
  swift.nodes[0].requests.swiftNIC = 0;
  nic = aggregate(swift, 'swiftNIC', svc);
  check(nic.noResource && nic.lines.requests.value === 0 && nic.requestsWithoutAdvertisement === 0, 'known zero requests retain explicit no-advertisement state');
  swift.nodes[0].swiftAdvertised = null; swift.nodes[0].requests.swiftNIC = 2;
  nic = aggregate(swift, 'swiftNIC', svc);
  check(!nic.noResource && nic.unknownSwift === 1 && nic.lines.capacity.value === null && nic.lines.allocatable.value === null, 'unknown advertisement blocks capacity and allocatable');
  check(nic.lines.requests.value === 2 && nic.lines.requests.count === 1 && nic.lines.requests.percent === null, 'unknown advertisement does not block absolute request line, only percentage denominator');
  nic = aggregate(swift, 'swiftNIC', fleet);
  check(nic.lines.requests.value === 9 && nic.lines.requests.percent === null && nic.lines.capacity.value === null, 'aggregate request completeness is independent of capacity applicability');
  const zero = JSON.parse(JSON.stringify(history[0])); zero.nodes = [zero.nodes[0]]; zero.nodes[0].capacity.cpu = 0;
  check(aggregate(zero, 'cpu', svc).lines.usage.percent === null, 'zero denominator is unknown, not infinity');
  zero.nodes[0].allocatable.cpu = 1; zero.nodes[0].requests.cpu = 2;
  check(aggregate(zero, 'cpu', svc).estimate === -1, 'negative estimate not clamped into misleading availability');
  check(!$('history-content').hidden && resources.every(r => $('chart-' + r).hidden), 'CDN-free sample table has no empty chart space');
  check($('samples').children.length === 15 && $('samples').textContent.includes('Data unavailable'), 'all fixture rows available without CDN');
  check($('coverage-cpu').textContent.includes('Capacity: 2/5 complete minutes'), 'per-line coverage visible without chart');
  check(!$('diagnostic-text').children.length, 'warnings remain lazy');
  $('diagnostics').open = true; $('diagnostics').ontoggle();
  check($('diagnostic-text').firstChild.value.includes(report.warnings.at(-1)), 'literal diagnostic text preserved');
  check(!window.injected && !document.querySelector('img'), 'diagnostics never execute HTML');
  $('diagnostics').open = false; $('diagnostics').ontoggle();
  check(!$('diagnostic-text').children.length, 'diagnostic collapse releases DOM');
  check(document.documentElement.scrollWidth <= window.innerWidth, 'mobile has no page overflow');
  const choose = (id, value) => { $(id).value = value; $(id).onchange(); };
  choose('cluster', 'synthetic-mgmt');
  check([...$('node').options].some(option => option.textContent === 'mgmt-old'), 'deleted node retained in selector');
  check([...$('pool').options].some(option => option.textContent === 'infra'), 'historical pool retained');
  choose('pool', JSON.stringify('hcp'));
  check([...$('node').options].some(option => option.textContent === 'mgmt-b'), 'pool selector includes node formerly in pool');
  choose('node', JSON.stringify(['synthetic-mgmt', 'mgmt-b']));
  check(aggregates.cpu[0].lines.capacity.value === 16 && aggregates.cpu[1].lines.capacity.value === null, 'selected node follows minute pool membership');
  choose('pool', unknown.pool);
  check([...$('node').options].some(option => option.textContent === 'mgmt-b') && aggregates.cpu[2].lines.capacity.value === 16, 'unknown bucket selector displays known capacity');
  choose('node', JSON.stringify(['synthetic-mgmt', 'mgmt-b']));
  check(aggregates.cpu[2].lines.usage.value === 12 && $('samples').textContent.includes('12 cores'), 'exact unknown-pool node measurements reach sample table');
  choose('cluster', 'synthetic-svc');
  check(!scope.pool && !scope.node && $('pool').value === '' && $('node').value === '', 'cluster cascades reset descendants');
  check($('status-swiftNIC').textContent.includes('No SWIFT-NIC resource advertised at 4/5 minutes') && aggregates.swiftNIC[2].lines.requests.value === null, 'known zero requests coexist with explicit advertisement absence and inventory gap');
  const originalSwift = history[0].nodes[0];
  history[0].nodes[0] = swift.nodes[0]; render();
  check(aggregates.swiftNIC[0].lines.requests.value === 2 && $('samples').textContent.includes('2 slots'), 'unknown advertisement assigned requests reach rendered table');
  check(chartOption('swiftNIC').series.find(series => series.id === 'requests').data[0][1] === 2, 'unknown advertisement does not discard request chart point');
  check($('status-swiftNIC').textContent.includes('Positive assigned requests without confirmed advertisement at 1/5 minutes'), 'request advertisement mismatch visible without opening diagnostics');
  history[0].nodes[0].swiftAdvertised = false; render();
  check(aggregates.swiftNIC[0].noResource && tooltip('swiftNIC', 0).textContent.includes('Requests: 2 slots'), 'no-resource tooltip still shows known requests');
  check(chartOption('swiftNIC').series.find(series => series.id === 'requests').data[0][1] === 2, 'no-resource state does not discard request chart point');
  choose('display', 'percent');
  check(tooltip('swiftNIC', 0).textContent.includes('Requests: Data unavailable'), 'percentage request tooltip cannot manufacture denominator');
  check(chartOption('swiftNIC').series.find(series => series.id === 'requests').data[0][1] === null, 'percentage request chart gaps without capacity denominator');
  choose('display', 'absolute');
  history[0].nodes[0] = originalSwift; render();
  choose('cluster', '');
  startCharts();
  const option = resource => charts[resource].getOption();
  check(option('cpu').useUTC && option('cpu').xAxis[0].min === Date.parse(report.start) && option('cpu').xAxis[0].max === Date.parse(report.end), 'UTC and global bounds');
  check(resources.every(resource => option(resource).xAxis[0].axisLabel.hideOverlap === true), 'time axes suppress overlapping labels on narrow viewports');
  check(option('cpu').series.every(series => series.connectNulls === false && series.smooth === false), 'no null interpolation or smooth usage');
  check(option('cpu').series.find(series => series.id === 'usage').step === false && option('cpu').series.find(series => series.id === 'requests').step === 'end', 'straight usage and step requests');
  check(option('memory').series[0].data[0][1] === 112, 'memory chart uses GiB');
  check(option('swiftNIC').series.length === 3, 'three NIC lines, never usage');
  const tip = tooltip('cpu', 0);
  check(tip.textContent.includes('Requests / allocatable: 56%') && tip.textContent.includes('estimate: 11 cores') && tip.textContent.includes('UTC'), 'safe tooltip with ratios, estimates, UTC');
  const cpuChart = charts.cpu;
  check(resources.every(resource => charts[resource].group === 'resource-history'), 'all resources share hover and zoom connection group');
  cpuChart.dispatchAction({type: 'dataZoom', start: 20, end: 80});
  if (window.cachedECharts) {
    check(option('memory').dataZoom[0].start === 20 && option('swiftNIC').dataZoom[0].end === 80, 'real ECharts synchronizes zoom before rerender');
    cpuChart.dispatchAction({type: 'showTip', seriesIndex: 0, dataIndex: 0});
    check(tooltip('cpu', 0).textContent.includes('3/3 nodes'), 'linked hover has exact per-line coverage');
    cpuChart.dispatchAction({type: 'hideTip'});
  }
  cpuChart.dispatchAction({type: 'legendToggleSelect', name: 'Usage'});
  choose('cluster', 'synthetic-mgmt');
  choose('pool', JSON.stringify('hcp'));
  choose('display', 'percent');
  for (const resource of resources) {
    const config = option(resource);
    check(config.dataZoom[0].start === 20 && config.dataZoom[0].end === 80, 'linked zoom preserved across scope and units');
    check(config.legend[0].selected.Usage === false, 'legend selection survives scope update');
    check(config.xAxis[0].min === Date.parse(report.start) && config.xAxis[0].max === Date.parse(report.end), 'scope does not shrink time axis');
  }
  check(charts.cpu === cpuChart, 'chart instance reused on scope change');
  check($('status-cpu').textContent.includes('Capacity denominator complete'), 'percentage denominator coverage visible');
  $('reset-zoom').click();
  check(option('cpu').dataZoom[0].start === 0 && option('memory').dataZoom[0].end === 100, 'reset applies across charts');
  const left = $('chart-cpu').getBoundingClientRect(), right = $('chart-memory').getBoundingClientRect();
  check(window.innerWidth <= 900 ? right.top > left.top : right.top === left.top && right.left > left.left, 'responsive resource charts');
  const before = JSON.stringify(history), malicious = '<' + '/script><img src=x onerror="window.injected=true">';
  history[0].nodes[0].pool = malicious; history[0].nodes[0].name = malicious;
  choose('cluster', 'synthetic-svc'); choose('pool', JSON.stringify(malicious));
  check([...$('pool').options].some(option => option.textContent === malicious), 'selector text preserves literal metric labels');
  choose('node', JSON.stringify(['synthetic-svc', malicious]));
  check($('scope').textContent.includes(malicious) && !window.injected && !document.querySelector('img'), 'scope labels cannot inject HTML');
  history.splice(0, history.length, ...JSON.parse(before));
  const original = history.slice();
  for (let i = 5; i < 30; i++) history.push({...history[0], time: new Date(Date.parse(history[0].time) + i * 60000).toISOString()});
  choose('cluster', '');
  check($('samples').children.length === 36 && !$('next').disabled, 'sample table bounded to twelve minutes');
  $('next').click(); $('next').click();
  check($('samples').children.length === 18 && $('next').disabled && !$('previous').disabled, 'last page reachable');
  $('previous').click(); check($('samples').children.length === 36, 'previous page works');
  history.splice(0, history.length, ...original);
`)
}

func TestRenderResourceHistoryBrowserLegacy(t *testing.T) {
	r := resourceHistoryFixture(t)
	r.History = nil
	checkResourceHistoryBrowser(t, r, `
  check($('history-status').textContent.startsWith('History not recorded.'), 'legacy artifacts explicitly lack history');
  check($('history-content').hidden && !$('samples').children.length, 'do not synthesize history from peak snapshots');
  startCharts(); check(!Object.keys(charts).length, 'no empty charts for legacy report');
`)
}

// Uses the existing optional Chrome/cached-ECharts convention. The fallback
// chart double exercises event wiring; a cached CDN asset exercises real charts.
func checkResourceHistoryBrowser(t *testing.T, report utilizationReport, assertions string) {
	t.Helper()
	browser, err := exec.LookPath("google-chrome")
	if err != nil {
		t.Skip("google-chrome unavailable; skipping optional browser test")
	}
	html, err := renderResourceHistoryHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	text, _, _ := strings.Cut(string(html), "<script defer src=")
	if cached := os.Getenv("UTILIZATION_ECHARTS_JS"); cached != "" {
		library, err := os.ReadFile(cached)
		if err != nil {
			t.Fatal(err)
		}
		text += "<script>" + string(library) + "\nwindow.cachedECharts = window.echarts; delete window.echarts;</script>"
	}
	text += `<script>
(async () => { try {
  const check = (condition, message) => { if (!condition) throw new Error(message); };
  function startCharts() {
    window.echarts = window.cachedECharts || {connect() {}, init() {
      let option; const handlers = {};
      return {setOption(value) { option = value; }, getOption() { return {...option, xAxis: [option.xAxis], yAxis: [option.yAxis], legend: [option.legend]}; },
        on(name, handler) { handlers[name] = handler; }, resize() {},
        dispatchAction(event) {
          if (event.type === 'dataZoom') handlers.datazoom(event);
          if (event.type === 'legendToggleSelect') handlers.legendselectchanged({selected: {...option.legend.selected, [event.name]: false}});
        }};
    }};
    drawCharts();
    if (window.cachedECharts) for (const chart of Object.values(charts)) {
      const setOption = chart.setOption.bind(chart);
      chart.setOption = (...args) => { setOption(...args); chart.getZr().flush(); chart.getZr().animation.stop(); };
      chart.getZr().flush(); chart.getZr().animation.stop();
    }
  }
` + assertions + `
  const result = document.createElement('output'); result.id = 'browser-result'; result.textContent = 'PASS'; document.body.append(result);
} catch (error) {
  const result = document.createElement('output'); result.id = 'browser-result'; result.textContent = 'FAIL: ' + error.stack; document.body.append(result);
} })();
</script></body></html>`
	dir := t.TempDir()
	file := filepath.Join(dir, "history.html")
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
				t.Fatalf("browser test: %v\n%s", err, stderr.String())
			}
			if !strings.Contains(string(dom), `<output id="browser-result">PASS</output>`) {
				_, result, _ := strings.Cut(string(dom), `<output id="browser-result">`)
				result, _, _ = strings.Cut(result, "</output>")
				t.Fatalf("browser assertions failed: %s", result)
			}
		})
	}
}
