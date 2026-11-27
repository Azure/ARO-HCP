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

package summary

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// testReport returns a report with one changed and one unchanged component.
func testReport() Report {
	return Report{
		Revision:          "0123456789abcdef",
		Branch:            "main",
		TemplatizeSource:  "prebuilt from the image",
		GeneratedAt:       "2026-11-27T12:00:00Z",
		JobURL:            "https://prow.ci.openshift.org/view/gs/example/run",
		UpdatedComponents: 1,
		Updates: []Update{
			{Name: "hypershift", OldDigest: "sha256:1111", NewDigest: "sha256:2222", Tag: "v1", Status: "updated"},
			{Name: "thanos", OldDigest: "sha256:3333", NewDigest: "sha256:3333", Status: "unchanged"},
		},
		PullRequest: PullRequest{Number: 42, URL: "https://github.com/Azure/ARO-HCP/pull/42", HeadSHA: "abc123"},
	}
}

// TestRenderHTML checks the page carries the run evidence.
func TestRenderHTML(t *testing.T) {
	var out bytes.Buffer
	if err := RenderHTML(&out, testReport()); err != nil {
		t.Fatalf("RenderHTML returned an error: %v", err)
	}

	page := out.String()
	for _, want := range []string{
		"<code>main</code>",
		"<code>0123456789abcdef</code>",
		"prebuilt from the image",
		"Components changed: 1.",
		`href="https://github.com/Azure/ARO-HCP/pull/42"`,
		`href="https://prow.ci.openshift.org/view/gs/example/run"`,
		"#42",
		"<td>hypershift</td>",
		"<td>unchanged</td>",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("page does not contain %q", want)
		}
	}
}

// TestRenderHTMLEscapesInput checks component names cannot inject markup.
func TestRenderHTMLEscapesInput(t *testing.T) {
	report := testReport()
	report.Updates = []Update{{Name: `<script>alert(1)</script>`, Status: "updated"}}

	var out bytes.Buffer
	if err := RenderHTML(&out, report); err != nil {
		t.Fatalf("RenderHTML returned an error: %v", err)
	}

	if strings.Contains(out.String(), "<script>") {
		t.Fatalf("page contains unescaped markup: %s", out.String())
	}
	if !strings.Contains(out.String(), "&lt;script&gt;") {
		t.Fatalf("page does not contain the escaped name: %s", out.String())
	}
}

// TestEncodeJSON checks the JSON artifact carries the same report.
func TestEncodeJSON(t *testing.T) {
	var out bytes.Buffer
	if err := EncodeJSON(&out, testReport()); err != nil {
		t.Fatalf("EncodeJSON returned an error: %v", err)
	}

	var decoded Report
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("the encoded report is not valid JSON: %v", err)
	}
	if decoded.Revision != "0123456789abcdef" {
		t.Fatalf("decoded revision is %q", decoded.Revision)
	}
	if len(decoded.Updates) != 2 || decoded.Updates[0].Name != "hypershift" {
		t.Fatalf("decoded updates are %#v", decoded.Updates)
	}
	if decoded.PullRequest.Number != 42 {
		t.Fatalf("decoded pull request number is %d", decoded.PullRequest.Number)
	}
}

// TestRenderMarkdown checks the pull request body table.
func TestRenderMarkdown(t *testing.T) {
	report := testReport()
	report.Updates = append(report.Updates, Update{Name: "a|b", Status: "updated"})

	var out bytes.Buffer
	if err := RenderMarkdown(&out, report); err != nil {
		t.Fatalf("RenderMarkdown returned an error: %v", err)
	}

	body := out.String()
	for _, want := range []string{
		"| Image | Old SHA | New SHA | Version |",
		"| hypershift | `sha256:1111` | `sha256:2222` | v1 |",
		"| a\\|b |",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body does not contain %q: %s", want, body)
		}
	}
	if strings.Contains(body, "thanos") {
		t.Fatalf("body contains the unchanged component: %s", body)
	}
}
