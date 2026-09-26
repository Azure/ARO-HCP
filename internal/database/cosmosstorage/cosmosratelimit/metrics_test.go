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

package cosmosratelimit

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"k8s.io/component-base/metrics/legacyregistry"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"

	"github.com/Azure/ARO-HCP/internal/database/informers/informerutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func metricLabels(kind, source, container, operation, status string) map[string]string {
	return map[string]string{"source_kind": kind, "source": source, "cosmosdb_container": container, "operation": operation, "status_code": status}
}

func gatherLimiterMetric(t *testing.T, gatherer prometheus.Gatherer, name string, labels map[string]string) *dto.Metric {
	t.Helper()
	families, err := gatherer.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			actual := map[string]string{}
			for _, label := range metric.GetLabel() {
				actual[label.GetName()] = label.GetValue()
			}
			require.Len(t, actual, 5, "all limiter metrics must have exactly the Cosmos accounting labels")
			if maps.Equal(labels, actual) {
				return metric
			}
		}
	}
	return nil
}

func testMetricsBucket(t *testing.T) (*TokenBucket, *rateLimitMetrics, *prometheus.Registry) {
	t.Helper()
	registry := prometheus.NewRegistry()
	metrics := newRateLimitMetrics(registry)
	bucket, err := NewTokenBucket("budget", 1, 1)
	require.NoError(t, err)
	bucket.metrics = metrics
	return bucket, metrics, registry
}

func metricsPipeline(bucket *TokenBucket, transport testTransport, retries int32) runtime.Pipeline {
	return runtime.NewPipeline("test", "v0.0.0", runtime.PipelineOptions{}, &policy.ClientOptions{
		PerRetryPolicies: []policy.Policy{NewPolicy(bucket)}, Transport: transport,
		Retry:     policy.RetryOptions{MaxRetries: retries, RetryDelay: time.Nanosecond, MaxRetryDelay: time.Nanosecond},
		Telemetry: policy.TelemetryOptions{Disabled: true},
	})
}

func TestLimiterMetricsAttribution(t *testing.T) {
	controller := utils.ContextWithControllerName(t.Context(), "ReconcileClusters")
	for _, tc := range []struct {
		name         string
		ctx          context.Context
		kind, source string
	}{
		{"unattributed", t.Context(), "unattributed", "unknown"},
		{"controller", controller, "controller", "ReconcileClusters"},
		{"informer", informerutils.ContextWithInformerName(controller, "ClusterInformer"), "informer", "ClusterInformer"},
		{"empty informer", informerutils.ContextWithInformerName(controller, ""), "controller", "ReconcileClusters"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bucket, _, registry := testMetricsBucket(t)
			p := metricsPipeline(bucket, func(req *http.Request) (*http.Response, error) { return response(req, http.StatusOK, ""), nil }, -1)
			// The client uses its bound bucket independently of the context.
			ctx := tc.ctx
			req, err := runtime.NewRequest(ctx, http.MethodGet, "https://cosmos.test/dbs/private-db/colls/Resources/docs/private-id")
			require.NoError(t, err)
			resp, err := p.Do(req)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			labels := metricLabels(tc.kind, tc.source, "Resources", "read", "200")
			require.Equal(t, 1.0, gatherLimiterMetric(t, registry, "cosmos_rate_limiter_requests_total", labels).GetCounter().GetValue())
			families, err := registry.Gather()
			require.NoError(t, err)
			require.Len(t, families, 1, "unblocked attempts do not create wait observations")
		})
	}
}

