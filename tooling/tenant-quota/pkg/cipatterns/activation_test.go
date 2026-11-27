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

package cipatterns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
)

type coverageHarness struct {
	mu                                 sync.Mutex
	newest                             failurePatternVersion
	found                              bool
	coverage                           []sourceCoverage
	cutoffs                            []time.Time
	rows                               []controlRow
	checks                             int
	controlErr, coverageErr, submitErr error
	lagVisibility                      bool // accepted rows stay invisible to readControl
}

func (h *coverageHarness) attach(c *Controller) {
	c.readControl = func(context.Context) (failurePatternVersion, bool, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.newest, h.found, h.controlErr
	}
	c.readCoverage = func(_ context.Context, version failurePatternVersion, cutoff time.Time) ([]sourceCoverage, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.checks++
		h.cutoffs = append(h.cutoffs, cutoff)
		return h.coverage, h.coverageErr
	}
	c.submitControl = func(_ context.Context, payload *bytes.Buffer) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.submitErr != nil {
			return h.submitErr
		}
		var row controlRow
		if err := decodeControlPayload(payload.Bytes(), &row); err != nil {
			return err
		}
		h.rows = append(h.rows, row)
		if !h.lagVisibility {
			h.newest, h.found = compiledFailurePatternVersion(), true
		}
		return nil
	}
}

func (h *coverageHarness) checkCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.checks
}

func (h *coverageHarness) written() []controlRow {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]controlRow(nil), h.rows...)
}

func TestCoverageWriteRule(t *testing.T) {
	compiled := compiledFailurePatternVersion()
	other := failurePatternVersion{extractorVersion: "other", resultSchemaVersion: compiled.resultSchemaVersion}
	otherSchema := failurePatternVersion{extractorVersion: compiled.extractorVersion, resultSchemaVersion: compiled.resultSchemaVersion + 1}
	for _, tc := range []struct {
		name                string
		newest              failurePatternVersion
		found               bool
		total, uncovered    int64
		wantRow, wantActive bool
	}{
		{"empty table, fully covered", failurePatternVersion{}, false, 1000, 0, true, false},
		{"empty table, exactly at threshold", failurePatternVersion{}, false, 1000, 10, true, false},
		{"empty table, one over threshold", failurePatternVersion{}, false, 1000, 11, false, false},
		{"threshold floors: 199 inputs allow one", failurePatternVersion{}, false, 199, 1, true, false},
		{"threshold floors: 199 inputs reject two", failurePatternVersion{}, false, 199, 2, false, false},
		{"under 100 inputs is strict", failurePatternVersion{}, false, 99, 1, false, false},
		{"under 100 inputs with none missing", failurePatternVersion{}, false, 99, 0, true, false},
		{"no inputs at all", failurePatternVersion{}, false, 0, 0, true, false},
		{"other version newest, within threshold", other, true, 1000, 10, true, false},
		{"other version newest, above threshold", other, true, 1000, 11, false, false},
		{"other schema newest, above threshold", otherSchema, true, 1000, 11, false, false},
		{"compiled newest: heartbeat within threshold", compiled, true, 1000, 3, true, true},
		{"compiled newest: heartbeat far above threshold", compiled, true, 1000, 900, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestController(t)
			now := time.Date(2026, 12, 1, 12, 30, 0, 0, time.UTC)
			c.now = func() time.Time { return now }
			h := &coverageHarness{newest: tc.newest, found: tc.found,
				coverage: []sourceCoverage{{source: "e2e", total: tc.total - tc.total/4, uncovered: tc.uncovered / 2}, {source: "rp-provision", total: tc.total / 4, uncovered: tc.uncovered - tc.uncovered/2}}}
			h.attach(c)
			active, err := c.coverageCheck(t.Context())
			require.NoError(t, err)
			require.Equal(t, tc.wantActive, active)
			require.Equal(t, []time.Time{now.Add(-coverageIngestionMargin)}, h.cutoffs)
			rows := h.written()
			if !tc.wantRow {
				require.Empty(t, rows)
				return
			}
			require.Equal(t, []controlRow{{
				ExtractorVersion: compiled.extractorVersion, ResultSchemaVersion: compiled.resultSchemaVersion, CoverageCheckedAt: now,
				RawFailedInputsConsidered: tc.total, RawFailedInputsWithoutResult: tc.uncovered,
			}}, rows)
		})
	}
}

