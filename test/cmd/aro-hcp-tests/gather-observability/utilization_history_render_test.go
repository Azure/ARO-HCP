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
	before, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	html, err := renderResourceHistoryHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(html)
	for _, want := range []string{"<!DOCTYPE html>", `name="viewport"`, `id="chart-cpu"`, `id="chart-memory"`, `id="chart-swiftNIC"`, `\u003c/script\u003e`, `\u0026`, `\u2028`, `\u2029`, "https://go-echarts.github.io/go-echarts-assets/assets/echarts.min.js"} {
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
	decoded := decodeHistoryHTML(t, []byte(embedded))
	after, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("HTML rendering changed the persisted report")
	}
	report.Snapshots = nil
	report.Coverage = nil
	if !reflect.DeepEqual(report, decoded) {
		t.Fatal("embedded JSON changed nullable measurements or untrusted strings")
	}
}

func decodeHistoryHTML(t *testing.T, data []byte) utilizationReport {
	t.Helper()
	var compact struct {
		utilizationReport
		History    []historyHTMLSample              `json:"history"`
		Metadata   []historyHTMLMetadata            `json:"metadata"`
		Quantities [][4]utilizationHistoryResources `json:"quantities"`
		Messages   []string                         `json:"messages"`
	}
	if err := json.Unmarshal(data, &compact); err != nil {
		t.Fatal(err)
	}
	report := compact.utilizationReport
	for _, sample := range compact.History {
		minute := utilizationHistorySample{Time: sample.Time, Expected: sample.Expected}
		if sample.Nodes != nil {
			minute.Nodes = make([]utilizationHistoryEntry, 0, len(sample.Nodes))
		}
		for _, pair := range sample.Nodes {
			m, q := compact.Metadata[pair[0]], compact.Quantities[pair[1]]
			minute.Nodes = append(minute.Nodes, utilizationHistoryEntry{m.Cluster, m.Name, m.Pool, m.SKU, m.Inventory, m.SwiftAdvertised, q[0], q[1], q[2], q[3]})
		}
		for _, index := range sample.Warnings {
			minute.Warnings = append(minute.Warnings, compact.Messages[index])
		}
		report.History = append(report.History, minute)
	}
	return report
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
  check(nic.requestsWithoutAdvertisement === 1, 'requests without advertisement remain diagnosed in aggregate');
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
  check(!$('history-content').hidden && resources.every(r => $('chart-' + r).hidden), 'unloaded charts have no empty chart space');
  check(!document.querySelector('details, table, textarea') && !$('samples') && !$('coverage-cpu'), 'graphs only: no coverage, samples or measurement sections');
  check(!window.injected && !document.querySelector('img'), 'saved diagnostics never execute HTML');
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
  check(aggregates.cpu[2].lines.usage.value === 12, 'exact unknown-pool node measurements remain available');
  choose('cluster', 'synthetic-svc');
  check(!scope.pool && !scope.node && $('pool').value === '' && $('node').value === '', 'cluster cascades reset descendants');
  check(aggregates.swiftNIC.filter(s => s.noResource).length === 4 && aggregates.swiftNIC[2].lines.requests.value === null, 'advertisement absence and inventory gap remain distinct');
  choose('cluster', '');
  startCharts();
  const option = resource => charts[resource].getOption();
  check(option('cpu').useUTC && option('cpu').xAxis[0].min === Date.parse(report.start) && option('cpu').xAxis[0].max === Date.parse(report.end), 'UTC and global bounds');
  check(resources.every(resource => option(resource).xAxis[0].axisLabel.hideOverlap === true), 'time axes suppress overlapping labels on narrow viewports');
  check(option('cpu').series.every(series => series.connectNulls === false && series.smooth === false), 'no null interpolation or smooth usage');
  check(resources.every(resource => option(resource).legend[0].show && option(resource).legend[0].icon === 'rect'), 'legends always visible without circular icons');
  check(resources.every(resource => option(resource).series.every(series => series.showSymbol === false && series.symbol === 'none' && series.triggerLineEvent)), 'lines only with hover enabled');
  check(option('cpu').series.find(series => series.id === 'usage').step === false && option('cpu').series.find(series => series.id === 'requests').step === 'end', 'straight usage and step requests');
  check(option('memory').series[0].data[0][1] === 112, 'memory chart uses GiB');
  check(option('swiftNIC').series.length === 3, 'three NIC lines, never usage');
  const tip = tooltip('cpu', 0, 'requests');
  check(tip.textContent.includes('Requests:') && tip.textContent.includes('UTC') && !tip.textContent.includes('Capacity') && !tip.textContent.includes('estimate') && !tip.textContent.includes('nodes'), 'tooltip contains only hovered series value and UTC');
  check(option('cpu').tooltip[0].trigger === 'item', 'only hovered item triggers tooltip');
  check(document.querySelectorAll('details[open]').length === 0 && $('history-status').hidden, 'details and explanatory text start hidden');
  const cpuChart = charts.cpu;
  check(resources.every(resource => !charts[resource].group), 'charts do not broadcast hover via connection group');
  cpuChart.dispatchAction({type: 'dataZoom', start: 20, end: 80});
  if (window.cachedECharts) {
    check(option('memory').dataZoom[0].start === 20 && option('swiftNIC').dataZoom[0].end === 80, 'real ECharts synchronizes zoom before rerender');
    cpuChart.dispatchAction({type: 'showTip', seriesIndex: 0, dataIndex: 0});
    check(tooltip('cpu', 0, 'capacity').children.length === 2, 'hover remains a compact timestamp and single value');
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
  $('reset-zoom').click();
  check(option('cpu').dataZoom[0].start === 0 && option('memory').dataZoom[0].end === 100, 'reset applies across charts');
  const left = $('chart-cpu').getBoundingClientRect(), right = $('chart-memory').getBoundingClientRect();
  check(window.innerWidth <= 900 ? right.top > left.top : right.top === left.top && right.left > left.left, 'responsive resource charts');
`)
}

func TestRenderResourceHistoryBrowserRequestsWithoutAdvertisement(t *testing.T) {
	for _, advertised := range []*bool{nil, new(bool)} {
		r := resourceHistoryFixture(t)
		r.History[0].Nodes[0].SwiftAdvertised = advertised
		requests := 2.0
		r.History[0].Nodes[0].Requests.SwiftNIC = &requests
		checkResourceHistoryBrowser(t, r, `
  $('cluster').value = 'synthetic-svc'; $('cluster').onchange();
  check(aggregates.swiftNIC[0].lines.requests.value === 2, 'assigned requests retained');
  check(chartOption('swiftNIC').series.find(series => series.id === 'requests').data[0][1] === 2, 'advertisement does not discard request chart point');
  check(aggregates.swiftNIC[0].requestsWithoutAdvertisement === 1, 'request advertisement mismatch retained');
  check(tooltip('swiftNIC', 0, 'requests').textContent.includes('Requests: 2 slots'), 'tooltip still shows known requests');
  $('display').value = 'percent'; $('display').onchange();
  check(tooltip('swiftNIC', 0, 'requests').textContent.includes('Requests: Data unavailable'), 'percentage request tooltip cannot manufacture denominator');
  check(chartOption('swiftNIC').series.find(series => series.id === 'requests').data[0][1] === null, 'percentage request chart gaps without capacity denominator');
`)
	}
}

func TestRenderResourceHistoryBrowserLabels(t *testing.T) {
	r := resourceHistoryFixture(t)
	attack := `</script><img src=x onerror="window.injected=true">`
	r.History[0].Nodes[0].Pool, r.History[0].Nodes[0].Name = attack, attack
	checkResourceHistoryBrowser(t, r, `
  const malicious = '<' + '/script><img src=x onerror="window.injected=true">';
  $('cluster').value = 'synthetic-svc'; $('cluster').onchange();
  $('pool').value = JSON.stringify(malicious); $('pool').onchange();
  check([...$('pool').options].some(option => option.textContent === malicious), 'selector text preserves literal metric labels');
  $('node').value = JSON.stringify(['synthetic-svc', malicious]); $('node').onchange();
  check(JSON.parse(scope.node)[1] === malicious && !window.injected && !document.querySelector('img'), 'scope labels cannot inject HTML');
`)
}

func TestRenderResourceHistoryBrowserLegacy(t *testing.T) {
	r := resourceHistoryFixture(t)
	r.History = nil
	checkResourceHistoryBrowser(t, r, `
  check($('history-status').textContent.startsWith('History not recorded.'), 'legacy artifacts explicitly lack history');
  check($('history-content').hidden && !$('samples'), 'do not synthesize history from peak snapshots');
  startCharts(); check(!Object.keys(charts).length, 'no empty charts for legacy report');
`)
}

func largeResourceHistoryFixture(t *testing.T) utilizationReport {
	t.Helper()
	r := resourceHistoryFixture(t)
	source := r.History
	r.History = nil
	for i := range 361 {
		minute := source[i%len(source)]
		minute.Time = r.Start.Add(time.Duration(i) * time.Minute)
		minute.Nodes = make([]utilizationHistoryEntry, 250)
		for j := range minute.Nodes {
			node := source[i%len(source)].Nodes[j%len(source[i%len(source)].Nodes)]
			node.Name = fmt.Sprintf("%s-%03d", node.Name, j)
			minute.Nodes[j] = node
		}
		r.History = append(r.History, minute)
	}
	r.End = r.History[len(r.History)-1].Time
	return r
}

func TestRenderResourceHistoryCompact(t *testing.T) {
	r := largeResourceHistoryFixture(t)
	full, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	compact, err := marshalResourceHistoryHTML(r)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r, decodeHistoryHTML(t, compact)) {
		t.Fatal("interning changed per-minute membership, inventory, advertisement, quantities or warnings")
	}
	if len(compact) >= len(full)/5 {
		t.Fatalf("repeated history should compact below 20%%: %d / %d", len(compact), len(full))
	}
	var tables struct {
		Metadata, Quantities, Messages []json.RawMessage
	}
	if err := json.Unmarshal(compact, &tables); err != nil {
		t.Fatal(err)
	}
	if len(tables.Metadata) > 1500 || len(tables.Quantities) > 20 || len(tables.Messages) != 3 {
		t.Fatalf("records not interned: metadata=%d quantities=%d messages=%d", len(tables.Metadata), len(tables.Quantities), len(tables.Messages))
	}
	t.Logf("90,250 node-minutes: %d -> %d JSON bytes", len(full), len(compact))

	t.Run("exact finite quantities", func(t *testing.T) {
		r := resourceHistoryFixture(t)
		values := []float64{0.1, math.Nextafter(0.1, 1), math.SmallestNonzeroFloat64, math.MaxFloat64, 0, math.Copysign(0, -1)}
		for i := range r.History {
			for j := range r.History[i].Nodes {
				node := &r.History[i].Nodes[j]
				for k, field := range []*utilizationHistoryResources{&node.Capacity, &node.Allocatable, &node.Usage, &node.Requests} {
					value := values[(i+j+k)%len(values)]
					*field = utilizationHistoryResources{CPU: &value, Memory: nil, SwiftNIC: nil}
				}
			}
		}
		compact, err := marshalResourceHistoryHTML(r)
		if err != nil {
			t.Fatal(err)
		}
		// JSON equality also distinguishes signed zero, unlike DeepEqual, and
		// catches rounding, underflow, overflow, and null-to-zero conversion.
		want, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(decodeHistoryHTML(t, compact))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(want, got) {
			t.Fatal("compaction changed exact finite floats, signed zero or null quantities")
		}
	})
}

func TestRenderResourceHistoryBrowserLarge(t *testing.T) {
	checkResourceHistoryBrowser(t, largeResourceHistoryFixture(t), historyPerformanceAssertions)
}

// Opt-in replay of a real collection or the saved stress fixture, without
// committing large artifacts. Set UTILIZATION_HISTORY_REPLAY to utilization.json
// and UTILIZATION_ECHARTS_JS to a locally cached real ECharts library.
func TestRenderResourceHistoryBrowserReplay(t *testing.T) {
	path := os.Getenv("UTILIZATION_HISTORY_REPLAY")
	if path == "" {
		t.Skip("set UTILIZATION_HISTORY_REPLAY to replay a saved utilization.json")
	}
	if os.Getenv("UTILIZATION_ECHARTS_JS") == "" {
		t.Fatal("replay requires UTILIZATION_ECHARTS_JS")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report utilizationReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	compact, err := marshalResourceHistoryHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report, decodeHistoryHTML(t, compact)) {
		t.Fatal("replay compaction changed the saved report")
	}
	checkResourceHistoryBrowser(t, report, historyPerformanceAssertions)
}

const historyPerformanceAssertions = `
  const choose = (id, value) => { $(id).value = value; $(id).onchange(); };
  check(history.length > 12, 'large test requires substantial history');
  check(!$('samples'), 'no sample-table DOM for large history');
  const scans = aggregate;
  let calls = 0;
  aggregate = (...args) => { calls++; return scans(...args); };
  const originalDates = Date.parse;
  Date.parse = () => { throw new Error('interaction reparsed an immutable timestamp'); };
  const fleet = aggregates.cpu;
  choose('display', 'percent'); choose('display', 'absolute');
  check(calls === 0 && aggregates.cpu === fleet, 'display changes reuse current scope aggregates');
  // Instrument array reads without changing the immutable report's values.
  // Unlike aggregate call counts, these guards catch filter/some, iterators,
  // copying and indexed loops over the full fleet or a whole node's cluster.
  let forbidFleetReads = true, forbidClusterReads = false;
  const guardedNodes = (nodes, forbidden, message) => new Proxy(nodes, {
    get(target, key, receiver) {
      if (typeof key === 'string' && /^(0|[1-9][0-9]*)$/.test(key)) check(!forbidden(), message);
      return Reflect.get(target, key, receiver);
    }
  });
  for (const sample of history) {
    const index = sampleIndex(sample);
    sample.nodes = guardedNodes(sample.nodes, () => forbidFleetReads, 'scoped interaction traversed full fleet');
    for (const [cluster, nodes] of index.clusters) {
      index.clusters.set(cluster, guardedNodes(nodes, () => forbidClusterReads, 'node selection traversed cluster nodes instead of indexed lookup'));
    }
  }
  choose('cluster', $('cluster').options[1].value);
  choose('pool', $('pool').options[1].value);
  const poolOption = $('pool').options[1], nodeOption = $('node').options[1];
  forbidClusterReads = true;
  choose('node', nodeOption.value);
  check($('pool').options[1] === poolOption && $('node').options[1] === nodeOption, 'node-only selection does not rebuild selectors');
  forbidFleetReads = false; forbidClusterReads = false;
  const selectedNode = scope.node;
  for (let i = 0; i < history.length; i++) {
    const expected = history[i].nodes.filter(node => nodeKey(node) === selectedNode && (!scope.pool || poolKey(node.pool) === scope.pool || unknownPool(node.pool))).length;
    check(aggregates.cpu[i].observed === expected, 'indexed selection respects minute-local membership');
  }
  choose('cluster', '');
  // Independently check fleet sums against the decoded source, including nulls,
  // inventory gaps and SWIFT request applicability (not chart-prepared values).
  for (let i = 0; i < history.length; i++) for (const resource of resources) {
    const sample = history[i], nodes = sample.nodes;
    const missingCluster = report.clusters.some(cluster => !list(sample.expected).includes(cluster) || !nodes.some(node => node.cluster === cluster && node.inventory));
    for (const field of resourceFields(resource)) {
      const advertised = resource === 'swiftNIC' && field !== 'requests';
      const relevant = nodes.filter(node => !advertised || node.swiftAdvertised !== false);
      const complete = !missingCluster && nodes.every(node => node.inventory) && relevant.length > 0 && relevant.every(node => (!advertised || node.swiftAdvertised === true) && known(node[field][resource]));
      const expected = complete ? relevant.reduce((sum, node) => sum + node[field][resource], 0) : null;
      check(aggregates[resource][i].lines[field].value === expected, 'independent exact fleet sum and gap check');
    }
  }
  Date.parse = originalDates;
  // Guard only our option preparation, including initial chart creation and
  // every subsequent update. ECharts itself may legitimately parse dates.
  const prepareChart = chartOption;
  let chartPreparations = 0;
  chartOption = (...args) => {
    const parse = Date.parse;
    Date.parse = () => { throw new Error('chart preparation reparsed an immutable timestamp'); };
    try { chartPreparations++; return prepareChart(...args); }
    finally { Date.parse = parse; }
  };
  startCharts();
  check(chartPreparations === resources.length, 'guard covers initial option preparation for every chart');
  const models = Object.fromEntries(resources.map(resource => [resource, window.cachedECharts ? charts[resource].getModel().getSeries() : []]));
  for (const chart of Object.values(charts)) {
    const update = chart.setOption.bind(chart);
    chart.setOption = (option, settings) => {
      check(settings !== true && !settings?.notMerge && !settings?.replaceMerge, 'chart updates must merge stable series');
      update(option, settings);
    };
  }
  charts.cpu.dispatchAction({type: 'dataZoom', start: 25, end: 75});
  charts.cpu.dispatchAction({type: 'legendToggleSelect', name: 'Usage'});
  for (const display of ['percent', 'absolute']) {
    const beforePreparation = chartPreparations;
    choose('display', display);
    check(chartPreparations === beforePreparation + resources.length, 'guard covers option updates for every chart');
    for (const resource of resources) {
      const option = charts[resource].getOption();
      check(option.dataZoom[0].start === 25 && option.dataZoom[0].end === 75, 'linked zoom retained');
      check(option.legend[0].selected.Usage === false, 'legend retained');
      if (window.cachedECharts) check(charts[resource].getModel().getSeries().every((model, i) => model === models[resource][i]), 'ECharts series models reused');
      for (const series of option.series) {
        check(series.data.length === history.length && series.connectNulls === false, 'all points and missing-data gaps retained');
        for (let i = 0; i < history.length; i++) {
          const line = aggregates[resource][i].lines[series.id];
          const expected = display === 'percent' ? line.percent : known(line.value) ? line.value / (resource === 'memory' ? 1073741824 : 1) : null;
          check(series.data[i][0] === timestamps[i] && series.data[i][1] === expected, 'exact timestamp and quantity in every ECharts point');
        }
      }
    }
  }
  $('reset-zoom').click();
  if (window.cachedECharts) check(resources.every(resource => charts[resource].getOption().dataZoom.every(z => z.start === 0 && z.end === 100)), 'reset linked zoom without setOption');
`

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
      return {setOption(value) { option = value; }, getOption() { return {...option, xAxis: [option.xAxis], yAxis: [option.yAxis], legend: [option.legend], tooltip: [option.tooltip]}; },
        on(name, handler) { handlers[name] = handler; }, resize() {},
        dispatchAction(event, settings = {}) {
           if (event.type === 'dataZoom') {
              for (const z of option.dataZoom) Object.assign(z, {start: event.start, end: event.end});
              if (!settings.silent) handlers.datazoom(event);
           }
          if (event.type === 'legendToggleSelect') handlers.legendselectchanged({name: event.name, selected: {...option.legend.selected, [event.name]: false}});
          if (event.type === 'legendSelect' || event.type === 'legendUnSelect') option.legend.selected[event.name] = event.type === 'legendSelect';
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
