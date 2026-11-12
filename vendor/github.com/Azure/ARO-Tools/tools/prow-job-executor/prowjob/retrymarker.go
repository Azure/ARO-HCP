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

package prowjob

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-logr/logr"
)

// ev2FailedTestsKey and ev2AllowRetryTestsKey are the finished.json metadata keys that the
// ARO-HCP E2E test binary (aro-hcp-tests, see AROSLSRE-1721) always writes once a run
// finishes: the full list of failed spec names, and the subset of those that were labeled
// allow-retry (a known, tracked issue with a fix already committed to). aro-hcp-tests
// writes both keys into $ARTIFACT_DIR/metadata.json, which Prow's sidecar merges into
// that specific step's own finished.json under the top-level "metadata" object - the
// standard Prow custom-metadata mechanism, so no log scraping is involved. For ARO-HCP's
// multi-stage e2e jobs that step-level finished.json lives nested under the build's
// artifacts/ tree (<build>/artifacts/<workflow>/<step>/finished.json), not at the
// job-level <build>/finished.json - see jobAllowsEV2Retry below for how we locate it.
//
// aro-hcp-tests only reports these raw facts, even when nothing failed (both lists empty);
// it is prow-job-executor's job (ev2RetryEligible below) to decide whether the shape of a
// failure is narrow enough to safely auto-retry. Keeping the policy here, rather than baked
// into an ARO-HCP release, means the retry threshold can be tuned without an ARO-HCP
// rebuild, and an absent key unambiguously means "this step never ran" rather than
// "the run failed but wasn't retry-eligible".
const (
	ev2FailedTestsKey     = "ev2-failed-tests"
	ev2AllowRetryTestsKey = "ev2-allow-retry-tests"
)

// DefaultMaxEV2AutoRetryFailures caps how many failed tests a run may have and still
// qualify for an automatic EV2 gating retry. If more tests than this fail, or any failure
// isn't labeled allow-retry, we stay silent and let the gate fail normally for a human to
// triage. Overridable via Monitor's maxAutoRetryFailures field (see the
// --max-ev2-auto-retry-failures flag).
const DefaultMaxEV2AutoRetryFailures = 2

// maxFinishedJSONBytes bounds how much of finished.json we'll read. The file
// is a small, flat JSON document; anything near this size indicates something
// unexpected and we'd rather fail closed than buffer an unbounded response.
const maxFinishedJSONBytes = 1 << 20 // 1 MiB

// finishedJSONFetchTimeout bounds a single finished.json fetch, independent of
// the overall job-monitoring timeout.
const finishedJSONFetchTimeout = 30 * time.Second

// finishedJSON is the subset of Prow's finished.json (produced by the sidecar
// utility, see sigs.k8s.io/prow/pkg/sidecar and the testgrid metadata.Finished
// type) that we need: the free-form metadata object merged in from each
// step's $ARTIFACT_DIR/metadata.json.
type finishedJSON struct {
	Metadata map[string]interface{} `json:"metadata"`
}

// gcsObjectBaseURL and gcsJSONAPIBaseURL are the public GCS endpoints this file talks to.
// They're package-level vars (rather than inlined literals) so tests can point them at a
// local httptest server instead of stubbing an HTTP client.
var (
	gcsObjectBaseURL  = "https://storage.googleapis.com"
	gcsJSONAPIBaseURL = "https://storage.googleapis.com/storage/v1/b"
)

// finishedJSONURLFromViewURL converts a Prow Deck "view" URL (the one
// reported in ProwJob.Status.URL, e.g.
// https://prow.ci.openshift.org/view/gs/origin-ci-test/logs/<job>/<build>)
// into finished.json's public GCS URL, e.g.
// https://storage.googleapis.com/origin-ci-test/logs/<job>/<build>/finished.json.
func finishedJSONURLFromViewURL(viewURL string) (string, error) {
	if viewURL == "" {
		return "", fmt.Errorf("job has no status URL, cannot locate its finished.json")
	}

	u, err := url.Parse(viewURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse job status URL %q: %w", viewURL, err)
	}

	const viewPrefix = "/view/gs/"
	if !strings.HasPrefix(u.Path, viewPrefix) {
		return "", fmt.Errorf("job status URL %q does not look like a GCS Prow view URL (missing %q prefix)", viewURL, viewPrefix)
	}

	gcsPath := strings.Trim(strings.TrimPrefix(u.Path, viewPrefix), "/")
	if gcsPath == "" {
		return "", fmt.Errorf("job status URL %q has an empty GCS path after the %q prefix", viewURL, viewPrefix)
	}
	return fmt.Sprintf("%s/%s/finished.json", gcsObjectBaseURL, gcsPath), nil
}

