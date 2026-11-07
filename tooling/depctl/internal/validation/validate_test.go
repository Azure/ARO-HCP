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

package validation

import (
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
)

func planWith(ids ...string) model.Plan {
	plan := model.Plan{PlanID: "plan-1"}
	for _, id := range ids {
		plan.Consumers = append(plan.Consumers, model.ConsumerRef{ID: id})
	}
	return plan
}

func result(id, phase, status string) model.ConsumerResult {
	return model.ConsumerResult{ID: id, Phase: phase, Status: status}
}

func TestCompareVerdicts(t *testing.T) {
	cases := []struct {
		name    string
		results []model.ConsumerResult
		want    string
	}{
		{
			name: "both pass",
			results: []model.ConsumerResult{
				result("a", PhaseBaseline, model.ConsumerPassed),
				result("a", PhaseCandidate, model.ConsumerPassed),
			},
			want: model.VerdictValidated,
		},
		{
			name: "candidate regression",
			results: []model.ConsumerResult{
				result("a", PhaseBaseline, model.ConsumerPassed),
				result("a", PhaseCandidate, model.ConsumerFailed),
			},
			want: model.VerdictRegression,
		},
		{
			name: "baseline blocked",
			results: []model.ConsumerResult{
				result("a", PhaseBaseline, model.ConsumerFailed),
				result("a", PhaseCandidate, model.ConsumerPassed),
			},
			want: model.VerdictBaselineBlocked,
		},
		{
			name: "missing result",
			results: []model.ConsumerResult{
				result("a", PhaseBaseline, model.ConsumerPassed),
			},
			want: model.VerdictIncomplete,
		},
		{
			name: "not run",
			results: []model.ConsumerResult{
				result("a", PhaseBaseline, model.ConsumerPassed),
				result("a", PhaseCandidate, model.ConsumerNotRun),
			},
			want: model.VerdictIncomplete,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			verdict := Compare(planWith("a"), model.Candidate{CandidateID: "candidate-1"}, testCase.results)
			if verdict.Verdict != testCase.want {
				t.Fatalf("verdict = %q, want %q (notRun=%v)", verdict.Verdict, testCase.want, verdict.NotRun)
			}
			if verdict.CandidateID != "candidate-1" {
				t.Fatalf("candidate binding lost: %q", verdict.CandidateID)
			}
		})
	}
}

func TestCompareRegressionOutranksBaselineBlocked(t *testing.T) {
	verdict := Compare(planWith("a", "b"), model.Candidate{}, []model.ConsumerResult{
		result("a", PhaseBaseline, model.ConsumerFailed),
		result("a", PhaseCandidate, model.ConsumerPassed),
		result("b", PhaseBaseline, model.ConsumerPassed),
		result("b", PhaseCandidate, model.ConsumerFailed),
	})
	if verdict.Verdict != model.VerdictRegression {
		t.Fatalf("verdict = %q, want %q", verdict.Verdict, model.VerdictRegression)
	}
}

func TestMissingEvidenceListsBothPhases(t *testing.T) {
	missing := MissingEvidence(planWith("a"), nil)
	if len(missing) != 2 {
		t.Fatalf("expected both phases to be missing, got %v", missing)
	}
}
