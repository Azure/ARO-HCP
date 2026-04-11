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
	"sort"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/cisearch"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/config"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/gcs"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/prow"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/sippy"
)

// InvestigateResult holds deep investigation data for a single test.
type InvestigateResult struct {
	Test           string                `json:"test"`
	Env            string                `json:"env"`
	FailureCount   int                   `json:"failure_count"`
	Classification Classification        `json:"classification"`
	Messages       []prow.DedupedMessage `json:"messages,omitempty"`
	OtherEnvs      []string              `json:"other_envs,omitempty"`
	Rollout        *sippy.RolloutInfo    `json:"onset_rollout,omitempty"`
	FirstSeen      string                `json:"first_seen,omitempty"`
	LastSeen       string                `json:"last_seen,omitempty"`
	LastPassed     string                `json:"last_passed,omitempty"`

	// Deep GCS artifacts
	StepTimings []gcs.StepExecution `json:"step_timings,omitempty"`
	SlowestStep string              `json:"slowest_step,omitempty"`
	AzureLog    string              `json:"azure_log,omitempty"`

	// Cross-CI scope
	CrossCI *cisearch.ScopeResult `json:"cross_ci,omitempty"`
}

// Investigate runs deep investigation for a specific test in an environment.
// It chains Sippy data, GCS artifact analysis, and cross-CI search.
// If testName is empty, investigates the top failing test.
func Investigate(ctx context.Context, sc *sippy.Client, env, testName string, since time.Duration) (*InvestigateResult, error) {
	log := logr.FromContextOrDiscard(ctx)

	// Get failure data
	failuresResult, err := Failures(ctx, sc, env, since)
	if err != nil {
		return nil, err
	}

	// Find the target test
	var target *FailureGroup
	if testName == "" {
		// Pick top failure
		if len(failuresResult.FailureGroups) > 0 {
			target = &failuresResult.FailureGroups[0]
		}
	} else {
		for i := range failuresResult.FailureGroups {
			if failuresResult.FailureGroups[i].Test == testName {
				target = &failuresResult.FailureGroups[i]
				break
			}
		}
	}

	if target == nil {
		return &InvestigateResult{
			Test: testName,
			Env:  env,
			Classification: Classification{
				Class:      ClassUnknown,
				Confidence: "low",
				Reason:     "test not found in recent failures",
			},
		}, nil
	}

	result := &InvestigateResult{
		Test:           target.Test,
		Env:            env,
		FailureCount:   target.Count,
		Classification: target.Classification,
		Messages:       target.Messages,
		OtherEnvs:      target.OtherEnvs,
		Rollout:        target.OnsetRollout,
		FirstSeen:      target.FirstSeen,
		LastSeen:       target.LastSeen,
		LastPassed:     target.LastPassed,
	}

	// Deep GCS artifacts: fetch from a representative failing job
	if len(target.Jobs) > 0 {
		jobURL := config.NormalizeBaseURL(target.Jobs[0])
		fetchDeepArtifacts(ctx, log, env, jobURL, target.Test, result)
	}

	// Cross-CI scope check using the first failure message
	if len(target.Messages) > 0 {
		searchClient := cisearch.NewClient()
		msg := target.Messages[0].Msg
		// Truncate to first 100 chars for search (long messages are too specific)
		if len(msg) > 100 {
			msg = msg[:100]
		}
		scope, err := searchClient.IsAROSpecific(ctx, msg)
		if err != nil {
			log.V(1).Info("cross-CI search skipped", "error", err)
		} else {
			result.CrossCI = scope
		}
	}

	return result, nil
}

// fetchDeepArtifacts fetches step-graph timing and Azure API logs from GCS.
func fetchDeepArtifacts(ctx context.Context, log logr.Logger, env, jobURL, testName string, result *InvestigateResult) {
	gcsClient := gcs.NewClient(nil)

	// Step graph timing
	steps, err := gcsClient.FetchStepGraph(ctx, jobURL)
	if err != nil {
		log.V(1).Info("step graph fetch skipped", "error", err)
	} else if len(steps) > 0 {
		result.StepTimings = steps
		// Find slowest step
		sort.Slice(steps, func(i, j int) bool {
			return steps[i].DurationSecs > steps[j].DurationSecs
		})
		result.SlowestStep = steps[0].Name
	}

	// Azure API log for the specific test
	cfg, ok := config.Envs[env]
	if !ok {
		return
	}
	azLog, err := gcsClient.FetchAzureLog(ctx, jobURL, cfg.Step, cfg.Container, testName)
	if err != nil {
		log.V(1).Info("azure log fetch skipped", "error", err)
	} else if azLog != "" {
		// Truncate to last 200 lines to avoid huge output
		lines := splitLines(azLog)
		if len(lines) > 200 {
			lines = lines[len(lines)-200:]
		}
		result.AzureLog = joinLines(lines)
	}
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	result := lines[0]
	for _, l := range lines[1:] {
		result += "\n" + l
	}
	return result
}
