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
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"

	"sigs.k8s.io/yaml"
)

// fakePacerClock is an injectable clock whose sleep advances it.
type fakePacerClock struct {
	mu      sync.Mutex
	current time.Time
}

func (c *fakePacerClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

func (c *fakePacerClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current = c.current.Add(d)
}

func newFakePacerClock() *fakePacerClock {
	return &fakePacerClock{current: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)}
}

// newTestPacer builds a pacer isolated in a temp directory whose sleep
// advances the fake clock instead of blocking.
func newTestPacer(t *testing.T, cfg clusterCreatePacing, clock *fakePacerClock) *clusterCreatePacer {
	t.Helper()
	dir := t.TempDir()
	return &clusterCreatePacer{
		cfg:       cfg,
		statePath: filepath.Join(dir, clusterCreatePacingStateFile),
		lockPath:  filepath.Join(dir, "cluster-create-pacing.lock"),
		now:       clock.Now,
		sleep: func(ctx context.Context, d time.Duration) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			clock.Advance(d)
			return nil
		},
	}
}

func (p *clusterCreatePacer) recordedStarts(t *testing.T) []string {
	t.Helper()
	state, err := p.readState()
	if err != nil {
		t.Fatalf("reading pacing state: %v", err)
	}
	return state.Starts
}

func TestParseClusterCreatePacing(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    clusterCreatePacing
		wantErr bool
	}{
		{name: "empty uses default", value: "", want: clusterCreatePacing{max: 12, window: 6 * time.Minute}},
		{name: "default string parses", value: "12/6m", want: clusterCreatePacing{max: 12, window: 6 * time.Minute}},
		{name: "zero disables", value: "0", want: clusterCreatePacing{}},
		{name: "off disables", value: "off", want: clusterCreatePacing{}},
		{name: "disabled disables case-insensitively", value: "DISABLED", want: clusterCreatePacing{}},
		{name: "explicit values", value: "20/5m", want: clusterCreatePacing{max: 20, window: 5 * time.Minute}},
		{name: "sub-minute window", value: "1/30s", want: clusterCreatePacing{max: 1, window: 30 * time.Second}},
		{name: "surrounding whitespace", value: " 12 / 6m ", want: clusterCreatePacing{max: 12, window: 6 * time.Minute}},
		{name: "missing window", value: "12", wantErr: true},
		{name: "non-numeric max", value: "abc/6m", wantErr: true},
		{name: "zero max", value: "0/6m", wantErr: true},
		{name: "negative max", value: "-1/6m", wantErr: true},
		{name: "invalid window", value: "12/xyz", wantErr: true},
		{name: "zero window", value: "12/0s", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseClusterCreatePacing(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseClusterCreatePacing(%q) = %+v, want error", tt.value, got)
				}
				return
			}
			if err != nil {
				t.Errorf("parseClusterCreatePacing(%q) unexpected error: %v", tt.value, err)
				return
			}
			if got != tt.want {
				t.Errorf("parseClusterCreatePacing(%q) = %+v, want %+v", tt.value, got, tt.want)
			}
			if tt.want.max == 0 && got.enabled() {
				t.Errorf("parseClusterCreatePacing(%q).enabled() = true, want false", tt.value)
			}
		})
	}
}

// TestClusterCreatePacerAdmitsWithinBudget checks that starts below the window
// limit are recorded without waiting.
func TestClusterCreatePacerAdmitsWithinBudget(t *testing.T) {
	clock := newFakePacerClock()
	pacer := newTestPacer(t, clusterCreatePacing{max: 3, window: time.Minute}, clock)

	for i := 0; i < 3; i++ {
		if err := pacer.pace(t.Context(), logr.Discard(), "cluster-a"); err != nil {
			t.Fatalf("pace %d unexpected error: %v", i, err)
		}
	}
	if got := len(pacer.recordedStarts(t)); got != 3 {
		t.Errorf("recorded %d starts, want 3", got)
	}
	if got := clock.Now(); !got.Equal(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("clock advanced to %v without any wait, want the start time", got)
	}
}