// RetryEligibility classifies why (if at all) a failed gating job qualifies for an
// automatic EV2 retry. jobAllowsEV2Retry is the only producer of this type; ExecuteAndWait
// switches on it to decide which retryable error (if any) to fail with.
type RetryEligibility int

const (
	// NotEligible means a step's finished.json did carry ev2FailedTestsKey (so
	// aro-hcp-tests ran and reported results), but the reported failures aren't eligible
	// per ev2RetryEligible (too many, or one wasn't labeled allow-retry) - or an error
	// occurred while checking. This is distinct from InfraPreconditionEligible, which
	// covers the case where no step ever reported results at all.
	NotEligible RetryEligibility = iota
	// KnownIssueEligible means the aro-hcp-tests step ran and reported its results, and
	// every failed spec was labeled allow-retry and within the configured cap (see
	// ev2RetryEligible and AROSLSRE-1721).
	KnownIssueEligible
	// InfraPreconditionEligible means no step's finished.json carried ev2FailedTestsKey at
	// all, anywhere under the build - the aro-hcp-tests step never got far enough to write
	// its metadata. This covers failures in shared multi-stage pre-steps (e.g.
	// aro-hcp-lease-acquire exhausting the e2e slot pool) that happen before any test runs
	// and so can't be attributed to a per-test allow-retry label the way KnownIssueEligible
	// failures are.
	InfraPreconditionEligible
)

// String renders a RetryEligibility as a stable, readable name (rather than a bare
// integer) for log messages and test failure output.
func (r RetryEligibility) String() string {
	switch r {
	case NotEligible:
		return "NotEligible"
	case KnownIssueEligible:
		return "KnownIssueEligible"
	case InfraPreconditionEligible:
		return "InfraPreconditionEligible"
	default:
		return fmt.Sprintf("RetryEligibility(%d)", int(r))
	}
}

// retryEligibilityFrom translates the known-issue eligibility already decided by the caller
// (ev2RetryEligibleFromFinishedJSON, only once it has confirmed the candidate finished.json
// carried ev2FailedTestsKey) into a RetryEligibility.
func retryEligibilityFrom(eligible bool) RetryEligibility {
	if eligible {
		return KnownIssueEligible
	}
	return NotEligible
}

