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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/onsi/ginkgo/v2"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/test/pkg/filelock"
)

// upgradeInPlaceSpecCount is set once at suite-setup time (before any specs run)
// by the main package via SetUpgradeInPlaceSpecCount. NewUpgradeBarrier reads it
// so each spec knows the total number of participants without an env var or a
// hard-coded constant that drifts from the real spec count.
var upgradeInPlaceSpecCount int

// SetUpgradeInPlaceSpecCount stores the number of UpgradeInPlace specs that will
// run in this suite invocation. It must be called from main's setupCli(), after
// BuildExtensionTestSpecsFromOpenShiftGinkgoSuite(), and before any specs start.
func SetUpgradeInPlaceSpecCount(n int) {
	upgradeInPlaceSpecCount = n
}

// defaultUpgradeBarrierPollInterval is how often each spec re-reads the state
// file while waiting for the barrier to settle or for the upgrade to finish.
// Both wait phases are measured in tens of minutes, so 30s polling is more
// than sufficient and avoids unnecessary file I/O.
const defaultUpgradeBarrierPollInterval = 30 * time.Second

// defaultUpgradeTimeout is the maximum time specs will wait in WaitForUpgrade
// for the UpgradeCoordinator to write UpgradeDone to the state file. This is a
// safety net for the case where the coordinator crashes hard after the barrier
// settles without ever signalling completion. Set slightly longer than
// defaultUpgradeRunTimeout so that if the coordinator times out it still has a
// window to write its error before specs also time out — surfacing the real
// cause rather than a generic "coordinator never signalled" message.
const defaultUpgradeTimeout = 60 * time.Minute

// upgradeBarrierState is the on-disk representation of the barrier.
type upgradeBarrierState struct {
	// CheckedIn counts specs that successfully provisioned their cluster and
	// baseline and are ready for the upgrade.
	CheckedIn int `yaml:"checked_in"`
	// AbortedCount counts specs that called abort() because they failed to
	// provision or capture their baseline. These specs are no longer
	// participating; survivors proceed once the barrier settles.
	AbortedCount int `yaml:"aborted_count"`
	// RunID holds the PID of the parent run-suite process (os.Getpid() in the
	// UpgradeCoordinator). In subprocess mode (normal parallel run) worker specs
	// match this via os.Getppid(); in in-process mode (single spec, same process)
	// they match via os.Getpid(). A new invocation spawns a new parent process
	// with a different PID, which triggers a stale-state reset.
	RunID int `yaml:"run_id"`
	// UpgradeDone is set to true by the UpgradeCoordinator once the Region
	// entrypoint pipeline has finished (successfully or not).
	UpgradeDone bool `yaml:"upgrade_done"`
	// UpgradeError is the error message written by the UpgradeCoordinator when
	// the upgrade failed. Empty string means success.
	UpgradeError string `yaml:"upgrade_error,omitempty"`
}

// settled reports whether every participating spec has either checked in or
// aborted, meaning the UpgradeCoordinator can start the upgrade.
func (s *upgradeBarrierState) settled(total int) bool {
	return s.CheckedIn+s.AbortedCount >= total
}

// UpgradeBarrier coordinates a set of parallel UpgradeInPlace specs so that
// all specs finish provisioning their clusters before the UpgradeCoordinator
// (running in the parent run-suite process) executes "make entrypoint/Region",
// after which every spec validates its own cluster independently.
//
// Cross-process synchronisation uses a YAML state file protected by an
// exclusive flock (the same pattern as the identity-pool lease), so it works
// when parallel Ginkgo workers are separate OS processes.
//
// Typical usage inside an It block (call after NewTestContext so barrier
// DeferCleanups run before tc teardown in FILO order):
//
//	tc := framework.NewTestContext()
//
//	barrier, err := framework.NewUpgradeBarrier()
//	Expect(err).NotTo(HaveOccurred(), "failed to create upgrade barrier")
//
//	// ... provisioning and baseline capture ...
//
//	// CheckIn settles the barrier and returns a context cancelled when the
//	// UpgradeCoordinator marks the upgrade done.
//	upgradeDoneCtx, err := barrier.CheckIn(ctx)
//	Expect(err).NotTo(HaveOccurred(), "barrier check-in failed")
//
//	// Consistently exits naturally when upgradeDoneCtx is cancelled.
//	Consistently(validateFn, upgradeDoneCtx, pollInterval).Should(Succeed())
//
//	// Collect the upgrade result; fast because upgrade already finished.
//	err = barrier.WaitForUpgrade(ctx)
//	Expect(err).NotTo(HaveOccurred(), "upgrade phase failed")
//
//	// ... post-upgrade per-spec validation ...
type UpgradeBarrier struct {
	statePath      string
	lockFile       *os.File
	total          int
	runID          int // PID that identifies this suite run; set to os.Getppid() in subprocess mode or os.Getpid() in in-process mode
	pollInterval   time.Duration
	upgradeTimeout time.Duration

	// checkedIn is set by checkIn (Phase 1) and read by the abort DeferCleanup
	// registered in registerGinkgoCleanup to decide whether to signal abort.
	checkedIn bool
}

