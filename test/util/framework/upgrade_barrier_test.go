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
		runID:          os.Getppid(),
		pollInterval:   10 * time.Millisecond,
		upgradeTimeout: 5 * time.Second,
	}

	// Initialise the state file so all workers agree on RunID from the start.
	require.NoError(t, b.withLock(func(state *upgradeBarrierState) (bool, error) {
		state.RunID = os.Getppid()
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
		upgradeTimeout: source.upgradeTimeout,
	}
}

// newTestCoordinator creates an UpgradeCoordinator backed by the same state
// file and lock as the given barrier, with fast poll intervals.
// The coordinator's runID is set to match the source barrier's runID (simulating
// the runtime relationship where workers use os.Getppid() == parent's os.Getpid()).
// It also calls initState() synchronously, mirroring NewUpgradeCoordinator which
// must write the state before any worker calls NewUpgradeBarrier.
func newTestCoordinator(t *testing.T, source *UpgradeBarrier) *UpgradeCoordinator {
	t.Helper()

	lf, err := os.OpenFile(source.lockFile.Name(), os.O_RDWR|os.O_CREATE, 0666)
	require.NoError(t, err, "opening coordinator lock file")
	t.Cleanup(func() { lf.Close() })

	c := &UpgradeCoordinator{
		statePath:         source.statePath,
		lockFile:          lf,
		total:             source.total,
		runID:             source.runID, // must match: at runtime os.Getpid()==parent==os.Getppid() of workers
		pollInterval:      10 * time.Millisecond,
		settleTimeout:     5 * time.Second,
		upgradeRunTimeout: 5 * time.Second,
	}

	require.NoError(t, c.initState(), "coordinator initState")
	return c
}

// TestUpgradeBarrier_SingleSpec verifies that with total=1 the single spec
// checks in, the barrier settles immediately, and the coordinator can mark
// upgrade done so the spec can unblock from waitForUpgrade.
func TestUpgradeBarrier_SingleSpec(t *testing.T) {
	t.Parallel()

	b := newTestBarrier(t, 1)
	coord := newTestCoordinator(t, b)

	// checkIn must settle immediately (1 checked-in >= total=1).
	require.NoError(t, b.checkIn(context.Background()), "checkIn should succeed for a single spec")

	// Coordinator marks upgrade done.
	require.NoError(t, coord.markUpgradeDone(nil), "markUpgradeDone should not error")

	// A second markUpgradeDone is a no-op — original nil error is preserved.
	require.NoError(t, coord.markUpgradeDone(errors.New("ignored")), "second markUpgradeDone should be no-op")

	// waitForUpgrade must return immediately since upgrade_done is already set.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	b2 := newLinkedBarrier(t, b)
	require.NoError(t, b2.waitForUpgrade(ctx), "waitForUpgrade should return immediately when upgrade is already done")
}

// TestUpgradeBarrier_AllSpecsCheckIn verifies that all specs can check in
// concurrently and the barrier settles once every spec is counted.
//
// Each simulated spec gets its own barrier instance with a fresh lock-file fd
// (via newLinkedBarrier) so that syscall.Flock provides mutual exclusion
// between goroutines — exactly as it would between separate OS processes.
func TestUpgradeBarrier_AllSpecsCheckIn(t *testing.T) {
	t.Parallel()

	const total = 3
	b0 := newTestBarrier(t, total)
	barriers := []*UpgradeBarrier{b0, newLinkedBarrier(t, b0), newLinkedBarrier(t, b0)}

	var wg sync.WaitGroup
	errs := make([]error, total)

	for i, b := range barriers {
		wg.Add(1)
		go func(idx int, bar *UpgradeBarrier) {
			defer wg.Done()
			errs[idx] = bar.checkIn(context.Background())
		}(i, b)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "checkIn should not error for spec %d", i)
	}

	state, err := b0.readState()
	require.NoError(t, err)
	assert.Equal(t, total, state.CheckedIn, "all specs should be checked in")
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
	// properly contends with b0's checkIn rather than bypassing the flock.
	b1 := newLinkedBarrier(t, b0)
	b2 := newLinkedBarrier(t, b0)

	var (
		wg       sync.WaitGroup
		checkErr error
	)

	// Spec 0: checks in.
	wg.Add(1)
	go func() {
		defer wg.Done()
		checkErr = b0.checkIn(context.Background())
	}()

	// Give checkIn a moment to start polling before sending aborts.
	time.Sleep(20 * time.Millisecond)

	require.NoError(t, b1.abort(context.Background()))
	require.NoError(t, b2.abort(context.Background()))

	wg.Wait()

	require.NoError(t, checkErr, "checkIn should succeed when survivors remain")
}

