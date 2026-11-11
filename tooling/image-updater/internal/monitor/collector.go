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

// Package monitor implements the image-updater health exporter. It polls the
// Prow API for the image-updater periodic jobs and exposes run-level health
// metrics so alerts can link the offending run and detect stalled runs (a
// hung run never completes, so only a missing-completion signal reveals it).
package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/prowjobs"
)

const (
	namespace = "image_updater"

	metricLastRun         = namespace + "_last_run_timestamp_seconds"
	metricLastRunDuration = namespace + "_last_run_duration_seconds"
	metricLastSuccess     = namespace + "_last_success_timestamp_seconds"
	metricConsecutive     = namespace + "_consecutive_failures"
	metricScrapeSuccess   = namespace + "_scrape_success"
	metricLastScrape      = namespace + "_last_scrape_timestamp_seconds"
	metricJobsMatched     = namespace + "_jobs_matched"
)

// run is one completed Prow run of a monitored job.
type run struct {
	jobName      string
	buildID      string
	result       string
	url          string
	duration     float64
	completionAt time.Time
}

// jobSnapshot is the accumulated state of one monitored job, merged across
// polls. Runs referenced by pointer, and the countedFailures map, are
// treated as immutable once stored in a snapshot: snapshots are replaced
// wholesale, never mutated in place.
type jobSnapshot struct {
	latest              run
	latestSuccess       *run
	consecutiveFailures int
	latestFailure       *run

	// countedFailures holds the failed/errored runs, keyed by build ID,
	// that are newer than latestSuccess: the current failure streak. The
	// set is what makes the snapshot robust to Prow's API behaviour —
	// retention aging, regressed windows, re-surfaced runs and
	// late-appearing successes — because runs are deduplicated by identity
	// instead of by position in a window. Its size is bounded by the
	// number of failures since the last success.
	countedFailures map[string]run

	// failureReason is the classified reason of latestFailure, empty when
	// no classification is available.
	failureReason string
}

// Collector polls Prow on an interval and exposes gauges describing the most
// recent state of each monitored job. Snapshot state is accumulated in
// memory across polls, merged by run identity, so retention aging and
// regressed API responses cannot lose or double-count runs. A restart
// starts from the currently visible window only: failures that have aged
// out of Prow are forgotten, so a long streak may under-report until the
// next success or fresh failures re-establish it.
type Collector struct {
	client   ProwClient
	reasons  ReasonClient
	jobRegex *regexp.Regexp
	logger   *slog.Logger
	now      func() time.Time

	mu            sync.RWMutex
	snapshots     map[string]jobSnapshot // keyed by job name
	scrapeSuccess float64
	lastScrape    float64
	jobsMatched   int

	// reasonCache memoizes classified failure reasons per build ID so a
	// stable failure streak costs one reason fetch, not one per poll.
	reasonCache map[string]string

	// reasonMisses counts consecutive failed classifications per build ID.
	// Empty results are retried because artifacts land in GCS
	// asynchronously, but a build whose junit is permanently unclassifiable
	// (truncated past the 1 MiB read limit, malformed XML) would otherwise
	// be re-fetched every poll for as long as it stays the latest failure.
	// The cap is generous enough that any realistically late artifact
	// upload still lands while retries remain.
	reasonMisses map[string]int

	lastRunDesc         *prometheus.Desc
	lastRunDurationDesc *prometheus.Desc
	lastSuccessDesc     *prometheus.Desc
	consecutiveDesc     *prometheus.Desc
	scrapeSuccessDesc   *prometheus.Desc
	lastScrapeDesc      *prometheus.Desc
	jobsMatchedDesc     *prometheus.Desc
}

