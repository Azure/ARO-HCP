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
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-echarts/go-echarts/v2/opts"
)

func TestTestTimingChartLinearData(t *testing.T) {
	for _, count := range []int{1, 10, 77, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			var times []TestInfo
			for i := range count {
				times = append(times, TestInfo{
					Identifier: []string{fmt.Sprintf("test-%d", i)},
					StartedAt:  start, FinishedAt: start.Add(time.Second),
					Steps: []StepTimingMetadata{{Name: "step", StartedAt: start, FinishedAt: start.Add(time.Second)}},
				})
			}
			chart := testTimingChart(times)
			if len(chart.MultiSeries) != 2 {
				t.Fatalf("expected two series regardless of test count, got %d", len(chart.MultiSeries))
			}
			for _, series := range chart.MultiSeries {
				if got := len(series.Data.([]opts.BarData)); got != 2*count {
					t.Errorf("%s: expected one item per test/step row, got %d", series.Name, got)
				}
			}
			if chart.Animation == nil || *chart.Animation || chart.Initialization.Renderer != "canvas" {
				t.Fatal("expected nonanimated canvas chart")
			}
		})
	}
}

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
	var offsets, durations []opts.BarData
	allOffsets := chart.MultiSeries[0].Data.([]opts.BarData)
	for i, row := range chart.MultiSeries[1].Data.([]opts.BarData) {
		if strings.Contains(row.Name, ": Untracked") {
			continue
		}
		offsets = append(offsets, allOffsets[i])
		durations = append(durations, row)
	}
	for i, want := range []struct {
		name             string
		offset, duration float64
	}{
		{"first", 0, 2000}, {"first: zero", 1000, 0}, {"first: short", 1500.5, 0.001},
		{"later", 3000, 0}, {"first", 4000, 1000},
	} {
		if offsets[i].Value != want.offset || durations[i].Value != want.duration || durations[i].Name != want.name {
			t.Errorf("row %d: got %v / %+v, expected %+v", i, offsets[i].Value, durations[i], want)
		}
	}
	if durations[0].ItemStyle.Color != durations[1].ItemStyle.Color || durations[0].ItemStyle.Color != durations[4].ItemStyle.Color {
		t.Error("steps and repeated tests must keep their test color")
	}
	if durations[0].ItemStyle.Color == durations[3].ItemStyle.Color {
		t.Error("adjacent tests should have distinct colors")
	}
	if durations[0].ItemStyle.BorderWidth != 1 || durations[1].ItemStyle.BorderWidth != 0 {
		t.Error("test rows, but not step rows, should have borders")
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
	require('node:vm').runInNewContext(` + string(encodedScript) + `, {
		document: {getElementById: () => ({})},
		window: {addEventListener: () => {}},
		echarts: {init: () => ({setOption: o => options.push(o)})}
	});
	assert.equal(options[0].series[1].data[0].name, ` + string(encodedName) + `);
	assert.equal(options[0].yAxis[0].data[0], ` + string(encodedName) + `);`
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
		{Name: "alpha", StartTime: 200, Duration: 7, FirstRow: 0, LastRow: 50},
		{Name: "beta", StartTime: 100, Duration: 12, FirstRow: 50, LastRow: 100},
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
const labels = Array.from({length:100}, (_, i) => i < 50 ? (i === 0 ? 'alpha root' : i === 1 ? 'alpha step' : 'alpha row ' + i) : (i === 50 ? 'beta root' : i === 51 ? 'beta step' : 'beta row ' + i));
const offsets = labels.map((_, i) => ({value:i * 1000}));
const durations = labels.map((name, i) => ({name,value:30,itemStyle:{color:'#e33',borderWidth:1,opacity:0.7}}));
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
require('node:vm').runInNewContext(` + string(sorting) + `, {mockChart:chart, document, window, Math, Number});
containerTop = 435;
windowListeners.load();
const sortedLabels = () => state.yAxis[0].data;
assert.deepEqual(JSON.parse(JSON.stringify(sortedLabels().slice(0, 3))), ['beta root','beta step','beta row 52']);
assert.equal(chart.height, 549);
assert.equal(chart.dom.style.height, '549px');
assert.equal(state.dataZoom[0].end, 100);
assert.equal(state.series[1].data[0].itemStyle.borderWidth, 0);
assert.equal(state.series[1].data[0].itemStyle.opacity, 1);
sort.value = 'name'; listeners.change();
assert.equal(sortedLabels()[0], 'alpha root');
sort.value = 'duration'; listeners.change();
assert.equal(sortedLabels()[0], 'beta root');
assert.equal(chart.height, 549);
assert.equal(state.dataZoom[0].end, 100);
const initialTimeSpan = state.dataZoom[1].end - state.dataZoom[1].start;
Object.assign(state.dataZoom[0], {start:0,end:40});
listeners['chart:datazoom']({batch:[{dataZoomId:'timing-rows',start:0,end:40}]});
assert.equal(chart.height, 1040);
assert.equal(state.series[1].data[0].itemStyle.borderWidth, 1);
assert.equal(state.series[1].data[0].itemStyle.opacity, 0.7);
assert.equal(chart.dom.style.height, '1040px');
const zoomedTimeSpan = state.dataZoom[1].end - state.dataZoom[1].start;
assert.ok(zoomedTimeSpan < initialTimeSpan);
Object.assign(state.dataZoom[0], {start:0,end:100});
listeners['chart:datazoom']({batch:[{dataZoomId:'timing-rows',start:0,end:100}]});
assert.equal(chart.height, 549);
assert.equal(chart.dom.style.height, '549px');
assert.equal(state.series[1].data[0].itemStyle.borderWidth, 0);
assert.equal(state.series[1].data[0].itemStyle.opacity, 1);
assert.ok(state.dataZoom[1].end - state.dataZoom[1].start > zoomedTimeSpan);
window.innerHeight = 800;
windowListeners.resize();
assert.equal(chart.height, 349);
assert.equal(chart.dom.style.height, '349px');`
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("chart sorting and zoom regression: %v\n%s", err, output)
	}
}
