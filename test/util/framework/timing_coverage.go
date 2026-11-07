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

// deriveTestStepSpans converts ginkgo By steps into spans covering the whole test-work phase.
// By nodes opened with a callback carry an end event; bare By nodes run until the next step.
func deriveTestStepSpans(report types.SpecReport, phaseStart, phaseEnd time.Time) []testStepSpan {
	if !phaseEnd.After(phaseStart) {
		return nil
	}
	type openStep struct {
		name      string
		startedAt time.Time
	}
	var (
		opens  []openStep
		starts []time.Time
		spans  []testStepSpan
	)
	for _, event := range report.SpecEvents {
		switch {
		case event.SpecEventType.Is(types.SpecEventByStart):
			startedAt := event.TimelineLocation.Time
			if startedAt.IsZero() {
				continue
			}
			starts = append(starts, startedAt)
			opens = append(opens, openStep{name: event.Message, startedAt: startedAt})
		case event.SpecEventType.Is(types.SpecEventByEnd):
			finishedAt := event.TimelineLocation.Time
			if finishedAt.IsZero() {
				continue
			}
			// Callback steps close explicitly, in last-in-first-out order.
			for i := len(opens) - 1; i >= 0; i-- {
				if opens[i].name != event.Message {
					continue
				}
				spans = append(spans, testStepSpan{name: opens[i].name, startedAt: opens[i].startedAt, finishedAt: finishedAt})
				opens = slices.Delete(opens, i, i+1)
				break
			}
		}
	}
	// Bare steps have no end event: they run until the next step starts, or until the phase ends.
	for _, step := range opens {
		finishedAt := phaseEnd
		for _, candidate := range starts {
			if candidate.After(step.startedAt) && candidate.Before(finishedAt) {
				finishedAt = candidate
			}
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
