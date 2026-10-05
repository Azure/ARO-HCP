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

package framework

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

func conflictError(code string) error {
	return &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: code}
}

func TestIsInFlightProvisioningState(t *testing.T) {
	for state, want := range map[string]bool{
		"":               false,
		"Accepted":       true,
		"provisioning":   true,
		"Updating":       true,
		"Succeeded":      false,
		"Failed":         false,
		"Canceled":       false,
		"Deleting":       false,
		"AwaitingSecret": false,
	} {
		if got := isInFlightProvisioningState(state); got != want {
			t.Errorf("isInFlightProvisioningState(%q) = %v, want %v", state, got, want)
		}
	}
}

func TestIsDeploymentActiveError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "non-Azure error", err: errors.New("boom"), want: false},
		{name: "409 DeploymentActive", err: conflictError("DeploymentActive"), want: true},
		{name: "wrapped 409 DeploymentActive", err: fmt.Errorf("outer: %w", conflictError("DeploymentActive")), want: true},
		{name: "409 other code", err: conflictError("Conflict"), want: false},
		{name: "400 DeploymentActive", err: &azcore.ResponseError{StatusCode: http.StatusBadRequest, ErrorCode: "DeploymentActive"}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDeploymentActiveError(tc.err); got != tc.want {
				t.Errorf("isDeploymentActiveError() = %v, want %v", got, tc.want)
			}
		})
	}
}

type fakeResource struct {
	Name string
}

// sequenceGetter returns the given states in order, repeating the last one.
func sequenceGetter(states ...string) (func(context.Context) (fakeResource, string, error), *int) {
	calls := 0
	return func(context.Context) (fakeResource, string, error) {
		state := states[len(states)-1]
		if calls < len(states) {
			state = states[calls]
		}
		calls++
		return fakeResource{Name: "np-1"}, state, nil
	}, &calls
}

// attemptTracker returns a tracker as requestAttemptTrackerPolicy would leave
// it after attempts tries, where outcomeUnknown says whether any of them ended
// without a definitive answer from the service.
func attemptTracker(attempts int32, outcomeUnknown bool) *requestAttemptTracker {
	tracker := &requestAttemptTracker{}
	tracker.attempts.Store(attempts)
	tracker.outcomeUnknown.Store(outcomeUnknown)
	return tracker
}

func TestResumeInFlightCreateReturnsOriginalError(t *testing.T) {
	notFound := &azcore.ResponseError{StatusCode: http.StatusNotFound}
	tests := []struct {
		name      string
		createErr error
		attempts  *requestAttemptTracker
		state     string
		getErr    error
		wantGets  int
	}{
		{name: "not a conflict", createErr: &azcore.ResponseError{StatusCode: http.StatusServiceUnavailable}, attempts: attemptTracker(2, true), state: "Provisioning", wantGets: 0},
		{name: "conflict on the first attempt", createErr: conflictError("Conflict"), attempts: attemptTracker(1, false), state: "Provisioning", wantGets: 0},
		{name: "conflict after a definitively rejected attempt", createErr: conflictError("Conflict"), attempts: attemptTracker(2, false), state: "Provisioning", wantGets: 0},
		{name: "resource cannot be read", createErr: conflictError("Conflict"), attempts: attemptTracker(2, true), getErr: notFound, wantGets: 1},
		{name: "resource already succeeded", createErr: conflictError("Conflict"), attempts: attemptTracker(2, true), state: "Succeeded", wantGets: 1},
		{name: "resource is deleting", createErr: conflictError("Conflict"), attempts: attemptTracker(2, true), state: "Deleting", wantGets: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gets := 0
			get := func(context.Context) (fakeResource, string, error) {
				gets++
				return fakeResource{}, tc.state, tc.getErr
			}
			poller, err := resumeInFlightCreate(context.Background(), tc.createErr, tc.attempts, "nodepool np-1", get)
			if poller != nil {
				t.Fatalf("resumeInFlightCreate() returned a poller, want nil")
			}
			if err != tc.createErr {
				t.Fatalf("resumeInFlightCreate() error = %v, want the original error %v", err, tc.createErr)
			}
			if gets != tc.wantGets {
				t.Errorf("get called %d times, want %d", gets, tc.wantGets)
			}
		})
	}
}

