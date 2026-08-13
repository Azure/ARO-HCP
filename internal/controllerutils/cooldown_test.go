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

package controllerutils

import (
	"context"
	"testing"
	"time"

	clocktesting "k8s.io/utils/clock/testing"
)

func TestTimeBasedCooldownChecker_RepeatedFalseDoesNotPreventTrue(t *testing.T) {
	startTime := time.Now()
	fakeClock := clocktesting.NewFakePassiveClock(startTime)
	checker := NewTimeBasedCooldownChecker(5 * time.Second)
	checker.SetClock(fakeClock)

	ctx := context.Background()
	key := "test-key"

	// First call should return true (no prior entry).
	if !checker.CanSync(ctx, key) {
		t.Fatal("expected first CanSync to return true")
	}

	// Requests at 1s, 2s, 3s, 4s, 5s should all return false (within cooldown).
	for i := 1; i <= 5; i++ {
		fakeClock.SetTime(startTime.Add(time.Duration(i) * time.Second))
		result := checker.CanSync(ctx, key)
		if result {
			t.Fatalf("expected CanSync to return false at second %d, got true", i)
		}
	}

	// At 6s the cooldown has elapsed; CanSync should return true again.
	fakeClock.SetTime(startTime.Add(6 * time.Second))
	if !checker.CanSync(ctx, key) {
		t.Fatal("expected CanSync to return true after cooldown expired")
	}
}

func TestSettableCooldownChecker_NoKeyAlwaysAllowed(t *testing.T) {
	checker := NewSettableCooldownChecker()
	ctx := context.Background()

	if !checker.CanSync(ctx, "unknown-key") {
		t.Fatal("expected CanSync to return true for key with no cooldown set")
	}
}

func TestSettableCooldownChecker_SetCooldownBlocksThenExpires(t *testing.T) {
	startTime := time.Now()
	fakeClock := clocktesting.NewFakePassiveClock(startTime)
	checker := NewSettableCooldownChecker()
	checker.SetClock(fakeClock)

	ctx := context.Background()
	key := "test-key"

	checker.SetCooldown(key, 30*time.Second)

	if checker.CanSync(ctx, key) {
		t.Fatal("expected CanSync to return false within cooldown window")
	}

	fakeClock.SetTime(startTime.Add(29 * time.Second))
	if checker.CanSync(ctx, key) {
		t.Fatal("expected CanSync to return false just before cooldown expires")
	}

	fakeClock.SetTime(startTime.Add(31 * time.Second))
	if !checker.CanSync(ctx, key) {
		t.Fatal("expected CanSync to return true after cooldown expired")
	}
}

func TestSettableCooldownChecker_TimeUntilReady(t *testing.T) {
	startTime := time.Now()
	fakeClock := clocktesting.NewFakePassiveClock(startTime)
	checker := NewSettableCooldownChecker()
	checker.SetClock(fakeClock)

	key := "test-key"

	if d := checker.TimeUntilReady(key); d != 0 {
		t.Fatalf("expected 0 for key with no cooldown, got %v", d)
	}

	checker.SetCooldown(key, 60*time.Second)

	if d := checker.TimeUntilReady(key); d != 60*time.Second {
		t.Fatalf("expected 60s, got %v", d)
	}

	fakeClock.SetTime(startTime.Add(45 * time.Second))
	if d := checker.TimeUntilReady(key); d != 15*time.Second {
		t.Fatalf("expected 15s, got %v", d)
	}

	fakeClock.SetTime(startTime.Add(61 * time.Second))
	if d := checker.TimeUntilReady(key); d != 0 {
		t.Fatalf("expected 0 after expiry, got %v", d)
	}
}

func TestSettableCooldownChecker_OverwriteCooldown(t *testing.T) {
	startTime := time.Now()
	fakeClock := clocktesting.NewFakePassiveClock(startTime)
	checker := NewSettableCooldownChecker()
	checker.SetClock(fakeClock)

	ctx := context.Background()
	key := "test-key"

	checker.SetCooldown(key, 10*time.Second)
	checker.SetCooldown(key, 60*time.Second)

	fakeClock.SetTime(startTime.Add(11 * time.Second))
	if checker.CanSync(ctx, key) {
		t.Fatal("expected CanSync to return false; second SetCooldown should overwrite the first")
	}

	fakeClock.SetTime(startTime.Add(61 * time.Second))
	if !checker.CanSync(ctx, key) {
		t.Fatal("expected CanSync to return true after the overwritten cooldown expires")
	}
}