func TestLimiterWaitMetricsVisibleWhileBlocked(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		name := "refill with more debt"
		if canceled {
			name = "canceled"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				bucket, _, registry := testMetricsBucket(t)
				ctx, cancel := context.WithCancel(utils.ContextWithControllerName(t.Context(), "Cleanup"))
				defer cancel()
				require.NoError(t, bucket.Wait(ctx))
				families, err := registry.Gather()
				require.NoError(t, err)
				require.Empty(t, families, "a full bucket is not a wait")
				bucket.Consume(4)
				done := make(chan error, 2)
				for range 2 {
					go func() { done <- bucket.Wait(ctx) }()
				}
				synctest.Wait()
				labels := metricLabels("controller", "Cleanup", "unknown", "unknown", "unknown")
				require.Equal(t, 2.0, gatherLimiterMetric(t, registry, "cosmos_rate_limiter_waits_total", labels).GetCounter().GetValue())
				require.Equal(t, 2.0, gatherLimiterMetric(t, registry, "cosmos_rate_limiter_waiting_requests", labels).GetGauge().GetValue())
				require.Nil(t, gatherLimiterMetric(t, registry, "cosmos_rate_limiter_wait_duration_seconds", labels), "duration is observed when the wait ends")
				time.Sleep(time.Second)
				if canceled {
					cancel()
				} else {
					bucket.Consume(2)
				}
				for range 2 {
					err := <-done
					if canceled {
						require.ErrorIs(t, err, context.Canceled)
					} else {
						require.NoError(t, err)
					}
				}
				require.Equal(t, 2.0, gatherLimiterMetric(t, registry, "cosmos_rate_limiter_waits_total", labels).GetCounter().GetValue(), "rechecking debt must not count another wait")
				require.Zero(t, gatherLimiterMetric(t, registry, "cosmos_rate_limiter_waiting_requests", labels).GetGauge().GetValue())
				histogram := gatherLimiterMetric(t, registry, "cosmos_rate_limiter_wait_duration_seconds", labels).GetHistogram()
				require.Equal(t, uint64(2), histogram.GetSampleCount())
				expectedSeconds := 10.0 // Two 5-second waits, including the debt added while blocked.
				if canceled {
					expectedSeconds = 2
				}
				require.Equal(t, expectedSeconds, histogram.GetSampleSum())
			})
		})
	}
}

func TestLimiterRetryMetrics(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bucket, _, registry := testMetricsBucket(t)
		ctx := utils.ContextWithControllerName(t.Context(), "Cleanup")
		attempts := 0
		p := metricsPipeline(bucket, func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				return response(req, http.StatusInternalServerError, "3.5"), nil
			}
			time.Sleep(time.Second) // HTTP latency must not be included in the wait histogram.
			return response(req, http.StatusOK, "1"), nil
		}, 1)
		req, err := runtime.NewRequest(ctx, http.MethodPost, "https://cosmos.test/dbs/db/colls/Resources/docs")
		require.NoError(t, err)
		req.Raw().Header.Set("x-ms-cosmos-is-batch-request", "True")
		resp, err := p.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, 2, attempts)
		for _, status := range []string{"500", "200"} {
			labels := metricLabels("controller", "Cleanup", "Resources", "batch", status)
			require.Equal(t, 1.0, gatherLimiterMetric(t, registry, "cosmos_rate_limiter_requests_total", labels).GetCounter().GetValue())
		}
		labels := metricLabels("controller", "Cleanup", "Resources", "batch", "unknown")
		require.Equal(t, 1.0, gatherLimiterMetric(t, registry, "cosmos_rate_limiter_waits_total", labels).GetCounter().GetValue())
		require.Zero(t, gatherLimiterMetric(t, registry, "cosmos_rate_limiter_waiting_requests", labels).GetGauge().GetValue())
		histogram := gatherLimiterMetric(t, registry, "cosmos_rate_limiter_wait_duration_seconds", labels).GetHistogram()
		require.Equal(t, uint64(1), histogram.GetSampleCount())
		require.InDelta(t, 2.5, histogram.GetSampleSum(), 1e-8)
	})
}

func TestLimiterCanceledRequestIsNotDispatched(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bucket, _, registry := testMetricsBucket(t)
		bucket.Consume(11)
		ctx, cancel := context.WithTimeout(utils.ContextWithControllerName(t.Context(), "Cleanup"), time.Second)
		defer cancel()
		p := metricsPipeline(bucket, func(*http.Request) (*http.Response, error) { t.Fatal("canceled request was sent"); return nil, nil }, -1)
		req, err := runtime.NewRequest(ctx, http.MethodGet, "https://cosmos.test/dbs/db/colls/Resources/docs/item")
		require.NoError(t, err)
		_, err = p.Do(req)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		labels := metricLabels("controller", "Cleanup", "Resources", "read", "unknown")
		require.Nil(t, gatherLimiterMetric(t, registry, "cosmos_rate_limiter_requests_total", labels))
		require.Equal(t, 1.0, gatherLimiterMetric(t, registry, "cosmos_rate_limiter_waits_total", labels).GetCounter().GetValue())
		require.Zero(t, gatherLimiterMetric(t, registry, "cosmos_rate_limiter_waiting_requests", labels).GetGauge().GetValue())
		require.Equal(t, 1.0, gatherLimiterMetric(t, registry, "cosmos_rate_limiter_wait_duration_seconds", labels).GetHistogram().GetSampleSum())
	})
}