// TestUpgradeBarrier_AllAbort verifies that when every spec aborts without
// checking in the coordinator's waitSettled observes settlement and returns nil.
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

	// The coordinator's waitSettled should see aborted_count==total and return nil.
	coord := newTestCoordinator(t, b)
	err := coord.waitSettled(ctx)
	require.NoError(t, err, "coordinator waitSettled should succeed when all specs aborted")

	state, err := b.readState()
	require.NoError(t, err)
	assert.Equal(t, 0, state.CheckedIn, "no spec checked in")
	assert.Equal(t, total, state.AbortedCount, "all specs aborted")
}

// TestUpgradeBarrier_UpgradeError verifies that an upgrade failure written by
// the coordinator is surfaced by waitForUpgrade on waiting specs.
func TestUpgradeBarrier_UpgradeError(t *testing.T) {
	t.Parallel()

	b0 := newTestBarrier(t, 2)
	b1 := newLinkedBarrier(t, b0)
	coord := newTestCoordinator(t, b0)

	// checkIn no longer blocks until settlement; abort b1 after check-in so the
	// coordinator can settle independently (simulates b1 failing to provision).
	require.NoError(t, b0.checkIn(context.Background()))
	require.NoError(t, b1.abort(context.Background()))

	upgradeErr := errors.New("make entrypoint/Region: exit status 1")
	require.NoError(t, coord.markUpgradeDone(upgradeErr))

	// b2 simulates a third "process" (just a reader) checking the upgrade outcome.
	b2 := newLinkedBarrier(t, b0)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := b2.waitForUpgrade(ctx)
	require.Error(t, err, "waitForUpgrade should return the coordinator's error")
	assert.Contains(t, err.Error(), "exit status 1", "error should contain the original message")
}

// TestUpgradeBarrier_WaitForUpgradeContextCancelled verifies that
// waitForUpgrade returns a context error when the context is cancelled before
// the upgrade completes. Uses total=2 so waitForUpgrade actually blocks on
// the state file rather than fast-pathing out.
func TestUpgradeBarrier_WaitForUpgradeContextCancelled(t *testing.T) {
	t.Parallel()

	b := newTestBarrier(t, 2)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := b.waitForUpgrade(ctx)
	require.Error(t, err, "waitForUpgrade should fail on cancelled context")
	assert.ErrorIs(t, err, context.Canceled)
}

// TestUpgradeBarrier_AbortIncrements verifies that each abort call increments
// aborted_count and the barrier settles once checked_in+aborted_count==total.
func TestUpgradeBarrier_AbortIncrements(t *testing.T) {
	t.Parallel()

	const total = 2
	b := newTestBarrier(t, total)
	ctx := context.Background()

	require.NoError(t, b.abort(ctx))
	state, err := b.readState()
	require.NoError(t, err)
	assert.Equal(t, 1, state.AbortedCount, "aborted_count should be 1 after one abort")
	assert.False(t, state.settled(b.total), "barrier should not be settled with only 1 of 2 resolved")

	require.NoError(t, b.abort(ctx))
	state, err = b.readState()
	require.NoError(t, err)
	assert.Equal(t, 2, state.AbortedCount, "aborted_count should be 2 after two aborts")
	assert.True(t, state.settled(b.total), "barrier should be settled once all specs resolved")
}

