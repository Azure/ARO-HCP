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
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
)

// The JSON tags on these types are the paths the tables' ingestion mappings
// expect. Changing one without changing the mapping silently drops a column.

// ciJobOutcome is one row of the ciJobOutcomes table: a single CI job run.
type ciJobOutcome struct {
	BuildID       string    `json:"buildId"`
	SvcCluster    string    `json:"svcCluster"`
	MgmtCluster   string    `json:"mgmtCluster"`
	JobName       string    `json:"jobName"`
	ProwURL       string    `json:"prowUrl"`
	SippyRelease  string    `json:"sippyRelease"`
	Family        string    `json:"family"`
	OverallResult string    `json:"overallResult"`
	Failed        bool      `json:"failed"`
	TestFailures  int       `json:"testFailures"`
	StartedAt     time.Time `json:"startedAt"`
	FinishedAt    time.Time `json:"finishedAt"`
}

// ciTestName is one row of the ciTestNames table: a distinct test.
type ciTestName struct {
	TestID string `json:"testId"`
	Name   string `json:"name"`
}

// ciTestResult is one row of the ciTestResults table: one test within one run.
type ciTestResult struct {
	BuildID          string    `json:"buildId"`
	TestID           string    `json:"testId"`
	Result           string    `json:"result"`
	Failed           bool      `json:"failed"`
	ResourceGroup    string    `json:"resourceGroup"`
	StartedAt        time.Time `json:"startedAt"`
	FinishedAt       time.Time `json:"finishedAt"`
	SetupFinishTime  time.Time `json:"setupFinishTime"`
	TestStartTime    time.Time `json:"testStartTime"`
	CleanupStartTime time.Time `json:"cleanupStartTime"`
}

// Sippy reports synthetic tests of its own, which describe Sippy rather than the
// run and are excluded everywhere else in the pipeline.
const sippySyntheticTestMarker = "[sig-sippy]"

// testIDFor returns the stable id for a test name.
//
// Kusto has no sequence and no upsert, and a queued row stays invisible to
// queries for several minutes, so an id allocated by reading the current
// maximum would collide with one allocated moments earlier. Hashing the name
// removes the need to allocate at all: every writer derives the same id for the
// same test, on every pass and after every restart, so re-ingesting a name
// already stored is a duplicate to be filtered rather than a conflict.
//
// The name is hashed whole. Truncating the digest would trade collision margin
// for a shorter key that is no cheaper to store, since ids are strings here to
// match every other id column in this database.
func testIDFor(name string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(name)))
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

// outcomeFor converts a Sippy run into the run row stored for it.
//
// Sippy is the discovery source and knows only what it collects itself: the
// cluster names and finish times are filled in later from the run's artifacts,
// and stay empty when those are unavailable.
func outcomeFor(run sippyRun, sippyRelease string) ciJobOutcome {
	failures := 0
	for _, name := range run.FailedTestNames {
		if strings.Contains(name, sippySyntheticTestMarker) {
			continue
		}
		failures++
	}

	return ciJobOutcome{
		BuildID:       run.ProwID,
		JobName:       run.Job,
		ProwURL:       run.URL,
		SippyRelease:  sippyRelease,
		Family:        familyFor(run.URL, run.Job),
		OverallResult: run.OverallResult,
		Failed:        !run.Succeeded,
		TestFailures:  failures,
		StartedAt:     run.Timestamp.UTC(),
	}
}
