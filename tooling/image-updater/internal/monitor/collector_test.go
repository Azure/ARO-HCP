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
	"fmt"
	"log/slog"
	"math/rand"
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
	snapshots, _, err := collector.computeSnapshots(context.Background(), nil)
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
	snapshots, _, err := collector.computeSnapshots(context.Background(), nil)
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
	snapshots, _, err := collector.computeSnapshots(context.Background(), nil)
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

func TestCollectorCarriesFailureStreakAcrossRetentionWindow(t *testing.T) {
	const job = "periodic-ci-Azure-ARO-HCP-main-image-updater-tooling"
	// Monday 07:00 UTC; Friday's runs have aged out of Prow's ~24-30h
	// retention except where noted.
	now := time.Date(2026, 11, 16, 7, 0, 0, 0, time.UTC)
	thursday := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	friday := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)

	// Friday evening's poll: a Thursday success, then three consecutive
	// Friday failures (06:00, 14:00, 22:00).
	collector := newTestCollector(&fakeProwClient{jobs: []ProwJob{
		completedJob(job, "f0", "success", "gs://b/f0", thursday.Add(22*time.Hour), thursday.Add(23*time.Hour)),
		completedJob(job, "f1", "failure", "gs://b/f1", friday.Add(6*time.Hour), friday.Add(7*time.Hour)),
		completedJob(job, "f2", "failure", "gs://b/f2", friday.Add(14*time.Hour), friday.Add(15*time.Hour)),
		completedJob(job, "f3", "failure", "gs://b/f3", friday.Add(22*time.Hour), friday.Add(23*time.Hour)),
	}})
	collector.now = func() time.Time { return now }
	prev, _, err := collector.computeSnapshots(context.Background(), nil)
	if err != nil {
		t.Fatalf("Friday poll: %v", err)
	}
	if got := prev[job].consecutiveFailures; got != 3 {
		t.Fatalf("Friday poll streak = %d, want 3", got)
	}
	if prev[job].latestSuccess == nil || prev[job].latestSuccess.buildID != "f0" {
		t.Fatalf("Friday poll latest success = %+v, want f0", prev[job].latestSuccess)
	}

	// Monday 06:00 failed too, and only that run is still visible: the
	// streak must carry to 4, not reset to 1, and the last-success series
	// must keep reporting Thursday's success instead of going dark.
	collector.client = &fakeProwClient{jobs: []ProwJob{
		completedJob(job, "m1", "failure", "gs://b/m1", now.Add(-1*time.Hour), now.Add(-30*time.Minute)),
	}}
	snapshots, _, err := collector.computeSnapshots(context.Background(), prev)
	if err != nil {
		t.Fatalf("Monday poll (aged out): %v", err)
	}
	if got := snapshots[job].consecutiveFailures; got != 4 {
		t.Fatalf("Monday poll streak with Friday aged out = %d, want 4", got)
	}
	if snapshots[job].latestFailure.buildID != "m1" {
		t.Fatalf("latest failure = %q, want m1", snapshots[job].latestFailure.buildID)
	}
	if snapshots[job].latestSuccess == nil || snapshots[job].latestSuccess.buildID != "f0" {
		t.Fatalf("Monday poll latest success = %+v, want the carried f0", snapshots[job].latestSuccess)
	}

	// Overlap case: Friday's last run is still visible alongside Monday's;
	// it must not be double-counted.
	collector.client = &fakeProwClient{jobs: []ProwJob{
		completedJob(job, "f3", "failure", "gs://b/f3", friday.Add(22*time.Hour), friday.Add(23*time.Hour)),
		completedJob(job, "m1", "failure", "gs://b/m1", now.Add(-1*time.Hour), now.Add(-30*time.Minute)),
	}}
	snapshots, _, err = collector.computeSnapshots(context.Background(), prev)
	if err != nil {
		t.Fatalf("Monday poll (overlap): %v", err)
	}
	if got := snapshots[job].consecutiveFailures; got != 4 {
		t.Fatalf("Monday poll streak with Friday overlapping = %d, want 4 (no double count)", got)
	}

	// Inconsistent-window case: a response that omits the anchor and
	// re-surfaces an already-counted older run must not inflate the streak;
	// only runs strictly newer than the anchor extend it.
	collector.client = &fakeProwClient{jobs: []ProwJob{
		completedJob(job, "m1", "failure", "gs://b/m1", now.Add(-1*time.Hour), now.Add(-30*time.Minute)),
		completedJob(job, "f2", "failure", "gs://b/f2", friday.Add(14*time.Hour), friday.Add(15*time.Hour)),
	}}
	snapshots, _, err = collector.computeSnapshots(context.Background(), prev)
	if err != nil {
		t.Fatalf("Monday poll (inconsistent window): %v", err)
	}
	if got := snapshots[job].consecutiveFailures; got != 4 {
		t.Fatalf("streak with already-counted run re-surfaced = %d, want 4 (anchor comparison, not presence)", got)
	}

	// A success visible in the window still resets the streak.
	collector.client = &fakeProwClient{jobs: []ProwJob{
		completedJob(job, "f3", "failure", "gs://b/f3", friday.Add(22*time.Hour), friday.Add(23*time.Hour)),
		completedJob(job, "m1", "success", "gs://b/m1", now.Add(-1*time.Hour), now.Add(-30*time.Minute)),
	}}
	snapshots, _, err = collector.computeSnapshots(context.Background(), prev)
	if err != nil {
		t.Fatalf("Monday poll (success): %v", err)
	}
	if got := snapshots[job].consecutiveFailures; got != 0 {
		t.Fatalf("streak after visible success = %d, want 0", got)
	}
	if snapshots[job].latestSuccess == nil || snapshots[job].latestSuccess.buildID != "m1" {
		t.Fatalf("latest success after visible success = %+v, want m1", snapshots[job].latestSuccess)
	}
}

