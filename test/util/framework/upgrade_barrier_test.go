// Copyright 2025 Microsoft Corporation
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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestBarrier creates an UpgradeBarrier backed by a unique temporary
// directory with a fast poll interval. It builds the struct directly (same
// package) to avoid mutating env vars, so tests can safely call t.Parallel().
func newTestBarrier(t *testing.T, total int) *UpgradeBarrier {
	t.Helper()

	dir := t.TempDir()
	lockDir := t.TempDir() // separate dir for the lock file to avoid sharing
	lockPath := filepath.Join(lockDir, "upgrade-barrier.lock")

	lf, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0666)
	require.NoError(t, err, "opening test barrier lock file")
	t.Cleanup(func() { lf.Close() })

	b := &UpgradeBarrier{
		statePath:      filepath.Join(dir, "upgrade-barrier-state.yaml"),
		lockFile:       lf,
		total:          total,
		pollInterval:   10 * time.Millisecond,
		settleTimeout:  5 * time.Second,
		upgradeTimeout: 5 * time.Second,
	}

	// Initialise the state file so readState never sees an empty Total.
	require.NoError(t, b.withLock(func(state *upgradeBarrierState) (bool, error) {
		state.Total = total
		return true, nil
	}), "initialising test barrier state")

	return b
}

// newLinkedBarrier simulates a second OS process accessing the same barrier
// state. It opens the lock file with a fresh file descriptor so that
// syscall.Flock treats it as an independent lock entry — exactly as two
// separate processes would behave. Sharing the fd of an existing barrier
// would make flock a no-op between goroutines in the same process.
func newLinkedBarrier(t *testing.T, source *UpgradeBarrier) *UpgradeBarrier {
	t.Helper()

	lf, err := os.OpenFile(source.lockFile.Name(), os.O_RDWR|os.O_CREATE, 0666)
	require.NoError(t, err, "opening linked barrier lock file")
	t.Cleanup(func() { lf.Close() })

	return &UpgradeBarrier{
		statePath:      source.statePath,
		lockFile:       lf,
		total:          source.total,
		pollInterval:   source.pollInterval,
		settleTimeout:  source.settleTimeout,
		upgradeTimeout: source.upgradeTimeout,
	}
}

// TestUpgradeBarrier_SingleSpec verifies that with total=1 the single spec is
// immediately elected runner and the barrier settles without any waiting.
// This is the common case for local development runs (UPGRADE_SPEC_COUNT=1).
func TestUpgradeBarrier_SingleSpec(t *testing.T) {
	t.Parallel()

	b := newTestBarrier(t, 1)

	// CheckIn must settle immediately (1 checked-in >= total=1) and elect runner.
	isRunner, err := b.checkIn(context.Background())
	require.NoError(t, err, "CheckIn should succeed for a single spec")
	assert.True(t, isRunner, "single spec should be elected runner")

	// Runner marks upgrade done.
	require.NoError(t, b.markUpgradeDone(nil), "MarkUpgradeDone should not error")

	// A second MarkUpgradeDone is a no-op — original nil error is preserved.
	require.NoError(t, b.markUpgradeDone(errors.New("ignored")), "second MarkUpgradeDone should be no-op")

	// WaitForUpgrade must return immediately since upgrade_done is already set.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	b2 := newLinkedBarrier(t, b)
	require.NoError(t, b2.waitForUpgrade(ctx), "WaitForUpgrade should return immediately when upgrade is already done")
}

// TestUpgradeBarrier_FirstCheckinIsRunner verifies that the first spec to call
// CheckIn is elected runner and subsequent specs are not.
//
// Each simulated spec gets its own barrier instance with a fresh lock-file fd
// (via newLinkedBarrier) so that syscall.Flock provides mutual exclusion
// between goroutines — exactly as it would between separate OS processes.
func TestUpgradeBarrier_FirstCheckinIsRunner(t *testing.T) {
	t.Parallel()

	const total = 3
	b0 := newTestBarrier(t, total)
	barriers := []*UpgradeBarrier{b0, newLinkedBarrier(t, b0), newLinkedBarrier(t, b0)}

	// Simulate three specs concurrently checking in.
	type result struct {
		isRunner bool
		err      error
	}
	results := make([]result, total)
	var wg sync.WaitGroup

	for i, b := range barriers {
		wg.Add(1)
		go func(idx int, bar *UpgradeBarrier) {
			defer wg.Done()
			isRunner, err := bar.checkIn(context.Background())
			results[idx] = result{isRunner: isRunner, err: err}
		}(i, b)
	}
	wg.Wait()

	runnerCount := 0
	for _, r := range results {
		require.NoError(t, r.err, "CheckIn should not error")
		if r.isRunner {
			runnerCount++
		}
	}
	assert.Equal(t, 1, runnerCount, "exactly one spec should be elected runner")
}

