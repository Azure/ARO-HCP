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

package statuscontrollers

import (
	"fmt"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SourcedCondition is a metav1.Condition tagged with the name of the
// controller that produced it. The controller name takes the role of the
// "condition type prefix" in openshift/library-go's status.UnionCondition:
// it is the key used to disambiguate source subsystems in the aggregate
// reason and message.
type SourcedCondition struct {
	ControllerName string
	Condition      metav1.Condition
}

// UnionCondition collapses many per-controller conditions into a single
// metav1.Condition for the aggregated parent. It is a metav1 port of
// openshift/library-go's status.UnionCondition:
//
//   - defaultStatus picks the "good" status. For Degraded that is False
//     (degraded should only flip True when something is actually wrong).
//   - When every source is at defaultStatus the result is defaultStatus
//     with Reason="AsExpected" and a joined-message ("All is well" if all
//     source messages are empty).
//   - When at least one source disagrees with defaultStatus and has held
//     its current value longer than inertia(...), the result flips to the
//     disagreeing status. Reason and message are aggregated across all
//     bad sources, sorted by controller name for stability.
//   - When inertia is nil, every disagreeing source is propagated
//     immediately (no flap protection).
//
// If no sources are supplied the result is Unknown/NoData — the parent
// has nothing to base its aggregate on.
func UnionCondition(
	conditionType string,
	defaultStatus metav1.ConditionStatus,
	inertia Inertia,
	now time.Time,
	sources ...SourcedCondition,
) metav1.Condition {
	oppositeStatus := metav1.ConditionTrue
	if defaultStatus == metav1.ConditionTrue {
		oppositeStatus = metav1.ConditionFalse
	}

	interesting := make([]SourcedCondition, 0, len(sources))
	bad := make([]SourcedCondition, 0, len(sources))
	badStatus := metav1.ConditionUnknown
	for _, src := range sources {
		if src.Condition.Type != conditionType {
			continue
		}
		interesting = append(interesting, src)
		if src.Condition.Status != defaultStatus {
			bad = append(bad, src)
			if src.Condition.Status == oppositeStatus {
				badStatus = oppositeStatus
			}
		}
	}
	// Sort both slices by controller name so every downstream consumer
	// (unionMessage / unionReason / latestTransitionTime) sees the same
	// stable order — including the all-good path that builds its message
	// from `interesting`. Without this, controller-list iteration order
	// would leak into the aggregated message and cause churn.
	sort.Sort(byControllerName(interesting))
	sort.Sort(byControllerName(bad))

	result := metav1.Condition{Type: conditionType, Status: metav1.ConditionUnknown}
	if len(interesting) == 0 {
		result.Reason = "NoData"
		return result
	}

	var elderBad []SourcedCondition
	if inertia == nil {
		elderBad = bad
	} else {
		for _, src := range bad {
			if src.Condition.LastTransitionTime.Time.Before(now.Add(-inertia(src.ControllerName, src.Condition))) {
				elderBad = append(elderBad, src)
			}
		}
	}

	if len(elderBad) == 0 {
		result.Status = defaultStatus
		result.Message = unionMessage(interesting)
		if result.Message == "" {
			result.Message = "All is well"
		}
		result.Reason = "AsExpected"
		result.LastTransitionTime = latestTransitionTime(interesting)
		return result
	}

	result.Status = badStatus
	result.Message = unionMessage(bad)
	result.Reason = unionReason(bad)
	result.LastTransitionTime = latestTransitionTime(bad)
	return result
}

// unionMessage formats per-source messages, one line per source message
// line, prefixed with the source controller name so the aggregated message
// remains attributable.
func unionMessage(sources []SourcedCondition) string {
	lines := []string{}
	for _, src := range sources {
		if len(src.Condition.Message) == 0 {
			continue
		}
		for _, line := range uniq(strings.Split(src.Condition.Message, "\n")) {
			lines = append(lines, fmt.Sprintf("%s: %s", src.ControllerName, line))
		}
	}
	return strings.Join(lines, "\n")
}

// unionReason builds a stable, machine-grep-able reason of the form
// "Controller1_Reason1::Controller2_Reason2", sorted alphabetically by
// controller name.
func unionReason(sources []SourcedCondition) string {
	parts := make([]string, 0, len(sources))
	for _, src := range sources {
		if len(src.Condition.Reason) > 0 {
			parts = append(parts, src.ControllerName+"_"+src.Condition.Reason)
		} else {
			parts = append(parts, src.ControllerName)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "::")
}

func latestTransitionTime(sources []SourcedCondition) metav1.Time {
	latest := metav1.Time{}
	for _, src := range sources {
		if latest.Before(&src.Condition.LastTransitionTime) {
			latest = src.Condition.LastTransitionTime
		}
	}
	return latest
}

func uniq(s []string) []string {
	seen := make(map[string]struct{}, len(s))
	j := 0
	for _, v := range s {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		s[j] = v
		j++
	}
	return s[:j]
}

type byControllerName []SourcedCondition

var _ sort.Interface = byControllerName{}

func (s byControllerName) Len() int           { return len(s) }
func (s byControllerName) Less(i, j int) bool { return s[i].ControllerName < s[j].ControllerName }
func (s byControllerName) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