func TestCoverageReadErrorsWriteNothing(t *testing.T) {
	for name, set := range map[string]func(*coverageHarness){
		"control read":  func(h *coverageHarness) { h.controlErr = errors.New("incomplete control response") },
		"coverage read": func(h *coverageHarness) { h.coverageErr = errors.New("incomplete coverage response") },
	} {
		t.Run(name, func(t *testing.T) {
			c := newTestController(t)
			h := &coverageHarness{coverage: []sourceCoverage{{source: "e2e", total: 500}}}
			set(h)
			h.attach(c)
			active, err := c.coverageCheck(t.Context())
			require.Error(t, err)
			require.False(t, active)
			require.Empty(t, h.written())
		})
	}
	t.Run("submit failure is an error and not counted", func(t *testing.T) {
		c := newTestController(t)
		h := &coverageHarness{coverage: []sourceCoverage{{source: "e2e", total: 500}}, submitErr: errors.New("queue unavailable")}
		h.attach(c)
		active, err := c.coverageCheck(t.Context())
		require.ErrorContains(t, err, "queue unavailable")
		require.False(t, active)
		require.Zero(t, testutil.ToFloat64(c.metrics.controlRows))
	})
}

func TestCoverageMetrics(t *testing.T) {
	c := newTestController(t)
	now := time.Date(2026, 12, 1, 12, 30, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	h := &coverageHarness{coverage: []sourceCoverage{{source: "e2e", total: 1000, uncovered: 50}, {source: "rp-upgrade", total: 10, uncovered: 0}}}
	h.attach(c)
	active, err := c.coverageCheck(t.Context())
	require.NoError(t, err)
	require.False(t, active, "5% uncovered must not activate")
	require.Equal(t, 50.0, testutil.ToFloat64(c.metrics.uncoveredInputs.WithLabelValues("e2e")))
	require.Equal(t, 1000.0, testutil.ToFloat64(c.metrics.consideredInputs.WithLabelValues("e2e")))
	require.Equal(t, 10.0, testutil.ToFloat64(c.metrics.consideredInputs.WithLabelValues("rp-upgrade")))
	require.Equal(t, float64(now.Unix()), testutil.ToFloat64(c.metrics.lastCoverageCheck))
	require.Zero(t, testutil.ToFloat64(c.metrics.controlRows))

	h.mu.Lock()
	h.coverage = []sourceCoverage{{source: "e2e", total: 1000, uncovered: 0}}
	h.mu.Unlock()
	active, err = c.coverageCheck(t.Context())
	require.NoError(t, err)
	require.False(t, active, "a queued activation row is not active until the control read sees it")
	require.Equal(t, 1, testutil.CollectAndCount(c.metrics.consideredInputs), "a source that disappears leaves no stale series")
	require.Equal(t, 1.0, testutil.ToFloat64(c.metrics.controlRows))
}

func TestCoverageMetricsStayConsistentWhenSubmitFails(t *testing.T) {
	c := newTestController(t)
	first := time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return first }
	h := &coverageHarness{coverage: []sourceCoverage{{source: "e2e", total: 1000, uncovered: 50}}}
	h.attach(c)
	_, err := c.coverageCheck(t.Context()) // 5% uncovered: nothing to write, counts published
	require.NoError(t, err)

	c.now = func() time.Time { return first.Add(time.Hour) }
	h.mu.Lock()
	h.coverage = []sourceCoverage{{source: "rp-upgrade", total: 40, uncovered: 0}}
	h.submitErr = errors.New("queue unavailable")
	h.mu.Unlock()
	_, err = c.coverageCheck(t.Context())
	require.ErrorContains(t, err, "queue unavailable")
	require.Equal(t, 50.0, testutil.ToFloat64(c.metrics.uncoveredInputs.WithLabelValues("e2e")))
	require.Equal(t, 1000.0, testutil.ToFloat64(c.metrics.consideredInputs.WithLabelValues("e2e")))
	require.Equal(t, 1, testutil.CollectAndCount(c.metrics.consideredInputs), "the failed check published no counts")
	require.Equal(t, float64(first.Unix()), testutil.ToFloat64(c.metrics.lastCoverageCheck))
}

func TestCoverageScheduleIntervalWhileInactiveThenOncePerSweep(t *testing.T) {
	c := newTestController(t)
	h := &coverageHarness{coverage: []sourceCoverage{{source: "e2e", total: 1000, uncovered: 500}}}
	h.attach(c)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); c.runCoverage(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	require.Eventually(t, func() bool { return h.checkCount() >= 3 }, 2*time.Second, time.Millisecond, "inactive: checks every interval")
	require.Empty(t, h.written())

	h.mu.Lock()
	h.coverage = []sourceCoverage{{source: "e2e", total: 1000, uncovered: 0}}
	h.mu.Unlock()
	// The activation row is queued, then the next check sees it and writes a heartbeat.
	require.Eventually(t, func() bool { return len(h.written()) == 2 }, 2*time.Second, time.Millisecond)
	// Active now: the 10ms interval must not trigger more checks.
	time.Sleep(50 * time.Millisecond)
	checks := h.checkCount()
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, checks, h.checkCount())
	require.Len(t, h.written(), 2)

	c.sweepDone <- struct{}{}
	require.Eventually(t, func() bool { return len(h.written()) == 3 }, 2*time.Second, time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, checks+1, h.checkCount(), "exactly one check per completed sweep")
}