// TestUpgradeBarrier_PartialAbort verifies that when some specs abort and at
// least one checks in, the barrier settles and the surviving spec proceeds.
//
// Each simulated spec uses a separate barrier instance (fresh lock fd) so that
// concurrent flock calls are properly serialised, matching real OS-process behaviour.
func TestUpgradeBarrier_PartialAbort(t *testing.T) {
	t.Parallel()

	const total = 3
	b0 := newTestBarrier(t, total)
	// Spec 1 and 2 will abort; give them their own fd so Abort's withLock
	// properly contends with b0's CheckIn rather than bypassing the flock.
	b1 := newLinkedBarrier(t, b0)
	b2 := newLinkedBarrier(t, b0)

	var (
		wg       sync.WaitGroup
		isRunner bool
		checkErr error
	)

	// Spec 0: checks in (runner candidate).
	wg.Add(1)
	go func() {
		defer wg.Done()
		isRunner, checkErr = b0.checkIn(context.Background())
	}()

	// Give CheckIn a moment to start polling before sending aborts.
	time.Sleep(20 * time.Millisecond)

	require.NoError(t, b1.abort(context.Background()))
	require.NoError(t, b2.abort(context.Background()))

	wg.Wait()

	require.NoError(t, checkErr, "CheckIn should succeed when survivors remain")
	assert.True(t, isRunner, "surviving spec should be elected runner")
}

// TestUpgradeBarrier_AllAbort verifies that when every spec aborts without
// checking in the barrier still settles and waitSettled returns nil.
//
// In practice this path is never observed by a live spec: any spec that
// reaches checkIn has already incremented checked_in (Phase 1) before calling
// waitSettled (Phase 2), so checked_in is always ≥ 1 from within checkIn.
// The test exercises the underlying waitSettled behaviour directly.
func TestUpgradeBarrier_AllAbort(t *testing.T) {
	t.Parallel()

	const total = 2
	b := newTestBarrier(t, total)
	b2 := newLinkedBarrier(t, b)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Both specs abort without checking in.
	require.NoError(t, b.abort(ctx))
	require.NoError(t, b2.abort(ctx))

	// waitSettled should return nil — the barrier is settled (aborted_count==total).
	b3 := newLinkedBarrier(t, b)
	err := b3.waitSettled(ctx)
	require.NoError(t, err, "waitSettled should succeed when barrier is settled, even if all aborted")

	state, err := b3.readState()
	require.NoError(t, err)
	assert.Equal(t, 0, state.CheckedIn, "no spec checked in")
	assert.Equal(t, total, state.AbortedCount, "all specs aborted")
}

// TestUpgradeBarrier_UpgradeError verifies that an upgrade failure written by
// the runner is surfaced by WaitForUpgrade on non-runner specs.
func TestUpgradeBarrier_UpgradeError(t *testing.T) {
	t.Parallel()

	// total=2: one runner (b0) and one waiter (b1) so that WaitForUpgrade
	// actually blocks on file state rather than fast-pathing out.
	b0 := newTestBarrier(t, 2)
	b1 := newLinkedBarrier(t, b0)

	// b1 must abort concurrently so b0.checkIn's waitSettled can settle.
	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = b1.abort(context.Background())
	}()

	isRunner, err := b0.checkIn(context.Background())
	require.NoError(t, err)
	require.True(t, isRunner)

	upgradeErr := errors.New("make entrypoint/Region: exit status 1")
	require.NoError(t, b0.markUpgradeDone(upgradeErr))

	// b2 simulates a third "process" (just a reader) checking the upgrade outcome.
	b2 := newLinkedBarrier(t, b0)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err = b2.waitForUpgrade(ctx)
	require.Error(t, err, "WaitForUpgrade should return the runner's error")
	assert.Contains(t, err.Error(), "exit status 1", "error should contain the original message")
}

// TestUpgradeBarrier_WaitForUpgradeContextCancelled verifies that
// WaitForUpgrade returns a context error when the context is cancelled before
// the upgrade completes. Uses total=2 so WaitForUpgrade actually blocks on
// the state file rather than fast-pathing out.
func TestUpgradeBarrier_WaitForUpgradeContextCancelled(t *testing.T) {
	t.Parallel()

	b := newTestBarrier(t, 2)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := b.waitForUpgrade(ctx)
	require.Error(t, err, "WaitForUpgrade should fail on cancelled context")
	assert.ErrorIs(t, err, context.Canceled)
}

