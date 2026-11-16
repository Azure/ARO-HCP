// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cifailures

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/cifailurepatterns"
)

func TestPatternCountsPreserveOccurrencesAndOverlappingMemberships(t *testing.T) {
	observation := Observation{Environment: "dev", BuildID: "run-1", Text: "identical evidence"}
	observation.Fingerprint = Fingerprint(observation)
	original := Result{Observation: observation, Findings: []cifailurepatterns.Finding{
		{Key: "a", Canonical: "Z description", Outcome: "matched", RuleID: "z", QualityReasons: []string{"z", "a", "a"}},
		{Key: "b", Canonical: "Other", Outcome: "fallback", RuleID: "fallback"},
	}}
	results := []Result{
		original, original,
		{Observation: Observation{Environment: "dev", BuildID: "run-2"}, Findings: []cifailurepatterns.Finding{
			{Key: "a", Canonical: "A description", Outcome: "fallback", RuleID: "a", QualityReasons: []string{"a"}},
			{Key: "a", Canonical: "B description", Outcome: "matched", RuleID: "z", QualityReasons: []string{"z"}},
		}},
		{Observation: Observation{Environment: "dev", Source: "e2e", BuildID: "run-1"}, Findings: []cifailurepatterns.Finding{
			{Key: "a", Canonical: "Separate source", Outcome: "matched", RuleID: "source"},
		}},
		{Observation: Observation{Environment: "int", BuildID: "run-3"}, Findings: []cifailurepatterns.Finding{
			{Key: "a", Canonical: "Separate environment", Outcome: "matched", RuleID: "environment"},
		}},
		{Observation: Observation{Environment: "dev", Source: "rp-upgrade", BuildID: "run-1"}, Findings: []cifailurepatterns.Finding{
			{Key: "a", Canonical: "Separate pipeline", Outcome: "matched", RuleID: "pipeline"},
		}},
		{Observation: Observation{Environment: "dev", BuildID: "run-4"}, Findings: []cifailurepatterns.Finding{
			{Key: "b", Canonical: "Missing", Outcome: "missing_evidence", RuleID: "missing", QualityReasons: []string{"weak"}},
		}},
	}
	before, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	summary, patterns := groupResults(results)
	wantSummary := Summary{InputCount: 7, FindingCount: 10, PatternCount: 5, AffectedRunCount: 4,
		SourceInputCounts: map[string]int{"": 5, "e2e": 1, "rp-upgrade": 1}}
	if !reflect.DeepEqual(summary, wantSummary) {
		t.Fatalf("global counts must not sum overlapping patterns: got %+v, want %+v", summary, wantSummary)
	}
	want := []Pattern{
		{Environment: "dev", Key: "a", Canonical: "A description", AffectedInputCount: 3, AffectedRunCount: 2,
			OutcomeCounts: map[string]int{"matched": 3, "fallback": 1}, RuleIDs: []string{"a", "z"}, QualityReasons: []string{"a", "z"}, ResultIndexes: []int{0, 1, 2}},
		{Environment: "dev", Key: "b", Canonical: "Missing", AffectedInputCount: 3, AffectedRunCount: 2,
			OutcomeCounts: map[string]int{"fallback": 2, "missing_evidence": 1}, RuleIDs: []string{"fallback", "missing"}, QualityReasons: []string{"weak"}, ResultIndexes: []int{0, 1, 6}},
		{Environment: "dev", Source: "e2e", Key: "a", Canonical: "Separate source", AffectedInputCount: 1, AffectedRunCount: 1,
			OutcomeCounts: map[string]int{"matched": 1}, RuleIDs: []string{"source"}, QualityReasons: []string{}, ResultIndexes: []int{3}},
		{Environment: "dev", Source: "rp-upgrade", Key: "a", Canonical: "Separate pipeline", AffectedInputCount: 1, AffectedRunCount: 1,
			OutcomeCounts: map[string]int{"matched": 1}, RuleIDs: []string{"pipeline"}, QualityReasons: []string{}, ResultIndexes: []int{5}},
		{Environment: "int", Key: "a", Canonical: "Separate environment", AffectedInputCount: 1, AffectedRunCount: 1,
			OutcomeCounts: map[string]int{"matched": 1}, RuleIDs: []string{"environment"}, QualityReasons: []string{}, ResultIndexes: []int{4}},
	}
	if !reflect.DeepEqual(patterns, want) {
		t.Fatalf("incorrect strict groups, counts, or evidence references:\ngot  %+v\nwant %+v", patterns, want)
	}
	after, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("aggregation changed raw results or finding order")
	}
}

func TestPatternRankingAndCanonicalSelectionAreStable(t *testing.T) {
	// Two runs outrank three occurrences from one run; input count breaks run ties.
	results := []Result{
		{Observation: Observation{Environment: "dev", BuildID: "run-1"}, Findings: []cifailurepatterns.Finding{
			{Key: "a", Canonical: "Z"}, {Key: "z", Canonical: "Z"},
		}},
		{Observation: Observation{Environment: "dev", BuildID: "run-1"}, Findings: []cifailurepatterns.Finding{{Key: "a", Canonical: "A"}}},
		{Observation: Observation{Environment: "dev", BuildID: "run-1"}, Findings: []cifailurepatterns.Finding{{Key: "a", Canonical: "B"}}},
		{Observation: Observation{Environment: "dev", BuildID: "run-2"}, Findings: []cifailurepatterns.Finding{{Key: "z", Canonical: "A"}}},
		{Observation: Observation{Environment: "dev", BuildID: "run-1"}, Findings: []cifailurepatterns.Finding{{Key: "b", Canonical: "B"}}},
	}
	for permutation := range 3 {
		_, patterns := groupResults(results)
		keys := make([]string, len(patterns))
		for i, pattern := range patterns {
			keys[i] = pattern.Key
		}
		if !slices.Equal(keys, []string{"z", "a", "b"}) || patterns[0].Canonical != "A" || patterns[1].Canonical != "A" {
			t.Fatalf("permutation %d changed ranking or canonical selection: %+v", permutation, patterns)
		}
		slices.Reverse(results)
		if permutation == 1 {
			results = append(results[1:], results[0])
		}
	}
}

// Check the actual BuildReport path, including all large/duplicate/multi-finding fixtures.
func checkPatternReferences(t *testing.T, report Report) {
	t.Helper()
	type membership struct {
		environment, source, key string
		index                    int
	}
	want := make(map[membership]bool)
	for index, result := range report.Results {
		for _, finding := range result.Findings {
			want[membership{result.Observation.Environment, result.Observation.Source, finding.Key, index}] = true
		}
	}
	for _, pattern := range report.Patterns {
		if pattern.AffectedInputCount != len(pattern.ResultIndexes) {
			t.Fatalf("input count differs from evidence membership: %+v", pattern)
		}
		previous := -1
		for _, index := range pattern.ResultIndexes {
			member := membership{pattern.Environment, pattern.Source, pattern.Key, index}
			if index <= previous || index >= len(report.Results) || !want[member] {
				t.Fatalf("invalid, repeated, unsorted, or unrelated result reference: %+v", pattern)
			}
			delete(want, member)
			previous = index
		}
	}
	if len(want) != 0 {
		t.Fatalf("pattern index omitted %d input memberships", len(want))
	}
}
