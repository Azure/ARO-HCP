// Package shardmap is a re-done version of Josh Baker's shardmap package. It switches out the hash
// from xxhash to maphash, uses generics and has a few other minor changes. It is a thread-safe.
package shardmap

import (
	"hash/maphash"
	"iter"
	"runtime"
	"sync"

	rhh "github.com/gostdlib/base/concurrency/sync/internal/shardmap/hashmap"
)

// Map is a hashmap. Like map[string]interface{}, but sharded and thread-safe.
type Map[K comparable, V any] struct {
	// IsEqual is a function that determines if two values are equal. This is not required unless using
	// CompareAndSwap or CompareAndDelete.
	IsEqual func(old, new V) bool
	init    sync.Once
	cap     int
	shards  int
	mus     []sync.RWMutex
	maps    []*rhh.Map[K, V]

	seed maphash.Seed
}

// New returns a new hashmap with the specified capacity. This function is only
// needed when you must define a minimum capacity, otherwise just use:
//
//	var m shardmap.Map
func New[K comparable, V any](cap int) *Map[K, V] {
	return &Map[K, V]{cap: cap}
}

// Clear out all values from map
func (m *Map[K, V]) Clear() {
	m.initDo()
	for i := 0; i < m.shards; i++ {
		m.mus[i].Lock()
		m.maps[i] = rhh.New[K, V](m.cap / m.shards)
		m.mus[i].Unlock()
	}
}

// Set assigns a value to a key.
// Returns the previous value, or false when no value was assigned.
func (m *Map[K, V]) Set(key K, value V) (prev V, replaced bool) {
	m.initDo()
	shard := m.choose(key)
	m.mus[shard].Lock()
	prev, replaced = m.maps[shard].Set(key, value)
	m.mus[shard].Unlock()
	return prev, replaced
}

// CompareAndSwap assigns a value to a key if the previous value is equal to
// old. If the key doesn't exist and old is the zero value, then the key will be created.
// Must have set Map.IsEqual or this will panic.
func (m *Map[K, V]) CompareAndSwap(k K, old, new V) (swapped bool) {
	m.initDo()
	if m.IsEqual == nil {
		panic("shardmap.Map.IsEqual must be set to use CompareAndSwap")
	}

	shard := m.choose(k)
	m.mus[shard].Lock()
	defer m.mus[shard].Unlock()

	prev, _ := m.maps[shard].Set(k, new)
	if m.IsEqual(prev, old) {
		return true
	}
	m.maps[shard].Set(k, prev)
	return false
}

// Get returns a value for a key.
// Returns false when no value has been assign for key.
func (m *Map[K, V]) Get(key K) (value V, ok bool) {
	m.initDo()
	shard := m.choose(key)
	m.mus[shard].RLock()
	value, ok = m.maps[shard].Get(key)
	m.mus[shard].RUnlock()
	return value, ok
}

// Delete deletes a value for a key.
// Returns the deleted value, or false when no value was assigned.
func (m *Map[K, V]) Delete(key K) (prev V, deleted bool) {
	m.initDo()
	shard := m.choose(key)
	m.mus[shard].Lock()
	prev, deleted = m.maps[shard].Delete(key)
	m.mus[shard].Unlock()
	return prev, deleted
}

// CompareAndDelete deletes a key/value if the value is equal to
// old. If the key doesn't exist this will return true. Must have set Map.IsEqual
// or this will panic.
func (m *Map[K, V]) CompareAndDelete(k K, old V) (deleted bool) {
	m.initDo()
	if m.IsEqual == nil {
		panic("shardmap.Map.IsEqual must be set to use CompareAndSwap")
	}

	shard := m.choose(k)
	m.mus[shard].Lock()
	defer m.mus[shard].Unlock()

	prev, deleted := m.maps[shard].Delete(k)
	if !deleted { // This means it didn't exist.
		return true
	}
	if m.IsEqual(prev, old) {
		return true
	}
	// It wasn't equal, so we need to put it back.
	m.maps[shard].Set(k, prev)
	return false
}

// Len returns the number of values in map.
func (m *Map[K, V]) Len() int {
	m.initDo()
	var len int
	for i := 0; i < m.shards; i++ {
		m.mus[i].Lock()
		len += m.maps[i].Len()
		m.mus[i].Unlock()
	}
	return len
}

// All returns a sequence of all key/values. It is not safe to call
// Set or Delete while iterating.
func (m *Map[K, V]) All() iter.Seq2[K, V] {
	m.initDo()
	return func(yield func(K, V) bool) {
		for i := 0; i < m.shards; i++ {
			for k, v := range m.maps[i].All() {
				if !yield(k, v) {
					return
				}
			}
		}
	}
}

func (m *Map[K, V]) choose(key K) int {
	return int(maphash.Comparable(m.seed, key) & uint64(m.shards-1))
}

func (m *Map[K, V]) initDo() {
	m.init.Do(func() {
		m.shards = 1
		for m.shards < runtime.NumCPU()*16 {
			m.shards *= 2
		}
		scap := m.cap / m.shards
		m.mus = make([]sync.RWMutex, m.shards)
		m.maps = make([]*rhh.Map[K, V], m.shards)
		for i := 0; i < len(m.maps); i++ {
			m.maps[i] = rhh.New[K, V](scap)
		}
		m.seed = maphash.MakeSeed()
	})
}
