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
	"errors"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2/types"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/test/util/timing"
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

func byEnd(start time.Time, startedOffset, finishedOffset time.Duration, message string) types.SpecEvent {
	return types.SpecEvent{
		SpecEventType:    types.SpecEventByEnd,
		Message:          message,
		Duration:         finishedOffset - startedOffset,
		TimelineLocation: types.TimelineLocation{Time: start.Add(finishedOffset)},
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
				byEnd(start, time.Minute, 2*time.Minute, "inner"),
				byEnd(start, 0, 3*time.Minute, "outer"),
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

// TestDeriveTestStepSpansBoundsBareStepInsideCallback checks that an inner bare step stops with its parent.
func TestDeriveTestStepSpansBoundsBareStepInsideCallback(t *testing.T) {
	start := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	report := buildSpecReport(start,
		byStart(start, 0, "outer"),
		byStart(start, time.Minute, "inner"),
		byEnd(start, 0, 3*time.Minute, "outer"),
	)
	spans := deriveTestStepSpans(report, start, start.Add(10*time.Minute))
	byName := map[string]testStepSpan{}
	for _, span := range spans {
		byName[span.name] = span
	}
	require.Equal(t, start.Add(3*time.Minute), byName["inner"].finishedAt, "a bare step must not outlive its callback")
	require.False(t, byName["outer"].finishedAt.Before(start.Add(3*time.Minute)), "an enclosing callback keeps its own end")
	require.Empty(t, uncoveredTestStepIntervals(clipTestStepSpans(spans, start, start.Add(10*time.Minute)), start, start.Add(10*time.Minute)))
}

// TestDeriveTestStepSpansWithRepeatedMessages checks duplicate step names keep their own endpoints.
func TestDeriveTestStepSpansWithRepeatedMessages(t *testing.T) {
	start := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	// The callback spans minutes 0 to 3; a bare step with the same message starts at minute 1.
	report := buildSpecReport(start,
		byStart(start, 0, "same"),
		byStart(start, time.Minute, "same"),
		byEnd(start, 0, 3*time.Minute, "same"),
	)
	phaseEnd := start.Add(10 * time.Minute)
	spans := deriveTestStepSpans(report, start, phaseEnd)
	require.Len(t, spans, 2)
	byStartTime := map[time.Duration]testStepSpan{}
	for _, span := range spans {
		byStartTime[span.startedAt.Sub(start)] = span
	}
	require.Equal(t, start.Add(3*time.Minute), byStartTime[time.Minute].finishedAt,
		"the bare step must end with its enclosing callback, not own the callback end")
	require.Equal(t, start, byStartTime[0].startedAt, "the callback keeps its own start")
	require.False(t, byStartTime[0].finishedAt.Before(start.Add(3*time.Minute)))
	require.Empty(t, uncoveredTestStepIntervals(clipTestStepSpans(spans, start, phaseEnd), start, phaseEnd),
		"repeated messages must still cover the phase")
}

// TestDeriveTestStepSpansBoundsConsecutiveBareSteps checks sibling bare steps split at the next start.
func TestDeriveTestStepSpansBoundsConsecutiveBareSteps(t *testing.T) {
	start := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	report := buildSpecReport(start,
		byStart(start, 0, "outer"),
		byStart(start, time.Minute, "first"),
		byStart(start, 2*time.Minute, "second"),
		byEnd(start, 0, 3*time.Minute, "outer"),
	)
	spans := deriveTestStepSpans(report, start, start.Add(10*time.Minute))
	byStartTime := map[time.Duration]testStepSpan{}
	for _, span := range spans {
		byStartTime[span.startedAt.Sub(start)] = span
	}
	require.Equal(t, start.Add(2*time.Minute), byStartTime[time.Minute].finishedAt, "the first bare step ends where the second begins")
	require.Equal(t, start.Add(3*time.Minute), byStartTime[2*time.Minute].finishedAt, "the second bare step ends with the callback")
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

// TestFinalizePhasesDoesNotClaimInterruptedCleanup checks serialization closes phases before attributing time.
func TestFinalizePhasesDoesNotClaimInterruptedCleanup(t *testing.T) {
	start := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	tc := &perItOrDescribeTestContext{}
	tc.timingMetadata.Phases = []timing.StepTimingMetadata{
		{Kind: "test-work", Name: "Setup and test execution", StartedAt: start.Format(time.RFC3339Nano), FinishedAt: start.Add(time.Minute).Format(time.RFC3339Nano)},
		{Kind: "resource-cleanup", Name: "Resource cleanup and identity release", StartedAt: start.Add(2 * time.Minute).Format(time.RFC3339Nano)},
	}
	tc.timingMetadata.Steps = []timing.StepTimingMetadata{
		{Kind: timing.KindTestStep, Name: "Resource cleanup and identity release", StartedAt: start.Add(2 * time.Minute).Format(time.RFC3339Nano)},
	}
	serializedAt := start.Add(5 * time.Minute)
	lastPhaseEnd := tc.finalizePhasesUnlocked(serializedAt.Format(time.RFC3339Nano))
	require.Equal(t, serializedAt, lastPhaseEnd, "an open phase is closed at serialization")
	require.Equal(t, serializedAt.Format(time.RFC3339Nano), tc.timingMetadata.Steps[0].FinishedAt)
	require.True(t, tc.timingMetadata.Steps[0].Incomplete)
	tc.recordFinalizeSpanUnlocked(lastPhaseEnd, serializedAt)
	require.Len(t, tc.timingMetadata.Steps, 1, "serialization must not claim the interrupted cleanup interval")

	// A phase that already ended leaves the serialization interval to be attributed.
	earlier := &perItOrDescribeTestContext{}
	earlier.timingMetadata.Phases = []timing.StepTimingMetadata{
		{Kind: "resource-cleanup", Name: "Resource cleanup and identity release", StartedAt: start.Format(time.RFC3339Nano), FinishedAt: start.Add(2 * time.Minute).Format(time.RFC3339Nano)},
	}
	lastPhaseEnd = earlier.finalizePhasesUnlocked(serializedAt.Format(time.RFC3339Nano))
	earlier.recordFinalizeSpanUnlocked(lastPhaseEnd, serializedAt)
	require.Len(t, earlier.timingMetadata.Steps, 1)
	require.Equal(t, start.Add(2*time.Minute).Format(time.RFC3339Nano), earlier.timingMetadata.Steps[0].StartedAt)
	require.Equal(t, serializedAt.Format(time.RFC3339Nano), earlier.timingMetadata.Steps[0].FinishedAt)
}

// TestDeriveTestStepSpansRepeatedMessagesNestedCallbacks checks nonzero-offset callbacks match exactly.
func TestDeriveTestStepSpansRepeatedMessagesNestedCallbacks(t *testing.T) {
	start := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	// Both callbacks share a message: one runs minutes 1 to 2 inside the other running 0 to 5.
	report := buildSpecReport(start,
		byStart(start, 0, "same"),
		byStart(start, time.Minute, "same"),
		byEnd(start, time.Minute, 2*time.Minute, "same"),
		byEnd(start, 0, 5*time.Minute, "same"),
	)
	spans := deriveTestStepSpans(report, start, start.Add(10*time.Minute))
	byStartTime := map[time.Duration]testStepSpan{}
	for _, span := range spans {
		byStartTime[span.startedAt.Sub(start)] = span
	}
	require.Equal(t, start.Add(2*time.Minute), byStartTime[time.Minute].finishedAt, "the inner callback keeps its own end")
	require.Equal(t, start, byStartTime[0].startedAt, "the outer callback keeps its own start")
	require.False(t, byStartTime[0].finishedAt.Before(start.Add(5*time.Minute)), "the outer callback keeps its own end")
}

// TestOperationRecorderPinsTheStartingSpec checks a late return cannot attach work to the next spec.
func TestOperationRecorderPinsTheStartingSpec(t *testing.T) {
	owner := &perItOrDescribeTestContext{}
	follower := &perItOrDescribeTestContext{}
	activeTestContext.Store(owner)
	t.Cleanup(func() { activeTestContext.Store(nil) })

	recorder := beginOperation()
	activeTestContext.Store(follower)
	start := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)

	recorder.Record(timing.KindUpdateCluster, "late update", start, start.Add(time.Minute), nil)
	require.Empty(t, follower.timingMetadata.Steps, "a late return must not attach to the next spec")
	require.Len(t, owner.timingMetadata.Steps, 1, "the span belongs to the spec that started it")

	owner.timingFinalized = true
	recorder.Record(timing.KindUpdateCluster, "late update", start, start.Add(time.Minute), nil)
	require.Len(t, owner.timingMetadata.Steps, 1, "a finalized spec stops collecting")

	require.True(t, recorder.testBodyOperation(), "an operation started in the body stays a test-body operation")
	owner.timingMetadata.CleanupStartedAt = start.Format(time.RFC3339Nano)
	require.True(t, recorder.testBodyOperation(), "the origin is fixed when the operation starts")
	follower.timingMetadata.CleanupStartedAt = start.Format(time.RFC3339Nano)
	require.False(t, beginOperation().testBodyOperation(), "an operation started in cleanup is a cleanup operation")

	// A reused context serves a new spec, so a recorder from the previous spec must stop appending.
	reused := &perItOrDescribeTestContext{}
	activeTestContext.Store(reused)
	staleRecorder := beginOperation()
	reused.timingFinalized = true
	// BeforeEach resets finalization and bumps the generation for the next spec.
	reused.timingFinalized = false
	reused.specGeneration++
	reused.timingMetadata.Steps = nil
	staleRecorder.Record(timing.KindUpdateCluster, "late update", start, start.Add(time.Minute), nil)
	require.Empty(t, reused.timingMetadata.Steps, "a recorder from a previous spec must not append to a reused context")
}

// TestRecordCleanupStageRecordsOutcome checks cleanup stages record their result honestly.
func TestRecordCleanupStageRecordsOutcome(t *testing.T) {
	tc := &perItOrDescribeTestContext{}
	sentinel := errors.New("delete failed")

	err := tc.recordCleanupStage("Delete clusters in rg", func() error { return sentinel })
	require.ErrorIs(t, err, sentinel)
	require.Len(t, tc.timingMetadata.Steps, 1)
	require.Equal(t, "Delete clusters in rg", tc.timingMetadata.Steps[0].Name)
	require.Equal(t, timing.OutcomeFailed, tc.timingMetadata.Steps[0].Outcome)
	require.Empty(t, tc.timingMetadata.Steps[0].Kind, "cleanup stages stay generic operations")

	require.NoError(t, tc.recordCleanupStage("Delete resource group rg", func() error { return nil }))
	require.Equal(t, timing.OutcomeSucceeded, tc.timingMetadata.Steps[1].Outcome)

	// A purge helper reports no error, so the stage must not claim success.
	tc.recordBestEffortCleanupStage("Purge deleted Managed HSMs in rg", func() {})
	require.Len(t, tc.timingMetadata.Steps, 3)
	require.Empty(t, tc.timingMetadata.Steps[2].Outcome, "a best-effort purge must not record an outcome")

	// A stage that returns after the context moved to the next spec must not append.
	before := len(tc.timingMetadata.Steps)
	require.NoError(t, tc.recordCleanupStage("Wait for managed resource groups in rg", func() error {
		tc.specGeneration++
		return nil
	}))
	require.Len(t, tc.timingMetadata.Steps, before, "a stage from a previous spec must not append")
}
