// Copyright 2025 Microsoft Corporation
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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderAlertsHTMLFiringDetails(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		condition string
		end       *time.Time
		want      []string
		absent    []string
	}{
		{
			name: "resolved", condition: "Resolved", end: mustTime("2026-04-13T07:12:34Z"),
			want:   []string{`class="condition-resolved">Resolved</span>`, "Duration: 12m34s", "Resolved: 2026-04-13 07:12:34"},
			absent: []string{"STILL FIRING", "Duration: still firing"},
		},
		{
			name: "still firing", condition: "Fired",
			want:   []string{`class="condition-fired">Fired <strong>STILL FIRING</strong></span>`, "Duration: still firing"},
			absent: []string{"Resolved: 2026-04-13"},
		},
	}
	for _, tt := range tests {
		for _, classification := range []string{"unknown", "known"} {
			t.Run(tt.name+"/"+classification, func(t *testing.T) {
				t.Parallel()
				data := alertsOutput{
					Summary:    alertsSummary{Total: 1},
					TimeWindow: timeWindow{Start: "2026-04-13T06:00:00Z", End: "2026-04-13T08:00:00Z"},
					Alerts: []alert{{
						Alert: alertData{
							Name: "TargetDown", Condition: tt.condition,
							StartsAt: mustTime("2026-04-13T07:00:00Z"), EndsAt: tt.end,
							Labels:      map[string]string{"namespace": "aro-hcp"},
							Description: "Azure alert description",
							Expression:  `sum(up{namespace="aro-hcp"}) < 1`,
							Annotations: map[string]string{
								"description": "Targets <unavailable>",
								"summary":     "Check targets & pods",
								"runbook_url": "https://example.com/runbook?alert=down&env=test",
							},
						},
						Metadata: alertMetadata{KnownIssue: classification == "known"},
					}},
				}
				for _, annotations := range []bool{true, false} {
					want := append([]string{
						"Fired: 2026-04-13 07:00:00", "Azure alert description",
						`<span class="label-key">namespace</span>=<span>aro-hcp</span>`,
						`sum(up{namespace=&#34;aro-hcp&#34;}) &lt; 1`,
					}, tt.want...)
					absent := append([]string{"<unavailable>"}, tt.absent...)
					if annotations {
						want = append(want, "Annotation description: Targets &lt;unavailable&gt;", "Summary: Check targets &amp; pods",
							`href="https://example.com/runbook?alert=down&amp;env=test"`)
					} else {
						data.Alerts[0].Alert.Annotations = nil
						absent = append(absent, "Annotation description:", "Summary:", "View Runbook")
					}
					output, err := renderAlertsHTML(data)
					if err != nil {
						t.Fatalf("renderAlertsHTML: %v", err)
					}
					for _, text := range want {
						if !strings.Contains(string(output), text) {
							t.Errorf("annotations=%t: output missing %q", annotations, text)
						}
					}
					for _, text := range absent {
						if strings.Contains(string(output), text) {
							t.Errorf("annotations=%t: output unexpectedly contains %q", annotations, text)
						}
					}
				}
			})
		}
	}
}

func TestRenderObservabilityPage(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "observability-summary.html")

	tabs := []observabilityTab{
		{Title: "Azure Monitor Alerts", HTML: "<html><body>alerts view</body></html>"},
		// Section HTML legitimately contains <script>...</script>; the closing
		// tag must not be able to terminate the wrapper page's own <script>.
		{Title: "Frontend Metrics", HTML: "<html><body><script>x=1;</script>MARKER_END</body></html>"},
	}

	if err := renderObservabilityPage(out, tabs); err != nil {
		t.Fatalf("renderObservabilityPage: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	s := string(data)

	// Single page carries the wrapper scaffolding and both tab titles.
	for _, want := range []string{`id="tabbar"`, "const TABS =", "Azure Monitor Alerts", "Frontend Metrics"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q", want)
		}
	}

	// The embedded </script> must be JSON/HTML-escaped so it cannot close the
	// page script early. If it leaked verbatim, everything after it (MARKER_END)
	// would escape into raw markup.
	if strings.Contains(s, "</script>MARKER_END") {
		t.Error("embedded </script> was not escaped; it would terminate the wrapper script")
	}
	if !strings.Contains(s, `\u003c/script\u003e`) {
		t.Error("expected the section's </script> to appear HTML-escaped in the JSON payload")
	}
}

func TestRenderObservabilityPageEmpty(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "observability-summary.html")
	if err := renderObservabilityPage(out, nil); err != nil {
		t.Fatalf("renderObservabilityPage(nil): %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !strings.Contains(string(data), "const TABS =") {
		t.Error("empty page should still render the wrapper scaffolding")
	}
}
