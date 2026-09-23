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

package informerutils

import (
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

// JitterFunc maps a base duration to a jittered duration. It is injected into
// the Cosmos-backed watchers so production code randomizes watch lifetimes
// while tests can supply a deterministic no-op.
type JitterFunc func(duration time.Duration) time.Duration

// jitterMaxFactor is the upper bound of the additive jitter, expressed as a
// fraction of the base duration. 0.3 => up to +30% (e.g. a 30-minute base
// yields a value in [30m, 39m)).
const jitterMaxFactor = 0.3

// defaultJitter spreads watch expiries so informers backed by the same relist
// duration do not re-List against Cosmos DB in lockstep (a "thundering herd").
//
// The jitter is ADDITIVE and one-sided: result = duration + rand[0, 0.3*duration),
// so the value never drops below the configured base and never exceeds +30%.
// Because a fresh value is drawn every watch cycle, informers that momentarily
// align drift apart again on their next relist instead of staying synchronized.
//
// wait.Jitter(d, maxFactor) returns d + time.Duration(rand.Float64()*maxFactor*d),
// which is exactly the additive form we want.
func defaultJitter(duration time.Duration) time.Duration {
	return wait.Jitter(duration, jitterMaxFactor)
}
