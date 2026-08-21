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
)

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
