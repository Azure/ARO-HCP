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

package testkit

import (
	"context"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
)

func TestScenarios(t *testing.T) {
	cases := []struct {
		name  string
		check func(t *testing.T, report Report)
	}{
		{
			name: ScenarioLifecycle,
			check: func(t *testing.T, report Report) {
				if report.Run.Outcome != model.OutcomeAwaitingAuthorization {
					t.Fatalf("outcome = %q (%v)", report.Run.Outcome, report.Run.Errors)
				}
				if report.Validation == nil || report.Validation.Verdict != model.VerdictValidated {
					t.Fatalf("validation = %+v", report.Validation)
				}
				if report.Publication == nil || !report.Publication.Confirmed {
					t.Fatalf("publication = %+v", report.Publication)
				}
				if len(report.Mutations) < 2 {
					t.Fatalf("expected branch and proposal mutations, got %+v", report.Mutations)
				}
				if report.Maintenance == nil {
					t.Fatal("expected a maintenance decision")
				}
				if report.Maintenance.Held || report.Maintenance.Action != "wait" {
					t.Fatalf("a green unheld proposal must wait: action=%q held=%t", report.Maintenance.Action, report.Maintenance.Held)
				}
				if report.Continuation == nil || !report.Continuation.Eligible {
					t.Fatalf("continuation = %+v", report.Continuation)
				}
				if report.Continuation.Target != "tool-version-updates" {
					t.Fatalf("continuation target = %q", report.Continuation.Target)
				}
				pullRequestURL := report.Publication.PullRequestURL
				if !strings.Contains(pullRequestURL, "/pull/") {
					t.Fatalf("unexpected proposal URL %q", pullRequestURL)
				}
			},
		},
		{
			name: ScenarioNoUpdate,
			check: func(t *testing.T, report Report) {
				if report.Run.Outcome != model.OutcomeNoChange {
					t.Fatalf("outcome = %q (%v)", report.Run.Outcome, report.Run.Errors)
				}
				if !report.Run.Complete {
					t.Fatal("a locally complete inventory must report a complete no-change")
				}
				if report.Proposal != nil || report.Publication != nil {
					t.Fatal("a no-change run must not propose or publish")
				}
				if len(report.Mutations) != 0 {
					t.Fatalf("a no-change run must not mutate: %+v", report.Mutations)
				}
			},
		},
		{
			name: ScenarioGenerationFails,
			check: func(t *testing.T, report Report) {
				if report.Run.Outcome != model.OutcomeFailed {
					t.Fatalf("outcome = %q (%v)", report.Run.Outcome, report.Run.Errors)
				}
				if report.Run.Phase != model.PhasePrepare {
					t.Fatalf("phase = %q", report.Run.Phase)
				}
				if report.Proposal != nil || report.Publication != nil {
					t.Fatal("a failed generation must not propose or publish")
				}
				if len(report.Mutations) != 0 {
					t.Fatalf("a failed generation must not mutate: %+v", report.Mutations)
				}
			},
		},
		{
			name: ScenarioValidationRegression,
			check: func(t *testing.T, report Report) {
				if report.Run.Outcome != model.OutcomeValidationFailed {
					t.Fatalf("outcome = %q (%v)", report.Run.Outcome, report.Run.Errors)
				}
				if report.Validation == nil || report.Validation.Verdict != model.VerdictRegression {
					t.Fatalf("validation = %+v", report.Validation)
				}
				if report.Publication != nil || len(report.Mutations) != 0 {
					t.Fatal("a candidate regression must not publish")
				}
			},
		},
		{
			name: ScenarioCIPinPair,
			check: func(t *testing.T, report Report) {
				if report.Run.Outcome != model.OutcomeAwaitingAuthorization {
					t.Fatalf("outcome = %q (%v)", report.Run.Outcome, report.Run.Errors)
				}
				if report.Validation == nil || report.Validation.Verdict != model.VerdictValidated {
					t.Fatalf("validation = %+v", report.Validation)
				}
				if report.Proposal == nil {
					t.Fatal("expected a proposal for the paired CI pin")
				}
				want := []string{".bingo/Variables.mk"}
				_ = want
				for _, path := range []string{"dev-infrastructure/openshift-ci/versions.mk", "dev-infrastructure/openshift-ci/Dockerfile"} {
					found := false
					for _, changed := range report.Candidate.ChangedPaths {
						if changed == path {
							found = true
						}
					}
					if !found {
						t.Fatalf("expected %s in the candidate changes: %v", path, report.Candidate.ChangedPaths)
					}
				}
			},
		},
		{
			name: ScenarioHeldFailure,
			check: func(t *testing.T, report Report) {
				if report.Maintenance == nil || report.Maintenance.Investigation == nil {
					t.Fatal("expected an investigation record")
				}
				investigation := report.Maintenance.Investigation
				if investigation.Diagnosis != model.DiagnosisUnrelatedFailure {
					t.Fatalf("diagnosis = %q (missing %v)", investigation.Diagnosis, investigation.Missing)
				}
				if !anyAccepted(investigation.Comparisons) {
					t.Fatalf("expected an accepted comparison: %+v", investigation.Comparisons)
				}
				if report.Maintenance.CanReleaseHold {
					t.Fatal("a failing current head must not release the hold")
				}
				if report.Maintenance.Action != "recover-retry" {
					t.Fatalf("action = %q (%s)", report.Maintenance.Action, report.Maintenance.ActionReason)
				}
				if len(report.Maintenance.ActiveHolds) == 0 {
					t.Fatal("the retry-exhaustion hold must be preserved in the decision")
				}
				if investigation.Attempt < 1 {
					t.Fatalf("attempt = %d", investigation.Attempt)
				}
				for _, item := range investigation.Evidence {
					if item.Status == "retrieved" && item.Digest == "" {
						t.Fatalf("retrieved evidence must carry a digest: %+v", item)
					}
				}
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			report, err := RunScenario(context.Background(), testCase.name, t.TempDir())
			if err != nil {
				t.Fatalf("RunScenario() error = %v", err)
			}
			testCase.check(t, report)
		})
	}
}

func anyAccepted(comparisons []model.Comparison) bool {
	for _, comparison := range comparisons {
		if comparison.Accepted {
			return true
		}
	}
	return false
}

func TestScenarioReportIsHumanReadable(t *testing.T) {
	report, err := RunScenario(context.Background(), ScenarioLifecycle, t.TempDir())
	if err != nil {
		t.Fatalf("RunScenario() error = %v", err)
	}
	rendered := FormatReport(report)
	for _, want := range []string{"scenario: lifecycle", "outcome:", "validation: validated", "continuation: eligible=true"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("report is missing %q:\n%s", want, rendered)
		}
	}
}
