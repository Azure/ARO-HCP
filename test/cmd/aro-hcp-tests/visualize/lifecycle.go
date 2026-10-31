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

package visualize

import (
	"bytes"
	"fmt"
	"html/template"
	"slices"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/test/util/timing"
)

// Parses operation kinds, outcomes and interrupted spans while rejecting malformed timestamps.
func parseSteps(raw []timing.StepTimingMetadata, logger logr.Logger) []StepTimingMetadata {
	steps := make([]StepTimingMetadata, 0, len(raw))
	for _, step := range raw {
		start, err := time.Parse(time.RFC3339Nano, step.StartedAt)
		if err != nil {
			logger.Error(err, "failed to parse start date", "identifier", step.Name)
			continue
		}
		finish, err := time.Parse(time.RFC3339Nano, step.FinishedAt)
		if err != nil || finish.Before(start) {
			logger.Error(err, "invalid step finish date", "identifier", step.Name)
			continue
		}
		steps = append(steps, StepTimingMetadata{Name: step.Name, Kind: step.Kind, Outcome: step.Outcome, Incomplete: step.Incomplete, StartedAt: start, FinishedAt: finish})
	}
	return steps
}

// HasFullLifecycle identifies metadata whose cleanup boundary lies inside its recorded root interval.
func hasFullLifecycle(item TestInfo) bool {
	return !item.CleanupStartedAt.IsZero() && !item.CleanupStartedAt.Before(item.StartedAt) && !item.CleanupStartedAt.After(item.FinishedAt)
}

// LifecycleSubtitle distinguishes full lifecycle envelopes from historical pre-cleanup recordings.
func lifecycleSubtitle(times []TestInfo, elapsed time.Duration) string {
	full := 0
	for _, item := range times {
		if hasFullLifecycle(item) {
			full++
		}
	}
	if full == len(times) {
		return fmt.Sprintf("Test elapsed: %s (including cleanup)", elapsed)
	}
	return fmt.Sprintf("Recorded: %s (%d/%d with full cleanup)", elapsed, full, len(times))
}

// ClipSpan intersects a recorded interval with its enclosing test or phase interval.
func clipSpan(span StepTimingMetadata, start, finish time.Time) (StepTimingMetadata, bool) {
	if span.StartedAt.Before(start) {
		span.StartedAt = start
	}
	if span.FinishedAt.After(finish) {
		span.FinishedAt = finish
	}
	return span, !span.FinishedAt.Before(span.StartedAt)
}

// UnionSpans merges overlapping operation intervals without counting nested work twice.
func unionSpans(spans []StepTimingMetadata) []StepTimingMetadata {
	slices.SortFunc(spans, func(a, b StepTimingMetadata) int {
		if c := a.StartedAt.Compare(b.StartedAt); c != 0 {
			return c
		}
		return b.FinishedAt.Compare(a.FinishedAt)
	})
	merged := spans[:0]
	for _, span := range spans {
		if !span.FinishedAt.After(span.StartedAt) {
			continue
		}
		if len(merged) == 0 || span.StartedAt.After(merged[len(merged)-1].FinishedAt) {
			merged = append(merged, span)
		} else if span.FinishedAt.After(merged[len(merged)-1].FinishedAt) {
			merged[len(merged)-1].FinishedAt = span.FinishedAt
		}
	}
	return merged
}

