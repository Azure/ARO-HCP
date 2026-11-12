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

package visualize

import (
	"encoding/json"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-echarts/go-echarts/v2/opts"
)

func TestTestTimingChartTimings(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 123000000, time.UTC)
	chart := testTimingChart([]TestInfo{
		{Identifier: []string{"later"}, StartedAt: start.Add(3 * time.Second), FinishedAt: start.Add(3 * time.Second)},
		{Identifier: []string{"first"}, StartedAt: start, FinishedAt: start.Add(2 * time.Second), Steps: []StepTimingMetadata{
			{Name: "zero", StartedAt: start.Add(time.Second), FinishedAt: start.Add(time.Second)},
			{Name: "short", StartedAt: start.Add(1500*time.Millisecond + 500*time.Microsecond), FinishedAt: start.Add(1500*time.Millisecond + 501*time.Microsecond)},
		}},
		{Identifier: []string{"first"}, StartedAt: start.Add(4 * time.Second), FinishedAt: start.Add(5 * time.Second)},
	})
	offsets := chart.MultiSeries[0].Data.([]opts.BarData)
	durations := chart.MultiSeries[1].Data.([]opts.BarData)
	for i, want := range []struct {
		name             string
		offset, duration float64
	}{
		{"first", 0, 2000}, {"later", 3000, 0}, {"first", 4000, 1000},
	} {
		if offsets[i].Value != want.offset || durations[i].Value != want.duration || durations[i].Name != want.name {
			t.Errorf("row %d: got %v / %+v, expected %+v", i, offsets[i].Value, durations[i], want)
		}
	}
	if !strings.Contains(string(chart.RenderContent()), "2026-01-01T00:00:00.123Z") {
		t.Error("axis formatter must retain the fractional-second start time")
	}
}

func TestTestTimingTooltipAndEscaping(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	name := "</script><img src=x onerror=alert(1)> & \"quoted\"\n\u2028\u2029__f__"
	chart := testTimingChart([]TestInfo{{Identifier: []string{name}, StartedAt: start, FinishedAt: start}})
	html := string(chart.RenderContent())
	if strings.Contains(html, name) || !strings.Contains(html, `\u003c/script\u003e`) {
		t.Fatal("test names must be safely encoded in the inline script")
	}
	if !strings.Contains(html, `\u005f\u005ff\u005f\u005f`) {
		t.Fatal("go-echarts function markers in names must remain data")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to execute tooltip formatter regression tests")
	}
	script := `const assert = require('node:assert/strict');
	global.document = {createElement: () => {
		let text = '';
		const children = [];
		return {
			style: {}, append: (...nodes) => children.push(...nodes),
			set textContent(value) {text = String(value); children.length = 0;},
			get textContent() {return text + children.map(node => node.textContent).join('');},
			set innerHTML(_) {throw Error('unsafe HTML');}
		};
	}};
	const formatter = ` + testTimingTooltipFormatter + `;
	const name = '<img src=x onerror=alert(1)> & "quoted"';
	for (const [value, expected] of [[0,'0s'],[1,'0s'],[12,'0s'],[999,'0s'],[1001,'1s'],[0.001,'0s'],[3601001,'1h0m1s']]) {
		const result = formatter([{seriesIndex:0,value:98765},{seriesIndex:1,name,value}]);
		assert.equal(result.textContent, name + ': ' + expected);
	}
	assert.equal(formatter([{seriesIndex:0,value:98765}]), '');`
	// Execute the actual emitted script too: labels must survive both JSON encoding
	// and go-echarts' function-marker substitution without becoming executable code.
	inline := regexp.MustCompile(`(?s)<script type="text/javascript">(.*?)</script>`).FindStringSubmatch(html)
	if len(inline) != 2 {
		t.Fatal("expected generated chart script")
	}
	encodedScript, err := json.Marshal(inline[1])
	if err != nil {
		t.Fatal(err)
	}
	encodedName, err := json.Marshal(name)
	if err != nil {
		t.Fatal(err)
	}
	script += `
	const options = [];
	const listeners = {};
	const windowListeners = {};
	const sort = {value:'start', addEventListener:(event, handler) => listeners[event] = handler};
	let containerTop = 405;
	const state = {};
	const chart = {
		height: 600,
		dom: {style:{height:'60vh'}, getBoundingClientRect:() => ({top:435})},
		setOption: option => {
			options.push(option);
			if (option.xAxis) state.xAxis = (Array.isArray(option.xAxis) ? option.xAxis : [option.xAxis]).map((axis, i) => ({...state.xAxis?.[i], ...axis}));
			if (option.yAxis) state.yAxis = (Array.isArray(option.yAxis) ? option.yAxis : [option.yAxis]).map((axis, i) => ({...state.yAxis?.[i], ...axis}));
			if (option.series) state.series = option.series.map((series, i) => ({...state.series?.[i], ...series}));
			if (option.dataZoom) state.dataZoom = option.dataZoom;
			if (option.tooltip) state.tooltip = {...state.tooltip, ...option.tooltip};
		},
		getOption: () => state,
		getZr: () => ({on: () => {}}),
		getHeight: () => chart.height,
		getDom: () => chart.dom,
		resize: ({height}) => chart.height = height,
		on: (event, handler) => listeners['chart:' + event] = handler,
		dispatchAction: action => {
			const zoom = state.dataZoom.find(item => item.id === action.dataZoomId);
			if (zoom) Object.assign(zoom, {start:action.start, end:action.end});
			listeners['chart:datazoom']?.({batch:[action]});
		}
	};
	const vmDocument = {
		body: {}, documentElement: {},
		createElement: global.document.createElement,
		getElementById: id => id === 'timing-sort' ? sort : null,
		querySelector: selector => selector === '.container' ? {getBoundingClientRect:() => ({top:containerTop})} : null
	};
	const vmWindow = {
		innerHeight: 1000, scrollY: 0,
		addEventListener: (event, handler) => windowListeners[event] = handler,
		matchMedia: () => ({addEventListener: () => {}})
	};
	require('node:vm').runInNewContext(` + string(encodedScript) + `, {
		document: vmDocument, window: vmWindow, getComputedStyle: () => ({color:'#000', backgroundColor:'#fff'}),
		MutationObserver: class {observe() {}},
		echarts: {init: () => chart}, Math, Number
	});
	assert.equal(options[0].series[1].data[0].name, ` + string(encodedName) + `);
	assert.equal(options[0].yAxis[0].data[0], ` + string(encodedName) + `);
	const timeline = state.tooltip.formatter([{seriesId:'timing-tests', dataIndex:0}]);
	assert.ok(timeline.textContent.includes(` + string(encodedName) + `));
	assert.ok(timeline.textContent.includes('0s'));`
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("tooltip regression: %v\n%s", err, output)
	}
}
func TestTimingChartSortingScriptEscapesFunctionMarkers(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	name := "__f__alert(1)__f__"
	chart := testTimingChart([]TestInfo{{
		Identifier: []string{name}, StartedAt: start, FinishedAt: start.Add(time.Second),
	}})
	html := string(chart.RenderContent())
	if strings.Contains(html, name) {
		t.Fatal("sorting group names must not expose go-echarts function markers to renderer substitution")
	}
	if !strings.Contains(html, `\u005f\u005ff\u005f\u005falert(1)\u005f\u005ff\u005f\u005f`) {
		t.Fatal("sorting group names must remain encoded as JavaScript string data")
	}
}

func TestTimingChartSortingScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to execute chart sorting regression tests")
	}
	groups := []timingChartGroup{
		{Name: "alpha", StartTime: 200, Duration: 7000, FirstRow: 0, Segments: []timingChartSegment{
			{Name: "create", Kind: "create-cluster", Color: "#2563eb", Start: 0, End: 5000},
			{Name: "nodepool", Kind: "create-nodepool", Color: "#0d9488", Start: 3000, End: 7000},
		}},
		{Name: "beta", StartTime: 100, Duration: 12000, FirstRow: 1},
	}
	sorting, err := json.Marshal(timingChartSortingJS("mockChart", groups, 120000))
	if err != nil {
		t.Fatal(err)
	}
	script := `const assert = require('node:assert/strict');
const listeners = {};
const windowListeners = {};
const sort = {value: 'start', addEventListener: (event, handler) => listeners[event] = handler};
let containerTop = 405;
const document = {getElementById: id => id === 'timing-sort' ? sort : null, querySelector: selector => selector === '.container' ? {getBoundingClientRect:()=>({top:containerTop})} : null, addEventListener: (event, handler) => listeners['document:' + event] = handler};
const window = {innerHeight:1000,scrollY:0,addEventListener:(event,handler)=>windowListeners[event]=handler};
window.top = window;
const labels = ['alpha', 'beta'];
const offsets = [{value:0},{value:50000}];
const durations = [{value:7000},{value:12000}];
const state = {yAxis:[{data:labels}],series:[{data:offsets},{data:durations}],dataZoom:[{start:0,end:100},{start:0,end:100}]};
const chart = {
  updates: [],
  height: 600,
  dom: {style:{height:'60vh'},getBoundingClientRect:()=>({top:435}), contains: target => Boolean(target && target.inside)},
  getOption: () => state,
  getZr: () => ({on: (event, handler) => listeners['zr:' + event] = handler}),
  getModel: () => ({getComponent: () => ({coordinateSystem: {getRect: () => ({x:0, y:0, width:100, height:100})}})}),
  getHeight: () => chart.height,
  getDom: () => chart.dom,
	resize: ({height}) => chart.height = height,
	on: (event, handler) => listeners['chart:' + event] = handler,
	setOption: option => {
		chart.updates.push(option);
		if (option.yAxis) state.yAxis = [{...state.yAxis[0], ...option.yAxis}];
		if (option.series) state.series = option.series.map((s, i) => ({...state.series[i], ...s}));
		if (option.dataZoom) state.dataZoom = option.dataZoom;
		if (option.tooltip) state.tooltip = {...(state.tooltip || {}), ...option.tooltip};
	},
	dispatchAction: action => {
		chart.actions = chart.actions || [];
		chart.actions.push(action);
		if (!action.dataZoomId) return;
		const zoom = state.dataZoom.find(item => item.id === action.dataZoomId);
		Object.assign(zoom, {start:action.start,end:action.end});
		listeners['chart:datazoom']({batch:[action]});
	}
};
require('node:vm').runInNewContext(` + string(sorting) + `, {mockChart:chart, document, window, Math, Number, echarts:{graphic:{clipRectByRect:shape=>shape}}});
containerTop = 435;
windowListeners.load();
assert.equal(chart.updates[chart.updates.length - 1].tooltip.triggerOn, 'click', 'the timeline detail must be pinned by a click, not by hover');
assert.ok(!state.tooltip.alwaysShowContent, 'the detail starts unpinned');
const showTips = () => (chart.actions || []).filter(action => action.type === 'showTip').map(action => [action.x, action.y]);
const hideTips = () => (chart.actions || []).filter(action => action.type === 'hideTip').length;
listeners['zr:click']({offsetX:50, offsetY:25});
assert.equal(state.tooltip.alwaysShowContent, true, 'an in-plot click pins the detail');
assert.deepEqual(showTips(), [[50,25]], 'the clicked position is the pinned one');
listeners['zr:click']({offsetX:50, offsetY:75});
assert.deepEqual(showTips(), [[50,25],[50,75]], 'another row replaces the pinned detail');
assert.equal(state.tooltip.alwaysShowContent, true, 'the replacement stays pinned');
listeners['document:click']({target:{inside:true}});
assert.equal(state.tooltip.alwaysShowContent, true, 'a page click inside the chart keeps the pin');
listeners['document:click']({target:{}});
assert.equal(state.tooltip.alwaysShowContent, false, 'a page click outside the chart dismisses the pin');
assert.equal(hideTips(), 1, 'dismissal asks ECharts to hide the tooltip');
listeners['zr:click']({offsetX:50, offsetY:25});
assert.equal(state.tooltip.alwaysShowContent, true, 'an in-plot click pins again');
listeners['zr:click']({offsetX:150, offsetY:150});
assert.equal(state.tooltip.alwaysShowContent, false, 'an in-chart click outside the plot dismisses the pin');
assert.equal(hideTips(), 2);
const sortedLabels = () => state.yAxis[0].data;
assert.deepEqual(JSON.parse(JSON.stringify(sortedLabels())), ['beta','alpha']);
assert.ok(chart.height >= 640);
assert.equal(chart.dom.style.height, chart.height + 'px');
assert.equal(state.dataZoom[0].end, 100);
	const viewportHeight = chart.height;
sort.value = 'name'; listeners.change();
assert.equal(sortedLabels()[0], 'alpha');
const shapes = state.series[0].renderItem({dataIndex:0,coordSys:{}}, {value:i=>state.series[0].data[0].value[i], coord:([x,y])=>[x,y*26], size:()=>[0,26]}).children;
assert.deepEqual(Array.from(shapes, s=>[s.shape.x,s.shape.width,s.style.fill]), [[0,3000,'#2563eb'],[3000,2000,'#2563eb'],[3000,2000,'#0d9488'],[5000,2000,'#0d9488']]);
sort.value = 'duration'; listeners.change();
assert.equal(sortedLabels()[0], 'beta');
assert.equal(chart.height, viewportHeight);
assert.equal(state.dataZoom[0].end, 100);
const initialTimeSpan = state.dataZoom[1].end - state.dataZoom[1].start;
Object.assign(state.dataZoom[0], {start:0,end:40});
listeners['chart:datazoom']({batch:[{dataZoomId:'timing-rows',start:0,end:40}]});
	assert.equal(chart.height, viewportHeight, 'row zoom must not move the slider geometry');
	assert.equal(chart.dom.style.height, viewportHeight + 'px');
const zoomedTimeSpan = state.dataZoom[1].end - state.dataZoom[1].start;
assert.ok(zoomedTimeSpan < initialTimeSpan);
Object.assign(state.dataZoom[0], {start:0,end:100});
listeners['chart:datazoom']({batch:[{dataZoomId:'timing-rows',start:0,end:100}]});
	assert.equal(chart.height, viewportHeight);
	assert.equal(chart.dom.style.height, viewportHeight + 'px');
assert.ok(state.dataZoom[1].end - state.dataZoom[1].start > zoomedTimeSpan);
window.innerHeight = 800;
windowListeners.resize();
assert.ok(chart.height < viewportHeight && chart.height >= 640);
assert.equal(chart.dom.style.height, chart.height + 'px');
containerTop = 700;
window.innerHeight = 600;
windowListeners.resize();
assert.ok(chart.height >= 640);
assert.equal(chart.dom.style.height, chart.height + 'px');
assert.equal(sortedLabels().length, 2);
assert.equal(state.dataZoom[0].end, 100);`
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("chart sorting and zoom regression: %v\n%s", err, output)
	}
}

