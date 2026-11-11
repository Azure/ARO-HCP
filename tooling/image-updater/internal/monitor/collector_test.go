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

package monitor

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type fakeProwClient struct {
	jobs []ProwJob
	err  error
}

func (f *fakeProwClient) ListJobs(context.Context) ([]ProwJob, error) {
	return f.jobs, f.err
}

func completedJob(name, buildID, state, url string, start, completion time.Time) ProwJob {
	var job ProwJob
	job.Spec.Job = name
	job.Spec.Type = "periodic"
	job.Status.State = state
	job.Status.URL = url
	job.Status.BuildID = buildID
	job.Status.StartTime = start
	job.Status.CompletionTime = completion
	return job
}

func newTestCollector(client ProwClient) *Collector {
	logger := slog.New(slog.DiscardHandler)
	return NewCollector(client, regexp.MustCompile(`^periodic-ci-Azure-ARO-HCP-main-image-updater-`), logger)
}

func TestCollectorIgnoresUnrelatedAndIncompleteJobs(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	client := &fakeProwClient{jobs: []ProwJob{
		completedJob("periodic-ci-Azure-ARO-HCP-main-image-updater-tooling", "100", "success", "gs://bucket/log", now.Add(-2*time.Hour), now.Add(-1*time.Hour)),
		completedJob("periodic-ci-Azure-ARO-HCP-main-e2e-parallel", "200", "success", "gs://bucket/other", now.Add(-2*time.Hour), now.Add(-1*time.Hour)),
	}}

	// A still-running image-updater run: not terminal, must be ignored.
	running := completedJob("periodic-ci-Azure-ARO-HCP-main-image-updater-tooling", "300", "pending", "", now.Add(-3*time.Hour), time.Time{})
	client.jobs = append(client.jobs, running)

	// A completed job missing its build ID: malformed, must be ignored.
	noBuildID := completedJob("periodic-ci-Azure-ARO-HCP-main-image-updater-tooling", "", "success", "", now.Add(-2*time.Hour), now.Add(-1*time.Hour))
	client.jobs = append(client.jobs, noBuildID)

	collector := newTestCollector(client)
	collector.now = func() time.Time { return now }
	snapshots, err := collector.computeSnapshots(context.Background())
	if err != nil {
		t.Fatalf("computeSnapshots: %v", err)
	}

	if len(snapshots) != 1 {
		t.Fatalf("got %d monitored jobs, want 1: %+v", len(snapshots), snapshots)
	}
	snapshot, ok := snapshots["periodic-ci-Azure-ARO-HCP-main-image-updater-tooling"]
	if !ok {
		t.Fatalf("missing snapshot for image-updater job: %+v", snapshots)
	}
	if snapshot.latest.buildID != "100" {
		t.Fatalf("latest run build ID = %q, want 100", snapshot.latest.buildID)
	}
}

func TestCollectorConsecutiveFailuresAndLatestRun(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	// Oldest first in the response; collector must sort by completion time.
	client := &fakeProwClient{jobs: []ProwJob{
		completedJob("periodic-ci-Azure-ARO-HCP-main-image-updater-tooling", "1", "failure", "gs://b/1", now.Add(-8*time.Hour), now.Add(-7*time.Hour)),
		completedJob("periodic-ci-Azure-ARO-HCP-main-image-updater-tooling", "2", "success", "gs://b/2", now.Add(-6*time.Hour), now.Add(-5*time.Hour)),
		completedJob("periodic-ci-Azure-ARO-HCP-main-image-updater-tooling", "3", "error", "gs://b/3", now.Add(-4*time.Hour), now.Add(-3*time.Hour)),
		completedJob("periodic-ci-Azure-ARO-HCP-main-image-updater-tooling", "4", "failure", "gs://b/4", now.Add(-2*time.Hour), now.Add(-1*time.Hour)),
	}}

	collector := newTestCollector(client)
	collector.now = func() time.Time { return now }
	snapshots, err := collector.computeSnapshots(context.Background())
	if err != nil {
		t.Fatalf("computeSnapshots: %v", err)
	}

	snapshot := snapshots["periodic-ci-Azure-ARO-HCP-main-image-updater-tooling"]
	if snapshot.latest.buildID != "4" {
		t.Fatalf("latest run build ID = %q, want 4", snapshot.latest.buildID)
	}
	if snapshot.latest.duration != 3600 {
		t.Fatalf("latest run duration = %v, want 3600", snapshot.latest.duration)
	}
	if snapshot.consecutiveFailures != 2 {
		t.Fatalf("consecutive failures = %d, want 2 (runs 4 and 3, stopping at success 2)", snapshot.consecutiveFailures)
	}
	if snapshot.latestFailure == nil || snapshot.latestFailure.buildID != "4" {
		t.Fatalf("latest failing run = %+v, want build 4", snapshot.latestFailure)
	}
	if snapshot.latestSuccess == nil || snapshot.latestSuccess.buildID != "2" {
		t.Fatalf("latest successful run = %+v, want build 2", snapshot.latestSuccess)
	}
}

