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
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRenderRightSizingHTML(t *testing.T) {
	report := buildRightSizingReport(rightSizingFixture(), 0.1)
	attack := "</script><img src=x onerror=alert(1)>&\u2028\u2029"
	report.Warnings = append(report.Warnings, attack)
	report.CPUWindow = attack
	row := &report.Recommendations[0]
	row.Cluster, row.Namespace, row.Kind, row.Workload, row.Container = attack, attack, attack, attack, attack
	row.SuggestedQuantity, row.Warnings = attack, []string{attack}
	html, err := renderRightSizingHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(html)
	for _, want := range []string{"<!DOCTYPE html>", `name="viewport"`, `id="cpu-rows"`, `id="memory-rows"`, `\u003c/script\u003e`, `\u0026`, `\u2028`, `\u2029`, "Within tolerance", "Insufficient evidence", "Round up (ceil)", "preserving the full 20% headroom"} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered report missing %q", want)
		}
	}
	for _, forbidden := range []string{attack, "innerHTML", "#ZgotmplZ", "https://", "http://", "<script src=", "fetch(", "nearest", "safety floor"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("unsafe, non-self-contained or outdated report: %q", forbidden)
		}
	}
	const marker = `<script id="right-sizing-data" type="application/json">`
	if strings.Count(text, marker) != 1 {
		t.Fatal("report must embed JSON exactly once")
	}
	_, embedded, _ := strings.Cut(text, marker)
	embedded, _, _ = strings.Cut(embedded, "</script>")
	var decoded rightSizingReport
	if err := json.Unmarshal([]byte(embedded), &decoded); err != nil {
		t.Fatalf("decode embedded JSON: %v", err)
	}
	if !reflect.DeepEqual(report, decoded) {
		t.Fatal("embedded report changed values, including HTML-like strings")
	}
}

func TestRenderRightSizingHTMLValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*rightSizingReport)
		want string
	}{
		{"old rounding policy", func(r *rightSizingReport) { r.Version = 1 }, "regenerate from replica-peaks.json using render-right-sizing"},
		{"version", func(r *rightSizingReport) { r.Version = 3 }, "version"},
		{"start", func(r *rightSizingReport) { r.Start = time.Time{} }, "timestamps"},
		{"end", func(r *rightSizingReport) { r.End = time.Time{} }, "timestamps"},
		{"reversed", func(r *rightSizingReport) { r.End = r.Start.Add(-time.Second) }, "start <= end"},
		{"nonfinite", func(r *rightSizingReport) { r.Headroom = math.NaN() }, "encode"},
		{"infinite peak", func(r *rightSizingReport) { v := math.Inf(1); r.Recommendations[0].Peak = &v }, "encode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := buildRightSizingReport(rightSizingFixture(), 0.1)
			tc.edit(&report)
			if _, err := renderRightSizingHTML(report); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
		})
	}
	report := buildRightSizingReport(rightSizingFixture(), 0.1)
	report.Recommendations = nil
	if _, err := renderRightSizingHTML(report); err != nil {
		t.Fatalf("empty report must render: %v", err)
	}
}

