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

package e2eidentities

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

func TestLeaseCredentialUsesSelectedProfileWithoutAzureLogin(t *testing.T) {
	t.Setenv("AZURE_CLIENT_ID", "")
	t.Setenv("AZURE_TENANT_ID", "")
	t.Setenv("AZURE_CLIENT_SECRET", "")
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CLUSTER_PROFILE_DIR", t.TempDir())
	profile := t.TempDir()
	for name, value := range map[string]string{
		"tenant":        "00000000-0000-0000-0000-000000000001",
		"client-id":     "00000000-0000-0000-0000-000000000002",
		"client-secret": "fake-unit-test-secret",
	} {
		if err := os.WriteFile(filepath.Join(profile, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	request := assets.LeaseRequest{
		SelectedClusterProfileDir: profile,
		AcquiredSlotState: &slots.AcquiredSlotState{Slot: slots.ExpandedSlot{
			Subscriptions: slots.ResolvedSubscriptions{E2E: slots.ResolvedSubscription{ID: "customer-subscription"}},
		}},
	}
	credential, subscription, err := leaseCredential(request)
	if err != nil {
		t.Fatalf("mounted profile must supply credentials without Azure CLI or environment credentials: %v", err)
	}
	if _, ok := credential.(*azidentity.ClientSecretCredential); !ok || subscription != "customer-subscription" {
		t.Fatalf("expected explicit selected-profile credential and E2E subscription, got %T, %q", credential, subscription)
	}
	if err := os.Remove(filepath.Join(profile, "client-secret")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := leaseCredential(request); !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "client-secret") {
		t.Fatalf("missing mounted credential must fail explicitly, got %v", err)
	}
}

func TestRunSerialPanicFailsClosed(t *testing.T) {
	previous := utilruntime.ReallyCrash
	utilruntime.ReallyCrash = false
	defer func() { utilruntime.ReallyCrash = previous }()

	err := runSerial(t.Context(), []func(context.Context) error{
		func(context.Context) error { panic("inventory unavailable") },
	})
	if err == nil || !strings.Contains(err.Error(), "panicked: inventory unavailable") {
		t.Fatalf("panic must become admission error, got %v", err)
	}
}

func TestRunOperationPreservesCrashPolicy(t *testing.T) {
	previous := utilruntime.ReallyCrash
	utilruntime.ReallyCrash = true
	defer func() { utilruntime.ReallyCrash = previous }()
	defer func() {
		if value := recover(); value == nil {
			t.Error("ReallyCrash=true must propagate the panic")
		}
	}()
	_ = runOperation(context.Background(), func(context.Context) error { panic("crash policy") })
}

func TestRunSerialCancellation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var active atomic.Int32
		var started atomic.Int32
		operations := make([]func(context.Context) error, 100)
		for i := range operations {
			operations[i] = func(ctx context.Context) error {
				started.Add(1)
				active.Add(1)
				defer active.Add(-1)
				<-ctx.Done()
				return ctx.Err()
			}
		}
		done := make(chan error, 1)
		go func() { done <- runSerial(ctx, operations) }()
		synctest.Wait()
		if active.Load() != 1 {
			t.Fatalf("expected one blocked operation, got %d", active.Load())
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) || active.Load() != 0 {
			t.Fatalf("cancellation failed: err=%v active=%d", err, active.Load())
		}
		if started.Load() != 1 {
			t.Fatalf("started more operations after cancellation: %d", started.Load())
		}
	})
}

func TestRunSerialRunsEveryOperationAndJoinsErrors(t *testing.T) {
	t.Parallel()

	calls := 0
	expectedErr := errors.New("operation failed")
	var operations []func(context.Context) error
	for _, err := range []error{nil, expectedErr, nil} {
		operations = append(operations, func(context.Context) error {
			calls++
			return err
		})
	}
	err := runSerial(context.Background(), operations)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected joined error to preserve operation failure, got %v", err)
	}
	if calls != len(operations) {
		t.Fatalf("expected all operations to run, got %d of %d", calls, len(operations))
	}
}

