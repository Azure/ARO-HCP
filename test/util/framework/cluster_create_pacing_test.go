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

	"github.com/Azure/ARO-HCP/test/pkg/filelock"
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

// TestClusterCreatePacerDoesNotCommitAdmissionPastDeadline checks the ordering
// of the fail-open bound: window capacity that frees *past*
// maxClusterCreatePacingWait must not be committed. Checking the deadline only
// after tryRecordStart returns would let a start whose wait already exceeded
// the bound be recorded anyway and returned as a paced success, silently
// violating the 30-minute safety valve documented in the README.
func TestClusterCreatePacerDoesNotCommitAdmissionPastDeadline(t *testing.T) {
	clock := newFakePacerClock()
	// With max=1 and a window 10s longer than the pacing wait, the seeded
	// start ages out — freeing window capacity — 10s after the deadline.
	pacer := newTestPacer(t, clusterCreatePacing{max: 1, window: maxClusterCreatePacingWait + 10*time.Second}, clock)

	if err := pacer.pace(t.Context(), logr.Discard(), "cluster-a"); err != nil {
		t.Fatalf("first pace unexpected error: %v", err)
	}
	if err := pacer.pace(t.Context(), logr.Discard(), "cluster-b"); err != nil {
		t.Fatalf("second pace unexpected error: %v", err)
	}

	// The second pace waited to the bound, then failed open...
	elapsed := clock.Now().Sub(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	if elapsed < maxClusterCreatePacingWait {
		t.Errorf("second pace gave up after only %v, want at least %v", elapsed, maxClusterCreatePacingWait)
	}
	// ...without committing the admission: the state keeps exactly the first
	// start. A deadline check placed after tryRecordStart would instead have
	// recorded this start ~10s past the deadline and returned paced success.
	if got := len(pacer.recordedStarts(t)); got != 1 {
		t.Errorf("recorded %d starts, want 1 (an admission past the pacing deadline must not be recorded)", got)
	}
}

// TestClusterCreatePacerSmoothsRefillAfterBurst checks the refill semantics
// with a backlog larger than the window (max > 1): the sliding window alone
// would admit the next max waiting starts together once the initial burst
// ages out, re-forming a synchronized batch; the persisted next-admission
// timestamp must instead refill the freed capacity one start per window/max.
func TestClusterCreatePacerSmoothsRefillAfterBurst(t *testing.T) {
	clock := newFakePacerClock()
	pacer := newTestPacer(t, clusterCreatePacing{max: 3, window: 90 * time.Second}, clock)

	// The initial burst: three starts recorded with no wait.
	for _, name := range []string{"cluster-1", "cluster-2", "cluster-3"} {
		if err := pacer.pace(t.Context(), logr.Discard(), name); err != nil {
			t.Fatalf("burst pace %s unexpected error: %v", name, err)
		}
	}
	burstDone := clock.Now()

	admittedAt := map[string]time.Duration{}
	for _, name := range []string{"cluster-4", "cluster-5", "cluster-6", "cluster-7"} {
		if err := pacer.pace(t.Context(), logr.Discard(), name); err != nil {
			t.Fatalf("pace %s unexpected error: %v", name, err)
		}
		admittedAt[name] = clock.Now().Sub(burstDone)
	}

	// The first post-burst start waits for the burst to age out of the
	// window: the trailing-window cap still binds.
	if got := admittedAt["cluster-4"]; got < 90*time.Second {
		t.Errorf("cluster-4 admitted %v after the burst, want at least the 90s window", got)
	}
	// The starts after it are spaced one window/max = 30s apart rather than
	// admitted together as a second batch.
	for _, want := range []struct {
		name string
		at   time.Duration
	}{
		{name: "cluster-5", at: 120 * time.Second},
		{name: "cluster-6", at: 150 * time.Second},
		{name: "cluster-7", at: 180 * time.Second},
	} {
		if got := admittedAt[want.name]; got != want.at {
			t.Errorf("%s admitted %v after the burst, want %v (one per window/max)", want.name, got, want.at)
		}
	}
}

// gatedPacerSleep replaces a test pacer's sleep so a test can observe and
// control the lock retry loop: every sleep signals entry, then blocks until
// released (advancing the fake clock) or the context ends. Holding a pacer
// lock while a waiter is inside gatedPacerSleep exercises lockState under real
// contention without real-time sleeps.
type gatedPacerSleep struct {
	entered chan struct{}
	release chan struct{}
	clock   *fakePacerClock
}

func newGatedPacerSleep(clock *fakePacerClock) *gatedPacerSleep {
	return &gatedPacerSleep{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
		clock:   clock,
	}
}

func (g *gatedPacerSleep) sleep(ctx context.Context, d time.Duration) error {
	g.entered <- struct{}{}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-g.release:
		g.clock.Advance(d)
		return nil
	}
}