// LifecycleRows keeps phases distinct from operation coverage and exposes every uncovered interval once.
func lifecycleRows(item TestInfo) []StepTimingMetadata {
	var rows, operations, phases []StepTimingMetadata
	boundaries := []time.Time{item.StartedAt, item.FinishedAt}
	if hasFullLifecycle(item) {
		boundaries = append(boundaries, item.CleanupStartedAt)
	}
	for _, phase := range item.Phases {
		if clipped, ok := clipSpan(phase, item.StartedAt, item.FinishedAt); ok {
			phases = append(phases, clipped)
			boundaries = append(boundaries, clipped.StartedAt, clipped.FinishedAt)
			clipped.Kind = "phase"
			clipped.Name = "Phase: " + clipped.Name
			if clipped.Incomplete {
				clipped.Name += " (incomplete)"
			}
			rows = append(rows, clipped)
		}
	}
	for _, step := range item.Steps {
		if clipped, ok := clipSpan(step, item.StartedAt, item.FinishedAt); ok {
			operations = append(operations, clipped)
			if clipped.Incomplete {
				clipped.Name += " (incomplete)"
			}
			if clipped.Outcome != "" {
				clipped.Name += " (" + clipped.Outcome + ")"
			} else if clipped.Kind != "" {
				clipped.Name += " (outcome unknown)"
			}
			if clipped.Kind == "phase" || clipped.Kind == "gap" || timingColor(clipped.Kind) == "" {
				clipped.Kind = "operation"
			}
			rows = append(rows, clipped)
		}
	}
	covered := unionSpans(operations)
	slices.SortFunc(boundaries, func(a, b time.Time) int { return a.Compare(b) })
	boundaries = slices.CompactFunc(boundaries, func(a, b time.Time) bool { return a.Equal(b) })
	coverageIndex := 0
	for i := 1; i < len(boundaries); i++ {
		start, finish := boundaries[i-1], boundaries[i]
		label := "Untracked recorded interval (legacy; cleanup coverage unknown)"
		if hasFullLifecycle(item) {
			label = "Untracked test work"
			if !start.Before(item.CleanupStartedAt) {
				label = "Untracked cleanup"
			}
		}
		for _, phase := range phases {
			if !start.Before(phase.StartedAt) && !finish.After(phase.FinishedAt) {
				label += ": " + phase.Name
				break
			}
		}
		cursor := start
		for coverageIndex < len(covered) && !covered[coverageIndex].FinishedAt.After(start) {
			coverageIndex++
		}
		for j := coverageIndex; j < len(covered) && covered[j].StartedAt.Before(finish); j++ {
			span, _ := clipSpan(covered[j], start, finish)
			if span.StartedAt.After(cursor) {
				rows = append(rows, StepTimingMetadata{Name: label, Kind: "gap", StartedAt: cursor, FinishedAt: span.StartedAt})
			}
			if span.FinishedAt.After(cursor) {
				cursor = span.FinishedAt
			}
		}
		if cursor.Before(finish) {
			rows = append(rows, StepTimingMetadata{Name: label, Kind: "gap", StartedAt: cursor, FinishedAt: finish})
		}
	}
	return rows
}

// Selects consistent row colors with contrast against both light and dark backgrounds.
func timingColor(kind string) string {
	switch kind {
	case timing.KindCreateCluster, timing.KindCreateClusterRequest:
		return "#2563eb"
	case timing.KindCreateNodePool:
		return "#0d9488"
	case timing.KindDeleteCluster:
		return "#d97706"
	case timing.KindResourceGroupCleanup:
		return "#db2777"
	case "phase":
		return "#9333ea"
	case "gap":
		return "#64748b"
	default:
		return ""
	}
}

type durationSummary struct {
	Name       string
	Color      string
	Count      int
	Total      time.Duration
	Min        time.Duration
	Max        time.Duration
	Incomplete int
}

// Records one successful sample without merging overlapping resource operations.
func (s *durationSummary) add(duration time.Duration) {
	if s.Count == 0 {
		s.Min, s.Max = duration, duration
	} else {
		s.Min = min(s.Min, duration)
		s.Max = max(s.Max, duration)
	}
	s.Count++
	s.Total += duration
}

// Minimum formats the shortest successful sample, including a valid zero duration.
func (s durationSummary) Minimum() string {
	if s.Count == 0 {
		return "N/A"
	}
	return s.Min.String()
}

// Maximum formats the longest successful sample when one exists.
func (s durationSummary) Maximum() string {
	if s.Count == 0 {
		return "N/A"
	}
	return s.Max.String()
}

// Mean formats a duration only when the category has successful complete samples.
func (s durationSummary) Mean() string {
	if s.Count == 0 {
		return "N/A"
	}
	return (s.Total / time.Duration(s.Count)).String()
}

type lifecycleSummary struct {
	Rows                              []durationSummary
	Passed, Failed, TimedOut, Unknown int
	Other, Full, Legacy               int
}