func TestTimingChartOutcomeFiltering(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to execute outcome filtering regression tests")
	}
	for _, includeUnknown := range []bool{false, true} {
		name := "known-results"
		if includeUnknown {
			name = "unknown-results"
		}
		t.Run(name, func(t *testing.T) {
			outcomes := []string{"passed", "skipped", "pending", "failed", "timedout", "panicked", "interrupted", "aborted"}
			if includeUnknown {
				outcomes = append(outcomes, "future-result", "")
			}
			groups := make([]timingChartGroup, len(outcomes))
			for i, outcome := range outcomes {
				groups[i] = timingChartGroup{
					Name: outcome + "-test", Outcome: outcome,
					StartTime: int64(len(outcomes) - i), Duration: float64((i + 1) * 1000), FirstRow: i,
				}
			}
			encodedGroups, err := json.Marshal(groups)
			if err != nil {
				t.Fatal(err)
			}
			encodedScript, err := json.Marshal(timingChartSortingJS("mockChart", groups, 200000))
			if err != nil {
				t.Fatal(err)
			}
			script := `const assert = require('node:assert/strict');
const groups = ` + string(encodedGroups) + `;
const controls = {};
for (const key of ['skipped', 'passed', 'failed']) {
	controls['timing-show-' + key] = {checked:true, listeners:{}, addEventListener(event, handler) {this.listeners[event] = handler;}};
}
const sort = {value:'start', listeners:{}, addEventListener(event, handler) {this.listeners[event] = handler;}};
controls['timing-sort'] = sort;
controls['timing-visible-count'] = {textContent:''};
const listeners = {};
const windowListeners = {};
const state = {
	yAxis:[{data:groups.map(group => group.name)}],
	series:[{data:groups.map((_, i) => ({value:1000 + i * 13000}))},{data:groups.map(group => ({value:group.duration}))}],
	dataZoom:[{start:0,end:100},{start:0,end:100}]
};
const chart = {
	height:600, dom:{style:{}, getBoundingClientRect:()=>({top:400})},
	getOption:()=>state, getZr:()=>({on:()=>{}}), getHeight:()=>chart.height, getDom:()=>chart.dom,
	resize:({height})=>chart.height=height,
	on:(event, handler)=>listeners[event]=handler,
	setOption:option=>{
		if (option.xAxis) state.xAxis = option.xAxis;
		if (option.yAxis) state.yAxis = [{...state.yAxis[0], ...option.yAxis}];
		if (option.series) state.series = option.series;
		if (option.dataZoom) state.dataZoom = option.dataZoom;
		if (option.tooltip) state.tooltip = option.tooltip;
	},
	dispatchAction:action=>{
		Object.assign(state.dataZoom.find(zoom=>zoom.id===action.dataZoomId), {start:action.start,end:action.end});
		listeners.datazoom?.({batch:[action]});
	}
};
require('node:vm').runInNewContext(` + string(encodedScript) + `, {
	mockChart:chart, Math, Number,
	document:{getElementById:id=>controls[id] || null, querySelector:()=>({getBoundingClientRect:()=>({top:400})})},
	window:{innerHeight:1000,scrollY:0,addEventListener:(event, handler)=>windowListeners[event]=handler}
});
const category = outcome => outcome === 'passed' ? 'passed' : ['skipped','pending'].includes(outcome) ? 'skipped' : ['failed','timedout','panicked','interrupted','aborted'].includes(outcome) ? 'failed' : null;
const assertVisible = () => {
	const expected = groups.filter(group => !category(group.outcome) || controls['timing-show-' + category(group.outcome)].checked);
	expected.sort((a,b) => sort.value === 'name' ? a.name.localeCompare(b.name) : sort.value === 'duration' ? b.duration-a.duration : a.startTime-b.startTime);
	assert.deepEqual(Array.from(state.yAxis[0].data), expected.map(group=>group.name));
	assert.equal(state.series[0].data.length, expected.length);
	if (!expected.length) assert.equal(state.tooltip.formatter({seriesId:'timing-tests',dataIndex:0}), '');
	expected.forEach((group, i) => {
		const row = state.series[0].data[i];
		const offset = 1000 + group.firstRow * 13000;
		assert.equal(row.name, group.name);
		assert.deepEqual(Array.from(row.value), [i, offset, offset + group.duration]);
	});
	assert.ok(Number.isFinite(chart.height));
	for (const zoom of state.dataZoom) {
		assert.ok(Number.isFinite(zoom.start));
		assert.ok(Number.isFinite(zoom.end));
		assert.ok(zoom.start >= 0 && zoom.end <= 100 && zoom.start <= zoom.end);
	}
};
const toggle = (key, checked) => {
	const control = controls['timing-show-' + key];
	control.checked = checked;
	control.listeners.change();
	assertVisible();
};
assertVisible();
for (const key of ['passed','skipped','failed']) {
	toggle(key, false);
	toggle(key, true);
}
toggle('passed', false);
toggle('skipped', false);
toggle('failed', false);
assert.equal(state.series[0].data.length, groups.filter(group=>!category(group.outcome)).length);
listeners.datazoom({batch:[{dataZoomId:'timing-rows',start:0,end:100}]});
windowListeners.resize();
windowListeners.load();
assertVisible();
sort.value = 'duration';
sort.listeners.change();
assertVisible();
toggle('failed', true);
toggle('passed', true);
toggle('skipped', true);
sort.value = 'name';
sort.listeners.change();
assertVisible();
sort.value = 'start';
sort.listeners.change();
assertVisible();`
			if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
				t.Fatalf("outcome filtering regression: %v\n%s", err, output)
			}
		})
	}
}