// startLockStateWaiter runs pacer.lockState in the background with a gated
// sleep and returns once the waiter is provably inside the lock retry loop
// (its first sleep), along with the channel carrying its result and the gate
// controlling its retry sleeps.
func startLockStateWaiter(pacer *clusterCreatePacer, clock *fakePacerClock, ctx context.Context, deadline time.Time) (<-chan error, *gatedPacerSleep) {
	g := newGatedPacerSleep(clock)
	pacer.sleep = g.sleep
	done := make(chan error, 1)
	go func() {
		release, err := pacer.lockState(ctx, deadline)
		if err == nil {
			release()
		}
		done <- err
	}()
	<-g.entered
	return done, g
}

// TestClusterCreatePacerLockStateMutexContention checks the in-process half of
// the pacer locking under contention: while another goroutine holds the state
// mutex, a waiting admission returns the caller's cancellation rather than
// blocking indefinitely, fails open with errPacingLockUnavailable once the
// pacing deadline passes, and a subsequent admission acquires the released
// mutex and records normally.
func TestClusterCreatePacerLockStateMutexContention(t *testing.T) {
	clock := newFakePacerClock()
	pacer := newTestPacer(t, clusterCreatePacing{max: 1, window: time.Hour}, clock)
	instantSleep := pacer.sleep
	deadline := clock.Now().Add(time.Hour)

	t.Run("returns the caller cancellation while the mutex is held", func(t *testing.T) {
		clusterCreatePacingMu.Lock()
		defer clusterCreatePacingMu.Unlock()

		ctx, cancel := context.WithCancel(t.Context())
		done, _ := startLockStateWaiter(pacer, clock, ctx, deadline)
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("lockState under mutex contention = %v, want context.Canceled", err)
		}
	})

	t.Run("fails open when the pacing deadline passes under mutex contention", func(t *testing.T) {
		clusterCreatePacingMu.Lock()
		defer clusterCreatePacingMu.Unlock()

		shortDeadline := clock.Now().Add(5 * time.Minute)
		done, g := startLockStateWaiter(pacer, clock, t.Context(), shortDeadline)
		// Advance fake time past the deadline, then let the retry loop see it.
		clock.Advance(6 * time.Minute)
		close(g.release)
		if err := <-done; !errors.Is(err, errPacingLockUnavailable) {
			t.Errorf("lockState after deadline under mutex contention = %v, want errPacingLockUnavailable", err)
		}
	})

	t.Run("a subsequent admission acquires the released mutex and records", func(t *testing.T) {
		pacer.sleep = instantSleep
		if err := pacer.pace(t.Context(), logr.Discard(), "cluster-after-mutex-contention"); err != nil {
			t.Fatalf("pace after mutex contention = %v, want nil", err)
		}
		if got := len(pacer.recordedStarts(t)); got != 1 {
			t.Errorf("recorded %d starts, want 1", got)
		}
	})
}