func TestRenderRightSizingCommandReplay(t *testing.T) {
	peaks := rightSizingFixture()
	peaks.GeneratedAt = peaks.End
	data, err := json.Marshal(peaks)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "replica-peaks.json")
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, threshold := range []string{"", "0.75"} {
		t.Run("threshold="+threshold, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "rendered")
			cmd := newRenderRightSizingCommand()
			args := []string{"--input", input, "--output", output}
			wantThreshold := 0.1
			if threshold != "" {
				args = append(args, "--change-threshold", threshold)
				wantThreshold = 0.75
			}
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("offline replay: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(output, "right-sizing.json"))
			if err != nil {
				t.Fatal(err)
			}
			var got rightSizingReport
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			want := buildRightSizingReport(peaks, wantThreshold)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("replayed JSON differs from builder: got %+v, want %+v", got, want)
			}
			html, err := os.ReadFile(filepath.Join(output, "right-sizing.html"))
			if err != nil {
				t.Fatal(err)
			}
			wantHTML, err := renderRightSizingHTML(want)
			if err != nil || !bytes.Equal(html, wantHTML) {
				t.Fatalf("replayed HTML differs from renderer: %v", err)
			}
			files, err := os.ReadDir(output)
			if err != nil || len(files) != 2 {
				t.Fatalf("expected only JSON and standalone HTML, got %v: %v", files, err)
			}
			unchanged, err := os.ReadFile(input)
			if err != nil || !bytes.Equal(unchanged, dataForPeaks(t, peaks)) {
				t.Fatalf("replay modified input: %v", err)
			}
		})
	}
}

func dataForPeaks(t *testing.T, peaks replicaPeakReport) []byte {
	t.Helper()
	data, err := json.Marshal(peaks)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRenderRightSizingCommandRejectsInvalidInput(t *testing.T) {
	peaks := rightSizingFixture()
	peaks.GeneratedAt = peaks.End
	valid := string(dataForPeaks(t, peaks))
	sized, err := json.Marshal(buildRightSizingReport(peaks, 0.1))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, input, threshold, want string
	}{
		{"version", strings.Replace(valid, `"version":1`, `"version":2`, 1), "0.1", "version"},
		{"negative threshold", valid, "-0.1", "change-threshold"},
		{"large threshold", valid, "1.1", "change-threshold"},
		{"NaN threshold", valid, "NaN", "change-threshold"},
		{"infinite threshold", valid, "+Inf", "change-threshold"},
		{"trailing object", valid + "{}", "0.1", "trailing data"},
		{"trailing junk", valid + "junk", "0.1", "trailing data"},
		{"unknown field", strings.Replace(valid, `"version":1`, `"extra":true,"version":1`, 1), "0.1", "unknown field"},
		{"nested unknown field", strings.Replace(valid, `"queryName":`, `"extra":true,"queryName":`, 1), "0.1", "unknown field"},
		{"sizing report", string(sized), "0.1", "unknown field"},
		{"null", "null", "0.1", "version"},
		{"nonfinite value", strings.Replace(valid, `"max":0.1`, `"max":1e999`, 1), "0.1", "decode"},
		{"missing start", strings.Replace(valid, peaks.Start.Format(time.RFC3339), "0001-01-01T00:00:00Z", 1), "0.1", "timestamps"},
		{"missing generatedAt", strings.Replace(valid, `"generatedAt":"`+peaks.End.Format(time.RFC3339)+`"`, `"generatedAt":null`, 1), "0.1", "timestamps"},
		{"reversed", strings.Replace(valid, peaks.Start.Format(time.RFC3339), peaks.End.Add(time.Hour).Format(time.RFC3339), 1), "0.1", "start <= end"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			input, output := filepath.Join(dir, "input.json"), filepath.Join(dir, "output")
			if err := os.WriteFile(input, []byte(tc.input), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := newRenderRightSizingCommand()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"--input", input, "--output", output, "--change-threshold", tc.threshold})
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatalf("invalid input created output: %v", err)
			}
		})
	}
}

func TestRenderRightSizingCommandProtectsInput(t *testing.T) {
	peaks := rightSizingFixture()
	peaks.GeneratedAt = peaks.End
	data := dataForPeaks(t, peaks)
	for _, alias := range []string{"same path", "symlink", "hardlink"} {
		t.Run(alias, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "right-sizing.json")
			if alias != "same path" {
				input = filepath.Join(dir, "replica-peaks.json")
			}
			if err := os.WriteFile(input, data, 0600); err != nil {
				t.Fatal(err)
			}
			switch alias {
			case "symlink":
				if err := os.Symlink(input, filepath.Join(dir, "right-sizing.html")); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(input, filepath.Join(dir, "right-sizing.json")); err != nil {
					t.Fatal(err)
				}
			}
			cmd := newRenderRightSizingCommand()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"--input", input, "--output", dir})
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "overwrite the input") {
				t.Fatalf("expected input overwrite rejection, got %v", err)
			}
			unchanged, err := os.ReadFile(input)
			if err != nil || !bytes.Equal(unchanged, data) {
				t.Fatalf("input was modified: %v", err)
			}
		})
	}
}

