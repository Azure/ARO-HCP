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

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConcurrencyLimiter_AcquireRelease(t *testing.T) {
	limiter := NewConcurrencyLimiter(2)

	assert.True(t, limiter.Acquire(), "first acquire should succeed")
	assert.Equal(t, 1, limiter.Current())

	assert.True(t, limiter.Acquire(), "second acquire should succeed")
	assert.Equal(t, 2, limiter.Current())

	assert.False(t, limiter.Acquire(), "third acquire should fail at capacity")
	assert.Equal(t, 2, limiter.Current())

	limiter.Release()
	assert.Equal(t, 1, limiter.Current())

	assert.True(t, limiter.Acquire(), "acquire after release should succeed")
	assert.Equal(t, 2, limiter.Current())
}

func TestConcurrencyLimiter_ZeroMax(t *testing.T) {
	limiter := NewConcurrencyLimiter(0)
	assert.False(t, limiter.Acquire(), "zero-max limiter should reject all")
}

func TestConcurrencyLimiter_Concurrent(t *testing.T) {
	const max = 10
	const goroutines = 100

	limiter := NewConcurrencyLimiter(max)
	barrier := make(chan struct{})
	acquiredCount := make(chan int, goroutines)

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			<-barrier
			if limiter.Acquire() {
				acquiredCount <- 1
				limiter.Release()
			} else {
				acquiredCount <- 0
			}
		})
	}

	close(barrier)
	wg.Wait()
	close(acquiredCount)

	total := 0
	for v := range acquiredCount {
		total += v
	}

	assert.Equal(t, 0, limiter.Current(), "all should be released")
	assert.LessOrEqual(t, total, goroutines, "should not exceed goroutine count")
	assert.Greater(t, total, 0, "at least some should have acquired")
}
