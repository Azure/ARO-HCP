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

package cijoboutcomes

import (
	"container/list"
	"sync"
	"time"
)

// ttlCache is FIFO and non-sliding. Reads and duplicate inserts do not extend
// acceptance visibility or retain frequently rediscovered metadata forever.
type ttlCache[K comparable, V any] struct {
	mu       sync.Mutex
	entries  map[K]*list.Element
	order    *list.List
	capacity int
	ttl      time.Duration
	now      func() time.Time
}

type cacheEntry[K comparable, V any] struct {
	key   K
	value V
	at    time.Time
}

func newTTLCache[K comparable, V any](capacity int, ttl time.Duration) *ttlCache[K, V] {
	return &ttlCache[K, V]{entries: map[K]*list.Element{}, order: list.New(), capacity: capacity, ttl: ttl, now: time.Now}
}

func (c *ttlCache[K, V]) prune() {
	for first := c.order.Front(); first != nil; first = c.order.Front() {
		entry := first.Value.(cacheEntry[K, V])
		if c.now().Before(entry.at.Add(c.ttl)) {
			break
		}
		delete(c.entries, entry.key)
		c.order.Remove(first)
	}
}

func (c *ttlCache[K, V]) get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune()
	if element, ok := c.entries[key]; ok {
		return element.Value.(cacheEntry[K, V]).value, true
	}
	var zero V
	return zero, false
}

func (c *ttlCache[K, V]) put(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune()
	if _, ok := c.entries[key]; ok {
		return
	}
	if c.order.Len() >= c.capacity {
		first := c.order.Front()
		delete(c.entries, first.Value.(cacheEntry[K, V]).key)
		c.order.Remove(first)
	}
	c.entries[key] = c.order.PushBack(cacheEntry[K, V]{key: key, value: value, at: c.now()})
}

func (c *ttlCache[K, V]) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune()
	return c.order.Len()
}
