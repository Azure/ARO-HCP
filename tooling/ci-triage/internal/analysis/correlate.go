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

package analysis

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/github"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/sippy"
)

// Correlate maps failure onsets to merged PRs in the same time window.
func Correlate(ctx context.Context, sc *sippy.Client, env string, since time.Duration, testFilter string, window time.Duration) (*CorrelateResult, error) {
	log := logr.FromContextOrDiscard(ctx)

	response, err := sc.ListJobRuns(ctx, env, since)
	if err != nil {
		return nil, err
	}

	// Build onset data: for each failing test, find first_failed and last_passed
	type onset struct {
		firstFailed string
		lastPassed  string
	}
	onsets := make(map[string]*onset)

	// Collect all failing test names, filtering out meta-tests
	failingTests := make(map[string]bool)
	for _, run := range response.Rows {
		for _, testName := range run.FailedTestNames {
			if !isMetaTest(testName) {
				failingTests[testName] = true
			}
		}
	}

	// For each failing test, find onset
	for testName := range failingTests {
		if testFilter != "" && testName != testFilter {
			continue
		}

		o := &onset{}
		// Find first failure (earliest timestamp where test appears in failures)
		// Response is sorted newest first, so iterate backwards
		for i := len(response.Rows) - 1; i >= 0; i-- {
			run := response.Rows[i]
			if contains(run.FailedTestNames, testName) {
				o.firstFailed = run.Timestamp()
				break
			}
		}

		// Find last pass (latest job before first failure where test wasn't failing)
		for _, run := range response.Rows {
			// Must be before or at first failure
			if run.Timestamp() >= o.firstFailed {
				continue
			}
			if !run.Failed() || !contains(run.FailedTestNames, testName) {
				o.lastPassed = run.Timestamp()
				break
			}
		}

		if o.firstFailed != "" {
			onsets[testName] = o
		}
	}

	// For each onset, query GitHub for merged PRs in the window
	var correlations []Correlation
	for testName, o := range onsets {
		// Compute the onset window
		windowStart := o.lastPassed
		windowEnd := o.firstFailed

		// Expand by the window duration
		if windowStart != "" {
			if t, err := time.Parse(time.RFC3339, windowStart); err == nil {
				windowStart = t.Add(-window).Format("2006-01-02")
			} else {
				windowStart = windowStart[:10] // fallback to date portion
			}
		} else {
			// No last_passed found — use first_failed minus window
			if t, err := time.Parse(time.RFC3339, windowEnd); err == nil {
				windowStart = t.Add(-window).Format("2006-01-02")
			}
		}
		if t, err := time.Parse(time.RFC3339, windowEnd); err == nil {
			windowEnd = t.Add(window).Format("2006-01-02")
		} else {
			windowEnd = windowEnd[:10]
		}

		prs, err := github.ListMergedPRs(ctx, windowStart, windowEnd)
		if err != nil {
			log.V(1).Info("github query failed", "test", testName, "error", err)
			continue
		}

		// Score each PR by relevance to the failing test
		scored := make([]ScoredPR, len(prs))
		for i, pr := range prs {
			score, reason := scorePRRelevance(pr, testName)
			scored[i] = ScoredPR{MergedPR: pr, RelevanceScore: score, RelevanceReason: reason}
		}
		// Sort by relevance score descending
		sort.Slice(scored, func(i, j int) bool {
			return scored[i].RelevanceScore > scored[j].RelevanceScore
		})

		// Compute confidence based on window narrowness
		confidence := "low"
		confidenceReason := "many PRs in onset window"
		if len(prs) == 0 {
			confidence = "low"
			confidenceReason = "no PRs found in onset window — may be infrastructure or deployment"
		} else if len(prs) <= 2 {
			confidence = "high"
			confidenceReason = fmt.Sprintf("only %d PR(s) in narrow onset window", len(prs))
		} else if len(prs) <= 5 {
			confidence = "medium"
			confidenceReason = fmt.Sprintf("%d PRs in onset window — further investigation needed", len(prs))
		}

		// Extract deployment correlation from onset-window jobs (Phase 3 will enhance)
		var deployment *DeploymentCorrelation
		deployment = extractDeploymentCorrelation(response.Rows, o.firstFailed, o.lastPassed)

		if deployment != nil && deployment.FirstBadRollout != nil && deployment.LastGoodRollout != nil {
			if deployment.FirstBadRollout.Commit != deployment.LastGoodRollout.Commit {
				confidence = "high"
				confidenceReason = fmt.Sprintf("deployment changed: %s → %s",
					deployment.LastGoodRollout.Commit, deployment.FirstBadRollout.Commit)
			}
		}

		correlations = append(correlations, Correlation{
			Test:             testName,
			LastPassed:       o.lastPassed,
			FirstFailed:      o.firstFailed,
			OnsetWindow:      fmt.Sprintf("%s to %s", windowStart, windowEnd),
			Deployment:       deployment,
			Confidence:       confidence,
			ConfidenceReason: confidenceReason,
			MergedPRs:        scored,
		})
	}

	// Sort by confidence (high first), then by PR count (most specific first)
	confidenceOrder := map[string]int{"high": 0, "medium": 1, "low": 2}
	sort.Slice(correlations, func(i, j int) bool {
		ci := confidenceOrder[correlations[i].Confidence]
		cj := confidenceOrder[correlations[j].Confidence]
		if ci != cj {
			return ci < cj
		}
		return len(correlations[i].MergedPRs) < len(correlations[j].MergedPRs)
	})

	return &CorrelateResult{
		Env:          env,
		Correlations: correlations,
	}, nil
}

