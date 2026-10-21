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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	promutil "github.com/Azure/ARO-HCP/test/util/prometheus"
)

func metricsReplayFixture() metricsReport {
	return metricsReport{Version: 1, TimeWindow: timeWindow{Start: "2026-10-21T07:00:00Z", End: "2026-10-21T08:00:00Z"}, Panels: []metricsPanel{{
		Title: "Controllers", Queries: []metricsQuery{
			{Spec: QuerySpec{Title: "Retries", ChartType: chartTypeLine, MinPeakThreshold: 5, Query: "rate(retries[5m])"}, Warning: "partial evidence", Results: []promutil.Result{
				{Metric: map[string]string{"name": "low", "cluster": "common"}, Values: [][]any{{float64(1792566000), "2"}, {float64(1792569600), "1"}}},
				{Metric: map[string]string{"name": "high", "cluster": "common"}, Values: [][]any{{float64(1792566000), "10"}, {float64(1792569600), "8"}}},
				{Metric: map[string]string{"name": "zero <&>", "cluster": "common"}, Values: [][]any{{float64(1792566000), "0"}, {float64(1792569600), "NaN"}}},
			}},
			{Spec: QuerySpec{Title: "Unavailable", ChartType: chartTypeLine}, Error: "query <failed>"},
			{Spec: QuerySpec{Title: "Empty", ChartType: chartTypeLine}},
		},
	}}}
}

func TestMetricsReplay(t *testing.T) {
	report := metricsReplayFixture()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	input, output := filepath.Join(dir, "metrics.json"), filepath.Join(dir, "metrics.html")
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	cmd, err := NewCommand()
	if err != nil {
		t.Fatal(err)
	}
	cmd.SetArgs([]string{"render-metrics", "--input", input, "--output", output})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	rendered, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var tabs []observabilityTab
	_, tail, ok := strings.Cut(string(rendered), "const TABS = ")
	if !ok {
		t.Fatal("missing rendered tabs")
	}
	if err := json.NewDecoder(strings.NewReader(tail)).Decode(&tabs); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"partial evidence", "query &lt;failed&gt;", "No results returned", "rate(retries[5m])", "series-controls"} {
		if !strings.Contains(tabs[0].HTML, want) {
			t.Errorf("replay lost %q", want)
		}
	}
	// Rendering must not mutate the stored evidence or compact away common labels.
	if _, err := renderMetricsPanels(report, renderPanelHTML); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(report)
	if !bytes.Equal(data, after) {
		t.Fatal("rendering changed original evidence")
	}
	for _, invalid := range []metricsReport{{Version: 2}, {Version: 1}, {Version: 1, TimeWindow: timeWindow{Start: report.TimeWindow.End, End: report.TimeWindow.Start}}} {
		if _, err := renderMetricsPanels(invalid, renderPanelHTML); err == nil {
			t.Errorf("accepted invalid report: %+v", invalid)
		}
	}
	cmd = newRenderMetricsCommand()
	cmd.SetArgs([]string{"--input", input, "--output", input})
	if err := cmd.Execute(); err == nil {
		t.Fatal("allowed overwriting evidence")
	}
}

