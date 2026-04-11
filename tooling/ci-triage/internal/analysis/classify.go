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
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/sippy"
)

// FailureClass categorizes the type of CI failure.
type FailureClass string

const (
	ClassInfrastructure FailureClass = "infrastructure" // Sippy infra flag or wipeout heuristic
	ClassRegression     FailureClass = "regression"     // deterministic, sharp onset
	ClassFlaky          FailureClass = "flaky"          // intermittent, no clear onset
	ClassFleetWide      FailureClass = "fleet_wide"     // failing in 2+ envs simultaneously
	ClassUnknown        FailureClass = "unknown"

	// wipeoutThreshold: if a single job has more than this fraction of tests
	// failing, it's likely an infrastructure event (provisioning failure, etc.)
	wipeoutThreshold = 0.8
	// flakyThreshold: if a test fails in less than this fraction of runs, it's flaky
	flakyThreshold = 0.5
)

// Classification holds the failure type, confidence, and reasoning.
type Classification struct {
	Class      FailureClass `json:"class"`
	Confidence string       `json:"confidence"` // "high", "medium", "low"
	Reason     string       `json:"reason"`
}

// ClassifyJob determines if a job run is an infrastructure event.
// A job with Sippy's infra flag set, or one where >80% of tests failed
// (wipeout), is classified as infrastructure.
func ClassifyJob(run sippy.JobRun, typicalTestCount int) Classification {
	if run.InfrastructureFailure {
		return Classification{
			Class:      ClassInfrastructure,
			Confidence: "high",
			Reason:     "Sippy classified as infrastructure failure",
		}
	}

	if typicalTestCount > 0 && len(run.FailedTestNames) > 0 {
		failRatio := float64(len(run.FailedTestNames)) / float64(typicalTestCount)
		if failRatio >= wipeoutThreshold {
			return Classification{
				Class:      ClassInfrastructure,
				Confidence: "high",
				Reason:     "wipeout: >80% of tests failed in one run",
			}
		}
	}

	return Classification{
		Class:      ClassUnknown,
		Confidence: "low",
		Reason:     "job did not meet infrastructure criteria",
	}
}

// ClassifyTest determines the failure classification for a test based on
// its failure pattern, onset characteristics, and fleet context.
func ClassifyTest(failCount, totalRuns int, hasOnset bool, otherEnvs []string, infraRunFraction float64) Classification {
	// Fleet-wide takes priority: same test failing in 2+ envs = code change
	if len(otherEnvs) >= 1 {
		return Classification{
			Class:      ClassFleetWide,
			Confidence: "high",
			Reason:     "failing in multiple environments — likely code change, not environment issue",
		}
	}

	// Mostly occurring in infra-flagged runs
	if infraRunFraction > 0.5 {
		return Classification{
			Class:      ClassInfrastructure,
			Confidence: "medium",
			Reason:     "majority of failures occurred in infrastructure-flagged runs",
		}
	}

	// Intermittent: fails in less than half the runs
	if totalRuns > 0 && float64(failCount)/float64(totalRuns) < flakyThreshold {
		return Classification{
			Class:      ClassFlaky,
			Confidence: "medium",
			Reason:     "intermittent — fails in <50% of runs",
		}
	}

	// Deterministic with clear onset
	if hasOnset && failCount > 1 {
		confidence := "high"
		if totalRuns > 0 && float64(failCount)/float64(totalRuns) < 0.8 {
			confidence = "medium"
		}
		return Classification{
			Class:      ClassRegression,
			Confidence: confidence,
			Reason:     "deterministic failure with identifiable onset",
		}
	}

	// Deterministic without clear onset (always failing in window)
	if totalRuns > 0 && float64(failCount)/float64(totalRuns) >= flakyThreshold {
		return Classification{
			Class:      ClassRegression,
			Confidence: "medium",
			Reason:     "consistent failure pattern — onset may predate lookback window",
		}
	}

	return Classification{
		Class:      ClassUnknown,
		Confidence: "low",
		Reason:     "insufficient data for classification",
	}
}

// EstimateTypicalTestCount estimates the typical number of tests from successful job runs.
func EstimateTypicalTestCount(runs []sippy.JobRun) int {
	// Look at successful runs to estimate total test count.
	// Successful runs have 0 test failures, so we can't get count from them directly.
	// Instead, look at runs with some failures but not wipeouts — the max
	// (passed + failed) gives us the best estimate.
	// As a heuristic: look at the median of failed test counts from non-wipeout
	// failing runs. If no failures, use a default.
	maxSeen := 0
	for _, run := range runs {
		if run.Failed() && !run.InfrastructureFailure {
			total := len(run.FailedTestNames)
			if total > maxSeen {
				maxSeen = total
			}
		}
	}

	if maxSeen == 0 {
		// No failing runs with test details — use a reasonable default
		return 30
	}
	return maxSeen
}
