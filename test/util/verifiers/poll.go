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
	// finalCheckTimeout bounds the one check made after the poll deadline, so it is also the most
	// the verify can overshoot its advertised timeout. Kept small rather than reusing
	// DefaultDiagnoseTimeout.
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

// gatherDiagnostics collects the failure dump on a fresh, bounded context: the verdict is already
// in, so a caller deadline that just expired must not take the diagnostics away.
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

// pollUntilReady polls check until it succeeds or timeout expires, logging delta-only and
// reporting elapsed wall-clock time in the result or the returned failure.
//
// Budget contract:
//   - A verdict takes at most timeout + finalCheckTimeout; the extra check covers a condition met
//     between the last poll and the deadline, and is skipped when the last verdict was the poll
//     context ending (see pollObservationError).
//   - Failure reporting may take a further diagnoseTimeout; the reported elapsed excludes it, so a
//     caller bounding total wall clock must allow for both parts.
//
// If diagnose is set, its output is appended to every returned failure, including cancellation.
// A check error wrapping the ended polling context's error does not replace an earlier failure's
// message; both stay available for unwrapping.
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

	// Cancellation reports diagnostics like a timeout: a suite-level timeout cutting off a long
	// upgrade poll is exactly when the dump matters most. lastErr carries the check's last
	// evidence; fall back to the context error when the check never observed anything.
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

	// Look once more after the deadline: a condition met between the last poll and the deadline
	// would otherwise read as a timeout. The retry is skipped when the last verdict was the poll
	// context ending, since the preserved observation is then the better report.
	var interruption *pollObservationError
	if lastErr == nil || !errors.As(lastErr, &interruption) {
		finalCtx, cancelFinal := context.WithTimeout(ctx, finalCheckTimeout)
		finalErr := check(finalCtx)
		cancelFinal()
		lastErr = finalErr

		// Recompute: the final check runs past the deadline, so the loop's own elapsed would
		// understate how long the verifier took.
		elapsed = time.Since(startTime)

		if finalErr == nil {
			// A verdict that raced the caller's cancellation is not a pass the caller can rely
			// on: report the interruption instead.
			if ctx.Err() != nil {
				return reportCancelled(nil)
			}
			logVerifierTiming(name, "succeeded on the final check after the deadline", elapsed)
			return nil
		}
	}

	// The caller's context can expire while the final check runs; report that cancellation
	// rather than recasting it as a timeout.
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
