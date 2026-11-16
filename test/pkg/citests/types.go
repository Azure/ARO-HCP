// Copyright 2026 Microsoft Corporation
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

// Package citests collects E2E test observations from Kusto and turns them into
// start-priority proposals. It is E2E suite maintenance: collection, saved
// replay and scoring are pure data operations and never edit test sources.
package citests

import "time"

// BundleSchemaVersion identifies saved collection input, independently of report output.
const BundleSchemaVersion = 1

// ReportSchemaVersion identifies priority report output.
const ReportSchemaVersion = 1

// AlgorithmEnvironmentNormalizedMean names the scoring method: each spec's
// arithmetic mean per environment is divided by that environment's median and
// the best ratio is multiplied back by the pooled global median.
const AlgorithmEnvironmentNormalizedMean = "environment-normalized-mean-v1"

// DefaultMinSamples is the minimum number of distinct eligible builds a
// spec/environment stratum needs before it may propose a priority.
const DefaultMinSamples = 3

// Environment order used everywhere a stable output order is required.
const (
	EnvironmentDev     = "dev"
	EnvironmentInt     = "int"
	EnvironmentStg     = "stg"
	EnvironmentProd    = "prod"
	EnvironmentUnknown = "unknown"
)

// ScoringEnvironments lists the environments that participate in normalization,
// in output order. The unknown environment is kept as evidence only.
var ScoringEnvironments = []string{EnvironmentDev, EnvironmentInt, EnvironmentStg, EnvironmentProd}

// Exclusion reasons reported by the quality section, in counting order.
const (
	ExclusionAmbiguousExecution = "ambiguous-execution"
	ExclusionRunNotEligible     = "run-not-eligible"
	ExclusionUnknownEnvironment = "unknown-environment"
	ExclusionNotPassed          = "not-passed"
	ExclusionNonE2ESource       = "non-e2e-source"
	ExclusionUnusableName       = "unusable-name"
	ExclusionInvalidTiming      = "invalid-timing"
)

// QualityReasonNoEligibleSamples marks a report whose input held no usable execution.
const QualityReasonNoEligibleSamples = "no-eligible-samples"

// Priority test row statuses.
const (
	StatusScored           = "scored"
	StatusInsufficientData = "insufficient-data"
)

// Selection records the collection scope so replay needs no live flags.
// From is inclusive and To exclusive, both in UTC.
type Selection struct {
	Origin      string    `json:"origin"`
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	JobFilter   string    `json:"job_filter"`
	Environment string    `json:"environment,omitempty"`
}

// Run is one selected CI job run. Failed and unprocessed runs stay in the bundle
// so quality accounting and later reuse do not depend on a partial population.
type Run struct {
	BuildID       string    `json:"build_id"`
	JobName       string    `json:"job_name"`
	Environment   string    `json:"environment"`
	RuntimeRegion string    `json:"runtime_region,omitempty"`
	OverallResult string    `json:"overall_result"`
	Failed        bool      `json:"failed"`
	Processed     bool      `json:"processed"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	ProwURL       string    `json:"prow_url,omitempty"`
}

// Observation is one recorded test result of one run. Zero timestamps mean the
// producer did not report them. Duplicate rows are collapsed at collection time;
// differing executions of one test in one build are retained and never scored.
type Observation struct {
	BuildID          string    `json:"build_id"`
	TestID           string    `json:"test_id"`
	Name             string    `json:"name"`
	NameVariants     int       `json:"name_variants"`
	Source           string    `json:"source"`
	Result           string    `json:"result"`
	Failed           bool      `json:"failed"`
	StartedAt        time.Time `json:"started_at"`
	FinishedAt       time.Time `json:"finished_at"`
	SetupFinishTime  time.Time `json:"setup_finish_time"`
	TestStartTime    time.Time `json:"test_start_time"`
	CleanupStartTime time.Time `json:"cleanup_start_time"`
}

// Bundle is the complete saved collection input.
type Bundle struct {
	SchemaVersion int           `json:"schema_version"`
	Selection     Selection     `json:"selection"`
	Runs          []Run         `json:"runs"`
	Observations  []Observation `json:"observations"`
}

// PriorityOptions tunes scoring. MinSamples zero means DefaultMinSamples.
type PriorityOptions struct {
	MinSamples int `json:"min_samples"`
}

// EnvironmentSummary describes one normalization denominator.
type EnvironmentSummary struct {
	Environment              string     `json:"environment"`
	EligibleBuildCount       int        `json:"eligible_build_count"`
	EligibleSampleCount      int        `json:"eligible_sample_count"`
	MedianMinutes            float64    `json:"median_minutes"`
	LatestEligibleCompletion *time.Time `json:"latest_eligible_completion"`
}

// Quality counts the selected population and every excluded observation reason.
// Exclusions describe records present in the input only: skipped results are
// absent from the exporter, so unobserved skips are never inferred. An
// unusable-name exclusion counts executions that still normalized the medians but
// cannot be proposed, because no single source label identifies them.
type Quality struct {
	SelectedBuildCount  int            `json:"selected_build_count"`
	ProcessedBuildCount int            `json:"processed_build_count"`
	GreenBuildCount     int            `json:"green_build_count"`
	Exclusions          map[string]int `json:"exclusions"`
	Reason              string         `json:"reason,omitempty"`
}

// TestEnvironmentStats reports one spec/environment stratum.
type TestEnvironmentStats struct {
	Environment         string   `json:"environment"`
	EligibleBuildCount  int      `json:"eligible_build_count"`
	EligibleSampleCount int      `json:"eligible_sample_count"`
	MeanMinutes         float64  `json:"mean_minutes"`
	MedianMinutes       float64  `json:"median_minutes"`
	Ratio               *float64 `json:"ratio,omitempty"`
	Sufficient          bool     `json:"sufficient"`
}

// TestReport is one observed test ID and its proposal, if any.
type TestReport struct {
	TestID           string                 `json:"test_id"`
	Name             string                 `json:"name"`
	Status           string                 `json:"status"`
	ScoreMinutes     *float64               `json:"score_minutes"`
	ProposedPriority *int                   `json:"proposed_priority"`
	Environments     []TestEnvironmentStats `json:"environments"`
}

// PriorityReport is the complete machine-readable score review.
type PriorityReport struct {
	SchemaVersion       int                  `json:"schema_version"`
	Algorithm           string               `json:"algorithm"`
	Selection           Selection            `json:"selection"`
	MinSamples          int                  `json:"min_samples"`
	GlobalMedianMinutes *float64             `json:"global_median_minutes"`
	Environments        []EnvironmentSummary `json:"environments"`
	Quality             Quality              `json:"quality"`
	Tests               []TestReport         `json:"tests"`
}
