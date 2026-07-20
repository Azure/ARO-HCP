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
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-logr/logr"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/test/pkg/filelock"
	entrypointrun "github.com/Azure/ARO-HCP/tooling/templatize/cmd/entrypoint/run"
)

// upgradeCoordinatorLogger is a package-level fallback used by the coordinator
// when no logger is injected. It writes to stderr via the logr/stdr adapter.
// Using a package-level default avoids a hard dependency on any particular logging
// library while still giving operators a way to see coordinator progress in the
// Prow job log.
var upgradeCoordinatorLogger = logr.Discard()

// defaultSettleTimeout is the maximum time to wait for all specs to check in
// or abort. It bounds the provisioning phase: if a spec hangs or crashes
// without signalling abort, survivors unblock after this deadline rather than
// burning the full spec timeout.
const defaultSettleTimeout = 45 * time.Minute

// defaultUpgradeRunTimeout is the maximum time the coordinator allows
// runRegionEntrypoint (the actual Region entrypoint pipeline) to run. When this
// deadline expires the coordinator cancels the pipeline context, writes an error
// to the state file, and unblocks waiting specs. Kept shorter than
// defaultUpgradeTimeout in upgrade_barrier.go so specs always see the
// coordinator's error rather than timing out themselves first.
const defaultUpgradeRunTimeout = 50 * time.Minute

// SetUpgradeCoordinatorLogger sets the logger used by all UpgradeCoordinator
// instances created after this call. It should be called once from main before
// any coordinator is created. The coordinator runs in the parent run-suite
// process (not inside a Ginkgo spec), so ginkgo.GinkgoLogr must not be used
// here — that logger is only valid inside running specs and its output would be
// attached to a spec's captured buffer, not the parent process's stderr.
func SetUpgradeCoordinatorLogger(l logr.Logger) {
	upgradeCoordinatorLogger = l
}

// UpgradeCoordinator runs the Region entrypoint pipeline from the long-lived
// parent run-suite process. Because it runs in the parent rather than in a
// per-spec worker subprocess, the upgrade is not affected by the lifecycle of
// any individual spec — it continues even if the runner spec fails or its
// process is replaced.
//
// Usage from main.go (inside AddBeforeAll, guarded to parent process only):
//
//	coord, err := framework.NewUpgradeCoordinator()
//	if err != nil { ... }
//	go coord.Run(context.Background())
type UpgradeCoordinator struct {
	statePath         string
	lockFile          *os.File
	total             int
	runID             int // os.Getpid() of the parent run-suite process
	pollInterval      time.Duration
	settleTimeout     time.Duration
	upgradeRunTimeout time.Duration
	logger            logr.Logger
}

// NewUpgradeCoordinator creates an UpgradeCoordinator backed by the same state
// file path convention as UpgradeBarrier so they share state across processes.
//
// It must be called from the parent run-suite process only (guarded by
// isRunSuiteProcess in main.go). The total number of participating specs is
// read from upgradeInPlaceSpecCount set by SetUpgradeInPlaceSpecCount.
func NewUpgradeCoordinator() (*UpgradeCoordinator, error) {
	total := upgradeInPlaceSpecCount
	if total <= 0 {
		return nil, fmt.Errorf("NewUpgradeCoordinator: spec count not set; " +
			"call framework.SetUpgradeInPlaceSpecCount from main before running tests")
	}

	lockPath := upgradeLockPath()
	lf, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return nil, fmt.Errorf("opening upgrade coordinator lock file %s: %w", lockPath, err)
	}

	c := &UpgradeCoordinator{
		statePath:         upgradeStatePath(),
		lockFile:          lf,
		total:             total,
		runID:             os.Getpid(), // parent PID; workers use os.Getppid() which equals this
		pollInterval:      defaultUpgradeBarrierPollInterval,
		settleTimeout:     defaultSettleTimeout,
		upgradeRunTimeout: defaultUpgradeRunTimeout,
		logger:            upgradeCoordinatorLogger,
	}

	// Initialise the state file synchronously so it is ready before BeforeAll
	// returns and before any worker subprocess calls NewUpgradeBarrier. Workers
	// will error if they see a mismatched RunID, so this must happen first.
	if err := c.initState(); err != nil {
		lf.Close()
		return nil, fmt.Errorf("initialising upgrade coordinator state: %w", err)
	}

	return c, nil
}

