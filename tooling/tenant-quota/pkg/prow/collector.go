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
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/prowjobs"
)

var (
	infoMetricLabels           = []string{"job_name", "job_type", "build_id", "result"}
	durationBucketMetricLabels = []string{"job_name", "job_type", "result", "le"}
	durationBuckets            = []float64{
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
)

type runMetrics struct {
	jobName      string
	jobType      string
	buildID      string
	result       string
	duration     float64
	completionAt time.Time
}

type durationMetricKey struct {
	jobName string
	jobType string
	result  string
}

type durationMetrics struct {
	buckets []float64
}

type Collector struct {
	config *config.Config
	logger *slog.Logger
	client prowjobs.Client
	now    func() time.Time

	infoDesc              *prometheus.Desc
	durationBucketDesc    *prometheus.Desc
	collectionSuccessDesc *prometheus.Desc
	lastSuccessDesc       *prometheus.Desc
	cachedRunsDesc        *prometheus.Desc

	mu                   sync.RWMutex
	runs                 map[string]runMetrics
	collectionSuccess    float64
	lastSuccessTimestamp float64
}

func NewCollector(cfg *config.Config, logger *slog.Logger, clients ...prowjobs.Client) *Collector {
	var client prowjobs.Client
	if len(clients) > 0 {
		client = clients[0]
	} else {
		client = prowjobs.NewHTTPClient(cfg.Prow.BaseURL, cfg.GetTimeout())
	}

	return &Collector{
		config: cfg,
		logger: logger,
		client: client,
		now:    time.Now,
		infoDesc: prometheus.NewDesc(
			"prow_ci_job_info",
			"Information about a completed Prow CI job",
			infoMetricLabels,
			nil,
		),
		durationBucketDesc: prometheus.NewDesc(
			"prow_ci_job_duration_window_runs",
			"Number of completed Prow CI jobs in the retention window with duration at or below the le boundary; use histogram_quantile() directly without rate() or increase()",
			durationBucketMetricLabels,
			nil,
		),
		collectionSuccessDesc: prometheus.NewDesc(
			"prow_ci_collection_success",
			"Whether the most recent Prow CI collection succeeded",
			nil,
			nil,
		),
		lastSuccessDesc: prometheus.NewDesc(
			"prow_ci_collection_last_success_timestamp_seconds",
			"Unix timestamp of the most recent successful Prow CI collection",
			nil,
			nil,
		),
		cachedRunsDesc: prometheus.NewDesc(
			"prow_ci_cached_runs",
			"Number of completed Prow CI runs currently exposed",
			nil,
			nil,
		),
		runs: make(map[string]runMetrics),
	}
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.infoDesc
	ch <- c.durationBucketDesc
	ch <- c.collectionSuccessDesc
	ch <- c.lastSuccessDesc
	ch <- c.cachedRunsDesc
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	durations := make(map[durationMetricKey]*durationMetrics)
	for _, run := range c.runs {
		labels := []string{run.jobName, run.jobType, run.buildID, run.result}
		ch <- prometheus.MustNewConstMetric(c.infoDesc, prometheus.GaugeValue, 1, labels...)

		key := durationMetricKey{
			jobName: run.jobName,
			jobType: run.jobType,
			result:  run.result,
		}
		metrics, found := durations[key]
		if !found {
			metrics = &durationMetrics{buckets: make([]float64, len(durationBuckets)+1)}
			durations[key] = metrics
		}
		for i, upperBound := range durationBuckets {
			if run.duration <= upperBound {
				metrics.buckets[i]++
			}
		}
		metrics.buckets[len(durationBuckets)]++
	}
	for key, metrics := range durations {
		labels := []string{key.jobName, key.jobType, key.result}
		for i, upperBound := range durationBuckets {
			bucketLabels := append(labels, strconv.FormatFloat(upperBound, 'g', -1, 64))
			ch <- prometheus.MustNewConstMetric(
				c.durationBucketDesc,
				prometheus.GaugeValue,
				metrics.buckets[i],
				bucketLabels...,
			)
		}
		ch <- prometheus.MustNewConstMetric(
			c.durationBucketDesc,
			prometheus.GaugeValue,
			metrics.buckets[len(durationBuckets)],
			append(labels, "+Inf")...,
		)
	}
	ch <- prometheus.MustNewConstMetric(c.collectionSuccessDesc, prometheus.GaugeValue, c.collectionSuccess)
	ch <- prometheus.MustNewConstMetric(c.lastSuccessDesc, prometheus.GaugeValue, c.lastSuccessTimestamp)
	ch <- prometheus.MustNewConstMetric(c.cachedRunsDesc, prometheus.GaugeValue, float64(len(c.runs)))
}

func (c *Collector) Start(ctx context.Context) {
	defer utilruntime.HandleCrash()

	interval := c.config.Prow.GetInterval()
	c.logger.Info(
		"Starting Prow CI collection",
		"interval", interval,
		"retention", c.config.Prow.GetRetention(),
		"repository", c.config.Prow.Repository.Org+"/"+c.config.Prow.Repository.Name,
		"excludedJobs", len(c.config.Prow.ExcludeJobs),
	)

	c.collect(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Stopping Prow CI collection")
			return
		case <-ticker.C:
			c.collect(ctx)
		}
	}
}

