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
	"fmt"
	"html/template"
	"io"
	"path"

	"github.com/Azure/ARO-HCP/test/pkg/citests"
)

// SummaryInput is everything the human-readable summary needs. Report and
// Summary are both optional so a run that stops before collecting can render
// only its note.
type SummaryInput struct {
	Report  *citests.PriorityReport
	Summary *Summary
	// Note is plain text and is always rendered as text, never as markup.
	Note string
	// PublicationURL, when set, is rendered as a link to the pull request this
	// run opened.
	PublicationURL string
}

// summaryData is the template view: presentation-ready values only, so the
// template stays free of formatting decisions.
type summaryData struct {
	Note            string
	PublicationURL  string
	WindowFrom      string
	WindowTo        string
	Origin          string
	Algorithm       string
	MinSamples      int
	GlobalMedian    string
	Environments    []coverageRow
	Changes         []changeRow
	Unchanged       int
	Insufficient    int
	Unmatched       int
	UnmatchedNames  []string
	HasCoverage     bool
	HasChangesTable bool
	// Declarations is false when the change set carries no source locations,
	// which is the verification path: listing an empty declaration would show
	// a placeholder location instead of saying nothing.
	Declarations bool
}

// coverageRow is one environment line of the coverage table.
type coverageRow struct {
	Environment string
	Builds      int
	Samples     int
	Median      string
	Latest      string
}

// changeRow is one proposed edit line of the changes table.
type changeRow struct {
	File         string
	FullPath     string
	Line         int
	OldPriority  int
	NewPriority  int
	Score        string
	Builds       int
	Environments string
	Spec         string
}

// WriteSummaryHTML renders the run summary as one self-contained HTML document.
// Prow's html lens sizes the frame from the measured content height, so the page
// ends with an in-content spacer rather than relying on body padding.
func WriteSummaryHTML(w io.Writer, input SummaryInput) error {
	data := summaryData{Note: input.Note, PublicationURL: input.PublicationURL}
	if input.Report != nil {
		report := input.Report
		data.WindowFrom = report.Selection.From.UTC().Format("2006-01-02 15:04:05")
		data.WindowTo = report.Selection.To.UTC().Format("2006-01-02 15:04:05")
		data.Origin = report.Selection.Origin
		data.Algorithm = report.Algorithm
		data.MinSamples = report.MinSamples
		data.GlobalMedian = "n/a"
		if report.GlobalMedianMinutes != nil {
			data.GlobalMedian = fmt.Sprintf("%.2fm", *report.GlobalMedianMinutes)
		}
		for _, environment := range report.Environments {
			latest := "-"
			if environment.LatestEligibleCompletion != nil {
				latest = environment.LatestEligibleCompletion.UTC().Format("2006-01-02 15:04:05")
			}
			data.Environments = append(data.Environments, coverageRow{
				Environment: environment.Environment,
				Builds:      environment.EligibleBuildCount,
				Samples:     environment.EligibleSampleCount,
				Median:      fmt.Sprintf("%.1fm", environment.MedianMinutes),
				Latest:      latest,
			})
		}
		data.HasCoverage = true
	}
	if input.Summary != nil {
		summary := input.Summary
		for _, change := range summary.Changes {
			data.Changes = append(data.Changes, changeRow{
				File:         path.Base(change.Path),
				FullPath:     fmt.Sprintf("%s:%d", change.Path, change.Line),
				Line:         change.Line,
				OldPriority:  change.OldPriority,
				NewPriority:  change.NewPriority,
				Score:        fmt.Sprintf("%.1fm", change.ScoreMinutes),
				Builds:       change.EligibleBuildCount,
				Environments: joinEnvironments(change.Environments),
				Spec:         change.Name,
			})
		}
		data.Unchanged = len(summary.Unchanged)
		data.Insufficient = len(summary.Insufficient)
		data.Unmatched = len(summary.Unmatched)
		data.UnmatchedNames = summary.Unmatched
		data.HasChangesTable = true
		data.Declarations = len(summary.Changes) > 0
		for _, change := range summary.Changes {
			if change.Path == "" {
				data.Declarations = false
				break
			}
		}
	}
	if err := summaryTemplate.Execute(w, data); err != nil {
		return fmt.Errorf("render summary: %w", err)
	}
	return nil
}

// joinEnvironments renders the environment list of one change.
func joinEnvironments(environments []string) string {
	result := ""
	for i, environment := range environments {
		if i > 0 {
			result += ", "
		}
		result += environment
	}
	return result
}

