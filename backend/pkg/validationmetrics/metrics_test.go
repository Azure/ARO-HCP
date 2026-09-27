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

package validationmetrics

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestResult(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, "success"},
		{errors.New("rate: Wait would exceed context deadline"), "error"},
		{context.Canceled, "canceled"},
		{context.DeadlineExceeded, "deadline_exceeded"},
		{&azcore.ResponseError{StatusCode: 429}, "throttled"},
		{&azcore.ResponseError{StatusCode: 409}, "conflict"},
		{&azcore.ResponseError{StatusCode: 412}, "conflict"},
		{&azcore.ResponseError{StatusCode: 500}, "error"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			require.Equal(t, tc.want, Result(tc.err))
			if tc.err != nil {
				require.Equal(t, tc.want, Result(fmt.Errorf("wrapped: %w", tc.err)))
			}
		})
	}
}

func TestPhases(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		registry := prometheus.NewPedanticRegistry()
		metrics := New(registry)
		baseline, err := registry.Gather()
		require.NoError(t, err)
		ctx := WithMetrics(utils.ContextWithLogger(t.Context(), logr.Discard()), metrics)
		StartPhase(ctx, "check_access_call")(nil) // Registry alone does not enable phases.
		StartPhase(context.Background(), "check_access_call")(nil)
		families, err := registry.Gather()
		require.NoError(t, err)
		require.Equal(t, baseline, families, "phase calls without validation context must not change any series")

		ctx, validation := StartValidation(ctx, "controller")
		finish := StartPhase(ctx, "check_access_call")
		require.Equal(t, 1.0, testutil.ToFloat64(metrics.inflight.WithLabelValues("controller", "check_access_call")))
		time.Sleep(120 * time.Second)
		finish(context.Canceled)
		finish(nil) // Finishing twice must not decrement twice or report success.
		validation.Observe("unknown")
		require.Equal(t, 0.0, testutil.ToFloat64(metrics.inflight.WithLabelValues("controller", "check_access_call")))

		var wg sync.WaitGroup
		for range 5 {
			wg.Go(func() { StartPhase(ctx, "identity_get")(nil) })
		}
		wg.Wait()
		validation.Complete(ctx, "validation", "unchanged")
		require.Equal(t, 120.0, validation.durations["check_access_call"])
		require.Equal(t, 1, validation.counts["check_access_call"])
		require.Equal(t, 5, validation.counts["identity_get"])

		families, err = registry.Gather()
		require.NoError(t, err)
		for _, family := range families {
			for _, metric := range family.Metric {
				labels := map[string]string{}
				for _, label := range metric.Label {
					labels[label.GetName()] = label.GetValue()
				}
				if labels["controller"] != "controller" {
					require.Contains(t, []string{ControlPlaneController, DataPlaneController}, labels["controller"])
					require.Zero(t, metric.GetHistogram().GetSampleCount())
					require.Zero(t, metric.GetCounter().GetValue())
					require.Zero(t, metric.GetGauge().GetValue())
					continue
				}
				require.Equal(t, "controller", labels["controller"])
				switch family.GetName() {
				case "backend_validation_duration_seconds":
					require.Equal(t, map[string]string{"controller": "controller", "outcome": "unknown"}, labels)
					require.Equal(t, 120.0, metric.Histogram.GetSampleSum())
				case "backend_validation_phase_duration_seconds":
					require.Len(t, labels, 3)
					if labels["phase"] == "check_access_call" {
						require.Equal(t, "canceled", labels["result"])
						require.Equal(t, uint64(1), metric.Histogram.GetSampleCount())
						require.Equal(t, 120.0, metric.Histogram.GetSampleSum())
						require.Len(t, metric.Histogram.Bucket, 15)
						require.Equal(t, 600.0, metric.Histogram.Bucket[14].GetUpperBound())
						require.Equal(t, uint64(1), metric.Histogram.Bucket[11].GetCumulativeCount())
					}
				case "backend_validation_phase_inflight":
					require.Len(t, labels, 2)
					require.Zero(t, metric.Gauge.GetValue())
				default:
					t.Fatalf("unexpected metric %s", family.GetName())
				}
			}
		}
	})
}

