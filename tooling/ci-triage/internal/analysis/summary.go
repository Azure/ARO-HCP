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
	"time"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/sippy"
)

// Summary runs a fleet-wide health scan using Sippy data.
func Summary(ctx context.Context, sc *sippy.Client, since time.Duration) (*SummaryResult, error) {
	// Track which tests fail in which envs for fleet correlation
	testEnvs := make(map[string]map[string]bool)

	var envSummaries []EnvSummary
	for _, env := range sippy.EnvNames() {
		response, err := sc.ListJobRuns(ctx, env, since)
		if err != nil {
			// Non-fatal: skip this env but continue
			envSummaries = append(envSummaries, EnvSummary{
				Env: env,
			})
			continue
		}

		typicalTestCount := EstimateTypicalTestCount(response.Rows)
		passed, failed, infraFailures := 0, 0, 0
		failingTests := make(map[string]int)
		flakySet := make(map[string]bool)

		for _, run := range response.Rows {
			if run.Failed() {
				failed++
			} else {
				passed++
			}

			// Count infrastructure failures
			jobClass := ClassifyJob(run, typicalTestCount)
			if jobClass.Class == ClassInfrastructure {
				infraFailures++
			}

			// Collect flaked tests
			for _, testName := range run.FlakedTestNames {
				flakySet[testName] = true
			}

			for _, testName := range run.FailedTestNames {
				if isMetaTest(testName) {
					continue
				}
				failingTests[testName]++
				if testEnvs[testName] == nil {
					testEnvs[testName] = make(map[string]bool)
				}
				testEnvs[testName][env] = true
			}
		}

		total := passed + failed
		passRate := 0.0
		if total > 0 {
			passRate = math.Round(float64(passed)/float64(total)*100) / 100
		}

		var flakyTests []string
		for t := range flakySet {
			flakyTests = append(flakyTests, t)
		}
		sort.Strings(flakyTests)

		envSummaries = append(envSummaries, EnvSummary{
			Env:           env,
			Total:         total,
			Passed:        passed,
			Failed:        failed,
			PassRate:      passRate,
			InfraFailures: infraFailures,
			FlakyTests:    flakyTests,
			TopFailures:   topN(failingTests, 5),
		})
	}

	sort.Slice(envSummaries, func(i, j int) bool {
		return envSummaries[i].Env < envSummaries[j].Env
	})

	// Fleet-wide failures: tests failing in 2+ envs
	var fleet []FleetFailure
	for test, envMap := range testEnvs {
		if len(envMap) < 2 {
			continue
		}
		var envs []string
		for e := range envMap {
			envs = append(envs, e)
		}
		sort.Strings(envs)

		classification := Classification{
			Class:      ClassFleetWide,
			Confidence: "high",
			Reason:     "failing across multiple environments — likely code change",
		}

		fleet = append(fleet, FleetFailure{
			Test:           test,
			Envs:           envs,
			Classification: classification,
		})
	}
	sort.Slice(fleet, func(i, j int) bool {
		return len(fleet[i].Envs) > len(fleet[j].Envs)
	})

	return &SummaryResult{
		Envs:          envSummaries,
		FleetFailures: fleet,
	}, nil
}

// topN returns the top N keys by value from a frequency map.
func topN(m map[string]int, n int) []string {
	type kv struct {
		Key   string
		Count int
	}
	var sorted []kv
	for k, v := range m {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Count > sorted[j].Count
	})
	result := make([]string, 0, n)
	for i, kv := range sorted {
		if i >= n {
			break
		}
		result = append(result, kv.Key)
	}
	return result
}
