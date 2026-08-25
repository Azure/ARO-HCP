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

package cijoboutcomes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// sippyRun is one CI job run as Sippy reports it. Only the fields that reach
// Kusto are decoded.
type sippyRun struct {
	ProwID          string         `json:"prow_id"`
	Job             string         `json:"job"`
	URL             string         `json:"url"`
	TestFailures    int            `json:"test_failures"`
	FailedTestNames []string       `json:"failed_test_names"`
	Failed          bool           `json:"failed"`
	Succeeded       bool           `json:"succeeded"`
	OverallResult   string         `json:"overall_result"`
	Timestamp       sippyTimestamp `json:"timestamp"`
}

// sippyTimestamp decodes the time a run started.
//
// Sippy reports it as an RFC 3339 string, but has historically reported Unix
// milliseconds, and still does on some endpoints. Accept either so an upstream
// change cannot silently stop ingestion.
type sippyTimestamp struct {
	time.Time
}

func (t *sippyTimestamp) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "null" || trimmed == `""` {
		return nil
	}

	if strings.HasPrefix(trimmed, `"`) {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return fmt.Errorf("failed to decode timestamp %s: %w", trimmed, err)
		}
		parsed, err := time.Parse(time.RFC3339, text)
		if err != nil {
			return fmt.Errorf("failed to parse timestamp %q: %w", text, err)
		}
		t.Time = parsed.UTC()
		return nil
	}

	var millis int64
	if err := json.Unmarshal(data, &millis); err != nil {
		return fmt.Errorf("failed to decode timestamp %s: %w", trimmed, err)
	}
	if millis > 0 {
		t.Time = time.UnixMilli(millis).UTC()
	}
	return nil
}

type sippyResponse struct {
	Rows []sippyRun `json:"rows"`
}

// ciJobOutcome is one row of the ciJobOutcomes table. The JSON tags are the
// paths the table's ingestion mapping expects.
type ciJobOutcome struct {
	BuildID       string    `json:"buildId"`
	ClusterToken  string    `json:"clusterToken"`
	JobName       string    `json:"jobName"`
	ProwURL       string    `json:"prowUrl"`
	SippyRelease  string    `json:"sippyRelease"`
	Family        string    `json:"family"`
	OverallResult string    `json:"overallResult"`
	Failed        bool      `json:"failed"`
	TestFailures  int       `json:"testFailures"`
	FailedTests   []string  `json:"failedTests"`
	StartedAt     time.Time `json:"startedAt"`
}

// Sippy reports synthetic tests of its own, which describe Sippy rather than the
// run and are excluded everywhere else in the pipeline.
const sippySyntheticTestMarker = "[sig-sippy]"

// presubmitsRelease is the release covering pull-request runs. It is the only
// population that provisions its own clusters.
const presubmitsRelease = "Presubmits"

// clusterTokenFor derives the token shared by every cluster a CI run creates.
//
// A run's clusters are named "<deployEnv>-j<last 7 of BUILD_ID>-svc" and
// "-mgmt-N", so this token is what links a run to its alerts and logs.
//
// Only runs that provision their own clusters get one. Runs against a promoted
// environment use clusters that already exist, and their telemetry lives in that
// environment's own Kusto rather than here, so a token derived from their build
// id would join to nothing while looking like it should.
func clusterTokenFor(buildID, sippyRelease string) string {
	if sippyRelease != presubmitsRelease || len(buildID) < 7 {
		return ""
	}
	return "j" + buildID[len(buildID)-7:]
}

// familyFor reports which CI population a run belongs to.
//
// Batch and per-pull-request runs cannot be told apart by job name - they share
// one - so the URL is the only discriminator.
func familyFor(prowURL, jobName string) string {
	switch {
	case strings.Contains(prowURL, "/pr-logs/pull/batch/"):
		return "batch"
	case strings.Contains(prowURL, "/pr-logs/pull/"):
		return "presubmit"
	case strings.HasPrefix(jobName, "periodic-"):
		return "periodic"
	case strings.HasPrefix(jobName, "branch-"):
		return "gating"
	default:
		return "unknown"
	}
}

// outcomeFor converts a Sippy run into the row stored for it.
func outcomeFor(run sippyRun, sippyRelease string) ciJobOutcome {
	failedTests := make([]string, 0, len(run.FailedTestNames))
	for _, name := range run.FailedTestNames {
		if strings.Contains(name, sippySyntheticTestMarker) {
			continue
		}
		failedTests = append(failedTests, name)
	}

	return ciJobOutcome{
		BuildID:       run.ProwID,
		ClusterToken:  clusterTokenFor(run.ProwID, sippyRelease),
		JobName:       run.Job,
		ProwURL:       run.URL,
		SippyRelease:  sippyRelease,
		Family:        familyFor(run.URL, run.Job),
		OverallResult: run.OverallResult,
		Failed:        !run.Succeeded,
		TestFailures:  len(failedTests),
		FailedTests:   failedTests,
		StartedAt:     run.Timestamp.UTC(),
	}
}

// fetchRuns lists a release's e2e runs that started after the given time.
func fetchRuns(ctx context.Context, client *http.Client, endpoint, release, jobFilter string, since time.Time) ([]sippyRun, error) {
	filter, err := json.Marshal(map[string]any{
		"items": []map[string]string{
			{"columnField": "name", "operatorValue": "contains", "value": jobFilter},
			{"columnField": "timestamp", "operatorValue": ">", "value": since.UTC().Format(time.RFC3339)},
		},
		"linkOperator": "and",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to build filter: %w", err)
	}

	query := url.Values{}
	query.Set("release", release)
	query.Set("filter", string(filter))
	requestURL := strings.TrimSuffix(endpoint, "/") + "/api/jobs/runs?" + query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("failed to query Sippy: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sippy returned %s for release %q", response.Status, release)
	}

	var decoded sippyResponse
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("failed to decode Sippy response: %w", err)
	}
	return decoded.Rows, nil
}
