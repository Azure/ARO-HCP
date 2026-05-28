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
