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
body{font-family:system-ui,sans-serif;margin:2rem;max-width:60rem;color:#1f2328;background:#fff}
table{border-collapse:collapse;margin:1rem 0}
th,td{border:1px solid #d1d9e0;padding:0.25rem 0.6rem;text-align:left}
th{background:#f6f8fa}
td.digest{white-space:nowrap}
code{font-family:ui-monospace,monospace}
a{color:#0969da}
@media (prefers-color-scheme: dark){
body{color:#e6edf3;background:#0d1117}
th,td{border-color:#3d444d}
th{background:#161b22}
a{color:#4493f8}
}
</style>
</head>
<body>
<h2>Image updater summary</h2>
<p class="meta">Branch <code>{{.Branch}}</code> at <code>{{.Revision}}</code>.</p>
<p class="meta">Templatize: {{.TemplatizeSource}}. Generated at {{.GeneratedAt}}.</p>
{{- if .JobURL}}
<p class="meta">Run: <a href="{{.JobURL}}">job log</a>.</p>
{{- end}}
<p class="meta">Components changed: {{.UpdatedComponents}}.</p>
{{- if .PullRequest.URL}}
<p class="meta">Pull request: <a href="{{.PullRequest.URL}}">#{{.PullRequest.Number}}</a> at <code>{{.PullRequest.HeadSHA}}</code>.</p>
{{- else}}
<p class="meta">No pull request was published.</p>
{{- end}}
<table>
<thead>
<tr><th>Component</th><th>Old digest</th><th>New digest</th><th>Tag</th><th>Status</th></tr>
</thead>
<tbody>
{{- range .Updates}}
<tr><td>{{.Name}}</td><td class="digest"><code>{{short .OldDigest}}</code></td><td class="digest"><code>{{short .NewDigest}}</code></td><td>{{.Tag}}</td><td>{{.Status}}</td></tr>
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
