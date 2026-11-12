package worker

import (
	"context"
	"sync/atomic"
)

var defaultPool atomic.Pointer[Pool]

// Default returns the default pool. If it has not been set, it will be created.
func Default() *Pool {
	dp := defaultPool.Load()
	if dp == nil {
		p, err := New(context.Background(), "defaultPool")
		if err != nil {
			panic(err) // Should not happen
		}
		if !defaultPool.CompareAndSwap(nil, p) {
			p.Close(context.Background())
		}
	}
	return defaultPool.Load()
}

// Set sets the default pool to the given pool. This can be used to override the default pool.
// However, this is usually used only internally. If using init.Service(), use the appropriate call option
// to set the default pool.
func Set(p *Pool) {
	defaultPool.Store(p)
}
