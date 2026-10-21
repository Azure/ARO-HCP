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
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderPanelBrowserSelection(t *testing.T) {
	assetPath := os.Getenv("ECHARTS_JS_PATH")
	if assetPath == "" {
		t.Skip("set ECHARTS_JS_PATH for real chart selection regression")
	}
	browser, err := exec.LookPath("google-chrome")
	if err != nil {
		t.Skip("google-chrome unavailable; skipping optional browser test")
	}
	rendered, err := renderPanelHTML(selectionTestPanel())
	if err != nil {
		t.Fatal(err)
	}
	asset, err := os.ReadFile(assetPath)
	if err != nil {
		t.Fatalf("load ECharts for browser test: %v", err)
	}
	assertions := `<script>
try {
  function check(condition, message) { if (!condition) throw new Error(message); }
  check(!window.selectionErrors.length, 'chart initialization errors: ' + window.selectionErrors.join('; '));
  const plots = Array.from(document.querySelectorAll('.item'));
  const charts = plots.map(plot => echarts.getInstanceByDom(plot));
  const buttons = Array.from(document.querySelectorAll('.series-controls button'));
  check(plots.length === 6 && buttons.length === 6, 'one control per instance, including each line facet and zero-valued chart, none for no data');
  function legend(index) { return charts[index].getOption().legend[0]; }
  function legendNames(index) { return charts[index].getModel().getComponent('legend').getData().map(entry => entry.get('name')); }
  function names(index) { return Array.from(new Set(charts[index].getOption().series.map(series => series.name))); }
  function selected(index) { return names(index).filter(name => legend(index).selected[name] !== false); }
  function threshold(index, value) {
    const slider = plots[index].querySelector('input[type=range]');
    check(slider, 'expected threshold slider for chart ' + index);
    slider.value = value;
    slider.dispatchEvent(new Event('input'));
  }
  function reselect(index, name) { charts[index].dispatchAction({type: 'legendToggleSelect', name: name}); }
  buttons.forEach((button, index) => {
    check(button.textContent === 'Deselect all' && button.type === 'button', 'accessible native action');
    check(button.getAttribute('aria-controls') === plots[index].id, 'control targets own plot');
    check(button.getAttribute('aria-label').includes(charts[index].getOption().title[0].text), 'accessible name identifies graph');
  });
  check(selected(0).length === 1, 'initial threshold excludes quiet controller');
  const retained = JSON.stringify(legend(0).data);
  const other = JSON.stringify(legend(1));
  buttons[0].click(); buttons[0].click();
  check(selected(0).length === 0, 'repeated deselect all is idempotent');
  check(JSON.stringify(legend(0).data) === retained, 'deselect retains eligible legend entries');
  const busy = legend(0).data[0];
  reselect(0, busy);
  check(selected(0).length === 1 && selected(0)[0] === busy, 'one controller can be immediately enabled');
  threshold(0, 0);
  check(legend(0).data.length === 2 && selected(0).length === 1, 'lower threshold reveals entries without undoing deselection');
  const quiet = names(0).find(name => name !== busy);
  reselect(0, quiet);
  threshold(0, 5);
  check(selected(0).length === 1, 'threshold still excludes manually selected low-peak series');
  threshold(0, 0);
  check(selected(0).length === 2, 'lowering threshold restores manual choices');
  reselect(0, quiet);
  threshold(0, 5); threshold(0, 0);
  check(selected(0).length === 1, 'individual deselection survives filtering');
  check(JSON.stringify(legend(1)) === other, 'second graph unaffected');
  buttons[2].click();
  check(selected(2).length === 0 && legendNames(2).length === 2, 'zero-valued chart can deselect without losing entries');
  reselect(2, names(2)[0]);
  check(selected(2).length === 1, 'zero-valued controller can be reselected');
  const otherFacet = JSON.stringify(legend(4));
  buttons[3].click(); reselect(3, legend(3).data[0]); threshold(3, 0);
  check(selected(3).length === 1, 'facet has independent selection and filtering');
  check(JSON.stringify(legend(4)) === otherFacet, 'sibling facet unaffected');
  check(!plots[5].querySelector('input[type=range]'), 'stacked chart keeps no-slider behavior');
  buttons[5].click();
  check(selected(5).length === 0 && legendNames(5).length > 0, 'stacked shared legend retains entries');
  reselect(5, names(5)[0]);
  check(selected(5).length === 1, 'stacked phase reselect works across its grids');
  document.body.setAttribute('data-selection-test', 'PASS');
} catch (error) { document.body.setAttribute('data-selection-test', 'FAIL: ' + error.message); }
</script>`
	page := strings.Replace(string(rendered), "<head>", `<head><script>window.selectionErrors = []; window.addEventListener('error', event => window.selectionErrors.push(event.message));</script>`, 1)
	page = strings.Replace(page, "https://go-echarts.github.io/go-echarts-assets/assets/echarts.min.js", "/echarts.js", 1)
	page = strings.Replace(page, "</body>", assertions+"</body>", 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/echarts.js" {
			writer.Header().Set("Content-Type", "application/javascript")
			_, _ = writer.Write(asset)
			return
		}
		writer.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(writer, page)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, browser, "--headless", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage", "--no-first-run", "--user-data-dir="+t.TempDir(), "--dump-dom", server.URL)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("browser failed: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(string(output), `data-selection-test="PASS"`) {
		for _, line := range strings.Split(string(output), "\n") {
			if strings.Contains(line, "<body") {
				t.Log(line)
			}
		}
		t.Fatalf("browser selection assertions did not pass\n%s", stderr.String())
	}
}

