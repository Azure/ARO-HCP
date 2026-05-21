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

type pollConfig struct {
	timeout  time.Duration
	interval time.Duration
	diagnose diagnoseFunc
}

// PollOption configures optional polling behavior for verifier constructors.
// Test authors should use [WithPolling] when a condition may take time to become true,
// and omit poll options for a single-shot check. Verifier authors pass opts to maybePoll.
type PollOption func(*pollConfig)

// WithPolling is the only poll option intended for E2E tests. It wraps the verifier so
// Verify polls until success or timeout, with delta-only logging and elapsed-time reporting.
// Omit WithPolling (and any other PollOption) when the condition must already be true.
//
// Example:
//
//	err := verifiers.VerifyDaemonSetReady(ns, name, verifiers.WithPolling(10*time.Minute, 15*time.Second)).
//		Verify(ctx, adminRESTConfig)
func WithPolling(timeout, interval time.Duration) PollOption {
	return func(c *pollConfig) {
		c.timeout = timeout
		if interval > 0 {
			c.interval = interval
		}
	}
}

func applyPollOptions(opts []PollOption) pollConfig {
	cfg := pollConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.interval == 0 {
		cfg.interval = DefaultPollInterval
	}
	return cfg
}

// maybePoll applies poll options from verifier constructors. Internal to this package; tests
// must not call it — use [WithPolling] on the public constructor instead.
func maybePoll(inner HostedClusterVerifier, opts []PollOption, defaultDiagnose diagnoseFunc) HostedClusterVerifier {
	cfg := applyPollOptions(opts)
	if cfg.timeout == 0 {
		return inner
	}
	if cfg.diagnose == nil {
		cfg.diagnose = defaultDiagnose
	}
	return pollVerifier{
		inner:    inner,
		timeout:  cfg.timeout,
		interval: cfg.interval,
		diagnose: cfg.diagnose,
	}
}

type pollVerifier struct {
	inner    HostedClusterVerifier
	timeout  time.Duration
	interval time.Duration
	diagnose diagnoseFunc
}

func (p pollVerifier) Name() string {
	return p.inner.Name()
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

// Verify polls inner.Verify until it succeeds or timeout expires. Failure details are logged
// only when the error message changes between polls (delta-only logging). Actual elapsed
// wall-clock time is logged on both success and failure. If diagnose is set, its output is
// logged and appended when the poll times out.
func (p pollVerifier) Verify(ctx context.Context, restConfig *rest.Config) error {
	logger := ginkgo.GinkgoLogr
	var previousError string
	var lastErr error
	startTime := time.Now()
	name := p.inner.Name()

	logger.Info("Verifier polling", "name", name, "timeout", p.timeout.String(), "interval", p.interval.String())
	ginkgo.GinkgoWriter.Printf("[%s] polling (timeout=%s, interval=%s)\n", name, p.timeout, p.interval)

	err := wait.PollUntilContextTimeout(ctx, p.interval, p.timeout, true, func(ctx context.Context) (bool, error) {
		err := p.inner.Verify(ctx, restConfig)
		if err == nil {
			return true, nil
		}
		currentError := err.Error()
		if currentError != previousError {
			logger.Info("Verifier check", "name", p.inner.Name(), "status", "failed", "error", currentError)
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
	if p.diagnose != nil {
		diagCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if details := p.diagnose(diagCtx, restConfig); details != "" {
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