func TestTimingChartUncoveredTooltip(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to execute uncovered tooltip regression tests")
	}
	groups := []timingChartGroup{{
		Name: `<img src=x onerror=alert(1)> & "quoted"`, Duration: 10000, FullLifecycle: true,
		Segments: []timingChartSegment{
			{Name: "Test work", Kind: "phase", Color: "#9333ea", Start: 50000, End: 60000},
			{Name: "No recorded operation", Kind: "gap", Color: "#64748b", Start: 50000, End: 50500},
			{Name: "operation", Kind: "create-cluster", Color: "#2563eb", Start: 50500, End: 54000},
			{Name: "No recorded operation", Kind: "gap", Color: "#64748b", Start: 54000, End: 54500},
			{Name: "operation", Kind: "create-cluster", Color: "#2563eb", Start: 54500, End: 58000},
			{Name: "No recorded operation", Kind: "gap", Color: "#64748b", Start: 58000, End: 60000},
		},
	}}
	groups = append(groups,
		timingChartGroup{Name: "stages", Duration: 10000, FullLifecycle: true, Segments: []timingChartSegment{
			{Name: "Phase: Setup <literal>", Kind: "phase", Color: "#9333ea", Start: 51000, End: 54000},
			{Name: "Phase: Nested", Kind: "phase", Color: "#9333ea", Start: 52000, End: 52500},
			{Name: "Phase: Cleanup", Kind: "phase", Color: "#9333ea", Start: 54000, End: 58000},
			{Name: "contained setup (succeeded)", Kind: "operation", Color: "#2563eb", Start: 53000, End: 53500},
			{Name: "contained cleanup", Kind: "operation", Color: "#2563eb", Start: 55000, End: 55500},
			{Name: "crossing operation", Kind: "operation", Color: "#2563eb", Start: 53500, End: 54500},
			{Name: "nested operation", Kind: "operation", Color: "#2563eb", Start: 52100, End: 52400},
			{Name: "boundary point", Kind: "operation", Color: "#2563eb", Start: 54000, End: 54000},
			{Name: "partial outside", Kind: "operation", Color: "#2563eb", Start: 57500, End: 59000},
			{Name: "outside operation", Kind: "operation", Color: "#2563eb", Start: 59000, End: 59500},
			{Name: "setup gap", Kind: "gap", Color: "#64748b", Start: 51000, End: 51100},
			{Name: "cleanup gap", Kind: "gap", Color: "#64748b", Start: 55500, End: 55700},
		}},
		timingChartGroup{Name: "unstaged", Duration: 1000, Segments: []timingChartSegment{
			{Name: "legacy operation", Kind: "operation", Color: "#2563eb", Start: 50000, End: 51000},
			{Name: "No recorded operation during test work", Kind: "gap", Color: "#64748b", Start: 50000, End: 50500},
			{Name: "No recorded operation during cleanup", Kind: "gap", Color: "#64748b", Start: 50500, End: 51000},
			{Name: "terminal operation", Kind: "operation", Color: "#2563eb", Start: 51000, End: 51000},
		}},
		timingChartGroup{Name: "zero", FullLifecycle: true, Segments: []timingChartSegment{
			{Name: "Phase: Instant", Kind: "phase", Color: "#9333ea", Start: 50000, End: 50000},
			{Name: "instant operation", Kind: "operation", Color: "#2563eb", Start: 50000, End: 50000},
		}},
		timingChartGroup{Name: "fractional endpoint", Duration: 1000.000002, FullLifecycle: true, Segments: []timingChartSegment{
			{Name: "Phase: Setup", Kind: "phase", Color: "#9333ea", Start: 10000.000001, End: 10500.000002},
			{Name: "Phase: Cleanup", Kind: "phase", Color: "#9333ea", Start: 10500.000002, End: 11000.000003},
			{Name: "initial instant", Kind: "operation", Color: "#2563eb", Start: 10000.000001, End: 10000.000001},
			{Name: "internal instant", Kind: "operation", Color: "#2563eb", Start: 10500.000002, End: 10500.000002},
			{Name: "final instant", Kind: "operation", Color: "#2563eb", Start: 11000.000003, End: 11000.000003},
		}},
	)
	for i := range groups {
		groups[i].StartTime = int64(i)
		groups[i].FirstRow = i
	}
	encodedGroups, err := json.Marshal(groups)
	if err != nil {
		t.Fatal(err)
	}
	encodedScript, err := json.Marshal(timingChartSortingJS("mockChart", groups, 120000))
	if err != nil {
		t.Fatal(err)
	}
	script := `const assert = require('node:assert/strict');
const groups = ` + string(encodedGroups) + `;
const createElement = tag => {
	let text = '';
	return {
		tagName:tag.toUpperCase(), style:{}, children:[], open:false,
		append(...nodes) {this.children.push(...nodes);},
		set textContent(value) {text = String(value); this.children.length = 0;},
		get textContent() {return text + this.children.map(node=>node.textContent).join('');},
		set innerHTML(_) {throw Error('unsafe HTML');}
	};
};
const sort = {value:'start', addEventListener:()=>{}};
const offsets = [50000,50000,50000,50000,10000.000001];
const state = {yAxis:[{data:[]}],series:[{data:groups.map((g,i)=>({value:offsets[i]}))},{data:groups.map(g=>({value:g.duration}))}],dataZoom:[{start:0,end:100},{start:0,end:100}]};
const chart = {
	height:600, dom:{style:{}}, getOption:()=>state, getZr:()=>({on:()=>{}}), getHeight:()=>chart.height, getDom:()=>chart.dom,
	resize:({height})=>chart.height=height, on:()=>{}, dispatchAction:()=>{},
	setOption:option=>{
		if (option.yAxis) state.yAxis = [{...state.yAxis[0], ...option.yAxis}];
		if (option.series) state.series = option.series;
		if (option.dataZoom) state.dataZoom = option.dataZoom;
		if (option.tooltip) state.tooltip = option.tooltip;
	}
};
require('node:vm').runInNewContext(` + string(encodedScript) + `, {
	mockChart:chart, Math, Number, echarts:{graphic:{clipRectByRect:shape=>shape}},
	document:{createElement,getElementById:id=>id==='timing-sort'?sort:null,querySelector:()=>({getBoundingClientRect:()=>({top:400})})},
	window:{innerHeight:1000,scrollY:0,addEventListener:()=>{}}
});
const content = state.tooltip.formatter([{seriesId:'timing-tests',dataIndex:0}]);
const descendants = node => [node, ...node.children.flatMap(descendants)];
const nodes = descendants(content);
assert.ok(content.textContent.includes('<img src=x onerror=alert(1)> & "quoted"'));
assert.match(content.textContent, /3s/);
assert.match(content.textContent, /30(?:\.0+)?%/);
const details = nodes.filter(node=>node.tagName==='DETAILS' && node.children[0]?.tagName==='SUMMARY' && node.children[0].textContent.startsWith('Small uncovered intervals'));
assert.equal(details.length, 1);
assert.equal(details[0].open, false);
assert.equal(details[0].children[0].tagName, 'SUMMARY');
const shortNodes = details[0].children.slice(1).flatMap(descendants);
assert.equal(shortNodes.filter(node=>node.textContent==='<1s' && !node.children.length).length, 2);
const tracks = shortNodes.filter(node=>node.style.position==='absolute');
assert.deepEqual(tracks.map(node=>[node.style.left,node.style.width]), [['0%','5%'],['40%','5%']]);
assert.ok(nodes.some(node=>node.style.position==='absolute' && node.style.left==='80%' && node.style.width==='20%'));
const row = state.series[0].data[0];
const shapes = state.series[0].renderItem({dataIndex:0,coordSys:{}}, {value:i=>row.value[i],coord:([x,y])=>[x,y*26],size:()=>[0,26]}).children;
assert.deepEqual(Array.from(shapes, shape=>[shape.shape.x,shape.shape.width,shape.style.fill]), [
	[50000,500,'#64748b'],[50500,3500,'#2563eb'],[54000,500,'#64748b'],[54500,3500,'#2563eb'],[58000,2000,'#64748b']
]);
const stageContent = state.tooltip.formatter([{seriesId:'timing-tests',dataIndex:1}]);
const sections = stageContent.children.filter(node=>node.tagName==='SECTION');
const section = name => sections.find(node=>descendants(node).some(child=>child.tagName==='H3' && child.textContent===name));
const setup = section('Setup <literal>'), cleanup = section('Cleanup');
assert.ok(setup.textContent.includes('contained setup (succeeded)'));
assert.ok(!setup.textContent.includes('contained cleanup'));
assert.ok(cleanup.textContent.includes('contained cleanup'));
assert.ok(cleanup.textContent.includes('boundary point'));
assert.ok(!setup.textContent.includes('boundary point'));
for (const group of [setup,cleanup]) {
	const stageDetails = group.children.filter(node=>node.tagName==='DETAILS');
	assert.equal(stageDetails.length,1);
	assert.equal(stageDetails[0].open,false);
	assert.ok(descendants(stageDetails[0]).filter(node=>node.style.position==='absolute').length>0);
}
const across = section('Across recorded stages');
assert.ok(across.textContent.includes('Setup <literal>') && across.textContent.includes('Cleanup'));
assert.ok(across.textContent.includes('Nested'));
assert.ok(across.textContent.includes('outside recorded stages'));
for (const operation of ['crossing operation','nested operation','partial outside']) {
	assert.equal(descendants(stageContent).filter(node=>!node.children.length && node.textContent.startsWith(operation)).length,1);
}
const crossTrack = descendants(across).find(node=>node.style.position==='absolute' && node.style.left==='35%');
assert.equal(crossTrack.style.width,'10%');
assert.ok(section('Outside recorded stages').textContent.includes('outside operation'));
const legacy = state.tooltip.formatter([{seriesId:'timing-tests',dataIndex:2}]);
assert.ok(legacy.textContent.includes('cleanup coverage unknown'));
assert.ok(descendants(legacy).some(node=>node.tagName==='H3' && node.textContent==='Outside recorded stages'));
assert.equal(descendants(legacy).filter(node=>node.textContent==='legacy operation').length,1);
assert.ok(legacy.textContent.includes('No recorded operation during test work'));
assert.ok(legacy.textContent.includes('No recorded operation during cleanup'));
const zero = state.tooltip.formatter([{seriesId:'timing-tests',dataIndex:3}]);
assert.ok(zero.textContent.includes('0s'));
assert.ok(!zero.textContent.includes('NaN') && !zero.textContent.includes('Infinity'));
assert.equal(descendants(zero).filter(node=>node.textContent==='instant operation').length,1);
assert.ok(descendants(zero).some(node=>node.tagName==='H3' && node.textContent==='Instant'));
const fractional = state.tooltip.formatter([{seriesId:'timing-tests',dataIndex:4}]);
const fractionalSections = fractional.children.filter(node=>node.tagName==='SECTION');
const fractionalSection = name => fractionalSections.find(node=>descendants(node).some(child=>child.tagName==='H3' && child.textContent===name));
assert.ok(fractionalSection('Setup').textContent.includes('initial instant'));
assert.ok(!fractionalSection('Setup').textContent.includes('internal instant'));
assert.ok(fractionalSection('Cleanup').textContent.includes('internal instant'));
assert.ok(fractionalSection('Cleanup').textContent.includes('final instant'));
const finalMarker = descendants(fractionalSection('Cleanup')).find(node=>node.style && typeof node.style.left === 'string' && node.style.left.includes('calc(100% - 2px)'));
assert.ok(finalMarker, 'fractional final instant must be clamped inside the track');
assert.equal(fractionalSection('Outside recorded stages'),undefined);`
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("uncovered tooltip regression: %v\n%s", err, output)
	}
}

