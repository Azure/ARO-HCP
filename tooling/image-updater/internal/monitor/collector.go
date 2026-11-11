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

// jobSnapshot is the recomputed state of one monitored job.
type jobSnapshot struct {
	latest              run
	latestSuccess       *run
	consecutiveFailures int
	latestFailure       *run
}

// Collector polls Prow on an interval and exposes gauges describing the most
// recent state of each monitored job. Snapshots are recomputed statelessly
// from the API response on every poll, so no retention window is needed.
type Collector struct {
	client   ProwClient
	jobRegex *regexp.Regexp
	logger   *slog.Logger
	now      func() time.Time

	mu            sync.RWMutex
	snapshots     map[string]jobSnapshot // keyed by job name
	scrapeSuccess float64
	lastScrape    float64
	jobsMatched   int

	lastRunDesc         *prometheus.Desc
	lastRunDurationDesc *prometheus.Desc
	lastSuccessDesc     *prometheus.Desc
	consecutiveDesc     *prometheus.Desc
	scrapeSuccessDesc   *prometheus.Desc
	lastScrapeDesc      *prometheus.Desc
	jobsMatchedDesc     *prometheus.Desc
}

// NewCollector returns a Collector monitoring Prow jobs whose name matches
// jobRegex (e.g. `^periodic-ci-Azure-ARO-HCP-main-image-updater-`).
func NewCollector(client ProwClient, jobRegex *regexp.Regexp, logger *slog.Logger) *Collector {
	return &Collector{
		client:    client,
		jobRegex:  jobRegex,
		logger:    logger,
		now:       time.Now,
		snapshots: make(map[string]jobSnapshot),
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
			"Number of consecutive failed or errored completed runs since the most recent success; build_id and url labels identify the most recent failing run",
			[]string{"job_name", "build_id", "url"},
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
	snapshots, err := c.computeSnapshots(ctx)
	now := c.now().UTC()

	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastScrape = float64(now.Unix())
	if err != nil {
		c.scrapeSuccess = 0
		c.logger.Error("Prow poll failed", "error", err)
		return
	}
	c.scrapeSuccess = 1
	c.snapshots = snapshots
	c.jobsMatched = len(snapshots)
}

// computeSnapshots fetches all Prow jobs, keeps completed runs of monitored
// jobs, and reduces them per job name to the snapshot the metrics describe.
func (c *Collector) computeSnapshots(ctx context.Context) (map[string]jobSnapshot, error) {
	jobs, err := c.client.ListJobs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list Prow jobs: %w", err)
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

		snapshot := jobSnapshot{latest: runs[0]}
		for i := range runs {
			if runs[i].result == "success" {
				snapshot.latestSuccess = &runs[i]
				break
			}
			snapshot.consecutiveFailures++
			if snapshot.latestFailure == nil {
				snapshot.latestFailure = &runs[i]
			}
		}
		snapshots[jobName] = snapshot
	}
	return snapshots, nil
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
				jobName, snapshot.latestFailure.buildID, snapshot.latestFailure.url,
			)
		}
	}
	ch <- prometheus.MustNewConstMetric(c.scrapeSuccessDesc, prometheus.GaugeValue, scrapeSuccess)
	ch <- prometheus.MustNewConstMetric(c.lastScrapeDesc, prometheus.GaugeValue, lastScrape)
	ch <- prometheus.MustNewConstMetric(c.jobsMatchedDesc, prometheus.GaugeValue, float64(jobsMatched))
}
