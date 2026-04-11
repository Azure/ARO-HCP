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
	"time"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/sippy"
)

// Timeline produces a time-series of job pass/fail for pattern recognition.
// Each entry includes rollout info and infrastructure failure flags to make
// deployment transitions and infra events visible.
func Timeline(ctx context.Context, sc *sippy.Client, env string, since time.Duration) (*TimelineResult, error) {
	response, err := sc.ListJobRuns(ctx, env, since)
	if err != nil {
		return nil, err
	}

	typicalTestCount := EstimateTypicalTestCount(response.Rows)

	entries := make([]TimelineEntry, 0, len(response.Rows))
	for _, run := range response.Rows {
		state := "success"
		if run.Failed() {
			state = "failure"
		}

		jobClass := ClassifyJob(run, typicalTestCount)
		isInfra := jobClass.Class == ClassInfrastructure

		entry := TimelineEntry{
			URL:                   run.URL,
			Started:               run.Timestamp(),
			State:                 state,
			Failed:                len(run.FailedTestNames),
			InfrastructureFailure: isInfra,
			Rollout:               run.Rollout(),
		}

		// Include failing test names so Claude can see patterns
		if len(run.FailedTestNames) > 0 {
			entry.TestNames = run.FailedTestNames
		}

		entries = append(entries, entry)
	}

	return &TimelineResult{
		Env:     env,
		Entries: entries,
	}, nil
}
