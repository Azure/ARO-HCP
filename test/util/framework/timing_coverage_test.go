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

package framework

import (
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2/types"
	"github.com/stretchr/testify/require"
)

// buildSpecReport assembles a spec report whose By events carry absolute timestamps.
func buildSpecReport(start time.Time, events ...types.SpecEvent) types.SpecReport {
	return types.SpecReport{StartTime: start, SpecEvents: events}
}

func byStart(start time.Time, offset time.Duration, message string) types.SpecEvent {
	return types.SpecEvent{
		SpecEventType:    types.SpecEventByStart,
		Message:          message,
		TimelineLocation: types.TimelineLocation{Time: start.Add(offset)},
	}
}

func byEnd(start time.Time, offset time.Duration, message string) types.SpecEvent {
	return types.SpecEvent{
		SpecEventType:    types.SpecEventByEnd,
		Message:          message,
		Duration:         offset,
		TimelineLocation: types.TimelineLocation{Time: start.Add(offset)},
	}
}

// TestDeriveTestStepSpansCoversPhase checks that every By form leaves no uncovered interval.
func TestDeriveTestStepSpansCoversPhase(t *testing.T) {
	start := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	phaseEnd := start.Add(10 * time.Minute)
	for _, tc := range []struct {
		name   string
		report types.SpecReport
		want   []string
	}{
		{
			name:   "bare steps chain until the next step",
			report: buildSpecReport(start, byStart(start, 30*time.Second, "first"), byStart(start, 2*time.Minute, "second")),
			want:   []string{"Framework setup", "first", "second"},
		},
		{
			name: "callback steps nest and keep their own interval",
			report: buildSpecReport(start,
				byStart(start, 0, "outer"),
				byStart(start, time.Minute, "inner"),
				byEnd(start, 2*time.Minute, "inner"),
				byEnd(start, 3*time.Minute, "outer"),
			),
			want: []string{"outer", "inner"},
		},
		{
			name:   "a spec without steps is one body span",
			report: buildSpecReport(start),
			want:   []string{"Test execution"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spans := deriveTestStepSpans(tc.report, start, phaseEnd)
			var names []string
			for _, span := range spans {
				names = append(names, span.name)
			}
			require.ElementsMatch(t, tc.want, names)
			require.Empty(t, uncoveredTestStepIntervals(spans, start, phaseEnd), "test work must be fully attributed")
		})
	}
}

// TestDeriveTestStepSpansExtendsBareSteps checks that a bare step ends where the next step begins.
func TestDeriveTestStepSpansExtendsBareSteps(t *testing.T) {
	start := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	report := buildSpecReport(start, byStart(start, 0, "first"), byStart(start, 4*time.Minute, "second"))
	spans := deriveTestStepSpans(report, start, start.Add(9*time.Minute))
	require.Equal(t, start.Add(4*time.Minute), spans[0].finishedAt)
	require.Equal(t, start.Add(9*time.Minute), spans[1].finishedAt)
}

// TestUncoveredTestStepIntervalsReportsHoles checks the gap detector itself.
func TestUncoveredTestStepIntervalsReportsHoles(t *testing.T) {
	start := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	spans := []testStepSpan{
		{name: "first", startedAt: start, finishedAt: start.Add(time.Minute)},
		{name: "second", startedAt: start.Add(3 * time.Minute), finishedAt: start.Add(4 * time.Minute)},
	}
	uncovered := uncoveredTestStepIntervals(spans, start, start.Add(5*time.Minute))
	require.Len(t, uncovered, 2)
	require.Equal(t, start.Add(time.Minute), uncovered[0].startedAt)
	require.Equal(t, start.Add(4*time.Minute), uncovered[1].startedAt)
}
