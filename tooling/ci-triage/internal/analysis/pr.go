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
	"sort"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/config"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/prow"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/store"
)

// PR runs PR triage analysis from the store.
func PR(ctx context.Context, s *store.Store, prNumber int) (*PRResult, error) {
	var envResults []PREnvResult

	for _, env := range config.EnvNames() {
		cfg := config.Envs[env]
		if cfg.PresubmitJob == "" {
			continue
		}

		// Get all jobs for this PR in this env
		jobs, err := s.ListJobs(ctx, store.JobFilter{
			Env:     env,
			JobType: "presubmit",
			PR:      prNumber,
		})
		if err != nil {
			return nil, err
		}
		if len(jobs) == 0 {
			continue
		}

		passed := 0
		failed := 0
		for _, j := range jobs {
			switch j.State {
			case "success":
				passed++
			case "failure", "error":
				failed++
			}
		}

		// Get test failures for this PR
		prFailures, err := s.PRTestFailures(ctx, env, prNumber)
		if err != nil {
			return nil, err
		}

		// Group failures by test name
		type failEntry struct {
			count    int
			messages []string
			jobs     []string
		}
		failMap := make(map[string]*failEntry)
		for _, f := range prFailures {
			name := f.TestName
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
			e.jobs = append(e.jobs, config.ShortURL(f.BaseURL))
		}

		// Check baseline
		hasBaseline := cfg.PeriodicJob != ""
		baseline := make(map[string]bool)
		if hasBaseline {
			baseline, err = s.BaselineFailingTests(ctx, env, 20)
			if err != nil {
				return nil, err
			}
		}

		// Classify
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
			Total:       len(jobs),
			Passed:      passed,
			Failed:      failed,
			HasBaseline: hasBaseline,
			Failures:    classified,
		})
	}

	sort.Slice(envResults, func(i, j int) bool {
		return envResults[i].Env < envResults[j].Env
	})

	return &PRResult{
		PR:          prNumber,
		Envs:        envResults,
		FetchErrors: make(map[string]int),
	}, nil
}