// TestUpgradeBarrier_AbortIncrements verifies that each Abort call increments
// aborted_count and the barrier settles once checked_in+aborted_count==total.
// The test skeleton in region_upgrade.go uses a !checkedIn guard so Abort is
// only ever called once per spec; this test confirms the mechanics are correct.
func TestUpgradeBarrier_AbortIncrements(t *testing.T) {
	t.Parallel()

	const total = 2
	b := newTestBarrier(t, total)
	ctx := context.Background()

	require.NoError(t, b.abort(ctx))
	state, err := b.readState()
	require.NoError(t, err)
	assert.Equal(t, 1, state.AbortedCount, "aborted_count should be 1 after one Abort")
	assert.False(t, state.settled(), "barrier should not be settled with only 1 of 2 resolved")

	require.NoError(t, b.abort(ctx))
	state, err = b.readState()
	require.NoError(t, err)
	assert.Equal(t, 2, state.AbortedCount, "aborted_count should be 2 after two Aborts")
	assert.True(t, state.settled(), "barrier should be settled once all specs resolved")
}

// TestUpgradeBarrier_AbortSafetyNet verifies that extra Abort calls beyond total
// are silently dropped by the safety-net guard inside withLock. Uses total=2
// so that Abort reaches the file-based path (total=1 fast-paths out entirely).
func TestUpgradeBarrier_AbortSafetyNet(t *testing.T) {
	t.Parallel()

	const total = 2
	b0 := newTestBarrier(t, total)
	b1 := newLinkedBarrier(t, b0)
	b2 := newLinkedBarrier(t, b0)
	ctx := context.Background()

	require.NoError(t, b0.abort(ctx))
	require.NoError(t, b1.abort(ctx)) // settles barrier

	// Extra call from b2: barrier already settled, safety net must drop it.
	require.NoError(t, b2.abort(ctx))

	state, err := b0.readState()
	require.NoError(t, err)
	assert.Equal(t, total, state.AbortedCount, "extra Abort beyond settled barrier must not increment aborted_count")
}

// TestUpgradeBarrier_MarkUpgradeDoneIdempotent verifies that calling
// MarkUpgradeDone after the upgrade is already marked done is a no-op and
// does not overwrite the original error. Uses total=2 so MarkUpgradeDone
// reaches the file-based path (total=1 fast-paths out entirely).
func TestUpgradeBarrier_MarkUpgradeDoneIdempotent(t *testing.T) {
	t.Parallel()

	b0 := newTestBarrier(t, 2)
	b1 := newLinkedBarrier(t, b0)

	// b1 must abort concurrently so b0.checkIn's waitSettled can settle.
	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = b1.abort(context.Background())
	}()

	isRunner, err := b0.checkIn(context.Background())
	require.NoError(t, err)
	require.True(t, isRunner)

	firstErr := errors.New("original error")
	require.NoError(t, b0.markUpgradeDone(firstErr))

	// Second call should be a no-op — original error is preserved.
	require.NoError(t, b0.markUpgradeDone(nil))

	state, err := b0.readState()
	require.NoError(t, err)
	assert.Equal(t, firstErr.Error(), state.UpgradeError, "original error must be preserved")
}

// TestNewUpgradeBarrier_InvalidEnvVar verifies that an invalid UPGRADE_SPEC_COUNT
// returns a descriptive error rather than silently defaulting.
// Not parallel: uses t.Setenv which mutates global env state.
func TestNewUpgradeBarrier_InvalidEnvVar(t *testing.T) {
	t.Setenv("ARTIFACT_DIR", t.TempDir())
	t.Setenv(UpgradeSpecCountEnvvar, "not-a-number")
	_, err := NewUpgradeBarrier()
	require.Error(t, err, "NewUpgradeBarrier should fail on non-numeric env var")
	assert.Contains(t, err.Error(), UpgradeSpecCountEnvvar)
}

// TestNewUpgradeBarrier_MissingEnvVar verifies that an absent or empty
// UPGRADE_SPEC_COUNT returns a descriptive error — no silent default.
// Not parallel: uses t.Setenv which mutates global env state.
func TestNewUpgradeBarrier_MissingEnvVar(t *testing.T) {
	t.Setenv("ARTIFACT_DIR", t.TempDir())
	t.Setenv(UpgradeSpecCountEnvvar, "")
	_, err := NewUpgradeBarrier()
	require.Error(t, err, "NewUpgradeBarrier should fail when UPGRADE_SPEC_COUNT is unset")
	assert.Contains(t, err.Error(), UpgradeSpecCountEnvvar, "error should name the missing variable")
	assert.Contains(t, err.Error(), "--output names", "error should include the command to compute the value")
}

// TestNewUpgradeBarrier_ZeroEnvVar verifies that UPGRADE_SPEC_COUNT=0 is rejected.
// Not parallel: uses t.Setenv which mutates global env state.
func TestNewUpgradeBarrier_ZeroEnvVar(t *testing.T) {
	t.Setenv("ARTIFACT_DIR", t.TempDir())
	t.Setenv(UpgradeSpecCountEnvvar, "0")
	_, err := NewUpgradeBarrier()
	require.Error(t, err, "NewUpgradeBarrier should fail when UPGRADE_SPEC_COUNT is zero")
	assert.Contains(t, err.Error(), "positive integer")
}