// TestClusterCreatePacerLockStateFlockContention checks the cross-process half
// of the pacer locking under contention: while another file descriptor holds
// the state flock — the situation a stalled spec subprocess creates — a
// waiting admission returns the caller's cancellation rather than blocking
// indefinitely, fails open with errPacingLockUnavailable once the pacing
// deadline passes, and a subsequent admission acquires the released flock and
// records normally.
func TestClusterCreatePacerLockStateFlockContention(t *testing.T) {
	clock := newFakePacerClock()
	pacer := newTestPacer(t, clusterCreatePacing{max: 1, window: time.Hour}, clock)
	instantSleep := pacer.sleep
	deadline := clock.Now().Add(time.Hour)

	// Hold the cross-process flock the way another process would: a separate
	// open file description on the same lock file.
	holder, err := os.OpenFile(pacer.lockPath, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		t.Fatalf("opening pacing lock file: %v", err)
	}
	if err := filelock.TryLock(holder.Fd()); err != nil {
		t.Fatalf("acquiring pacing lock file for the test: %v", err)
	}
	t.Cleanup(func() {
		_ = filelock.Unlock(holder.Fd())
		_ = holder.Close()
	})

	t.Run("returns the caller cancellation while the flock is held", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		done, _ := startLockStateWaiter(pacer, clock, ctx, deadline)
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("lockState under flock contention = %v, want context.Canceled", err)
		}
	})

	t.Run("fails open when the pacing deadline passes under flock contention", func(t *testing.T) {
		shortDeadline := clock.Now().Add(5 * time.Minute)
		done, g := startLockStateWaiter(pacer, clock, t.Context(), shortDeadline)
		// Advance fake time past the deadline, then let the retry loop see it.
		clock.Advance(6 * time.Minute)
		close(g.release)
		if err := <-done; !errors.Is(err, errPacingLockUnavailable) {
			t.Errorf("lockState after deadline under flock contention = %v, want errPacingLockUnavailable", err)
		}
	})

	t.Run("a subsequent admission acquires the released flock and records", func(t *testing.T) {
		if err := filelock.Unlock(holder.Fd()); err != nil {
			t.Fatalf("releasing the test's pacing lock: %v", err)
		}
		pacer.sleep = instantSleep
		if err := pacer.pace(t.Context(), logr.Discard(), "cluster-after-flock-contention"); err != nil {
			t.Fatalf("pace after flock contention = %v, want nil", err)
		}
		if got := len(pacer.recordedStarts(t)); got != 1 {
			t.Errorf("recorded %d starts, want 1", got)
		}
	})
}

// TestPaceHCPClusterCreateStartPacesOncePerCluster checks the per-process memo:
// the FromParam → AndWait call chain passes through both hooks, so a second
// pace of the same cluster must be a no-op even when the window is saturated,
// while a different cluster — a different name, or the same name in another
// resource group — still waits.
func TestPaceHCPClusterCreateStartPacesOncePerCluster(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ARTIFACT_DIR", dir)
	t.Setenv(ClusterCreatePacingEnvvar, "1/1h")
	// pacedClusterCreates is process-global: drop the entries these calls
	// insert so a repeated execution in the same process (-count=2, another
	// test binary run) does not bypass admission.
	t.Cleanup(func() {
		pacedClusterCreates.Delete(clusterCreatePacingKey("rg-one", "cluster-twice"))
		pacedClusterCreates.Delete(clusterCreatePacingKey("rg-two", "cluster-twice"))
		pacedClusterCreates.Delete(clusterCreatePacingKey("rg-one", "cluster-other"))
	})

	if err := paceHCPClusterCreateStart(t.Context(), logr.Discard(), "rg-one", "cluster-twice"); err != nil {
		t.Fatalf("first pace unexpected error: %v", err)
	}

	// The window is now saturated; a pre-cancelled context surfaces any wait.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := paceHCPClusterCreateStart(ctx, logr.Discard(), "rg-one", "cluster-twice"); err != nil {
		t.Errorf("second pace for the same cluster = %v, want nil (memoized no-op)", err)
	}
	// The same cluster name in a different resource group is a different
	// cluster: it must pace again, not piggyback on the memoized admission.
	err := paceHCPClusterCreateStart(ctx, logr.Discard(), "rg-two", "cluster-twice")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("pace for the same name in another resource group = %v, want context.Canceled (not memoized)", err)
	}
	err = paceHCPClusterCreateStart(ctx, logr.Discard(), "rg-one", "cluster-other")
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
	if err := paceHCPClusterCreateStart(ctx, logr.Discard(), "rg-one", "cluster-noartifactdir"); err != nil {
		t.Errorf("pace without ARTIFACT_DIR = %v, want nil (pacing off)", err)
	}
}

// startsInStateFileAt reads the starts recorded in a pacing state file.
func startsInStateFileAt(t *testing.T, statePath string) []string {
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

// TestPaceHCPClusterCreateStartEnvHandling checks the package-level entry
// point: "off" disables pacing entirely and a malformed value fails open.
// That the ARTIFACT_DIR state file is actually consulted is covered by
// TestPaceHCPClusterCreateStartSaturatedStateBlocksUntilDeadline.
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
		return startsInStateFileAt(t, statePath)
	}

	t.Run("off leaves a saturated window untouched", func(t *testing.T) {
		t.Setenv(ClusterCreatePacingEnvvar, "off")
		seed(t)
		if err := paceHCPClusterCreateStart(t.Context(), logr.Discard(), "rg-one", "cluster-off"); err != nil {
			t.Errorf("pace with pacing disabled returned error: %v", err)
		}
		if got := len(startsInStateFile(t)); got != 1 {
			t.Errorf("state has %d starts after a disabled pace, want 1 (unchanged)", got)
		}
	})

	t.Run("malformed value fails open", func(t *testing.T) {
		t.Setenv(ClusterCreatePacingEnvvar, "not-a-value")
		seed(t)
		if err := paceHCPClusterCreateStart(t.Context(), logr.Discard(), "rg-one", "cluster-malformed"); err != nil {
			t.Errorf("pace with malformed %s returned error, want fail-open nil: %v", ClusterCreatePacingEnvvar, err)
		}
	})
}

