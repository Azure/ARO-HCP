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

package cifailures

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestEnvironmentSelectionAndReplayPreserveClassifications(t *testing.T) {
	metadata := testBundle().Metadata
	metadata.Environment = ""
	var runs []runRow
	var observations []observationRow
	for _, tc := range []struct{ id, job string }{
		{"dev", "pull-ci-Azure-ARO-HCP-main-e2e-parallel"},
		{"prod", "branch-ci-Azure-ARO-HCP-main-e2e-prod-e2e-parallel"},
		{"unknown", "custom-e2e-parallel"},
	} {
		runs = append(runs, runRow{BuildID: tc.id, JobName: tc.job, RunFinishedAt: metadata.Window.From})
		observations = append(observations, observationRow{BuildID: tc.id, TestID: "test", Message: "failure"})
	}
	all, err := assembleBundle(metadata, runs, observations)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Runs) != 3 || len(all.Observations) != 3 {
		t.Fatalf("lost environment population: %+v", all)
	}
	for _, run := range all.Runs {
		if run.Environment != run.BuildID {
			t.Fatalf("wrong classification: %+v", run)
		}
	}
	data, err := json.Marshal(all)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := ReadBundle(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range replay.Runs {
		if run.Environment != run.BuildID {
			t.Fatalf("replay reclassified: %+v", run)
		}
	}
	metadata.Environment = "prod"
	selected, err := assembleBundle(metadata, runs, observations)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Runs) != 1 || selected.Runs[0].BuildID != "prod" || len(selected.Observations) != 1 || selected.Observations[0].Environment != "prod" {
		t.Fatalf("environment filter did not select population: %+v", selected)
	}
	replay.Observations[0].Environment = "tampered"
	if err := replay.Validate(); err == nil {
		t.Fatal("accepted observation/run environment disagreement")
	}
}

func TestLegacyReplayRetainsRecordedEnvironment(t *testing.T) {
	bundle := testBundle()
	bundle.SchemaVersion = 1
	for i := range bundle.Runs {
		bundle.Runs[i].Environment = ""
		bundle.Runs[i].JobName = "branch-ci-Azure-ARO-HCP-main-e2e-prod-e2e-parallel"
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := ReadBundle(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if replay.Metadata.Environment != "dev" || replay.Observations[0].Environment != "dev" || replay.Runs[0].Environment != "" {
		t.Fatalf("legacy context was reinterpreted: %+v", replay)
	}
	report, err := BuildReport(replay)
	if err != nil {
		t.Fatal(err)
	}
	if report.Metadata.Environment != "dev" || len(report.Results) != 1 || report.Results[0].Observation.Environment != "dev" {
		t.Fatalf("legacy context changed: %+v", report)
	}
	if report.Results[0].Observation != replay.Observations[0] {
		t.Fatalf("legacy observation was reinterpreted: %+v", report.Results[0].Observation)
	}
}
