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

	"github.com/go-logr/logr"
	"github.com/onsi/ginkgo/v2"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/test/pkg/filelock"
	entrypointrun "github.com/Azure/ARO-HCP/tooling/templatize/cmd/entrypoint/run"
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

// defaultSettleTimeout is the maximum time to wait for all specs to check in
// or abort. It bounds the provisioning phase: if a spec hangs or crashes
// without signalling abort, survivors unblock after this deadline rather than
// burning the full spec timeout.
const defaultSettleTimeout = 45 * time.Minute

// defaultUpgradeTimeout is the maximum time a waiter will wait for the elected
// runner to complete "make entrypoint/Region" and call markUpgradeDone. If the
// runner crashes after check-in (OOM, hard kill) without signalling, waiters
// fail with a clear timeout error rather than hanging until Prow kills the job.
const defaultUpgradeTimeout = 70 * time.Minute

// upgradeBarrierState is the on-disk representation of the barrier.
type upgradeBarrierState struct {
	// CheckedIn counts specs that successfully provisioned their cluster and
	// baseline and are ready for the upgrade.
	CheckedIn int `yaml:"checked_in"`
	// AbortedCount counts specs that called abort() because they failed to
	// provision or capture their baseline. These specs are no longer
	// participating; survivors proceed once the barrier settles.
	AbortedCount int `yaml:"aborted_count"`
	// RunnerPID holds the OS PID of the spec elected to run
	// the Region entrypoint pipeline. It is set atomically by the first spec
	// to call checkIn. A value of 0 means no runner has been elected yet.
	RunnerPID int `yaml:"runner_pid"`
	// RunID holds the parent PID (os.Getppid()) of the worker processes in this
	// suite invocation. All parallel workers spawned by the same runner share the
	// same parent PID; a new suite invocation gets a different parent PID.
	// Any mismatch means the state file is stale and must be reset.
	RunID int `yaml:"run_id"`
	// UpgradeDone is set to true by markUpgradeDone once the runner has
	// finished (successfully or not).
	UpgradeDone bool `yaml:"upgrade_done"`
	// UpgradeError is the error message written by markUpgradeDone when the
	// upgrade failed. Empty string means success.
	UpgradeError string `yaml:"upgrade_error,omitempty"`
}

// settled reports whether every participating spec has either checked in or
// aborted, meaning the barrier can make a routing decision.
func (s *upgradeBarrierState) settled(total int) bool {
	return s.CheckedIn+s.AbortedCount >= total
}

