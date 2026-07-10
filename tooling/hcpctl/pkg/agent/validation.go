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
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FirstChainQuestion is the required question for the first link in the causal chain.
const FirstChainQuestion = "Why did this test fail?"

// ValidationContext holds the data needed to validate a DraftChain beyond
// structural checks — file system paths, log contents, and worktree locations.
type ValidationContext struct {
	// ValidRepos is the set of repository names that have source code worktrees available.
	ValidRepos map[string]bool
	// WorktreePaths maps repository names to local filesystem paths for their git worktrees.
	WorktreePaths map[string]string
	// DataDir is the root of the gathered data directory (for discovery path validation).
	DataDir string
	// TestError is the contents of the test error.log file.
	TestError string
	// TestOutput is the contents of the test output.log file.
	TestOutput string
	// NodeConsoleLogs maps console log filenames to their contents.
	// Used for validating node_console_log proof items.
	NodeConsoleLogs map[string]string
}

// ValidateDraft checks a DraftChain for structural problems and executes every
// KQL snippet against the provided Kusto client. It validates log proof line
// ranges against actual log contents, code proof line ranges against actual
// source files, and discovery paths against the data directory.
// It returns a human-readable feedback string describing all issues found,
// suitable for sending back to the agent as a correction prompt. If everything
// is valid, the returned string is empty.
func ValidateDraft(ctx context.Context, client KustoClient, draft *DraftChain, vc *ValidationContext) string {
	var problems []string

	// Structural checks.
	if draft.RootCause == "" {
		problems = append(problems, "- The root_cause is empty. Every analysis must include a terse, one-sentence root cause.")
	}
	if draft.Summary == "" {
		problems = append(problems, "- The summary is empty. Every analysis must include a non-empty summary.")
	}
	if len(draft.Chain) == 0 {
		problems = append(problems, "- The chain is empty. Every analysis must include at least one causal chain link.")
	}
	for i, link := range draft.Chain {
		if i == 0 && link.Question != FirstChainQuestion {
			problems = append(problems, fmt.Sprintf(
				"- The first chain link's question must be exactly %q, but got %q.",
				FirstChainQuestion, link.Question,
			))
		} else if link.Question == "" {
			problems = append(problems, fmt.Sprintf("- Chain link %d has an empty question.", i))
		}
		if link.Answer == "" {
			problems = append(problems, fmt.Sprintf("- Chain link %d (%q) has an empty answer.", i, link.Question))
		}
		if len(link.Proof) == 0 {
			problems = append(problems, fmt.Sprintf("- Chain link %d (%q) has no proof items.", i, link.Question))
		}
		for j, proof := range link.Proof {
			switch proof.Type {
			case "kusto":
				if proof.KQL == "" {
					problems = append(problems, fmt.Sprintf("- Chain link %d (%q), proof #%d: kusto proof has empty KQL.", i, link.Question, j+1))
				}
			case "code":
				if proof.Repo == "" || proof.File == "" {
					problems = append(problems, fmt.Sprintf("- Chain link %d (%q), proof #%d: code proof is missing repo or file.", i, link.Question, j+1))
				} else if !vc.ValidRepos[proof.Repo] {
					var available []string
					for repo := range vc.ValidRepos {
						available = append(available, fmt.Sprintf("%q", repo))
					}
					sort.Strings(available)
					problems = append(problems, fmt.Sprintf(
						"- Chain link %d (%q), proof #%d: code proof references repository %q which is not available as a worktree. "+
							"Code proofs may only reference repositories with available source code. Available repositories: %s. "+
							"Either use one of the available repositories or convert this proof to a different type.",
						i, link.Question, j+1, proof.Repo, strings.Join(available, ", "),
					))
				} else if proof.Lines[0] < 1 || proof.Lines[1] < proof.Lines[0] {
					problems = append(problems, fmt.Sprintf(
						"- Chain link %d (%q), proof #%d: code proof has invalid line range [%d, %d] (must be 1-indexed with start <= end).",
						i, link.Question, j+1, proof.Lines[0], proof.Lines[1],
					))
				} else if worktreePath, ok := vc.WorktreePaths[proof.Repo]; ok {
					lineCount, err := countFileLines(filepath.Join(worktreePath, proof.File))
					if err != nil {
						problems = append(problems, fmt.Sprintf(
							"- Chain link %d (%q), proof #%d: code proof references file %q in repo %q which cannot be read: %s",
							i, link.Question, j+1, proof.File, proof.Repo, err.Error(),
						))
					} else if proof.Lines[1] > lineCount {
						problems = append(problems, fmt.Sprintf(
							"- Chain link %d (%q), proof #%d: code proof line range [%d, %d] exceeds the file length (%d lines) for %s in repo %q.",
							i, link.Question, j+1, proof.Lines[0], proof.Lines[1], lineCount, proof.File, proof.Repo,
						))
					}
				}
			case "log":
				if proof.Source != "error" && proof.Source != "output" && proof.Source != "node_console_log" {
					problems = append(problems, fmt.Sprintf(
						"- Chain link %d (%q), proof #%d: log proof has invalid source %q (must be \"error\", \"output\", or \"node_console_log\").",
						i, link.Question, j+1, proof.Source,
					))
				} else if proof.Source == "node_console_log" {
					if proof.File == "" {
						problems = append(problems, fmt.Sprintf(
							"- Chain link %d (%q), proof #%d: node_console_log proof is missing the file field specifying which console log to reference.",
							i, link.Question, j+1,
						))
					} else if proof.Lines[0] < 1 || proof.Lines[1] < proof.Lines[0] {
						problems = append(problems, fmt.Sprintf(
							"- Chain link %d (%q), proof #%d: log proof has invalid line range [%d, %d] (must be 1-indexed with start <= end).",
							i, link.Question, j+1, proof.Lines[0], proof.Lines[1],
						))
					} else if logContent, ok := vc.NodeConsoleLogs[proof.File]; !ok {
						var available []string
						for f := range vc.NodeConsoleLogs {
							available = append(available, fmt.Sprintf("%q", f))
						}
						sort.Strings(available)
						problems = append(problems, fmt.Sprintf(
							"- Chain link %d (%q), proof #%d: node_console_log proof references file %q which is not available. Available console logs: %s.",
							i, link.Question, j+1, proof.File, strings.Join(available, ", "),
						))
					} else {
						lineCount := strings.Count(logContent, "\n") + 1
						if proof.Lines[1] > lineCount {
							problems = append(problems, fmt.Sprintf(
								"- Chain link %d (%q), proof #%d: log proof line range [%d, %d] exceeds the console log %q length (%d lines).",
								i, link.Question, j+1, proof.Lines[0], proof.Lines[1], proof.File, lineCount,
							))
						}
					}
				} else if proof.Lines[0] < 1 || proof.Lines[1] < proof.Lines[0] {
					problems = append(problems, fmt.Sprintf(
						"- Chain link %d (%q), proof #%d: log proof has invalid line range [%d, %d] (must be 1-indexed with start <= end).",
						i, link.Question, j+1, proof.Lines[0], proof.Lines[1],
					))
				} else {
					var logContent string
					if proof.Source == "error" {
						logContent = vc.TestError
					} else {
						logContent = vc.TestOutput
					}
					if logContent == "" {
						problems = append(problems, fmt.Sprintf(
							"- Chain link %d (%q), proof #%d: log proof references %s log, but the %s log is empty.",
							i, link.Question, j+1, proof.Source, proof.Source,
						))
					} else {
						lineCount := strings.Count(logContent, "\n") + 1
						if proof.Lines[1] > lineCount {
							problems = append(problems, fmt.Sprintf(
								"- Chain link %d (%q), proof #%d: log proof line range [%d, %d] exceeds the %s log length (%d lines).",
								i, link.Question, j+1, proof.Lines[0], proof.Lines[1], proof.Source, lineCount,
							))
						}
					}
				}
			default:
				problems = append(problems, fmt.Sprintf("- Chain link %d (%q), proof #%d: unknown proof type %q (expected \"kusto\", \"code\", or \"log\").", i, link.Question, j+1, proof.Type))
			}
		}

		// The first chain link must include at least one log proof referencing
		// the test error log, so readers always see the failure output.
		if i == 0 {
			hasErrorLog := false
			for _, proof := range link.Proof {
				if proof.Type == "log" && proof.Source == "error" {
					hasErrorLog = true
					break
				}
			}
			if !hasErrorLog {
				problems = append(problems, "- The first chain link must include at least one log proof with source \"error\" referencing the test error log.")
			}
		}
	}

	// Discovery validation — each item must be a KQL query with a label.
	for i, item := range draft.Discovery {
		if item.KQL == "" {
			problems = append(problems, fmt.Sprintf(
				"- Discovery item %d has empty kql.",
				i,
			))
		}
		if item.Label == "" {
			problems = append(problems, fmt.Sprintf(
				"- Discovery item %d: agent-authored KQL discovery must have a non-empty label.",
				i,
			))
		}
	}

	// At least one code proof must exist somewhere in the chain — the agent must
	// cite specific source code to back claims about intended behavior.
	hasCodeProof := false
	for _, link := range draft.Chain {
		for _, proof := range link.Proof {
			if proof.Type == "code" {
				hasCodeProof = true
				break
			}
		}
		if hasCodeProof {
			break
		}
	}
	if !hasCodeProof && len(vc.ValidRepos) > 0 {
		var repos []string
		for repo := range vc.ValidRepos {
			repos = append(repos, fmt.Sprintf("%q", repo))
		}
		sort.Strings(repos)
		problems = append(problems, fmt.Sprintf(
			"- The chain contains no code proof items. When source code worktrees are available (%s), "+
				"the analysis must include at least one code proof citing the specific file and line range "+
				"that implements the behavior under investigation. Use code proofs to show *why* the system "+
				"behaves the way it does — for example, the code path that produces an error, the timeout "+
				"constant that was exceeded, or the retry logic that should have recovered. Read the source "+
				"code in the worktrees and add code proof items to the relevant chain links.",
			strings.Join(repos, ", "),
		))
	}

	// KQL execution checks — run every query to verify it is syntactically and
	// semantically valid against the target cluster, and that it returns data.
	for _, loc := range extractKQL(draft) {
		table, err := client.Query(ctx, loc.KQL)

		where := fmt.Sprintf("chain link %q, proof #%d", loc.Label, loc.ProofIndex+1)

		if err != nil {
			problems = append(problems, fmt.Sprintf(
				"- %s: KQL query failed when executed against Kusto:\n  Error: %s\n  Query:\n  ```kql\n  %s\n  ```",
				where, summarizeKustoError(err), loc.KQL,
			))
			continue
		}

		if table == nil || len(table.Rows) == 0 {
			problems = append(problems, fmt.Sprintf(
				"- %s: KQL query returned no rows. A query used as evidence must return data. "+
					"If the point of the query is to show that something did NOT happen, "+
					"use a `summarize count=count()` and explicitly show a zero count instead of an empty result set.\n"+
					"  Query:\n  ```kql\n  %s\n  ```",
				where, loc.KQL,
			))
		}
	}

	if len(problems) == 0 {
		return ""
	}

	return fmt.Sprintf(
		"Your output has %d %s that must be fixed. Please correct all issues and re-emit the complete JSON output.\n\n%s",
		len(problems),
		pluralize(len(problems), "problem", "problems"),
		strings.Join(problems, "\n\n"),
	)
}

// kqlLocation identifies where a KQL snippet came from in the draft chain.
type kqlLocation struct {
	Label      string // chain link question
	ProofIndex int    // proof item index
	KQL        string
}

// extractKQL collects all KQL snippets from chain link proofs with their locations.
func extractKQL(draft *DraftChain) []kqlLocation {
	var locs []kqlLocation
	for _, link := range draft.Chain {
		for j, proof := range link.Proof {
			if proof.Type == "kusto" && proof.KQL != "" {
				locs = append(locs, kqlLocation{
					Label:      link.Question,
					ProofIndex: j,
					KQL:        proof.KQL,
				})
			}
		}
	}
	return locs
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

// countFileLines counts the number of lines in a file.
func countFileLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Warn("Failed to close file.", "path", path, "error", err)
		}
	}()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		count++
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return count, nil
}