func TestCollectorAllRunsFailingHasNoSuccess(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	client := &fakeProwClient{jobs: []ProwJob{
		completedJob("periodic-ci-Azure-ARO-HCP-main-image-updater-tooling", "1", "failure", "gs://b/1", now.Add(-4*time.Hour), now.Add(-3*time.Hour)),
		completedJob("periodic-ci-Azure-ARO-HCP-main-image-updater-tooling", "2", "failure", "gs://b/2", now.Add(-2*time.Hour), now.Add(-1*time.Hour)),
	}}

	collector := newTestCollector(client)
	collector.now = func() time.Time { return now }
	snapshots, err := collector.computeSnapshots(context.Background())
	if err != nil {
		t.Fatalf("computeSnapshots: %v", err)
	}

	snapshot := snapshots["periodic-ci-Azure-ARO-HCP-main-image-updater-tooling"]
	if snapshot.consecutiveFailures != 2 {
		t.Fatalf("consecutive failures = %d, want 2", snapshot.consecutiveFailures)
	}
	if snapshot.latestSuccess != nil {
		t.Fatalf("latest success = %+v, want nil", snapshot.latestSuccess)
	}
}

func TestCollectorMetricsEmitted(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	client := &fakeProwClient{jobs: []ProwJob{
		completedJob("periodic-ci-Azure-ARO-HCP-main-image-updater-tooling", "1", "success", "gs://b/1", now.Add(-4*time.Hour), now.Add(-3*time.Hour)),
		completedJob("periodic-ci-Azure-ARO-HCP-main-image-updater-tooling", "2", "failure", "gs://b/2", now.Add(-2*time.Hour), now.Add(-1*time.Hour)),
	}}

	collector := newTestCollector(client)
	collector.now = func() time.Time { return now }
	collector.poll(context.Background())

	registry := prometheus.NewPedanticRegistry()
	if err := registry.Register(collector); err != nil {
		t.Fatalf("register collector: %v", err)
	}

	expected := `
# HELP image_updater_consecutive_failures Number of consecutive failed or errored completed runs since the most recent success; build_id and url labels identify the most recent failing run
# TYPE image_updater_consecutive_failures gauge
image_updater_consecutive_failures{build_id="2",job_name="periodic-ci-Azure-ARO-HCP-main-image-updater-tooling",url="gs://b/2"} 1
# HELP image_updater_last_run_duration_seconds Duration in seconds of the most recent completed run; build_id and url labels identify that run
# TYPE image_updater_last_run_duration_seconds gauge
image_updater_last_run_duration_seconds{build_id="2",job_name="periodic-ci-Azure-ARO-HCP-main-image-updater-tooling",url="gs://b/2"} 3600
# HELP image_updater_last_run_timestamp_seconds Unix timestamp of the most recent completed run; build_id, result and url labels identify that run
# TYPE image_updater_last_run_timestamp_seconds gauge
image_updater_last_run_timestamp_seconds{build_id="2",job_name="periodic-ci-Azure-ARO-HCP-main-image-updater-tooling",result="failure",url="gs://b/2"} 1.7943084e+09
# HELP image_updater_last_scrape_timestamp_seconds Unix timestamp of the most recent Prow poll
# TYPE image_updater_last_scrape_timestamp_seconds gauge
image_updater_last_scrape_timestamp_seconds 1.794312e+09
# HELP image_updater_scrape_success Whether the most recent Prow poll succeeded
# TYPE image_updater_scrape_success gauge
image_updater_scrape_success 1
`
	if err := testutil.GatherAndCompare(registry, strings.NewReader(expected), metricConsecutive, metricLastRunDuration, metricLastRun, metricLastScrape, metricScrapeSuccess); err != nil {
		t.Fatalf("metrics comparison failed: %v", err)
	}
}