func TestRenderRightSizingBrowser(t *testing.T) {
	report := buildRightSizingReport(rightSizingFixture(), 0.1)
	// Preserve fractional seconds: real collection windows have nanosecond precision.
	report.End = report.End.Add(867262418 * time.Nanosecond)
	base := report.Recommendations[0]
	report.Recommendations = nil
	for i := range 10000 {
		row := base
		row.Resource = "cpu"
		row.Workload = fmt.Sprintf("workload-%05d", i)
		row.Cluster = "large-cluster"
		delta := float64(i+1) / 1000
		row.Delta = &delta
		row.Direction, row.Eligible, row.Actionable = "over", true, true
		report.Recommendations = append(report.Recommendations, row)
	}
	for _, resource := range []string{"cpu", "memory"} {
		for _, direction := range []string{"under", "matched", "unknown"} {
			row := base
			row.Resource, row.Direction = resource, direction
			row.Cluster, row.Workload = "small-cluster", direction+"-workload"
			row.Eligible, row.Actionable = direction != "unknown", direction == "under"
			row.AlertRisk = direction == "under"
			delta := -0.03
			row.Delta = &delta
			if direction == "matched" {
				row.Direction = "over" // Nonzero but below tolerance, not exactly matched.
			}
			if direction == "unknown" {
				row.Peak, row.RequestMin, row.RequestMax, row.Suggested, row.Delta = nil, nil, nil, nil, nil
				row.Warnings = []string{"</script><img src=x onerror=window.injected=true>"}
				row.Container = row.Warnings[0]
				row.InitContainer = true
			}
			report.Recommendations = append(report.Recommendations, row)
		}
	}
	report.Warnings = []string{"<img src=x onerror=window.injected=true>"}
	html, err := renderRightSizingHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	assertions := `<script>
try {
  function check(condition, message) { if (!condition) throw new Error(message); }
  function change(id, value) { const node = document.getElementById(id); if (node.type === 'checkbox') node.checked = value; else node.value = value; node.dispatchEvent(new Event('input', {bubbles: true})); }
  const cpu = document.getElementById('cpu-rows'), memory = document.getElementById('memory-rows');
  check(document.getElementById('actionable').checked, 'actionable is default');
  check(cpu.children.length === 30 && memory.children.length === 1, 'bounded independent pages');
  check(cpu.rows[0].textContent.includes('workload-09999'), 'largest absolute gap ranks first');
  check(cpu.rows[0].cells[5].textContent.includes('+10,000m'), 'gap is per container, not multiplied by replicas');
  check(cpu.rows[0].textContent.includes('2 / 2'), 'historical coverage shown');
  check(cpu.rows[0].textContent.includes('100m to 100m') && cpu.rows[0].textContent.includes('Burst: 200m'), 'request range and CPU burst shown');
  document.getElementById('cpu-next').click();
  check(cpu.rows[0].textContent.includes('workload-09969'), 'next page ranks');
  check(document.getElementById('memory-page').textContent.includes('1 of 1'), 'paging resources independently');
  change('cluster', 'small-cluster');
  check(cpu.children.length === 1 && cpu.textContent.includes('under-workload'), 'cluster filter resets pagination');
  check(cpu.textContent.includes('Alert risk'), 'alert risk visible');
  change('direction', 'matched');
  check(!document.getElementById('actionable').checked && cpu.textContent.includes('matched-workload'), 'within tolerance exposes non-actionable rows');
  change('direction', 'unknown');
  check(cpu.textContent.includes('Insufficient evidence') && cpu.textContent.includes('Unknown'), 'missing values are not zero');
  check(cpu.textContent.includes('(init container)'), 'init identity shown');
  const details = cpu.querySelector('details'); details.open = true;
  check(details.textContent.includes('<img') && details.textContent.includes('Ineligible'), 'literal evidence and eligibility visible');
  check(!document.querySelector('img') && !window.injected, 'injection cannot create executable DOM');
  change('direction', 'under');
  check(cpu.textContent.includes('under-workload') && memory.textContent.includes('under-workload'), 'under-requested filter');
  change('search', 'no-matches');
  check(cpu.textContent.includes('No matching candidates') && document.getElementById('cpu-next').disabled, 'empty state');
  change('search', 'UNDER-WORKLOAD');
  check(cpu.textContent.includes('under-workload'), 'case insensitive search');
  document.getElementById('reset').click();
  check(cpu.children.length === 30 && document.getElementById('actionable').checked, 'reset restores defaults');
  change('direction', 'over');
  check(memory.textContent.includes('No matching candidates') && cpu.rows[0].textContent.includes('workload-09999'), 'over filter preserves rank');
  check(document.querySelectorAll('tbody tr').length <= 60, '10k rows do not build 10k DOM');
  check(document.documentElement.scrollWidth <= window.innerWidth, 'mobile has no page-level horizontal overflow');
  check(document.getElementById('window').scrollWidth <= document.getElementById('window').clientWidth, 'precise timestamps wrap within header');
  check([...document.querySelectorAll('.table-scroll')].every(node => node.tabIndex === 0), 'wide tables keyboard scrollable');
  const result = document.createElement('output'); result.id = 'browser-result'; result.textContent = 'PASS'; document.body.append(result);
} catch (error) {
  const result = document.createElement('output'); result.id = 'browser-result'; result.textContent = 'FAIL: ' + error.message; document.body.append(result);
}
</script>`
	page := []byte(strings.Replace(string(html), "</body>", assertions+"</body>", 1))
	for _, size := range [][2]int{{1440, 1000}, {390, 844}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			call := rightSizingBrowser(t, page, size[0], size[1])
			result := call("Runtime.evaluate", map[string]any{"expression": `document.getElementById('browser-result')?.textContent`, "returnByValue": true})
			var evaluation struct {
				Result struct{ Value string }
			}
			if err := json.Unmarshal(result, &evaluation); err != nil || evaluation.Result.Value != "PASS" {
				t.Fatalf("browser assertions: %s (%v)", result, err)
			}
		})
	}
}

