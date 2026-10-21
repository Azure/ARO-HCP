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

package agent

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/internal/tabular"
)

// Hydrator takes a draft chain produced by the agent and populates share URIs
// and result tables for all Kusto proofs by re-running the queries deterministically.
type Hydrator struct {
	kustoClient   KustoClient
	kustoEndpoint string
	kustoDatabase string
	worktreePaths map[string]string // repo name → local checkout path
	testError     string            // contents of test error.log
	testOutput    string            // contents of test output.log
	dataDir       string            // root of the gathered data directory
}

// NewHydrator creates a Hydrator with the given Kusto client, cluster details,
// worktree paths for resolving code proof excerpts, test log contents for
// resolving log proof excerpts, and data directory for resolving discovery paths.
func NewHydrator(kustoClient KustoClient, kustoEndpoint, kustoDatabase string, worktreePaths map[string]string, testError, testOutput, dataDir string) *Hydrator {
	return &Hydrator{
		kustoClient:   kustoClient,
		kustoEndpoint: kustoEndpoint,
		kustoDatabase: kustoDatabase,
		worktreePaths: worktreePaths,
		testError:     testError,
		testOutput:    testOutput,
		dataDir:       dataDir,
	}
}

// queryToDeepLink compresses the input query with gzip and then encodes it to base64.
// Necessary to compress long queries to fit in the default browser URI length limits.
// Returns a Kusto deep link with encoded query and proper cluster/database.
// See: https://learn.microsoft.com/en-us/kusto/api/rest/deeplink
func queryToDeepLink(kustoEndpoint, kustoDatabase, query string) (string, error) {
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)

	if _, err := gzipWriter.Write([]byte(query)); err != nil {
		return "", fmt.Errorf("failed to write to gzip writer: %w", err)
	}

	if err := gzipWriter.Close(); err != nil {
		return "", fmt.Errorf("failed to close gzip writer: %w", err)
	}

	encodedQuery := base64.StdEncoding.EncodeToString(buf.Bytes())
	return fmt.Sprintf("%s/%s?query=%s", kustoEndpoint, kustoDatabase, encodedQuery), nil
}

// Hydrate takes a DraftChain and produces a HydratedChain by re-running all
// KQL queries and generating share URIs.
func (h *Hydrator) Hydrate(ctx context.Context, draft *DraftChain) (*HydratedChain, error) {
	result := &HydratedChain{
		RootCause:   draft.RootCause,
		Summary:     draft.Summary,
		Notes:       draft.Notes,
		Suggestions: draft.Suggestions,
	}

	// Backfill all discovery directories from the data dir. All pre-gathered
	// discovery data is included deterministically — the agent does not need
	// to reference data-dir paths in its output.
	backfillItems := h.backfillAllDiscovery(ctx)
	result.Discovery = append(result.Discovery, backfillItems...)

	// Hydrate agent-supplied discovery items (KQL only).
	for _, item := range draft.Discovery {
		if item.KQL == "" {
			continue
		}
		hd, err := h.hydrateDiscoveryKQL(ctx, item)
		if err != nil {
			slog.WarnContext(ctx, "Failed to hydrate agent-authored discovery query; continuing.", "label", item.Label, "error", err)
			continue
		}
		result.Discovery = append(result.Discovery, *hd)
	}

	// Hydrate chain links.
	for _, link := range draft.Chain {
		hl := HydratedLink{
			Question: link.Question,
			Answer:   link.Answer,
			Notes:    link.Notes,
		}

		for _, proof := range link.Proof {
			hp := HydratedProofItem{
				ProofItem: proof,
			}

			if proof.Type == "kusto" && proof.KQL != "" {
				shareURI, err := queryToDeepLink(h.kustoEndpoint, h.kustoDatabase, proof.KQL)
				if err != nil {
					return nil, fmt.Errorf("failed to generate share URI for proof on %q: %w", link.Question, err)
				}
				hp.ShareURI = shareURI

				tableRows, err := h.kustoClient.Query(ctx, proof.KQL)
				if err != nil {
					slog.WarnContext(ctx, "Failed to hydrate kusto proof query; continuing with empty table.", "question", link.Question, "error", err)
				} else {
					hp.Table = tableRows
				}
			}

			if proof.Type == "code" && proof.Repo != "" && proof.File != "" {
				excerpt, err := h.extractCodeExcerpt(proof.Repo, proof.File, proof.Lines[0], proof.Lines[1])
				if err != nil {
					slog.WarnContext(ctx, "Failed to extract code excerpt; continuing without excerpt.", "question", link.Question, "repo", proof.Repo, "file", proof.File, "error", err)
				} else {
					hp.CodeExcerpt = excerpt
				}
			}

			if proof.Type == "log" && proof.Source != "" {
				excerpt, err := h.extractLogExcerpt(proof.Source, proof.Lines[0], proof.Lines[1])
				if err != nil {
					slog.WarnContext(ctx, "Failed to extract log excerpt; continuing without excerpt.", "question", link.Question, "source", proof.Source, "error", err)
				} else {
					hp.LogExcerpt = excerpt
				}
			}

			hl.Proof = append(hl.Proof, hp)
		}

		result.Chain = append(result.Chain, hl)
	}

	return result, nil
}