func TestResumeInFlightCreateWaitsForSuccess(t *testing.T) {
	get, calls := sequenceGetter("Provisioning", "Provisioning", "Succeeded")
	poller, err := resumeInFlightCreate(context.Background(), conflictError("Conflict"), attemptTracker(2, true), "nodepool np-1", get)
	if err != nil {
		t.Fatalf("resumeInFlightCreate() unexpected error: %v", err)
	}

	result, err := poller.PollUntilDone(context.Background(), &runtime.PollUntilDoneOptions{Frequency: time.Second})
	if err != nil {
		t.Fatalf("PollUntilDone() unexpected error: %v", err)
	}
	if result.Name != "np-1" {
		t.Errorf("PollUntilDone() result = %+v, want the resource returned by get", result)
	}
	if *calls != 3 {
		t.Errorf("get called %d times, want 3 (conflict check plus two polls)", *calls)
	}
	if !poller.Done() {
		t.Errorf("poller.Done() = false after PollUntilDone returned")
	}
}

func TestResumeInFlightCreateReportsFailure(t *testing.T) {
	createErr := conflictError("Conflict")
	get, _ := sequenceGetter("Accepted", "Failed")
	poller, err := resumeInFlightCreate(context.Background(), createErr, attemptTracker(2, true), "nodepool np-1", get)
	if err != nil {
		t.Fatalf("resumeInFlightCreate() unexpected error: %v", err)
	}

	_, err = poller.PollUntilDone(context.Background(), &runtime.PollUntilDoneOptions{Frequency: time.Second})
	if err == nil {
		t.Fatalf("PollUntilDone() error = nil, want an error for provisioning state Failed")
	}
	if !strings.Contains(err.Error(), `provisioning state "Failed"`) || !strings.Contains(err.Error(), "nodepool np-1") {
		t.Errorf("PollUntilDone() error = %q, want it to name the resource and the Failed state", err)
	}
	if !errors.Is(err, createErr) {
		t.Errorf("PollUntilDone() error does not wrap the original conflict")
	}
}

func TestResumeInFlightCreatePollReportsFailure(t *testing.T) {
	createErr := conflictError("Conflict")
	get, _ := sequenceGetter("Accepted", "Canceled")
	poller, err := resumeInFlightCreate(context.Background(), createErr, attemptTracker(2, true), "cluster c1", get)
	if err != nil {
		t.Fatalf("resumeInFlightCreate() unexpected error: %v", err)
	}

	// Callers that do not wait for completion only call Poll once.
	_, err = poller.Poll(context.Background())
	if err == nil || !strings.Contains(err.Error(), `provisioning state "Canceled"`) {
		t.Fatalf("Poll() error = %v, want the Canceled terminal state", err)
	}
	if !errors.Is(err, createErr) {
		t.Errorf("Poll() error does not wrap the original conflict")
	}
	if !poller.Done() {
		t.Errorf("poller.Done() = false after a terminal state")
	}
	if _, err := poller.Result(context.Background()); err == nil {
		t.Errorf("Result() error = nil after a terminal failure")
	}
}

func TestWaitForActiveDeployment(t *testing.T) {
	t.Run("returns once the active deployment is terminal", func(t *testing.T) {
		err := waitForActiveDeployment(context.Background(), "deployment d1", func(context.Context) (string, error) {
			return "Failed", nil
		})
		if err != nil {
			t.Fatalf("waitForActiveDeployment() unexpected error: %v", err)
		}
	})

	t.Run("returns once the active deployment is gone", func(t *testing.T) {
		err := waitForActiveDeployment(context.Background(), "deployment d1", func(context.Context) (string, error) {
			return "", &azcore.ResponseError{StatusCode: http.StatusNotFound}
		})
		if err != nil {
			t.Fatalf("waitForActiveDeployment() unexpected error: %v", err)
		}
	})

	t.Run("gives up when the context ends", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		err := waitForActiveDeployment(ctx, "deployment d1", func(context.Context) (string, error) {
			return "Running", nil
		})
		if err == nil {
			t.Fatalf("waitForActiveDeployment() error = nil, want a timeout")
		}
		if !strings.Contains(err.Error(), `last provisioning state "Running"`) {
			t.Errorf("waitForActiveDeployment() error = %q, want it to report the last state", err)
		}
	})
}