// Use Chrome's pipe transport so viewport emulation needs neither a new browser
// dependency nor a listening debugging port. --window-size has a 500px minimum
// on some Chrome builds and cannot verify a phone's CSS viewport.
func rightSizingBrowser(t *testing.T, html []byte, width, height int) func(string, map[string]any) json.RawMessage {
	t.Helper()
	browser, err := exec.LookPath("google-chrome")
	if err != nil {
		t.Skip("google-chrome unavailable; skipping optional browser test")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "right-sizing.html")
	if err := os.WriteFile(file, html, 0600); err != nil {
		t.Fatal(err)
	}
	toChrome, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = toChrome.Close(); _ = writer.Close() })
	reader, fromChrome, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close(); _ = fromChrome.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, browser, "--headless", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage", "--disable-background-networking", "--no-first-run", "--remote-debugging-pipe", "--user-data-dir="+dir)
	cmd.ExtraFiles = []*os.File{toChrome, fromChrome}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			t.Log(stderr.String())
		}
	})
	_ = toChrome.Close()
	_ = fromChrome.Close()
	responses := bufio.NewReader(reader)
	id, session := 0, ""
	call := func(method string, params map[string]any) json.RawMessage {
		t.Helper()
		id++
		request := map[string]any{"id": id, "method": method, "params": params}
		if session != "" {
			request["sessionId"] = session
		}
		data, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(append(data, 0)); err != nil {
			t.Fatal(err)
		}
		for {
			data, err := responses.ReadBytes(0)
			if err != nil {
				t.Fatalf("CDP %s: %v", method, err)
			}
			var response struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if err := json.Unmarshal(data[:len(data)-1], &response); err != nil {
				t.Fatal(err)
			}
			if response.ID != id {
				continue
			}
			if len(response.Error) != 0 {
				t.Fatalf("CDP %s: %s", method, response.Error)
			}
			return response.Result
		}
	}
	var target struct{ TargetID string }
	if err := json.Unmarshal(call("Target.createTarget", map[string]any{"url": "about:blank"}), &target); err != nil {
		t.Fatal(err)
	}
	var attached struct{ SessionID string }
	if err := json.Unmarshal(call("Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true}), &attached); err != nil {
		t.Fatal(err)
	}
	session = attached.SessionID
	call("Emulation.setDeviceMetricsOverride", map[string]any{"width": width, "height": height, "deviceScaleFactor": 1, "mobile": width < 650})
	pageURL := (&url.URL{Scheme: "file", Path: file}).String()
	call("Page.navigate", map[string]any{"url": pageURL})
	for {
		result := call("Runtime.evaluate", map[string]any{
			"expression":    fmt.Sprintf(`location.href === %q && document.readyState === 'complete' && innerWidth === %d && innerHeight === %d`, pageURL, width, height),
			"returnByValue": true,
		})
		var evaluation struct {
			Result struct{ Value bool }
		}
		if err := json.Unmarshal(result, &evaluation); err != nil {
			t.Fatal(err)
		}
		if evaluation.Result.Value {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("page failed to load at exact CSS viewport %dx%d: %s", width, height, result)
		case <-time.After(10 * time.Millisecond):
		}
	}
	return call
}

