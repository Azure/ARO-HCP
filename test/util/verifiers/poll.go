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

package verifiers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
)

const (
	// DefaultPollInterval is the standard interval between verifier poll attempts in E2E tests.
	DefaultPollInterval = 15 * time.Second
	// DefaultDiagnoseTimeout is the timeout for collecting failure diagnostics after a poll times out.
	DefaultDiagnoseTimeout = 30 * time.Second
	// finalCheckTimeout bounds the one last check made after the poll deadline expires. It is
	// therefore also the amount by which reaching a verdict may overshoot the advertised timeout,
	// so it is kept well below any caller's budget rather than reusing DefaultDiagnoseTimeout.
	finalCheckTimeout = 5 * time.Second
)

type diagnoseFunc func(ctx context.Context, restConfig *rest.Config) string

type pollObservationError struct {
	observation  error
	interruption error
}

func (failure *pollObservationError) Error() string {
	return failure.observation.Error()
}

func (failure *pollObservationError) Unwrap() []error {
	return []error{failure.observation, failure.interruption}
}

// logVerifierTiming records actual wall-clock duration when a verifier finishes polling.
// Logs to GinkgoLogr (structured) and GinkgoWriter (visible in default CI output).
func logVerifierTiming(name, outcome string, elapsed time.Duration) {
	rounded := elapsed.Round(time.Millisecond)
	ginkgo.GinkgoLogr.Info("Verifier "+outcome,
		"name", name,
		"elapsed", rounded.String(),
	)
	ginkgo.GinkgoWriter.Printf("[%s] %s after %s\n", name, outcome, rounded)
}

// gatherDiagnostics collects the failure dump detached from the caller's context: the verdict is
// already in, and the diagnostics are the reason the failure is attributable at all, so a caller
// deadline that has just expired must not take them away. diagnoseTimeout bounds this extra
// wall clock.
func gatherDiagnostics(name string, restConfig *rest.Config, diagnoseTimeout time.Duration, diagnose diagnoseFunc) string {
	if diagnose == nil {
		return ""
	}
	diagCtx, cancel := context.WithTimeout(context.Background(), diagnoseTimeout)
	defer cancel()
	details := diagnose(diagCtx, restConfig)
	if details != "" {
		ginkgo.GinkgoLogr.Info("Failure diagnostics", "name", name, "details", details)
	}
	return details
}

// withDiagnostics appends a gathered dump to a verdict, leaving the verdict untouched when there
// is nothing to append.
func withDiagnostics(verdict error, details string) error {
	if details == "" {
		return verdict
	}
	return fmt.Errorf("%w\n%s", verdict, details)
}

