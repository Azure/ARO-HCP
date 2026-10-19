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

package azureutil

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

func TestRunWithWorkersPanicFailsClosed(t *testing.T) {
	previous := utilruntime.ReallyCrash
	utilruntime.ReallyCrash = false
	defer func() { utilruntime.ReallyCrash = previous }()

	err := RunWithWorkers(t.Context(), []func(context.Context) error{
		func(context.Context) error { panic("inventory unavailable") },
	}, 1)
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

func TestRunWithWorkersCancellation(t *testing.T) {
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
		go func() { done <- RunWithWorkers(ctx, operations, 1) }()
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

func TestRunWithWorkersRunsEveryOperationAndJoinsErrors(t *testing.T) {
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
	err := RunWithWorkers(context.Background(), operations, 1)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected joined error to preserve operation failure, got %v", err)
	}
	if calls != len(operations) {
		t.Fatalf("expected all operations to run, got %d of %d", calls, len(operations))
	}
}

func TestRunWithWorkersBoundsConcurrency(t *testing.T) {
	t.Parallel()
	for _, workers := range []int{0, 1, 2, 5} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				want := max(workers, 1)
				var active, peak, completed atomic.Int32
				operations := make([]func(context.Context) error, 20)
				for i := range operations {
					operations[i] = func(ctx context.Context) error {
						current := active.Add(1)
						defer active.Add(-1)
						for {
							seen := peak.Load()
							if current <= seen || peak.CompareAndSwap(seen, current) {
								break
							}
						}
						time.Sleep(time.Second)
						completed.Add(1)
						return nil
					}
				}
				if err := RunWithWorkers(context.Background(), operations, workers); err != nil {
					t.Fatal(err)
				}
				if completed.Load() != int32(len(operations)) {
					t.Fatalf("expected every operation to complete, got %d", completed.Load())
				}
				if peak.Load() != int32(want) {
					t.Fatalf("expected peak concurrency %d, got %d", want, peak.Load())
				}
			})
		})
	}
}

func TestRunWithWorkersCancellationStopsLaunching(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var started atomic.Int32
		operations := make([]func(context.Context) error, 100)
		for i := range operations {
			operations[i] = func(ctx context.Context) error {
				started.Add(1)
				<-ctx.Done()
				return ctx.Err()
			}
		}
		done := make(chan error, 1)
		go func() { done <- RunWithWorkers(ctx, operations, 2) }()
		synctest.Wait()
		if started.Load() != 2 {
			t.Fatalf("expected two blocked operations, got %d", started.Load())
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation error, got %v", err)
		}
		if started.Load() != 2 {
			t.Fatalf("started operations after cancellation: %d", started.Load())
		}
	})
}

func TestRunWithWorkersKeepsGoingAfterFailure(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	failure := errors.New("delete failed")
	operations := make([]func(context.Context) error, 10)
	for i := range operations {
		operations[i] = func(context.Context) error {
			calls.Add(1)
			if i == 3 {
				return failure
			}
			return nil
		}
	}
	err := RunWithWorkers(context.Background(), operations, 3)
	if !errors.Is(err, failure) || calls.Load() != int32(len(operations)) {
		t.Fatalf("a failing operation must not stop the others: err=%v calls=%d", err, calls.Load())
	}
}