func TestRenderRightSizingBrowserReplay(t *testing.T) {
	input := os.Getenv("RIGHT_SIZING_REPORT")
	if input == "" {
		t.Skip("set RIGHT_SIZING_REPORT to replay a collected right-sizing.json")
	}
	data, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	var report rightSizingReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	html, err := renderRightSizingHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []struct {
		name          string
		width, height int
	}{{"desktop", 1440, 1000}, {"mobile", 390, 844}} {
		t.Run(size.name, func(t *testing.T) {
			call := rightSizingBrowser(t, html, size.width, size.height)
			result := call("Runtime.evaluate", map[string]any{"expression": `JSON.stringify({width: innerWidth, height: innerHeight, scrollWidth: document.documentElement.scrollWidth, timestampWidth: document.getElementById('window').scrollWidth, timestampClientWidth: document.getElementById('window').clientWidth})`, "returnByValue": true})
			t.Logf("viewport measurements: %s", result)
			result = call("Runtime.evaluate", map[string]any{"expression": `document.documentElement.scrollWidth <= innerWidth && document.getElementById('window').scrollWidth <= document.getElementById('window').clientWidth`, "returnByValue": true})
			var evaluation struct {
				Result struct{ Value bool }
			}
			if err := json.Unmarshal(result, &evaluation); err != nil || !evaluation.Result.Value {
				t.Fatalf("actual report overflows viewport: %s (%v)", result, err)
			}
			if output := os.Getenv("RIGHT_SIZING_SCREENSHOT_DIR"); output != "" {
				var screenshot struct{ Data string }
				if err := json.Unmarshal(call("Page.captureScreenshot", map[string]any{"format": "png", "captureBeyondViewport": false}), &screenshot); err != nil {
					t.Fatal(err)
				}
				png, err := base64.StdEncoding.DecodeString(screenshot.Data)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(output, "right-sizing-"+size.name+".png"), png, 0644); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
