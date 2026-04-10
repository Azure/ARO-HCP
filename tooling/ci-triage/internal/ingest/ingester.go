// Copyright 2025 Microsoft Corporation
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

package ingest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/config"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/gcs"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/prow"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/store"
)

const maxWorkers = 20

// Ingester orchestrates data acquisition from GCS into the store.
type Ingester struct {
	gcs   *gcs.Client
	store *store.Store
	log   logr.Logger
}

// New creates a new Ingester.
func New(gcsClient *gcs.Client, s *store.Store, log logr.Logger) *Ingester {
	return &Ingester{
		gcs:   gcsClient,
		store: s,
		log:   log,
	}
}

// IngestResult holds counts from an ingestion run.
type IngestResult struct {
	NewJobs   int
	Skipped   int
	Errors    int
	FetchErrs map[string]int
}

// IngestEnv ingests new builds for a given env and job type since the given time.
func (ing *Ingester) IngestEnv(ctx context.Context, env, jobType string, since time.Time) (*IngestResult, error) {
	jobName, err := config.JobName(env, jobType)
	if err != nil {
		return nil, err
	}

	sinceBID := prow.TimeToBuildID(since)
	result := &IngestResult{}

	if jobType == "periodic" {
		buildIDs, err := ing.gcs.ListPeriodicBuilds(ctx, jobName, sinceBID, 1000)
		if err != nil {
			return nil, fmt.Errorf("listing periodic builds for %s: %w", env, err)
		}
		ing.ingestPeriodicBuilds(ctx, env, jobType, jobName, buildIDs, result)
	} else {
		presubmits, err := ing.gcs.ListPresubmitBuilds(ctx, jobName, sinceBID, 1000)
		if err != nil {
			return nil, fmt.Errorf("listing presubmit builds for %s: %w", env, err)
		}
		ing.ingestPresubmitBuilds(ctx, env, jobType, jobName, presubmits, result)
	}

	result.FetchErrs = ing.gcs.Errors()
	return result, nil
}

// IngestAll ingests all environments and job types since the given time.
func (ing *Ingester) IngestAll(ctx context.Context, since time.Time) (*IngestResult, error) {
	total := &IngestResult{FetchErrs: make(map[string]int)}

	for _, env := range config.EnvNames() {
		for _, jt := range config.JobTypes(env) {
			r, err := ing.IngestEnv(ctx, env, jt, since)
			if err != nil {
				ing.log.Error(err, "ingestion failed", "env", env, "jobType", jt)
				total.Errors++
				continue
			}
			total.NewJobs += r.NewJobs
			total.Skipped += r.Skipped
			total.Errors += r.Errors
		}
	}

	total.FetchErrs = ing.gcs.Errors()
	return total, nil
}

// PollLoop runs IngestAll on a recurring interval until ctx is cancelled.
func (ing *Ingester) PollLoop(ctx context.Context, interval time.Duration, since time.Time) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run immediately on start
	ing.log.Info("starting ingestion poll", "interval", interval)
	r, err := ing.IngestAll(ctx, since)
	if err != nil {
		ing.log.Error(err, "initial ingestion failed")
	} else {
		ing.log.Info("ingestion complete", "new", r.NewJobs, "skipped", r.Skipped, "errors", r.Errors)
	}

	for {
		select {
		case <-ctx.Done():
			ing.log.Info("stopping ingestion poll")
			return
		case <-ticker.C:
			r, err := ing.IngestAll(ctx, since)
			if err != nil {
				ing.log.Error(err, "ingestion failed")
			} else {
				ing.log.Info("ingestion complete", "new", r.NewJobs, "skipped", r.Skipped, "errors", r.Errors)
			}
		}
	}
}

func (ing *Ingester) ingestPeriodicBuilds(ctx context.Context, env, jobType, jobName string, buildIDs []string, result *IngestResult) {
	// Sort descending (newest first)
	sort.Sort(sort.Reverse(sort.StringSlice(buildIDs)))

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(maxWorkers)

	for _, bid := range buildIDs {
		g.Go(func() error {
			exists, err := ing.store.HasBuild(ctx, bid)
			if err != nil {
				return nil // non-fatal
			}
			if exists {
				result.Skipped++
				return nil
			}

			gcsURL := fmt.Sprintf("%s/logs/%s/%s", config.GCSDirect, jobName, bid)
			return ing.ingestBuild(ctx, env, jobType, jobName, bid, gcs.GCSWebURL(gcsURL), gcsURL, 0, result)
		})
	}

	if err := g.Wait(); err != nil {
		ing.log.Error(err, "periodic ingestion errors", "env", env)
	}
}

