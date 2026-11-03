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
	global.document = {createElement: () => ({style: {}, set innerHTML(_) {throw Error('unsafe HTML');}})};
	const formatter = ` + testTimingTooltipFormatter + `;
	const name = '<img src=x onerror=alert(1)> & "quoted"';
	for (const [value, expected] of [[0,'0s'],[1,'0.001s'],[12,'0.012s'],[999,'0.999s'],[1001,'1.001s'],[0.001,'0.000001s'],[3601001,'1h0m1.001s']]) {
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
		getElementById: id => id === 'timing-sort' ? sort : {},
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
	assert.equal(state.tooltip.formatter([{dataIndex:0}]).textContent, ` + string(encodedName) + ` + '\n0s');`
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
const document = {getElementById: id => id === 'timing-sort' ? sort : null, querySelector: selector => selector === '.container' ? {getBoundingClientRect:()=>({top:containerTop})} : null};
const window = {innerHeight:1000,scrollY:0,addEventListener:(event,handler)=>windowListeners[event]=handler};
window.top = window;
const labels = ['alpha', 'beta'];
const offsets = [{value:0},{value:50000}];
const durations = [{value:7000},{value:12000}];
const state = {yAxis:[{data:labels}],series:[{data:offsets},{data:durations}],dataZoom:[{start:0,end:100},{start:0,end:100}]};
const chart = {
  updates: [],
  height: 600,
  dom: {style:{height:'60vh'},getBoundingClientRect:()=>({top:435})},
  getOption: () => state,
  getHeight: () => chart.height,
  getDom: () => chart.dom,
	resize: ({height}) => chart.height = height,
	on: (event, handler) => listeners['chart:' + event] = handler,
	setOption: option => {
		chart.updates.push(option);
		if (option.yAxis) state.yAxis = [{...state.yAxis[0], ...option.yAxis}];
		if (option.series) state.series = option.series.map((s, i) => ({...state.series[i], ...s}));
		if (option.dataZoom) state.dataZoom = option.dataZoom;
	},
	dispatchAction: action => {
		const zoom = state.dataZoom.find(item => item.id === action.dataZoomId);
		Object.assign(zoom, {start:action.start,end:action.end});
		listeners['chart:datazoom']({batch:[action]});
	}
};
require('node:vm').runInNewContext(` + string(sorting) + `, {mockChart:chart, document, window, Math, Number, echarts:{graphic:{clipRectByRect:shape=>shape}}});
containerTop = 435;
windowListeners.load();
const sortedLabels = () => state.yAxis[0].data;
assert.deepEqual(JSON.parse(JSON.stringify(sortedLabels())), ['beta','alpha']);
assert.equal(chart.height, 549);
assert.equal(chart.dom.style.height, '549px');
assert.equal(state.dataZoom[0].end, 100);
sort.value = 'name'; listeners.change();
assert.equal(sortedLabels()[0], 'alpha');
const shapes = state.series[0].renderItem({dataIndex:0,coordSys:{}}, {value:i=>state.series[0].data[0].value[i], coord:([x,y])=>[x,y*26], size:()=>[0,26]}).children;
assert.deepEqual(Array.from(shapes, s=>[s.shape.x,s.shape.width,s.style.fill]), [[0,3000,'#2563eb'],[3000,2000,'#2563eb'],[3000,2000,'#0d9488'],[5000,2000,'#0d9488']]);
sort.value = 'duration'; listeners.change();
assert.equal(sortedLabels()[0], 'beta');
assert.equal(chart.height, 549);
assert.equal(state.dataZoom[0].end, 100);
const initialTimeSpan = state.dataZoom[1].end - state.dataZoom[1].start;
Object.assign(state.dataZoom[0], {start:0,end:40});
listeners['chart:datazoom']({batch:[{dataZoomId:'timing-rows',start:0,end:40}]});
assert.equal(chart.height, 320);
assert.equal(chart.dom.style.height, '320px');
const zoomedTimeSpan = state.dataZoom[1].end - state.dataZoom[1].start;
assert.ok(zoomedTimeSpan < initialTimeSpan);
Object.assign(state.dataZoom[0], {start:0,end:100});
listeners['chart:datazoom']({batch:[{dataZoomId:'timing-rows',start:0,end:100}]});
assert.equal(chart.height, 549);
assert.equal(chart.dom.style.height, '549px');
assert.ok(state.dataZoom[1].end - state.dataZoom[1].start > zoomedTimeSpan);
window.innerHeight = 800;
windowListeners.resize();
assert.equal(chart.height, 349);
assert.equal(chart.dom.style.height, '349px');
containerTop = 700;
window.innerHeight = 600;
windowListeners.resize();
assert.equal(chart.height, 320);
assert.equal(chart.dom.style.height, '320px');
assert.equal(sortedLabels().length, 2);
assert.equal(state.dataZoom[0].end, 100);`
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("chart sorting and zoom regression: %v\n%s", err, output)
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
	getOption:()=>state, getDom:()=>chart.dom, getHeight:()=>chart.height,
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
		getElementById:()=>sort,
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
assertStableOverview(sameOrigin ? 549 : 320);
if (sameOrigin) {
	host.innerHeight = 1200;
	dispatch(hostListeners, 'resize');
	assert.equal(chart.height, 749, 'host resize must update the overview before an iframe resize');
	assertStableOverview(749);
}
// Row zoom remains content-sized even when the host viewport is inaccessible.
Object.assign(state.dataZoom[0], {start:20,end:40});
listeners['chart:datazoom'].forEach(handler=>handler({dataZoomId:'timing-rows',start:20,end:40}));
assert.equal(chart.height, 546);
const timeZoom = {...state.dataZoom[1]};
host.innerHeight = 1400;
dispatch(hostListeners, 'resize');
for (let i = 0; i < 20; i++) dispatch(childListeners, 'resize');
assert.equal(chart.height, 546, 'viewport resize must preserve row zoom height');
assert.equal(chart.dom.style.height, '546px');
assert.equal(state.dataZoom[0].start, 20);
assert.equal(state.dataZoom[0].end, 40);
assert.deepEqual({...state.dataZoom[1]}, timeZoom, 'viewport resize must preserve time zoom');
Object.assign(state.dataZoom[0], {start:0,end:100});
listeners['chart:datazoom'].forEach(handler=>handler({dataZoomId:'timing-rows',start:0,end:100}));
assertStableOverview(sameOrigin ? 949 : 320);`
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
	getOption:()=>state, getDom:()=>dom, getHeight:()=>height, resize:o=>height=o.height,
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
	document:{getElementById:()=>({value:'start',addEventListener:()=>{}}),querySelector:()=>({getBoundingClientRect:()=>({top:400})})},
	window:{innerHeight:1000,scrollY:0,addEventListener:()=>{}}
});
for (const [start,end] of [[0,20],[20,40],[20,20],[0,100]]) {
	Object.assign(state.dataZoom[0],{start,end});
	listeners.datazoom({dataZoomId:'timing-rows',start,end});
	const zoom = state.dataZoom[1];
	const boundaryStart = groups.length === 1 ? 0 : 15000;
	const boundaryEnd = boundaryStart + 80000;
	assert.ok(zoom.start/100*200000 <= boundaryStart, 'time zoom clips boundary test start');
	assert.ok(zoom.end/100*200000 >= boundaryEnd, 'time zoom clips boundary test end');
	if (start !== 0 || end !== 100) {
		const first = Math.round((groups.length-1)*start/100);
		const last = Math.round((groups.length-1)*end/100);
		assert.equal(height,Math.max(320,130+(last-first+1)*26));
	}
}`
		if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
			t.Fatalf("boundary zoom regression with %d tests: %v\n%s", count, err, output)
		}
	}
}
