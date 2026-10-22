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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega/matchers"
	"github.com/stretchr/testify/require"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
)

type imagePullRoundTripper func(*http.Request) (*http.Response, error)

func (transport imagePullRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestImagePullFailureSummary(t *testing.T) {
	requestFailure := errors.New("API connection interrupted")
	for _, scenario := range []struct {
		name        string
		body        string
		status      int
		requestErr  error
		cancel      bool
		observation string
	}{
		{name: "image not observed", body: `{"apiVersion":"v1","kind":"PodList","items":[]}`, status: 200, observation: "no pods found"},
		{name: "API not found", body: `{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"NotFound","message":"namespace missing","code":404}`, status: 404, observation: "namespace missing"},
		{name: "unrelated API error", requestErr: requestFailure, cancel: true, observation: requestFailure.Error()},
		{name: "no pod observation", requestErr: context.Canceled, cancel: true, observation: "context canceled"},
		{name: "independent deadline", requestErr: context.DeadlineExceeded, observation: "context deadline exceeded"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			config := &rest.Config{Host: "https://api.example", Transport: imagePullRoundTripper(func(*http.Request) (*http.Response, error) {
				if scenario.cancel {
					cancel()
				}
				if scenario.requestErr != nil {
					return nil, scenario.requestErr
				}
				return &http.Response{StatusCode: scenario.status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(scenario.body))}, nil
			})}
			err := VerifyImagePulled("namespace", "registry.example", "image", 20*time.Millisecond).Verify(ctx, config)
			require.Error(t, err)
			headline, _, _ := strings.Cut(err.Error(), "\n")
			require.Equal(t, "image pull not confirmed; cause undetermined (insufficient evidence)", headline)
			require.Contains(t, err.Error(), "polling observation: VerifyImagePulled(")
			require.Contains(t, err.Error(), scenario.observation)
			if scenario.requestErr != nil {
				require.ErrorIs(t, err, scenario.requestErr)
			}
			if scenario.status == 404 {
				require.True(t, apierrors.IsNotFound(err))
			}
			rendered := (&matchers.HaveOccurredMatcher{}).NegatedFailureMessage(err)
			require.Contains(t, rendered, headline)
			require.Contains(t, rendered, "polling observation: VerifyImagePulled(")
			require.Less(t, strings.Index(rendered, headline), strings.Index(rendered, "VerifyImagePulled("))
			t.Logf("summary: %s; observation: %s", headline, scenario.observation)
		})
	}
}