// NewUpgradeBarrier creates an UpgradeBarrier backed by a YAML state file at
// $ARTIFACT_DIR/upgrade-barrier-state.yaml and a lock file at
// os.TempDir()/upgrade-barrier.lock.
//
// The total number of participating specs is read from the package-level
// upgradeInPlaceSpecCount variable, which must be set by calling
// SetUpgradeInPlaceSpecCount from main's setupCli() before any specs run.
// NewUpgradeBarrier returns an error if that count is still zero.
//
// ARTIFACT_DIR is optional; when absent the state file falls back to
// os.TempDir() so local single-spec runs work without CI scaffolding.
func NewUpgradeBarrier() (*UpgradeBarrier, error) {
	total := upgradeInPlaceSpecCount
	if total <= 0 {
		return nil, fmt.Errorf("NewUpgradeBarrier: spec count not set; " +
			"call framework.SetUpgradeInPlaceSpecCount from main before running tests")
	}

	lockPath := upgradeLockPath()
	lf, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return nil, fmt.Errorf("opening upgrade barrier lock file %s: %w", lockPath, err)
	}

	statePath := upgradeStatePath()

	b := &UpgradeBarrier{
		statePath:      statePath,
		lockFile:       lf,
		total:          total,
		pollInterval:   defaultUpgradeBarrierPollInterval,
		upgradeTimeout: defaultUpgradeTimeout,
	}

	// The state file must already be initialised by the UpgradeCoordinator
	// (NewUpgradeCoordinator calls initState synchronously in BeforeAll, before
	// any worker is dispatched). A matching RunID confirms we are part of the
	// same suite invocation; if the IDs differ the coordinator is out of sync
	// with this worker and coordination will fail, so we return an error rather
	// than silently resetting state that belongs to a running coordinator.
	//
	// Two execution modes are supported:
	//  - Subprocess mode (normal parallel run): each spec runs in a separate OS
	//    process spawned by run-suite. The coordinator's PID == os.Getppid() here.
	//  - In-process mode (single spec): run-suite runs the spec in the same
	//    process as the coordinator. The coordinator's PID == os.Getpid() here.
	if err := b.withLock(func(state *upgradeBarrierState) (bool, error) {
		switch state.RunID {
		case os.Getppid():
			b.runID = os.Getppid() // subprocess: parent is the coordinator
		case os.Getpid():
			b.runID = os.Getpid() // in-process: we share the process with the coordinator
		default:
			return false, fmt.Errorf(
				"upgrade barrier: RunID mismatch (state=%d, own=%d, parent=%d); "+
					"the UpgradeCoordinator must initialise the state file before workers start. "+
					"If invoking via 'run-test', note that upgrade/in-place specs are not supported "+
					"with 'run-test' — use 'run-suite upgrade/in-place' or CI instead",
				state.RunID, os.Getpid(), os.Getppid())
		}
		return false, nil
	}); err != nil {
		lf.Close()
		return nil, fmt.Errorf("initialising upgrade barrier state: %w", err)
	}

	// When constructed inside a running Ginkgo spec, register the two barrier
	// DeferCleanups so the test body stays free of bookkeeping. The cleanups
	// are intentionally registered here (after NewTestContext) so they run
	// before the test-context teardown in FILO order, unblocking other specs
	// as early as possible.
	if ginkgo.GetSuite().InRunPhase() {
		b.registerGinkgoCleanup()
	}

	return b, nil
}

