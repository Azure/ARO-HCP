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

package sippy

// RecentFailuresResponse is the Sippy API response for recent test failures.
type RecentFailuresResponse struct {
	Rows      []RecentFailure `json:"rows"`
	TotalRows int             `json:"total_rows"`
}

// RecentFailure represents a test that has been failing recently.
type RecentFailure struct {
	TestID       int             `json:"test_id"`
	TestName     string          `json:"test_name"`
	SuiteName    string          `json:"suite_name"`
	FailureCount int             `json:"failure_count"`
	FirstFailure string          `json:"first_failure"`
	LastFailure  string          `json:"last_failure"`
	LastPass     string          `json:"last_pass,omitempty"`
	Outputs      []FailureOutput `json:"outputs,omitempty"`
}

// FailureOutput is a single failure occurrence with its output text.
type FailureOutput struct {
	ProwJobRunID int64  `json:"prow_job_run_id"`
	ProwJobName  string `json:"prow_job_name"`
	ProwJobURL   string `json:"prow_job_url"`
	FailedAt     string `json:"failed_at"`
	Output       string `json:"output"`
}

// RunSummary is the response from /api/job/run/summary for a single job run.
type RunSummary struct {
	ID                    int64             `json:"id"`
	Name                  string            `json:"name"`
	Release               string            `json:"release"`
	Cluster               string            `json:"cluster"`
	URL                   string            `json:"url"`
	StartTime             string            `json:"startTime"`
	DurationSeconds       int               `json:"durationSeconds"`
	OverallResult         string            `json:"overallResult"`
	Reason                string            `json:"reason"`
	Succeeded             bool              `json:"succeeded"`
	Failed                bool              `json:"failed"`
	InfrastructureFailure bool              `json:"infrastructureFailure"`
	KnownFailure          bool              `json:"knownFailure"`
	TestCount             int               `json:"testCount"`
	TestFailureCount      int               `json:"testFailureCount"`
	TestFailures          map[string]string  `json:"testFailures,omitempty"` // test_name → failure output
	Variants              []string           `json:"variants,omitempty"`
}

// TestsResponse is the Sippy API response for test listings.
type TestsResponse []TestRecord

// TestRecord represents a single test's aggregate statistics.
type TestRecord struct {
	ID                      int     `json:"id"`
	Name                    string  `json:"name"`
	SuiteName               string  `json:"suite_name"`
	CurrentSuccesses        int     `json:"current_successes"`
	CurrentFailures         int     `json:"current_failures"`
	CurrentFlakes           int     `json:"current_flakes"`
	CurrentPassPercentage   float64 `json:"current_pass_percentage"`
	CurrentFlakePercentage  float64 `json:"current_flake_percentage"`
	CurrentRuns             int     `json:"current_runs"`
	PreviousPassPercentage  float64 `json:"previous_pass_percentage"`
	PreviousFlakePercentage float64 `json:"previous_flake_percentage"`
	PreviousRuns            int     `json:"previous_runs"`
	NetImprovement          float64 `json:"net_improvement"`
	OpenBugs                int     `json:"open_bugs"`
}

// TestOutput is a single failure output for a test from /api/tests/outputs.
type TestOutput struct {
	URL    string `json:"url"`
	Output string `json:"output"`
}

// HealthResponse is the response from /api/health.
type HealthResponse struct {
	Indicators  HealthIndicators  `json:"indicators"`
	LastUpdated string            `json:"last_updated"`
	CurrentStats  *HealthStatistics `json:"current_statistics,omitempty"`
	PreviousStats *HealthStatistics `json:"previous_statistics,omitempty"`
}

// HealthIndicators contains pass/fail indicators by category.
type HealthIndicators struct {
	Infrastructure *HealthIndicator `json:"infrastructure,omitempty"`
	Install        *HealthIndicator `json:"install,omitempty"`
	Tests          *HealthIndicator `json:"tests,omitempty"`
}

// HealthIndicator is a single health metric.
type HealthIndicator struct {
	CurrentPassPercentage  float64 `json:"current_pass_percentage"`
	CurrentRuns            int     `json:"current_runs"`
	CurrentFailures        int     `json:"current_failures"`
	PreviousPassPercentage float64 `json:"previous_pass_percentage"`
}

// HealthStatistics contains statistical distribution data.
type HealthStatistics struct {
	Mean              float64   `json:"mean"`
	StandardDeviation float64   `json:"standard_deviation"`
	P95               float64   `json:"p95"`
	Quartiles         []float64 `json:"quartiles,omitempty"`
	Histogram         []float64 `json:"histogram,omitempty"`
}

// ArtifactSearchResponse is the response from /api/jobs/artifacts.
type ArtifactSearchResponse struct {
	Matches []ArtifactMatch `json:"matches"`
}

// ArtifactMatch represents a single artifact file match.
type ArtifactMatch struct {
	ProwJobRunID int64    `json:"prow_job_run_id"`
	FilePath     string   `json:"file_path"`
	Lines        []string `json:"lines,omitempty"`
}
