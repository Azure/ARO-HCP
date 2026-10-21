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

package prowjob

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"

	prowjobs "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowgangway "sigs.k8s.io/prow/pkg/gangway"
)

// JobFailedError indicates a Prow job completed in the "failure" state - as
// opposed to "error" or "aborted", which usually indicate an infra problem or
// a cancellation rather than a test failure. Timeouts are reported separately
// as plain errors, not as JobFailedError. Only jobs that failed this way are
// candidates for the EV2 auto-retry, since that's the class of failure the
// allow-retry test label and finished.json retry metadata are about (see
// AROSLSRE-1721).
type JobFailedError struct {
	ProwExecutionID string
	JobURL          string
}

func (e *JobFailedError) Error() string {
	return fmt.Sprintf("job %s failed - check the Prow UI for detailed logs: %s", e.ProwExecutionID, e.JobURL)
}

// EV2RetryableMarker is the fixed, matchable substring prow-job-executor emits when a
// gating job failure is eligible for an automatic EV2 retry (see ev2RetryEligible). The
// EV2 gating step's pipeline.yaml configures automatedRetry.errorContainsAny with this
// marker, so EV2 - not prow-job-executor - re-runs the whole step from scratch when it
// sees the marker in the step's captured error output. prow-job-executor never resubmits
// the job itself; it only decides whether the failure qualifies and surfaces that
// decision through the error text.
const EV2RetryableMarker = "ev2-retryable-known-issue-failure"

// EV2RetryableError wraps a job failure that finished.json's metadata marks as safe to
// auto-retry. Its Error() text always contains EV2RetryableMarker, so EV2's
// automatedRetry.errorContainsAny can match on it and re-run the gating step.
type EV2RetryableError struct {
	Cause error
}

func (e *EV2RetryableError) Error() string {
	return fmt.Sprintf("%s: %s", EV2RetryableMarker, e.Cause.Error())
}

func (e *EV2RetryableError) Unwrap() error {
	return e.Cause
}

// EV2InfraRetryableMarker is the fixed, matchable substring prow-job-executor emits when a
// gating job failed in a shared multi-stage pre-step (e.g. aro-hcp-lease-acquire exhausting
// the e2e slot pool) before the aro-hcp-tests binary ever ran, and so is eligible for the
// same bounded, single automatic EV2 retry as a KnownIssueEligible failure - see
// InfraPreconditionEligible. Kept distinct from EV2RetryableMarker (rather than reusing it)
// so the two retry causes stay independently observable in the step's captured output.
const EV2InfraRetryableMarker = "ev2-retryable-infra-precondition-failure"

// EV2InfraRetryableError wraps a job failure where no step's finished.json ever reported
// aro-hcp-tests results at all, meaning the failure happened before any test could run.
// Its Error() text always contains EV2InfraRetryableMarker, so EV2's
// automatedRetry.errorContainsAny can match on it and re-run the gating step.
type EV2InfraRetryableError struct {
	Cause error
}

func (e *EV2InfraRetryableError) Error() string {
	return fmt.Sprintf("%s: %s", EV2InfraRetryableMarker, e.Cause.Error())
}

func (e *EV2InfraRetryableError) Unwrap() error {
	return e.Cause
}

// Monitor handles job execution and monitoring
type Monitor struct {
	client               *Client
	pollInterval         time.Duration
	timeout              time.Duration
	notFoundGracePeriod  time.Duration
	dryRun               bool
	gatePromotion        bool
	allowEV2Retry        bool
	maxAutoRetryFailures int

	// checkRetryMarker fetches finished.json for a job's status URL and reports whether,
	// and why, the run is safe to auto-retry. Defaults to jobAllowsEV2Retry; overridable in
	// tests.
	checkRetryMarker func(ctx context.Context, jobURL string, maxAutoRetryFailures int) (RetryEligibility, error)
}

// DefaultNotFoundGracePeriod bounds how long waitForCompletion tolerates a consecutive run
// of 404s from GetJobStatus before giving up - long enough to ride out a short-lived Prow/
// OCP hiccup (e.g. an oauth-proxy restart or a brief maintenance window), but short enough
// that a job that's genuinely gone for good doesn't burn the whole --timeout budget. 30
// minutes is several multiples of the 300s default --poll-interval, so a real outage gets
// more than one chance to recover before this gives up.
const DefaultNotFoundGracePeriod = 30 * time.Minute

