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
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
)

// reviewMaxTableRows caps the rows of each table embedded in a review prompt.
// It bounds the review document (which re-includes every proof's results) so a
// few large tables cannot exhaust the model's context window, while still
// showing enough of each table — plus its true row count — to review it.
const reviewMaxTableRows = 50

// AnalyzeOptions configures a single analysis run.
type AnalyzeOptions struct {
	// Manifest is the raw manifest.json content.
	Manifest []byte

	// TestName is the name of the failed test (used for rendering).
	TestName string

	// Intent is the human-written investigation objective. When non-empty the
	// analysis runs in intent mode: intent-framed prompts, relaxed first-link
	// validation, and an objective-driven rendered document. When empty the
	// analysis runs in the original failed-test mode.
	Intent string

	// TestError is the content of test_logs/error.log, or empty.
	TestError string

	// TestOutput is the content of test_logs/output.log, or empty.
	TestOutput string

	// SiblingTests is the content of sibling_tests.json, or empty.
	SiblingTests string

	// DataDir is the root of the structured data directory.
	DataDir string

	// WorktreePaths maps repository names to local filesystem paths.
	WorktreePaths map[string]string

	// KustoCluster is the Kusto cluster URI for hydration share links.
	KustoCluster string

	// KustoDatabase is the Kusto database name.
	KustoDatabase string

	// MaxValidationRounds is the maximum number of parse/validate correction
	// rounds per validate-draft cycle. Zero defaults to 10.
	MaxValidationRounds int

	// ReviewRounds is the number of review passes. Zero defaults to 3.
	ReviewRounds int

	// NodeConsoleLogs maps console log filenames to their contents.
	// Used for validating and hydrating node_console_log proof items.
	NodeConsoleLogs map[string]string

	// NodeConsoleLogURLs maps console log filenames to artifact download URLs.
	// Used for populating ArtifactURL on hydrated node_console_log proof items.
	NodeConsoleLogURLs map[string]string

	// OnProgress, when set, is called with the latest fully hydrated analysis
	// after the initial hydration and after each completed review round. It lets
	// callers persist or display the best result so far, so that a later failure
	// (e.g. a review round exhausting the model's context window) degrades to
	// the most recent good analysis instead of losing everything.
	OnProgress func(ProgressUpdate)
}

// ProgressUpdate carries an intermediate, fully hydrated analysis emitted during
// a run. Markdown is the rendered document for the same chain, provided so
// consumers can surface incremental output without re-rendering.
type ProgressUpdate struct {
	// Phase identifies the checkpoint: "initial" for the pre-review analysis,
	// or "review-N" after the Nth review round.
	Phase string
	// HydratedChain is the latest fully validated and hydrated causal chain.
	HydratedChain *HydratedChain
	// Markdown is HydratedChain rendered to a document.
	Markdown string
}

// AnalyzeResult contains the output of a successful analysis.
type AnalyzeResult struct {
	// HydratedChain is the fully validated and hydrated causal chain.
	HydratedChain *HydratedChain

	// DraftChain is the last validated draft before the final hydration.
	DraftChain *DraftChain

	// Usage is the aggregate token usage from all LLM requests made during
	// the analysis.
	Usage UsageReport
}