// TestClusterCreatePacerWaitsWhenWindowFull checks that a start beyond the
// window limit waits until the oldest recorded start ages out, then records.
func TestClusterCreatePacerWaitsWhenWindowFull(t *testing.T) {
	clock := newFakePacerClock()
	pacer := newTestPacer(t, clusterCreatePacing{max: 1, window: 45 * time.Second}, clock)

	if err := pacer.pace(t.Context(), logr.Discard(), "cluster-a"); err != nil {
		t.Fatalf("first pace unexpected error: %v", err)
	}
	if err := pacer.pace(t.Context(), logr.Discard(), "cluster-b"); err != nil {
		t.Fatalf("second pace unexpected error: %v", err)
	}

	// The second start is only admissible once 45s have elapsed.
	if elapsed := clock.Now().Sub(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)); elapsed < 45*time.Second {
		t.Errorf("second pace admitted after only %v, want at least the 45s window", elapsed)
	}
	starts := pacer.recordedStarts(t)
	if len(starts) != 1 {
		t.Errorf("recorded %d starts, want 1 (the aged-out start pruned, the new one recorded)", len(starts))
	}
	if want := clock.Now().UTC().Format(time.RFC3339Nano); starts[0] != want {
		t.Errorf("recorded start %q, want %q", starts[0], want)
	}
}

// TestClusterCreatePacerKeepsFractionalSecondStarts checks that a start at a
// fractional second is recorded with sub-second precision. A truncated (pure
// RFC3339) timestamp would always fall back to the start of the second, so a
// window shorter than one second would admit a second start immediately even
// though the real start has not aged out of the window yet.
func TestClusterCreatePacerKeepsFractionalSecondStarts(t *testing.T) {
	clock := newFakePacerClock()
	pacer := newTestPacer(t, clusterCreatePacing{max: 1, window: 500 * time.Millisecond}, clock)
	deadline := clock.Now().Add(maxClusterCreatePacingWait + time.Hour)

	clock.Advance(900 * time.Millisecond) // 12:00:00.900

	if retryAfter, err := pacer.tryRecordStart(t.Context(), deadline); err != nil || retryAfter != 0 {
		t.Fatalf("first tryRecordStart = (%v, %v), want recorded start (0, nil)", retryAfter, err)
	}
	retryAfter, err := pacer.tryRecordStart(t.Context(), deadline)
	if err != nil {
		t.Fatalf("second tryRecordStart unexpected error: %v", err)
	}
	if retryAfter <= 0 {
		t.Errorf("second start admitted immediately (retryAfter %v); the fractional-second start was truncated to the second boundary", retryAfter)
	}
}