// summaryTemplate is the single source of the summary layout. Styling follows
// the PR lifecycle summary: transparent background, light and dark support, and
// internally scrollable tables so the page height stays predictable.
var summaryTemplate = template.Must(template.New("summary").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="color-scheme" content="light dark">
<title>E2E start priority refresh</title>
<style>
html, body { margin: 0; padding: 0; background: transparent; }
body { padding: 8px 10px 32px; font: 12px/1.45 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; color: #1f2328; }
h2 { margin: 14px 0 6px; font-size: 13px; }
.meta { margin: 0 0 10px; color: #59636e; }
.table-wrap { max-height: 320px; overflow: auto; border: 1px solid #d1d9e0; border-radius: 6px; }
table { border-collapse: separate; border-spacing: 0; width: 100%; }
table.changes { table-layout: fixed; }
table.changes th:nth-child(1) { width: 26%; }
table.changes th:nth-child(2) { width: 9%; }
table.changes th:nth-child(3) { width: 8%; }
table.changes th:nth-child(4) { width: 8%; }
table.changes th:nth-child(5) { width: 11%; }
table.changes th:nth-child(6) { width: 38%; }
th, td { text-align: left; padding: 5px 10px; border-bottom: 1px solid #d1d9e0; vertical-align: top; }
thead th { position: sticky; top: 0; z-index: 1; background: #f6f8fa; font-weight: 600; }
tbody tr:last-child td { border-bottom: 0; }
td.num { white-space: nowrap; font-variant-numeric: tabular-nums; }
td { overflow-wrap: anywhere; }
code { font: 11px ui-monospace, SFMono-Regular, Consolas, monospace; }
details { margin: 8px 0 0; }
details ul { max-height: 132px; overflow: auto; margin: 4px 0 0; padding-left: 20px; }
@media (prefers-color-scheme: dark) {
  body { color: #e6edf3; }
  .meta { color: #9198a1; }
  .table-wrap { border-color: #3d444d; }
  th, td { border-color: #3d444d; }
  thead th { background: #151b23; }
}
</style>
</head>
<body>
{{if .HasCoverage}}<p class="meta">
Window {{.WindowFrom}} to {{.WindowTo}} UTC, <code>{{.Origin}}</code>,
algorithm <code>{{.Algorithm}}</code>, minimum {{.MinSamples}} builds per spec and environment,
global median {{.GlobalMedian}}.
<br>{{.Note}}{{if .PublicationURL}}<br>Published pull request: <a href="{{.PublicationURL}}" target="_blank" rel="noopener noreferrer">{{.PublicationURL}}</a>{{end}}
</p>
{{else}}<p class="meta">{{.Note}}{{if .PublicationURL}}<br>Published pull request: <a href="{{.PublicationURL}}" target="_blank" rel="noopener noreferrer">{{.PublicationURL}}</a>{{end}}</p>
{{end}}
{{if .HasChangesTable}}<p class="meta">{{.Unchanged}} unchanged, {{.Insufficient}} insufficient, {{.Unmatched}} unmatched.</p>
{{if .UnmatchedNames}}<details><summary>Unmatched specs (retired or absent from this catalog)</summary><ul>
{{range .UnmatchedNames}}<li>{{.}}</li>{{end}}
</ul></details>
{{end}}{{end}}
{{if .HasCoverage}}<h2>Coverage</h2>
<div class="table-wrap"><table class="coverage">
<thead><tr><th>Environment</th><th>Eligible builds</th><th>Eligible samples</th><th>Median</th><th>Latest eligible run</th></tr></thead>
<tbody>
{{range .Environments}}<tr><td>{{.Environment}}</td><td class="num">{{.Builds}}</td><td class="num">{{.Samples}}</td><td class="num">{{.Median}}</td><td>{{.Latest}}</td></tr>
{{end}}</tbody></table></div>
{{end}}
{{if .HasChangesTable}}<h2>Proposed changes ({{len .Changes}})</h2>
<div class="table-wrap"><table class="changes">
<thead><tr>{{if .Declarations}}<th>Declaration</th>{{end}}<th>Priority</th><th>Score</th><th>Builds</th><th>Environments</th><th>Spec</th></tr></thead>
<tbody>
{{range .Changes}}<tr>{{if $.Declarations}}<td><code title="{{.FullPath}}">{{.File}}:{{.Line}}</code></td>{{end}}<td class="num">{{.OldPriority}} to {{.NewPriority}}</td><td class="num">{{.Score}}</td><td class="num">{{.Builds}}</td><td>{{.Environments}}</td><td>{{.Spec}}</td></tr>
{{end}}</tbody></table></div>
{{end}}
<div style="height:24px" aria-hidden="true"></div>
</body></html>
`))
