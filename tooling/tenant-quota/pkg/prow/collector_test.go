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

package prow

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/prowjobs"
)

type fakeClient struct {
	jobs []prowjobs.Job
	err  error
}

func (f *fakeClient) ListJobs(context.Context) ([]prowjobs.Job, error) {
	return f.jobs, f.err
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := &config.Config{
		Timeout: "5s",
		Tenants: []config.TenantConfig{{
			TenantID:                 "tenant",
			ServicePrincipalClientId: "client",
			KeyVaultSecretName:       "secret",
		}},
		Prow: config.ProwConfig{
			Enabled:   true,
			BaseURL:   "https://prow.example.com",
			Interval:  "5m",
			Retention: "24h",
			Repository: config.ProwRepositoryConfig{
				Org:  "Azure",
				Name: "ARO-HCP",
			},
			ExcludeJobs: []string{"pull-ci-Azure-ARO-HCP-main-excluded"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	return cfg
}

func completedJob(name, buildID, state string, completed time.Time) prowjobs.Job {
	return prowjobs.Job{
		Spec: prowjobs.Spec{
			Type: "presubmit",
			Job:  name,
			Refs: &prowjobs.Refs{Org: "Azure", Repo: "ARO-HCP"},
		},
		Status: prowjobs.Status{
			State:          state,
			StartTime:      completed.Add(-30 * time.Minute),
			CompletionTime: completed,
			URL:            "gs://test-platform-results/pr-logs/pull/Azure_ARO-HCP/123/" + name + "/" + buildID,
			BuildID:        buildID,
		},
	}
}

func TestCollectOnceFiltersAndExportsCompletedJobs(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	jobs := []prowjobs.Job{
		completedJob("pull-ci-Azure-ARO-HCP-main-e2e-parallel", "1", "success", now.Add(-time.Hour)),
		completedJob("pull-ci-Azure-ARO-HCP-main-excluded", "2", "failure", now.Add(-time.Hour)),
		{
			Spec: prowjobs.Spec{
				Type:      "periodic",
				Job:       "periodic-ci-Azure-ARO-HCP-main-health",
				ExtraRefs: []prowjobs.Refs{{Org: "Azure", Repo: "ARO-HCP"}},
			},
			Status: prowjobs.Status{
				State:          "failure",
				StartTime:      now.Add(-2 * time.Hour),
				CompletionTime: now.Add(-90 * time.Minute),
				BuildID:        "3",
			},
		},
		{
			Spec: prowjobs.Spec{
				Type: "periodic",
				Job:  "periodic-ci-Azure-ARO-HCP-main-missing-refs",
			},
			Status: prowjobs.Status{
				State:          "success",
				StartTime:      now.Add(-2 * time.Hour),
				CompletionTime: now.Add(-90 * time.Minute),
				BuildID:        "missing-refs",
			},
		},
		completedJob("pull-ci-other-repo", "4", "success", now.Add(-time.Hour)),
	}
	jobs[0].Spec.Type = "batch"
	jobs[4].Spec.Refs = &prowjobs.Refs{Org: "other", Repo: "repo"}

	collector := NewCollector(testConfig(t), testLogger(), &fakeClient{jobs: jobs})
	collector.now = func() time.Time { return now }
	if err := collector.collectOnce(context.Background()); err != nil {
		t.Fatalf("collectOnce() error = %v", err)
	}

	metrics := gatherMetrics(t, collector)
	assertGauge(t, metrics, "prow_ci_cached_runs", nil, 2)
	assertGauge(t, metrics, "prow_ci_collection_success", nil, 1)
	assertGauge(t, metrics, "prow_ci_job_info", map[string]string{
		"job_name": "pull-ci-Azure-ARO-HCP-main-e2e-parallel",
		"job_type": "batch",
		"build_id": "1",
		"result":   "success",
	}, 1)
	assertGauge(t, metrics, "prow_ci_job_duration_seconds_bucket", map[string]string{
		"job_name": "periodic-ci-Azure-ARO-HCP-main-health",
		"job_type": "periodic",
		"result":   "failure",
		"le":       "1800",
	}, 1)
	assertMetricAbsent(t, metrics, "prow_ci_job_success")
	assertMetricAbsent(t, metrics, "prow_ci_job_completed_timestamp_seconds")
	assertMetricAbsent(t, metrics, "prow_ci_job_duration_seconds_count")
	assertMetricAbsent(t, metrics, "prow_ci_job_duration_seconds_sum")
}

func TestDurationMetricsAggregateRuns(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	first := completedJob("pull-ci-Azure-ARO-HCP-main-lint", "1", "success", now.Add(-time.Hour))
	first.Status.StartTime = first.Status.CompletionTime.Add(-10 * time.Minute)
	second := completedJob("pull-ci-Azure-ARO-HCP-main-lint", "2", "success", now.Add(-30*time.Minute))
	second.Status.StartTime = second.Status.CompletionTime.Add(-25 * time.Minute)

	collector := NewCollector(testConfig(t), testLogger(), &fakeClient{
		jobs: []prowjobs.Job{first, second},
	})
	collector.now = func() time.Time { return now }
	if err := collector.collectOnce(context.Background()); err != nil {
		t.Fatalf("collectOnce() error = %v", err)
	}

	metrics := gatherMetrics(t, collector)
	labels := map[string]string{
		"job_name": "pull-ci-Azure-ARO-HCP-main-lint",
		"job_type": "presubmit",
		"result":   "success",
	}
	assertGauge(t, metrics, "prow_ci_job_duration_seconds_bucket", withLabel(labels, "le", "900"), 1)
	assertGauge(t, metrics, "prow_ci_job_duration_seconds_bucket", withLabel(labels, "le", "1500"), 2)
	assertGauge(t, metrics, "prow_ci_job_duration_seconds_bucket", withLabel(labels, "le", "+Inf"), 2)
}

func TestDurationBuckets(t *testing.T) {
	want := []float64{
		60,
		300,
		600,
		900,
		1200,
		1500,
		1800,
		2700,
		3600,
		5400,
		7200,
		7800,
		8400,
		9000,
		10800,
	}
	if len(durationBuckets) != len(want) {
		t.Fatalf("durationBuckets length = %d, want %d", len(durationBuckets), len(want))
	}
	for i := range want {
		if durationBuckets[i] != want[i] {
			t.Fatalf("durationBuckets[%d] = %v, want %v", i, durationBuckets[i], want[i])
		}
	}
}

func TestCollectErrorPreservesRunsAndReportsFailure(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	client := &fakeClient{
		jobs: []prowjobs.Job{
			completedJob("pull-ci-Azure-ARO-HCP-main-e2e-parallel", "1", "success", now.Add(-time.Hour)),
		},
	}
	collector := NewCollector(testConfig(t), testLogger(), client)
	collector.now = func() time.Time { return now }
	collector.collect(context.Background())

	client.err = fmt.Errorf("Prow unavailable")
	collector.collect(context.Background())

	metrics := gatherMetrics(t, collector)
	assertGauge(t, metrics, "prow_ci_cached_runs", nil, 1)
	assertGauge(t, metrics, "prow_ci_collection_success", nil, 0)
}

func TestCollectPrunesRunsByCompletionTime(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	client := &fakeClient{
		jobs: []prowjobs.Job{
			completedJob("pull-ci-Azure-ARO-HCP-main-old", "1", "success", now.Add(-25*time.Hour)),
			completedJob("pull-ci-Azure-ARO-HCP-main-recent", "2", "success", now.Add(-23*time.Hour)),
		},
	}
	collector := NewCollector(testConfig(t), testLogger(), client)
	collector.now = func() time.Time { return now }
	if err := collector.collectOnce(context.Background()); err != nil {
		t.Fatalf("collectOnce() error = %v", err)
	}

	metrics := gatherMetrics(t, collector)
	assertGauge(t, metrics, "prow_ci_cached_runs", nil, 1)
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func gatherMetrics(t *testing.T, collector prometheus.Collector) map[string][]*dto.Metric {
	t.Helper()
	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	result := make(map[string][]*dto.Metric, len(families))
	for _, family := range families {
		result[family.GetName()] = family.Metric
	}
	return result
}

func assertGauge(t *testing.T, metrics map[string][]*dto.Metric, name string, labels map[string]string, want float64) {
	t.Helper()
	for _, metric := range metrics[name] {
		if hasLabels(metric, labels) {
			if got := metric.GetGauge().GetValue(); got != want {
				t.Fatalf("%s value = %v, want %v", name, got, want)
			}
			return
		}
	}
	t.Fatalf("metric %s with labels %v not found", name, labels)
}

func assertMetricAbsent(t *testing.T, metrics map[string][]*dto.Metric, name string) {
	t.Helper()
	if _, found := metrics[name]; found {
		t.Fatalf("metric %s unexpectedly found", name)
	}
}

func withLabel(labels map[string]string, name, value string) map[string]string {
	result := make(map[string]string, len(labels)+1)
	for labelName, labelValue := range labels {
		result[labelName] = labelValue
	}
	result[name] = value
	return result
}

func hasLabels(metric *dto.Metric, want map[string]string) bool {
	for name, value := range want {
		found := false
		for _, label := range metric.Label {
			if label.GetName() == name && label.GetValue() == value {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