// sendAttempts records a PUT's attempts against the tracker in ctx, as
// requestAttemptTrackerPolicy does for each try of a request.
func sendAttempts(ctx context.Context, result putResult) {
	if tracker, ok := ctx.Value(requestAttemptTrackerKey{}).(*requestAttemptTracker); ok {
		tracker.attempts.Add(result.attempts)
		if result.outcomeUnknown {
			tracker.outcomeUnknown.Store(true)
		}
	}
}

// putResult is the outcome of one deployment PUT: how many SDK attempts it
// took, whether any attempt ended with an unknown outcome (a 503, say), and its
// error (nil for success).
type putResult struct {
	attempts       int32
	outcomeUnknown bool
	err            error
}

func TestBeginDeploymentWithResubmit(t *testing.T) {
	deploymentActive := conflictError("DeploymentActive")
	succeeded := func(context.Context) (string, error) { return "Succeeded", nil }

	tests := []struct {
		name string
		// results[i] is the outcome of the i-th PUT; the last entry repeats.
		results     []putResult
		wantBegins  int
		wantSuccess bool
		wantErrText string
	}{
		{
			name:        "resubmits after an SDK retry was rejected with DeploymentActive",
			results:     []putResult{{2, true, deploymentActive}, {1, false, nil}},
			wantBegins:  2,
			wantSuccess: true,
		},
		{
			name:        "resubmits again when the resubmission is also retried into DeploymentActive",
			results:     []putResult{{2, true, deploymentActive}, {2, true, deploymentActive}, {1, false, nil}},
			wantBegins:  3,
			wantSuccess: true,
		},
		{
			name:        "returns DeploymentActive on the first attempt without resubmitting",
			results:     []putResult{{1, false, deploymentActive}},
			wantBegins:  1,
			wantErrText: "DeploymentActive",
		},
		{
			name:        "returns DeploymentActive after a definitively rejected attempt without resubmitting",
			results:     []putResult{{2, false, deploymentActive}},
			wantBegins:  1,
			wantErrText: "DeploymentActive",
		},
		{
			name:        "returns other errors without resubmitting",
			results:     []putResult{{2, true, &azcore.ResponseError{StatusCode: http.StatusBadRequest, ErrorCode: "InvalidTemplate"}}},
			wantBegins:  1,
			wantErrText: "InvalidTemplate",
		},
		{
			name:        "gives up after the maximum number of resubmissions",
			results:     []putResult{{2, true, deploymentActive}},
			wantBegins:  maxDeploymentResubmissions + 1,
			wantErrText: fmt.Sprintf("after %d resubmissions", maxDeploymentResubmissions),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			begins := 0
			begin := func(ctx context.Context) (*runtime.Poller[fakeResource], error) {
				result := tc.results[len(tc.results)-1]
				if begins < len(tc.results) {
					result = tc.results[begins]
				}
				begins++
				sendAttempts(ctx, result)
				if result.err != nil {
					return nil, result.err
				}
				return &runtime.Poller[fakeResource]{}, nil
			}

			poller, err := beginDeploymentWithResubmit(context.Background(), "deployment d1", begin, succeeded)
			if begins != tc.wantBegins {
				t.Errorf("begin called %d times, want %d", begins, tc.wantBegins)
			}
			if tc.wantSuccess {
				if err != nil || poller == nil {
					t.Fatalf("beginDeploymentWithResubmit() = (%v, %v), want a poller and no error", poller, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErrText) {
				t.Fatalf("beginDeploymentWithResubmit() error = %v, want it to contain %q", err, tc.wantErrText)
			}
		})
	}

	t.Run("returns the conflict and the wait error when the active deployment never finishes", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		begin := func(ctx context.Context) (*runtime.Poller[fakeResource], error) {
			sendAttempts(ctx, putResult{attempts: 2, outcomeUnknown: true})
			return nil, deploymentActive
		}
		running := func(context.Context) (string, error) { return "Running", nil }
		_, err := beginDeploymentWithResubmit(ctx, "deployment d1", begin, running)
		if err == nil || !errors.Is(err, deploymentActive) || !strings.Contains(err.Error(), "failed waiting for active deployment") {
			t.Fatalf("beginDeploymentWithResubmit() error = %v, want the conflict joined with the wait timeout", err)
		}
	})
}