// jobAllowsEV2Retry fetches finished.json for the job reported at viewURL and reports
// whether it qualifies for an automatic EV2 gating retry, and why.
//
// ARO-HCP e2e jobs are multi-stage ci-operator tests (lease-acquire, write-config, the
// actual test container, gather-*, lease-release, ...). Prow's sidecar merges each step's
// own $ARTIFACT_DIR/metadata.json into THAT STEP's OWN finished.json
// (<build>/artifacts/<workflow>/<step>/finished.json) - never into the job-level
// finished.json (<build>/finished.json) that viewURL itself resolves to, whose "metadata"
// object is ci-operator's own job bookkeeping (pod, revision, repo, ...) and never carries
// per-step custom keys. Checking only the job-level finished.json therefore always found
// ev2FailedTestsKey absent on every real multi-stage job and silently reported "not
// eligible" with no error - verified empirically against real ARO-HCP prod/stage/PR e2e
// jobs (AROSLSRE-1721 postmortem). We check the job-level finished.json first - cheap, and
// sufficient on its own for any job shape where ci-operator does aggregate it there - and
// only pay for listing the build's artifacts/ tree when that candidate doesn't carry
// ev2FailedTestsKey at all.
//
// If, after checking every candidate, none of them ever carried ev2FailedTestsKey, that
// means the aro-hcp-tests step itself never ran (or crashed before writing its metadata) -
// this reports InfraPreconditionEligible rather than NotEligible, since a pre-test infra
// failure (like lease-acquire's e2e slot pool being exhausted, AROSLSRE-1938) is a distinct,
// still-safe-to-retry-once class of failure from an ordinary per-test one.
func jobAllowsEV2Retry(ctx context.Context, viewURL string, maxAutoRetryFailures int) (RetryEligibility, error) {
	jobFinishedURL, err := finishedJSONURLFromViewURL(viewURL)
	if err != nil {
		return NotEligible, err
	}

	if eligible, done, err := ev2RetryEligibleFromFinishedJSON(ctx, jobFinishedURL, maxAutoRetryFailures); done {
		if err != nil {
			return NotEligible, err
		}
		return retryEligibilityFrom(eligible), nil
	}

	bucket, buildPath, err := gcsBucketAndBuildPath(jobFinishedURL)
	if err != nil {
		return NotEligible, err
	}
	stepURLs, err := listStepFinishedJSONURLs(ctx, bucket, buildPath)
	if err != nil {
		return NotEligible, err
	}
	for _, rawURL := range stepURLs {
		if eligible, done, err := ev2RetryEligibleFromFinishedJSON(ctx, rawURL, maxAutoRetryFailures); done {
			if err != nil {
				return NotEligible, err
			}
			return retryEligibilityFrom(eligible), nil
		}
	}
	// No candidate finished.json carried ev2FailedTestsKey at all - the aro-hcp-tests step
	// never got far enough to report its results. Treat this as an infra precondition
	// failure, not "nothing to retry".
	return InfraPreconditionEligible, nil
}

// ev2RetryEligibleFromFinishedJSON fetches rawURL and, only if its metadata carries
// ev2FailedTestsKey, decides EV2 retry eligibility for it. done is true whenever the
// caller should stop trying further candidate URLs: either this one had a definitive
// answer (found the key, or hit a real error), or false when rawURL had nothing to say
// (404, or metadata present but missing ev2FailedTestsKey) and the caller should move on
// to the next candidate.
func ev2RetryEligibleFromFinishedJSON(ctx context.Context, rawURL string, maxAutoRetryFailures int) (eligible, done bool, err error) {
	metadata, found, err := fetchFinishedJSONMetadata(ctx, rawURL)
	if err != nil {
		return false, true, err
	}
	if !found {
		return false, false, nil // 404: this candidate doesn't exist for this job shape.
	}
	if _, present := metadata[ev2FailedTestsKey]; !present {
		return false, false, nil // not the step that ran aro-hcp-tests.
	}

	failed, ok := stringSliceFromMetadata(metadata, ev2FailedTestsKey)
	if !ok {
		return false, true, fmt.Errorf("finished.json %q metadata key %q is present but not a list of strings", rawURL, ev2FailedTestsKey)
	}
	allowRetry, ok := stringSliceFromMetadata(metadata, ev2AllowRetryTestsKey)
	if !ok {
		return false, true, fmt.Errorf("finished.json %q metadata key %q is present but not a list of strings", rawURL, ev2AllowRetryTestsKey)
	}
	return ev2RetryEligible(failed, allowRetry, maxAutoRetryFailures), true, nil
}

// gcsBucketAndBuildPath splits a storage.googleapis.com finished.json URL (as produced by
// finishedJSONURLFromViewURL) back into its bucket and build-directory object path, so
// listStepFinishedJSONURLs can enumerate that build's artifacts/ tree.
func gcsBucketAndBuildPath(finishedURL string) (bucket, buildPath string, err error) {
	prefix := gcsObjectBaseURL + "/"
	if !strings.HasPrefix(finishedURL, prefix) {
		return "", "", fmt.Errorf("finished.json URL %q is not a %s URL", finishedURL, gcsObjectBaseURL)
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(finishedURL, prefix), "/finished.json")
	bucket, buildPath, ok := strings.Cut(rest, "/")
	if !ok || bucket == "" || buildPath == "" {
		return "", "", fmt.Errorf("finished.json URL %q does not contain both a bucket and a build path", finishedURL)
	}
	return bucket, buildPath, nil
}

