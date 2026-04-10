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
	"math"
	"sort"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/config"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/prow"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/store"
)

// Failures runs failure analysis for all job types in an environment.
func Failures(ctx context.Context, s *store.Store, env, since, until string) (*FailuresResult, error) {
	cfg, ok := config.Envs[env]
	if !ok {
		return nil, &config.UnknownEnvError{Env: env}
	}

	var results []FailureSummary
	jobTypes := config.JobTypes(env)
	for _, jt := range jobTypes {
		fs, err := failureSummary(ctx, s, env, jt, cfg, since, until)
		if err != nil {
			return nil, err
		}
		results = append(results, *fs)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].PassRate < results[j].PassRate
	})

	return &FailuresResult{
		Env:         env,
		Results:     results,
		FetchErrors: make(map[string]int),
	}, nil
}

func failureSummary(ctx context.Context, s *store.Store, env, jobType string, _ config.EnvConfig, since, until string) (*FailureSummary, error) {
	// Job state counts
	counts, err := s.JobStateCounts(ctx, env, jobType, since, until)
	if err != nil {
		return nil, err
	}

	passed := counts["success"]
	failed := counts["failure"] + counts["error"]
	aborted := counts["aborted"]
	completed := passed + failed
	passRate := 0.0
	if completed > 0 {
		passRate = math.Round(float64(passed)/float64(completed)*100) / 100
	}

	// Failure groups
	groups, err := s.FailureGroups(ctx, env, jobType, since, until)
	if err != nil {
		return nil, err
	}

	// Onset detection
	testNames := make([]string, len(groups))
	for i, g := range groups {
		testNames[i] = g.TestName
	}
	onsetMap, err := s.OnsetMap(ctx, env, jobType, testNames)
	if err != nil {
		return nil, err
	}

	// Build failure groups with messages, jobs, PRs
	var failureGroups []FailureGroup
	for _, g := range groups {
		msgs, err := s.FailureMessages(ctx, env, jobType, since, g.TestName)
		if err != nil {
			return nil, err
		}

		var dedupedMsgs []prow.DedupedMessage
		for _, m := range msgs {
			dedupedMsgs = append(dedupedMsgs, prow.DedupedMessage{
				Msg:   m.Representative,
				Count: m.Count,
			})
		}

		jobURLs, err := s.FailureJobURLs(ctx, env, jobType, since, g.TestName)
		if err != nil {
			return nil, err
		}
		shortURLs := make([]string, len(jobURLs))
		for i, u := range jobURLs {
			shortURLs[i] = config.ShortURL(u)
		}

		prs, err := s.FailurePRs(ctx, env, jobType, since, g.TestName)
		if err != nil {
			return nil, err
		}

		fg := FailureGroup{
			Test:      g.TestName,
			Count:     g.Count,
			Jobs:      shortURLs,
			FirstSeen: g.FirstSeen,
			LastSeen:  g.LastSeen,
			Messages:  dedupedMsgs,
			PRs:       prs,
		}
		if lp, ok := onsetMap[g.TestName]; ok {
			fg.LastPassed = lp
		}
		failureGroups = append(failureGroups, fg)
	}

	// Per-job test entries
	perJobRows, err := s.PerJobTests(ctx, env, jobType, since, until)
	if err != nil {
		return nil, err
	}
	var perJobTests []PerJobEntry
	for _, r := range perJobRows {
		perJobTests = append(perJobTests, PerJobEntry{
			Job:      config.ShortURL(r.BaseURL),
			Started:  r.Started,
			Passed:   r.Passed,
			Failed:   r.Failed,
			Revision: r.Revision,
		})
	}

	return &FailureSummary{
		Env:           env,
		Type:          jobType,
		Passed:        passed,
		Failed:        failed,
		Aborted:       aborted,
		PassRate:      passRate,
		FailureGroups: failureGroups,
		PerJobTests:   perJobTests,
	}, nil
}
