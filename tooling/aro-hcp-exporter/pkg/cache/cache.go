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

package cache

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// MetricsCache is a cache for metrics
// It is used to cache metrics for a given TTL
// It improves response time and resilience of the exporter by avoiding
// adhoc API calls during metrics collection.
type MetricsCache struct {
	cacheMutex *sync.Mutex
	entries    map[string]cacheEntry
	ttl        time.Duration
}

// NewMetricsCache creates a new MetricsCache
func NewMetricsCache(ttl time.Duration) *MetricsCache {
	return &MetricsCache{
		cacheMutex: &sync.Mutex{},
		entries:    map[string]cacheEntry{},
		ttl:        ttl,
	}
}

func getMetricHash(metric prometheus.Metric) (string, error) {
	var dto dto.Metric
	err := metric.Write(&dto)
	if err != nil {
		return "", fmt.Errorf("failed to write metric: %w", err)
	}
	labelString := metric.Desc().String()

	for _, labelPair := range dto.GetLabel() {
		labelString = fmt.Sprintf("%s,%s,%s", labelString, labelPair.GetName(), labelPair.GetValue())
	}

	checksum := sha256.Sum256([]byte(labelString))
	return fmt.Sprintf("%x", checksum[:]), nil
}

// AddMetric adds a metric to the cache
func (mc *MetricsCache) AddMetric(metric prometheus.Metric) error {
	mc.cacheMutex.Lock()
	hash, err := getMetricHash(metric)
	if err != nil {
		return fmt.Errorf("failed to get metric hash: %w", err)
	}
	mc.entries[hash] = cacheEntry{
		creation: time.Now(),
		metric:   metric,
	}
	mc.cacheMutex.Unlock()
	return nil
}

// GetAllMetrics Iterates over all cached metrics and discards expired ones.
func (mc *MetricsCache) GetAllMetrics() []prometheus.Metric {
	mc.cacheMutex.Lock()
	returnArr := make([]prometheus.Metric, 0)
	for k, v := range mc.entries {
		if time.Since(v.creation).Seconds() > mc.ttl.Seconds() {
			delete(mc.entries, k)
		} else {
			returnArr = append(returnArr, v.metric)
		}
	}
	mc.cacheMutex.Unlock()
	return returnArr
}

type cacheEntry struct {
	creation time.Time
	metric   prometheus.Metric
}