// Analyze runs the full agentic analysis loop: initial prompt, validate-draft,
// hydrate, and review rounds. It requires an already-created Session and
// KustoClient. The caller is responsible for session lifecycle (create, save
// conversation, delete/disconnect) and Kusto client lifecycle (create, close).
//
// The function sends the initial prompt, validates and corrects the agent's
// output, hydrates proof items with real query results and code excerpts,
// then runs review rounds where the agent sees its rendered output and can
// refine it.
func Analyze(ctx context.Context, logger logr.Logger, session LLMSession, kustoClient KustoClient, opts AnalyzeOptions) (*AnalyzeResult, error) {
	maxValidationRounds := opts.MaxValidationRounds
	if maxValidationRounds <= 0 {
		maxValidationRounds = 10
	}
	reviewRounds := opts.ReviewRounds
	if reviewRounds <= 0 {
		reviewRounds = 3
	}

	// Phase 1: Send initial prompt.
	logger.Info("Sending initial analysis prompt.")
	var prompt string
	if opts.Intent != "" {
		prompt = BuildIntentInitialPrompt(opts.Intent, string(opts.Manifest), opts.TestError, opts.TestOutput, opts.SiblingTests, opts.DataDir, opts.WorktreePaths)
	} else {
		prompt = BuildInitialPrompt(string(opts.Manifest), opts.TestError, opts.TestOutput, opts.SiblingTests, opts.DataDir, opts.WorktreePaths)
	}
	output, err := session.SendAndWait(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("agent analysis failed: %w", err)
	}

	// Build validation context.
	validRepos := make(map[string]bool, len(opts.WorktreePaths))
	for repo := range opts.WorktreePaths {
		validRepos[repo] = true
	}
	vc := &ValidationContext{
		ValidRepos:      validRepos,
		WorktreePaths:   opts.WorktreePaths,
		DataDir:         opts.DataDir,
		TestError:       opts.TestError,
		TestOutput:      opts.TestOutput,
		NodeConsoleLogs: opts.NodeConsoleLogs,
		Intent:          opts.Intent,
	}

	// Phase 2: Validate draft loop.
	logger.Info("Validating agent output.")
	draftChain, _, err := ValidateDraftLoop(ctx, logger, session, kustoClient, vc, output, maxValidationRounds)
	if err != nil {
		return nil, err
	}

	// Log the validated draft for exemplar collection.
	if draftJSON, err := json.Marshal(draftChain); err != nil {
		logger.Error(err, "Failed to marshal draft chain to JSON for logging.")
	} else {
		logger.V(1).Info("Validated draft chain.", "draft", string(draftJSON))
	}

	// Phase 3: Hydration.
	logger.Info("Hydrating analysis.")
	hydrator := NewHydrator(kustoClient, opts.KustoCluster, opts.KustoDatabase, opts.WorktreePaths, opts.TestError, opts.TestOutput, opts.NodeConsoleLogs, opts.NodeConsoleLogURLs, opts.DataDir, opts.Intent)
	hydratedChain, err := hydrator.Hydrate(ctx, draftChain)
	if err != nil {
		return nil, fmt.Errorf("hydration failed: %w", err)
	}
	if err := Validate(hydratedChain); err != nil {
		return nil, fmt.Errorf("hydrated chain validation failed: %w", err)
	}

	// emitProgress renders the chain and hands the caller the latest good result
	// so partial output survives a later failure.
	emitProgress := func(phase string, chain *HydratedChain) {
		if opts.OnProgress == nil {
			return
		}
		opts.OnProgress(ProgressUpdate{
			Phase:         phase,
			HydratedChain: chain,
			Markdown:      RenderMarkdown(chain, renderTitle(chain, opts.TestName)),
		})
	}
	emitProgress("initial", hydratedChain)

	// Phase 4: Review rounds. A failure in any review round is non-fatal: review
	// passes only refine an already-complete analysis, so we keep the last good
	// result rather than discarding the whole run. This is what lets a run that
	// exhausts the context window mid-review still return a usable analysis.
	for review := 0; review < reviewRounds; review++ {
		logger.Info("Review pass.", "round", review+1)

		// Compact conversation: summarize prior reasoning, then reset active
		// context. The review prompt already contains the full rendered
		// analysis (all evidence), so the model gets both the summary of
		// prior reasoning and the complete evidence document.
		compactPrompt := "Before the next review pass, briefly summarize (2-3 paragraphs) " +
			"the key findings, root cause, and causal chain from our analysis so far. " +
			"Focus on the reasoning and conclusions, not the raw data."
		summary, compactErr := session.SendAndWait(ctx, compactPrompt)
		if compactErr != nil {
			logger.Info("Compaction summary failed; proceeding with context reset only.",
				"error", compactErr, "round", review+1)
			summary = ""
		}
		session.ResetHistory()

		// Bound the review document: cap each table's rows so a huge proof
		// table can't blow the model's context window during review. No table is
		// dropped — each keeps its columns, representative rows, and a total-row
		// count — so the model can still judge whether every proof is concise and
		// actually supports its claim. (Note: ResetHistory is a no-op on the
		// Copilot provider, so bounding what we send is what actually keeps the
		// review turn within the context window there.)
		rendered := RenderMarkdown(hydratedChain, renderTitle(hydratedChain, opts.TestName), WithMaxTableRows(reviewMaxTableRows))
		reviewPrompt := BuildReviewPrompt(rendered)
		if summary != "" {
			reviewPrompt = "Context from prior analysis rounds:\n\n" + summary + "\n\n---\n\n" + reviewPrompt
		}

		reviewOutput, reviewErr := session.SendAndWait(ctx, reviewPrompt)
		if reviewErr != nil {
			logger.Info("Review round did not complete; keeping last good analysis.", "round", review+1, "error", reviewErr)
			break
		}

		newDraft, _, reviewErr := ValidateDraftLoop(ctx, logger, session, kustoClient, vc, reviewOutput, maxValidationRounds)
		if reviewErr != nil {
			logger.Info("Review round validation did not complete; keeping last good analysis.", "round", review+1, "error", reviewErr)
			break
		}

		newHydrated, reviewErr := hydrator.Hydrate(ctx, newDraft)
		if reviewErr != nil {
			logger.Info("Review round hydration failed; keeping last good analysis.", "round", review+1, "error", reviewErr)
			break
		}
		if reviewErr := Validate(newHydrated); reviewErr != nil {
			logger.Info("Review round produced an invalid analysis; keeping last good analysis.", "round", review+1, "error", reviewErr)
			break
		}

		// The round succeeded — adopt its refined result and checkpoint it.
		draftChain, hydratedChain = newDraft, newHydrated
		emitProgress(fmt.Sprintf("review-%d", review+1), hydratedChain)
	}

	return &AnalyzeResult{
		HydratedChain: hydratedChain,
		DraftChain:    draftChain,
		Usage:         session.Usage(),
	}, nil
}

