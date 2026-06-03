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
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
)

// DefaultPollInterval is the standard interval between verifier poll attempts in E2E tests.
const DefaultPollInterval = 15 * time.Second

type diagnoseFunc func(ctx context.Context, restConfig *rest.Config) string

type waitSettings struct {
	timeout  time.Duration
	interval time.Duration
}

// WaitOption configures optional wait behavior on verifier constructors. When timeout is
// zero (the default), Verify performs a single check. Verifier authors store the result
// on the verifier struct and call verifyOnceOrPoll from Verify.
type WaitOption func(*waitSettings)

// WithWait configures Verify to poll until the condition succeeds or timeout expires.
// Omit WithWait when the condition must already be true.
//
// Example:
//
//	err := verifiers.VerifyDaemonSetReady(ns, name, verifiers.WithWait(10*time.Minute, 15*time.Second)).
//		Verify(ctx, adminRESTConfig)
func WithWait(timeout, interval time.Duration) WaitOption {
	return func(c *waitSettings) {
		c.timeout = timeout
		if interval > 0 {
			c.interval = interval
		}
	}
}

func applyWaitOptions(opts []WaitOption) waitSettings {
	cfg := waitSettings{}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.timeout > 0 && cfg.interval == 0 {
		cfg.interval = DefaultPollInterval
	}
	return cfg
}

// verifyOnceOrPoll runs check once or polls until success depending on wait settings.
func verifyOnceOrPoll(
	ctx context.Context,
	name string,
	restConfig *rest.Config,
	wait waitSettings,
	diagnose diagnoseFunc,
	check func(context.Context, *rest.Config) error,
) error {
	if wait.timeout == 0 {
		return check(ctx, restConfig)
	}
	return pollUntilReady(ctx, name, wait.timeout, wait.interval, restConfig, diagnose, func(ctx context.Context) error {
		return check(ctx, restConfig)
	})
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
// logged and appended when the poll times out.
func pollUntilReady(
	ctx context.Context,
	name string,
	timeout, interval time.Duration,
	restConfig *rest.Config,
	diagnose diagnoseFunc,
	check func(context.Context) error,
) error {
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
		currentError := err.Error()
		if currentError != previousError {
			logger.Info("Verifier check", "name", name, "status", "failed", "error", currentError)
			previousError = currentError
		}
		lastErr = err
		return false, nil
	})

	elapsed := time.Since(startTime)
	if err == nil {
		logVerifierTiming(name, "succeeded", elapsed)
		return nil
	}

	logVerifierTiming(name, "timed out", elapsed)
	if diagnose != nil {
		diagCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
