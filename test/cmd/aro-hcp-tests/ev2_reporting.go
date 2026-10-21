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

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

// ev2FailedTestsKey and ev2AllowRetryTestsKey are the finished.json metadata keys
// prow-job-executor reads (see AROSLSRE-1721) to decide whether an EV2 gating job failure
// is safe to auto-retry. They are written into $ARTIFACT_DIR/metadata.json, which Prow's
// sidecar merges verbatim into the job's finished.json under the top-level "metadata"
// object - the standard Prow custom-metadata mechanism (see sigs.k8s.io/prow/pkg/sidecar),
// rather than a log line prow-job-executor would otherwise have to scrape out of the build
// log.
//
// aro-hcp-tests only reports raw facts here - which specs failed, and which of those carry
// labels.AllowRetry - it does not decide whether a run qualifies for auto-retry. That
// decision (how many failures are tolerable, etc.) is policy that belongs to
// prow-job-executor, which can evolve independently of an ARO-HCP release. See
// retrymarker.go in ARO-Tools for the eligibility logic that consumes these keys.
//
// ev2SuiteSummaryKey is a third, purely informational key reported alongside the two
// above. It isn't used by the EV2 retry decision, but gives anyone looking at a gating
// run's finished.json (a human triaging a failure, or a future dashboard) the basic shape
// of the run - how many specs ran, how many of each result, and how long the suite took
// wall-clock - without having to open the Prow job UI. It's a nested object rather than
// more flat ev2-* keys so its field names (total/passed/failed/skipped/duration-seconds)
// don't read as confusingly similar to the neighboring ev2-failed-tests list (a name list,
// not a count).
const (
	ev2FailedTestsKey     = "ev2-failed-tests"
	ev2AllowRetryTestsKey = "ev2-allow-retry-tests"
	ev2SuiteSummaryKey    = "ev2-suite-summary"
)

// ev2RetryMetadataFile is where Prow's sidecar picks up per-step custom metadata to merge
// into finished.json. See sigs.k8s.io/prow/pkg/pod-utils/decorate.metadataFile.
const ev2RetryMetadataFile = "metadata.json"

// registerEV2RetryCatcher watches test results as they complete and, once the full suite has
// finished, always writes ev2FailedTestsKey, ev2AllowRetryTestsKey, and ev2SuiteSummaryKey
// into $ARTIFACT_DIR/metadata.json - even when nothing failed, in which case both failure
// lists are empty. Writing unconditionally means the keys' presence tells prow-job-executor
// the run reached this point at all, so an absent key is unambiguously "this step didn't
// run", never "the run failed but wasn't retry-eligible". prow-job-executor reads the
// failure lists back out of the job's finished.json to decide whether to resubmit the job
// once, instead of requiring a human to notice the failure, review Prow output, and
// manually retrigger. ev2SuiteSummaryKey isn't used by that decision, but saves a trip to
// the Prow job UI for anyone triaging a run straight from finished.json. See AROSLSRE-1721.
//
// This must only run in the long-lived parent run-suite process. openshift-tests-extension
// spawns each spec as a separate "run-test" worker subprocess, and that subprocess calls
// specs.Run() itself with just its own single spec - which would re-trigger AddAfterEach/
// AddAfterAll in the worker too, reporting one spec's result as if it were the whole
// suite's, and racing multiple workers writing the same file. Guard registration with
// isRunSuiteProcess(), the same pattern used for the upgrade coordinator's AddBeforeAll
// hook above.
func registerEV2RetryCatcher(specs et.ExtensionTestSpecs) {
	if !isRunSuiteProcess() {
		return
	}

	allowRetryNames := map[string]bool{}
	for _, spec := range specs {
		if spec.Labels.Has(labels.AllowRetry[0]) {
			allowRetryNames[spec.Name] = true
		}
	}

	var mu sync.Mutex
	var failedNames []string
	var allowRetryFailedNames []string
	var passedCount, failedCount, skippedCount int
	var suiteStart time.Time

	specs.AddBeforeAll(func() {
		mu.Lock()
		defer mu.Unlock()
		suiteStart = time.Now()
	})

	specs.AddAfterEach(func(res *et.ExtensionTestResult) {
		mu.Lock()
		defer mu.Unlock()
		switch res.Result {
		case et.ResultPassed:
			passedCount++
		case et.ResultSkipped:
			skippedCount++
		case et.ResultFailed:
			failedCount++
			failedNames = append(failedNames, res.Name)
			if allowRetryNames[res.Name] {
				allowRetryFailedNames = append(allowRetryFailedNames, res.Name)
			}
		}
	})

	specs.AddAfterAll(func() {
		mu.Lock()
		defer mu.Unlock()
		// Specs run in parallel, so failedNames/allowRetryFailedNames are accumulated in
		// nondeterministic completion order. Sort before logging/writing metadata.json so
		// the emitted metadata is stable between runs of the same failure set.
		sort.Strings(failedNames)
		sort.Strings(allowRetryFailedNames)
		var durationSeconds float64
		if !suiteStart.IsZero() {
			durationSeconds = roundToDecisecond(time.Since(suiteStart))
		}
		summary := ev2SuiteSummary{
			Total:           passedCount + failedCount + skippedCount,
			Passed:          passedCount,
			Failed:          failedCount,
			Skipped:         skippedCount,
			DurationSeconds: durationSeconds,
		}
		artifactDir := os.Getenv("ARTIFACT_DIR")
		verb, destination := "writing", filepath.Join(artifactDir, ev2RetryMetadataFile)
		if artifactDir == "" {
			verb, destination = "skipping", ev2RetryMetadataFile+" (ARTIFACT_DIR not set)"
		}
		fmt.Fprintf(os.Stderr, "%d test(s) failed (%d labeled %q) out of %d total, suite took %.1fs: %s %s/%s/%s to %s: failed=%v allow-retry=%v\n",
			summary.Failed, len(allowRetryFailedNames), labels.AllowRetry[0], summary.Total, summary.DurationSeconds,
			verb, ev2FailedTestsKey, ev2AllowRetryTestsKey, ev2SuiteSummaryKey, destination, failedNames, allowRetryFailedNames)
		if err := writeEV2RetryMetadata(artifactDir, failedNames, allowRetryFailedNames, summary); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: failed to write EV2 retry metadata: %v\n", err)
		}
	})
}