func TestRenderObservabilityBrowserResize(t *testing.T) {
	browser, err := exec.LookPath("google-chrome")
	if err != nil {
		t.Skip("google-chrome unavailable; skipping optional browser test")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "observability-summary.html")
	section := `<!DOCTYPE html><html><head><style>
/* Alerts inherits these viewport-dependent rules from MDL. */
html { height: 100%; }
body { min-height: 100%; }
body { margin: 16px; padding: 7px; }
#content { height: 400px; }
</style></head><body><div id="content"></div><div id="spacer" style="height:30px"></div>
<script>
window.initialWidth = document.body.getBoundingClientRect().width;
window.resizeErrors = [];
window.addEventListener('error', event => window.resizeErrors.push(event.message));
</script></body></html>`
	if err := renderObservabilityPage(file, []observabilityTab{
		{Title: "First", HTML: section},
		// Alerts has no doctype, so also cover quirks-mode sizing.
		{Title: "Lazy", HTML: strings.TrimPrefix(section, "<!DOCTYPE html>")},
	}); err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	assertions := `<script>
(async () => { const measurements = []; try {
  function check(condition, message) { if (!condition) throw new Error(message); }
  const tick = () => new Promise(resolve => setTimeout(resolve, 100));
  const frames = () => document.querySelectorAll('.tabframe');
  const buttons = document.querySelectorAll('.tab');
  const errors = [];
  window.addEventListener('error', event => errors.push(event.message));
  check(frames().length === 1, 'inactive tab must remain lazy');
  const first = frames()[0];
  await new Promise(resolve => { if (first.contentDocument?.getElementById('content')) resolve(); else first.addEventListener('load', resolve, {once: true}); });
  let writes = 0;
  new MutationObserver(records => { writes += records.length; }).observe(first, {attributes: true, attributeFilter: ['style']});
  async function measure(frame, label, expected) {
    await tick();
    const before = frame.offsetHeight;
    await tick();
    const after = frame.offsetHeight;
    measurements.push(label + '=' + before + '->' + after);
    check(before === after, label + ': height must settle');
    check(after === expected, label + ': expected ' + expected + 'px, got ' + after + 'px');
    check(frame.contentDocument.documentElement.scrollHeight <= frame.clientHeight, label + ': content must not be clipped');
    return after;
  }
  // 400px content + 30px section spacer + 14px padding + 32px margins + 24px clearance.
  await measure(first, 'initial', 500);
  const doc = first.contentDocument;
  doc.getElementById('content').style.height = '1400px';
  await measure(first, 'grown', 1500);
  doc.getElementById('content').style.height = '40px';
  await measure(first, 'shrunk', 140);
  const settledWrites = writes;
  await tick(); await tick();
  check(writes === settledWrites, 'idle frame must not keep writing its height');
  for (let height = 50; height <= 240; height += 10) doc.getElementById('content').style.height = height + 'px';
  await measure(first, 'burst', 340);
  check(writes === settledWrites + 1, 'burst of content changes must coalesce into one height write');
  const details = doc.createElement('details');
  details.innerHTML = '<summary style="height:20px">Details</summary><div style="height:600px"></div>';
  doc.body.insertBefore(details, doc.getElementById('spacer'));
  await measure(first, 'details closed', 360);
  details.open = true;
  await measure(first, 'details open', 960);
  details.open = false;
  await measure(first, 'details collapsed', 360);
  details.remove();
  // Width-dependent content must resize through the same observer path.
  const responsive = doc.createElement('style');
  responsive.textContent = '@media (max-width: 350px) { #content { height: 840px !important; } }';
  doc.head.append(responsive);
  first.style.width = '300px';
  await measure(first, 'narrow', 940);
  first.style.width = '';
  await measure(first, 'wide again', 340);
  buttons[1].click();
  check(frames().length === 2, 'second tab created on activation');
  const second = frames()[1];
  await new Promise(resolve => second.addEventListener('load', resolve, {once: true}));
  check(second.contentWindow.initialWidth > 0, 'lazy tab must initialize while visible');
  await measure(second, 'lazy', 500);
  const hiddenHeight = first.style.height;
  const hiddenWrites = writes;
  doc.getElementById('content').style.height = '700px';
  await tick(); await tick();
  check(first.style.height === hiddenHeight && writes === hiddenWrites, 'hidden frame must not be resized');
  buttons[0].click();
  await measure(first, 'reactivated', 800);
  check(frames().length === 2, 'reactivation must reuse the loaded frame');
  // Switch away while a resize is queued; only the active frame may be written.
  doc.getElementById('content').style.height = '200px';
  buttons[1].click(); buttons[0].click(); buttons[1].click();
  await measure(second, 'rapid switch', 500);
  check(first.style.height === '800px', 'queued resize must not write to the now-hidden frame');
  buttons[0].click();
  await measure(first, 'return after rapid switch', 300);
  check(!errors.length && !first.contentWindow.resizeErrors.length && !second.contentWindow.resizeErrors.length, 'no ResizeObserver loop errors: ' + errors.concat(first.contentWindow.resizeErrors, second.contentWindow.resizeErrors));
  await fetch('/result', {method: 'POST', body: 'PASS: ' + measurements.join(', ')});
} catch (error) {
  await fetch('/result', {method: 'POST', body: 'FAIL: ' + error.message + '; ' + measurements.join(', ')});
} })();
</script>`
	page := strings.Replace(string(html), "</body>", assertions+"</body>", 1)
	for _, size := range []string{"1440,1000", "390,844"} {
		t.Run(size, func(t *testing.T) {
			results := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/result" {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
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
			// Use real rendering frames: --dump-dom's virtual clock can advance
			// timers without delivering requestAnimationFrame/ResizeObserver callbacks.
			cmd := exec.CommandContext(ctx, browser, "--headless", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage", "--disable-background-networking", "--no-first-run", "--window-size="+size, "--user-data-dir="+filepath.Join(dir, "chrome-"+size), server.URL)
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
			var result string
			select {
			case result = <-results:
			case <-ctx.Done():
				t.Fatal("timed out waiting for browser resize assertions")
			}
			if !strings.HasPrefix(result, "PASS: ") {
				t.Fatalf("browser resize assertions failed: %s", result)
			}
			t.Log(result)
		})
	}
}
