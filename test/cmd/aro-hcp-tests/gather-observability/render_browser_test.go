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

func TestRenderObservabilityBrowserResize(t *testing.T) {
	browser, err := exec.LookPath("google-chrome")
	if err != nil {
		t.Skip("google-chrome unavailable; skipping optional browser test")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "observability-summary.html")
	section := `<!DOCTYPE html><html><head><style>
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
		{Title: "Lazy", HTML: section},
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
					results <- string(body)
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