func (c *Collector) collect(parentCtx context.Context) {
	ctx, cancel := context.WithTimeout(parentCtx, c.config.GetTimeout())
	defer cancel()

	if err := c.collectOnce(ctx); err != nil {
		c.mu.Lock()
		c.collectionSuccess = 0
		c.pruneLocked(c.now().UTC())
		c.mu.Unlock()
		c.logger.Error("Failed to collect Prow CI jobs", "error", err)
	}
}

func (c *Collector) collectOnce(ctx context.Context) error {
	jobs, err := c.client.ListJobs(ctx)
	if err != nil {
		return fmt.Errorf("list Prow jobs: %w", err)
	}

	now := c.now().UTC()
	excluded := make(map[string]struct{}, len(c.config.Prow.ExcludeJobs))
	for _, jobName := range c.config.Prow.ExcludeJobs {
		excluded[strings.TrimSpace(jobName)] = struct{}{}
	}

	matched := 0
	completed := 0
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, job := range jobs {
		if !matchesRepository(job, c.config.Prow.Repository) {
			continue
		}
		matched++

		jobName := strings.TrimSpace(job.Spec.Job)
		if _, found := excluded[jobName]; found {
			continue
		}
		if !validCompletedJob(job) {
			continue
		}

		result := prowjobs.NormalizeState(job.Status.State)
		jobType := strings.TrimSpace(job.Spec.Type)
		if jobType == "" {
			jobType = "unknown"
		}
		buildID := strings.TrimSpace(job.Status.BuildID)
		completion := job.Status.CompletionTime.UTC()
		c.runs[jobName+"/"+buildID] = runMetrics{
			jobName:      jobName,
			jobType:      jobType,
			buildID:      buildID,
			result:       result,
			duration:     completion.Sub(job.Status.StartTime.UTC()).Seconds(),
			completionAt: completion,
		}
		completed++
	}

	c.pruneLocked(now)
	c.collectionSuccess = 1
	c.lastSuccessTimestamp = float64(now.Unix())
	c.logger.Info(
		"Collected Prow CI jobs",
		"fetched", len(jobs),
		"repositoryMatches", matched,
		"completedRuns", completed,
		"cachedRuns", len(c.runs),
	)
	return nil
}

func (c *Collector) pruneLocked(now time.Time) {
	retention := c.config.Prow.GetRetention()
	for key, run := range c.runs {
		if now.Sub(run.completionAt) >= retention {
			delete(c.runs, key)
		}
	}
}

func matchesRepository(job prowjobs.Job, repository config.ProwRepositoryConfig) bool {
	if job.Spec.Refs != nil {
		if refsMatch(*job.Spec.Refs, repository) {
			return true
		}
	}
	for _, refs := range job.Spec.ExtraRefs {
		if refsMatch(refs, repository) {
			return true
		}
	}
	return false
}

func refsMatch(refs prowjobs.Refs, repository config.ProwRepositoryConfig) bool {
	return strings.EqualFold(strings.TrimSpace(refs.Org), strings.TrimSpace(repository.Org)) &&
		strings.EqualFold(strings.TrimSpace(refs.Repo), strings.TrimSpace(repository.Name))
}

func validCompletedJob(job prowjobs.Job) bool {
	return strings.TrimSpace(job.Spec.Job) != "" &&
		strings.TrimSpace(job.Status.BuildID) != "" &&
		prowjobs.IsTerminalState(job.Status.State) &&
		!job.Status.StartTime.IsZero() &&
		!job.Status.CompletionTime.IsZero() &&
		!job.Status.CompletionTime.Before(job.Status.StartTime)
}