// extractDeploymentCorrelation finds the EV2 rollout info from the last-good
// and first-bad jobs around an onset boundary.
func extractDeploymentCorrelation(runs []sippy.JobRun, firstFailed, lastPassed string) *DeploymentCorrelation {
	if firstFailed == "" {
		return nil
	}

	var firstBadRollout, lastGoodRollout *sippy.RolloutInfo

	// Find the rollout on the first failing run
	for _, run := range runs {
		if run.Timestamp() == firstFailed {
			firstBadRollout = run.Rollout()
			break
		}
	}

	// Find the rollout on the last passing run
	if lastPassed != "" {
		for _, run := range runs {
			if run.Timestamp() == lastPassed {
				lastGoodRollout = run.Rollout()
				break
			}
		}
	}

	if firstBadRollout == nil && lastGoodRollout == nil {
		return nil
	}

	dc := &DeploymentCorrelation{
		FirstBadRollout: firstBadRollout,
		LastGoodRollout: lastGoodRollout,
	}

	if firstBadRollout != nil && lastGoodRollout != nil &&
		firstBadRollout.Commit != "" && lastGoodRollout.Commit != "" &&
		firstBadRollout.Commit != lastGoodRollout.Commit {
		dc.CommitRange = lastGoodRollout.Commit + ".." + firstBadRollout.Commit
	}

	return dc
}

// scorePRRelevance scores how likely a PR is related to a failing test.
// Returns a score (0.0-1.0) and a human-readable reason.
func scorePRRelevance(pr github.MergedPR, testName string) (float64, string) {
	score := 0.0
	var reasons []string

	// Map test name to likely code areas
	testLower := strings.ToLower(testName)
	for _, f := range pr.Files {
		fLower := strings.ToLower(f)

		// Test file matches test name keywords
		if strings.HasPrefix(fLower, "test/") {
			score += 0.3
			reasons = append(reasons, "touches test/ code")
			break
		}
	}

	for _, f := range pr.Files {
		fLower := strings.ToLower(f)

		// Infrastructure changes (bicep, config) affect all tests
		if strings.Contains(fLower, "dev-infrastructure/") || strings.HasSuffix(fLower, ".bicep") || strings.HasSuffix(fLower, ".bicepparam") {
			score += 0.4
			reasons = append(reasons, "infrastructure change (bicep)")
			break
		}
	}

	for _, f := range pr.Files {
		fLower := strings.ToLower(f)

		// Backend/frontend changes affect cluster operations
		if strings.HasPrefix(fLower, "backend/") || strings.HasPrefix(fLower, "frontend/") {
			if strings.Contains(testLower, "cluster") || strings.Contains(testLower, "nodepool") || strings.Contains(testLower, "update") || strings.Contains(testLower, "create") {
				score += 0.5
				reasons = append(reasons, "backend/frontend change + cluster-related test")
			} else {
				score += 0.3
				reasons = append(reasons, "backend/frontend change")
			}
			break
		}
	}

	for _, f := range pr.Files {
		fLower := strings.ToLower(f)

		// Cluster-service, maestro, hypershift changes
		if strings.HasPrefix(fLower, "cluster-service/") || strings.HasPrefix(fLower, "maestro/") || strings.HasPrefix(fLower, "hypershiftoperator/") {
			score += 0.4
			reasons = append(reasons, "cluster-service/maestro/hypershift change")
			break
		}
	}

	for _, f := range pr.Files {
		fLower := strings.ToLower(f)

		// Config/deploy changes can affect behavior
		if strings.Contains(fLower, "/deploy/") || strings.Contains(fLower, "config/") {
			score += 0.2
			reasons = append(reasons, "config/deploy change")
			break
		}
	}

	// Keyword matching between test name and PR title
	prLower := strings.ToLower(pr.Title)
	keywords := []string{"upgrade", "nodepool", "cluster", "credential", "auth", "version", "create", "update", "delete", "timeout", "retry"}
	for _, kw := range keywords {
		if strings.Contains(testLower, kw) && strings.Contains(prLower, kw) {
			score += 0.3
			reasons = append(reasons, fmt.Sprintf("keyword match: %q in both test and PR title", kw))
			break
		}
	}

	// Cap at 1.0
	if score > 1.0 {
		score = 1.0
	}

	reason := "no strong correlation"
	if len(reasons) > 0 {
		reason = strings.Join(reasons, "; ")
	}

	return score, reason
}
