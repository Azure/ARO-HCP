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

package gathersnapshot

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

func TestHTMLOverviewBrowser(t *testing.T) {
	browser, err := exec.LookPath("google-chrome")
	if err != nil {
		t.Skip("google-chrome unavailable; skipping optional browser test")
	}
	dir := t.TempDir()
	manifests, reports := overviewFixture()
	if err := WriteHTMLOverview(dir, manifests, reports); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "snapshot-summary.html"))
	if err != nil {
		t.Fatal(err)
	}
	assertions := `<script>
(async () => { try {
  function check(condition, message) { if (!condition) throw new Error(message); }
  const tick = () => new Promise(resolve => setTimeout(resolve, 30));
  const tree = document.querySelector('.tree');
  const data = JSON.parse(document.getElementById('snapshot-data').textContent);
  const kql = JSON.parse(document.getElementById('snapshot-kql').textContent);
  const all = selector => [...tree.querySelectorAll(selector)];
  const root = () => [...tree.children];
  const filters = [...document.querySelectorAll('.filter-toggle')];
  const errors = [];
  addEventListener('error', event => errors.push(event.message));
  async function toggle(details, open) { details.open = open; await tick(); }
  function fits() {
    check(document.documentElement.scrollWidth <= innerWidth, 'page must fit viewport');
    for (const label of all('.label')) check(label.scrollWidth <= label.clientWidth + 1, 'labels must wrap, not clip');
  }
  check(!window.injected && !document.querySelector('img'), 'embedded JSON must not execute markup');
  check(root().length === 3 && all('li').length === 3, 'only section summaries initially rendered');
  check(all('details[open], .kql').length === 0, 'everything starts collapsed');
  check(root()[0].querySelector('.badge-fail').textContent === '1 failed', 'collapsed section failure count');
  check(root()[0].querySelector('.label').textContent === data[0].testName + ' / rg-one', 'literal section text');
  const initialElements = all('*').length;
  fits();
  const section = root()[0].querySelector('details');
  section.querySelector('summary').click();
  await tick();
  check(section.open, 'native summary click opens section');
  check(all('li').length === 5 && all('.kql').length === 0, 'section opens only its immediate children');
  let node = section.querySelector('ul > li > details');
  check(node.querySelector('.badge-fail').textContent === '1 failed', 'collapsed node failure count');
  await toggle(node, true);
  let category = node.querySelector('ul > li > details');
  check(category.querySelector('.badge-fail').textContent === '1 failed', 'collapsed category failure count');
  await toggle(category, true);
  check(category.querySelectorAll('ul > li').length === 3, 'category contains all queries');
  let query = category.querySelector('ul > li > details');
  check(query.querySelector('.label').textContent === data[0].nodes[0].children[0].queries[0].key, 'literal query label');
  check(all('.kql').length === 0, 'query text is not rendered before opening');
  await toggle(query, true);
  check(query.querySelector('.kql').textContent === kql[1], 'KQL is exact, including HTML-sensitive characters');
  check(!window.injected && !document.querySelector('img'), 'materialized text must not execute markup');
  fits();
  await toggle(query, false);
  check(all('.kql').length === 0 && query.children.length === 1, 'closing query disposes text');
  await toggle(query, true);
  check(query.querySelector('.kql').textContent === kql[1], 'query reopens losslessly');
  await toggle(category, false);
  check(category.children.length === 1 && all('.kql').length === 0, 'closing category disposes queries');
  await toggle(section, false);
  check(all('*').length === initialElements, 'closing section restores baseline DOM');
  section.open = true; section.open = false; section.open = true;
  await tick();
  check(section.children.length === 2, 'coalesced toggles render once');
  await toggle(section, false);
  // Repeated traversal must not accumulate detached/connected child content.
  for (let i = 0; i < 3; i++) {
    await toggle(section, true);
    node = section.querySelector('ul > li > details');
    check(!node.open && node.children.length === 1, 'descendants reopen collapsed');
    await toggle(node, true);
    await toggle(section, false);
    check(all('*').length === initialElements, 'repeated traversal must bound DOM');
  }
  // Exercise every combination, preserving the original CSS intersection at
  // EACH level (mixed branches can remain visible with no matching leaves).
  for (let mask = 0; mask < 8; mask++) {
    filters.forEach((filter, index) => { filter.checked = !!(mask & (1 << index)); });
    filters[0].dispatchEvent(new Event('change'));
    check(all('details[open], .kql').length === 0, 'filter changes release open branches');
    const matches = tokens => filters.every(filter => !filter.checked || tokens.split(' ').includes(filter.id.slice(7)));
    async function visit(items, parent, level) {
      const expected = (items || []).filter(item => matches(level === 3 ? item.status : item.statuses));
      const rows = [...parent.children];
      check(rows.length === expected.length, 'filter count at level ' + level + ', mask ' + mask);
      for (let i = 0; i < rows.length; i++) {
        const item = expected[i], row = rows[i], details = row.firstElementChild;
        check(getComputedStyle(row).display !== 'none', 'JS filtering must agree with CSS');
        check(row.querySelector('.label').textContent === (level === 0 ? item.testName + ' / ' + item.resourceGroup : level === 3 ? item.key : item.name), 'filter order/text');
        if (level < 3) {
          await toggle(details, true);
          await visit([item.nodes, item.children, item.queries][level], details.lastElementChild, level + 1);
        } else if (item.kql) {
          await toggle(details, true);
          check(details.querySelector('.kql').textContent === kql[item.kql], 'filtered query content');
        } else {
          check(details.tagName === 'DIV' && !row.querySelector('details'), 'absent KQL remains a leaf');
        }
      }
    }
    await visit(data, tree, 0);
    fits();
  }
  check(document.querySelector('label[for="filter-pass"]').textContent === '2 passed', 'pass total unchanged');
  check(document.querySelector('label[for="filter-fail"]').textContent === '1 failed', 'fail total unchanged');
  check(document.querySelector('label[for="filter-skip"]').textContent === '2 skipped', 'skip total unchanged');
  check(!errors.length, 'no runtime errors: ' + errors.join(', '));
  await fetch('/result', {method: 'POST', body: 'PASS: lazy branches/query text, disposal, all filter intersections, ordering, escaping, counts, responsive width=' + innerWidth});
} catch (error) {
  await fetch('/result', {method: 'POST', body: 'FAIL: ' + error.stack});
} })();
</script>`
	page := strings.Replace(string(raw), "</body>", assertions+"</body>", 1)
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
				if r.URL.Path == "/" {
					// Chrome's outer window has a minimum width. An iframe exercises
					// the exact mobile viewport and the artifact's Spyglass context.
					_, _ = io.WriteString(w, `<iframe src="/overview" style="border:0;width:`+strings.Split(size, ",")[0]+`px;height:100vh"></iframe>`)
					return
				}
				_, _ = io.WriteString(w, page)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
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
			select {
			case result := <-results:
				if !strings.HasPrefix(result, "PASS: ") {
					t.Fatal(result)
				}
				t.Log(result)
			case <-ctx.Done():
				t.Fatal("timed out waiting for browser assertions")
			}
		})
	}
}
