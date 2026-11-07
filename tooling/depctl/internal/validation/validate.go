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

// Package validation compares baseline and candidate consumer evidence.
package validation

import (
	"fmt"
	"sort"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/schema"
)

// PhaseBaseline and PhaseCandidate are the two consumer phases.
const (
	PhaseBaseline  = "baseline"
	PhaseCandidate = "candidate"
)

// Compare decides whether a candidate is validated.
//
// A baseline pass with a candidate failure is a regression. A baseline failure
// blocks validation but proves nothing about the candidate. A consumer that did
// not run leaves the evidence incomplete; it never counts as a pass.
func Compare(plan model.Plan, candidate model.Candidate, results []model.ConsumerResult) model.Validation {
	ordered := append([]model.ConsumerResult{}, results...)
	sort.SliceStable(ordered, func(left, right int) bool {
		if ordered[left].ID != ordered[right].ID {
			return ordered[left].ID < ordered[right].ID
		}
		return ordered[left].Phase < ordered[right].Phase
	})
	validation := model.Validation{
		Envelope:    schema.Envelope{SchemaVersion: schema.Version, Kind: model.KindValidation},
		PlanID:      plan.PlanID,
		CandidateID: candidate.CandidateID,
		Results:     ordered,
	}
	outcome := model.VerdictValidated
	for _, required := range plan.Consumers {
		baseline := find(ordered, required.ID, PhaseBaseline)
		candidateResult := find(ordered, required.ID, PhaseCandidate)
		switch {
		case baseline == nil || candidateResult == nil:
			validation.NotRun = append(validation.NotRun, required.ID+": result missing")
			outcome = escalate(outcome, model.VerdictIncomplete)
		case baseline.Status == model.ConsumerNotRun || candidateResult.Status == model.ConsumerNotRun:
			validation.NotRun = append(validation.NotRun, required.ID+": "+notRunReason(baseline, candidateResult))
			outcome = escalate(outcome, model.VerdictIncomplete)
		case baseline.Status == model.ConsumerFailed || baseline.Status == model.ConsumerError:
			validation.NotRun = append(validation.NotRun, required.ID+": baseline did not pass")
			outcome = escalate(outcome, model.VerdictBaselineBlocked)
		case candidateResult.Status == model.ConsumerFailed || candidateResult.Status == model.ConsumerError:
			outcome = escalate(outcome, model.VerdictRegression)
		}
	}
	validation.Verdict = outcome
	validation.Complete = outcome == model.VerdictValidated || outcome == model.VerdictRegression
	return validation
}

// Keeps the most severe verdict: regression outranks baseline blocked,
// which outranks incomplete.
// Keeps the most severe verdict: regression outranks baseline blocked,
// which outranks incomplete.
func escalate(current, next string) string {
	rank := map[string]int{
		model.VerdictValidated:       0,
		model.VerdictIncomplete:      1,
		model.VerdictBaselineBlocked: 2,
		model.VerdictRegression:      3,
	}
	if rank[next] > rank[current] {
		return next
	}
	return current
}

// Returns the first not-run reason of a consumer pair.
func notRunReason(results ...*model.ConsumerResult) string {
	for _, result := range results {
		if result != nil && result.Status == model.ConsumerNotRun {
			if result.Reason != "" {
				return result.Reason
			}
		}
	}
	return "consumer did not run"
}

// Returns the result for one consumer and phase.
func find(results []model.ConsumerResult, id, phase string) *model.ConsumerResult {
	for index := range results {
		if results[index].ID == id && results[index].Phase == phase {
			return &results[index]
		}
	}
	return nil
}

// RequiredConsumers returns the sorted consumer IDs a plan requires.
func RequiredConsumers(plan model.Plan) []string {
	ids := make([]string, 0, len(plan.Consumers))
	seen := map[string]bool{}
	for _, consumer := range plan.Consumers {
		if seen[consumer.ID] {
			continue
		}
		seen[consumer.ID] = true
		ids = append(ids, consumer.ID)
	}
	sort.Strings(ids)
	return ids
}

// MissingEvidence describes required consumers with no result at all.
func MissingEvidence(plan model.Plan, results []model.ConsumerResult) []string {
	var missing []string
	for _, id := range RequiredConsumers(plan) {
		for _, phase := range []string{PhaseBaseline, PhaseCandidate} {
			if find(results, id, phase) == nil {
				missing = append(missing, fmt.Sprintf("%s/%s", id, phase))
			}
		}
	}
	return missing
}