// hydrateDiscoveryKQL hydrates an agent-authored KQL discovery item by generating
// a share URI and executing the query to produce a results table.
func (h *Hydrator) hydrateDiscoveryKQL(ctx context.Context, item DiscoveryItem) (*HydratedDiscovery, error) {
	hd := &HydratedDiscovery{
		Label: item.Label,
		KQL:   item.KQL,
	}

	shareURI, err := queryToDeepLink(h.kustoEndpoint, h.kustoDatabase, item.KQL)
	if err != nil {
		return nil, fmt.Errorf("failed to generate share URI: %w", err)
	}
	hd.ShareURI = shareURI

	table, err := h.kustoClient.Query(ctx, item.KQL)
	if err != nil {
		slog.WarnContext(ctx, "Failed to execute discovery query; continuing without results.", "label", item.Label, "error", err)
	} else {
		hd.Table = table
	}

	return hd, nil
}

// backfillAllDiscovery walks the entire data directory for directories named
// "discovery" and hydrates every Markdown file with a KQL block found within.
// This ensures all pre-gathered discovery data is included in the final output
// deterministically, without requiring the agent to reference paths.
func (h *Hydrator) backfillAllDiscovery(ctx context.Context) []HydratedDiscovery {
	var result []HydratedDiscovery

	_ = filepath.WalkDir(h.dataDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() || d.Name() != "discovery" {
			return nil
		}
		items, hydrateErr := h.hydrateDiscoveryDir(p)
		if hydrateErr != nil {
			slog.WarnContext(ctx, "Failed to hydrate discovery directory; skipping.", "path", p, "error", hydrateErr)
			return filepath.SkipDir
		}
		result = append(result, items...)
		return filepath.SkipDir
	})

	return result
}

// extractCodeExcerpt reads lines [start, end] (1-indexed, inclusive) from the
// given file in the repo's worktree. Returns empty string if the repo has no
// worktree path configured.
func (h *Hydrator) extractCodeExcerpt(repo, filePath string, start, end int) (string, error) {
	worktreePath, ok := h.worktreePaths[repo]
	if !ok || worktreePath == "" {
		return "", fmt.Errorf("no worktree path for repo %q", repo)
	}

	absPath := filepath.Join(worktreePath, filePath)
	f, err := os.Open(absPath)
	if err != nil {
		return "", fmt.Errorf("failed to open %s: %w", absPath, err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Warn("Failed to close file.", "path", absPath, "error", err)
		}
	}()

	var lines []string
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum >= start && lineNum <= end {
			lines = append(lines, scanner.Text())
		}
		if lineNum > end {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to read %s: %w", absPath, err)
	}
	if len(lines) == 0 {
		return "", fmt.Errorf("no lines found in range %d–%d of %s (file has %d lines)", start, end, absPath, lineNum)
	}

	return strings.Join(lines, "\n"), nil
}

// extractLogExcerpt extracts lines [start, end] (1-indexed, inclusive) from
// the test error or output log held in memory.
func (h *Hydrator) extractLogExcerpt(source string, start, end int) (string, error) {
	var content string
	switch source {
	case "error":
		content = h.testError
	case "output":
		content = h.testOutput
	default:
		return "", fmt.Errorf("unknown log source %q", source)
	}

	if content == "" {
		return "", fmt.Errorf("test %s log is empty", source)
	}

	allLines := strings.Split(content, "\n")
	if start < 1 || start > len(allLines) {
		return "", fmt.Errorf("line_start %d is out of range (log has %d lines)", start, len(allLines))
	}
	if end < start || end > len(allLines) {
		return "", fmt.Errorf("line_end %d is out of range (log has %d lines, start is %d)", end, len(allLines), start)
	}

	return strings.Join(allLines[start-1:end], "\n"), nil
}

// hydrateDiscoveryDir recursively walks a discovery directory and produces a
// HydratedDiscovery for every Markdown file that contains a KQL query block,
// regardless of nesting depth.
func (h *Hydrator) hydrateDiscoveryDir(absDir string) ([]HydratedDiscovery, error) {
	var items []HydratedDiscovery

	err := filepath.WalkDir(absDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}

		content, err := os.ReadFile(p)
		if err != nil {
			slog.Warn("Failed to read discovery markdown file; skipping.", "path", p, "error", err)
			return nil
		}

		kql := extractMarkdownKQL(string(content))
		if kql == "" {
			return nil // No KQL block — skip this file.
		}

		relPath, err := filepath.Rel(filepath.Dir(absDir), p)
		if err != nil {
			slog.Warn("Failed to compute relative path for discovery file; skipping.", "path", p, "error", err)
			return nil
		}
		// Strip the .md extension for the directory label.
		relPath = strings.TrimSuffix(relPath, ".md")

		// Use the parent directory name and file stem as a fallback label.
		parentDir := filepath.Base(filepath.Dir(p))
		factName := strings.TrimSuffix(d.Name(), ".md")

		hd := HydratedDiscovery{
			Directory: relPath,
			KQL:       kql,
		}

		// Extract label from ## Summary section.
		hd.Label = extractReadmeSummary(string(content))
		if hd.Label == "" {
			hd.Label = parentDir + " / " + factName
		}

		// Generate share URI.
		shareURI, err := queryToDeepLink(h.kustoEndpoint, h.kustoDatabase, hd.KQL)
		if err != nil {
			slog.Warn("Failed to generate share URI for discovery query; continuing.", "path", relPath, "error", err)
		} else {
			hd.ShareURI = shareURI
		}

		// Parse table from ## Results section.
		resultsMarkdown := extractMarkdownResults(string(content))
		if resultsMarkdown != "" {
			table, parseErr := parseMarkdownTable(resultsMarkdown)
			if parseErr != nil {
				slog.Warn("Failed to parse results table; continuing.", "path", relPath, "error", parseErr)
			} else {
				hd.Table = table
			}
		}

		items = append(items, hd)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk discovery directory: %w", err)
	}

	return items, nil
}