// UpgradeBarrier coordinates a set of parallel UpgradeInPlace specs so that
// all specs finish provisioning their clusters before a single elected spec
// runs "make entrypoint/Region", after which every spec validates its own
// cluster independently.
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
//	err = barrier.CheckInAndUpgrade(ctx)
//	Expect(err).NotTo(HaveOccurred(), "upgrade phase failed")
//
//	// ... per-spec validation ...
type UpgradeBarrier struct {
	statePath      string
	lockFile       *os.File
	total          int
	runID          int // os.Getppid() of this invocation; all workers in the same suite run share the same parent PID, so only a truly new invocation (different parent) resets the state file
	pollInterval   time.Duration
	settleTimeout  time.Duration
	upgradeTimeout time.Duration

	// checkedIn is set by checkIn (Phase 2) and read by the abort DeferCleanup
	// registered in registerGinkgoCleanup to decide whether to signal abort.
	checkedIn bool

	// upgradeDoneSignaled is set to true once markUpgradeDone has been called
	// successfully. The DeferCleanup safety net checks this to avoid a double
	// signal if CheckInAndUpgrade already called markUpgradeDone eagerly.
	upgradeDoneSignaled bool
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

	dir := artifactDir()
	if dir == "" {
		dir = os.TempDir()
	}

	lockPath := filepath.Join(os.TempDir(), "upgrade-barrier.lock")
	lf, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return nil, fmt.Errorf("opening upgrade barrier lock file %s: %w", lockPath, err)
	}

	statePath := filepath.Join(dir, "upgrade-barrier-state.yaml")

	b := &UpgradeBarrier{
		statePath:      statePath,
		lockFile:       lf,
		total:          total,
		runID:          os.Getppid(),
		pollInterval:   defaultUpgradeBarrierPollInterval,
		settleTimeout:  defaultSettleTimeout,
		upgradeTimeout: defaultUpgradeTimeout,
	}

	// Write a clean initial state unless a sibling spec in the same suite
	// invocation already did so (RunID matches). Any mismatch — zero RunID
	// (uninitialized file), different parent PID (new suite invocation), or
	// UpgradeDone=true (completed run) — means the file is stale and must be
	// reset before coordination begins.
	if err := b.withLock(func(state *upgradeBarrierState) (bool, error) {
		if state.RunID == b.runID {
			return false, nil // same run, sibling already initialised — leave it
		}
		if state.RunID != 0 {
			ginkgo.GinkgoLogr.Info("upgrade barrier: resetting stale state file",
				"previous_run_id", state.RunID,
				"previous_upgrade_done", state.UpgradeDone,
				"previous_checked_in", state.CheckedIn,
				"previous_runner_pid", state.RunnerPID)
		}
		*state = upgradeBarrierState{RunID: b.runID}
		return true, nil
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

// CheckInAndUpgrade encapsulates the full barrier synchronisation and upgrade
// phase in a single call, hiding the runner/waiter split from the test body.
//
// After all specs have provisioned their clusters (barrier settled), the
// elected runner invokes the Region entrypoint pipeline via templatize's
// run.RunPipeline. On completion (success or failure) it signals all waiting
// specs immediately so they can start their validation in parallel with the
// runner.
//
// CheckInAndUpgrade returns an error if check-in fails (all specs aborted), if
// the pipeline invocation fails (runner only), or if the runner reported a
// failure (non-runner specs). Per-spec validation runs after this call returns.
func (b *UpgradeBarrier) CheckInAndUpgrade(ctx context.Context) error {
	isRunner, err := b.checkIn(ctx)
	if err != nil {
		return err
	}
	if !isRunner {
		return b.waitForUpgrade(ctx)
	}

	// Safety net: if RunPipeline panics, or an Expect fires inside a helper
	// we call before we reach the eager markUpgradeDone below, the
	// DeferCleanup ensures waiting specs are unblocked rather than hanging
	// until suite timeout.
	ginkgo.DeferCleanup(func(ctx context.Context) {
		if b.upgradeDoneSignaled {
			return
		}
		if markErr := b.markUpgradeDone(errors.New("runner did not signal upgrade done (panicked or exited early)")); markErr != nil {
			ginkgo.GinkgoLogr.Error(markErr, "failed to mark upgrade done in barrier safety net")
		}
	}, AnnotatedLocation("upgrade barrier: mark upgrade done (runner safety net)"))

	upgradeErr := b.runRegionEntrypoint(ctx)

	// Signal immediately after the pipeline completes — success or failure —
	// so all waiting specs can start their validation window in parallel with
	// the runner rather than being held until the runner's cleanup phase.
	b.upgradeDoneSignaled = true
	if markErr := b.markUpgradeDone(upgradeErr); markErr != nil {
		ginkgo.GinkgoLogr.Error(markErr, "failed to mark upgrade done in barrier")
	}
	return upgradeErr
}

// runRegionEntrypoint invokes the Region entrypoint pipeline in-process via
// templatize's run.RunPipeline.
//
// Configuration is driven by the same environment variables that the CI step
// registry shell script (aro-hcp-test-local-upgrade-commands.sh) exports:
//
//   - DEPLOY_ENV          → dev environment name (e.g. "cspr")
//   - OVERRIDE_CONFIG_FILE → config overlay with PR image overrides
//
// Paths are resolved as absolutes from the repository root so the call works
// regardless of the test binary's working directory.
func (b *UpgradeBarrier) runRegionEntrypoint(ctx context.Context) error {
	root, err := repoRoot()
	if err != nil {
		return fmt.Errorf("determining repo root for entrypoint invocation: %w", err)
	}

	opts := entrypointrun.DefaultOptions()
	opts.BaseOptions.ConfigFile = filepath.Join(root, "config", "config.yaml")
	if override := overrideConfigFile(); override != "" {
		opts.BaseOptions.ConfigFileOverride = override
	}
	opts.TopologyFiles = []string{filepath.Join(root, "topology.yaml")}
	opts.Entrypoint = "Microsoft.Azure.ARO.HCP.Region"
	opts.DevSettingsFile = filepath.Join(root, "tooling", "templatize", "settings.yaml")
	opts.DevEnvironment = deployEnv()
	opts.Region = location()
	opts.StepCacheDir = filepath.Join(root, ".step-cache")
	opts.Persist = true

	if dir := artifactDir(); dir != "" {
		opts.JUnitOutputFile = filepath.Join(dir, "junit_entrypoint.xml")
		opts.ConfigOutputFile = filepath.Join(dir, "config.yaml")
	}
	opts.TimingOutputFile = filepath.Join(root, "timing", "steps.yaml")

	ctx = logr.NewContext(ctx, ginkgo.GinkgoLogr)
	return entrypointrun.RunPipeline(ctx, opts)
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

// deployEnv returns the DEPLOY_ENV environment variable, which selects the
// templatize dev-environment (e.g. "cspr", "pers").
func deployEnv() string {
	return os.Getenv("DEPLOY_ENV")
}

// overrideConfigFile returns the OVERRIDE_CONFIG_FILE environment variable.
// In CI the local-upgrade step sets this to a rendered config overlay that
// substitutes PR-built image tags on top of the base config.yaml.
func overrideConfigFile() string {
	return os.Getenv("OVERRIDE_CONFIG_FILE")
}

// registerGinkgoCleanup registers DeferCleanup handlers on the current Ginkgo
// node for all barrier-owned resources:
//
//   - abort: fires if the spec fails before CheckInAndUpgrade so surviving
//     specs are not blocked waiting for a participant that will never arrive.
//   - lock file close: releases the OS file descriptor held since construction.
//
// The markUpgradeDone DeferCleanup is registered separately inside
// CheckInAndUpgrade, only for the elected runner, immediately before the
// upgrade runs.
func (b *UpgradeBarrier) registerGinkgoCleanup() {
	// The lock file must outlive the abort and markUpgradeDone cleanups,
	// both of which acquire the flock.
	ginkgo.DeferCleanup(func() {
		_ = b.lockFile.Close()
	}, AnnotatedLocation("upgrade barrier: close lock file"))

	// Registered second → runs before the lock file is closed.
	ginkgo.DeferCleanup(func(ctx context.Context) {
		if b.checkedIn {
			return
		}
		_ = b.abort(ctx)
	}, AnnotatedLocation("upgrade barrier: abort if not checked in"))
}

// checkIn atomically increments checked_in and, if runner_pid is not yet set,
// claims the runner role for the calling process. It then polls until the
// barrier settles (checked_in+aborted_count >= total), ensuring no spec
// proceeds to the upgrade phase while others are still provisioning.
//
// Note: because Phase 1 always increments checked_in before Phase 2 polls the
// state file, the barrier can never appear fully-aborted (checked_in==0) from
// within this function — the caller has already contributed at least one
// checked_in count.
func (b *UpgradeBarrier) checkIn(ctx context.Context) (isRunner bool, err error) {
	// Phase 1: atomically increment checked_in and optionally claim runner.
	if err := b.withLock(func(state *upgradeBarrierState) (bool, error) {
		state.CheckedIn++
		if state.RunnerPID == 0 {
			state.RunnerPID = os.Getpid()
			isRunner = true
		}
		return true, nil
	}); err != nil {
		return false, fmt.Errorf("check-in: %w", err)
	}

	// Set immediately after Phase 1: if Phase 2 fails, the abort DeferCleanup
	// must not also increment aborted_count — checked_in is already written.
	b.checkedIn = true

	// Phase 2: poll until all specs have either checked in or aborted.
	if err := b.waitSettled(ctx); err != nil {
		return false, err
	}

	return isRunner, nil
}

// abort signals that this spec is no longer participating because it failed to
// provision its cluster or capture its baseline. It increments aborted_count
// so that other specs waiting for the barrier to settle can proceed without
// this spec.
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

// waitForUpgrade polls the state file until the runner has marked the upgrade
// done. It returns the upgrade_error written by the runner, if any.
//
// An inner deadline of b.upgradeTimeout is applied on top of ctx so that
// waiters fail with a clear error if the runner crashes after check-in without
// calling markUpgradeDone, rather than hanging until Prow kills the job.
func (b *UpgradeBarrier) waitForUpgrade(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, b.upgradeTimeout)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for upgrade to complete: %w", ctx.Err())
		case <-time.After(b.pollInterval):
		}

		done, upgradeErr, err := b.readUpgradeDone()
		if err != nil {
			return fmt.Errorf("reading upgrade barrier state: %w", err)
		}
		if done {
			if upgradeErr != "" {
				return fmt.Errorf("upgrade runner reported failure: %s", upgradeErr)
			}
			return nil
		}
	}
}

