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

package holmes

import "sync/atomic"

type ConcurrencyLimiter struct {
	max     int32
	current atomic.Int32
}

func NewConcurrencyLimiter(max int) *ConcurrencyLimiter {
	return &ConcurrencyLimiter{max: int32(max)}
}

func (l *ConcurrencyLimiter) Acquire() bool {
	for {
		cur := l.current.Load()
		if cur >= l.max {
			return false
		}
		if l.current.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

func (l *ConcurrencyLimiter) Release() {
	for {
		cur := l.current.Load()
		if cur <= 0 {
			return
		}
		if l.current.CompareAndSwap(cur, cur-1) {
			return
		}
	}
}

func (l *ConcurrencyLimiter) Current() int {
	return int(l.current.Load())
}
