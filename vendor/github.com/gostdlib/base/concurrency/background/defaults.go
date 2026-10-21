package background

import (
	"context"
	"sync/atomic"
)

var defaultTasks atomic.Pointer[Tasks]

// Default returns the default Tasks object. It is safe to call this from multiple goroutines.
func Default() *Tasks {
	v := defaultTasks.Load()
	if v == nil {
		t := New(context.Background())
		if defaultTasks.CompareAndSwap(nil, t) {
			return t
		}
		return defaultTasks.Load()
	}
	return v
}

// Set sets the default tasks to t. This is normally not called by user code. Instead, use options
// in init.Service().
func Set(t *Tasks) {
	defaultTasks.Store(t)
}