// Run waits for all specs to check in, runs the Region entrypoint pipeline,
// then writes the result to the state file so that waiting specs can unblock.
// The state file is already initialised by NewUpgradeCoordinator.
//
// If all specs abort before checking in Run returns early without running the
// pipeline — there are no waiters, and the parent run-suite process is about
// to exit anyway.
//
// Run is intended to be called in a goroutine (go coord.Run(ctx)) from BeforeAll.
// It returns nil on success, or the first non-nil error encountered.
func (c *UpgradeCoordinator) Run(ctx context.Context) error {
	defer func() { _ = c.lockFile.Close() }()

	// Validate required env vars before blocking on settlement so failures are
	// surfaced immediately rather than after all specs have provisioned clusters.
	if deployEnv() == "" {
		return fmt.Errorf("upgrade coordinator: DEPLOY_ENV must be set for the Region entrypoint pipeline")
	}

	if err := c.waitSettled(ctx); err != nil {
		return fmt.Errorf("upgrade coordinator: waiting for specs to check in: %w", err)
	}

	// If every spec aborted without checking in there are no waiters; skip the
	// pipeline entirely and return. The parent run-suite process will exit soon.
	state, err := c.readState()
	if err != nil {
		return fmt.Errorf("upgrade coordinator: reading post-settle state: %w", err)
	}
	if state.CheckedIn == 0 {
		return fmt.Errorf("upgrade coordinator: all specs aborted before check-in; skipping upgrade")
	}

	upgradeCtx, upgradeCancel := context.WithTimeout(ctx, c.upgradeRunTimeout)
	defer upgradeCancel()

	if overrideConfigFile() == "" {
		// OVERRIDE_CONFIG_FILE is unset — the pipeline runs against base config.yaml
		// without PR image overrides. Expected for local runs, not for CI.
		c.logger.Info("upgrade coordinator: OVERRIDE_CONFIG_FILE is not set; running without config override")
	}

	upgradeErr := runRegionEntrypoint(upgradeCtx)

	if markErr := c.markUpgradeDone(upgradeErr); markErr != nil {
		return fmt.Errorf("upgrade coordinator: marking upgrade done: %w", markErr)
	}
	return upgradeErr
}

// initState writes a clean initial state (RunID = parent PID). If a state file
// from a previous run with a different RunID is found it is reset. If the file
// already has the correct RunID (another process in the same invocation
// initialised it first) it is left untouched.
func (c *UpgradeCoordinator) initState() error {
	return c.withLock(func(state *upgradeBarrierState) (bool, error) {
		if state.RunID == c.runID {
			return false, nil
		}
		if state.RunID != 0 {
			c.logger.Info("upgrade coordinator: resetting stale state file",
				"previous_run_id", state.RunID,
				"previous_upgrade_done", state.UpgradeDone,
				"previous_checked_in", state.CheckedIn)
		}
		*state = upgradeBarrierState{RunID: c.runID}
		return true, nil
	})
}

// waitSettled polls the state file until checked_in+aborted_count >= total.
func (c *UpgradeCoordinator) waitSettled(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.settleTimeout)
	defer cancel()

	for {
		state, err := c.readState()
		if err != nil {
			return fmt.Errorf("reading barrier state: %w", err)
		}
		if state.settled(c.total) {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for all specs to check in or abort: %w", ctx.Err())
		case <-time.After(c.pollInterval):
		}
	}
}

