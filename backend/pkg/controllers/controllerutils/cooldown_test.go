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
	checker.clock = fakeClock

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