// Summarizes successful operation duration ranges independently of enclosing cleanup.
func summarizeLifecycle(times []TestInfo) lifecycleSummary {
	summary := lifecycleSummary{Rows: []durationSummary{
		{Name: timing.KindCreateCluster, Color: timingColor(timing.KindCreateCluster)},
		{Name: timing.KindCreateNodePool, Color: timingColor(timing.KindCreateNodePool)},
		{Name: timing.KindDeleteCluster, Color: timingColor(timing.KindDeleteCluster)},
		{Name: timing.KindResourceGroupCleanup, Color: timingColor(timing.KindResourceGroupCleanup)},
		{Name: "Full cleanup", Color: timingColor("phase")},
	}}
	for _, item := range times {
		if hasFullLifecycle(item) {
			summary.Full++
		} else {
			summary.Legacy++
		}
		switch item.Outcome {
		case "passed":
			summary.Passed++
		case "failed", "panicked":
			summary.Failed++
			continue
		case "timedout":
			summary.TimedOut++
			continue
		case "":
			summary.Unknown++
			continue
		default:
			summary.Other++
			continue
		}
		for i := range summary.Rows[:4] {
			for _, step := range item.Steps {
				if step.Kind != summary.Rows[i].Name {
					continue
				}
				if step.Incomplete {
					summary.Rows[i].Incomplete++
					continue
				}
				if step.Outcome != timing.OutcomeSucceeded {
					continue
				}
				if clipped, ok := clipSpan(step, item.StartedAt, item.FinishedAt); ok {
					// Each typed span records one resource operation, even when operations overlap.
					summary.Rows[i].add(clipped.FinishedAt.Sub(clipped.StartedAt))
				}
			}
		}
		if hasFullLifecycle(item) {
			incomplete := false
			for _, phase := range item.Phases {
				if phase.Incomplete && !phase.FinishedAt.Before(item.CleanupStartedAt) {
					incomplete = true
				}
			}
			for _, step := range item.Steps {
				if step.Incomplete && step.FinishedAt.After(item.CleanupStartedAt) {
					incomplete = true
				}
			}
			if incomplete {
				summary.Rows[4].Incomplete++
			} else {
				summary.Rows[4].add(item.FinishedAt.Sub(item.CleanupStartedAt))
			}
		}
	}
	return summary
}

var summaryTemplate = template.Must(template.New("lifecycle-summary").Parse(`<style>
:root { color-scheme: light dark; }
body { margin: 0; color: CanvasText; background: Canvas; }
.timing-summary { font: 14px/1.5 sans-serif; padding: 16px; max-width: 1000px; margin: auto; }
.timing-summary h2 { font-size: 18px; margin: 0 0 8px; }
.timing-summary p { margin: 8px 0; }
.timing-summary table { width: 100%; border-collapse: collapse; text-align: left; }
.timing-summary th, .timing-summary td { padding: 6px 8px; border-bottom: 1px solid GrayText; }
</style><section class="timing-summary">
<h2>Operation durations</h2>
<p>{{.Passed}} passed · {{.Failed}} failed · {{.TimedOut}} timed out{{if .Unknown}} · {{.Unknown}} unknown{{end}}{{if .Other}} · {{.Other}} other{{end}}. Successful operations only.</p>
<table><thead><tr><th>Operation</th><th>Min</th><th>Average</th><th>Max</th><th>Samples</th><th>Incomplete</th></tr></thead><tbody>{{range .Rows}}<tr style="border-left:4px solid {{.Color}}"><td>{{.Name}}</td><td>{{.Minimum}}</td><td>{{.Mean}}</td><td>{{.Maximum}}</td><td>{{.Count}}</td><td>{{.Incomplete}}</td></tr>{{end}}</tbody></table>
<p>Row colors match the table · Purple: phase · Slate: untracked work · Outline: test.</p>
</section>`))

// LifecycleSummaryHTML renders an escaped, visible summary above the timing chart.
func lifecycleSummaryHTML(times []TestInfo) ([]byte, error) {
	var output bytes.Buffer
	if err := summaryTemplate.Execute(&output, summarizeLifecycle(times)); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
