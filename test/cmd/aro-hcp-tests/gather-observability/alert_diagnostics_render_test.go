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
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestAlertDiagnosticsJSON(t *testing.T) {
	attack := "</script><img src=x onerror=alert(1)>&\u2028\u2029"
	report := map[string]any{"expression": attack, "error": attack, "metric": map[string]string{"label": attack}}
	encoded := string(alertDiagnosticsJSON(&report))
	if strings.ContainsAny(encoded, "<>&\u2028\u2029") {
		t.Fatalf("report contains unsafe script characters: %s", encoded)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["expression"] != attack || decoded["error"] != attack {
		t.Fatal("HTML escaping must preserve original expressions and errors")
	}
	if got := alertDiagnosticsJSON(nil); got != "null" {
		t.Fatalf("nil report: got %s", got)
	}
	if err := json.Unmarshal([]byte(alertDiagnosticsJSON(map[string]float64{"bad": math.Inf(1)})), &decoded); err != nil || decoded["error"] == nil {
		t.Fatalf("serialization failure must be an escaped report error: %v, %v", decoded, err)
	}
}

func diagnosticsTestPage(t *testing.T, enabled bool) string {
	t.Helper()
	var report alertDiagnosticsReport
	if err := json.Unmarshal([]byte(`{
  "schemaVersion":1,"start":"2026-01-01T00:00:00Z","end":"2026-01-01T00:10:00Z","generatedAt":"2026-01-01T00:11:00Z",
  "alerts":[
    {"charts":[{"expression":"up < 5","path":"root.left","queries":[{"result":0,"role":"left"},{"result":1,"role":"right"}],"thresholds":[{"operator":"<","value":5},{"operator":">","value":500},{"operator":"<","value":-50}]},{"expression":"fallback","path":"root.right","queries":[{"result":0,"role":"value"}],"fallback":"Unsupported expression; original expression queried"}]},
    {"charts":[{"expression":"shared","path":"root","queries":[{"result":0,"role":"value"}]}]},
    {"charts":[{"expression":"shared","path":"root","queries":[{"result":0,"role":"value"}]}]},
    {"charts":[{"expression":"shared","path":"root","queries":[{"result":0,"role":"value"}]}]},
    {"error":"Missing expression / infra <img src=x onerror=alert(1)>"},
    {"warning":"Partial diagnostics","charts":[{"expression":"empty","path":"root","queries":[{"result":2,"role":"value"},{"result":3,"role":"right"}]}]}
  ],
  "queries":[
    {"expression":"up{label=\"</script><img src=x onerror=alert(1)>\"}","workspace":"svc","step":"1m0s","series":[{"metric":{"label":"</script><img src=x onerror=alert(1)>"},"values":[[1767225600,"1"],[1767225660,"NaN"],[1767225720,"3"],[1767225900,"4"],[1767225960,"+Inf"],[1767226020,"-Inf"]]},{"metric":{"low":"kept"},"values":[[1767225600,"0"]]}]},
    {"expression":"other","workspace":"hcp","step":"60s","series":[{"metric":{},"values":[[1767225600,"2"],[1767225660,"2.5"],[1767225720,"3"]]}]},
    {"expression":"empty","workspace":"svc","step":"60s","series":[]},
    {"expression":"failed","workspace":"hcp","step":"60s","error":"backend <img src=x onerror=alert(1)>","warnings":["partial response"],"series":[]}
  ]
}`), &report); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2025, 12, 31, 23, 59, 0, 0, time.UTC)
	end := time.Date(2026, 1, 1, 0, 4, 0, 0, time.UTC)
	alerts := make([]alert, len(report.Alerts))
	for i := range alerts {
		begins, ends := &start, &end
		condition := "Resolved"
		if i == 1 {
			ends, condition = nil, "Fired"
		}
		if i == 2 {
			begins = nil
		}
		if i == 3 {
			ends = nil
		}
		alerts[i] = alert{
			Alert:    alertData{StartsAt: begins, EndsAt: ends, Condition: condition, Severity: "Sev2", Name: "Alert", Expression: "original < expression"},
			Metadata: alertMetadata{KnownIssue: i == 1, MonitoringWorkspaceType: "svc", KnownIssueReason: "known reason"},
		}
	}
	alerts[4].Alert.Expression = ""
	data := alertsOutput{
		Alerts: alerts, TimeWindow: timeWindow{Start: "2026-01-01T00:00:00Z", End: "2026-01-01T00:10:00Z"},
		Summary: alertsSummary{Total: 6, Unknown: 5, Known: 1},
	}
	data.FilterKeys, data.FilterOptions = collectFilterOptions(alerts)
	if enabled {
		data.Diagnostics = &report
	}
	page, err := renderAlertsHTML(data)
	if err != nil {
		t.Fatal(err)
	}
	return string(page)
}