// CheckIn atomically increments checked_in and returns immediately — it does
// not wait for the barrier to settle. The UpgradeCoordinator running in the
// parent process independently polls for settlement and begins the upgrade once
// all specs have checked in or aborted.
//
// CheckIn starts a lightweight background goroutine that polls the state file
// and cancels the returned upgradeDoneCtx when the UpgradeCoordinator marks the
// upgrade done. All specs behave identically — there is no runner election.
//
// Callers should pass upgradeDoneCtx to a Consistently block for during-upgrade
// validation. The validation window spans from check-in until upgrade completion
// (covering any remaining provisioning time of peer specs plus the full upgrade)
// which is intentionally broader than "strictly during upgrade". Then call
// WaitForUpgrade to retrieve the upgrade result.
func (b *UpgradeBarrier) CheckIn(ctx context.Context) (upgradeDoneCtx context.Context, err error) {
	if err := b.checkIn(ctx); err != nil {
		return ctx, err
	}

	upgradeDoneCtx, upgradeDoneCancel := context.WithCancel(ctx)

	// Poll the state file and cancel upgradeDoneCtx when the coordinator marks
	// the upgrade done. The goroutine stops when ctx is cancelled (spec ends)
	// so it does not outlive the spec.
	go func() {
		defer upgradeDoneCancel()
		ticker := time.NewTicker(b.pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			done, _, pollErr := b.readUpgradeDone()
			if pollErr != nil {
				ginkgo.GinkgoLogr.Error(pollErr, "upgrade barrier: failed to poll upgrade done state")
				continue
			}
			if done {
				return
			}
		}
	}()

	return upgradeDoneCtx, nil
}

// WaitForUpgrade polls the state file until the UpgradeCoordinator has marked
// the upgrade done, then returns the coordinator's error (if any).
//
// When called after upgradeDoneCtx has been cancelled (normal flow), the state
// file already shows UpgradeDone=true so this returns on the first read.
func (b *UpgradeBarrier) WaitForUpgrade(ctx context.Context) error {
	return b.waitForUpgrade(ctx)
}

// CheckInAndWait is a convenience wrapper that calls CheckIn then WaitForUpgrade
// back-to-back, skipping any during-upgrade validation. Use CheckIn + WaitForUpgrade
// separately when per-spec validation during the upgrade is needed.
func (b *UpgradeBarrier) CheckInAndWait(ctx context.Context) error {
	if _, err := b.CheckIn(ctx); err != nil {
		return err
	}
	return b.WaitForUpgrade(ctx)
}

// repoRoot derives the repository root from the test binary path.
// The binary is built at <repo-root>/test/aro-hcp-tests (see test/Makefile),
// so the repo root is two directory levels above the executable.
// Using the executable path rather than os.Getwd() makes make invocations
// resilient when the test is launched from any working directory.
func repoRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolving executable path: %w", err)
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("evaluating symlinks for %q: %w", exe, err)
	}
	// real == <repo-root>/test/aro-hcp-tests → Dir → test/ → Dir → repo root
	return filepath.Dir(filepath.Dir(real)), nil
}

// registerGinkgoCleanup registers DeferCleanup handlers on the current Ginkgo
// node for all barrier-owned resources:
//
//   - abort: fires if the spec fails before CheckIn so surviving specs are not
//     blocked waiting for a participant that will never arrive.
//   - lock file close: releases the OS file descriptor held since construction.
func (b *UpgradeBarrier) registerGinkgoCleanup() {
	// The lock file must outlive the abort cleanup which acquires the flock.
	ginkgo.DeferCleanup(func() {
		if err := b.lockFile.Close(); err != nil {
			ginkgo.GinkgoLogr.Error(err, "upgrade barrier: failed to close lock file")
		}
	}, AnnotatedLocation("upgrade barrier: close lock file"))

	// Registered second → runs before the lock file is closed.
	ginkgo.DeferCleanup(func(ctx context.Context) {
		if b.checkedIn {
			return
		}
		if err := b.abort(ctx); err != nil {
			ginkgo.GinkgoLogr.Error(err, "upgrade barrier: abort failed; other specs may hang until settleTimeout")
		}
	}, AnnotatedLocation("upgrade barrier: abort if not checked in"))
}

// checkIn atomically increments checked_in. The UpgradeCoordinator (running in
// the parent run-suite process) independently polls for settlement and starts
// the upgrade once all specs have checked in or aborted — there is no need for
// each spec to also wait here.
func (b *UpgradeBarrier) checkIn(ctx context.Context) error {
	if err := b.withLock(func(state *upgradeBarrierState) (bool, error) {
		state.CheckedIn++
		return true, nil
	}); err != nil {
		return fmt.Errorf("check-in: %w", err)
	}

	// Set immediately after the write: if the lock call above succeeded but a
	// later step fails, the abort DeferCleanup must not also increment
	// aborted_count — checked_in is already written.
	b.checkedIn = true
	return nil
}