// renderTitle picks the document heading for a hydrated chain: the
// model-authored Title in intent mode, otherwise the test name. In test mode
// (Intent empty) the test name is always used so headings stay backwards
// compatible even if the model emits a title opportunistically.
func renderTitle(chain *HydratedChain, testName string) string {
	if chain != nil && chain.Intent != "" && chain.Title != "" {
		return chain.Title
	}
	return testName
}

// ValidateDraftLoop parses and validates the agent's output, sending correction
// feedback for up to maxRounds iterations. It returns the validated draft chain
// and the raw output string (which may have been updated by agent corrections).
func ValidateDraftLoop(
	ctx context.Context,
	logger logr.Logger,
	session LLMSession,
	kustoClient KustoClient,
	vc *ValidationContext,
	output string,
	maxRounds int,
) (*DraftChain, string, error) {
	var draftChain *DraftChain
	var err error
	for attempt := 0; ; attempt++ {
		draftChain, err = ParseDraftChain(output)
		if err != nil {
			if attempt >= maxRounds {
				return nil, output, fmt.Errorf("failed to parse agent output as draft chain after %d correction rounds: %w", attempt, err)
			}
			logger.Info("Failed to parse agent output as JSON; sending correction to agent.", "attempt", attempt+1, "error", err)
			output, err = session.SendAndWait(ctx, fmt.Sprintf(
				"Your output could not be parsed as valid JSON: %v\n\nPlease re-emit the complete JSON output.", err,
			))
			if err != nil {
				return nil, output, fmt.Errorf("agent correction failed at attempt %d: %w", attempt+1, err)
			}
			continue
		}

		result := ValidateDraft(ctx, kustoClient, draftChain, vc)
		if result.Feedback == "" {
			break // all validation passed
		}

		if attempt >= maxRounds {
			logger.Info("Validation still has failures after max correction rounds; proceeding with best-effort.", "attempts", attempt, "problemCount", len(result.Problems), "problems", result.Problems)
			break
		}

		logger.Info("Validation found errors; sending corrections to agent.", "attempt", attempt+1, "problemCount", len(result.Problems), "problems", result.Problems)
		output, err = session.SendAndWait(ctx, result.Feedback)
		if err != nil {
			return nil, output, fmt.Errorf("agent correction failed at attempt %d: %w", attempt+1, err)
		}
	}
	return draftChain, output, nil
}

// BuildReviewPrompt constructs the prompt sent to the agent during review
// rounds, asking it to review and re-emit the analysis.
func BuildReviewPrompt(rendered string) string {
	return fmt.Sprintf(
		"Below is your analysis rendered as a complete document with query results.\n\n"+
			"Review it for:\n"+
			"1. **Narrative coherence** — does each answer directly and completely address its question? "+
			"Does each subsequent question follow naturally from the previous answer? "+
			"Do any answers 'jump' more than one layer down the stack, omitting crucial context?\n"+
			"2. **Evidence quality** — do the query results actually support the answers? "+
			"Are there unexpected results (empty tables, too many rows, irrelevant columns, repetitive output)?\n"+
			"3. **Depth** — have you stopped the chain too early? Could you ask another \"why?\" to get deeper?\n"+
			"4. **Accuracy** — now that you can see the actual query results, do any of your answers need revision?\n\n"+
			"**Important:** The output you produce is the final document shown to readers. "+
			"Do not mention the review process, do not add notes about what you changed or why, "+
			"and do not reference these instructions. The document should read as if it were "+
			"written correctly the first time.\n\n"+
			"Re-emit the complete corrected JSON output (even if no changes are needed). "+
			"---\n\n%s", rendered,
	)
}
