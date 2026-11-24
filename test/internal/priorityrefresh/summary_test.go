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

package priorityrefresh

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/test/pkg/citests"
)

// summaryReport builds a report with two covered environments.
func summaryReport() citests.PriorityReport {
	median := 29.367
	from := time.Date(2026, 10, 19, 9, 34, 6, 0, time.UTC)
	to := time.Date(2026, 11, 18, 9, 34, 6, 0, time.UTC)
	latest := time.Date(2026, 11, 18, 7, 37, 28, 0, time.UTC)
	return citests.PriorityReport{
		SchemaVersion:       citests.ReportSchemaVersion,
		Algorithm:           citests.AlgorithmEnvironmentNormalizedMean,
		Selection:           citests.Selection{Origin: "https://hcp-dev-us-2.eastus2.kusto.windows.net/ServiceLogs", From: from, To: to, JobFilter: "e2e-parallel"},
		MinSamples:          3,
		GlobalMedianMinutes: &median,
		Environments: []citests.EnvironmentSummary{
			{Environment: "dev", EligibleBuildCount: 600, EligibleSampleCount: 24906, MedianMinutes: 28.0, LatestEligibleCompletion: &latest},
			{Environment: "int", EligibleBuildCount: 44, EligibleSampleCount: 3529, MedianMinutes: 29.9},
		},
	}
}

// summaryChanges builds a summary with one mapped change and one unmatched spec.
func summaryChanges() Summary {
	return Summary{
		Changes: []Change{{
			Name: "Customer should create a cluster", Path: "test/e2e/cluster.go", Line: 42,
			OldPriority: 50, NewPriority: 40, ScoreMinutes: 29.2, EligibleBuildCount: 477,
			Environments: []string{"dev", "int"},
		}},
		Unchanged: []string{"unchanged spec"},
		Unmatched: []string{"retired spec"},
	}
}

func TestWriteSummaryHTMLRendersDecision(t *testing.T) {
	report := summaryReport()
	summary := summaryChanges()
	var out bytes.Buffer
	err := WriteSummaryHTML(&out, SummaryInput{
		Report:  &report,
		Summary: &summary,
		Note:    "Read-only run: nothing was written.",
	})
	if err != nil {
		t.Fatal(err)
	}
	page := out.String()
	for _, want := range []string{
		"Window 2026-10-19 09:34:06 to 2026-11-18 09:34:06 UTC",
		"https://hcp-dev-us-2.eastus2.kusto.windows.net/ServiceLogs",
		"environment-normalized-mean-v1",
		"global median 29.37m",
		"Read-only run: nothing was written.",
		"1 unchanged, 0 insufficient, 1 unmatched",
		"<h2>Coverage</h2>",
		"<td>dev</td><td class=\"num\">600</td><td class=\"num\">24906</td><td class=\"num\">28.0m</td>",
		"<td>int</td>",
		"2026-11-18 07:37:28",
		"<h2>Proposed changes (1)</h2>",
		`title="test/e2e/cluster.go:42">cluster.go:42</code>`,
		"50 to 40",
		"29.2m",
		"<li>retired spec</li>",
		`style="height:24px"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("summary is missing %q", want)
		}
	}
}

func TestWriteSummaryHTMLEscapesSpecNames(t *testing.T) {
	summary := summaryChanges()
	summary.Changes[0].Name = `<script>alert("x")</script> & co`
	summary.Unmatched = []string{"<b>retired</b>"}
	var out bytes.Buffer
	if err := WriteSummaryHTML(&out, SummaryInput{Summary: &summary, Note: "note"}); err != nil {
		t.Fatal(err)
	}
	page := out.String()
	if strings.Contains(page, "<script>alert") || strings.Contains(page, "<b>retired</b>") {
		t.Fatalf("unescaped markup reached the page:\n%s", page)
	}
	for _, want := range []string{`&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt; &amp; co`, `&lt;b&gt;retired&lt;/b&gt;`} {
		if !strings.Contains(page, want) {
			t.Errorf("summary is missing escaped text %q", want)
		}
	}
}

func TestWriteSummaryHTMLOmitsUnlocatedDeclarations(t *testing.T) {
	// The verification path classifies changes without mapping source files, so
	// the page must not present a placeholder location for them.
	summary := Summary{Changes: []Change{{
		Name: "suite should be faster", OldPriority: 50, NewPriority: 40,
		ScoreMinutes: 29.2, EligibleBuildCount: 477, Environments: []string{"dev"},
	}}}
	var out bytes.Buffer
	if err := WriteSummaryHTML(&out, SummaryInput{Summary: &summary, Note: "verification"}); err != nil {
		t.Fatal(err)
	}
	page := out.String()
	if strings.Contains(page, "<th>Declaration</th>") {
		t.Errorf("verification page kept the declaration column:\n%s", page)
	}
	if strings.Contains(page, ":0<") || strings.Contains(page, "title=\":0\"") {
		t.Errorf("verification page rendered an empty location:\n%s", page)
	}
	if !strings.Contains(page, "50 to 40") || !strings.Contains(page, "suite should be faster") {
		t.Errorf("verification page lost the change itself:\n%s", page)
	}
}

func TestWriteSummaryHTMLNoticeWithoutData(t *testing.T) {
	var out bytes.Buffer
	if err := WriteSummaryHTML(&out, SummaryInput{Note: "Publication skipped: Azure/ARO-HCP#8096 is still open."}); err != nil {
		t.Fatal(err)
	}
	page := out.String()
	if !strings.Contains(page, "Publication skipped: Azure/ARO-HCP#8096 is still open.") {
		t.Fatalf("notice text missing:\n%s", page)
	}
	if strings.Contains(page, "<table") {
		t.Fatalf("notice page must not render tables:\n%s", page)
	}
	if !strings.Contains(page, `style="height:24px"`) {
		t.Fatal("notice page lost the trailing spacer")
	}
}

func TestWriteSummaryHTMLPublicationLinkIsAnAnchor(t *testing.T) {
	var out bytes.Buffer
	err := WriteSummaryHTML(&out, SummaryInput{
		Note:           "Published one pull request.",
		PublicationURL: "https://github.com/Azure/ARO-HCP/pull/9000",
	})
	if err != nil {
		t.Fatal(err)
	}
	page := out.String()
	if !strings.Contains(page, `Published pull request: <a href="https://github.com/Azure/ARO-HCP/pull/9000" target="_blank" rel="noopener noreferrer">`) {
		t.Errorf("publication link missing, not an anchor, or not opening outside the summary frame:\n%s", page)
	}
	if strings.Contains(page, "<table") {
		t.Errorf("notice page must not render tables:\n%s", page)
	}
}

func TestWriteSummaryHTMLKeepsNoteAsText(t *testing.T) {
	// A caller that hands over markup must see it escaped, never interpreted.
	var out bytes.Buffer
	if err := WriteSummaryHTML(&out, SummaryInput{Note: `published <a href="x">here</a>`}); err != nil {
		t.Fatal(err)
	}
	page := out.String()
	if strings.Contains(page, `<a href="x">here</a>`) {
		t.Fatalf("note markup was rendered as HTML:\n%s", page)
	}
	if !strings.Contains(page, "&lt;a href=&#34;x&#34;&gt;here&lt;/a&gt;") {
		t.Errorf("note was not escaped:\n%s", page)
	}
}
