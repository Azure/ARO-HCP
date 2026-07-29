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
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func testMetric(name string, value float64, labelValues ...string) prometheus.Metric {
	desc := prometheus.NewDesc(name, "test metric", []string{"label"}, nil)
	return prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, labelValues...)
}

func TestSet_And_GetAll(t *testing.T) {
	c := NewCache(1 * time.Minute)

	m := testMetric("test_metric", 42, "a")
	c.Set("key1", m)

	result := c.GetAll()
	assert.Len(t, result, 1)
	assert.Contains(t, result, m)
}

func TestSet_OverwritesSameKey(t *testing.T) {
	c := NewCache(1 * time.Minute)

	m1 := testMetric("test_metric", 1, "a")
	m2 := testMetric("test_metric", 2, "a")
	c.Set("key1", m1)
	c.Set("key1", m2)

	result := c.GetAll()
	assert.Len(t, result, 1)
	assert.Contains(t, result, m2)
	assert.NotContains(t, result, m1)
}

func TestSet_DifferentKeysCoexist(t *testing.T) {
	c := NewCache(1 * time.Minute)

	m1 := testMetric("metric_a", 1, "a")
	m2 := testMetric("metric_b", 2, "b")
	c.Set("key1", m1)
	c.Set("key2", m2)

	result := c.GetAll()
	assert.Len(t, result, 2)
	assert.Contains(t, result, m1)
	assert.Contains(t, result, m2)
}

func TestGetAll_PrunesExpiredEntries(t *testing.T) {
	c := NewCache(1 * time.Second)

	m := testMetric("test_metric", 1, "a")
	c.Set("key1", m)

	assert.Len(t, c.GetAll(), 1)

	time.Sleep(2 * time.Second)

	assert.Len(t, c.GetAll(), 0)
	assert.Equal(t, 0, len(c.entries))
}

func TestGetAll_MixedFreshAndExpired(t *testing.T) {
	c := NewCache(1 * time.Second)

	old := testMetric("old_metric", 1, "a")
	c.Set("old", old)

	time.Sleep(600 * time.Millisecond)

	fresh := testMetric("fresh_metric", 2, "b")
	c.Set("fresh", fresh)

	time.Sleep(600 * time.Millisecond)

	result := c.GetAll()
	assert.Len(t, result, 1)
	assert.Contains(t, result, fresh)
	assert.NotContains(t, result, old)
}

func TestGetAll_PrunesFromMap(t *testing.T) {
	c := NewCache(1 * time.Second)

	c.Set("key1", testMetric("m", 1, "a"))
	time.Sleep(2 * time.Second)

	// expired entry still in map before GetAll
	assert.Equal(t, 1, len(c.entries))

	// GetAll prunes it
	c.GetAll()
	assert.Equal(t, 0, len(c.entries))
}

func TestGetAll_EmptyCache(t *testing.T) {
	c := NewCache(1 * time.Minute)
	result := c.GetAll()
	assert.Empty(t, result)
}
