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
	// Intent is the human-written investigation objective. When non-empty the
	// validator uses intent-mode rules: the first chain question need not match
	// FirstChainQuestion, no error-log anchor is required, and a non-empty Title
	// is required. When empty, the strict test-mode rules apply.
	Intent string
}

// intentMode reports whether the validation context is for a free-form
// investigation rather than a failed-test analysis.
func (vc *ValidationContext) intentMode() bool {
	return vc != nil && vc.Intent != ""
}

// ValidationProblem describes a single structured validation issue found in a DraftChain.
type ValidationProblem struct {
	// Category is a machine-readable identifier for the type of problem.
	Category string `json:"category"`
	// Chain is the chain link index where the problem was found, or -1 for top-level issues.
	Chain int `json:"chain"`
	// Proof is the proof item index (0-based) where the problem was found, or -1 if N/A.
	Proof int `json:"proof"`
	// Detail is the human-readable description of the problem.
	Detail string `json:"detail"`
}

// ValidationResult holds the structured output of ValidateDraft.
type ValidationResult struct {
	// Problems is the list of validation issues found.
	Problems []ValidationProblem
	// Feedback is the human-readable text suitable for sending to the agent as
	// a correction prompt. It is empty when there are no problems.
	Feedback string
}

// ValidateDraft checks a DraftChain for structural problems and executes every
// KQL snippet against the provided Kusto client. It validates log proof line
// ranges against actual log contents, code proof line ranges against actual
// source files, and discovery paths against the data directory.
// It returns a ValidationResult containing structured problems and a
// human-readable feedback string for sending back to the agent.
func ValidateDraft(ctx context.Context, client KustoClient, draft *DraftChain, vc *ValidationContext) ValidationResult {
	var problems []ValidationProblem

	// Structural checks.
	if draft.RootCause == "" {
		problems = append(problems, ValidationProblem{
			Category: "empty_root_cause",
			Chain:    -1,
			Proof:    -1,
			Detail:   "- The root_cause is empty. Every analysis must include a terse, one-sentence root cause.",
		})
	}
	if draft.Summary == "" {
		problems = append(problems, ValidationProblem{
			Category: "empty_summary",
			Chain:    -1,
			Proof:    -1,
			Detail:   "- The summary is empty. Every analysis must include a non-empty summary.",
		})
	}
	// Classification checks.
	if draft.Classification == nil {
		problems = append(problems, ValidationProblem{
			Category: "missing_classification",
			Chain:    -1,
			Proof:    -1,
			Detail:   "- The classification is missing. Every analysis must include a classification object with l1_category and confidence.",
		})
	} else {
		if !ValidL1Categories[draft.Classification.L1Category] {
			var allowed []string
			for cat := range ValidL1Categories {
				allowed = append(allowed, fmt.Sprintf("%q", cat))
			}
			sort.Strings(allowed)
			problems = append(problems, ValidationProblem{
				Category: "invalid_l1_category",
				Chain:    -1,
				Proof:    -1,
				Detail: fmt.Sprintf(
					"- The classification l1_category %q is not valid. Must be one of: %s.",
					draft.Classification.L1Category, strings.Join(allowed, ", "),
				),
			})
		}
		if draft.Classification.L1Category == L1ProductFailures {
			if !ValidL2Subcategories[draft.Classification.L2Subcategory] {
				var allowed []string
				for sub := range ValidL2Subcategories {
					allowed = append(allowed, fmt.Sprintf("%q", sub))
				}
				sort.Strings(allowed)
				problems = append(problems, ValidationProblem{
					Category: "invalid_l2_subcategory",
					Chain:    -1,
					Proof:    -1,
					Detail: fmt.Sprintf(
						"- When l1_category is %q, l2_subcategory must be one of: %s. Got %q.",
						L1ProductFailures, strings.Join(allowed, ", "), draft.Classification.L2Subcategory,
					),
				})
			}
		} else if draft.Classification.L2Subcategory != "" {
			problems = append(problems, ValidationProblem{
				Category: "unexpected_l2_subcategory",
				Chain:    -1,
				Proof:    -1,
				Detail: fmt.Sprintf(
					"- l2_subcategory should be omitted when l1_category is %q (l2 is only for %q).",
					draft.Classification.L1Category, L1ProductFailures,
				),
			})
		}
		if draft.Classification.Confidence < 0 || draft.Classification.Confidence > 1 {
			problems = append(problems, ValidationProblem{
				Category: "invalid_confidence",
				Chain:    -1,
				Proof:    -1,
				Detail: fmt.Sprintf(
					"- The classification confidence must be between 0 and 1, got %g.",
					draft.Classification.Confidence,
				),
			})
		}
	}

	if len(draft.Chain) == 0 {
		problems = append(problems, ValidationProblem{
			Category: "empty_chain",
			Chain:    -1,
			Proof:    -1,
			Detail:   "- The chain is empty. Every analysis must include at least one causal chain link.",
		})
	}
	// In intent mode the analysis must carry a model-authored title, used as the
	// rendered document heading. Test mode falls back to the test name.
	if vc.intentMode() && draft.Title == "" {
		problems = append(problems, ValidationProblem{
			Category: "empty_title",
			Chain:    -1,
			Proof:    -1,
			Detail:   "- The title is empty. In an intent-driven investigation, provide a short title headlining the finding.",
		})
	}
	for i, link := range draft.Chain {
		if i == 0 && !vc.intentMode() && link.Question != FirstChainQuestion {
			problems = append(problems, ValidationProblem{
				Category: "wrong_first_question",
				Chain:    i,
				Proof:    -1,
				Detail: fmt.Sprintf(
					"- The first chain link's question must be exactly %q, but got %q.",
					FirstChainQuestion, link.Question,
				),
			})
		} else if link.Question == "" {
			problems = append(problems, ValidationProblem{
				Category: "empty_question",
				Chain:    i,
				Proof:    -1,
				Detail:   fmt.Sprintf("- Chain link %d has an empty question.", i),
			})
		}
		if link.Answer == "" {
			problems = append(problems, ValidationProblem{
				Category: "empty_answer",
				Chain:    i,
				Proof:    -1,
				Detail:   fmt.Sprintf("- Chain link %d (%q) has an empty answer.", i, link.Question),
			})
		}
		if len(link.Proof) == 0 {
			problems = append(problems, ValidationProblem{
				Category: "missing_proof",
				Chain:    i,
				Proof:    -1,
				Detail:   fmt.Sprintf("- Chain link %d (%q) has no proof items.", i, link.Question),
			})
		}
		for j, proof := range link.Proof {
			switch proof.Type {
			case "kusto":
				if proof.KQL == "" {
					problems = append(problems, ValidationProblem{
						Category: "empty_kql",
						Chain:    i,
						Proof:    j,
						Detail:   fmt.Sprintf("- Chain link %d (%q), proof #%d: kusto proof has empty KQL.", i, link.Question, j+1),
					})
				}
			case "code":
				if proof.Repo == "" || proof.File == "" {
					problems = append(problems, ValidationProblem{
						Category: "code_missing_fields",
						Chain:    i,
						Proof:    j,
						Detail:   fmt.Sprintf("- Chain link %d (%q), proof #%d: code proof is missing repo or file.", i, link.Question, j+1),
					})
				} else if !vc.ValidRepos[proof.Repo] {
					var available []string
					for repo := range vc.ValidRepos {
						available = append(available, fmt.Sprintf("%q", repo))
					}
					sort.Strings(available)
					problems = append(problems, ValidationProblem{
						Category: "code_invalid_repo",
						Chain:    i,
						Proof:    j,
						Detail: fmt.Sprintf(
							"- Chain link %d (%q), proof #%d: code proof references repository %q which is not available as a worktree. "+
								"Code proofs may only reference repositories with available source code. Available repositories: %s. "+
								"Either use one of the available repositories or convert this proof to a different type.",
							i, link.Question, j+1, proof.Repo, strings.Join(available, ", "),
						),
					})
				} else if proof.Lines[0] < 1 || proof.Lines[1] < proof.Lines[0] {
					problems = append(problems, ValidationProblem{
						Category: "code_invalid_lines",
						Chain:    i,
						Proof:    j,
						Detail: fmt.Sprintf(
							"- Chain link %d (%q), proof #%d: code proof has invalid line range [%d, %d] (must be 1-indexed with start <= end).",
							i, link.Question, j+1, proof.Lines[0], proof.Lines[1],
						),
					})
				} else if worktreePath, ok := vc.WorktreePaths[proof.Repo]; ok {
					lineCount, err := countFileLines(filepath.Join(worktreePath, proof.File))
					if err != nil {
						problems = append(problems, ValidationProblem{
							Category: "code_file_unreadable",
							Chain:    i,
							Proof:    j,
							Detail: fmt.Sprintf(
								"- Chain link %d (%q), proof #%d: code proof references file %q in repo %q which cannot be read: %s",
								i, link.Question, j+1, proof.File, proof.Repo, err.Error(),
							),
						})
					} else if proof.Lines[1] > lineCount {
						problems = append(problems, ValidationProblem{
							Category: "code_lines_exceed_file",
							Chain:    i,
							Proof:    j,
							Detail: fmt.Sprintf(
								"- Chain link %d (%q), proof #%d: code proof line range [%d, %d] exceeds the file length (%d lines) for %s in repo %q.",
								i, link.Question, j+1, proof.Lines[0], proof.Lines[1], lineCount, proof.File, proof.Repo,
							),
						})
					}
				}
			case "log":
				if proof.Source != "error" && proof.Source != "output" && proof.Source != "node_console_log" {
					problems = append(problems, ValidationProblem{
						Category: "log_invalid_source",
						Chain:    i,
						Proof:    j,
						Detail: fmt.Sprintf(
							"- Chain link %d (%q), proof #%d: log proof has invalid source %q (must be \"error\", \"output\", or \"node_console_log\").",
							i, link.Question, j+1, proof.Source,
						),
					})
				} else if proof.Source == "node_console_log" {
					if proof.File == "" {
						problems = append(problems, ValidationProblem{
							Category: "log_missing_file",
							Chain:    i,
							Proof:    j,
							Detail: fmt.Sprintf(
								"- Chain link %d (%q), proof #%d: node_console_log proof is missing the file field specifying which console log to reference.",
								i, link.Question, j+1,
							),
						})
					} else if proof.Lines[0] < 1 || proof.Lines[1] < proof.Lines[0] {
						problems = append(problems, ValidationProblem{
							Category: "log_invalid_lines",
							Chain:    i,
							Proof:    j,
							Detail: fmt.Sprintf(
								"- Chain link %d (%q), proof #%d: log proof has invalid line range [%d, %d] (must be 1-indexed with start <= end).",
								i, link.Question, j+1, proof.Lines[0], proof.Lines[1],
							),
						})
					} else if logContent, ok := vc.NodeConsoleLogs[proof.File]; !ok {
						var available []string
						for f := range vc.NodeConsoleLogs {
							available = append(available, fmt.Sprintf("%q", f))
						}
						sort.Strings(available)
						problems = append(problems, ValidationProblem{
							Category: "log_missing_file",
							Chain:    i,
							Proof:    j,
							Detail: fmt.Sprintf(
								"- Chain link %d (%q), proof #%d: node_console_log proof references file %q which is not available. Available console logs: %s.",
								i, link.Question, j+1, proof.File, strings.Join(available, ", "),
							),
						})
					} else {
						lineCount := strings.Count(logContent, "\n") + 1
						if proof.Lines[1] > lineCount {
							problems = append(problems, ValidationProblem{
								Category: "log_lines_exceed_file",
								Chain:    i,
								Proof:    j,
								Detail: fmt.Sprintf(
									"- Chain link %d (%q), proof #%d: log proof line range [%d, %d] exceeds the console log %q length (%d lines).",
									i, link.Question, j+1, proof.Lines[0], proof.Lines[1], proof.File, lineCount,
								),
							})
						}
					}
				} else if proof.Lines[0] < 1 || proof.Lines[1] < proof.Lines[0] {
					problems = append(problems, ValidationProblem{
						Category: "log_invalid_lines",
						Chain:    i,
						Proof:    j,
						Detail: fmt.Sprintf(
							"- Chain link %d (%q), proof #%d: log proof has invalid line range [%d, %d] (must be 1-indexed with start <= end).",
							i, link.Question, j+1, proof.Lines[0], proof.Lines[1],
						),
					})
				} else {
					var logContent string
					if proof.Source == "error" {
						logContent = vc.TestError
					} else {
						logContent = vc.TestOutput
					}
					if logContent == "" {
						problems = append(problems, ValidationProblem{
							Category: "log_empty_source",
							Chain:    i,
							Proof:    j,
							Detail: fmt.Sprintf(
								"- Chain link %d (%q), proof #%d: log proof references %s log, but the %s log is empty.",
								i, link.Question, j+1, proof.Source, proof.Source,
							),
						})
					} else {
						lineCount := strings.Count(logContent, "\n") + 1
						if proof.Lines[1] > lineCount {
							problems = append(problems, ValidationProblem{
								Category: "log_lines_exceed_file",
								Chain:    i,
								Proof:    j,
								Detail: fmt.Sprintf(
									"- Chain link %d (%q), proof #%d: log proof line range [%d, %d] exceeds the %s log length (%d lines).",
									i, link.Question, j+1, proof.Lines[0], proof.Lines[1], proof.Source, lineCount,
								),
							})
						}
					}
				}
			default:
				problems = append(problems, ValidationProblem{
					Category: "unknown_proof_type",
					Chain:    i,
					Proof:    j,
					Detail:   fmt.Sprintf("- Chain link %d (%q), proof #%d: unknown proof type %q (expected \"kusto\", \"code\", or \"log\").", i, link.Question, j+1, proof.Type),
				})
			}
		}

		// In test mode, the first chain link must include at least one log proof
		// referencing the test error log, so readers always see the failure
		// output. Intent-driven investigations have no such anchor requirement.
		if i == 0 && !vc.intentMode() {
			hasErrorLog := false
			for _, proof := range link.Proof {
				if proof.Type == "log" && proof.Source == "error" {
					hasErrorLog = true
					break
				}
			}
			if !hasErrorLog {
				problems = append(problems, ValidationProblem{
					Category: "first_link_missing_error_log",
					Chain:    0,
					Proof:    -1,
					Detail:   "- The first chain link must include at least one log proof with source \"error\" referencing the test error log.",
				})
			}
		}
	}

	// Discovery validation — each item must be a KQL query with a label.
	for i, item := range draft.Discovery {
		if item.KQL == "" {
			problems = append(problems, ValidationProblem{
				Category: "discovery_empty_kql",
				Chain:    -1,
				Proof:    -1,
				Detail: fmt.Sprintf(
					"- Discovery item %d has empty kql.",
					i,
				),
			})
		}
		if item.Label == "" {
			problems = append(problems, ValidationProblem{
				Category: "discovery_empty_label",
				Chain:    -1,
				Proof:    -1,
				Detail: fmt.Sprintf(
					"- Discovery item %d: agent-authored KQL discovery must have a non-empty label.",
					i,
				),
			})
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
		problems = append(problems, ValidationProblem{
			Category: "no_code_proof",
			Chain:    -1,
			Proof:    -1,
			Detail: fmt.Sprintf(
				"- The chain contains no code proof items. When source code worktrees are available (%s), "+
					"the analysis must include at least one code proof citing the specific file and line range "+
					"that implements the behavior under investigation. Use code proofs to show *why* the system "+
					"behaves the way it does — for example, the code path that produces an error, the timeout "+
					"constant that was exceeded, or the retry logic that should have recovered. Read the source "+
					"code in the worktrees and add code proof items to the relevant chain links.",
				strings.Join(repos, ", "),
			),
		})
	}

	// KQL execution checks — run every query to verify it is syntactically and
	// semantically valid against the target cluster, and that it returns data.
	for _, loc := range extractKQL(draft) {
		table, err := client.Query(ctx, loc.KQL)

		where := fmt.Sprintf("chain link %q, proof #%d", loc.Label, loc.ProofIndex+1)

		if err != nil {
			problems = append(problems, ValidationProblem{
				Category: "kql_failed",
				Chain:    loc.ChainIndex,
				Proof:    loc.ProofIndex,
				Detail: fmt.Sprintf(
					"- %s: KQL query failed when executed against Kusto:\n  Error: %s\n  Query:\n  ```kql\n  %s\n  ```",
					where, summarizeKustoError(err), loc.KQL,
				),
			})
			continue
		}

		if table == nil || len(table.Rows) == 0 {
			problems = append(problems, ValidationProblem{
				Category: "kql_empty_result",
				Chain:    loc.ChainIndex,
				Proof:    loc.ProofIndex,
				Detail: fmt.Sprintf(
					"- %s: KQL query returned no rows. A query used as evidence must return data. "+
						"If the point of the query is to show that something did NOT happen, "+
						"use a `summarize count=count()` and explicitly show a zero count instead of an empty result set.\n"+
						"  Query:\n  ```kql\n  %s\n  ```",
					where, loc.KQL,
				),
			})
		}
	}

	if len(problems) == 0 {
		return ValidationResult{}
	}

	details := make([]string, len(problems))
	for i, p := range problems {
		details[i] = p.Detail
	}

	return ValidationResult{
		Problems: problems,
		Feedback: fmt.Sprintf(
			"Your output has %d %s that must be fixed. Please correct all issues and re-emit the complete JSON output.\n\n%s",
			len(problems),
			pluralize(len(problems), "problem", "problems"),
			strings.Join(details, "\n\n"),
		),
	}
}

// kqlLocation identifies where a KQL snippet came from in the draft chain.
type kqlLocation struct {
	Label      string // chain link question
	ChainIndex int    // chain link index
	ProofIndex int    // proof item index
	KQL        string
}

// extractKQL collects all KQL snippets from chain link proofs with their locations.
func extractKQL(draft *DraftChain) []kqlLocation {
	var locs []kqlLocation
	for i, link := range draft.Chain {
		for j, proof := range link.Proof {
			if proof.Type == "kusto" && proof.KQL != "" {
				locs = append(locs, kqlLocation{
					Label:      link.Question,
					ChainIndex: i,
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
