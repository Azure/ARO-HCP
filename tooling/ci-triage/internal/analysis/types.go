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

import "github.com/Azure/ARO-HCP/tooling/ci-triage/internal/prow"

// SummaryResult is the top-level result from a health scan.
type SummaryResult struct {
	Envs          []EnvSummary   `json:"envs"`
	FleetFailures []FleetFailure `json:"fleet_failures"`
	FetchErrors   map[string]int `json:"fetch_errors"`
}

// EnvSummary summarizes one env/type combination.
type EnvSummary struct {
	Env         string   `json:"env"`
	Type        string   `json:"type"`
	Passed      int      `json:"passed"`
	Failed      int      `json:"failed"`
	Aborted     int      `json:"aborted"`
	PassRate    float64  `json:"pass_rate"`
	TopFailures []string `json:"top_failures"`
}

// FleetFailure represents a test failing across multiple envs.
type FleetFailure struct {
	Test string   `json:"test"`
	Envs []string `json:"envs"`
}

// FailuresResult is the top-level result from failures analysis.
type FailuresResult struct {
	Env         string           `json:"env"`
	Results     []FailureSummary `json:"results"`
	FetchErrors map[string]int   `json:"fetch_errors"`
}

// FailureSummary holds failure analysis for one env/type combination.
type FailureSummary struct {
	Env           string         `json:"env"`
	Type          string         `json:"type"`
	Passed        int            `json:"passed"`
	Failed        int            `json:"failed"`
	Aborted       int            `json:"aborted"`
	PassRate      float64        `json:"pass_rate"`
	FailureGroups []FailureGroup `json:"failure_groups"`
	PerJobTests   []PerJobEntry  `json:"per_job_tests"`
}

// FailureGroup represents a group of failures for a single test.
type FailureGroup struct {
	Test       string               `json:"test"`
	Count      int                  `json:"count"`
	Jobs       []string             `json:"jobs"`
	FirstSeen  string               `json:"first_seen"`
	LastSeen   string               `json:"last_seen"`
	LastPassed string               `json:"last_passed,omitempty"`
	Messages   []prow.DedupedMessage `json:"messages"`
	PRs        []int                `json:"prs"`
}

// PerJobEntry represents per-job test counts.
type PerJobEntry struct {
	Job      string `json:"job"`
	Started  string `json:"started"`
	Passed   int    `json:"passed"`
	Failed   int    `json:"failed"`
	Revision string `json:"revision,omitempty"`
}

// PRResult is the top-level result from PR triage.
type PRResult struct {
	PR          int            `json:"pr"`
	Envs        []PREnvResult  `json:"envs"`
	FetchErrors map[string]int `json:"fetch_errors"`
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
	Test     string               `json:"test"`
	Count    int                  `json:"count"`
	Baseline bool                 `json:"baseline"`
	Messages []prow.DedupedMessage `json:"messages"`
	Jobs     []string             `json:"jobs"`
}