func TestTimingChartSortingScriptAutoHeightIframe(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to execute iframe chart regression tests")
	}
	groups := make([]timingChartGroup, 75)
	for i := range groups {
		groups[i] = timingChartGroup{Name: "test", StartTime: int64(i), Duration: 1000, FirstRow: i}
	}
	encodedGroups, err := json.Marshal(groups)
	if err != nil {
		t.Fatal(err)
	}
	encodedScript, err := json.Marshal(timingChartSortingJS("mockChart", groups, 200000))
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"same-origin", "cross-origin"} {
		t.Run(scenario, func(t *testing.T) {
			encodedScenario, err := json.Marshal(scenario)
			if err != nil {
				t.Fatal(err)
			}
			script := `const assert = require('node:assert/strict');
const sameOrigin = ` + string(encodedScenario) + ` === 'same-origin';
const groups = ` + string(encodedGroups) + `;
const listeners = {};
const childListeners = {};
const hostListeners = {};
const addListener = target => (event, handler) => (target[event] ??= []).push(handler);
const dispatch = (target, event) => (target[event] || []).forEach(handler => handler());
const sort = {value:'start', addEventListener:addListener(listeners)};
const chartTop = 435;
const scrollY = 100;
const state = {
	yAxis:[{data:groups.map(group=>group.name)}],
	series:[{data:groups.map((_,i)=>({value:i*1000}))},{data:groups.map(group=>({value:group.duration}))}],
	dataZoom:[{start:0,end:100},{start:0,end:100}]
};
const chart = {
	height:600,
	dom:{style:{height:'600px'}},
	getOption:()=>state, getZr:()=>({on:()=>{}}), getDom:()=>chart.dom, getHeight:()=>chart.height,
	resize:({height})=>chart.height=height,
	on:(event,handler)=>listeners['chart:'+event]=[handler],
	setOption:option=>{
		if (option.yAxis) state.yAxis=[{...state.yAxis[0], ...option.yAxis}];
		if (option.series) state.series=option.series;
		if (option.dataZoom) state.dataZoom=option.dataZoom;
	},
	dispatchAction:action=>{
		Object.assign(state.dataZoom.find(zoom=>zoom.id===action.dataZoomId), action);
		(listeners['chart:datazoom'] || []).forEach(handler=>handler({batch:[action]}));
	}
};
const host = {innerHeight:1000, addEventListener:addListener(hostListeners)};
const inaccessibleHost = {
	get innerHeight() {throw Error('cross-origin top.innerHeight');},
	addEventListener:()=>{throw Error('cross-origin top.addEventListener');}
};
const window = {
	top:sameOrigin ? host : inaccessibleHost,
	scrollY,
	// Prow sizes the iframe from its content, including the report's 30px spacer.
	// Using this viewport for chart height adds 30 - 16 = 14px on every resize.
	get innerHeight() {return chartTop + chart.height + 30;},
	addEventListener:addListener(childListeners)
};
require('node:vm').runInNewContext(` + string(encodedScript) + `, {
	mockChart:chart, window, Math, Number,
	document:{
		getElementById:id=>id==='timing-sort'?sort:null,
		querySelector:()=>({getBoundingClientRect:()=>({top:chartTop-scrollY})})
	}
});
dispatch(childListeners, 'load');
const assertStableOverview = expected => {
	const heights = [];
	for (let i = 0; i < 20; i++) {
		dispatch(childListeners, 'resize');
		heights.push(chart.height);
	}
	assert.deepEqual(heights, Array(20).fill(expected), 'auto-height iframe must settle without 14px resize feedback');
	assert.equal(chart.dom.style.height, expected+'px');
	assert.equal(state.dataZoom[0].end, 100);
};
const initialOverviewHeight = chart.height;
assert.ok(initialOverviewHeight >= 640);
assertStableOverview(initialOverviewHeight);
if (sameOrigin) {
	host.innerHeight = 1200;
	dispatch(hostListeners, 'resize');
	assert.ok(chart.height > initialOverviewHeight, 'host resize must update the overview before an iframe resize');
	assertStableOverview(chart.height);
}
	const viewportHeight = chart.height;
	for (const [start, end] of [[0,99],[0,95],[20,40],[20,21],[0,100]]) {
		chart.dispatchAction({type:'dataZoom',dataZoomId:'timing-rows',start,end});
		dispatch(childListeners, 'resize');
		assert.equal(chart.height, viewportHeight, 'row zoom must keep the slider track stable');
		assert.equal(chart.dom.style.height, viewportHeight+'px');
		assert.equal(state.dataZoom[0].start, start);
		assert.equal(state.dataZoom[0].end, end);
	}
	chart.dispatchAction({type:'dataZoom',dataZoomId:'timing-rows',start:20,end:40});
	const timeZoom = {...state.dataZoom[1]};
	const beforeHostResize = chart.height;
	host.innerHeight = 1400;
	dispatch(hostListeners, 'resize');
	for (let i = 0; i < 20; i++) dispatch(childListeners, 'resize');
	assert.ok(sameOrigin ? chart.height > beforeHostResize : chart.height === viewportHeight, 'viewport resizing must fit the chart without resetting zoom');
assert.equal(state.dataZoom[0].start, 20);
assert.equal(state.dataZoom[0].end, 40);
assert.deepEqual({...state.dataZoom[1]}, timeZoom, 'viewport resize must preserve time zoom');
Object.assign(state.dataZoom[0], {start:0,end:100});
listeners['chart:datazoom'].forEach(handler=>handler({dataZoomId:'timing-rows',start:0,end:100}));
assertStableOverview(chart.height);`
			if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
				t.Fatalf("auto-height iframe regression: %v\n%s", err, output)
			}
		})
	}
}

