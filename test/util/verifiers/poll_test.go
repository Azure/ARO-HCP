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
