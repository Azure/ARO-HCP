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
	"sort"

	"k8s.io/apimachinery/pkg/util/sets"
)

type patternIdentity struct {
	environment string
	source      string
	key         string
}

func groupResults(results []Result) (Summary, []Pattern) {
	summary := Summary{InputCount: len(results), SourceInputCounts: make(map[string]int)}
	// Bundle validation guarantees one origin and unique build IDs across environments.
	runs := sets.New[string]()
	type group struct {
		pattern Pattern
		runs    sets.Set[string]
		rules   sets.Set[string]
		reasons sets.Set[string]
	}
	groups := make(map[patternIdentity]*group)
	for index := range results {
		result := &results[index]
		observation := &result.Observation
		summary.SourceInputCounts[observation.Source]++
		summary.FindingCount += len(result.Findings)
		runs.Insert(observation.BuildID)
		for i := range result.Findings {
			finding := &result.Findings[i]
			identity := patternIdentity{observation.Environment, observation.Source, finding.Key}
			g := groups[identity]
			if g == nil {
				g = &group{
					pattern: Pattern{Environment: observation.Environment, Source: observation.Source,
						Key: finding.Key, Canonical: finding.Canonical, OutcomeCounts: make(map[string]int)},
					runs: sets.New[string](), rules: sets.New[string](), reasons: sets.New[string](),
				}
				groups[identity] = g
			}
			p := &g.pattern
			if finding.Canonical < p.Canonical {
				p.Canonical = finding.Canonical
			}
			p.OutcomeCounts[finding.Outcome]++
			g.runs.Insert(observation.BuildID)
			g.rules.Insert(finding.RuleID)
			g.reasons.Insert(finding.QualityReasons...)
			// Indexes increase monotonically; repeated findings in this input count once.
			// Fingerprints cannot distinguish separate identical input occurrences.
			if len(p.ResultIndexes) == 0 || p.ResultIndexes[len(p.ResultIndexes)-1] != index {
				p.ResultIndexes = append(p.ResultIndexes, index)
			}
		}
	}

	patterns := make([]Pattern, 0, len(groups))
	for _, g := range groups {
		p := &g.pattern
		p.AffectedInputCount = len(p.ResultIndexes)
		p.AffectedRunCount = g.runs.Len()
		p.RuleIDs = sets.List(g.rules)
		p.QualityReasons = sets.List(g.reasons)
		patterns = append(patterns, *p)
	}
	sort.Slice(patterns, func(i, j int) bool {
		left, right := &patterns[i], &patterns[j]
		switch {
		case left.AffectedRunCount != right.AffectedRunCount:
			return left.AffectedRunCount > right.AffectedRunCount
		case left.AffectedInputCount != right.AffectedInputCount:
			return left.AffectedInputCount > right.AffectedInputCount
		case left.Environment != right.Environment:
			return left.Environment < right.Environment
		case left.Source != right.Source:
			return left.Source < right.Source
		default:
			return left.Key < right.Key
		}
	})
	summary.PatternCount = len(patterns)
	summary.AffectedRunCount = runs.Len()
	return summary, patterns
}
