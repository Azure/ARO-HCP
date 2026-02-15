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
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func createTestMetric(fqdn string) prometheus.Metric {
	desc := prometheus.NewDesc(fqdn, "help", []string{"labels"}, nil)
	return prometheus.MustNewConstMetric(desc, prometheus.CounterValue, 1, "test")
}

func TestGetMetricHash(t *testing.T) {
	hash, err := getMetricHash(createTestMetric("foo_bar"))
	assert.NoError(t, err)
	assert.Equal(t, "5e5435705ad2e07a1f989a92f230e6437dec1a12ae4f43fd26f74bcf8fa029cf", hash)
	hash, err = getMetricHash(createTestMetric("foo_bar"))
	assert.NoError(t, err)
	assert.Equal(t, "5e5435705ad2e07a1f989a92f230e6437dec1a12ae4f43fd26f74bcf8fa029cf", hash)
	hash, err = getMetricHash(createTestMetric("other"))
	assert.NoError(t, err)
	assert.NotEqual(t, "5e5435705ad2e07a1f989a92f230e6437dec1a12ae4f43fd26f74bcf8fa029cf", hash)
}

func TestSameMetricWithDifferentLabelsDontOverwrite(t *testing.T) {
	cache := NewMetricsCache(1 * time.Second)
	desc := prometheus.NewDesc("test", "multimetric", []string{"aws_region"}, nil)

	metricEast1 := prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, 1, "us-east-1")
	metricWest1 := prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, 1, "us-west-1")
	metricEast2 := prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, 2, "us-east-1")
	err := cache.AddMetric(metricEast1)
	assert.NoError(t, err)
	err = cache.AddMetric(metricWest1) // should *not* overwrite metricEast1
	assert.NoError(t, err)
	err = cache.AddMetric(metricEast2) // should overwrite metricEast1
	assert.NoError(t, err)

	assert.Len(t, cache.GetAllMetrics(), 2)
	assert.NotContains(t, cache.GetAllMetrics(), metricEast1)
	assert.Contains(t, cache.GetAllMetrics(), metricWest1)
	assert.Contains(t, cache.GetAllMetrics(), metricEast2)
}

func TestMetricCacheGetAllWithTTL(t *testing.T) {
	cache := NewMetricsCache(1 * time.Second)

	testMetric := createTestMetric("testing")
	err := cache.AddMetric(testMetric)
	assert.NoError(t, err)
	assert.Len(t, cache.entries, 1)

	assert.Equal(t, []prometheus.Metric{testMetric}, cache.GetAllMetrics())
	time.Sleep(2 * time.Second)
	assert.Len(t, cache.GetAllMetrics(), 0)
}
