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