// gcsListTimeout bounds each GCS directory-listing call used to discover per-step
// finished.json files.
const gcsListTimeout = 15 * time.Second

// gcsListMaxPrefixes caps how many directory entries we'll walk at each artifacts/ tree
// level. ARO-HCP e2e jobs have a handful of multi-stage steps (lease-acquire,
// write-config, the test container, a couple of gather-*, lease-release); anything near
// this cap means the tree looks nothing like what we expect, or the listing was
// paginated, so we fail closed rather than silently missing a remainder page.
const gcsListMaxPrefixes = 100

// gcsListResponse is the subset of the GCS JSON API's objects.list response
// (https://cloud.google.com/storage/docs/json_api/v1/objects/list) we need: the
// "directory" entries found at the requested prefix+delimiter.
type gcsListResponse struct {
	Prefixes      []string `json:"prefixes"`
	NextPageToken string   `json:"nextPageToken"`
}

// listGCSPrefixes lists the immediate "subdirectories" under prefix in bucket, using
// GCS's public, unauthenticated JSON API with delimiter=/ - the same trick gsutil/gcsweb
// use to browse a GCS "directory" without listing every object beneath it recursively.
func listGCSPrefixes(ctx context.Context, bucket, prefix string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, gcsListTimeout)
	defer cancel()

	listURL := fmt.Sprintf("%s/%s/o?prefix=%s&delimiter=%s&fields=%s",
		gcsJSONAPIBaseURL, url.QueryEscape(bucket), url.QueryEscape(prefix), url.QueryEscape("/"), url.QueryEscape("prefixes,nextPageToken"))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS list request for %q: %w", prefix, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to list GCS prefix %q: %w", prefix, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			logr.FromContextOrDiscard(ctx).Error(cerr, "failed to close body")
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list GCS prefix %q: unexpected status %d", prefix, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFinishedJSONBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read GCS list response for %q: %w", prefix, err)
	}
	if len(body) > maxFinishedJSONBytes {
		return nil, fmt.Errorf("GCS list response for %q exceeds %d byte limit", prefix, maxFinishedJSONBytes)
	}

	var listResp gcsListResponse
	if err := json.Unmarshal(body, &listResp); err != nil {
		return nil, fmt.Errorf("failed to decode GCS list response for %q: %w", prefix, err)
	}
	if listResp.NextPageToken != "" || len(listResp.Prefixes) > gcsListMaxPrefixes {
		return nil, fmt.Errorf("GCS prefix %q has more than %d entries or is paginated - refusing to guess which step ran the tests", prefix, gcsListMaxPrefixes)
	}
	return listResp.Prefixes, nil
}

// listStepFinishedJSONURLs finds every per-step finished.json nested under a build's
// artifacts/ directory, by walking the two directory levels ci-operator always creates
// there (the workflow name, then one directory per step) - without assuming any
// particular step name, since that varies by job (e.g. aro-hcp-test-persistent for
// postsubmits, aro-hcp-test-local for presubmits).
func listStepFinishedJSONURLs(ctx context.Context, bucket, buildPath string) ([]string, error) {
	workflowPrefixes, err := listGCSPrefixes(ctx, bucket, buildPath+"/artifacts/")
	if err != nil {
		return nil, err
	}

	var stepURLs []string
	for _, workflowPrefix := range workflowPrefixes {
		stepPrefixes, err := listGCSPrefixes(ctx, bucket, workflowPrefix)
		if err != nil {
			return nil, err
		}
		for _, stepPrefix := range stepPrefixes {
			stepURLs = append(stepURLs, fmt.Sprintf("%s/%s/%sfinished.json", gcsObjectBaseURL, bucket, stepPrefix))
		}
	}
	return stepURLs, nil
}

