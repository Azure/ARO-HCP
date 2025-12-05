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

package output

import (
	"fmt"
	"io"
	"strings"
	"text/template"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/yaml"
)

// summaryTemplate is a Go template for the summary output.
// Available fields:
//   - .TotalImages: total number of images processed
//   - .UpdatedCount: number of images updated
//   - .DryRun: whether this is a dry-run
const summaryTemplate = `
Summary
-------
{{- if .DryRun }}
Total images checked: {{ .TotalImages }}
Updates available:    {{ .UpdatedCount }}

This was a dry-run. No files were modified.
{{- else }}
Total images checked: {{ .TotalImages }}
Images updated:       {{ .UpdatedCount }}
{{- end }}
`

// commitMessageTemplate is a Go template for the commit message.
// Available fields in .Updates array:
//   - .Name: image name
//   - .OldSHA: old digest (truncated)
//   - .NewSHA: new digest (truncated)
//   - .Version: version tag
//   - .Timestamp: build timestamp
const commitMessageTemplate = `Updated images in target config files:

| Image | Old SHA | New SHA | Version | Timestamp |
|-------|---------|---------|---------|-----------|
{{- range .Updates }}
| {{ .Name }} | {{ .OldSHA }} | {{ .NewSHA }} | {{ .Version }} | {{ .Timestamp }} |
{{- end }}
`

// summaryData holds data for the summary template
type summaryData struct {
	TotalImages  int
	UpdatedCount int
	DryRun       bool
}

// updateData holds data for a single update in the commit message
type updateData struct {
	Name      string
	OldSHA    string
	NewSHA    string
	Version   string
	Timestamp string
}

// commitMessageData holds data for the commit message template
type commitMessageData struct {
	Updates []updateData
}

// PrintSummary prints a formatted summary of the update operation
func PrintSummary(w io.Writer, totalImages, updatedCount int, dryRun bool) {
	tmpl, err := template.New("summary").Parse(summaryTemplate)
	if err != nil {
		fmt.Fprintf(w, "error parsing summary template: %v\n", err)
		return
	}

	data := summaryData{
		TotalImages:  totalImages,
		UpdatedCount: updatedCount,
		DryRun:       dryRun,
	}

	if err := tmpl.Execute(w, data); err != nil {
		fmt.Fprintf(w, "error executing summary template: %v\n", err)
	}
}

// GenerateCommitMessage creates a markdown table commit message for the updated images
func GenerateCommitMessage(updates map[string][]yaml.Update) string {
	var allUpdates []yaml.Update
	for _, updates := range updates {
		allUpdates = append(allUpdates, updates...)
	}

	if len(allUpdates) == 0 {
		return ""
	}

	// Deduplicate updates by image name
	seen := make(map[string]bool)
	var uniqueUpdates []updateData
	for _, update := range allUpdates {
		if !seen[update.Name] {
			seen[update.Name] = true

			// Strip sha256: prefix if present, then take first 7 chars
			oldSHA := strings.TrimPrefix(update.OldDigest, "sha256:")
			if len(oldSHA) > 7 {
				oldSHA = oldSHA[:7] + "…"
			}

			newSHA := strings.TrimPrefix(update.NewDigest, "sha256:")
			if len(newSHA) > 7 {
				newSHA = newSHA[:7] + "…"
			}

			version := update.Tag
			if version == "" {
				version = "-"
			}

			timestamp := update.Date
			if timestamp == "" {
				timestamp = "-"
			}

			uniqueUpdates = append(uniqueUpdates, updateData{
				Name:      update.Name,
				OldSHA:    oldSHA,
				NewSHA:    newSHA,
				Version:   version,
				Timestamp: timestamp,
			})
		}
	}

	tmpl, err := template.New("commitMessage").Parse(commitMessageTemplate)
	if err != nil {
		return fmt.Sprintf("error parsing commit message template: %v", err)
	}

	data := commitMessageData{
		Updates: uniqueUpdates,
	}

	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		return fmt.Sprintf("error executing commit message template: %v", err)
	}

	return sb.String()
}