func TestCollectorStreakAnchorDoesNotRegress(t *testing.T) {
	const job = "periodic-ci-Azure-ARO-HCP-main-image-updater-tooling"
	now := time.Date(2026, 11, 16, 7, 0, 0, 0, time.UTC)
	friday := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	f1 := completedJob(job, "f1", "failure", "gs://b/f1", friday.Add(6*time.Hour), friday.Add(7*time.Hour))
	f2 := completedJob(job, "f2", "failure", "gs://b/f2", friday.Add(14*time.Hour), friday.Add(15*time.Hour))

	// An oscillating Prow response: a full window, then a regressed
	// older-only window, then the full window again. Each snapshot is fed
	// into the next poll, as poll() does. The count must stay 2: adopting
	// the regressed window's older failure as the anchor would let the
	// third poll re-count f2 and falsely reach the alert threshold.
	windows := [][]ProwJob{{f2, f1}, {f1}, {f2, f1}}
	collector := newTestCollector(nil)
	collector.now = func() time.Time { return now }
	var prev map[string]jobSnapshot
	for i, window := range windows {
		collector.client = &fakeProwClient{jobs: window}
		snapshots, _, err := collector.computeSnapshots(context.Background(), prev)
		if err != nil {
			t.Fatalf("poll %d: %v", i+1, err)
		}
		snapshot := snapshots[job]
		if got := snapshot.consecutiveFailures; got != 2 {
			t.Fatalf("poll %d streak = %d, want 2 (anchor must not regress)", i+1, got)
		}
		if snapshot.latestFailure.buildID != "f2" {
			t.Fatalf("poll %d anchor = %q, want f2", i+1, snapshot.latestFailure.buildID)
		}
		if snapshot.latest.buildID != "f2" {
			t.Fatalf("poll %d latest run = %q, want f2 (latest must not move backwards)", i+1, snapshot.latest.buildID)
		}
		prev = snapshots
	}

	// A regressed window surfacing an old success must not reset the
	// streak either.
	s0 := completedJob(job, "s0", "success", "gs://b/s0", friday.Add(2*time.Hour), friday.Add(3*time.Hour))
	collector.client = &fakeProwClient{jobs: []ProwJob{s0}}
	snapshots, _, err := collector.computeSnapshots(context.Background(), prev)
	if err != nil {
		t.Fatalf("regressed success poll: %v", err)
	}
	if got := snapshots[job].consecutiveFailures; got != 2 {
		t.Fatalf("streak after regressed old success = %d, want 2", got)
	}
}