// Guards against silent blindness: a regex typo or renamed Prow job yields
// zero monitored jobs while scrape_success stays 1.
func TestCollectorEmitsJobsMatchedGauge(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

	collector := newTestCollector(&fakeProwClient{jobs: []ProwJob{
		completedJob("periodic-ci-Azure-ARO-HCP-main-image-updater-tooling", "1", "success", "gs://b/1", now.Add(-2*time.Hour), now.Add(-1*time.Hour)),
		completedJob("periodic-ci-Azure-ARO-HCP-main-e2e-parallel", "2", "success", "gs://b/2", now.Add(-2*time.Hour), now.Add(-1*time.Hour)),
	}})
	collector.now = func() time.Time { return now }
	collector.poll(context.Background())

	registry := prometheus.NewPedanticRegistry()
	if err := registry.Register(collector); err != nil {
		t.Fatalf("register collector: %v", err)
	}

	expected := `
# HELP image_updater_jobs_matched Number of monitored Prow jobs with at least one valid completed run; 0 means no monitored job ever completed a run and all other metrics are blind
# TYPE image_updater_jobs_matched gauge
image_updater_jobs_matched 1
`
	if err := testutil.GatherAndCompare(registry, strings.NewReader(expected), metricJobsMatched); err != nil {
		t.Fatalf("metrics comparison failed: %v", err)
	}
}

func TestCollectorJobsMatchedZeroWhenRegexMatchesNothing(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

	collector := newTestCollector(&fakeProwClient{jobs: []ProwJob{
		completedJob("periodic-ci-Azure-ARO-HCP-main-e2e-parallel", "1", "success", "gs://b/1", now.Add(-2*time.Hour), now.Add(-1*time.Hour)),
	}})
	collector.now = func() time.Time { return now }
	collector.poll(context.Background())

	if got := collector.jobsMatched; got != 0 {
		t.Fatalf("jobs matched = %d, want 0", got)
	}
	if collector.scrapeSuccess != 1 {
		t.Fatalf("scrape_success = %v, want 1 (poll succeeded, only the regex matched nothing)", collector.scrapeSuccess)
	}
}

func TestCollectorPollFailureFlipsScrapeSuccess(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

	// First a successful poll, then a failing one.
	collector := newTestCollector(&fakeProwClient{jobs: []ProwJob{
		completedJob("periodic-ci-Azure-ARO-HCP-main-image-updater-tooling", "1", "success", "gs://b/1", now.Add(-2*time.Hour), now.Add(-1*time.Hour)),
	}})
	collector.now = func() time.Time { return now }
	collector.poll(context.Background())
	if collector.scrapeSuccess != 1 {
		t.Fatalf("scrape_success after success = %v, want 1", collector.scrapeSuccess)
	}

	collector.client = &fakeProwClient{err: context.DeadlineExceeded}
	collector.poll(context.Background())
	if collector.scrapeSuccess != 0 {
		t.Fatalf("scrape_success after failure = %v, want 0", collector.scrapeSuccess)
	}
	if collector.lastScrape == 0 {
		t.Fatal("last_scrape timestamp not updated on failed poll")
	}
}