// extractReadmeSummary extracts the first paragraph under the "## Summary"
// heading from a README.md file. Returns empty string if not found.
func extractReadmeSummary(readme string) string {
	lines := strings.Split(readme, "\n")
	inSummary := false
	var summary []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "## Summary" {
			inSummary = true
			continue
		}
		if inSummary {
			// Stop at the next heading or end of content.
			if strings.HasPrefix(trimmed, "#") {
				break
			}
			if trimmed == "" && len(summary) > 0 {
				// End of first paragraph.
				break
			}
			if trimmed != "" {
				summary = append(summary, trimmed)
			}
		}
	}
	return strings.Join(summary, " ")
}

// extractMarkdownKQL extracts the content of the first fenced ```kql code block
// found under a "## Query" heading in a machine-generated discovery Markdown file.
// Returns empty string if no such block is found.
func extractMarkdownKQL(md string) string {
	lines := strings.Split(md, "\n")
	inQuerySection := false
	inCodeBlock := false
	var kqlLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "## Query" {
			inQuerySection = true
			continue
		}
		if inQuerySection && !inCodeBlock {
			// Stop at the next heading if we haven't entered a code block.
			if strings.HasPrefix(trimmed, "## ") {
				break
			}
			if trimmed == "```kql" {
				inCodeBlock = true
				continue
			}
		}
		if inCodeBlock {
			if trimmed == "```" {
				// End of code block — we have our KQL.
				return strings.Join(kqlLines, "\n")
			}
			kqlLines = append(kqlLines, line)
		}
	}
	return ""
}

// extractMarkdownResults extracts the raw markdown table content under the
// "## Results" heading in a machine-generated discovery Markdown file.
// Returns empty string if no results section is found.
func extractMarkdownResults(md string) string {
	lines := strings.Split(md, "\n")
	inResults := false
	var resultLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "## Results" {
			inResults = true
			continue
		}
		if inResults {
			if strings.HasPrefix(trimmed, "## ") {
				break
			}
			resultLines = append(resultLines, line)
		}
	}
	return strings.TrimSpace(strings.Join(resultLines, "\n"))
}

// parseMarkdownTable parses a pipe-delimited Markdown table into a tabular.Table.
// It expects the standard format: header row, separator row, then data rows.
// Returns nil (not an error) if the input is empty or has no table content.
func parseMarkdownTable(md string) (*tabular.Table, error) {
	lines := strings.Split(strings.TrimSpace(md), "\n")
	if len(lines) < 2 {
		return nil, nil
	}

	parseRow := func(line string) []string {
		// Trim leading/trailing pipes and split by pipe.
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "|")
		line = strings.TrimSuffix(line, "|")
		parts := strings.Split(line, "|")
		cells := make([]string, len(parts))
		for i, p := range parts {
			cells[i] = strings.TrimSpace(p)
		}
		return cells
	}

	columns := parseRow(lines[0])
	if len(columns) == 0 {
		return nil, nil
	}

	// Skip separator row (line 1), parse data rows.
	var rows [][]string
	for i := 2; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		row := parseRow(line)
		// Pad or trim to match column count.
		if len(row) < len(columns) {
			padded := make([]string, len(columns))
			copy(padded, row)
			row = padded
		} else if len(row) > len(columns) {
			row = row[:len(columns)]
		}
		rows = append(rows, row)
	}

	return &tabular.Table{
		Columns: columns,
		Rows:    rows,
	}, nil
}

// Validate checks hydration-specific requirements on a hydrated chain:
// - Every kusto proof has a non-empty share URI (generated during hydration)
// This is a lightweight post-hydration check; structural validation (non-empty
// summary, claims, proof items, etc.) is performed earlier by ValidateDraft.
func Validate(chain *HydratedChain) error {
	for i, link := range chain.Chain {
		for j, proof := range link.Proof {
			if proof.Type == "kusto" && proof.ShareURI == "" {
				return fmt.Errorf("chain link %d proof %d: kusto proof has empty share_uri after hydration", i, j)
			}
		}
	}
	return nil
}
