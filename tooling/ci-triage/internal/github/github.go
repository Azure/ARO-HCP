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

package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// MergedPR represents a merged pull request.
type MergedPR struct {
	Number   int      `json:"number"`
	Title    string   `json:"title"`
	Author   string   `json:"author"`
	MergedAt string   `json:"merged_at"`
	Files    []string `json:"files"`
}

// PRDetail holds metadata about a single PR.
type PRDetail struct {
	Title    string   `json:"title"`
	Author   string   `json:"author"`
	MergedAt string   `json:"mergedAt"`
	Files    []string `json:"files"`
}

// ghPRListEntry matches gh pr list --json output.
type ghPRListEntry struct {
	Number   int    `json:"number"`
	Title    string `json:"title"`
	MergedAt string `json:"mergedAt"`
	Author   struct {
		Login string `json:"login"`
	} `json:"author"`
	Files []struct {
		Path string `json:"path"`
	} `json:"files"`
}

// ghPRViewEntry matches gh pr view --json output.
type ghPRViewEntry struct {
	Title    string `json:"title"`
	MergedAt string `json:"mergedAt"`
	Author   struct {
		Login string `json:"login"`
	} `json:"author"`
	Files []struct {
		Path string `json:"path"`
	} `json:"files"`
}

// GetPR fetches metadata for a single PR using gh CLI.
// Returns nil (not error) if gh is unavailable.
func GetPR(ctx context.Context, number int) (*PRDetail, error) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "view",
		strconv.Itoa(number),
		"--repo", "Azure/ARO-HCP",
		"--json", "title,mergedAt,author,files",
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, nil // gh unavailable — non-fatal
	}

	var entry ghPRViewEntry
	if err := json.Unmarshal(out, &entry); err != nil {
		return nil, nil
	}

	files := make([]string, len(entry.Files))
	for i, f := range entry.Files {
		files[i] = f.Path
	}

	return &PRDetail{
		Title:    entry.Title,
		Author:   entry.Author.Login,
		MergedAt: entry.MergedAt,
		Files:    files,
	}, nil
}

// ListMergedPRs lists PRs merged between since and until (ISO dates) using gh CLI.
// Returns nil (not error) if gh is unavailable.
func ListMergedPRs(ctx context.Context, since, until string) ([]MergedPR, error) {
	searchQuery := fmt.Sprintf("merged:%s..%s", since, until)
	cmd := exec.CommandContext(ctx, "gh", "pr", "list",
		"--repo", "Azure/ARO-HCP",
		"--state", "merged",
		"--limit", "100",
		"--search", searchQuery,
		"--json", "number,title,mergedAt,author,files",
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, nil // gh unavailable — non-fatal
	}

	var entries []ghPRListEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, nil
	}

	result := make([]MergedPR, 0, len(entries))
	for _, e := range entries {
		files := make([]string, len(e.Files))
		for i, f := range e.Files {
			files[i] = f.Path
		}
		// Trim timestamp to date for cleaner display
		mergedAt := e.MergedAt
		if idx := strings.IndexByte(mergedAt, 'T'); idx > 0 {
			mergedAt = mergedAt[:idx]
		}
		result = append(result, MergedPR{
			Number:   e.Number,
			Title:    e.Title,
			Author:   e.Author.Login,
			MergedAt: mergedAt,
			Files:    files,
		})
	}

	return result, nil
}
