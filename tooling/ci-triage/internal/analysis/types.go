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
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/github"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/prow"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/sippy"
)

// SummaryResult is the top-level result from a fleet health scan.
type SummaryResult struct {
	Envs          []EnvSummary   `json:"envs"`
	FleetFailures []FleetFailure `json:"fleet_failures"`
}

// EnvSummary summarizes one environment.
type EnvSummary struct {
	Env           string   `json:"env"`
	Total         int      `json:"total"`
	Passed        int      `json:"passed"`
	Failed        int      `json:"failed"`
	PassRate      float64  `json:"pass_rate"`
	InfraFailures int      `json:"infra_failures,omitempty"`
	FlakyTests    []string `json:"flaky_tests,omitempty"`
	TopFailures   []string `json:"top_failures"`
}

// FleetFailure represents a test failing across multiple envs.
type FleetFailure struct {
	Test           string         `json:"test"`
	Envs           []string       `json:"envs"`
	Classification Classification `json:"classification"`
}

// FailuresResult is the top-level result from failure analysis.
type FailuresResult struct {
	Env           string             `json:"env"`
	Total         int                `json:"total"`
	Passed        int                `json:"passed"`
	Failed        int                `json:"failed"`
	PassRate      float64            `json:"pass_rate"`
	FailureGroups []FailureGroup     `json:"failure_groups"`
	InfraJobs     []InfraJobEntry    `json:"infra_jobs,omitempty"`
	FleetContext  map[string]float64 `json:"fleet_context,omitempty"`
	Rollout       *sippy.RolloutInfo `json:"rollout,omitempty"`
	PerJobTests   []PerJobEntry      `json:"per_job_tests"`
}

// InfraJobEntry represents a job classified as an infrastructure event.
type InfraJobEntry struct {
	URL            string         `json:"url"`
	Started        string         `json:"started"`
	FailedTests    int            `json:"failed_tests"`
	Classification Classification `json:"classification"`
}

// FailureGroup represents a group of failures for a single test.
type FailureGroup struct {
	Test           string                `json:"test"`
	Count          int                   `json:"count"`
	FirstSeen      string                `json:"first_seen"`
	LastSeen       string                `json:"last_seen"`
	LastPassed     string                `json:"last_passed,omitempty"`
	Classification Classification        `json:"classification"`
	OtherEnvs      []string              `json:"other_envs,omitempty"`
	OnsetRollout   *sippy.RolloutInfo    `json:"onset_rollout,omitempty"`
	Messages       []prow.DedupedMessage `json:"messages,omitempty"`
	Jobs           []string              `json:"jobs"`
}

// PerJobEntry represents per-job test counts.
type PerJobEntry struct {
	URL                   string             `json:"url"`
	Started               string             `json:"started"`
	State                 string             `json:"state"`
	Failed                int                `json:"failed"`
	InfrastructureFailure bool               `json:"infrastructure_failure,omitempty"`
	Rollout               *sippy.RolloutInfo `json:"rollout,omitempty"`
}

// PRResult is the top-level result from PR triage.
type PRResult struct {
	PR           int           `json:"pr"`
	Title        string        `json:"title,omitempty"`
	Author       string        `json:"author,omitempty"`
	MergedAt     string        `json:"merged_at,omitempty"`
	ChangedFiles []string      `json:"changed_files,omitempty"`
	Envs         []PREnvResult `json:"envs"`
}

// PREnvResult holds PR triage results for one environment.
type PREnvResult struct {
	Env         string           `json:"env"`
	Total       int              `json:"total"`
	Passed      int              `json:"passed"`
	Failed      int              `json:"failed"`
	HasBaseline bool             `json:"has_baseline"`
	Failures    []ClassifiedFail `json:"failures"`
}

// ClassifiedFail holds a classified test failure with baseline info.
type ClassifiedFail struct {
	Test     string                `json:"test"`
	Count    int                   `json:"count"`
	Baseline bool                  `json:"baseline"`
	Messages []prow.DedupedMessage `json:"messages,omitempty"`
	Jobs     []string              `json:"jobs"`
}

// TimelineResult is a time-series of job pass/fail.
type TimelineResult struct {
	Env     string          `json:"env"`
	Entries []TimelineEntry `json:"entries"`
}

// TimelineEntry is one job run in the timeline.
type TimelineEntry struct {
	URL                   string             `json:"url"`
	Started               string             `json:"started"`
	State                 string             `json:"state"`
	Failed                int                `json:"failed"`
	InfrastructureFailure bool               `json:"infrastructure_failure,omitempty"`
	Rollout               *sippy.RolloutInfo `json:"rollout,omitempty"`
	TestNames             []string           `json:"failed_tests,omitempty"`
}

// CorrelateResult maps failure onsets to merged PRs.
type CorrelateResult struct {
	Env          string        `json:"env"`
	Correlations []Correlation `json:"correlations"`
}

// DeploymentCorrelation connects a failure onset to a deployment change.
type DeploymentCorrelation struct {
	LastGoodRollout *sippy.RolloutInfo `json:"last_good_rollout,omitempty"`
	FirstBadRollout *sippy.RolloutInfo `json:"first_bad_rollout,omitempty"`
	CommitRange     string             `json:"commit_range,omitempty"`
}

// ScoredPR is a merged PR with a relevance score for correlation.
type ScoredPR struct {
	github.MergedPR
	RelevanceScore  float64 `json:"relevance_score"`
	RelevanceReason string  `json:"relevance_reason"`
}

// Correlation maps a single test's onset window to deployment changes and PRs.
type Correlation struct {
	Test             string                 `json:"test"`
	LastPassed       string                 `json:"last_passed"`
	FirstFailed      string                 `json:"first_failed"`
	OnsetWindow      string                 `json:"onset_window"`
	Deployment       *DeploymentCorrelation `json:"deployment,omitempty"`
	Confidence       string                 `json:"confidence"`
	ConfidenceReason string                 `json:"confidence_reason"`
	MergedPRs        []ScoredPR             `json:"merged_prs"`
}
