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
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

func TestMetricsTrackAcceptanceAndLiveCacheSize(t *testing.T) {
	w := newTestWriter(t)
	now := time.Now()
	w.batches.now = func() time.Time { return now }
	w.metadata.now = func() time.Time { return now }
	registry := prometheus.NewRegistry()
	w.RegisterMetrics(registry)
	seedRun(w, "123")
	require.NoError(t, w.reconcile(testContext(t, w), "123"))
	require.NoError(t, w.reconcile(testContext(t, w), "123"))
	for _, kind := range batchKinds {
		require.Equal(t, float64(1), testutil.ToFloat64(w.metrics.submissions.WithLabelValues(string(kind), "accepted")))
	}
	require.Equal(t, float64(5), testutil.ToFloat64(w.metrics.cacheHits.WithLabelValues("batches")))
	require.Equal(t, float64(1), testutil.ToFloat64(w.metrics.cacheHits.WithLabelValues("metadata")))
	cacheSizes := func() map[string]float64 {
		families, err := registry.Gather()
		require.NoError(t, err)
		sizes := map[string]float64{}
		for _, family := range families {
			if family.GetName() != "ci_job_outcomes_cache_size" {
				continue
			}
			for _, metric := range family.Metric {
				sizes[metric.Label[0].GetValue()] = metric.Gauge.GetValue()
			}
		}
		return sizes
	}
	require.Equal(t, map[string]float64{"batches": 5, "metadata": 1}, cacheSizes())
	now = now.Add(15 * time.Minute)
	require.Equal(t, map[string]float64{"batches": 0, "metadata": 0}, cacheSizes(), "scrapes exclude expired entries even when no work is arriving")
}

func TestPermanentArtifactProblemsAreCounted(t *testing.T) {
	w := newTestWriter(t)
	seedRun(w, "123")
	w.e2eRows = func(ctx context.Context, _ *http.Client, _ string) ([]ciTestResult, []ciTestName, error) {
		snapshot.ReportProwArtifactProblem(ctx, "e2e", "malformed")
		return nil, nil, nil
	}
	require.NoError(t, w.reconcile(testContext(t, w), "123"))
	require.Equal(t, float64(1), testutil.ToFloat64(w.metrics.artifacts.WithLabelValues("e2e", "malformed")))
}