func TestAlertDiagnosticsMarkup(t *testing.T) {
	page := diagnosticsTestPage(t, true)
	if got := strings.Count(page, `class="metric-history"`); got != 6 {
		t.Fatalf("want pane in every known and unknown card, got %d", got)
	}
	for _, want := range []string{`id="alert-diagnostics-data"`, `data-start="2025-12-31T23:59:00Z"`, `data-end=""`, `data-start=""`, `data-condition="Fired"`, `original &lt; expression`, `\u003c/script\u003e`, `id="bar-5"`, `id="timeSliderContainer"`} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(page, "<img") {
		t.Fatal("report payload escaped into HTML")
	}
	legacy := diagnosticsTestPage(t, false)
	for _, forbidden := range []string{"metric-history", "alert-diagnostics", "echarts", "Metric history"} {
		if strings.Contains(legacy, forbidden) {
			t.Errorf("absent diagnostics must emit no UI or script: found %q", forbidden)
		}
	}
}

func TestAlertDiagnosticsBrowser(t *testing.T) {
	browser, err := exec.LookPath("google-chrome")
	if err != nil {
		t.Skip("google-chrome unavailable; skipping optional browser test")
	}
	page := diagnosticsTestPage(t, true)
	// No network dependencies: remove stylesheets and stub only the chart API.
	page = regexp.MustCompile(`<link[^>]*>`).ReplaceAllString(page, "")
	stub := `<script>
window.chartCalls = []; window.browserErrors = [];
window.addEventListener('error', event => browserErrors.push(event.message));
window.echarts = {init(host) {
  if (!host.clientWidth || !host.clientHeight) throw Error('initialized hidden chart');
  const call = {host, resized:0}; chartCalls.push(call);
  return {setOption(option) { call.option = option; }, resize() { call.resized++; }};
}};
</script>`
	page = strings.Replace(page, "<head>", "<head>"+stub, 1)
	assertions := `<script>
(async () => { try {
  const check = (value, message) => { if (!value) throw Error(message); };
  const tick = () => new Promise(resolve => setTimeout(resolve, 120));
  const panes = [...document.querySelectorAll('.metric-history')];
  await tick(); check(chartCalls.length === 0, 'closed panes must stay lazy');
  const row = index => panes[index].closest('.alert-row');
  const classification = value => document.querySelector('.filter-dropdown[data-filter-key="classification"] input[value="' + value + '"]');
  function toggleClassification(value) {
    const dropdown = document.querySelector('.filter-dropdown[data-filter-key="classification"]');
    if (!dropdown.querySelector('.filter-dropdown-menu').classList.contains('open')) dropdown.querySelector('button').click();
    classification(value).click();
  }
  check(classification('unknown').checked && row(1).style.display === 'none', 'production default hides known alerts');
  check(parent !== window && parent.document.querySelectorAll('.tabframe').length === 1, 'production tab wrapper is lazy');
  async function open(index) {
    const pane = panes[index];
    const ancestor = pane.parentElement.closest('details'); if (ancestor) ancestor.open = true;
    pane.open = true; pane.scrollIntoView(); await tick(); await tick();
    return pane;
  }
  await open(0);
  check(chartCalls.length === 2, 'each condition gets a separate chart');
  const first = chartCalls[0].option;
  check(first.useUTC && first.xAxis.min === 1767225600000 && first.xAxis.max === 1767226200000, 'shared UTC range');
  check(first.legend.type === 'scroll' && first.dataZoom.length === 2, 'scroll legend and zoom');
  check(first.tooltip.renderMode === 'richText', 'no HTML tooltip interpolation');
  check(first.series.length === 4, 'all series, including zero-valued, must be kept');
  check(first.series[0].lineStyle.type === 'solid' && first.series[2].lineStyle.type === 'dashed', 'left/right styles');
  check(first.series.every(series => !series.connectNulls), 'never connect gaps');
  const samples = first.series[0].data;
  check(samples.length === 7 && samples[1][1] === null && samples[3][1] === null && samples[5][1] === null && samples[6][1] === null, 'NaN, Inf and missing steps become gaps');
  const symbolSize = (series, index) => series.symbolSize(series.data[index], {dataIndex:index});
  check(first.series[0].showSymbol && first.series[0].showAllSymbol, 'sparse symbols must be enabled, including dense time axes');
  check([0, 2, 4].every(index => symbolSize(first.series[0], index) > 0), 'isolated finite samples remain visible between gaps');
  check(symbolSize(first.series[0], 1) === 0 && symbolSize(first.series[1], 0) > 0, 'null has no marker; one-sample series has a marker');
  check([0, 1, 2].every(index => symbolSize(first.series[2], index) === 0), 'connected runs avoid per-sample markers');
  const extent = {min:0, max:4};
  check(first.yAxis.min(extent) < -50 && first.yAxis.max(extent) > 500, 'offscale thresholds on both sides are included with padding');
  check(first.yAxis.min({min:-1000,max:1000}) < -1000 && first.yAxis.max({min:-1000,max:1000}) > 1000, 'domain still expands with data');
  check(first.yAxis.min({min:Infinity,max:-Infinity}) < -50 && first.yAxis.max({min:Infinity,max:-Infinity}) > 500, 'threshold domain survives hidden data series');
  const autoAxis = chartCalls[1].option.yAxis;
  check(autoAxis.min(extent) < 0 && autoAxis.max(extent) > 4, 'no-threshold chart autoscales data');
  for (const value of [0, 7, -7]) {
    const constant = {min:value,max:value};
    check(autoAxis.min(constant) < value && autoAxis.max(constant) > value, 'equal domain has padding at ' + value);
  }
  const overlay = first.series.at(-1);
  check(overlay.markLine.data[0].name === '< 5' && overlay.markLine.lineStyle.type === 'dashed', 'threshold operator and dashed line');
  check(overlay.markArea.data[0][0].xAxis === 1767225600000 && overlay.markArea.data[0][1].xAxis === 1767225840000, 'shade only clipped recorded interval');
  check(panes[0].textContent.includes('not matched') && panes[0].textContent.includes('Fallback:'), 'instance disclaimer and fallback');
  check(panes[0].textContent.includes('\u003c/script>\u003cimg'), 'exact query shown as text');
  const known = panes[1], ancestor = known.closest('.known-details');
  known.open = true; await tick();
  check(chartCalls.length === 2, 'nested hidden pane must remain lazy');
  toggleClassification('unknown'); await tick();
  check(row(1).style.display !== 'none' && chartCalls.length === 2, 'unchecking classification reveals card, not closed nested chart');
  await open(1);
  check(chartCalls.length === 3 && known.textContent.includes('extends to report end'), 'known unresolved pane');
  check(chartCalls[2].option.series.at(-1).markArea.data[0][1].xAxis === 1767226200000, 'unresolved extends to report end');
  const before = chartCalls[2].resized;
  ancestor.open = false; await tick(); ancestor.open = true; await tick();
  check(chartCalls.length === 3 && chartCalls[2].resized > before, 'nested reopening resizes and reuses charts');
  for (const index of [2, 3]) {
    await open(index);
    check(!chartCalls.at(-1).option.series.at(-1).markArea, 'unknown start or resolved missing end has no shading');
    check(panes[index].textContent.includes('unavailable'), 'interval warning');
  }
  await open(4); check(panes[4].textContent.includes('Missing expression / infra'), 'card error visible without expression');
  await open(5);
  check(panes[5].textContent.includes('No data:') && panes[5].textContent.includes('Query error:') && panes[5].textContent.includes('Query warning:') && panes[5].textContent.includes('Partial diagnostics'), 'no-data, errors and warnings distinct');
  check(!document.querySelector('img'), 'untrusted text cannot create markup');
  await open(0); const count = chartCalls.length;
  const host = chartCalls[0].host, resized = chartCalls[0].resized;
  panes[0].closest('.alert-card').style.width = '250px'; await tick(); await tick();
  check(host.clientWidth <= 250 && chartCalls[0].resized > resized, 'responsive resize');
  toggleClassification('known'); await tick();
  check(row(0).style.display === 'none' && row(1).style.display !== 'none', 'known checkbox filters actual cards');
  check(document.querySelector('.summary-card-number').textContent === '1 / 6', 'production summary follows classification');
  toggleClassification('known'); await tick();
  check(row(0).style.display !== 'none', 'unchecking restores unknown cards');
  const track = document.getElementById('timeSliderTrack').getBoundingClientRect();
  document.getElementById('timeSliderStart').dispatchEvent(new MouseEvent('mousedown', {bubbles:true, clientX:track.left}));
  document.dispatchEvent(new MouseEvent('mousemove', {clientX:track.left + track.width * 0.7}));
  document.dispatchEvent(new MouseEvent('mouseup'));
  await tick();
  check(row(0).style.display === 'none' && row(1).style.display !== 'none', 'time slider excludes resolved interval but retains unresolved alert');
  document.getElementById('resetTimeSlider').click(); await tick();
  check(row(0).style.display !== 'none' && chartCalls.length === count, 'time reset restores existing chart instances');
  const tabs = parent.document.querySelectorAll('.tab');
  tabs[1].click(); await tick(); tabs[0].click(); await tick(); await tick();
  check(parent.document.querySelectorAll('.tabframe').length === 2 && chartCalls.length === count, 'tab reactivation reuses initialized charts');
  const frame = window.frameElement, height = frame.clientHeight;
  await tick();
  check(frame.clientHeight === height && document.documentElement.scrollHeight <= height, 'wrapper height settles without clipping charts');
  check(document.getElementById('bar-0').style.width !== '', 'production duration bars remain rendered');
  check(!browserErrors.length, 'browser errors: ' + browserErrors.join(', '));
  await fetch('/result', {method:'POST', body:'PASS'});
} catch (error) { await fetch('/result', {method:'POST', body:'FAIL: ' + error.stack}); } })();
</script>`
	page = strings.Replace(page, "</body>", assertions+"</body>", 1)
	wrapper := filepath.Join(t.TempDir(), "observability-summary.html")
	if err := renderObservabilityPage(wrapper, []observabilityTab{
		{Title: "Alerts", HTML: page},
		{Title: "Other", HTML: "<html><body>Other diagnostics</body></html>"},
	}); err != nil {
		t.Fatal(err)
	}
	wrapped, err := os.ReadFile(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	page = string(wrapped)
	for _, size := range []string{"1440,1000", "390,844"} {
		t.Run(size, func(t *testing.T) {
			results := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/result" {
					body, _ := io.ReadAll(r.Body)
					select {
					case results <- string(body):
					default:
					}
					return
				}
				w.Header().Set("Content-Type", "text/html")
				_, _ = io.WriteString(w, page)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, browser, "--headless", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage", "--disable-background-networking", "--no-first-run", "--window-size="+size, "--user-data-dir="+filepath.Join(t.TempDir(), "chrome"), server.URL)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				if t.Failed() {
					t.Log(stderr.String())
				}
			}()
			select {
			case result := <-results:
				if result != "PASS" {
					t.Fatal(result)
				}
			case <-ctx.Done():
				t.Fatal("timed out waiting for diagnostics browser assertions")
			}
		})
	}
}