// NewMonitor creates a new job monitor with the specified polling interval and timeout.
// allowEV2Retry opts into checking a failed job's finished.json metadata and, if it marks
// the failure as safe to retry (see AROSLSRE-1721), failing with EV2RetryableError instead
// of a plain JobFailedError - so the EV2 gating step's automatedRetry re-runs the whole
// step, rather than prow-job-executor resubmitting the job itself. It has no effect unless
// gatePromotion is also true. maxAutoRetryFailures caps how many failed tests a run may
// have (all labeled allow-retry) and still qualify; pass DefaultMaxEV2AutoRetryFailures
// unless a caller wants to tune it without an ARO-HCP rebuild. notFoundGracePeriod bounds
// how long a consecutive run of 404s from GetJobStatus is tolerated before the monitor
// gives up on the job (see DefaultNotFoundGracePeriod); pass that constant unless a caller
// wants to tune it.
func NewMonitor(client *Client, pollInterval, timeout, notFoundGracePeriod time.Duration, dryRun, gatePromotion, allowEV2Retry bool, maxAutoRetryFailures int) *Monitor {
	return &Monitor{
		client:               client,
		pollInterval:         pollInterval,
		timeout:              timeout,
		notFoundGracePeriod:  notFoundGracePeriod,
		dryRun:               dryRun,
		gatePromotion:        gatePromotion,
		allowEV2Retry:        allowEV2Retry,
		maxAutoRetryFailures: maxAutoRetryFailures,
		checkRetryMarker:     jobAllowsEV2Retry,
	}
}

// JobOutcome carries the result of waiting for a Prow job, with retry eligibility as an
// explicit field rather than something callers have to infer by type-asserting Err. Only a
// job that completed in the "failure" state (as opposed to "error"/"aborted", which usually
// indicate an infra problem or a cancellation) is ever Retryable; ExecuteAndWait uses this
// field directly instead of errors.As-ing for *JobFailedError.
type JobOutcome struct {
	// Err is nil on success, otherwise the reason WaitForCompletion stopped waiting.
	Err error
	// Retryable is true only when Err is non-nil and came from the job finishing in the
	// "failure" state - the class of failure the EV2 auto-retry (AROSLSRE-1721) applies to.
	Retryable bool
	// JobURL is the Prow status page URL. It's only populated when Err is non-nil and
	// gating was requested (FailureState/ErrorState/AbortedState with m.gatePromotion set,
	// or a timeout after a status was observed) - it's empty on success and on the
	// non-gating "unexpected status" paths, since there's no failure to report there.
	JobURL string
}

// WaitForCompletion polls job status until completion.
func (m *Monitor) WaitForCompletion(ctx context.Context, logger logr.Logger, prowExecutionID string) error {
	return m.waitForCompletion(ctx, logger, prowExecutionID).Err
}

// waitForCompletion is the shared polling implementation. It returns a JobOutcome so callers
// that need to distinguish a retry-eligible job failure (ExecuteAndWait) can check
// outcome.Retryable directly, without control flow via typed-error inspection.
func (m *Monitor) waitForCompletion(ctx context.Context, logger logr.Logger, prowExecutionID string) JobOutcome {
	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()

	// Create ticker for polling interval
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	// notFoundSince tracks how long the current consecutive run of 404s from
	// GetJobStatus has lasted (zero means "not currently 404ing"). It's reset on any
	// successful status fetch, so a job that goes 404 -> recovers -> 404 again (e.g.
	// across two separate Prow/OCP hiccups) gets a fresh grace period each time, rather
	// than accumulating toward a single budget across unrelated outages.
	var notFoundSince time.Time

	// Check status immediately, then poll at intervals
	for {
		job, err := m.client.GetJobStatus(ctx, prowExecutionID)
		if err != nil {
			if IsNotFoundError(err) {
				if notFoundSince.IsZero() {
					notFoundSince = time.Now()
				}
				// A 404 that survived GetJobStatus's own propagation-delay retries could
				// still be a transient Prow/OCP hiccup (e.g. an oauth-proxy restart or a
				// brief maintenance window), so give it up to notFoundGracePeriod to
				// recover before concluding the job's status page will never appear
				// (e.g. it was garbage-collected) and giving up.
				if elapsed := time.Since(notFoundSince); elapsed >= m.notFoundGracePeriod {
					return JobOutcome{Err: fmt.Errorf("job %s not found for over %v: %w", prowExecutionID, m.notFoundGracePeriod, err)}
				}
				logger.Error(err, "Job status endpoint returned 404, still within grace period, will continue polling", "notFoundFor", time.Since(notFoundSince))
			} else {
				logger.Error(err, "Failed to get job status after retries, will continue polling")
			}
		} else {
			notFoundSince = time.Time{}
			status := string(job.Status.State)
			logger = logger.WithValues(
				"prowExecutionID", prowExecutionID,
				"status", status,
				"jobName", job.Spec.Job,
				"prowUrl", job.Status.URL,
			)
			logger.Info("Job status update")

			switch status {
			case string(prowjobs.SuccessState):
				logger.Info("Job completed successfully")
				return JobOutcome{}
			case string(prowjobs.FailureState):
				if m.gatePromotion {
					return JobOutcome{
						Err:       &JobFailedError{ProwExecutionID: prowExecutionID, JobURL: job.Status.URL},
						Retryable: true,
						JobURL:    job.Status.URL,
					}
				} else {
					logger.Info("Unexpected job state, but gating is not requested.")
					return JobOutcome{}
				}
			case string(prowjobs.ErrorState):
				if m.gatePromotion {
					return JobOutcome{
						Err:    fmt.Errorf("job %s encountered an error - check Prow status page and job logs for details: %s", prowExecutionID, job.Status.URL),
						JobURL: job.Status.URL,
					}
				} else {
					logger.Info("Unexpected job state, but gating is not requested.")
					return JobOutcome{}
				}
			case string(prowjobs.AbortedState):
				if m.gatePromotion {
					return JobOutcome{
						Err:    fmt.Errorf("job %s was aborted - this may be due to timeout or manual cancellation", prowExecutionID),
						JobURL: job.Status.URL,
					}
				} else {
					logger.Info("Unexpected job state, but gating is not requested.")
					return JobOutcome{}
				}
			}
		}

		select {
		case <-ctx.Done():
			if job != nil {
				return JobOutcome{
					Err:    fmt.Errorf("job monitoring timed out after %v - job %s may still be running, check Prow UI: %s", m.timeout, prowExecutionID, job.Status.URL),
					JobURL: job.Status.URL,
				}
			}
			return JobOutcome{Err: fmt.Errorf("job monitoring timed out after %v - job %s may still be running (unable to retrieve job status)", m.timeout, prowExecutionID)}
		case <-ticker.C:
			// Continue to next iteration
		}
	}
}