// TestPaceHCPClusterCreateStartSaturatedStateBlocksUntilDeadline checks that a
// saturated shared state file actually blocks admission. A pre-cancelled
// context cannot prove that — lockState returns before ever reading the state —
// so instead a live context bounded by a short deadline must expire *inside*
// the admission wait, proving the state was read and found full; the same
// short deadline over an empty state must admit immediately, proving the
// deadline alone does not fail the pace.
func TestPaceHCPClusterCreateStartSaturatedStateBlocksUntilDeadline(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ARTIFACT_DIR", dir)
	t.Setenv(ClusterCreatePacingEnvvar, "1/1h")
	statePath := filepath.Join(dir, clusterCreatePacingStateFile)
	t.Cleanup(func() {
		pacedClusterCreates.Delete(clusterCreatePacingKey("rg-live", "cluster-saturated"))
		pacedClusterCreates.Delete(clusterCreatePacingKey("rg-live", "cluster-empty"))
	})

	// The package-level entry point uses the real clock, so seed relative to
	// time.Now: inside any positive window the entry is always recent.
	saturated, err := yaml.Marshal(clusterCreatePacingState{Starts: []string{
		time.Now().UTC().Format(time.RFC3339),
	}})
	if err != nil {
		t.Fatalf("marshaling seed state: %v", err)
	}
	if err := os.WriteFile(statePath, saturated, 0666); err != nil {
		t.Fatalf("writing seed state: %v", err)
	}

	started := time.Now()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	err = paceHCPClusterCreateStart(ctx, logr.Discard(), "rg-live", "cluster-saturated")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pace over saturated window = %v, want context.DeadlineExceeded (the wait must engage)", err)
	}
	if blocked := time.Since(started); blocked < 2*time.Second {
		t.Errorf("saturated pace returned after %v, want it to block until the context deadline", blocked)
	}
	// The blocked admission was never granted, so it never recorded a start:
	// the state keeps exactly the seeded one.
	if got := len(startsInStateFileAt(t, statePath)); got != 1 {
		t.Errorf("state has %d starts after a blocked pace, want 1 (the seeded start only)", got)
	}

	// Contrast: the same short deadline over an empty state admits at once
	// and records its start — the saturated state file, not the deadline, is
	// what blocked the pace above.
	if err := os.Remove(statePath); err != nil {
		t.Fatalf("removing state file for the empty-state contrast: %v", err)
	}
	ctx, cancel = context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := paceHCPClusterCreateStart(ctx, logr.Discard(), "rg-live", "cluster-empty"); err != nil {
		t.Fatalf("pace over empty state = %v, want nil (the deadline alone must not fail the pace)", err)
	}
	if got := len(startsInStateFileAt(t, statePath)); got != 1 {
		t.Errorf("state has %d starts after an admitted pace, want 1 (the new start)", got)
	}
}

