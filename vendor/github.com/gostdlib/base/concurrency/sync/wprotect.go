package sync

import (
	"errors"
	"sync"
	"sync/atomic"
)

// Copier is a type that can be copied. The copy must not modify the original value
// and must make a deep copy of the value. This included making copies of any pointer
// or reference types. Aka, you must make new maps and slices and copy the old values into
// the new ones before modifying them.
type Copier[T any] interface {
	// Copy returns a copy of the value. Thread-safe.
	Copy() *T
}

// WProtect provides a type that protects a value from concurrent writes and reads, but only locks for writes.
// This is useful for values that are not updated often but have heavy reads you do not wish to lock for.
// This is highly performant in comparison to a RWMutex, but it does require that you do not modify the
// value retrieved and any value you store must be a modified copy, not containing any references to
// the original value. V is the value type that is stored, C is the same value as a pointer but as a pointer
// that enforces that the value has a Copy() method. The result of the Copy() method is what is stored when Set()
// is called.
type WProtect[V any, C Copier[V]] struct {
	mu    sync.Mutex        // Protects writes, also enforces noCopy
	value atomic.Pointer[V] // Protects reads
}

// Get returns the value. The value must not be mutated in any way.
// Thread-safe.
func (m *WProtect[V, C]) Get() *V {
	return m.value.Load()
}

// Set sets the value. This value must not be the same value as the one stored.
// If you wish to retrieve a value and modify it, use GetModifySet().
// Thread-safe.
func (m *WProtect[V, C]) Set(v *V) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.value.Load() == v {
		return errors.New("setting the same value, which usually indicates a bug since you should always be making a copy")
	}

	m.value.Store(v)
	return nil
}

// GetModifySet is a function that allows you to modify the value in a thread-safe manner.
// The modifier function will receive a copy of the value currently stored by using the type's Copy() method.
func (m *WProtect[V, C]) GetModifySet(modifier func(v *V)) {
	m.mu.Lock()

	v := m.value.Load()
	n := any(v).(C).Copy()
	modifier(n)
	m.value.Store(n)

	m.mu.Unlock()
}