// markUpgradeDone writes UpgradeDone=true and (if non-nil) the upgrade error
// to the state file so that waiting specs can unblock.
func (c *UpgradeCoordinator) markUpgradeDone(upgradeErr error) error {
	return c.withLock(func(state *upgradeBarrierState) (bool, error) {
		if state.UpgradeDone {
			return false, nil
		}
		state.UpgradeDone = true
		if upgradeErr != nil {
			state.UpgradeError = upgradeErr.Error()
		}
		return true, nil
	})
}

// withLock acquires the exclusive flock, reads state, calls fn, and writes
// back if dirty. Mirrors the UpgradeBarrier.withLock logic.
func (c *UpgradeCoordinator) withLock(fn func(state *upgradeBarrierState) (dirty bool, err error)) error {
	if err := filelock.Lock(c.lockFile.Fd()); err != nil {
		return fmt.Errorf("acquiring upgrade coordinator lock: %w", err)
	}
	defer func() {
		if err := filelock.Unlock(c.lockFile.Fd()); err != nil {
			// Unlock errors are not actionable but should be visible
			c.logger.Error(err, "upgrade coordinator: failed to release lock; waiting specs may hang")
		}
	}()

	state, err := c.readState()
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
	return c.writeState(state)
}

// readState reads the current barrier state from disk without holding the flock.
// Returns an empty state if the file does not exist yet (coordinator starts
// before the first worker calls NewUpgradeBarrier).
func (c *UpgradeCoordinator) readState() (*upgradeBarrierState, error) {
	data, err := os.ReadFile(c.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return &upgradeBarrierState{}, nil
		}
		return nil, fmt.Errorf("reading upgrade coordinator state file: %w", err)
	}
	if len(data) == 0 {
		return &upgradeBarrierState{}, nil
	}

	var state upgradeBarrierState
	if err := yaml.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshalling upgrade coordinator state: %w", err)
	}
	return &state, nil
}

func (c *UpgradeCoordinator) writeState(state *upgradeBarrierState) error {
	// Delegate to UpgradeBarrier's atomic-rename logic by constructing a
	// temporary barrier value with only the statePath set.
	b := &UpgradeBarrier{statePath: c.statePath}
	return b.writeState(state)
}

// runRegionEntrypoint invokes the Region entrypoint pipeline in-process via
// templatize's run.RunPipeline.
//
// Configuration is driven by environment variables set by the CI step registry
// shell script (aro-hcp-test-local-upgrade-commands.sh):
//
//   - DEPLOY_ENV          → dev environment name (e.g. "cspr")
//   - OVERRIDE_CONFIG_FILE → config overlay with PR image overrides
//   - LOCATION            → Azure region
//
// Paths are resolved as absolutes from the repository root so the call works
// regardless of the working directory.
func runRegionEntrypoint(ctx context.Context) error {
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

	// The coordinator runs outside a Ginkgo spec so we use a no-op logr sink
	// unless the caller injected one via context.
	if _, err := logr.FromContext(ctx); err != nil {
		ctx = logr.NewContext(ctx, logr.Discard())
	}
	return entrypointrun.RunPipeline(ctx, opts)
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

// upgradeLockPath returns the path to the flock file shared by the
// UpgradeCoordinator (parent process) and all UpgradeBarrier instances (worker
// processes). It always lives in os.TempDir() so it is accessible to all
// processes in the same Prow pod regardless of ARTIFACT_DIR.
func upgradeLockPath() string {
	return filepath.Join(os.TempDir(), "upgrade-barrier.lock")
}

// upgradeStatePath returns the path to the YAML state file shared by the
// UpgradeCoordinator and all UpgradeBarrier instances. It is placed under
// ARTIFACT_DIR when set (so the file is collected as a CI artifact) and falls
// back to os.TempDir() for local runs.
func upgradeStatePath() string {
	dir := artifactDir()
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "upgrade-barrier-state.yaml")
}
