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

package statuscontrollers

import (
	"strings"
	"time"

	"k8s.io/utils/clock"
	"k8s.io/utils/lru"
)

// firstObservedBadCacheCapacity is sized like the other LRU caches in this
// codebase (e.g. the deletion-timestamp caches) — large enough that an
// operator with tens of thousands of controllers won't churn through it.
const firstObservedBadCacheCapacity = 50000

// firstObservedBadCache records, in memory, the time at which each
// controller was first observed in a "bad" state for which the controller
// itself has not supplied a LastTransitionTime — namely a missing
// Degraded condition. (A controller that has reported any Degraded status,
// including Unknown, has its own LastTransitionTime, so it does not need
// this cache; collectDegradedConditions passes those through and calls
// forget on this cache for them.)
//
// The aggregator uses this timestamp as the LastTransitionTime on the
// synthetic Degraded=True SourcedCondition it feeds into UnionCondition,
// so the same inertia window that protects against flapping True
// conditions also protects against not-yet-reported controllers.
//
// The cache entry is dropped as soon as the controller reports any
// Degraded condition, so a controller whose condition disappears later
// starts its inertia fresh rather than tripping on a stale entry.
type firstObservedBadCache struct {
	clock clock.PassiveClock
	cache *lru.Cache
}

// newFirstObservedBadCache returns a cache bound to the given clock. The
// clock is the source of "now" for new observations.
func newFirstObservedBadCache(clock clock.PassiveClock) *firstObservedBadCache {
	return &firstObservedBadCache{
		clock: clock,
		cache: lru.New(firstObservedBadCacheCapacity),
	}
}

// observe returns the first-observed-bad time for the given controller. If
// no entry exists, the current time is recorded and returned, so callers
// effectively get "now or the previously-recorded earlier time".
//
// Pass the controller's resource ID string as the key; it is lowercased
// internally to match the rest of the codebase's case-insensitive ID
// conventions.
func (c *firstObservedBadCache) observe(controllerResourceID string) time.Time {
	key := strings.ToLower(controllerResourceID)
	if v, ok := c.cache.Get(key); ok {
		return v.(time.Time)
	}
	now := c.clock.Now()
	c.cache.Add(key, now)
	return now
}

// forget drops any previously-recorded bad observation for the given
// controller. Call this when the controller reports a definite Degraded
// status (True or False) so that a later flap back to Unknown starts a
// fresh inertia window.
func (c *firstObservedBadCache) forget(controllerResourceID string) {
	c.cache.Remove(strings.ToLower(controllerResourceID))
}
