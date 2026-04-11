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
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/sippy"
)

// DiagnoseResult is the full synthesis output — verdict, confidence, evidence chain.
type DiagnoseResult struct {
	Env        string `json:"env"`
	Test       string `json:"test,omitempty"`
	Verdict    string `json:"verdict"`
	Confidence string `json:"confidence"` // "high", "medium", "low"
	Evidence   []string `json:"evidence"`
	NextSteps  []string `json:"next_steps,omitempty"`

	FleetHealth   *SummaryResult     `json:"fleet_health"`
	Investigation *InvestigateResult `json:"investigation,omitempty"`
	Correlation   *CorrelateResult   `json:"correlation,omitempty"`
}

// Diagnose runs a full synthesis for an environment (and optionally a specific test).
// It chains fleet health scan, failure analysis, deep investigation, and correlation
// to produce a verdict with confidence and evidence.
func Diagnose(ctx context.Context, sc *sippy.Client, env, testFilter string, since time.Duration) (*DiagnoseResult, error) {
	log := logr.FromContextOrDiscard(ctx)

	// 1. Fleet health
	summary, err := Summary(ctx, sc, since)
	if err != nil {
		return nil, fmt.Errorf("fleet health: %w", err)
	}

	// 2. Deep investigation
	investigation, err := Investigate(ctx, sc, env, testFilter, since)
	if err != nil {
		return nil, fmt.Errorf("investigation: %w", err)
	}

	// 3. Correlation (if we have onset data)
	var correlation *CorrelateResult
	if investigation.LastPassed != "" || investigation.FirstSeen != "" {
		corr, err := Correlate(ctx, sc, env, since, investigation.Test, 6*time.Hour)
		if err != nil {
			log.V(1).Info("correlation skipped", "error", err)
		} else {
			correlation = corr
		}
	}

	// 4. Synthesize verdict
	result := &DiagnoseResult{
		Env:           env,
		Test:          investigation.Test,
		FleetHealth:   summary,
		Investigation: investigation,
		Correlation:   correlation,
	}

	synthesizeVerdict(result)

	return result, nil
}

