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

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	// DefaultEndpoint is the Sippy API endpoint.
	DefaultEndpoint = "https://sippy.dptools.openshift.org"
	// MaxLookbackDays is the maximum lookback Sippy supports (90 days).
	MaxLookbackDays = 90
	jobRunsPath     = "api/jobs/runs"
)

// SippyEnvs maps our short env names to Sippy release names.
var SippyEnvs = map[string]string{
	"int":  "aro-integration",
	"stg":  "aro-stage",
	"prod": "aro-production",
}

// Client queries the Sippy API for CI job run data.
type Client struct {
	endpoint   string
	httpClient *http.Client
}

// NewClient creates a Sippy client with sensible defaults.
func NewClient() *Client {
	return &Client{
		endpoint:   DefaultEndpoint,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// JobRun represents a single Prow job run from Sippy.
type JobRun struct {
	ID              int64    `json:"id"`
	Name            string   `json:"job"`
	URL             string   `json:"url"`
	TimestampMs     int64    `json:"timestamp"`
	TestFailures    int      `json:"test_failures"`
	OverallResult   string   `json:"overall_result"` // "S" = success, "F" = failure
	FailedTestNames []string `json:"failed_test_names"`

	// Classification fields
	InfrastructureFailure bool `json:"infrastructure_failure"`
	KnownFailure          bool `json:"known_failure"`

	// Flake data
	TestFlakes      int      `json:"test_flakes"`
	FlakedTestNames []string `json:"flaked_test_names"`

	// Build metadata
	Cluster     string            `json:"cluster"`
	Annotations map[string]string `json:"annotations"`

	// PR fields (populated for presubmit runs)
	PullRequestOrg    string `json:"pull_request_org"`
	PullRequestRepo   string `json:"pull_request_repo"`
	PullRequestLink   string `json:"pull_request_link"`
	PullRequestSHA    string `json:"pull_request_sha"`
	PullRequestAuthor string `json:"pull_request_author"`
}

// Timestamp returns the run timestamp as an RFC3339 string.
func (r JobRun) Timestamp() string {
	if r.TimestampMs == 0 {
		return ""
	}
	return time.Unix(r.TimestampMs/1000, (r.TimestampMs%1000)*1e6).UTC().Format(time.RFC3339)
}

// Succeeded returns true if the job passed.
func (r JobRun) Succeeded() bool {
	return r.OverallResult == "S"
}

// Failed returns true if the job failed.
func (r JobRun) Failed() bool {
	return r.OverallResult == "F"
}

// RolloutInfo contains EV2 deployment metadata extracted from job annotations.
type RolloutInfo struct {
	Commit    string `json:"commit"`              // ev2.rollout/ARO-HCP
	Build     string `json:"build"`               // ev2.rollout/build
	Region    string `json:"region"`              // ev2.rollout/region
	Pipelines string `json:"pipelines,omitempty"` // ev2.rollout/sdp-pipelines
}

// Rollout extracts EV2 deployment info from the job's annotations.
// Returns nil if no rollout annotations are present.
func (r JobRun) Rollout() *RolloutInfo {
	if r.Annotations == nil {
		return nil
	}
	commit := r.Annotations["ev2.rollout/ARO-HCP"]
	build := r.Annotations["ev2.rollout/build"]
	if commit == "" && build == "" {
		return nil
	}
	return &RolloutInfo{
		Commit:    commit,
		Build:     build,
		Region:    r.Annotations["ev2.rollout/region"],
		Pipelines: r.Annotations["ev2.rollout/sdp-pipelines"],
	}
}

// IsPresubmit returns true if this is a presubmit (PR) job run.
func (r JobRun) IsPresubmit() bool {
	return r.PullRequestOrg != ""
}

// JobRunsResponse is the Sippy API response for job runs.
type JobRunsResponse struct {
	Rows      []JobRun `json:"rows"`
	TotalRows int      `json:"total_rows"`
}

// Filter is a Sippy query filter.
type Filter struct {
	Items        []FilterItem `json:"items"`
	LinkOperator string       `json:"linkOperator,omitempty"`
}

// FilterItem is a single filter clause.
type FilterItem struct {
	ColumnField   string `json:"columnField"`
	OperatorValue string `json:"operatorValue"`
	Value         string `json:"value"`
}

// ListJobRuns fetches job runs for an environment within the given lookback duration.
func (c *Client) ListJobRuns(ctx context.Context, env string, since time.Duration) (*JobRunsResponse, error) {
	sippyRelease, ok := SippyEnvs[env]
	if !ok {
		return nil, fmt.Errorf("no Sippy environment for %q", env)
	}

	cutoff := time.Now().Add(-since)
	filter := Filter{
		Items: []FilterItem{
			{ColumnField: "timestamp", OperatorValue: ">", Value: fmt.Sprintf("%d", cutoff.UnixMilli())},
			{ColumnField: "job", OperatorValue: "contains", Value: "e2e-parallel"},
		},
		LinkOperator: "and",
	}

	filterJSON, err := json.Marshal(filter)
	if err != nil {
		return nil, fmt.Errorf("marshal filter: %w", err)
	}

	reqURL, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}
	reqURL.Path = jobRunsPath

	q := reqURL.Query()
	q.Set("release", sippyRelease)
	q.Set("filter", string(filterJSON))
	reqURL.RawQuery = q.Encode()

	var result JobRunsResponse
	if err := c.doGet(ctx, reqURL.String(), &result); err != nil {
		return nil, err
	}

	// Sort by timestamp descending (newest first)
	sort.Slice(result.Rows, func(i, j int) bool {
		return result.Rows[i].TimestampMs > result.Rows[j].TimestampMs
	})

	return &result, nil
}

// GetRecentFailures returns tests that failed recently. When previousPeriod is non-empty,
// only returns NEW failures (tests that started failing in the current period but were
// not failing in the previous period). This provides onset detection in a single call.
// When includeOutputs is true, failure messages are included.
func (c *Client) GetRecentFailures(ctx context.Context, env string, period string, previousPeriod string, includeOutputs bool) (*RecentFailuresResponse, error) {
	sippyRelease, ok := SippyEnvs[env]
	if !ok {
		return nil, fmt.Errorf("no Sippy environment for %q", env)
	}

	reqURL, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}
	reqURL.Path = "api/tests/recent_failures"

	q := reqURL.Query()
	q.Set("release", sippyRelease)
	q.Set("period", period)
	if previousPeriod != "" {
		q.Set("previousPeriod", previousPeriod)
	}
	if includeOutputs {
		q.Set("includeOutputs", "true")
	}
	reqURL.RawQuery = q.Encode()

	var result RecentFailuresResponse
	if err := c.doGet(ctx, reqURL.String(), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetRunSummary returns detailed information for a single job run,
// including test failure messages as a map of test_name → failure output.
func (c *Client) GetRunSummary(ctx context.Context, prowJobRunID int64) (*RunSummary, error) {
	reqURL, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}
	reqURL.Path = "api/job/run/summary"

	q := reqURL.Query()
	q.Set("prow_job_run_id", fmt.Sprintf("%d", prowJobRunID))
	reqURL.RawQuery = q.Encode()

	var result RunSummary
	if err := c.doGet(ctx, reqURL.String(), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetTests returns aggregate test statistics for an environment.
func (c *Client) GetTests(ctx context.Context, env string) (TestsResponse, error) {
	sippyRelease, ok := SippyEnvs[env]
	if !ok {
		return nil, fmt.Errorf("no Sippy environment for %q", env)
	}

	reqURL, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}
	reqURL.Path = "api/tests"

	q := reqURL.Query()
	q.Set("release", sippyRelease)
	reqURL.RawQuery = q.Encode()

	var result TestsResponse
	if err := c.doGet(ctx, reqURL.String(), &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetTestOutputs returns the most recent failure outputs for a specific test.
func (c *Client) GetTestOutputs(ctx context.Context, env string, testName string) ([]TestOutput, error) {
	sippyRelease, ok := SippyEnvs[env]
	if !ok {
		return nil, fmt.Errorf("no Sippy environment for %q", env)
	}

	reqURL, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}
	reqURL.Path = "api/tests/outputs"

	q := reqURL.Query()
	q.Set("release", sippyRelease)
	q.Set("test", testName)
	reqURL.RawQuery = q.Encode()

	var result []TestOutput
	if err := c.doGet(ctx, reqURL.String(), &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetHealth returns statistical health information for an environment.
func (c *Client) GetHealth(ctx context.Context, env string) (*HealthResponse, error) {
	sippyRelease, ok := SippyEnvs[env]
	if !ok {
		return nil, fmt.Errorf("no Sippy environment for %q", env)
	}

	reqURL, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}
	reqURL.Path = "api/health"

	q := reqURL.Query()
	q.Set("release", sippyRelease)
	reqURL.RawQuery = q.Encode()

	var result HealthResponse
	if err := c.doGet(ctx, reqURL.String(), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SearchArtifacts searches through GCS artifacts for matching content.
func (c *Client) SearchArtifacts(ctx context.Context, runIDs []int64, pathGlob string, textContains string) (*ArtifactSearchResponse, error) {
	reqURL, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}
	reqURL.Path = "api/jobs/artifacts"

	// Build comma-separated run IDs
	ids := make([]string, len(runIDs))
	for i, id := range runIDs {
		ids[i] = fmt.Sprintf("%d", id)
	}

	q := reqURL.Query()
	q.Set("prowJobRuns", strings.Join(ids, ","))
	q.Set("pathGlob", pathGlob)
	if textContains != "" {
		q.Set("textContains", textContains)
	}
	reqURL.RawQuery = q.Encode()

	var result ArtifactSearchResponse
	if err := c.doGet(ctx, reqURL.String(), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// doGet performs an HTTP GET request, parses the JSON response, and handles errors.
func (c *Client) doGet(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sippy request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("sippy returned %d: %s", resp.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	return nil
}

// EnvNames returns the environment names that have Sippy data.
func EnvNames() []string {
	return []string{"int", "prod", "stg"}
}

// HasEnv returns true if the environment has Sippy data.
func HasEnv(env string) bool {
	_, ok := SippyEnvs[env]
	return ok
}