// pollUntilReady polls check until it succeeds or timeout expires. Failure details are logged
// only when the error message changes between polls (delta-only logging). Actual elapsed
// wall-clock time is logged on both success and failure. If diagnose is set, its output is
// logged and appended to every failure the poll returns: a timeout, or cancellation of the
// caller's context. If the polling context ends, a check error wrapping that context's error
// does not replace or re-log an earlier failure's message. Both observed errors remain
// available for unwrapping.
//
// Budget contract, in two parts.
//
// Reaching a verdict takes at most timeout plus finalCheckTimeout: the polling loop stops at
// timeout, but one further check is then made so that a condition met between the last poll and
// the deadline is not misreported as a timeout. That overshoot is bounded, paid only on the path
// that was already going to fail, and included in the elapsed duration logged and reported in the
// returned error. The further check is skipped when the last observed verdict was the poll
// context itself ending; waiting once more there yields no new evidence, so the preserved
// observation is reported directly.
//
// Reporting the failure can take a further diagnoseTimeout on top, on the failure paths only
// (timeout or cancellation) and only when diagnose is set. The verdict has already been reached by
// the time diagnostics are gathered, so the reported elapsed deliberately excludes them: it
// measures how long the condition was waited on, not how long the report took to assemble. A
// caller that must bound its total wall clock has to allow for both parts.
func pollUntilReady(
	ctx context.Context,
	name string,
	timeout, interval time.Duration,
	restConfig *rest.Config,
	diagnoseTimeout time.Duration,
	diagnose diagnoseFunc,
	check func(context.Context) error,
) error {
	if timeout <= 0 {
		return fmt.Errorf("%s: timeout must be > 0, got %s", name, timeout)
	}

	logger := ginkgo.GinkgoLogr
	var previousError string
	var lastErr error
	startTime := time.Now()

	logger.Info("Verifier polling", "name", name, "timeout", timeout.String(), "interval", interval.String())
	ginkgo.GinkgoWriter.Printf("[%s] polling (timeout=%s, interval=%s)\n", name, timeout, interval)

	err := wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		err := check(ctx)
		if err == nil {
			return true, nil
		}
		if lastErr != nil && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			lastErr = &pollObservationError{observation: lastErr, interruption: err}
			return false, nil
		}
		currentError := err.Error()
		if currentError != previousError {
			logger.Info("Verifier check", "name", name, "status", "failed", "error", currentError)
			previousError = currentError
		}
		lastErr = err
		return false, nil
	})

	elapsed := time.Since(startTime)

	// A suite-level timeout cutting off a long upgrade poll is a realistic CI failure and the moment
	// the dump matters most, so cancellation reports diagnostics like a timeout does. lastErr
	// carries the last known evidence of what the check saw; fall back to the context error when
	// the poll was interrupted before the check ever got to observe anything.
	reportCancelled := func(lastKnown error) error {
		logVerifierTiming(name, "cancelled", elapsed)
		details := gatherDiagnostics(name, restConfig, diagnoseTimeout, diagnose)
		observed := lastKnown
		if observed == nil {
			observed = ctx.Err()
		}
		return withDiagnostics(fmt.Errorf("%s cancelled after %s: %w", name, elapsed.Round(time.Millisecond), observed), details)
	}

	if err == nil {
		logVerifierTiming(name, "succeeded", elapsed)
		return nil
	}

	if ctx.Err() != nil {
		return reportCancelled(lastErr)
	}

	// The loop stops looking at the deadline, so a condition that became true between the last
	// check and the deadline reads as a timeout even though it was met within the budget. Look
	// once more before failing. It costs at most finalCheckTimeout of wall clock, only on the
	// path that was going to fail anyway, and it is the difference between a real timeout and a
	// near-miss: ARO-26775 failed on a control plane upgrade that landed 22s inside a 45m
	// budget, missed because the preceding poll had fired 2m earlier.
	//
	// Skip that retry when the last verdict was the poll context ending: the loop records an
	// interruption as a pollObservationError, and a check that already had the full remaining
	// budget and reported a context error cannot become a condition verdict after one more
	// finalCheckTimeout of waiting. Re-running it would only bury the earlier observation
	// underneath a fresh context error, so the preserved observation is reported as the timeout.
	var interruption *pollObservationError
	if lastErr == nil || !errors.As(lastErr, &interruption) {
		finalCtx, cancelFinal := context.WithTimeout(ctx, finalCheckTimeout)
		finalErr := check(finalCtx)
		cancelFinal()
		lastErr = finalErr

		// Recompute rather than reusing the pre-check value: the final check runs after the
		// deadline, so reporting the loop's elapsed time would understate how long the verifier
		// actually took.
		elapsed = time.Since(startTime)

		if finalErr == nil {
			logVerifierTiming(name, "succeeded on the final check after the deadline", elapsed)
			return nil
		}
	}

	// The caller's context can expire while the final check runs. Re-check it here so an
	// interruption inside the final-check window is reported as a cancellation, not recast as a
	// timeout -- or replaced by whatever error a check that ignores the child context produced.
	if ctx.Err() != nil {
		return reportCancelled(lastErr)
	}

	logVerifierTiming(name, "timed out", elapsed)
	details := gatherDiagnostics(name, restConfig, diagnoseTimeout, diagnose)
	if lastErr != nil {
		return withDiagnostics(fmt.Errorf("%s timed out after %s: %w", name, elapsed.Round(time.Millisecond), lastErr), details)
	}
	return withDiagnostics(fmt.Errorf("%s timed out after %s: %w", name, elapsed.Round(time.Millisecond), err), details)
}
