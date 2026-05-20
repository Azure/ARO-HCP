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

// Package controllerutils holds controller helpers shared across services
// (backend, kube-applier, ...) that all build Cosmos-backed informer-driven
// controllers and want consistent cadence/gating behavior.
//
// The cooldown gate exposed here was originally
// backend/pkg/controllers/controllerutils' TimeBasedCooldownChecker; it
// lives in internal/ so kube-applier can use the same implementation
// without re-importing the backend module.
package controllerutils

import (
	"context"
	"time"

	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/lru"
)

// CooldownChecker decides whether a key may be (re-)queued.
//
// Implementations must be safe to call concurrently — informer event
// handlers, periodic resyncs, and worker goroutines may all consult the
// same checker.
//
// CanSync takes a context so implementations may emit logs or use
// context-bound services; the time-based variant ignores it. A true return
// is taken as "the caller WILL sync this key" and the implementation is
// free to record state (e.g. stamp a next-allowed time) on that basis.
type CooldownChecker interface {
	CanSync(ctx context.Context, key any) bool
}

// TimeBasedCooldownChecker is a fixed-interval cooldown gate: after
// CanSync returns true for a key, subsequent calls for the same key
// return false until cooldownDuration has elapsed since the allowed call.
//
// The next-exec map is an LRU rather than an unbounded map so that a
// long-running process whose keys come and go does not leak memory. 1M
// entries is far above any realistic management cluster's resource count.
type TimeBasedCooldownChecker struct {
	clock            utilsclock.PassiveClock
	cooldownDuration time.Duration
	nextExecTime     *lru.Cache
}

// NewTimeBasedCooldownChecker constructs a checker bound to the real
// wall-clock and a 1M-entry LRU. Tests should call SetClock to inject a
// fake clock.
func NewTimeBasedCooldownChecker(cooldownDuration time.Duration) *TimeBasedCooldownChecker {
	return &TimeBasedCooldownChecker{
		clock:            utilsclock.RealClock{},
		cooldownDuration: cooldownDuration,
		nextExecTime:     lru.New(1000000),
	}
}

// SetClock substitutes the time source used to evaluate the cooldown.
// Intended for tests; production code should use the real clock from the
// constructor.
func (c *TimeBasedCooldownChecker) SetClock(clock utilsclock.PassiveClock) {
	c.clock = clock
}

// CanSync stamps now+cooldownDuration on a true return so subsequent calls
// within the cooldown window return false. A key with no record is always
// allowed. ctx is part of the CooldownChecker interface but unused here.
func (c *TimeBasedCooldownChecker) CanSync(_ context.Context, key any) bool {
	now := c.clock.Now()

	nextExecTime, ok := c.nextExecTime.Get(key)
	if !ok || now.After(nextExecTime.(time.Time)) {
		c.nextExecTime.Add(key, now.Add(c.cooldownDuration))
		return true
	}
	return false
}