// NewCollector returns a Collector monitoring Prow jobs whose name matches
// jobRegex (e.g. `^periodic-ci-Azure-ARO-HCP-main-image-updater-`). reasons
// may be nil, in which case consecutive-failure metrics carry an empty
// failure_reason label.
func NewCollector(client ProwClient, jobRegex *regexp.Regexp, logger *slog.Logger, reasons ReasonClient) *Collector {
	return &Collector{
		client:       client,
		reasons:      reasons,
		jobRegex:     jobRegex,
		logger:       logger,
		now:          time.Now,
		snapshots:    make(map[string]jobSnapshot),
		reasonCache:  make(map[string]string),
		reasonMisses: make(map[string]int),
		lastRunDesc: prometheus.NewDesc(
			metricLastRun,
			"Unix timestamp of the most recent completed run; build_id, result and url labels identify that run",
			[]string{"job_name", "build_id", "result", "url"},
			nil,
		),
		lastRunDurationDesc: prometheus.NewDesc(
			metricLastRunDuration,
			"Duration in seconds of the most recent completed run; build_id and url labels identify that run",
			[]string{"job_name", "build_id", "url"},
			nil,
		),
		lastSuccessDesc: prometheus.NewDesc(
			metricLastSuccess,
			"Unix timestamp of the most recent successful run",
			[]string{"job_name"},
			nil,
		),
		consecutiveDesc: prometheus.NewDesc(
			metricConsecutive,
			"Number of consecutive failed or errored completed runs since the most recent success; build_id, failure_reason and url labels identify the most recent failing run and its classified failure reason",
			[]string{"job_name", "build_id", "failure_reason", "url"},
			nil,
		),
		scrapeSuccessDesc: prometheus.NewDesc(
			metricScrapeSuccess,
			"Whether the most recent Prow poll succeeded",
			nil,
			nil,
		),
		lastScrapeDesc: prometheus.NewDesc(
			metricLastScrape,
			"Unix timestamp of the most recent Prow poll",
			nil,
			nil,
		),
		jobsMatchedDesc: prometheus.NewDesc(
			metricJobsMatched,
			"Number of monitored Prow jobs with at least one valid completed run; 0 means no monitored job ever completed a run and all other metrics are blind",
			nil,
			nil,
		),
	}
}