// ExecuteAndWait submits a job once and waits for completion. It never resubmits the job
// itself. If the job's JobOutcome comes back Retryable (see JobOutcome) and this Monitor has
// allowEV2Retry set, it fetches the failed job's finished.json and, depending on what it
// finds, returns a distinct retryable error instead of the plain job-failure error:
// EV2RetryableError when only known-issue tests failed (AROSLSRE-1721), or
// EV2InfraRetryableError when no step ever ran the aro-hcp-tests suite at all (a pre-test
// infra failure, e.g. lease-acquire capacity exhaustion). The EV2 gating step's
// pipeline.yaml matches both markers via automatedRetry.errorContainsAny and re-runs the
// whole step from scratch - prow-job-executor only decides eligibility, EV2 owns the
// actual retry.
func (m *Monitor) ExecuteAndWait(ctx context.Context, logger logr.Logger, request *prowgangway.CreateJobExecutionRequest) error {
	// WaitForCompletion applies m.timeout itself; don't apply it again here too, since a
	// child context can't outlive its parent - doing so would start the whole monitoring
	// window early, before the job is even submitted.

	// Submit job
	logger.Info("Submitting Prow job", "jobName", request.JobName)
	if m.dryRun {
		logger.Info("Dry-run is set, exiting.")
		return nil
	}
	prowExecutionID, err := m.client.SubmitJob(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to submit job: %w", err)
	}

	logger.Info("Job submitted successfully", "prowExecutionID", prowExecutionID, "jobName", request.JobName)

	outcome := m.waitForCompletion(ctx, logger, prowExecutionID)
	if outcome.Err == nil || !m.allowEV2Retry || !outcome.Retryable {
		return outcome.Err
	}

	eligibility, checkErr := m.checkRetryMarker(ctx, outcome.JobURL, m.maxAutoRetryFailures)
	if checkErr != nil {
		logger.Error(checkErr, "Failed to inspect finished.json for the EV2 retry signal, failing normally", "prowExecutionID", prowExecutionID)
		return outcome.Err
	}

	switch eligibility {
	case KnownIssueEligible:
		logger.Info("finished.json marks the run as safe to retry, failing with the EV2-retryable marker so the gating step's automatedRetry re-runs it", "prowExecutionID", prowExecutionID, "jobURL", outcome.JobURL)
		return &EV2RetryableError{Cause: outcome.Err}
	case InfraPreconditionEligible:
		logger.Info("no step ever ran the aro-hcp-tests suite - treating this as a pre-test infra failure safe to retry once, failing with the EV2 infra-retryable marker", "prowExecutionID", prowExecutionID, "jobURL", outcome.JobURL)
		return &EV2InfraRetryableError{Cause: outcome.Err}
	default:
		return outcome.Err
	}
}