// Run with METRICS_ECHARTS_JS pointing at a cached copy of the template's CDN
// asset. Uses real ECharts: filtered legend.data and legend.selected interact in
// ways a chart stub cannot verify.
func TestMetricsLegendBrowser(t *testing.T) {
	asset := os.Getenv("METRICS_ECHARTS_JS")
	if asset == "" {
		t.Skip("set METRICS_ECHARTS_JS for real ECharts legend test")
	}
	browser, err := exec.LookPath("google-chrome")
	if err != nil {
		t.Skip("google-chrome unavailable")
	}
	library, err := os.ReadFile(asset)
	if err != nil {
		t.Fatal(err)
	}
	report := metricsReplayFixture()
	zero := report.Panels[0].Queries[0]
	zero.Spec.Title, zero.Spec.MinPeakThreshold = "Zero", 0
	zero.Results = []promutil.Result{
		{Metric: map[string]string{"name": "a"}, Values: [][]any{{float64(1792566000), "0"}}},
		{Metric: map[string]string{"name": "b"}, Values: [][]any{{float64(1792566000), "0"}}},
	}
	report.Panels[0].Queries = append(report.Panels[0].Queries, zero)
	faceted := report.Panels[0].Queries[0]
	faceted.Spec.ChartType, faceted.Spec.FacetBy = chartTypeFacetedLine, "name"
	report.Panels[0].Queries = append(report.Panels[0].Queries, faceted)
	stacked := zero
	stacked.Spec.ChartType, stacked.Spec.FacetBy, stacked.Spec.StackBy = chartTypeFacetedStackedArea, "name", "name"
	report.Panels[0].Queries = append(report.Panels[0].Queries, stacked)
	tabs, err := renderMetricsPanels(report, renderPanelHTML)
	if err != nil {
		t.Fatal(err)
	}
	page := strings.Replace(tabs[0].HTML, `https://go-echarts.github.io/go-echarts-assets/assets/echarts.min.js`, "echarts.min.js", 1)
	assertions := `<script>
try {
 const check = (value, message) => { if (!value) throw new Error(message); };
 const charts = Array.from(document.querySelectorAll('.item')).map(el => echarts.getInstanceByDom(el));
 const controls = document.querySelectorAll('.series-controls');
 check(charts.length === 6 && controls.length === 6, 'controls for every chart, including facets and stacked plots');
 const chart = charts[0], ui = controls[0], search = ui.querySelector('[type=search]'), only = ui.querySelector('[type=checkbox]');
 const legend = () => chart.getOption().legend[0];
 const selected = () => Object.entries(legend().selected).filter(([k,v]) => v).map(([k]) => k).sort().join(',');
 const names = () => legend().data.map(d => d.name).join(',');
 const button = name => ui.querySelector('[data-action='+name+']').click();
 const find = value => { search.value = value; search.dispatchEvent(new Event('input')); };
 const filter = value => { only.checked = value; only.dispatchEvent(new Event('change')); };
 const slider = chart.getDom().querySelector('[type=range]');
 const threshold = value => { slider.value=value; slider.dispatchEvent(new Event('input')); };
 check(selected() === 'high', 'initial peak threshold');
 check(names().startsWith('high'), 'plotted series appear first');
 button('none'); check(selected() === '', 'deselect all');
 find('HIGH'); check(names() === 'high' && selected() === '', 'case-insensitive search preserves selection');
 button('matches'); check(selected() === 'high', 'select matches');
 find('not found'); check(selected() === 'high' && names() === '', 'search does not hide plotted lines');
 check(ui.querySelector('[data-action=matches]').disabled, 'no-match action disabled');
 find(''); filter(true); check(names() === 'high', 'selected only');
 check(ui.textContent.includes('Showing: high'), 'single plotted series identified');
 threshold(0); check(selected() === 'high', 'lower threshold preserves manual deselections');
 filter(false); chart.dispatchAction({type:'legendToggleSelect', name:'low'});
 check(selected() === 'high,low', 'native legend click selects another line');
 threshold(5); check(selected() === 'high', 'threshold hides low selected line');
 threshold(0); check(selected() === 'high,low', 'lower threshold restores selected line');
 chart.dispatchAction({type:'legendToggleSelect', name:'low'});
 threshold(5); threshold(0); check(selected() === 'high', 'manual deselection survives threshold cycle');
 find('zero'); button('all'); check(selected() === 'high,low,zero <&>', 'select all includes labels outside search');
 button('none'); find(''); filter(false);
 check(!ui.querySelector('script'), 'labels treated as text');
 check(charts[1].getDom().querySelector('[type=range]') === null, 'all-zero chart has no slider');
 controls[1].querySelector('[data-action=none]').click();
 check(Object.values(charts[1].getOption().legend[0].selected).every(v => !v), 'zero-valued series can be deselected');
 controls[5].querySelector('[data-action=none]').click();
 check(Object.values(charts[5].getOption().legend[0].selected).every(v => !v), 'stacked facets can be deselected');
 check(Object.values(charts[2].getOption().legend[0].selected).some(v => v), 'other facets remain independent');
 const result = document.createElement('output'); result.id='browser-result'; result.textContent='PASS'; document.body.append(result);
} catch (error) {
 const result = document.createElement('output'); result.id='browser-result'; result.textContent='FAIL: '+error.stack; document.body.append(result);
}
</script>`
	page = strings.Replace(page, "</body>", assertions+"</body>", 1)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "echarts.min.js"), library, 0600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "metrics.html")
	if err := os.WriteFile(file, []byte(page), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, browser, "--headless", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage", "--no-first-run", "--virtual-time-budget=5000", "--user-data-dir="+filepath.Join(dir, "chrome"), "--dump-dom", "file://"+file)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	dom, err := cmd.Output()
	if err != nil {
		t.Fatalf("browser: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(string(dom), `<output id="browser-result">PASS</output>`) {
		_, result, _ := strings.Cut(string(dom), `<output id="browser-result">`)
		result, _, _ = strings.Cut(result, "</output>")
		t.Fatalf("browser assertions: %s\n%s", result, stderr.String())
	}
}