// Run polls Prow every interval until ctx is done.
func (c *Collector) Run(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		c.poll(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (c *Collector) poll(ctx context.Context) {
	// Snapshot the previous poll's state for streak carrying; poll is the
	// only writer of c.snapshots, so the reference is stable while unlocked.
	c.mu.Lock()
	prev := c.snapshots
	c.mu.Unlock()

	snapshots, matched, err := c.computeSnapshots(ctx, prev)
	now := c.now().UTC()

	// Enrich before taking the lock: reason fetches are slow network calls,
	// and blocking Collect would fail the scrape.
	if err == nil {
		c.enrichFailureReasons(ctx, snapshots)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastScrape = float64(now.Unix())
	if err != nil {
		c.scrapeSuccess = 0
		c.logger.Error("Prow poll failed", "error", err)
		return
	}
	c.scrapeSuccess = 1
	c.jobsMatched = matched
	c.snapshots = snapshots
}

// enrichFailureReasons classifies the latest failure of each failing job.
// Reason fetches must not fail the poll: an unavailable classification
// degrades to an empty failure_reason label.
func (c *Collector) enrichFailureReasons(ctx context.Context, snapshots map[string]jobSnapshot) {
	if c.reasons == nil {
		return
	}
	for jobName, snapshot := range snapshots {
		if snapshot.latestFailure == nil {
			continue
		}
		snapshot.failureReason = c.failureReason(ctx, jobName, snapshot.latestFailure.buildID)
		snapshots[jobName] = snapshot
	}
}

// maxReasonMisses caps how often an unclassifiable build is retried. Empty
// results are usually transient (artifacts land in GCS asynchronously), but
// a permanently unclassifiable junit (truncated past the read limit,
// malformed XML) would otherwise be re-fetched every poll for as long as it
// stays the latest failure.
const maxReasonMisses = 8

// failureReason returns the classified reason for a build, memoized so a
// stable failure streak costs one fetch. Empty results are not memoized,
// only miss-capped: artifacts land in GCS asynchronously, so a miss is
// often transient.
func (c *Collector) failureReason(ctx context.Context, jobName, buildID string) string {
	if reason, ok := c.reasonCache[buildID]; ok {
		return reason
	}
	if c.reasonMisses[buildID] >= maxReasonMisses {
		return ""
	}
	reason := c.reasons.FailureReason(ctx, jobName, buildID)
	if reason != "" {
		c.reasonCache[buildID] = reason
		delete(c.reasonMisses, buildID)
		return reason
	}
	c.reasonMisses[buildID]++
	return reason
}

// computeSnapshots fetches all Prow jobs, keeps completed runs of monitored
// jobs, and reduces them per job name to the snapshot the metrics describe.
// prev is the previous poll's snapshot, used to carry failure streaks across
// Prow's retention window.
func (c *Collector) computeSnapshots(ctx context.Context, prev map[string]jobSnapshot) (map[string]jobSnapshot, int, error) {
	jobs, err := c.client.ListJobs(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("list Prow jobs: %w", err)
	}

	runsByJob := make(map[string][]run)
	for _, job := range jobs {
		jobName := strings.TrimSpace(job.Spec.Job)
		if jobName == "" || !c.jobRegex.MatchString(jobName) || !prowjobs.IsTerminalState(job.Status.State) {
			continue
		}
		buildID := strings.TrimSpace(job.Status.BuildID)
		if buildID == "" || job.Status.CompletionTime.IsZero() || job.Status.StartTime.IsZero() {
			continue
		}
		completion := job.Status.CompletionTime.UTC()
		if completion.Before(job.Status.StartTime) {
			continue
		}
		runsByJob[jobName] = append(runsByJob[jobName], run{
			jobName:      jobName,
			buildID:      buildID,
			result:       prowjobs.NormalizeState(job.Status.State),
			url:          strings.TrimSpace(job.Status.URL),
			duration:     completion.Sub(job.Status.StartTime.UTC()).Seconds(),
			completionAt: completion,
		})
	}

	snapshots := make(map[string]jobSnapshot, len(runsByJob))
	for jobName, runs := range runsByJob {
		// Most recent completion first; build ID breaks exact ties.
		slices.SortFunc(runs, func(a, b run) int {
			if order := b.completionAt.Compare(a.completionAt); order != 0 {
				return order
			}
			return strings.Compare(b.buildID, a.buildID)
		})

		snapshots[jobName] = mergeSnapshot(prev[jobName], runs)
	}

	// Jobs absent from this response keep their last known state: a
	// regressed or partial response must not discard a job's streak and
	// series. A job that genuinely stops reporting still pages through the
	// stall rule on its frozen last-run timestamp.
	for jobName, prevSnapshot := range prev {
		if _, ok := snapshots[jobName]; !ok {
			snapshots[jobName] = prevSnapshot
		}
	}

	// The matched count covers the jobs present in this response (not
	// retained ones), so ImageUpdaterMonitorBlind still detects a regex
	// that no longer matches any job. The caller publishes it under the
	// lock together with the snapshots.
	return snapshots, len(runsByJob), nil
}

// mergeSnapshot folds one job's visible runs into its previous snapshot.
// The merge is by run identity, not window position: latest and
// latestSuccess are maxima over everything ever seen (monotone by
// construction, so a regressed window cannot move them backwards), and the
// failure streak is the set of failed runs keyed by build ID, pruned to
// those newer than the newest known success. Retention aging, regressed
// windows, re-surfaced runs, late-appearing successes and duplicate
// responses are therefore all non-events: there is no boundary to
// misplace and no count to ratchet.
func mergeSnapshot(prev jobSnapshot, runs []run) jobSnapshot {
	s := jobSnapshot{latest: runs[0]}
	if runNewer(prev.latest, s.latest) {
		s.latest = prev.latest
	}

	// The newest known success: the visible one wins only when it is
	// newer than the carried one.
	s.latestSuccess = newestSuccess(runs)
	if s.latestSuccess == nil || (prev.latestSuccess != nil && runNewer(*prev.latestSuccess, *s.latestSuccess)) {
		s.latestSuccess = prev.latestSuccess
	}

	// The failure streak: previous set plus this window's failures,
	// deduplicated by build ID, then pruned of runs at or older than the
	// newest known success.
	failures := make(map[string]run, len(prev.countedFailures)+len(runs))
	for buildID, failure := range prev.countedFailures {
		failures[buildID] = failure
	}
	for i := range runs {
		if runs[i].result != "success" {
			failures[runs[i].buildID] = runs[i]
		}
	}
	if s.latestSuccess != nil {
		for buildID, failure := range failures {
			if !runNewer(failure, *s.latestSuccess) {
				delete(failures, buildID)
			}
		}
	}
	s.countedFailures = failures
	s.consecutiveFailures = len(failures)
	for _, failure := range failures {
		if s.latestFailure == nil || runNewer(failure, *s.latestFailure) {
			failure := failure
			s.latestFailure = &failure
		}
	}
	return s
}

// newestSuccess returns the newest successful run in a newest-first
// sorted window, or nil when the window contains none.
func newestSuccess(runs []run) *run {
	for i := range runs {
		if runs[i].result == "success" {
			return &runs[i]
		}
	}
	return nil
}

// runNewer reports whether a completed before-or-at b's position in the
// newest-first ordering, using the same completion-time-then-build-ID
// comparison the snapshot sort uses.
func runNewer(a, b run) bool {
	if order := a.completionAt.Compare(b.completionAt); order != 0 {
		return order > 0
	}
	return strings.Compare(a.buildID, b.buildID) > 0
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.lastRunDesc
	ch <- c.lastRunDurationDesc
	ch <- c.lastSuccessDesc
	ch <- c.consecutiveDesc
	ch <- c.scrapeSuccessDesc
	ch <- c.lastScrapeDesc
	ch <- c.jobsMatchedDesc
}

// Collect implements prometheus.Collector.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	snapshots := c.snapshots
	scrapeSuccess := c.scrapeSuccess
	lastScrape := c.lastScrape
	jobsMatched := c.jobsMatched
	c.mu.RUnlock()

	for jobName, snapshot := range snapshots {
		ch <- prometheus.MustNewConstMetric(
			c.lastRunDesc, prometheus.GaugeValue,
			float64(snapshot.latest.completionAt.Unix()),
			jobName, snapshot.latest.buildID, snapshot.latest.result, snapshot.latest.url,
		)
		ch <- prometheus.MustNewConstMetric(
			c.lastRunDurationDesc, prometheus.GaugeValue,
			snapshot.latest.duration,
			jobName, snapshot.latest.buildID, snapshot.latest.url,
		)
		if snapshot.latestSuccess != nil {
			ch <- prometheus.MustNewConstMetric(
				c.lastSuccessDesc, prometheus.GaugeValue,
				float64(snapshot.latestSuccess.completionAt.Unix()),
				jobName,
			)
		}
		if snapshot.consecutiveFailures > 0 && snapshot.latestFailure != nil {
			ch <- prometheus.MustNewConstMetric(
				c.consecutiveDesc, prometheus.GaugeValue,
				float64(snapshot.consecutiveFailures),
				jobName, snapshot.latestFailure.buildID, snapshot.failureReason, snapshot.latestFailure.url,
			)
		}
	}
	ch <- prometheus.MustNewConstMetric(c.scrapeSuccessDesc, prometheus.GaugeValue, scrapeSuccess)
	ch <- prometheus.MustNewConstMetric(c.lastScrapeDesc, prometheus.GaugeValue, lastScrape)
	ch <- prometheus.MustNewConstMetric(c.jobsMatchedDesc, prometheus.GaugeValue, float64(jobsMatched))
}
