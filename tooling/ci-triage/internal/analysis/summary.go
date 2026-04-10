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
	"strings"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/config"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/store"
)

// Summary runs a cross-environment health scan from the store.
func Summary(ctx context.Context, s *store.Store, since, until string) (*SummaryResult, error) {
	var envSummaries []EnvSummary

	for _, env := range config.EnvNames() {
		for _, jt := range config.JobTypes(env) {
			counts, err := s.JobStateCounts(ctx, env, jt, since, until)
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

			topFails, err := s.TopFailures(ctx, env, jt, since, 5)
			if err != nil {
				return nil, err
			}

			envSummaries = append(envSummaries, EnvSummary{
				Env:         env,
				Type:        jt,
				Passed:      passed,
				Failed:      failed,
				Aborted:     aborted,
				PassRate:    passRate,
				TopFailures: topFails,
			})
		}
	}

	sort.Slice(envSummaries, func(i, j int) bool {
		if envSummaries[i].Env != envSummaries[j].Env {
			return envSummaries[i].Env < envSummaries[j].Env
		}
		return envSummaries[i].Type < envSummaries[j].Type
	})

	// Fleet-wide failure correlation
	fleetRows, err := s.FleetFailures(ctx, since, 10)
	if err != nil {
		return nil, err
	}

	var fleet []FleetFailure
	for _, row := range fleetRows {
		envs := strings.Split(row.Envs, ",")
		sort.Strings(envs)
		fleet = append(fleet, FleetFailure{
			Test: row.TestName,
			Envs: envs,
		})
	}

	return &SummaryResult{
		Envs:          envSummaries,
		FleetFailures: fleet,
		FetchErrors:   make(map[string]int),
	}, nil
}
