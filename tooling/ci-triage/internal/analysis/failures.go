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
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/config"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/gcs"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/prow"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/sippy"
)

// metaTestPrefixes are test name prefixes that are aggregate/meta results,
// not individual test failures. They always fail when any sub-test fails
// and inflate failure counts without adding signal.
var metaTestPrefixes = []string{
	"[sig-sippy]",
	"Job run should complete before timeout",
}

// isMetaTest returns true if the test name is a meta/aggregate test.
func isMetaTest(name string) bool {
	for _, prefix := range metaTestPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// maxParallelFetch limits concurrent GCS fetches.
const maxParallelFetch = 10

// Failures runs deep failure analysis for one environment using Sippy + GCS.
// Classifies failures, detects infrastructure events, checks cross-env context,
// and extracts EV2 deployment data from job annotations.
func Failures(ctx context.Context, sc *sippy.Client, env string, since time.Duration) (*FailuresResult, error) {
	log := logr.FromContextOrDiscard(ctx)

	if !sippy.HasEnv(env) {
		return &FailuresResult{
			Env:      env,
			PassRate: -1,
		}, nil
	}

	response, err := sc.ListJobRuns(ctx, env, since)
	if err != nil {
		return nil, err
	}

	// Estimate typical test count for wipeout detection
	typicalTestCount := EstimateTypicalTestCount(response.Rows)

	passed, failed := 0, 0
	type testInfo struct {
		count        int
		firstSeen    string
		lastSeen     string
		jobs         []string
		infraRunHits int // how many of the runs with this failure were infra-flagged
	}
	testMap := make(map[string]*testInfo)

	var perJobTests []PerJobEntry
	var infraJobs []InfraJobEntry

	for _, run := range response.Rows {
		if run.Failed() {
			failed++
		} else {
			passed++
		}

		// Classify the job as infrastructure or normal
		jobClass := ClassifyJob(run, typicalTestCount)
		isInfra := jobClass.Class == ClassInfrastructure

		if isInfra {
			infraJobs = append(infraJobs, InfraJobEntry{
				URL:            run.URL,
				Started:        run.Timestamp(),
				FailedTests:    len(run.FailedTestNames),
				Classification: jobClass,
			})
		}

		state := "success"
		if run.Failed() {
			state = "failure"
		}

		perJobTests = append(perJobTests, PerJobEntry{
			URL:                   run.URL,
			Started:               run.Timestamp(),
			State:                 state,
			Failed:                len(run.FailedTestNames),
			InfrastructureFailure: isInfra,
			Rollout:               run.Rollout(),
		})

		// Skip infra/wipeout jobs for test-level analysis — they inflate counts
		if isInfra {
			continue
		}

		for _, testName := range run.FailedTestNames {
			if isMetaTest(testName) {
				continue // tracked separately below
			}
			info, ok := testMap[testName]
			if !ok {
				info = &testInfo{firstSeen: run.Timestamp(), lastSeen: run.Timestamp()}
				testMap[testName] = info
			}
			info.count++
			if run.Timestamp() < info.firstSeen {
				info.firstSeen = run.Timestamp()
			}
			if run.Timestamp() > info.lastSeen {
				info.lastSeen = run.Timestamp()
			}
			info.jobs = append(info.jobs, run.URL)
			if run.InfrastructureFailure {
				info.infraRunHits++
			}
		}
	}

	// Onset detection: skip infra jobs — a wipeout where no tests ran should NOT
	// count as "this test passed"
	onsetMap := make(map[string]string)
	onsetRollouts := make(map[string]*sippy.RolloutInfo)
	for testName, tInfo := range testMap {
		for _, run := range response.Rows {
			jobClass := ClassifyJob(run, typicalTestCount)
			if jobClass.Class == ClassInfrastructure {
				continue
			}

			if !run.Failed() {
				// Successful job — all tests passed
				if run.Timestamp() < tInfo.firstSeen {
					onsetMap[testName] = run.Timestamp()
					onsetRollouts[testName] = run.Rollout()
					break
				}
			}
			if run.Failed() && !contains(run.FailedTestNames, testName) {
				if run.Timestamp() < tInfo.firstSeen {
					onsetMap[testName] = run.Timestamp()
					onsetRollouts[testName] = run.Rollout()
					break
				}
			}
		}
	}

	// Cross-env context
	otherEnvFailures := crossEnvCheck(ctx, sc, env, since, log)

	// Non-infra run count for classification
	nonInfraRuns := passed + failed - len(infraJobs)
	if nonInfraRuns < 0 {
		nonInfraRuns = 0
	}

	// Build failure groups with classification
	var failureGroups []FailureGroup
	for testName, info := range testMap {
		hasOnset := onsetMap[testName] != ""
		otherEnvs := otherEnvFailures[testName]
		infraFraction := 0.0
		if info.count > 0 {
			infraFraction = float64(info.infraRunHits) / float64(info.count)
		}

		classification := ClassifyTest(info.count, nonInfraRuns, hasOnset, otherEnvs, infraFraction)

		fg := FailureGroup{
			Test:           testName,
			Count:          info.count,
			FirstSeen:      info.firstSeen,
			LastSeen:       info.lastSeen,
			Classification: classification,
			OtherEnvs:      otherEnvs,
			Jobs:           info.jobs,
		}
		if lp, ok := onsetMap[testName]; ok {
			fg.LastPassed = lp
		}
		if rollout, ok := onsetRollouts[testName]; ok {
			fg.OnsetRollout = rollout
		}
		failureGroups = append(failureGroups, fg)
	}
	sort.Slice(failureGroups, func(i, j int) bool {
		return failureGroups[i].Count > failureGroups[j].Count
	})

	// Fetch failure messages: try Sippy first, then parallel GCS fetch
	fetchMessagesSippy(ctx, sc, env, failureGroups, log)
	fetchAllMessages(ctx, log, env, response, failureGroups)

	total := passed + failed
	passRate := 0.0
	if total > 0 {
		passRate = math.Round(float64(passed)/float64(total)*100) / 100
	}

	// Extract the most recent rollout info
	var currentRollout *sippy.RolloutInfo
	for _, run := range response.Rows {
		if r := run.Rollout(); r != nil {
			currentRollout = r
			break
		}
	}

	// Fleet context: pass rates of other envs
	fleetContext := make(map[string]float64)
	for _, otherEnv := range sippy.EnvNames() {
		if otherEnv == env {
			continue
		}
		otherResp, err := sc.ListJobRuns(ctx, otherEnv, since)
		if err != nil {
			continue
		}
		p, f := 0, 0
		for _, run := range otherResp.Rows {
			if run.Failed() {
				f++
			} else {
				p++
			}
		}
		t := p + f
		if t > 0 {
			fleetContext[otherEnv] = math.Round(float64(p)/float64(t)*100) / 100
		}
	}

	return &FailuresResult{
		Env:           env,
		Total:         total,
		Passed:        passed,
		Failed:        failed,
		PassRate:      passRate,
		FailureGroups: failureGroups,
		InfraJobs:     infraJobs,
		FleetContext:  fleetContext,
		Rollout:       currentRollout,
		PerJobTests:   perJobTests,
	}, nil
}

// crossEnvCheck queries Sippy for all other envs and returns a map of
// testName → list of other envs where that test is also failing.
func crossEnvCheck(ctx context.Context, sc *sippy.Client, currentEnv string, since time.Duration, log logr.Logger) map[string][]string {
	result := make(map[string][]string)

	for _, otherEnv := range sippy.EnvNames() {
		if otherEnv == currentEnv {
			continue
		}
		response, err := sc.ListJobRuns(ctx, otherEnv, since)
		if err != nil {
			log.V(1).Info("cross-env check skipped", "env", otherEnv, "error", err)
			continue
		}
		for _, run := range response.Rows {
			for _, testName := range run.FailedTestNames {
				envs := result[testName]
				if !contains(envs, otherEnv) {
					result[testName] = append(envs, otherEnv)
				}
			}
		}
	}

	return result
}

// fetchMessagesSippy fetches failure messages from Sippy's test outputs endpoint
// in parallel with a concurrency limit.
func fetchMessagesSippy(ctx context.Context, sc *sippy.Client, env string, groups []FailureGroup, log logr.Logger) {
	var (
		wg  sync.WaitGroup
		sem = make(chan struct{}, maxParallelFetch)
	)

	for i := range groups {
		if len(groups[i].Messages) > 0 {
			continue
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			outputs, err := sc.GetTestOutputs(ctx, env, groups[idx].Test)
			if err != nil {
				log.V(1).Info("sippy test outputs skipped", "test", groups[idx].Test, "error", err)
				return
			}
			if len(outputs) == 0 {
				return
			}
			msgs := make([]string, 0, len(outputs))
			for _, o := range outputs {
				if o.Output != "" {
					msgs = append(msgs, o.Output)
				}
			}
			if len(msgs) > 0 {
				groups[idx].Messages = prow.DedupMessages(msgs)
			}
		}(i)
	}
	wg.Wait()
}

// fetchAllMessages fetches extension test results from ALL failing jobs
// in parallel with a concurrency limit, falling back to JUnit.
func fetchAllMessages(ctx context.Context, log logr.Logger, env string, response *sippy.JobRunsResponse, groups []FailureGroup) {
	// Check if any groups still need messages
	needsMessages := false
	for _, g := range groups {
		if len(g.Messages) == 0 {
			needsMessages = true
			break
		}
	}
	if !needsMessages {
		return
	}

	cfg, ok := config.Envs[env]
	if !ok {
		return
	}

	gcsClient := gcs.NewClient(&http.Client{Timeout: 20 * time.Second})

	// Collect ALL failing job URLs
	var failingURLs []string
	for _, run := range response.Rows {
		if run.Failed() && run.URL != "" {
			failingURLs = append(failingURLs, run.URL)
		}
	}

	if len(failingURLs) == 0 {
		return
	}

	log.V(1).Info("fetching extension results", "jobs", len(failingURLs), "env", env)

	var (
		mu       sync.Mutex
		testMsgs = make(map[string][]string)
		sem      = make(chan struct{}, maxParallelFetch)
		wg       sync.WaitGroup
	)

	for _, baseURL := range failingURLs {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			normalized := config.NormalizeBaseURL(url)

			// Try extension test results first
			results, err := gcsClient.FetchExtensionResults(ctx, normalized, cfg.Step, cfg.Container)
			if err == nil && len(results) > 0 {
				mu.Lock()
				for _, r := range results {
					if r.Result == "failed" && r.Error != "" {
						testMsgs[r.Name] = append(testMsgs[r.Name], r.Error)
					}
				}
				mu.Unlock()
				return
			}

			// Fallback to JUnit
			data, err := gcsClient.FetchJUnit(ctx, normalized, cfg.Step, cfg.Container)
			if err != nil {
				log.V(1).Info("artifact fetch skipped", "url", url, "error", err)
				return
			}
			parsed := prow.ParseJUnit(data)
			if parsed == nil {
				return
			}
			mu.Lock()
			for _, f := range parsed.Failures {
				if f.Message != "" {
					testMsgs[f.Name] = append(testMsgs[f.Name], f.Message)
				}
			}
			mu.Unlock()
		}(baseURL)
	}

	wg.Wait()

	// Attach deduplicated messages to groups that don't have them yet
	for i := range groups {
		if len(groups[i].Messages) > 0 {
			continue
		}
		msgs, ok := testMsgs[groups[i].Test]
		if ok && len(msgs) > 0 {
			groups[i].Messages = prow.DedupMessages(msgs)
		}
	}
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