// fetchFinishedJSONMetadata downloads rawURL as a finished.json document and returns its
// free-form "metadata" object. found is false (with a nil error) only when rawURL doesn't
// exist (404) - callers walking multiple candidate step URLs should treat that as "this
// step doesn't exist for this job shape" and move on, rather than as an error.
func fetchFinishedJSONMetadata(ctx context.Context, rawURL string) (metadata map[string]interface{}, found bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, finishedJSONFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, false, fmt.Errorf("failed to create finished.json request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("failed to fetch finished.json %q: %w", rawURL, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			logr.FromContextOrDiscard(ctx).Error(cerr, "failed to close body")
		}
	}()

	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("failed to fetch finished.json %q: unexpected status %d", rawURL, resp.StatusCode)
	}

	// Read one byte past the cap so we can distinguish "fits within the cap" from
	// "was truncated" - io.LimitReader alone would silently accept an oversized body as
	// long as valid JSON appears before the limit, which defeats the fail-closed intent.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFinishedJSONBytes+1))
	if err != nil {
		return nil, false, fmt.Errorf("failed to read finished.json %q: %w", rawURL, err)
	}
	if len(body) > maxFinishedJSONBytes {
		return nil, false, fmt.Errorf("finished.json %q exceeds %d byte limit", rawURL, maxFinishedJSONBytes)
	}

	var finished finishedJSON
	if err := json.Unmarshal(body, &finished); err != nil {
		return nil, false, fmt.Errorf("failed to decode finished.json %q: %w", rawURL, err)
	}
	return finished.Metadata, true, nil
}

// fetchFinishedJSONAllowsRetry downloads rawURL as a finished.json document and reports
// whether its metadata qualifies for an automatic EV2 gating retry. This is the
// single-known-URL building block underneath fetchFinishedJSONMetadata; jobAllowsEV2Retry
// itself walks multiple candidate URLs (see above) rather than trusting a single one.
func fetchFinishedJSONAllowsRetry(ctx context.Context, rawURL string, maxAutoRetryFailures int) (bool, error) {
	metadata, found, err := fetchFinishedJSONMetadata(ctx, rawURL)
	if err != nil {
		return false, err
	}
	if !found {
		return false, fmt.Errorf("failed to fetch finished.json %q: unexpected status %d", rawURL, http.StatusNotFound)
	}

	failed, ok := stringSliceFromMetadata(metadata, ev2FailedTestsKey)
	if !ok {
		return false, fmt.Errorf("finished.json %q metadata key %q is present but not a list of strings", rawURL, ev2FailedTestsKey)
	}
	allowRetry, ok := stringSliceFromMetadata(metadata, ev2AllowRetryTestsKey)
	if !ok {
		return false, fmt.Errorf("finished.json %q metadata key %q is present but not a list of strings", rawURL, ev2AllowRetryTestsKey)
	}
	return ev2RetryEligible(failed, allowRetry, maxAutoRetryFailures), nil
}

// stringSliceFromMetadata extracts a []string out of finished.json's loosely-typed
// metadata map for the given key. A missing key returns (nil, true) - callers treat an
// absent list the same as an empty one. A key that's present but not a list of strings
// returns (nil, false) so the caller can fail closed instead of silently dropping the
// non-string elements and understating the failure count.
func stringSliceFromMetadata(metadata map[string]interface{}, key string) ([]string, bool) {
	rawVal, present := metadata[key]
	if !present {
		return nil, true
	}
	raw, ok := rawVal.([]interface{})
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// ev2RetryEligible decides whether a finished run qualifies for an automatic EV2 gating
// retry, given the raw facts aro-hcp-tests reported: every test name that failed, and the
// subset of those that were labeled allow-retry (a known, tracked issue). This is the
// whole eligibility policy, kept separate from the HTTP fetch/parse logic above so it can
// be tested directly.
//
// A run qualifies only when it failed at all, no more than maxAutoRetryFailures tests
// failed, and every failure was labeled allow-retry. Any other shape - too many failures,
// or even a single failure not carrying the label - disqualifies the whole run, so the
// gate fails normally and a human triages it.
func ev2RetryEligible(failed, allowRetry []string, maxAutoRetryFailures int) bool {
	if len(failed) == 0 || len(failed) > maxAutoRetryFailures {
		return false
	}
	allowRetrySet := make(map[string]bool, len(allowRetry))
	for _, name := range allowRetry {
		allowRetrySet[name] = true
	}
	for _, name := range failed {
		if !allowRetrySet[name] {
			return false
		}
	}
	return true
}