func TestCoverageFailedCheckRetriesAtInterval(t *testing.T) {
	c := newTestController(t)
	h := &coverageHarness{coverage: []sourceCoverage{{source: "e2e", total: 100}}, coverageErr: errors.New("boom")}
	h.attach(c)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); c.runCoverage(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	require.Eventually(t, func() bool { return h.checkCount() >= 3 }, 2*time.Second, time.Millisecond)
	require.Empty(t, h.written())
	h.mu.Lock()
	h.coverageErr = nil
	h.mu.Unlock()
	require.Eventually(t, func() bool { return len(h.written()) == 1 }, 2*time.Second, time.Millisecond)
}

func TestSweepCompletionSignalsCoverageLoop(t *testing.T) {
	c := newTestController(t)
	c.readBuildIDs = func(context.Context, string, int) ([]string, error) { return []string{"2"}, nil }
	require.NoError(t, c.sweepStep(t.Context()))
	select {
	case <-c.sweepDone:
	default:
		t.Fatal("completed sweep did not signal")
	}
	require.NoError(t, c.sweepStep(t.Context()))
	select {
	case <-c.sweepDone:
		t.Fatal("resting sweep must not signal")
	default:
	}
}

func TestStartJoinsCoverageLoopOnShutdown(t *testing.T) {
	c := newTestController(t)
	entered := make(chan struct{})
	var once sync.Once
	c.readControl = func(ctx context.Context) (failurePatternVersion, bool, error) {
		once.Do(func() { close(entered) })
		<-ctx.Done()
		return failurePatternVersion{}, false, ctx.Err()
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); c.Start(ctx) }()
	<-entered
	select {
	case <-done:
		t.Fatal("Start returned while the coverage check was in flight")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not join the coverage loop")
	}
}

func decodeControlPayload(payload []byte, row *controlRow) error {
	return json.NewDecoder(bytes.NewReader(payload)).Decode(row)
}

func TestCoverageActivationRetriesUntilControlRowIsVisible(t *testing.T) {
	c := newTestController(t)
	h := &coverageHarness{coverage: []sourceCoverage{{source: "e2e", total: 1000}}, lagVisibility: true}
	h.attach(c)
	c.coverageFallback = time.Hour
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); c.runCoverage(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	// Accepted but invisible (delayed or lost ingestion): keep checking every interval.
	require.Eventually(t, func() bool { return len(h.written()) >= 3 }, 2*time.Second, time.Millisecond)

	h.mu.Lock()
	h.newest, h.found, h.lagVisibility = compiledFailurePatternVersion(), true, false
	h.mu.Unlock()
	// The next check sees the row, writes a heartbeat and then waits for a sweep.
	require.Eventually(t, func() bool {
		rows := h.written()
		before := h.checkCount()
		time.Sleep(50 * time.Millisecond)
		return len(rows) > 3 && h.checkCount() == before
	}, 2*time.Second, time.Millisecond)
}

func TestCoverageSweepCompletionDuringCheckTriggersNextCheck(t *testing.T) {
	c := newTestController(t)
	h := &coverageHarness{newest: compiledFailurePatternVersion(), found: true, coverage: []sourceCoverage{{source: "e2e", total: 1000}}}
	h.attach(c)
	c.coverageFallback = time.Hour
	read := c.readCoverage
	var once sync.Once
	c.readCoverage = func(ctx context.Context, version failurePatternVersion, cutoff time.Time) ([]sourceCoverage, error) {
		coverage, err := read(ctx, version, cutoff)
		once.Do(func() { c.sweepDone <- struct{}{} }) // a sweep completes while the first check runs
		return coverage, err
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); c.runCoverage(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	require.Eventually(t, func() bool { return h.checkCount() == 2 }, 2*time.Second, time.Millisecond, "completion during a check must not be discarded")
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, 2, h.checkCount())
}

func TestCoverageActiveVersionIsCheckedWhenSweepStalls(t *testing.T) {
	c := newTestController(t)
	h := &coverageHarness{newest: compiledFailurePatternVersion(), found: true, coverage: []sourceCoverage{{source: "e2e", total: 1000, uncovered: 900}}}
	h.attach(c)
	c.coverageFallback = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); c.runCoverage(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	// No sweep ever completes, yet heartbeats continue with the honest residual.
	require.Eventually(t, func() bool { return len(h.written()) >= 3 }, 2*time.Second, time.Millisecond)
	require.EqualValues(t, 900, h.written()[0].RawFailedInputsWithoutResult)
}