// ev2SuiteSummary is the basic shape of a suite run, reported under ev2SuiteSummaryKey
// alongside the retry facts so a human (or a future dashboard) can see run size/duration
// without opening the Prow job UI. Field names are deliberately short (no repeated ev2-/
// tests- prefix) since they're already scoped under the ev2-suite-summary object.
type ev2SuiteSummary struct {
	Total           int     `json:"total"`
	Passed          int     `json:"passed"`
	Failed          int     `json:"failed"`
	Skipped         int     `json:"skipped"`
	DurationSeconds float64 `json:"duration-seconds"`
}

// roundToDecisecond rounds d to one decimal place of a second. A suite runs for minutes to
// hours, so the sub-millisecond precision time.Duration.Seconds() returns is meaningless
// noise in the reported metadata.
func roundToDecisecond(d time.Duration) float64 {
	return d.Round(100 * time.Millisecond).Seconds()
}

// writeEV2RetryMetadata merges ev2FailedTestsKey and ev2AllowRetryTestsKey (always present,
// even as empty lists), plus the ev2SuiteSummary, into $ARTIFACT_DIR/metadata.json, creating
// the file if it doesn't exist yet and preserving any keys another step may have already
// written there. artifactDir empty (not running under Prow, e.g. a local run) is not an
// error - it just means there's nowhere to write the signal, so we skip it.
func writeEV2RetryMetadata(artifactDir string, failedNames, allowRetryFailedNames []string, summary ev2SuiteSummary) error {
	if artifactDir == "" {
		fmt.Fprintln(os.Stderr, "WARNING: ARTIFACT_DIR is not set, skipping EV2 retry metadata")
		return nil
	}

	path := filepath.Join(artifactDir, ev2RetryMetadataFile)

	metadata := map[string]interface{}{}
	if existing, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(existing, &metadata); err != nil {
			return fmt.Errorf("failed to parse existing %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to read existing %s: %w", path, err)
	}

	// Always write both failure-list keys, even when empty, so their presence is unambiguous:
	// prow-job-executor can tell "the suite ran and nothing/little failed" apart from "this
	// step never ran".
	metadata[ev2FailedTestsKey] = orEmptySlice(failedNames)
	metadata[ev2AllowRetryTestsKey] = orEmptySlice(allowRetryFailedNames)
	metadata[ev2SuiteSummaryKey] = summary

	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal %s: %w", path, err)
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", artifactDir, err)
	}
	// Write atomically: this file is an external signal consumed by prow-job-executor for
	// EV2 gating, so a crash or interruption mid-write must never leave a truncated or
	// invalid metadata.json behind for it to read.
	tmp, err := os.CreateTemp(artifactDir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file for %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if n, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write temp file for %s: %w", path, err)
	} else if n != len(data) {
		tmp.Close()
		return fmt.Errorf("short write to temp file for %s: wrote %d of %d bytes", path, n, len(data))
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp file for %s: %w", path, err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("failed to chmod temp file for %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("failed to rename temp file to %s: %w", path, err)
	}
	return nil
}

// orEmptySlice returns a non-nil empty slice in place of nil, so json.Marshal emits "[]"
// instead of "null" for metadata consumers that expect a list.
func orEmptySlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
