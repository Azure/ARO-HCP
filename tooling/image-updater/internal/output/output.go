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

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/yaml"
)

// PrintSummary prints a formatted summary of the update operation
func PrintSummary(w io.Writer, totalImages, updatedCount int, dryRun bool) {
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "Summary\n")
	fmt.Fprintf(w, "-------\n")
	if dryRun {
		fmt.Fprintf(w, "Total images checked: %d\n", totalImages)
		fmt.Fprintf(w, "Updates available:    %d\n", updatedCount)
		fmt.Fprintf(w, "\nThis was a dry-run. No files were modified.\n")
	} else {
		fmt.Fprintf(w, "Total images checked: %d\n", totalImages)
		fmt.Fprintf(w, "Images updated:       %d\n", updatedCount)
		if updatedCount > 0 {
			fmt.Fprintf(w, "\nFiles successfully updated!\n")
		} else {
			fmt.Fprintf(w, "\nAll images are up to date.\n")
		}
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
	var uniqueUpdates []yaml.Update
	for _, update := range allUpdates {
		if !seen[update.Name] {
			seen[update.Name] = true
			uniqueUpdates = append(uniqueUpdates, update)
		}
	}

	var sb strings.Builder
	sb.WriteString("Updated images for dev/int:\n\n")
	sb.WriteString("| Image | Old SHA | New SHA | Version | Timestamp |\n")
	sb.WriteString("|-------|---------|---------|---------|------------|\n")

	for _, update := range uniqueUpdates {
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

		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s |\n",
			update.Name, oldSHA, newSHA, version, timestamp))
	}

	return sb.String()
}
