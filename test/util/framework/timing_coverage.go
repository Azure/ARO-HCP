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
	"slices"
	"time"

	"github.com/onsi/ginkgo/v2/types"
)

// testStepSpan is a named interval taken from a ginkgo step rather than from an Azure call.
type testStepSpan struct {
	name       string
	startedAt  time.Time
	finishedAt time.Time
}

// byStep tracks one recorded ginkgo step while its interval is resolved.
type byStep struct {
	name       string
	startedAt  time.Time
	finishedAt time.Time
	callback   bool
}

// deriveTestStepSpans converts ginkgo By steps into spans covering the whole test-work phase.
// A By opened with a callback carries an end event that identifies its start exactly; a bare By
// ends at the next step start or at the end of its innermost enclosing callback.
func deriveTestStepSpans(report types.SpecReport, phaseStart, phaseEnd time.Time) []testStepSpan {
	if !phaseEnd.After(phaseStart) {
		return nil
	}
	var steps []byStep
	for _, event := range report.SpecEvents {
		switch {
		case event.SpecEventType.Is(types.SpecEventByStart):
			startedAt := event.TimelineLocation.Time
			if startedAt.IsZero() {
				continue
			}
			steps = append(steps, byStep{name: event.Message, startedAt: startedAt})
		case event.SpecEventType.Is(types.SpecEventByEnd):
			finishedAt := event.TimelineLocation.Time
			if finishedAt.IsZero() {
				continue
			}
			// Ginkgo reports the callback duration, so its start is exact and identifies the owner.
			if matchCallbackStep(steps, event.Message, finishedAt.Add(-event.Duration), finishedAt) {
				continue
			}
			// Fall back to the innermost open step with this message when the timestamps disagree.
			for i := len(steps) - 1; i >= 0; i-- {
				if steps[i].finishedAt.IsZero() && steps[i].name == event.Message {
					steps[i].finishedAt = finishedAt
					steps[i].callback = true
					break
				}
			}
		}
	}
	// A bare step ends at the next step start or at the end of its innermost enclosing callback.
	enclosingEnd := func(startedAt time.Time) time.Time {
		bound := time.Time{}
		for _, step := range steps {
			if !step.callback || step.startedAt.After(startedAt) || step.finishedAt.Before(startedAt) {
				continue
			}
			if bound.IsZero() || step.finishedAt.Before(bound) {
				bound = step.finishedAt
			}
		}
		return bound
	}
	spans := make([]testStepSpan, 0, len(steps))
	for _, step := range steps {
		finishedAt := step.finishedAt
		if finishedAt.IsZero() {
			finishedAt = phaseEnd
			for _, other := range steps {
				if other.startedAt.After(step.startedAt) && other.startedAt.Before(finishedAt) {
					finishedAt = other.startedAt
				}
			}
			if bound := enclosingEnd(step.startedAt); !bound.IsZero() && bound.Before(finishedAt) {
				finishedAt = bound
			}
		}
		if !finishedAt.After(step.startedAt) {
			continue
		}
		spans = append(spans, testStepSpan{name: step.name, startedAt: step.startedAt, finishedAt: finishedAt})
	}
	slices.SortFunc(spans, func(a, b testStepSpan) int { return a.startedAt.Compare(b.startedAt) })
	spans = extendUnnestedSpans(clipTestStepSpans(spans, phaseStart, phaseEnd), phaseEnd)
	// Time before the first step is framework setup, and a spec without steps is one body span.
	switch {
	case len(spans) == 0:
		spans = append(spans, testStepSpan{name: "Test execution", startedAt: phaseStart, finishedAt: phaseEnd})
	case spans[0].startedAt.After(phaseStart):
		spans = append([]testStepSpan{{name: "Framework setup", startedAt: phaseStart, finishedAt: spans[0].startedAt}}, spans...)
	}
	return spans
}

// Matches a callback end to the open step that started at the reported instant.
func matchCallbackStep(steps []byStep, name string, startedAt, finishedAt time.Time) bool {
	for i := len(steps) - 1; i >= 0; i-- {
		if steps[i].finishedAt.IsZero() && steps[i].startedAt.Equal(startedAt) && steps[i].name == name {
			steps[i].finishedAt = finishedAt
			steps[i].callback = true
			return true
		}
	}
	return false
}

// Intersects spans with the phase so no step claims time outside its own test.
func clipTestStepSpans(spans []testStepSpan, phaseStart, phaseEnd time.Time) []testStepSpan {
	clipped := make([]testStepSpan, 0, len(spans))
	for _, span := range spans {
		if span.startedAt.Before(phaseStart) {
			span.startedAt = phaseStart
		}
		if span.finishedAt.After(phaseEnd) {
			span.finishedAt = phaseEnd
		}
		if !span.finishedAt.After(span.startedAt) {
			continue
		}
		clipped = append(clipped, span)
	}
	return clipped
}

// Grows top-level spans to the next top-level step so consecutive steps leave no hole.
// Nested spans are left untouched so an inner step cannot claim its parent's remaining time.
func extendUnnestedSpans(spans []testStepSpan, phaseEnd time.Time) []testStepSpan {
	for i := range spans {
		if isNestedTestStepSpan(spans, i) {
			continue
		}
		next := phaseEnd
		for j := i + 1; j < len(spans); j++ {
			if isNestedTestStepSpan(spans, j) || !spans[j].startedAt.After(spans[i].startedAt) {
				continue
			}
			next = spans[j].startedAt
			break
		}
		if spans[i].finishedAt.Before(next) {
			spans[i].finishedAt = next
		}
	}
	return spans
}

// Reports whether another span strictly contains the span at index.
func isNestedTestStepSpan(spans []testStepSpan, index int) bool {
	for i, other := range spans {
		if i == index {
			continue
		}
		if !other.startedAt.After(spans[index].startedAt) && !other.finishedAt.Before(spans[index].finishedAt) &&
			(other.startedAt.Before(spans[index].startedAt) || other.finishedAt.After(spans[index].finishedAt)) {
			return true
		}
	}
	return false
}

// Returns the parts of the phase that no span covers.
func uncoveredTestStepIntervals(spans []testStepSpan, phaseStart, phaseEnd time.Time) []testStepSpan {
	cursor := phaseStart
	var uncovered []testStepSpan
	for _, span := range spans {
		if span.startedAt.After(cursor) {
			uncovered = append(uncovered, testStepSpan{startedAt: cursor, finishedAt: span.startedAt})
		}
		if span.finishedAt.After(cursor) {
			cursor = span.finishedAt
		}
	}
	if cursor.Before(phaseEnd) {
		uncovered = append(uncovered, testStepSpan{startedAt: cursor, finishedAt: phaseEnd})
	}
	return uncovered
}