// TestPaceClusterCreateOnceConcurrentSameNameWaitsForInFlight checks the memo's
// singleflight behavior: a concurrent pace of the same cluster name waits for
// the in-flight admission instead of returning immediately beside it.
func TestPaceClusterCreateOnceConcurrentSameNameWaitsForInFlight(t *testing.T) {
	t.Cleanup(func() { pacedClusterCreates.Delete("cluster-concurrent") })

	admits := make(chan struct{}, 2)
	release := make(chan struct{})
	leaderDone := make(chan error, 1)
	go func() {
		leaderDone <- paceClusterCreateOnce(t.Context(), "cluster-concurrent", func(context.Context) error {
			admits <- struct{}{}
			<-release
			return nil
		})
	}()
	<-admits // the leader's admission is in flight

	followerDone := make(chan error, 1)
	go func() {
		followerDone <- paceClusterCreateOnce(t.Context(), "cluster-concurrent", func(context.Context) error {
			admits <- struct{}{}
			return nil
		})
	}()

	// While the leader's admission is in flight, the follower must neither
	// return nor run a second admission.
	select {
	case err := <-followerDone:
		t.Fatalf("follower returned %v while the admission was still in flight", err)
	case <-admits:
		t.Fatal("follower ran a second admission for the same cluster name")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if err := <-leaderDone; err != nil {
		t.Fatalf("leader unexpected error: %v", err)
	}
	if err := <-followerDone; err != nil {
		t.Fatalf("follower unexpected error: %v", err)
	}
	select {
	case <-admits:
		t.Fatal("admission ran more than once for the same cluster name")
	default:
	}
}

// TestPaceClusterCreateOnceRemovesFailedAdmission checks that a failed
// admission does not memoize: a retry after the failure paces again instead of
// bypassing the window.
func TestPaceClusterCreateOnceRemovesFailedAdmission(t *testing.T) {
	t.Cleanup(func() { pacedClusterCreates.Delete("cluster-retry") })

	admits := 0
	admit := func(context.Context) error {
		admits++
		if admits == 1 {
			return context.Canceled
		}
		return nil
	}
	if err := paceClusterCreateOnce(t.Context(), "cluster-retry", admit); !errors.Is(err, context.Canceled) {
		t.Fatalf("first pace = %v, want context.Canceled", err)
	}
	if err := paceClusterCreateOnce(t.Context(), "cluster-retry", admit); err != nil {
		t.Fatalf("retry pace = %v, want nil (re-paced after the failure)", err)
	}
	if admits != 2 {
		t.Errorf("admission ran %d times, want 2 (a failed admission must not memoize)", admits)
	}
}

// TestPaceClusterCreateOnceFollowerCancellationKeepsEntry checks that a
// follower whose own context is cancelled while waiting returns promptly,
// without disturbing the in-flight admission or dropping the memo entry.
func TestPaceClusterCreateOnceFollowerCancellationKeepsEntry(t *testing.T) {
	t.Cleanup(func() { pacedClusterCreates.Delete("cluster-follow") })

	admitted := make(chan struct{}, 1)
	release := make(chan struct{})
	leaderDone := make(chan error, 1)
	go func() {
		leaderDone <- paceClusterCreateOnce(t.Context(), "cluster-follow", func(context.Context) error {
			admitted <- struct{}{}
			<-release
			return nil
		})
	}()
	<-admitted

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	followerDone := make(chan error, 1)
	go func() {
		followerDone <- paceClusterCreateOnce(ctx, "cluster-follow", func(context.Context) error {
			t.Error("follower must not run its own admission")
			return nil
		})
	}()
	select {
	case err := <-followerDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("follower = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("follower did not return after its context was cancelled")
	}

	close(release)
	if err := <-leaderDone; err != nil {
		t.Fatalf("leader unexpected error: %v", err)
	}
	// The completed admission stays memoized: a later pace is a no-op.
	if err := paceClusterCreateOnce(t.Context(), "cluster-follow", func(context.Context) error {
		t.Error("memoized pace must not run another admission")
		return nil
	}); err != nil {
		t.Fatalf("post-completion pace = %v, want nil", err)
	}
}

// TestPaceHCPClusterCreateStartRetryAfterCancellation checks the package-level
// entry point: an admission aborted by cancellation must not memoize, so a
// retry waits for window capacity again instead of bypassing pacing.
func TestPaceHCPClusterCreateStartRetryAfterCancellation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ARTIFACT_DIR", dir)
	t.Setenv(ClusterCreatePacingEnvvar, "1/1h")
	// Saturate the window so every admission must wait.
	saturated, err := yaml.Marshal(clusterCreatePacingState{Starts: []string{
		time.Now().UTC().Format(time.RFC3339),
	}})
	if err != nil {
		t.Fatalf("marshaling seed state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, clusterCreatePacingStateFile), saturated, 0666); err != nil {
		t.Fatalf("writing seed state: %v", err)
	}
	t.Cleanup(func() {
		pacedClusterCreates.Delete(clusterCreatePacingKey("rg-one", "cluster-retry-real"))
	})

	for attempt := 1; attempt <= 2; attempt++ {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err := paceHCPClusterCreateStart(ctx, logr.Discard(), "rg-one", "cluster-retry-real")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("pace %d = %v, want context.Canceled (a retry must re-pace, not memoize the failure)", attempt, err)
		}
	}
}
