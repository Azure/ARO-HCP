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
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"github.com/jedib0t/go-pretty/v6/table"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/yaml"
)

const (
	// truncatedSHALength is the number of characters to show for truncated SHA digests
	truncatedSHALength = 7
)

// commitMessageTemplate is a Go template for the commit message.
// Available fields in .Updates array:
//   - .Name: image name
//   - .OldSHA: old digest (truncated)
//   - .NewSHA: new digest (truncated)
//   - .Version: version tag
//   - .Timestamp: build timestamp
const commitMessageTemplate = `| Image | Old SHA | New SHA | Version | Timestamp |
|-------|---------|---------|---------|-----------|
{{- range .Updates }}
| {{ .Name }} | {{ .OldSHA }} | {{ .NewSHA }} | {{ .Version }} | {{ .Timestamp }} |
{{- end }}
`

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

// GenerateCommitMessage creates a markdown table commit message for the updated images.
// Returns empty string if there are no updates to report.
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
		if seen[update.Name] {
			continue
		}
		seen[update.Name] = true

		uniqueUpdates = append(uniqueUpdates, updateData{
			Name:      update.Name,
			OldSHA:    truncateSHA(update.OldDigest, truncatedSHALength),
			NewSHA:    truncateSHA(update.NewDigest, truncatedSHALength),
			Version:   valueOrDefault(update.Tag, "-"),
			Timestamp: valueOrDefault(update.Date, "-"),
		})
	}

	tmpl, err := template.New("commitMessage").Parse(commitMessageTemplate)
	if err != nil {
		return fmt.Sprintf("error parsing commit message template: %v", err)
	}

	var sb strings.Builder
	if err := tmpl.Execute(&sb, commitMessageData{Updates: uniqueUpdates}); err != nil {
		return fmt.Sprintf("error executing commit message template: %v", err)
	}

	return sb.String()
}

// UpdateResult represents the result of updating a single image.
// This structure is used for formatting output in various formats (table, markdown, JSON).
type UpdateResult struct {
	Name      string `json:"name"`       // Component/image name
	OldDigest string `json:"old_digest"` // Previous digest (may include sha256: prefix)
	NewDigest string `json:"new_digest"` // New digest (may include sha256: prefix)
	Tag       string `json:"tag"`        // Version tag (e.g., v1.2.3)
	Date      string `json:"date"`       // Build/modification date (YYYY-MM-DD HH:MM format)
	Status    string `json:"status"`     // "updated", "unchanged", or "dry-run"
}

// FormatResults formats update results in the specified format.
// Supported formats: "table" (ASCII table), "markdown" (Markdown table), "json" (JSON array).
// For dry-run mode, status will be set to "dry-run" for changed images.
// Returns empty string if there are no results to format.
func FormatResults(updates map[string][]yaml.Update, format string, dryRun bool) (string, error) {
	if updates == nil {
		return "", fmt.Errorf("updates map is nil")
	}

	results := convertToResults(updates, dryRun)
	if len(results) == 0 {
		return "", nil
	}

	switch format {
	case "table":
		return formatTable(results), nil
	case "markdown":
		return formatMarkdown(results), nil
	case "json":
		return formatJSON(results)
	default:
		return "", fmt.Errorf("unsupported output format '%s': must be one of: table, markdown, json", format)
	}
}

// convertToResults converts yaml.Update map to UpdateResult slice.
// Deduplicates updates by image name (taking the first occurrence).
// Determines status based on whether digests changed and if it's a dry-run.
func convertToResults(updates map[string][]yaml.Update, dryRun bool) []UpdateResult {
	seen := make(map[string]bool)
	var results []UpdateResult

	for _, updateList := range updates {
		for _, update := range updateList {
			if seen[update.Name] {
				continue
			}
			seen[update.Name] = true

			// Determine status based on changes and mode
			status := "unchanged"
			if update.OldDigest != update.NewDigest {
				if dryRun {
					status = "dry-run"
				} else {
					status = "updated"
				}
			}

			results = append(results, UpdateResult{
				Name:      update.Name,
				OldDigest: update.OldDigest,
				NewDigest: update.NewDigest,
				Tag:       update.Tag,
				Date:      update.Date,
				Status:    status,
			})
		}
	}

	return results
}

// formatTable formats results as an ASCII table suitable for terminal output.
// Uses go-pretty/v6 for production-grade table rendering with proper alignment.
func formatTable(results []UpdateResult) string {
	if len(results) == 0 {
		return ""
	}

	t := table.NewWriter()
	t.SetStyle(table.StyleLight)
	t.Style().Options.SeparateRows = false
	t.Style().Options.DrawBorder = true
	t.AppendHeader(table.Row{"Name", "Old Digest", "New Digest", "Tag", "Date", "Status"})

	appendResultRows(t, results)

	return t.Render()
}

// formatMarkdown formats results as a Markdown table.
// Uses go-pretty/v6 for production-grade markdown table rendering.
// Suitable for embedding in documentation, pull requests, or issue comments.
func formatMarkdown(results []UpdateResult) string {
	if len(results) == 0 {
		return ""
	}

	t := table.NewWriter()
	t.AppendHeader(table.Row{"Name", "Old Digest", "New Digest", "Tag", "Date", "Status"})

	appendResultRows(t, results)

	return t.RenderMarkdown()
}

// appendResultRows appends formatted result rows to the table writer.
// Centralizes the common logic for formatting and adding rows.
func appendResultRows(t table.Writer, results []UpdateResult) {
	for _, result := range results {
		t.AppendRow(table.Row{
			result.Name,
			truncateDigest(result.OldDigest, 12),
			truncateDigest(result.NewDigest, 12),
			valueOrDefault(result.Tag, "-"),
			valueOrDefault(result.Date, "-"),
			result.Status,
		})
	}
}

// formatJSON formats results as a JSON array.
// Produces pretty-printed JSON with 2-space indentation for readability.
// Suitable for consumption by other tools and automation scripts.
func formatJSON(results []UpdateResult) (string, error) {
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal results to JSON: %w", err)
	}
	return string(data), nil
}

// truncateSHA truncates a SHA digest to the specified length with ellipsis.
// Strips the "sha256:" prefix if present.
func truncateSHA(digest string, length int) string {
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) > length {
		return digest[:length] + "…"
	}
	return digest
}

// truncateDigest truncates a digest to the specified length for display.
// Strips the "sha256:" prefix if present, then truncates to length and adds ellipsis.
// Returns the original digest if it's already shorter than the specified length.
func truncateDigest(digest string, length int) string {
	if digest == "" {
		return ""
	}

	// Strip sha256: prefix if present
	digest = strings.TrimPrefix(digest, "sha256:")

	if len(digest) > length {
		return digest[:length] + "…"
	}
	return digest
}

// valueOrDefault returns the value if non-empty, otherwise returns the default.
// Used for displaying "-" in place of empty values in tables.
func valueOrDefault(value, defaultValue string) string {
	if value == "" {
		return defaultValue
	}
	return value
}
