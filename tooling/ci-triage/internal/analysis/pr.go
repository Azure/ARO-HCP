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

package analysis

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/config"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/gcs"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/github"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/prow"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/sippy"
)

// PR runs PR triage analysis using GCS for PR builds + Sippy for baseline.
func PR(ctx context.Context, sc *sippy.Client, prNumber int) (*PRResult, error) {
	log := logr.FromContextOrDiscard(ctx)
	gcsClient := gcs.NewClient(&http.Client{Timeout: 20 * time.Second})

	var envResults []PREnvResult

	for _, env := range config.EnvNames() {
		cfg := config.Envs[env]
		if cfg.PresubmitJob == "" {
			continue
		}

		// List builds for this PR from GCS
		builds, err := gcsClient.ListPRBuilds(ctx, prNumber, cfg.PresubmitJob)
		if err != nil {
			log.V(1).Info("pr build list skipped", "env", env, "error", err)
			continue
		}
		if len(builds) == 0 {
			continue
		}

		passed, failed := 0, 0
		type failEntry struct {
			count    int
			messages []string
			jobs     []string
		}
		failMap := make(map[string]*failEntry)

		for _, buildID := range builds {
			baseURL := config.GCSWebBase + "/logs/" + cfg.PresubmitJob + "/" + buildID
			finished, err := gcsClient.FetchFinished(ctx, baseURL)
			if err != nil {
				continue
			}

			switch finished.Result {
			case "SUCCESS":
				passed++
			case "FAILURE", "ERROR":
				failed++
			default:
				continue
			}

			// Fetch JUnit for failing builds
			if finished.Result != "SUCCESS" {
				data, err := gcsClient.FetchJUnit(ctx, baseURL, cfg.Step, cfg.Container)
				if err != nil {
					continue
				}
				result := prow.ParseJUnit(data)
				if result == nil {
					continue
				}
				for _, f := range result.Failures {
					name := f.Name
					if name == "" {
						name = "unknown"
					}
					e, ok := failMap[name]
					if !ok {
						e = &failEntry{}
						failMap[name] = e
					}
					e.count++
					if f.Message != "" {
						e.messages = append(e.messages, f.Message)
					}
					e.jobs = append(e.jobs, config.ShortURL(baseURL))
				}
			}
		}

		// Baseline comparison using Sippy periodic data
		hasBaseline := cfg.PeriodicJob != "" && sippy.HasEnv(env)
		baseline := make(map[string]bool)
		if hasBaseline {
			baseline = baselineFromSippy(ctx, sc, env, log)
		}

		// Classify failures
		var classified []ClassifiedFail
		for name, e := range failMap {
			classified = append(classified, ClassifiedFail{
				Test:     name,
				Count:    e.count,
				Baseline: baseline[name],
				Messages: prow.DedupMessages(e.messages),
				Jobs:     e.jobs,
			})
		}
		sort.Slice(classified, func(i, j int) bool {
			return classified[i].Count > classified[j].Count
		})

		envResults = append(envResults, PREnvResult{
			Env:         env,
			Total:       passed + failed,
			Passed:      passed,
			Failed:      failed,
			HasBaseline: hasBaseline,
			Failures:    classified,
		})
	}

	sort.Slice(envResults, func(i, j int) bool {
		return envResults[i].Env < envResults[j].Env
	})

	result := &PRResult{
		PR:   prNumber,
		Envs: envResults,
	}

	// Enrich with GitHub PR metadata (non-fatal if unavailable)
	if detail, err := github.GetPR(ctx, prNumber); err == nil && detail != nil {
		result.Title = detail.Title
		result.Author = detail.Author
		result.MergedAt = detail.MergedAt
		result.ChangedFiles = detail.Files
	}

	return result, nil
}

// baselineFromSippy queries Sippy for recent periodic job failures to build
// a baseline set of currently-failing tests.
func baselineFromSippy(ctx context.Context, sc *sippy.Client, env string, log logr.Logger) map[string]bool {
	baseline := make(map[string]bool)
	response, err := sc.ListJobRuns(ctx, env, 7*24*time.Hour) // 7 days
	if err != nil {
		log.V(1).Info("baseline query failed", "env", env, "error", err)
		return baseline
	}

	for _, run := range response.Rows {
		for _, testName := range run.FailedTestNames {
			baseline[testName] = true
		}
	}
	return baseline
}