func TestPanicCleanup(t *testing.T) {
	registry := prometheus.NewPedanticRegistry()
	metrics := New(registry)
	ctx, validation := StartValidation(WithMetrics(utils.ContextWithLogger(t.Context(), logr.Discard()), metrics), "controller")
	var finish func(error)
	require.PanicsWithValue(t, "original panic", func() {
		defer validation.Complete(ctx, "validation", "not_attempted")
		finish = StartPhase(ctx, "identity_token")
		panic("original panic")
	})
	finish(nil)
	validation.Complete(ctx, "validation", "success")
	StartPhase(ctx, "identity_get")(nil)
	require.Equal(t, map[string]int{"identity_token": 1}, validation.counts, "closed aggregates must remain immutable")
	require.Equal(t, "panic", validation.outcome)
	require.Equal(t, 0.0, testutil.ToFloat64(metrics.inflight.WithLabelValues("controller", "identity_token")))
	families, err := registry.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() == "backend_validation_phase_duration_seconds" {
			require.Len(t, family.Metric, 109)
			observations := uint64(0)
			for _, metric := range family.Metric {
				observations += metric.Histogram.GetSampleCount()
				if metric.Histogram.GetSampleCount() == 0 {
					continue
				}
				for _, label := range metric.Label {
					if label.GetName() == "result" {
						require.Equal(t, "error", label.GetValue())
					}
				}
			}
			require.Equal(t, uint64(1), observations)
		}
	}
}

func TestInitializedSeries(t *testing.T) {
	registry := prometheus.NewPedanticRegistry()
	New(registry)
	families, err := registry.Gather()
	require.NoError(t, err)
	want := map[string]int{
		"backend_validation_duration_seconds":       12,
		"backend_validation_attempts_total":         18,
		"backend_validation_phase_duration_seconds": 108,
		"backend_validation_phase_inflight":         18,
	}
	require.Len(t, families, len(want))
	series := 0
	for _, family := range families {
		require.Contains(t, want, family.GetName())
		require.Len(t, family.Metric, want[family.GetName()])
		for _, metric := range family.Metric {
			require.Equal(t, "controller", metric.Label[0].GetName())
			require.Contains(t, []string{ControlPlaneController, DataPlaneController}, metric.Label[0].GetValue())
			for _, label := range metric.Label[1:] {
				switch label.GetName() {
				case "outcome":
					require.Contains(t, []string{"passed", "failed", "unknown", "skipped", "invalid_result", "panic"}, label.GetValue())
				case "disposition":
					require.Contains(t, []string{"cooldown", "prerequisite_skip", "read_error", "completed", "persist_conflict", "persist_error", "invalid_result", "reported_unknown", "panic"}, label.GetValue())
				case "phase":
					require.Contains(t, []string{"check_access_wait", "check_access_call", "identity_credentials", "identity_credential_build", "identity_token", "subnet_get", "identity_get", "role_definition_lookup", "persist_result"}, label.GetValue())
				case "result":
					require.Contains(t, []string{"success", "error", "canceled", "deadline_exceeded", "throttled", "conflict"}, label.GetValue())
				default:
					t.Fatalf("unexpected label %s", label.GetName())
				}
			}
			require.Zero(t, metric.GetCounter().GetValue())
			require.Zero(t, metric.GetGauge().GetValue())
			if metric.Histogram != nil {
				require.Zero(t, metric.Histogram.GetSampleCount())
				require.Zero(t, metric.Histogram.GetSampleSum())
				require.Len(t, metric.Histogram.Bucket, 15)
				for _, bucket := range metric.Histogram.Bucket {
					require.Zero(t, bucket.GetCumulativeCount())
				}
				series += 18 // 15 finite buckets, +Inf, sum, count.
			} else {
				series++
			}
		}
	}
	require.Equal(t, 2196, series)
}
