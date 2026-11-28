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

// Package summary renders the human-readable and machine-readable artifacts of
// one image-updater run.
package summary

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"strings"
)

// Update describes one component's resolved digest.
type Update struct {
	Name      string `json:"name"`
	OldDigest string `json:"old_digest"`
	NewDigest string `json:"new_digest"`
	Tag       string `json:"tag"`
	Date      string `json:"date"`
	Status    string `json:"status"`
}

// PullRequest describes the pull request the run published.
type PullRequest struct {
	Number  int    `json:"number"`
	URL     string `json:"url"`
	HeadSHA string `json:"head_sha"`
}

// Report is the complete evidence of one image-updater run.
type Report struct {
	Revision          string      `json:"revision"`
	Branch            string      `json:"branch"`
	TemplatizeSource  string      `json:"templatize_source"`
	GeneratedAt       string      `json:"generated_at"`
	JobURL            string      `json:"job_url,omitempty"`
	Updates           []Update    `json:"updates"`
	PullRequest       PullRequest `json:"pull_request"`
	UpdatedComponents int         `json:"updated_components"`
}

const htmlTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Image updater summary</title>
<style>
:root{color-scheme:light dark}
body{box-sizing:border-box;margin:0;padding:0 10px 2px;max-width:64rem;font-family:system-ui,sans-serif;font-size:11px;line-height:1.25;color:#1f2328;background:#fff}
.meta{margin:4px 0 2px;color:#59636e;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
table{border-collapse:collapse;margin:2px 0 0;width:100%}
th,td{padding:0 8px;text-align:left;border-bottom:1px solid #d1d9e0;white-space:nowrap}
td.name{white-space:normal}
thead th{background:#f6f8fa;font-weight:600}
code{font-family:ui-monospace,monospace;font-size:11px;line-height:1.2}
a{color:#0969da}
@media (prefers-color-scheme: dark){
body{color:#e6edf3;background:#0d1117}
.meta{color:#9198a1}
th,td{border-color:#3d444d}
thead th{background:#151b23}
a{color:#4493f8}
}
</style>
</head>
<body>
<p class="meta">{{.Branch}} at <code>{{short .Revision}}</code> · {{.TemplatizeSource}} · components changed: {{.UpdatedComponents}} · generated {{.GeneratedAt}}{{if .PullRequest.URL}} · <a href="{{.PullRequest.URL}}">pull request #{{.PullRequest.Number}}</a> at <code>{{short .PullRequest.HeadSHA}}</code>{{end}}{{if .JobURL}} · <a href="{{.JobURL}}">job log</a>{{end}}</p>
<table>
<thead>
<tr><th>Component</th><th>Old digest</th><th>New digest</th><th class="tag">Tag</th><th class="date">Date</th><th>Status</th></tr>
</thead>
<tbody>
{{- range .Updates}}
<tr><td class="name">{{.Name}}</td><td class="digest"><code>{{short .OldDigest}}</code></td><td class="digest"><code>{{short .NewDigest}}</code></td><td class="tag">{{.Tag}}</td><td class="date">{{.Date}}</td><td>{{.Status}}</td></tr>
{{- end}}
</tbody>
</table>
</body>
</html>
`

// RenderHTML writes the self-contained summary page.
func RenderHTML(out io.Writer, report Report) error {
	funcs := template.FuncMap{
		"short": func(digest string) string {
			const keep = 19
			if len(digest) <= keep {
				return digest
			}
			return digest[:keep] + "…"
		},
	}
	tmpl, err := template.New("summary").Funcs(funcs).Parse(htmlTemplate)
	if err != nil {
		return fmt.Errorf("parse the summary template: %w", err)
	}
	if err := tmpl.Execute(out, report); err != nil {
		return fmt.Errorf("render the summary page: %w", err)
	}
	return nil
}

// EncodeJSON writes the machine-readable summary.
func EncodeJSON(out io.Writer, report Report) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("encode the summary: %w", err)
	}
	return nil
}

// RenderMarkdown writes the pull request body table for the changed components.
// Unchanged rows stay in the HTML and JSON artifacts, not in the body.
func RenderMarkdown(out io.Writer, report Report) error {
	if _, err := fmt.Fprintf(out, "| Image | Old SHA | New SHA | Version |\n|-------|---------|---------|---------|\n"); err != nil {
		return fmt.Errorf("write the markdown header: %w", err)
	}
	for _, update := range report.Updates {
		if update.Status == "unchanged" {
			continue
		}
		if _, err := fmt.Fprintf(out, "| %s | `%s` | `%s` | %s |\n", markdownCell(update.Name), update.OldDigest, update.NewDigest, markdownCell(update.Tag)); err != nil {
			return fmt.Errorf("write the markdown row for %q: %w", update.Name, err)
		}
	}
	return nil
}

// markdownCell escapes a table cell value.
func markdownCell(value string) string {
	return strings.ReplaceAll(value, "|", "\\|")
}