// abort signals that this spec is no longer participating because it failed to
// provision its cluster or capture its baseline. It increments aborted_count
// so the UpgradeCoordinator's settlement check can account for this spec and
// proceed without it.
//
// abort carries "I'm out" semantics: survivors are unaffected and continue to
// the upgrade as long as at least one spec checked in.
func (b *UpgradeBarrier) abort(ctx context.Context) error {
	return b.withLock(func(state *upgradeBarrierState) (bool, error) {
		// Safety net: never let aborted_count push the settled sum above total.
		// In normal usage the !checkedIn guard in registerGinkgoCleanup ensures
		// abort is called at most once per spec, so this branch should never
		// fire. It protects against a bug or future misuse that could deadlock
		// survivors by overcounting and permanently satisfying settled() with
		// wrong counts.
		if state.CheckedIn+state.AbortedCount >= b.total {
			return false, nil
		}
		state.AbortedCount++
		return true, nil
	})
}

// waitForUpgrade polls the state file until the UpgradeCoordinator has marked
// the upgrade done. It returns the upgrade_error written by the coordinator,
// if any.
//
// The state is read before waiting so that when called after upgradeDoneCtx has
// been cancelled (normal flow), the function returns on the first iteration
// without waiting a full pollInterval.
//
// An inner deadline of b.upgradeTimeout is applied on top of ctx so that
// waiters fail with a clear error if the coordinator crashes after check-in
// without calling markUpgradeDone, rather than hanging until Prow kills the job.
func (b *UpgradeBarrier) waitForUpgrade(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, b.upgradeTimeout)
	defer cancel()

	ticker := time.NewTicker(b.pollInterval)
	defer ticker.Stop()

	for {
		done, upgradeErr, err := b.readUpgradeDone()
		if err != nil {
			return fmt.Errorf("reading upgrade barrier state: %w", err)
		}
		if done {
			if upgradeErr != "" {
				return fmt.Errorf("upgrade coordinator reported failure: %s", upgradeErr)
			}
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for upgrade to complete: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// readUpgradeDone reads the state file and reports upgrade_done and
// upgrade_error without acquiring the lock (eventually consistent read).
func (b *UpgradeBarrier) readUpgradeDone() (done bool, upgradeErr string, err error) {
	state, err := b.readState()
	if err != nil {
		return false, "", err
	}
	return state.UpgradeDone, state.UpgradeError, nil
}

// withLock acquires the exclusive flock, reads the current state, calls fn,
// and — if fn returns dirty=true — writes the updated state back. The flock
// is released before returning.
//
// The flock is the sole synchronisation primitive here. Each parallel Ginkgo
// worker runs in its own OS process, so no intra-process mutex is needed.
func (b *UpgradeBarrier) withLock(fn func(state *upgradeBarrierState) (dirty bool, err error)) error {
	if err := filelock.Lock(b.lockFile.Fd()); err != nil {
		return fmt.Errorf("acquiring upgrade barrier lock: %w", err)
	}
	defer func() {
		if err := filelock.Unlock(b.lockFile.Fd()); err != nil {
			ginkgo.GinkgoLogr.Error(err, "failed to release upgrade barrier lock; other specs may hang waiting for flock")
		}
	}()

	state, err := b.readState()
	if err != nil {
		return err
	}

	dirty, err := fn(state)
	if err != nil {
		return err
	}
	if !dirty {
		return nil
	}

	return b.writeState(state)
}

// readState reads the current barrier state from disk without holding the
// flock. Safe for poll loops where an eventually-consistent snapshot is
// sufficient; the atomic rename in writeState ensures readers always see a
// complete file, never a partially-written one.
func (b *UpgradeBarrier) readState() (*upgradeBarrierState, error) {
	f, err := os.OpenFile(b.statePath, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return nil, fmt.Errorf("opening upgrade barrier state file %s: %w", b.statePath, err)
	}
	defer f.Close()

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seeking upgrade barrier state file: %w", err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("reading upgrade barrier state file: %w", err)
	}

	var state upgradeBarrierState
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &state); err != nil {
			return nil, fmt.Errorf("unmarshalling upgrade barrier state: %w", err)
		}
	}
	return &state, nil
}

// writeState marshals state and atomically replaces the state file via a
// temp-file rename. Must be called with the flock acquired (i.e. from within
// withLock) to serialise concurrent writers across processes.
func (b *UpgradeBarrier) writeState(state *upgradeBarrierState) error {
	data, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshalling upgrade barrier state: %w", err)
	}

	dir := filepath.Dir(b.statePath)
	tmp, err := os.CreateTemp(dir, "upgrade-barrier-state-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp upgrade barrier state file: %w", err)
	}

	cleanup := func() {
		if err := os.Remove(tmp.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = err // best-effort cleanup
		}
	}

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("writing temp upgrade barrier state file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("syncing temp upgrade barrier state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("closing temp upgrade barrier state file: %w", err)
	}
	if err := os.Rename(tmp.Name(), b.statePath); err != nil {
		cleanup()
		return fmt.Errorf("replacing upgrade barrier state file: %w", err)
	}

	return nil
}
