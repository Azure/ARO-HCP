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

// pollUntilReady polls check until it succeeds or timeout expires. Failure details are logged
// only when the error message changes between polls (delta-only logging). Actual elapsed
// wall-clock time is logged on both success and failure. If diagnose is set, its output is
// logged and appended when the poll times out. If the polling context ends, a check
// error wrapping that context's error does not replace or re-log an earlier failure's
// message. Both observed errors remain available for unwrapping, and a cancellation that
// lands between checks returns the retained observation together with the context's error.
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
	// lastErrLive records whether lastErr was observed while the poll context was
	// still live — an observation that predates the context ending is paired with
	// the context's error on a cancelled exit, while an error observed after the
	// context ended is retained as the failure.
	var lastErrLive bool
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
			lastErrLive = false
			return false, nil
		}
		currentError := err.Error()
		if currentError != previousError {
			logger.Info("Verifier check", "name", name, "status", "failed", "error", currentError)
			previousError = currentError
		}
		lastErr = err
		lastErrLive = ctx.Err() == nil
		return false, nil
	})

	elapsed := time.Since(startTime)
	if err == nil {
		logVerifierTiming(name, "succeeded", elapsed)
		return nil
	}

	if ctx.Err() != nil {
		logVerifierTiming(name, "cancelled", elapsed)
		switch {
		case lastErr == nil:
			return fmt.Errorf("%s cancelled after %s: %w", name, elapsed.Round(time.Millisecond), ctx.Err())
		case lastErrLive:
			// Cancellation landed between polls, after the check that recorded the
			// observation: pair them (as the in-request interruption path does) so
			// errors.Is(err, ctx.Err()) still identifies the cancellation.
			return fmt.Errorf("%s cancelled after %s: %w", name, elapsed.Round(time.Millisecond),
				&pollObservationError{observation: lastErr, interruption: ctx.Err()})
		default:
			return fmt.Errorf("%s cancelled after %s: %w", name, elapsed.Round(time.Millisecond), lastErr)
		}
	}

	logVerifierTiming(name, "timed out", elapsed)
	if diagnose != nil {
		diagCtx, cancel := context.WithTimeout(context.Background(), diagnoseTimeout)
		defer cancel()
		if details := diagnose(diagCtx, restConfig); details != "" {
			logger.Info("Failure diagnostics", "name", name, "details", details)
			if lastErr != nil {
				return fmt.Errorf("%s timed out after %s: %w\n%s", name, elapsed.Round(time.Millisecond), lastErr, details)
			}
			return fmt.Errorf("%s timed out after %s\n%s", name, elapsed.Round(time.Millisecond), details)
		}
	}
	if lastErr != nil {
		return fmt.Errorf("%s timed out after %s: %w", name, elapsed.Round(time.Millisecond), lastErr)
	}
	return fmt.Errorf("%s timed out after %s: %w", name, elapsed.Round(time.Millisecond), err)
}
