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

package metricscache

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type cacheEntry struct {
	metric  prometheus.Metric
	created time.Time
}

// Cache is a thread-safe cache for Prometheus metrics with TTL-based
// expiry. It decouples background metric collection from the Prometheus
// scrape path: collectors write metrics into the cache on their own schedule,
// and Prometheus reads from the cache without triggering API calls.
//
// Expired entries are automatically pruned during GetAll, so callers do not
// need to manage cleanup.
type Cache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
	ttl     time.Duration
}

// NewCache creates a Cache with the given TTL. Metrics older than the TTL
// are pruned on the next call to GetAll.
func NewCache(ttl time.Duration) *Cache {
	return &Cache{
		entries: make(map[string]cacheEntry),
		ttl:     ttl,
	}
}

// Set adds or updates a metric in the cache, resetting its TTL.
func (c *Cache) Set(key string, metric prometheus.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cacheEntry{metric: metric, created: time.Now()}
}

// GetAll returns all non-expired metrics and prunes expired entries.
func (c *Cache) GetAll() []prometheus.Metric {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	result := make([]prometheus.Metric, 0, len(c.entries))
	for k, e := range c.entries {
		if now.Sub(e.created) >= c.ttl {
			delete(c.entries, k)
		} else {
			result = append(result, e.metric)
		}
	}
	return result
}
