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
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

// Sippy is a discovery source, not a complete one: it reports that a run
// happened and whether it passed, but not the clusters it provisioned, when it
// finished, or how long each of its tests took. All of that is in the run's own
// Prow artifacts, so the rows are completed from there.
//
// The artifact readers are hcpctl's - see tooling/hcpctl/pkg/snapshot - because
// they already encode where each file lives, how the artifact directory is
// named per job, and how test timing metadata is stitched onto test results.
// Re-deriving that here would leave two copies to drift apart.

// finishedJSONURL is the run's completion record, written by Prow itself rather
// than by a test step. It is fetched directly because it needs none of the
// artifact-directory discovery the other files do.
const finishedJSONURL = "https://storage.googleapis.com/test-platform-results/%s/finished.json"

// prowFinished is the subset of finished.json that reaches Kusto.
type prowFinished struct {
	Timestamp int64  `json:"timestamp"`
	Result    string `json:"result"`
}

// runDetail is what a run's artifacts add to what Sippy already reported.
type runDetail struct {
	SvcCluster  string
	MgmtCluster string
	FinishedAt  time.Time
	Tests       []ciTestResult
	Names       []ciTestName
}

// fetchRunDetail reads a single run's artifacts.
//
// Every field is optional and the returned detail carries whatever was
// readable, even when the error is non-nil. Artifacts are written by steps that
// can be skipped, time out, or be cut short when a run is aborted, so a run
// whose test results are missing may still have yielded its cluster names.
// Discarding those because a later read failed would lose data the caller can
// still store.
func fetchRunDetail(ctx context.Context, client *http.Client, prowURL string) (runDetail, error) {
	var detail runDetail

	info, err := snapshot.ParseProwURL(prowURL)
	if err != nil {
		return detail, fmt.Errorf("failed to parse Prow URL %q: %w", prowURL, err)
	}

	var failures []error

	// Only runs that provision their own clusters have a config.yaml artifact.
	// The rest resolve their configuration from the sdp-pipelines repository,
	// which this collector has no checkout of - and their telemetry lives in
	// their own environment's Kusto, so cluster names would join to nothing
	// here anyway.
	if info.IsPullRequest() {
		jobConfig, err := snapshot.FetchProwJobConfig(ctx, info, "")
		switch {
		case err != nil:
			failures = append(failures, fmt.Errorf("failed to read cluster names: %w", err))
		default:
			detail.SvcCluster = jobConfig.ServiceClusterName
			detail.MgmtCluster = jobConfig.ManagementClusterName
		}
	}

	finishedAt, err := fetchFinishedAt(ctx, client, info.GCSPrefix)
	if err != nil {
		failures = append(failures, err)
	} else {
		detail.FinishedAt = finishedAt
	}

	results, err := snapshot.FetchProwJobTestResults(ctx, info)
	if err != nil {
		failures = append(failures, fmt.Errorf("failed to read test results: %w", err))
	} else {
		detail.Tests, detail.Names = testRowsFor(info.ProwID, results)
	}

	return detail, errors.Join(failures...)
}

// fetchFinishedAt reads when the run completed.
func fetchFinishedAt(ctx context.Context, client *http.Client, gcsPrefix string) (time.Time, error) {
	return fetchFinishedAtFrom(ctx, client, fmt.Sprintf(finishedJSONURL, gcsPrefix))
}

func fetchFinishedAtFrom(ctx context.Context, client *http.Client, url string) (time.Time, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to build request for %s: %w", url, err)
	}

	response, err := client.Do(request)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to fetch %s: %w", url, err)
	}
	defer func() { _ = response.Body.Close() }()

	// A run still in progress has no finished.json. That is not an error: the
	// next pass will find it once the run completes.
	if response.StatusCode == http.StatusNotFound {
		return time.Time{}, nil
	}
	if response.StatusCode != http.StatusOK {
		return time.Time{}, fmt.Errorf("unexpected status %d fetching %s", response.StatusCode, url)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to read %s: %w", url, err)
	}

	var finished prowFinished
	if err := json.Unmarshal(body, &finished); err != nil {
		return time.Time{}, fmt.Errorf("failed to parse %s: %w", url, err)
	}
	if finished.Timestamp <= 0 {
		return time.Time{}, nil
	}
	return time.Unix(finished.Timestamp, 0).UTC(), nil
}

// testRowsFor converts a run's test results into join rows and the distinct
// names they reference.
//
// Only tests that ran are kept. A suite skips a large share of its specs
// depending on what the run exercises, and a skipped test has no timings and no
// outcome to attribute, so recording them would roughly double the table to say
// nothing. Sippy's own synthetic tests are dropped for the same reason they are
// dropped from the failure counts: they describe Sippy, not the run.
func testRowsFor(buildID string, results []snapshot.TestResult) ([]ciTestResult, []ciTestName) {
	var rows []ciTestResult
	var names []ciTestName
	seen := make(map[string]struct{}, len(results))

	for _, result := range results {
		if strings.Contains(result.Name, sippySyntheticTestMarker) {
			continue
		}
		if !executed(result) {
			continue
		}

		testID := testIDFor(result.Name)
		if _, duplicate := seen[testID]; !duplicate {
			seen[testID] = struct{}{}
			names = append(names, ciTestName{TestID: testID, Name: result.Name})
		}

		rows = append(rows, ciTestResult{
			BuildID:          buildID,
			TestID:           testID,
			Result:           resultOf(result),
			Failed:           result.Failed,
			ResourceGroup:    result.ResourceGroup,
			StartedAt:        result.StartTime.UTC(),
			FinishedAt:       result.EndTime.UTC(),
			SetupFinishTime:  result.SetupFinishTime.UTC(),
			TestStartTime:    result.TestStartTime.UTC(),
			CleanupStartTime: result.CleanupStartTime.UTC(),
		})
	}
	return rows, names
}

// executed reports whether a test ran, as opposed to being skipped.
//
// A skipped test is stamped with start and end times just as a passing one is,
// so the timestamps cannot separate them - only the verdict can.
func executed(result snapshot.TestResult) bool {
	return result.Result != skippedResult
}

// skippedResult is the verdict the test extension reports for a test it did not
// run.
const skippedResult = "skipped"

// resultOf reports the verdict for a test, falling back to the Failed flag for
// results that carry none.
func resultOf(result snapshot.TestResult) string {
	if result.Result != "" {
		return result.Result
	}
	if result.Failed {
		return "failed"
	}
	return "passed"
}
