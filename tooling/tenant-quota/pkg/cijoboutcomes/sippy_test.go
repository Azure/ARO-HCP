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
	"encoding/json"
	"testing"
	"time"
)

// Sippy reports the start time as an RFC 3339 string today, but has reported
// Unix milliseconds in the past. Decoding must survive either, because a silent
// decode failure stops ingestion for every run.
func TestSippyTimestampAcceptsBothFormats(t *testing.T) {
	want := time.Date(2026, 8, 25, 9, 47, 22, 0, time.UTC)

	for _, testCase := range []struct {
		name string
		data string
		want time.Time
	}{
		{name: "rfc3339 string", data: `"2026-08-25T09:47:22Z"`, want: want},
		{name: "unix milliseconds", data: "1787651242000", want: want},
		{name: "null is zero", data: "null", want: time.Time{}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var decoded sippyTimestamp
			if err := json.Unmarshal([]byte(testCase.data), &decoded); err != nil {
				t.Fatalf("failed to decode %s: %v", testCase.data, err)
			}
			if !decoded.UTC().Equal(testCase.want) {
				t.Errorf("decoded %s = %v, want %v", testCase.data, decoded.UTC(), testCase.want)
			}
		})
	}
}

func TestFamilyFor(t *testing.T) {
	const (
		base    = "https://prow.ci.openshift.org/view/gs/test-platform-results"
		jobName = "pull-ci-Azure-ARO-HCP-main-e2e-parallel"
	)

	for _, testCase := range []struct {
		name    string
		prowURL string
		jobName string
		want    string
	}{
		{
			// Batch and per-pull-request runs share a job name, so the URL is
			// the only thing that tells them apart.
			name:    "a batch run is recognised by its url",
			prowURL: base + "/pr-logs/pull/batch/" + jobName + "/2092003732647579648",
			jobName: jobName,
			want:    "batch",
		},
		{
			name:    "a pull request run is recognised by its url",
			prowURL: base + "/pr-logs/pull/Azure_ARO-HCP/6670/" + jobName + "/2092187087024427008",
			jobName: jobName,
			want:    "presubmit",
		},
		{
			name:    "a periodic run is recognised by its job name",
			prowURL: base + "/logs/periodic-ci-Azure-ARO-HCP-main-e2e-parallel/2092187087024427008",
			jobName: "periodic-ci-Azure-ARO-HCP-main-e2e-parallel",
			want:    "periodic",
		},
		{
			name:    "a gating run is recognised by its job name",
			prowURL: base + "/logs/branch-ci-Azure-ARO-HCP-main-e2e-parallel/2092187087024427008",
			jobName: "branch-ci-Azure-ARO-HCP-main-e2e-parallel",
			want:    "gating",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := familyFor(testCase.prowURL, testCase.jobName); got != testCase.want {
				t.Errorf("familyFor(...) = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestOutcomeForDropsSippysOwnTests(t *testing.T) {
	run := sippyRun{
		ProwID:    "2092003732647579648",
		Job:       "pull-ci-Azure-ARO-HCP-main-e2e-parallel",
		URL:       "https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/batch/pull-ci-Azure-ARO-HCP-main-e2e-parallel/2092003732647579648",
		Succeeded: false,
		FailedTestNames: []string{
			"[sig-sippy] infrastructure should work",
			"TestClusterCreate",
		},
		OverallResult: "F",
		Timestamp:     sippyTimestamp{Time: time.UnixMilli(1756000000000).UTC()},
	}

	outcome := outcomeFor(run, "Presubmits")

	// The count has to agree with the tests Sippy attributed to the run, minus
	// its own synthetic ones, not with Sippy's total.
	if outcome.TestFailures != 1 {
		t.Errorf("TestFailures = %d, want 1", outcome.TestFailures)
	}
	if !outcome.Failed {
		t.Error("Failed must be true for a run that did not succeed")
	}
	if outcome.Family != "batch" {
		t.Errorf("Family = %q, want %q", outcome.Family, "batch")
	}
	// Cluster names and the finish time come from the run's artifacts, so a run
	// built from Sippy alone must leave them unset rather than guess.
	if outcome.SvcCluster != "" || outcome.MgmtCluster != "" {
		t.Errorf("cluster names must come from artifacts, got svc=%q mgmt=%q", outcome.SvcCluster, outcome.MgmtCluster)
	}
	if !outcome.FinishedAt.IsZero() {
		t.Errorf("FinishedAt must come from artifacts, got %v", outcome.FinishedAt)
	}
}

// Sippy reports an aborted run as neither succeeded nor, necessarily, failed.
// Treating "not succeeded" as failed keeps the column usable as a filter.
func TestOutcomeForTreatsAbortedRunsAsFailed(t *testing.T) {
	outcome := outcomeFor(sippyRun{
		ProwID:        "2092003732647579648",
		Succeeded:     false,
		Failed:        false,
		OverallResult: "A",
	}, "Presubmits")

	if !outcome.Failed {
		t.Error("Failed must be true for an aborted run")
	}
	if outcome.OverallResult != "A" {
		t.Errorf("OverallResult = %q, want the value Sippy reported", outcome.OverallResult)
	}
}
