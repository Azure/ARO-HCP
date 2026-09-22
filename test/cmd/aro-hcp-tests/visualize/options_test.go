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
					Steps: []StepTimingMetadata{{Name: "step", StartedAt: start, FinishedAt: start}},
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
	offsets := chart.MultiSeries[0].Data.([]opts.BarData)
	durations := chart.MultiSeries[1].Data.([]opts.BarData)
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
	if chart.Subtitle != "Overall Runtime: 5s" {
		t.Errorf("unexpected runtime: %q", chart.Subtitle)
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