// markUpgradeDone is called by the runner after the upgrade has finished (or
// failed). It writes upgrade_done=true and, when upgradeErr is non-nil, the
// error message so that waitForUpgrade can surface it to non-runner specs.
//
// markUpgradeDone is idempotent: subsequent calls when upgrade_done is already
// true are no-ops, making it safe to call from both the happy path and a
// DeferCleanup handler.
func (b *UpgradeBarrier) markUpgradeDone(upgradeErr error) error {
	return b.withLock(func(state *upgradeBarrierState) (bool, error) {
		if state.UpgradeDone {
			return false, nil // already marked; no-op
		}
		state.UpgradeDone = true
		if upgradeErr != nil {
			state.UpgradeError = upgradeErr.Error()
		}
		return true, nil
	})
}

// waitSettled polls until checked_in+aborted_count >= total (all registered
// specs have either checked in or aborted).
//
// An inner deadline of b.settleTimeout is applied on top of ctx so that a
// spec that crashes during provisioning without signalling abort does not
// block survivors for the full spec timeout.
func (b *UpgradeBarrier) waitSettled(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, b.settleTimeout)
	defer cancel()

	for {
		settled, err := b.readSettled()
		if err != nil {
			return fmt.Errorf("reading upgrade barrier state: %w", err)
		}
		if settled {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for all specs to check in or abort: %w", ctx.Err())
		case <-time.After(b.pollInterval):
		}
	}
}

// readSettled reads the state file without holding the lock (eventually
// consistent) and reports whether the barrier has settled.
func (b *UpgradeBarrier) readSettled() (settled bool, err error) {
	state, err := b.readState()
	if err != nil {
		return false, err
	}
	return state.settled(b.total), nil
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