// synthesizeVerdict analyzes all evidence and produces a verdict with confidence.
func synthesizeVerdict(r *DiagnoseResult) {
	inv := r.Investigation
	if inv == nil {
		r.Verdict = "no investigation data available"
		r.Confidence = "low"
		return
	}

	var evidence []string
	var nextSteps []string

	// Classification-based verdict
	switch inv.Classification.Class {
	case ClassInfrastructure:
		r.Verdict = "infrastructure event — test failures are collateral"
		r.Confidence = inv.Classification.Confidence
		evidence = append(evidence, "Classified as infrastructure: "+inv.Classification.Reason)
		if inv.SlowestStep != "" {
			evidence = append(evidence, fmt.Sprintf("Slowest step: %s", inv.SlowestStep))
		}

	case ClassFleetWide:
		r.Verdict = fmt.Sprintf("fleet-wide failure — also failing in %v", inv.OtherEnvs)
		r.Confidence = "high"
		evidence = append(evidence, "Failing across multiple environments — likely code change, not env issue")

		// Check deployment + PR correlation for fleet-wide failures
		if r.Correlation != nil {
			for _, c := range r.Correlation.Correlations {
				if c.Test != inv.Test {
					continue
				}
				if c.Deployment != nil && c.Deployment.CommitRange != "" {
					r.Verdict = fmt.Sprintf("fleet-wide regression — deployment changed: %s", c.Deployment.CommitRange)
					evidence = append(evidence, fmt.Sprintf("Deployment correlation: %s", c.Deployment.CommitRange))
				}
				if len(c.MergedPRs) > 0 && len(c.MergedPRs) <= 3 {
					for _, pr := range c.MergedPRs {
						evidence = append(evidence, fmt.Sprintf("PR #%d (%s) by %s in onset window",
							pr.Number, pr.Title, pr.Author))
					}
					if len(c.MergedPRs) == 1 {
						r.Verdict = fmt.Sprintf("fleet-wide regression from PR #%d: %s",
							c.MergedPRs[0].Number, c.MergedPRs[0].Title)
					}
				}
				break
			}
		}

	case ClassRegression:
		r.Verdict = "regression — deterministic failure with onset"
		r.Confidence = inv.Classification.Confidence

		// Try to pinpoint the cause
		if r.Correlation != nil {
			for _, c := range r.Correlation.Correlations {
				if c.Test != inv.Test {
					continue
				}
				if c.Deployment != nil && c.Deployment.CommitRange != "" {
					r.Verdict = fmt.Sprintf("regression from deployment %s", c.Deployment.CommitRange)
					r.Confidence = "high"
					evidence = append(evidence, fmt.Sprintf("Deployment changed across onset: %s", c.Deployment.CommitRange))
				}
				if len(c.MergedPRs) > 0 && len(c.MergedPRs) <= 3 {
					for _, pr := range c.MergedPRs {
						evidence = append(evidence, fmt.Sprintf("PR #%d (%s) by %s in onset window",
							pr.Number, pr.Title, pr.Author))
					}
					if len(c.MergedPRs) == 1 {
						r.Verdict = fmt.Sprintf("regression from PR #%d: %s", c.MergedPRs[0].Number, c.MergedPRs[0].Title)
						r.Confidence = c.Confidence
					}
				}
				break
			}
		}

		if inv.LastPassed != "" {
			evidence = append(evidence, fmt.Sprintf("Onset: last passed %s, first failed %s", inv.LastPassed, inv.FirstSeen))
		}

	case ClassFlaky:
		r.Verdict = "flaky test — intermittent failures"
		r.Confidence = "medium"
		evidence = append(evidence, "Fails in <50% of runs with no clear onset")
		nextSteps = append(nextSteps, "Check if failure rate is increasing or stable")

	default:
		r.Verdict = "insufficient data for determination"
		r.Confidence = "low"
		nextSteps = append(nextSteps, "Extend lookback window with --since 14d")
	}

	// Add message evidence
	if len(inv.Messages) > 0 {
		evidence = append(evidence, fmt.Sprintf("Error: %s", truncate(inv.Messages[0].Msg, 200)))
	}

	// Add rollout evidence
	if inv.Rollout != nil {
		evidence = append(evidence, fmt.Sprintf("Onset rollout: commit=%s build=%s region=%s",
			inv.Rollout.Commit, inv.Rollout.Build, inv.Rollout.Region))
	}

	// Add cross-CI evidence
	if inv.CrossCI != nil {
		evidence = append(evidence, fmt.Sprintf("Cross-CI: %s (ARO=%d, all=%d)",
			inv.CrossCI.Assessment, inv.CrossCI.AROMatches, inv.CrossCI.AllMatches))
		if !inv.CrossCI.AROSpecific {
			r.Verdict += " [platform-wide]"
			nextSteps = append(nextSteps, "Check upstream OpenShift CI for related issues")
		}
	}

	// Add Azure log evidence
	if inv.AzureLog != "" {
		evidence = append(evidence, "Azure API log available — check for ARM operation failures")
	}

	// Add actionable next steps based on evidence
	if r.Correlation != nil {
		for _, c := range r.Correlation.Correlations {
			if c.Test != inv.Test {
				continue
			}
			if c.Deployment != nil && c.Deployment.CommitRange != "" {
				nextSteps = append(nextSteps,
					fmt.Sprintf("Run: git log %s --oneline", c.Deployment.CommitRange))
			}
			if len(c.MergedPRs) > 0 && c.MergedPRs[0].RelevanceScore > 0.3 {
				nextSteps = append(nextSteps,
					fmt.Sprintf("Inspect top suspect: gh pr diff %d", c.MergedPRs[0].Number))
			}
			break
		}
	}

	if r.Confidence == "low" {
		nextSteps = append(nextSteps, "Use build-log command to check raw execution logs")
		nextSteps = append(nextSteps, "Check if failure message points to specific component")
	}

	r.Evidence = evidence
	r.NextSteps = nextSteps
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