// TestUpgradeBarrier_AbortSafetyNet verifies that extra abort calls beyond total
// are silently dropped by the safety-net guard inside withLock. Uses total=2
// so that abort reaches the file-based path (total=1 fast-paths out entirely).
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
	assert.Equal(t, total, state.AbortedCount, "extra abort beyond settled barrier must not increment aborted_count")
}

// TestUpgradeCoordinator_MarkUpgradeDoneIdempotent verifies that calling
// markUpgradeDone after the upgrade is already marked done is a no-op and
// does not overwrite the original error.
func TestUpgradeCoordinator_MarkUpgradeDoneIdempotent(t *testing.T) {
	t.Parallel()

	b0 := newTestBarrier(t, 2)
	b1 := newLinkedBarrier(t, b0)
	coord := newTestCoordinator(t, b0)

	// checkIn no longer blocks until settlement; abort b1 after check-in so the
	// coordinator can settle independently.
	require.NoError(t, b0.checkIn(context.Background()))
	require.NoError(t, b1.abort(context.Background()))

	firstErr := errors.New("original error")
	require.NoError(t, coord.markUpgradeDone(firstErr))

	// Second call should be a no-op — original error is preserved.
	require.NoError(t, coord.markUpgradeDone(nil))

	state, err := b0.readState()
	require.NoError(t, err)
	assert.Equal(t, firstErr.Error(), state.UpgradeError, "original error must be preserved")
}

// TestUpgradeBarrier_CheckIn_UpgradeDoneCtxCancelledOnDone verifies that the
// context returned by CheckIn is cancelled when the coordinator marks the
// upgrade done in the state file. This is the primary signal for specs to stop
// their during-upgrade Consistently validation.
func TestUpgradeBarrier_CheckIn_UpgradeDoneCtxCancelledOnDone(t *testing.T) {
	t.Parallel()

	const total = 2
	b0 := newTestBarrier(t, total)
	b1 := newLinkedBarrier(t, b0)

	// Pre-seed b0 as already checked-in to represent the peer spec.
	// checkIn no longer blocks for settlement, so this is only needed to give
	// the state file a realistic checked_in count.
	require.NoError(t, b0.withLock(func(state *upgradeBarrierState) (bool, error) {
		state.CheckedIn = 1
		return true, nil
	}), "pre-seeding b0 as checked-in")
	b0.checkedIn = true

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// b1.CheckIn increments checked_in to 2, starts the polling goroutine,
	// and returns upgradeDoneCtx immediately (no settle-wait).
	upgradeDoneCtx, err := b1.CheckIn(ctx)
	require.NoError(t, err, "CheckIn should succeed")

	// upgradeDoneCtx must not be cancelled yet.
	select {
	case <-upgradeDoneCtx.Done():
		t.Fatal("upgradeDoneCtx was cancelled before upgrade finished")
	default:
	}

	// Coordinator marks upgrade done; polling goroutine should
	// cancel upgradeDoneCtx. pollInterval in tests is 10ms so this should be fast.
	coord := newTestCoordinator(t, b0)
	require.NoError(t, coord.markUpgradeDone(nil))

	select {
	case <-upgradeDoneCtx.Done():
		// pass
	case <-time.After(time.Second):
		t.Fatal("upgradeDoneCtx was not cancelled within 1s after coordinator marked upgrade done")
	}
}

// TestUpgradeBarrier_WaitForUpgrade_NonRunnerPath verifies that WaitForUpgrade
// polls the state file and returns when UpgradeDone is true.
func TestUpgradeBarrier_WaitForUpgrade_NonRunnerPath(t *testing.T) {
	t.Parallel()

	b0 := newTestBarrier(t, 2)
	b1 := newLinkedBarrier(t, b0)
	coord := newTestCoordinator(t, b0)

	// Coordinator writes upgrade done after a short delay.
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = coord.markUpgradeDone(nil)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := b1.WaitForUpgrade(ctx)
	require.NoError(t, err, "WaitForUpgrade should return nil when upgrade succeeds")
}

