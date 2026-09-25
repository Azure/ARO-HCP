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
	"strings"
	"testing"
	"time"
)

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

// TestPollUntilReady_SucceedsWhenConditionLandsAfterLastPoll covers the ARO-26775 near-miss: the
// condition becomes true between the final poll and the deadline. Without the post-deadline
// check this reports a timeout on work that finished inside the budget.
func TestPollUntilReady_SucceedsWhenConditionLandsAfterLastPoll(t *testing.T) {
	// A 400ms budget with a 250ms interval polls at 0ms and 250ms; the next tick would land at
	// 500ms, past the deadline, so the loop stops looking at 250ms. Becoming ready at 320ms
	// falls in that blind window -- the same shape as a target version that appeared 22s before
	// a 45m deadline whose preceding poll had fired 2m earlier. Success here is reachable only
	// through the post-deadline check.
	const (
		timeout  = 400 * time.Millisecond
		interval = 250 * time.Millisecond
	)
	readyAt := time.Now().Add(320 * time.Millisecond)
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
			if time.Now().Before(readyAt) {
				return fmt.Errorf("not ready")
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("expected the post-deadline check to observe the condition, got: %v", err)
	}
	// Two in-loop polls that saw "not ready", then the post-deadline check that saw success.
	if checks != 3 {
		t.Fatalf("expected 3 checks (2 in-loop polls + 1 post-deadline), got %d", checks)
	}
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