// TestClusterCreatePacerContextCancelledWhileWaiting checks that cancelling
// the context while waiting for window capacity aborts the creation instead of
// proceeding.
func TestClusterCreatePacerContextCancelledWhileWaiting(t *testing.T) {
	clock := newFakePacerClock()
	pacer := newTestPacer(t, clusterCreatePacing{max: 1, window: time.Hour}, clock)

	if err := pacer.pace(t.Context(), logr.Discard(), "cluster-a"); err != nil {
		t.Fatalf("first pace unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := pacer.pace(ctx, logr.Discard(), "cluster-b")
	if err == nil {
		t.Fatal("pace with cancelled context returned nil, want error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("pace error = %v, want context.Canceled", err)
	}
}

// TestClusterCreatePacerFailsOpenOnStateError checks that an unreadable state
// file disables pacing for that creation instead of failing it.
func TestClusterCreatePacerFailsOpenOnStateError(t *testing.T) {
	clock := newFakePacerClock()
	dir := t.TempDir()
	pacer := &clusterCreatePacer{
		cfg:       clusterCreatePacing{max: 1, window: time.Hour},
		statePath: dir, // a directory: reading it as a state file must fail
		lockPath:  filepath.Join(dir, "cluster-create-pacing.lock"),
		now:       clock.Now,
		sleep: func(ctx context.Context, d time.Duration) error {
			t.Error("pace should not sleep when the state is unavailable")
			return nil
		},
	}

	if err := pacer.pace(t.Context(), logr.Discard(), "cluster-a"); err != nil {
		t.Errorf("pace with unusable state returned error, want fail-open nil: %v", err)
	}
}

// TestClusterCreatePacerPrunesAgedStarts checks that timestamps older than the
// window are dropped rather than counted against the budget.
func TestClusterCreatePacerPrunesAgedStarts(t *testing.T) {
	clock := newFakePacerClock()
	pacer := newTestPacer(t, clusterCreatePacing{max: 2, window: time.Minute}, clock)

	// Two starts an hour before the fake "now": both outside the window.
	stale := []string{
		time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC).Format(time.RFC3339),
		time.Date(2026, 10, 22, 11, 0, 30, 0, time.UTC).Format(time.RFC3339),
	}
	seeded, err := yaml.Marshal(clusterCreatePacingState{Starts: stale})
	if err != nil {
		t.Fatalf("marshaling seed state: %v", err)
	}
	if err := os.WriteFile(pacer.statePath, seeded, 0666); err != nil {
		t.Fatalf("writing seed state: %v", err)
	}

	if err := pacer.pace(t.Context(), logr.Discard(), "cluster-a"); err != nil {
		t.Fatalf("pace unexpected error: %v", err)
	}
	starts := pacer.recordedStarts(t)
	if len(starts) != 1 {
		t.Errorf("recorded %d starts, want 1 (both stale starts pruned)", len(starts))
	}
}

// TestClusterCreatePacerProceedsAfterMaxWait checks the safety valve: a window
// that stays saturated past maxClusterCreatePacingWait stops blocking the
// creation instead of stalling the spec.
func TestClusterCreatePacerProceedsAfterMaxWait(t *testing.T) {
	clock := newFakePacerClock()
	pacer := newTestPacer(t, clusterCreatePacing{max: 1, window: 24 * time.Hour}, clock)

	if err := pacer.pace(t.Context(), logr.Discard(), "cluster-a"); err != nil {
		t.Fatalf("first pace unexpected error: %v", err)
	}
	if err := pacer.pace(t.Context(), logr.Discard(), "cluster-b"); err != nil {
		t.Fatalf("second pace unexpected error: %v", err)
	}

	elapsed := clock.Now().Sub(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	if elapsed < maxClusterCreatePacingWait {
		t.Errorf("saturated window gave up after only %v, want at least %v", elapsed, maxClusterCreatePacingWait)
	}
	// The second start was never recorded: it proceeded without admission.
	if got := len(pacer.recordedStarts(t)); got != 1 {
		t.Errorf("recorded %d starts, want 1 (the unpaced start must not be recorded)", got)
	}
}

// TestPaceHCPClusterCreateStartPacesOncePerName checks the per-process memo:
// the FromParam → AndWait call chain passes through both hooks, so a second
// pace of the same cluster name must be a no-op even when the window is
// saturated, while a different name still waits.
func TestPaceHCPClusterCreateStartPacesOncePerName(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ARTIFACT_DIR", dir)
	t.Setenv(ClusterCreatePacingEnvvar, "1/1h")
	// pacedClusterCreates is process-global: drop the entries these calls
	// insert so a repeated execution in the same process (-count=2, another
	// test binary run) does not bypass admission.
	t.Cleanup(func() {
		pacedClusterCreates.Delete("cluster-twice")
		pacedClusterCreates.Delete("cluster-other")
	})

	if err := paceHCPClusterCreateStart(t.Context(), logr.Discard(), "cluster-twice"); err != nil {
		t.Fatalf("first pace unexpected error: %v", err)
	}

	// The window is now saturated; a pre-cancelled context surfaces any wait.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := paceHCPClusterCreateStart(ctx, logr.Discard(), "cluster-twice"); err != nil {
		t.Errorf("second pace for the same cluster name = %v, want nil (memoized no-op)", err)
	}
	err := paceHCPClusterCreateStart(ctx, logr.Discard(), "cluster-other")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("pace for a different cluster name = %v, want context.Canceled (not memoized)", err)
	}
}

// TestPaceHCPClusterCreateStartRequiresArtifactDir checks that pacing stays
// off without ARTIFACT_DIR: there is no shared state to coordinate through, so
// a saturated window must not cause a wait (plain unit-test conditions).
func TestPaceHCPClusterCreateStartRequiresArtifactDir(t *testing.T) {
	t.Setenv("ARTIFACT_DIR", "")
	t.Setenv(ClusterCreatePacingEnvvar, "1/1h")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := paceHCPClusterCreateStart(ctx, logr.Discard(), "cluster-noartifactdir"); err != nil {
		t.Errorf("pace without ARTIFACT_DIR = %v, want nil (pacing off)", err)
	}
}

// TestPaceHCPClusterCreateStartEnvHandling checks the package-level entry
// point: "off" disables pacing entirely, a malformed value fails open, and the
// ARTIFACT_DIR state file is what the pacer consults.
func TestPaceHCPClusterCreateStartEnvHandling(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ARTIFACT_DIR", dir)
	statePath := filepath.Join(dir, clusterCreatePacingStateFile)

	seed := func(t *testing.T) {
		t.Helper()
		// The package-level entry point uses the real clock, so seed relative
		// to time.Now: inside any positive window the entry is always recent.
		saturated, err := yaml.Marshal(clusterCreatePacingState{Starts: []string{
			time.Now().UTC().Format(time.RFC3339),
		}})
		if err != nil {
			t.Fatalf("marshaling seed state: %v", err)
		}
		if err := os.WriteFile(statePath, saturated, 0666); err != nil {
			t.Fatalf("writing seed state: %v", err)
		}
	}

	startsInStateFile := func(t *testing.T) []string {
		t.Helper()
		data, err := os.ReadFile(statePath)
		if err != nil {
			t.Fatalf("reading pacing state: %v", err)
		}
		var state clusterCreatePacingState
		if err := yaml.Unmarshal(data, &state); err != nil {
			t.Fatalf("unmarshaling pacing state: %v", err)
		}
		return state.Starts
	}

	t.Run("off leaves a saturated window untouched", func(t *testing.T) {
		t.Setenv(ClusterCreatePacingEnvvar, "off")
		seed(t)
		if err := paceHCPClusterCreateStart(t.Context(), logr.Discard(), "cluster-off"); err != nil {
			t.Errorf("pace with pacing disabled returned error: %v", err)
		}
		if got := len(startsInStateFile(t)); got != 1 {
			t.Errorf("state has %d starts after a disabled pace, want 1 (unchanged)", got)
		}
	})

	t.Run("malformed value fails open", func(t *testing.T) {
		t.Setenv(ClusterCreatePacingEnvvar, "not-a-value")
		seed(t)
		if err := paceHCPClusterCreateStart(t.Context(), logr.Discard(), "cluster-malformed"); err != nil {
			t.Errorf("pace with malformed %s returned error, want fail-open nil: %v", ClusterCreatePacingEnvvar, err)
		}
	})

	t.Run("consults the ARTIFACT_DIR state file", func(t *testing.T) {
		// Max 1 with a start already recorded: the window is saturated, so the
		// pacer must wait — a pre-cancelled context surfaces that wait without
		// sleeping, proving the shared state file is being consulted.
		t.Setenv(ClusterCreatePacingEnvvar, "1/1h")
		seed(t)
		t.Cleanup(func() {
			// pacedClusterCreates is process-global: drop the entry this call
			// inserts so a repeated execution in the same process does not
			// bypass admission.
			pacedClusterCreates.Delete("cluster-saturated")
		})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err := paceHCPClusterCreateStart(ctx, logr.Discard(), "cluster-saturated")
		if !errors.Is(err, context.Canceled) {
			t.Errorf("pace over saturated window = %v, want context.Canceled (proving the wait engaged)", err)
		}
	})
}