func TestImagePullVerificationSuccess(t *testing.T) {
	config := &rest.Config{Host: "https://api.example", Transport: imagePullRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"apiVersion":"v1","kind":"PodList","items":[{"status":{"containerStatuses":[{"image":"registry.example/image","imageID":"sha256:observed"}]}}]}`))}, nil
	})}
	require.NoError(t, VerifyImagePulled("namespace", "registry.example", "image", time.Second).Verify(context.Background(), config))
}

func TestPollUntilReady_ZeroTimeout(t *testing.T) {
	err := pollUntilReady(
		context.Background(),
		"test-verifier",
		0,
		DefaultPollInterval,
		nil,
		DefaultDiagnoseTimeout,
		nil,
		func(ctx context.Context) error { return nil },
	)
	if err == nil {
		t.Fatal("expected error for zero timeout, got nil")
	}
	if !strings.Contains(err.Error(), "timeout must be > 0") {
		t.Fatalf("unexpected error message: %s", err.Error())
	}
}

func TestPollUntilReady_NegativeTimeout(t *testing.T) {
	err := pollUntilReady(
		context.Background(),
		"test-verifier",
		-1*time.Second,
		DefaultPollInterval,
		nil,
		DefaultDiagnoseTimeout,
		nil,
		func(ctx context.Context) error { return nil },
	)
	if err == nil {
		t.Fatal("expected error for negative timeout, got nil")
	}
}

func TestPollUntilReady_ParentContextDeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := pollUntilReady(
		ctx,
		"test-verifier",
		10*time.Second,
		10*time.Millisecond,
		nil,
		DefaultDiagnoseTimeout,
		nil,
		func(ctx context.Context) error { return fmt.Errorf("not ready") },
	)
	if err == nil {
		t.Fatal("expected error when parent context deadline exceeded, got nil")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("expected 'cancelled' in error, got: %s", err.Error())
	}
}

// TestPollUntilReady_CancellationRunsDiagnostics pins the cancellation branch to the same contract
// as the timeout branch: a caller context cancelled mid-poll (a suite timeout firing is a
// realistic CI failure) must still gather the diagnostics, detached from the cancelled context,
// and append them to the returned error.
func TestPollUntilReady_CancellationRunsDiagnostics(t *testing.T) {
	parentCtx, cancel := context.WithCancel(context.Background())

	diagnoseSawCancelledContext := false
	err := pollUntilReady(
		parentCtx,
		"test-verifier",
		10*time.Second, // far beyond the cancellation: only the caller's context can end the wait
		10*time.Millisecond,
		nil,
		DefaultDiagnoseTimeout,
		func(ctx context.Context, _ *rest.Config) string {
			// A diagnose bound to the cancelled caller would see Done closed and bail; a
			// detached one gets a fresh context.
			select {
			case <-ctx.Done():
				diagnoseSawCancelledContext = true
				return ""
			default:
			}
			return "clusterversion diagnostics"
		},
		func(ctx context.Context) error {
			cancel() // the suite timeout fires mid-poll
			return fmt.Errorf("not ready")
		},
	)
	if err == nil {
		t.Fatal("expected an error when the caller's context is cancelled, got nil")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("expected 'cancelled' in the error, got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("expected the last check error to be wrapped, got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "clusterversion diagnostics") {
		t.Fatalf("expected cancellation to carry the diagnostics, got: %s", err.Error())
	}
	if diagnoseSawCancelledContext {
		t.Fatal("diagnose ran bound to the cancelled caller context instead of a detached one")
	}
}

func TestPollUntilReady_PositiveTimeout(t *testing.T) {
	err := pollUntilReady(
		context.Background(),
		"test-verifier",
		5*time.Second,
		100*time.Millisecond,
		nil,
		DefaultDiagnoseTimeout,
		nil,
		func(ctx context.Context) error { return nil },
	)
	if err != nil {
		t.Fatalf("expected success for positive timeout with passing check, got: %v", err)
	}
}

// TestPollUntilReady_SucceedsWhenConditionLandsAfterLastPoll covers the ARO-26775 near-miss: the
// condition becomes true between the final poll and the deadline. Without the post-deadline
// check this reports a timeout on work that finished inside the budget.
func TestPollUntilReady_SucceedsWhenConditionLandsAfterLastPoll(t *testing.T) {
	// An interval longer than the timeout means the ticker can never fire before the deadline,
	// so the loop gets exactly one look: the immediate poll. Readiness is then driven off the
	// check counter rather than the wall clock, so which call observes success does not depend
	// on goroutine scheduling. The second call can only be the post-deadline check -- the same
	// shape as a target version that appeared 22s before a 45m deadline whose preceding poll had
	// fired 2m earlier. Success here is reachable only through the post-deadline check.
	const (
		timeout  = 1 * time.Second
		interval = 5 * time.Second
	)
	var checks int

	err := pollUntilReady(
		context.Background(),
		"test-verifier",
		timeout,
		interval,
		nil,
		DefaultDiagnoseTimeout,
		nil,
		func(ctx context.Context) error {
			checks++
			if checks < 2 {
				return fmt.Errorf("not ready")
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("expected the post-deadline check to observe the condition, got: %v", err)
	}
	// One in-loop poll that saw "not ready", then the post-deadline check that saw success.
	if checks != 2 {
		t.Fatalf("expected 2 checks (1 in-loop poll + 1 post-deadline), got %d", checks)
	}
}

// TestPollUntilReady_OvershootIsBoundedByFinalCheckTimeout pins the first half of the budget
// contract documented on pollUntilReady: reaching a verdict takes timeout plus at most
// finalCheckTimeout, and no longer. A check that blocks forever must be cancelled by the final
// check's own deadline. Diagnostics are the second half and are covered separately by
// TestPollUntilReady_DiagnosticsAreBoundedAndExcludedFromElapsed.
func TestPollUntilReady_OvershootIsBoundedByFinalCheckTimeout(t *testing.T) {
	const timeout = 250 * time.Millisecond

	startTime := time.Now()
	err := pollUntilReady(
		context.Background(),
		"test-verifier",
		timeout,
		100*time.Millisecond,
		nil,
		DefaultDiagnoseTimeout,
		nil,
		func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	)
	total := time.Since(startTime)

	if err == nil {
		t.Fatal("expected an error when the check never succeeds, got nil")
	}
	// Generous upper bound: the point is that the overshoot is governed by finalCheckTimeout
	// rather than being unbounded, not that the scheduler is precise.
	if maximum := timeout + finalCheckTimeout + 2*time.Second; total > maximum {
		t.Fatalf("overshot the budget contract: ran for %s, expected at most %s", total, maximum)
	}
	if total < timeout {
		t.Fatalf("returned before the timeout elapsed: ran for %s, expected at least %s", total, timeout)
	}
}

// TestPollUntilReady_ReportedElapsedIncludesFinalCheck guards the elapsed-time reporting that this
// package exists to provide: the final check runs past the deadline, so reporting the loop's own
// elapsed time would tell a reader the verifier finished sooner than it did.
func TestPollUntilReady_ReportedElapsedIncludesFinalCheck(t *testing.T) {
	const (
		timeout         = 200 * time.Millisecond
		finalCheckDelay = 1500 * time.Millisecond
	)
	var checks int

	err := pollUntilReady(
		context.Background(),
		"test-verifier",
		timeout,
		1*time.Second, // longer than the timeout, so the loop gets exactly one look
		nil,
		DefaultDiagnoseTimeout,
		nil,
		func(ctx context.Context) error {
			checks++
			if checks > 1 {
				// The post-deadline check: slow, and still unsuccessful.
				select {
				case <-time.After(finalCheckDelay):
				case <-ctx.Done():
				}
			}
			return fmt.Errorf("not ready")
		},
	)
	if err == nil {
		t.Fatal("expected a timeout error when the condition never holds, got nil")
	}

	// The reported duration must account for the slow final check rather than stopping at the
	// deadline. Parsing the exact value is brittle, so assert that the pre-check value is gone.
	if strings.Contains(err.Error(), "timed out after "+timeout.String()) {
		t.Fatalf("reported elapsed excludes the final check, understating the real duration: %s", err.Error())
	}
}

// TestPollUntilReady_DiagnosticsAreBoundedAndExcludedFromElapsed pins the second half of the
// budget contract. Gathering diagnostics happens after the verdict, so it must be bounded by
// diagnoseTimeout rather than running indefinitely, and it must not inflate the elapsed duration
// the error reports -- that number says how long the condition was waited on, and a reader uses it
// to size the timeout.
func TestPollUntilReady_DiagnosticsAreBoundedAndExcludedFromElapsed(t *testing.T) {
	const (
		timeout         = 200 * time.Millisecond
		diagnoseTimeout = 750 * time.Millisecond
	)

	startTime := time.Now()
	err := pollUntilReady(
		context.Background(),
		"test-verifier",
		timeout,
		1*time.Second, // longer than the timeout, so the loop gets exactly one look
		nil,
		diagnoseTimeout,
		func(ctx context.Context, _ *rest.Config) string {
			// A diagnostic collector that never returns on its own: only diagnoseTimeout can
			// stop it.
			<-ctx.Done()
			return "diagnostics gave up: " + ctx.Err().Error()
		},
		func(ctx context.Context) error { return fmt.Errorf("not ready") },
	)
	total := time.Since(startTime)

	if err == nil {
		t.Fatal("expected a timeout error when the condition never holds, got nil")
	}
	if !strings.Contains(err.Error(), "diagnostics gave up") {
		t.Fatalf("expected the diagnostics to be appended to the error, got: %s", err.Error())
	}
	// Generous upper bound: the point is that diagnostics are governed by diagnoseTimeout rather
	// than being unbounded, not that the scheduler is precise.
	if maximum := timeout + finalCheckTimeout + diagnoseTimeout + 2*time.Second; total > maximum {
		t.Fatalf("diagnostics overran their bound: ran for %s, expected at most %s", total, maximum)
	}

	reported := reportedElapsed(t, err)
	if reported >= diagnoseTimeout {
		t.Fatalf("reported elapsed %s includes the %s spent on diagnostics; it should measure the wait for the condition only",
			reported, diagnoseTimeout)
	}
}

// reportedElapsed extracts the duration pollUntilReady reports in a timeout error.
func reportedElapsed(t *testing.T, err error) time.Duration {
	t.Helper()
	const marker = "timed out after "
	message := err.Error()
	index := strings.Index(message, marker)
	if index < 0 {
		t.Fatalf("error does not report an elapsed duration: %s", message)
	}
	remainder := message[index+len(marker):]
	value, _, _ := strings.Cut(remainder, "\n")
	value, _, _ = strings.Cut(value, ":")
	parsed, parseErr := time.ParseDuration(strings.TrimSpace(value))
	if parseErr != nil {
		t.Fatalf("parse reported elapsed %q: %v", value, parseErr)
	}
	return parsed
}

// TestPollUntilReady_StillFailsWhenConditionNeverHolds guards against the post-deadline check
// masking a genuine timeout.
func TestPollUntilReady_StillFailsWhenConditionNeverHolds(t *testing.T) {
	err := pollUntilReady(
		context.Background(),
		"test-verifier",
		250*time.Millisecond,
		100*time.Millisecond,
		nil,
		DefaultDiagnoseTimeout,
		nil,
		func(ctx context.Context) error { return fmt.Errorf("not ready") },
	)
	if err == nil {
		t.Fatal("expected a timeout error when the condition never holds, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected 'timed out' in error, got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("expected the last check error to be wrapped, got: %s", err.Error())
	}
}

func TestPollUntilReady_TerminalErrors(t *testing.T) {
	observation := apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, "not-yet-created")
	requestCanceled := fmt.Errorf("failed to list pods: %w", context.Canceled)
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "test-pod", errors.New("access denied"))
	notFound := apierrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, "test-namespace")

	for _, scenario := range []struct {
		name        string
		previousErr error
		terminalErr error
		wantErr     error
	}{
		{"preserve observation on cancellation", observation, requestCanceled, observation},
		{"retain cancellation without observation", nil, requestCanceled, requestCanceled},
		{"retain genuine API failure", observation, forbidden, forbidden},
		{"retain missing namespace during cancellation", observation, notFound, notFound},
		{"success after observation", observation, nil, nil},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var output bytes.Buffer
			ginkgo.GinkgoWriter.TeeTo(&output)
			defer ginkgo.GinkgoWriter.ClearTeeWriters()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			attempts := 0
			err := pollUntilReady(ctx, "test-verifier", time.Second, time.Millisecond, nil, 0, nil, func(context.Context) error {
				attempts++
				if attempts == 1 && scenario.previousErr != nil {
					return scenario.previousErr
				}
				if scenario.terminalErr != nil {
					cancel()
				}
				return scenario.terminalErr
			})
			if !errors.Is(err, scenario.wantErr) {
				t.Fatalf("expected %v, got %v", scenario.wantErr, err)
			}
			if scenario.wantErr == observation {
				t.Logf("preserved observation=%t, preserved cancellation=%t", errors.Is(err, observation), errors.Is(err, context.Canceled))
				require.ErrorIs(t, err, requestCanceled)
				require.ErrorIs(t, err, context.Canceled)
				require.True(t, wait.Interrupted(err))
				var statusError *apierrors.StatusError
				require.ErrorAs(t, err, &statusError)
				require.Same(t, observation, statusError)
				require.NotContains(t, err.Error(), requestCanceled.Error())
			}
			if scenario.terminalErr == forbidden || scenario.terminalErr == notFound {
				require.NotErrorIs(t, err, context.Canceled)
				require.NotErrorIs(t, err, context.DeadlineExceeded)
				require.False(t, wait.Interrupted(err))
			}
			if scenario.wantErr == observation && strings.Contains(output.String(), requestCanceled.Error()) {
				t.Fatalf("terminal cancellation was logged as another failure: %s", output.String())
			}
		})
	}
}

func TestPollUntilReady_DeadlineErrors(t *testing.T) {
	observation := errors.New("pod is not ready")
	for _, parentDeadline := range []bool{false, true} {
		for _, previousObservation := range []bool{false, true} {
			t.Run(fmt.Sprintf("parentDeadline=%t/previousObservation=%t", parentDeadline, previousObservation), func(t *testing.T) {
				ctx := context.Background()
				timeout := 100 * time.Millisecond
				wantOutcome := "timed out"
				if parentDeadline {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, timeout)
					defer cancel()
					timeout = time.Second
					wantOutcome = "cancelled"
				}
				attempts := 0
				var terminalErr error
				err := pollUntilReady(ctx, "test-verifier", timeout, time.Millisecond, nil, 0, nil, func(ctx context.Context) error {
					attempts++
					if attempts == 1 && previousObservation {
						return observation
					}
					<-ctx.Done()
					terminalErr = fmt.Errorf("failed to list pods: %w", ctx.Err())
					return terminalErr
				})
				wantErr := terminalErr
				if previousObservation {
					wantErr = observation
				}
				if terminalErr == nil || !errors.Is(err, wantErr) {
					t.Fatalf("expected %v after a deadline request error, got %v", wantErr, err)
				}
				t.Logf("previous observation=%t, preserved deadline=%t", previousObservation, errors.Is(err, context.DeadlineExceeded))
				require.ErrorIs(t, err, terminalErr)
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.True(t, wait.Interrupted(err))
				if previousObservation {
					require.NotContains(t, err.Error(), terminalErr.Error())
				}
				if !strings.Contains(err.Error(), wantOutcome) {
					t.Fatalf("expected outcome %q, got %v", wantOutcome, err)
				}
			})
		}
	}
}

func TestPollUntilReady_IndependentContextErrors(t *testing.T) {
	for _, requestErr := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(requestErr.Error(), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			observation := errors.New("pod is not ready")
			independentErr := fmt.Errorf("independent request failed: %w", requestErr)
			attempts := 0
			err := pollUntilReady(ctx, "test-verifier", time.Second, time.Millisecond, nil, 0, nil, func(ctx context.Context) error {
				attempts++
				switch attempts {
				case 1:
					return observation
				case 2:
					if ctx.Err() != nil {
						t.Fatalf("poll context unexpectedly ended: %v", ctx.Err())
					}
					return independentErr
				default:
					cancel()
					return ctx.Err()
				}
			})
			if !errors.Is(err, independentErr) {
				t.Fatalf("expected independent request error %v, got %v", independentErr, err)
			}
		})
	}
}