// TestUpgradeCoordinator_RunsAfterSettled verifies that the UpgradeCoordinator
// waits for all specs to check in before the upgrade step runs.
func TestUpgradeCoordinator_RunsAfterSettled(t *testing.T) {
	t.Parallel()

	const total = 2
	b0 := newTestBarrier(t, total)
	b1 := newLinkedBarrier(t, b0)
	coord := newTestCoordinator(t, b0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Both specs check in concurrently so the barrier settles.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = b0.withLock(func(state *upgradeBarrierState) (bool, error) {
			state.CheckedIn++
			return true, nil
		})
	}()
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		_ = b1.withLock(func(state *upgradeBarrierState) (bool, error) {
			state.CheckedIn++
			return true, nil
		})
	}()

	// waitSettled should unblock once both specs have checked in.
	require.NoError(t, coord.waitSettled(ctx), "coordinator waitSettled should succeed")

	wg.Wait()

	state, err := b0.readState()
	require.NoError(t, err)
	assert.Equal(t, total, state.CheckedIn, "all specs should be checked in before coordinator proceeds")
}

// TestUpgradeCoordinator_SkipsUpgradeWhenAllAborted verifies that the
// UpgradeCoordinator returns early without running the pipeline when all specs
// aborted before checking in — there are no waiters and the parent process is
// about to exit anyway.
func TestUpgradeCoordinator_SkipsUpgradeWhenAllAborted(t *testing.T) {
	// Not parallel: uses t.Setenv which requires serial execution.
	// DEPLOY_ENV must be set so Run() passes the early env var check and
	// reaches the all-aborted detection path.
	t.Setenv("DEPLOY_ENV", "test")

	const total = 2
	b0 := newTestBarrier(t, total)
	b1 := newLinkedBarrier(t, b0)
	coord := newTestCoordinator(t, b0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Both specs abort before checking in; barrier settles with CheckedIn=0.
	require.NoError(t, b0.abort(ctx))
	require.NoError(t, b1.abort(ctx))

	// Run observes the settled-but-no-checkin state and should abort early.
	err := coord.Run(ctx)
	require.Error(t, err, "coordinator should return an error when all specs aborted")
	assert.Contains(t, err.Error(), "all specs aborted", "error should describe the situation")

	// UpgradeDone is written by the deferred markUpgradeDone even on the
	// all-aborted path. This is harmless — no spec is polling — and it
	// ensures any late-arriving spec gets an immediate error rather than hanging.
	state, readErr := b0.readState()
	require.NoError(t, readErr)
	assert.True(t, state.UpgradeDone, "UpgradeDone should be written even when all specs aborted")
	assert.Contains(t, state.UpgradeError, "all specs aborted", "UpgradeError should describe the situation")
}

// writes UpgradeDone=true to the state file after the upgrade completes,
// allowing waiting specs to unblock.
func TestUpgradeCoordinator_WritesUpgradeDone(t *testing.T) {
	t.Parallel()

	b0 := newTestBarrier(t, 1)
	coord := newTestCoordinator(t, b0)

	// Pre-settle the barrier so the coordinator skips the wait phase.
	require.NoError(t, b0.withLock(func(state *upgradeBarrierState) (bool, error) {
		state.CheckedIn = 1
		return true, nil
	}), "pre-settling barrier")

	upgradeErr := errors.New("simulated upgrade failure")
	require.NoError(t, coord.markUpgradeDone(upgradeErr), "coordinator markUpgradeDone should succeed")

	state, err := b0.readState()
	require.NoError(t, err)
	assert.True(t, state.UpgradeDone, "UpgradeDone should be true after coordinator marks done")
	assert.Equal(t, upgradeErr.Error(), state.UpgradeError, "UpgradeError should match")

	// Idempotency: second call with nil error must not overwrite.
	require.NoError(t, coord.markUpgradeDone(nil))

	state, err = b0.readState()
	require.NoError(t, err)
	assert.Equal(t, upgradeErr.Error(), state.UpgradeError, "original error must be preserved on second call")
}