func TestLimiterTransportFailureIsCounted(t *testing.T) {
	bucket, _, registry := testMetricsBucket(t)
	failure := errors.New("transport failure")
	p := metricsPipeline(bucket, func(*http.Request) (*http.Response, error) { return nil, failure }, -1)
	req, err := runtime.NewRequest(t.Context(), http.MethodGet, "https://cosmos.test")
	require.NoError(t, err)
	_, err = p.Do(req)
	require.ErrorIs(t, err, failure)
	labels := metricLabels("unattributed", "unknown", "unknown", "metadata", "unknown")
	require.Equal(t, 1.0, gatherLimiterMetric(t, registry, "cosmos_rate_limiter_requests_total", labels).GetCounter().GetValue())
}

func TestRegisterLimiterMetricsExposesSharedCollectors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		registry := prometheus.NewRegistry()
		require.NoError(t, RegisterMetrics(registry))
		requestLabels := metricLabels("controller", t.Name(), "Resources", "read", "200")
		waitLabels := metricLabels("controller", t.Name(), "unknown", "unknown", "unknown")
		requestsBefore := gatherLimiterMetric(t, registry, "cosmos_rate_limiter_requests_total", requestLabels).GetCounter().GetValue()
		waitsBefore := gatherLimiterMetric(t, registry, "cosmos_rate_limiter_waits_total", waitLabels).GetCounter().GetValue()
		durationBefore := gatherLimiterMetric(t, registry, "cosmos_rate_limiter_wait_duration_seconds", waitLabels).GetHistogram().GetSampleSum()
		ctx := utils.ContextWithControllerName(t.Context(), t.Name())
		bucket, err := NewTokenBucket("registration", 1, 1)
		require.NoError(t, err)
		bucket.Consume(2)
		p := pipeline(bucket, func(req *http.Request) (*http.Response, error) { return response(req, http.StatusOK, "0"), nil }, -1)
		require.NoError(t, bucket.Do(ctx, func(ctx context.Context) error {
			req, err := runtime.NewRequest(ctx, http.MethodGet, "https://cosmos.test/dbs/db/colls/Resources/docs/item")
			require.NoError(t, err)
			resp, err := p.Do(req)
			if resp != nil {
				require.NoError(t, resp.Body.Close())
			}
			return err
		}))
		for _, gatherer := range []prometheus.Gatherer{registry, legacyregistry.DefaultGatherer} {
			require.Equal(t, requestsBefore+1, gatherLimiterMetric(t, gatherer, "cosmos_rate_limiter_requests_total", requestLabels).GetCounter().GetValue())
			require.Equal(t, waitsBefore+1, gatherLimiterMetric(t, gatherer, "cosmos_rate_limiter_waits_total", waitLabels).GetCounter().GetValue())
			require.Zero(t, gatherLimiterMetric(t, gatherer, "cosmos_rate_limiter_waiting_requests", waitLabels).GetGauge().GetValue())
			require.Equal(t, durationBefore+1, gatherLimiterMetric(t, gatherer, "cosmos_rate_limiter_wait_duration_seconds", waitLabels).GetHistogram().GetSampleSum())
		}
		recorder := httptest.NewRecorder()
		promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		require.Equal(t, http.StatusOK, recorder.Code)
		for _, name := range []string{"cosmos_rate_limiter_requests_total", "cosmos_rate_limiter_waits_total", "cosmos_rate_limiter_waiting_requests", "cosmos_rate_limiter_wait_duration_seconds"} {
			require.Contains(t, recorder.Body.String(), "# HELP "+name)
		}
		require.Error(t, RegisterMetrics(registry), "duplicate registration must surface its error")
	})
}