func TestCollectorStreakBoundedByCarriedSuccess(t *testing.T) {
	const job = "periodic-ci-Azure-ARO-HCP-main-image-updater-tooling"
	now := time.Date(2026, 11, 16, 7, 0, 0, 0, time.UTC)
	friday := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	s4 := completedJob(job, "s4", "success", "gs://b/s4", friday.Add(6*time.Hour), friday.Add(7*time.Hour))
	f3 := completedJob(job, "f3", "failure", "gs://b/f3", friday.Add(-2*time.Hour), friday.Add(-1*time.Hour))
	f2 := completedJob(job, "f2", "failure", "gs://b/f2", friday.Add(-4*time.Hour), friday.Add(-3*time.Hour))
	f1 := completedJob(job, "f1", "failure", "gs://b/f1", friday.Add(-6*time.Hour), friday.Add(-5*time.Hour))

	collector := newTestCollector(nil)
	collector.now = func() time.Time { return now }

	// A poll observes the success s4 with older failures behind it.
	collector.client = &fakeProwClient{jobs: []ProwJob{s4, f3}}
	prev, _, err := collector.computeSnapshots(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// A regressed response containing only failures older than s4 must
	// report zero consecutive failures, not three.
	collector.client = &fakeProwClient{jobs: []ProwJob{f3, f2, f1}}
	snapshots, _, err := collector.computeSnapshots(context.Background(), prev)
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshots[job].consecutiveFailures; got != 0 {
		t.Fatalf("streak after regressed older-only failures = %d, want 0 (bounded by success s4)", got)
	}
	prev = snapshots

	// One new failure newer than s4 must report one, not four.
	f5 := completedJob(job, "f5", "failure", "gs://b/f5", now.Add(-1*time.Hour), now.Add(-30*time.Minute))
	collector.client = &fakeProwClient{jobs: []ProwJob{f5, f3, f2, f1}}
	snapshots, _, err = collector.computeSnapshots(context.Background(), prev)
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshots[job].consecutiveFailures; got != 1 {
		t.Fatalf("streak after one new failure past the success = %d, want 1", got)
	}
}

// TestCollectorSnapshotModelValidation drives computeSnapshots through
// randomized poll sequences and checks every snapshot against a truth
// model computed from the union of all runs visible so far: the streak is
// the set of failed runs (by build ID) newer than the newest visible
// success, latest is the newest visible run, and latestSuccess the newest
// visible success. Windows are arbitrary subsets of the known runs —
// retention aging, regressed windows and re-surfaced runs — with new runs
// appearing over time, so the test pins the snapshot spec itself rather
// than any individual failure mode.
func TestCollectorSnapshotModelValidation(t *testing.T) {
	const job = "periodic-ci-Azure-ARO-HCP-main-image-updater-tooling"
	for seed := int64(1); seed <= 200; seed++ {
		rng := rand.New(rand.NewSource(seed))

		// Ground-truth history: a run per step, completing in order.
		type truthRun struct {
			run
			visible bool
		}
		var history []truthRun
		base := time.Date(2026, 11, 2, 6, 0, 0, 0, time.UTC)

		collector := newTestCollector(nil)
		now := base.Add(48 * time.Hour)
		collector.now = func() time.Time { return now }

		var prev map[string]jobSnapshot
		for poll := range 12 {
			// A new run completes before this poll most steps.
			if rng.Float64() < 0.65 {
				result := "failure"
				if rng.Float64() < 0.35 {
					result = "success"
				}
				completion := base.Add(time.Duration(len(history)) * 8 * time.Hour)
				history = append(history, truthRun{run: run{
					jobName:      job,
					buildID:      fmt.Sprintf("%d", 1000+len(history)),
					result:       result,
					url:          "gs://b/" + fmt.Sprintf("%d", 1000+len(history)),
					duration:     3600,
					completionAt: completion,
				}})
			}
			// The window is a random subset of the history: each run is
			// independently visible or hidden, so windows regress and
			// re-surface runs freely.
			var window []ProwJob
			visible := map[string]run{}
			for i := range history {
				if rng.Float64() < 0.55 {
					r := history[i].run
					window = append(window, ProwJob{})
					j := &window[len(window)-1]
					j.Spec.Job = r.jobName
					j.Status.State = r.result
					j.Status.BuildID = r.buildID
					j.Status.URL = r.url
					j.Status.StartTime = r.completionAt.Add(-time.Hour)
					j.Status.CompletionTime = r.completionAt
					visible[r.buildID] = r
					history[i].visible = true
				}
			}
			collector.client = &fakeProwClient{jobs: window}
			snapshots, _, err := collector.computeSnapshots(context.Background(), prev)
			if err != nil {
				t.Fatalf("seed %d poll %d: %v", seed, poll, err)
			}
			snapshot := snapshots[job]

			// Truth model over everything visible so far.
			var newest, newestSuccess run
			truthFailures := map[string]run{}
			for _, r := range history {
				if !r.visible {
					continue
				}
				if runNewer(r.run, newest) {
					newest = r.run
				}
				if r.result == "success" && runNewer(r.run, newestSuccess) {
					newestSuccess = r.run
				}
			}
			for _, r := range history {
				if !r.visible || r.result == "success" {
					continue
				}
				if newestSuccess.buildID != "" && !runNewer(r.run, newestSuccess) {
					continue
				}
				truthFailures[r.buildID] = r.run
			}

			if snapshot.latest.buildID != newest.buildID {
				t.Fatalf("seed %d poll %d: latest = %s, want %s", seed, poll, snapshot.latest.buildID, newest.buildID)
			}
			if (snapshot.latestSuccess == nil) != (newestSuccess.buildID == "") ||
				(snapshot.latestSuccess != nil && snapshot.latestSuccess.buildID != newestSuccess.buildID) {
				t.Fatalf("seed %d poll %d: latestSuccess = %+v, want %s", seed, poll, snapshot.latestSuccess, newestSuccess.buildID)
			}
			if snapshot.consecutiveFailures != len(truthFailures) {
				t.Fatalf("seed %d poll %d: streak = %d, want %d (failures %v)", seed, poll, snapshot.consecutiveFailures, len(truthFailures), truthFailures)
			}
			var truthAnchor *run
			for _, r := range truthFailures {
				r := r
				if truthAnchor == nil || runNewer(r, *truthAnchor) {
					truthAnchor = &r
				}
			}
			if (snapshot.latestFailure == nil) != (truthAnchor == nil) ||
				(snapshot.latestFailure != nil && snapshot.latestFailure.buildID != truthAnchor.buildID) {
				t.Fatalf("seed %d poll %d: latestFailure = %+v, want %+v", seed, poll, snapshot.latestFailure, truthAnchor)
			}
			prev = snapshots
		}
	}
}

// TestCollectorRetainsJobsAbsentFromResponse pins the job-omission
// behaviour: a job whose runs are entirely absent from one response keeps
// its streak and series, and jobs_matched reflects only the jobs present.
func TestCollectorRetainsJobsAbsentFromResponse(t *testing.T) {
	const job = "periodic-ci-Azure-ARO-HCP-main-image-updater-tooling"
	now := time.Date(2026, 11, 16, 7, 0, 0, 0, time.UTC)
	friday := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)

	collector := newTestCollector(&fakeProwClient{jobs: []ProwJob{
		completedJob(job, "f1", "failure", "gs://b/f1", friday.Add(6*time.Hour), friday.Add(7*time.Hour)),
		completedJob(job, "f2", "failure", "gs://b/f2", friday.Add(14*time.Hour), friday.Add(15*time.Hour)),
	}})
	collector.now = func() time.Time { return now }
	prev, matched, err := collector.computeSnapshots(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if matched != 1 {
		t.Fatalf("matched = %d, want 1", matched)
	}

	// The next response omits the job entirely: state must be retained.
	collector.client = &fakeProwClient{jobs: nil}
	snapshots, matched, err := collector.computeSnapshots(context.Background(), prev)
	if err != nil {
		t.Fatal(err)
	}
	if matched != 0 {
		t.Fatalf("matched after omission = %d, want 0 (MonitorBlind semantics)", matched)
	}
	snapshot, ok := snapshots[job]
	if !ok {
		t.Fatal("job dropped from snapshots on absent response")
	}
	if got := snapshot.consecutiveFailures; got != 2 {
		t.Fatalf("streak after absent response = %d, want 2 (retained)", got)
	}
	if snapshot.latest.buildID != "f2" {
		t.Fatalf("latest after absent response = %q, want f2", snapshot.latest.buildID)
	}
}