func TestTimingChartZoomIncludesBoundaryTests(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to execute chart zoom regression tests")
	}
	for _, count := range []int{1, 75} {
		groups := make([]timingChartGroup, count)
		for i := range groups {
			duration := float64(1000)
			if i == 15 || count == 1 {
				duration = 80000
			}
			groups[i] = timingChartGroup{Name: "test", StartTime: int64(i), Duration: duration, FirstRow: i}
		}
		encodedGroups, err := json.Marshal(groups)
		if err != nil {
			t.Fatal(err)
		}
		encodedScript, err := json.Marshal(timingChartSortingJS("mockChart", groups, 200000))
		if err != nil {
			t.Fatal(err)
		}
		script := `const assert = require('node:assert/strict');
const groups = ` + string(encodedGroups) + `;
const listeners = {};
const state = {
	yAxis:[{data:groups.map(g=>g.name)}],
	series:[{data:groups.map((_,i)=>({value:i*1000}))},{data:groups.map(g=>({value:g.duration}))}],
	dataZoom:[{start:0,end:100},{start:0,end:100}]
};
const dom = {style:{height:'600px'}};
let height = 600;
const chart = {
	getOption:()=>state, getZr:()=>({on:()=>{}}), getDom:()=>dom, getHeight:()=>height, resize:o=>height=o.height,
	on:(event,handler)=>listeners[event]=handler,
	setOption:option=>{
		if (option.yAxis) state.yAxis=[option.yAxis];
		if (option.series) state.series=option.series;
		if (option.dataZoom) state.dataZoom=option.dataZoom;
	},
	dispatchAction:action=>Object.assign(state.dataZoom[1],action)
};
require('node:vm').runInNewContext(` + string(encodedScript) + `, {
	mockChart:chart,
	document:{getElementById:id=>id==='timing-sort'?{value:'start',addEventListener:()=>{}}:null,querySelector:()=>({getBoundingClientRect:()=>({top:400})})},
	window:{innerHeight:1000,scrollY:0,addEventListener:()=>{}}
});
const viewportHeight = height;
for (const [start,end] of [[0,20],[20,40],[20,20],[0,100]]) {
	Object.assign(state.dataZoom[0],{start,end});
	listeners.datazoom({dataZoomId:'timing-rows',start,end});
	const zoom = state.dataZoom[1];
	const boundaryStart = groups.length === 1 ? 0 : 15000;
	const boundaryEnd = boundaryStart + 80000;
	assert.ok(zoom.start/100*200000 <= boundaryStart, 'time zoom clips boundary test start');
	assert.ok(zoom.end/100*200000 >= boundaryEnd, 'time zoom clips boundary test end');
	assert.equal(height, viewportHeight, 'boundary zoom must preserve the slider track');
}`
		if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
			t.Fatalf("boundary zoom regression with %d tests: %v\n%s", count, err, output)
		}
	}
}