func TestRetryableAdmissionError(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil"},
		{name: "throttled", err: &azcore.ResponseError{StatusCode: http.StatusTooManyRequests}, want: true},
		{name: "server error", err: &azcore.ResponseError{StatusCode: http.StatusInternalServerError}, want: true},
		{name: "unavailable", err: &azcore.ResponseError{StatusCode: http.StatusServiceUnavailable}, want: true},
		{name: "gateway timeout", err: &azcore.ResponseError{StatusCode: http.StatusGatewayTimeout}, want: true},
		{name: "unauthorized", err: &azcore.ResponseError{StatusCode: http.StatusUnauthorized}},
		{name: "forbidden", err: &azcore.ResponseError{StatusCode: http.StatusForbidden}},
		{name: "not found", err: &azcore.ResponseError{StatusCode: http.StatusNotFound}},
		{name: "conflict", err: &azcore.ResponseError{StatusCode: http.StatusConflict}},
		{name: "deadline", err: context.DeadlineExceeded, want: true},
		{name: "canceled", err: context.Canceled},
		{name: "network timeout", err: &net.DNSError{IsTimeout: true}, want: true},
		{name: "temporary network error", err: &net.DNSError{IsTemporary: true}, want: true},
		{name: "permanent network error", err: &net.DNSError{IsNotFound: true}},
		{name: "connection reset", err: &net.OpError{Op: "read", Err: syscall.ECONNRESET}, want: true},
		{name: "connection refused", err: syscall.ECONNREFUSED, want: true},
		{name: "broken pipe", err: syscall.EPIPE, want: true},
		{name: "EOF", err: io.EOF, want: true},
		{name: "unexpected EOF", err: io.ErrUnexpectedEOF, want: true},
		{name: "other", err: errors.New("invalid operation")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := retryableAdmissionError(test.err); got != test.want {
				t.Fatalf("retryableAdmissionError(%v) = %v, want %v", test.err, got, test.want)
			}
			if test.err != nil {
				if got := retryableAdmissionError(fmt.Errorf("delete failed: %w", test.err)); got != test.want {
					t.Fatalf("wrapped retryableAdmissionError(%v) = %v, want %v", test.err, got, test.want)
				}
			}
		})
	}
}

func TestRunOperationWithRetry(t *testing.T) {
	t.Parallel()
	transientErr := &azcore.ResponseError{StatusCode: http.StatusServiceUnavailable}
	for _, test := range []struct {
		name      string
		failures  int
		err       error
		wantCalls int
		wantErr   bool
	}{
		{name: "success", wantCalls: 1},
		{name: "transient recovery", failures: 2, err: transientErr, wantCalls: 3},
		{name: "exhaustion", failures: admissionOperationBackoff.Steps, err: transientErr, wantCalls: admissionOperationBackoff.Steps, wantErr: true},
		{name: "fatal", failures: 1, err: &azcore.ResponseError{StatusCode: http.StatusForbidden}, wantCalls: 1, wantErr: true},
		{name: "not found", failures: 1, err: &azcore.ResponseError{StatusCode: http.StatusNotFound}, wantCalls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var attempts []time.Time
				var contexts []context.Context
				operationDeadline := time.Now().Add(admissionOperationTimeout)
				err := runOperationWithRetry(t.Context(), admissionOperationTimeout, func(ctx context.Context) error {
					remaining := time.Until(operationDeadline)
					wantTimeout := min(remaining, max(admissionOperationMinAttemptTimeout, remaining/time.Duration(admissionOperationBackoff.Steps-len(attempts))))
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) != wantTimeout {
						t.Fatalf("attempt %d must use its share of the remaining budget: got %s, want %s", len(attempts)+1, time.Until(deadline), wantTimeout)
					}
					attempts = append(attempts, time.Now())
					contexts = append(contexts, ctx)
					if len(attempts) <= test.failures {
						return fmt.Errorf("delete failed: %w", test.err)
					}
					return nil
				})
				if (err != nil) != test.wantErr || (test.wantErr && !errors.Is(err, test.err)) {
					t.Fatalf("expected error=%v preserving %v, got %v", test.wantErr, test.err, err)
				}
				if test.name == "exhaustion" && !wait.Interrupted(err) {
					t.Fatalf("expected retry exhaustion, got %v", err)
				}
				if len(attempts) != test.wantCalls {
					t.Fatalf("expected %d attempts, got %d", test.wantCalls, len(attempts))
				}
				backoff := admissionOperationBackoff
				backoff.Jitter = 0
				for index := 1; index < len(attempts); index++ {
					if elapsed, minimum := attempts[index].Sub(attempts[index-1]), backoff.Step(); elapsed < minimum {
						t.Fatalf("retry %d waited %s, expected at least %s", index, elapsed, minimum)
					}
				}
				for _, ctx := range contexts {
					if !errors.Is(ctx.Err(), context.Canceled) {
						t.Fatalf("attempt context was not canceled: %v", ctx.Err())
					}
				}
			})
		})
	}
}

func TestRunOperationWithRetryAttemptTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		var firstContext context.Context
		err := runOperationWithRetry(t.Context(), admissionOperationTimeout, func(ctx context.Context) error {
			calls++
			if calls == 1 {
				firstContext = ctx
				<-ctx.Done()
				return fmt.Errorf("ARM delete timed out: %w", ctx.Err())
			}
			return ctx.Err()
		})
		if err != nil || calls != 2 || !errors.Is(firstContext.Err(), context.DeadlineExceeded) {
			t.Fatalf("expected timed-out attempt to recover on retry: calls=%d err=%v", calls, err)
		}
	})
}

func TestRunOperationWithRetryParentDone(t *testing.T) {
	t.Parallel()
	for _, duringBackoff := range []bool{false, true} {
		t.Run(fmt.Sprintf("duringBackoff=%v", duringBackoff), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				phaseTimeout := time.Second
				wantErr := context.DeadlineExceeded
				if duringBackoff {
					phaseTimeout = admissionOperationTimeout
					wantErr = context.Canceled
				}
				ctx, cancel := context.WithTimeout(t.Context(), phaseTimeout)
				defer cancel()
				if duringBackoff {
					timer := time.AfterFunc(time.Second, cancel)
					defer timer.Stop()
				}
				calls := 0
				start := time.Now()
				err := runOperationWithRetry(ctx, admissionOperationTimeout, func(context.Context) error {
					calls++
					if duringBackoff {
						return &azcore.ResponseError{StatusCode: http.StatusTooManyRequests}
					}
					<-ctx.Done()
					return ctx.Err()
				})
				if err != ctx.Err() || !errors.Is(err, wantErr) || calls != 1 || time.Since(start) != time.Second {
					t.Fatalf("expected immediate parent context handling: calls=%d elapsed=%s err=%v", calls, time.Since(start), err)
				}
			})
		})
	}
}

func TestRunSerialReservesBudgetForRemainingOperations(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		start := time.Now()
		completed := false
		err := runSerial(ctx, []func(context.Context) error{
			func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			},
			func(ctx context.Context) error {
				completed = ctx.Err() == nil
				return ctx.Err()
			},
		})
		if !errors.Is(err, context.DeadlineExceeded) || !completed || ctx.Err() != nil || time.Since(start) > 10*time.Second {
			t.Fatalf("slow delete must preserve the next operation's phase budget: completed=%v elapsed=%s phaseErr=%v err=%v", completed, time.Since(start), ctx.Err(), err)
		}
	})
}