func (ing *Ingester) ingestPresubmitBuilds(ctx context.Context, env, jobType, jobName string, builds []gcs.PresubmitBuild, result *IngestResult) {
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(maxWorkers)

	for _, build := range builds {
		g.Go(func() error {
			exists, err := ing.store.HasBuild(ctx, build.BuildID)
			if err != nil {
				return nil
			}
			if exists {
				result.Skipped++
				return nil
			}

			if !strings.HasPrefix(build.GSLink, "gs://") {
				return nil
			}
			gcsURL := gcs.GCSDirectURL(build.GSLink)
			gcswebURL := gcs.GCSWebURL(gcsURL)

			// Extract PR number from GCS URL
			prNumber := extractPRNumber(gcsURL)

			return ing.ingestBuild(ctx, env, jobType, jobName, build.BuildID, gcswebURL, gcsURL, prNumber, result)
		})
	}

	if err := g.Wait(); err != nil {
		ing.log.Error(err, "presubmit ingestion errors", "env", env)
	}
}

func (ing *Ingester) ingestBuild(ctx context.Context, env, jobType, jobName, buildID, gcswebURL, gcsURL string, prNumber int, result *IngestResult) error {
	started, err := prow.BuildIDToISO(buildID)
	if err != nil {
		result.Errors++
		return nil
	}

	// Fetch finished.json
	state := "pending"
	var revision string
	finished, err := ing.gcs.FetchFinished(ctx, gcsURL)
	if err == nil && finished != nil {
		state = strings.ToLower(finished.Result)
		if state == "" {
			state = "unknown"
		}
		if finished.Revision != "" && len(finished.Revision) > 12 {
			revision = finished.Revision[:12]
		} else {
			revision = finished.Revision
		}
	}

	// Insert job
	job := &store.Job{
		Env:       env,
		JobType:   jobType,
		JobName:   jobName,
		BuildID:   buildID,
		BaseURL:   gcswebURL,
		Revision:  revision,
		PRNumber:  prNumber,
		State:     state,
		StartedAt: started,
	}
	jobID, err := ing.store.UpsertJob(ctx, job)
	if err != nil {
		ing.log.Error(err, "failed to upsert job", "buildID", buildID)
		result.Errors++
		return nil
	}

	// Fetch and parse JUnit
	cfg := config.Envs[env]
	junitData, err := ing.gcs.FetchJUnit(ctx, gcsURL, cfg.Step, cfg.Container)
	if err != nil || junitData == nil {
		// No junit data is normal for some jobs
		result.NewJobs++
		return nil
	}

	parsed := prow.ParseJUnit(junitData)
	if parsed == nil {
		// Try step-level
		parsed = prow.ParseJUnitStepLevel(junitData)
	}
	if parsed == nil {
		result.NewJobs++
		return nil
	}

	// Build test results
	var testResults []store.TestResult
	for _, f := range parsed.Failures {
		testResults = append(testResults, store.TestResult{
			JobID:                    jobID,
			TestName:                 f.Name,
			Status:                   "failed",
			FailureMessage:           f.Message,
			FailureMessageNormalized: prow.NormalizeForDedup(f.Message),
		})
	}
	for _, name := range parsed.Passed {
		testResults = append(testResults, store.TestResult{
			JobID:    jobID,
			TestName: name,
			Status:   "passed",
		})
	}

	if len(testResults) > 0 {
		if err := ing.store.InsertTestResults(ctx, testResults); err != nil {
			ing.log.Error(err, "failed to insert test results", "buildID", buildID)
			result.Errors++
			return nil
		}
	}

	result.NewJobs++
	return nil
}

// extractPRNumber extracts PR number from a GCS URL path.
func extractPRNumber(gcsURL string) int {
	parts := strings.Split(gcsURL, "/")
	for i, p := range parts {
		if p == "pull" && i+2 < len(parts) {
			var pr int
			fmt.Sscanf(parts[i+2], "%d", &pr)
			return pr
		}
	}
	return 0
}