func TestRunSerialAttemptTimeoutFloor(t *testing.T) {
	t.Parallel()
	for _, phaseTimeout := range []time.Duration{10 * time.Second, admissionOperationMinAttemptTimeout - time.Second} {
		t.Run(phaseTimeout.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), phaseTimeout)
				defer cancel()
				phaseDeadline, _ := ctx.Deadline()
				calls := 0
				operations := make([]func(context.Context) error, 100)
				for index := range operations {
					operations[index] = func(ctx context.Context) error {
						if calls != index {
							t.Fatalf("expected operation %d to run next, got %d", calls, index)
						}
						calls++
						deadline, ok := ctx.Deadline()
						wantTimeout := min(admissionOperationMinAttemptTimeout, time.Until(phaseDeadline))
						if !ok || time.Until(deadline) != wantTimeout {
							t.Fatalf("operation %d must get a floored attempt capped by the phase deadline: got %s, want %s", index, time.Until(deadline), wantTimeout)
						}
						if index == 0 {
							select {
							case <-time.After(time.Second):
							case <-ctx.Done():
								return ctx.Err()
							}
						}
						return nil
					}
				}
				if err := runSerial(ctx, operations); err != nil || calls != len(operations) {
					t.Fatalf("expected all %d operations to complete once: calls=%d err=%v", len(operations), calls, err)
				}
			})
		})
	}
}

func TestRunOperationWithRetryBelowFloor(t *testing.T) {
	t.Parallel()
	shortBudget := admissionOperationMinAttemptTimeout - time.Second
	for _, test := range []struct {
		name             string
		phaseTimeout     time.Duration
		operationTimeout time.Duration
	}{
		{name: "operation budget", phaseTimeout: admissionOperationTimeout, operationTimeout: shortBudget},
		{name: "phase budget", phaseTimeout: shortBudget, operationTimeout: admissionOperationTimeout},
		{name: "nearly exhausted phase", phaseTimeout: 100 * time.Millisecond, operationTimeout: admissionOperationTimeout},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), test.phaseTimeout)
				defer cancel()
				transientErr := &azcore.ResponseError{StatusCode: http.StatusServiceUnavailable}
				calls := 0
				start := time.Now()
				err := runOperationWithRetry(ctx, test.operationTimeout, func(ctx context.Context) error {
					calls++
					deadline, ok := ctx.Deadline()
					wantTimeout := min(test.phaseTimeout, test.operationTimeout)
					if !ok || time.Until(deadline) != wantTimeout {
						t.Fatalf("best-effort attempt must get the entire remaining budget: got %s, want %s", time.Until(deadline), wantTimeout)
					}
					return transientErr
				})
				if !errors.Is(err, transientErr) || calls != 1 || time.Since(start) != 0 {
					t.Fatalf("expected one best-effort attempt without retry backoff: calls=%d elapsed=%s err=%v", calls, time.Since(start), err)
				}
			})
		})
	}
}

func TestRunOperationWithRetryStopsWhenAttemptConsumesBudget(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		transientErr := &azcore.ResponseError{StatusCode: http.StatusServiceUnavailable}
		workTime := admissionOperationMinAttemptTimeout - 100*time.Millisecond
		calls := 0
		start := time.Now()
		err := runOperationWithRetry(t.Context(), admissionOperationMinAttemptTimeout+3*time.Second, func(ctx context.Context) error {
			calls++
			if calls == 1 {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) != admissionOperationMinAttemptTimeout {
					t.Fatalf("first attempt must receive the minimum timeout, got %s", time.Until(deadline))
				}
				time.Sleep(workTime)
			}
			return transientErr
		})
		if !errors.Is(err, transientErr) || calls != 1 || time.Since(start) != workTime {
			t.Fatalf("expected no backoff or retry after the attempt leaves less than the minimum budget: calls=%d elapsed=%s err=%v", calls, time.Since(start), err)
		}
	})
}

func TestRunOperationWithRetryParentCancellationPrecedence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "success"},
		{name: "not found", err: &azcore.ResponseError{StatusCode: http.StatusNotFound}},
		{name: "fatal", err: &azcore.ResponseError{StatusCode: http.StatusForbidden}},
		{name: "retryable", err: &azcore.ResponseError{StatusCode: http.StatusServiceUnavailable}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			err := runOperationWithRetry(ctx, admissionOperationTimeout, func(context.Context) error {
				calls++
				cancel()
				return test.err
			})
			if err != context.Canceled || calls != 1 {
				t.Fatalf("parent cancellation must take precedence without retrying: calls=%d err=%v", calls, err)
			}
		})
	}
}
