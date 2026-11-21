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
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/onsi/ginkgo/v2"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/test/pkg/filelock"
)

// Cluster-creation start pacing. Opt-in: off unless
// ClusterCreatePacingEnvvar is set to a "<max>/<window>" sizing.
//
// It spaces out cluster-creation starts across a run's spec subprocesses so
// their ARM-heavy phases do not arrive as one wave that saturates
// subscription-level ARM quotas (the ARO-29937 failure mode). It only helps
// when the creation burst itself saturates the quota; where throttling tracks
// steady-state load instead, spreading starts moves load around without
// removing it.
//
// The pacer fails open: unreadable state, an unavailable lock, or a wait
// exceeding maxClusterCreatePacingWait proceeds unpaced. Only context
// cancellation while waiting aborts the creation.

const (
	// ClusterCreatePacingEnvvar opts a run in to cluster-creation start
	// pacing: "<max>/<window>", e.g. "12/6m" allows at most 12 starts within
	// any trailing 6-minute window, shared by every spec subprocess through
	// ARTIFACT_DIR. "0", "off" or "disabled" turns pacing off.
	ClusterCreatePacingEnvvar = "ARO_HCP_E2E_CLUSTER_CREATE_PACING"

	// RecommendedClusterCreatePacing is the sizing for a suite that opts in
	// without measuring its own: an initial burst of 12 starts, then one per
	// 30s. It is a value for callers to pass in, never a fallback.
	RecommendedClusterCreatePacing = "12/6m"

	// clusterCreatePacingStateFile is shared by every spec subprocess of a run
	// through ARTIFACT_DIR; without ARTIFACT_DIR pacing stays off.
	clusterCreatePacingStateFile = "cluster-create-pacing-state.yaml"

	// clusterCreatePacingLockFile guards the state file and sits beside it, so
	// the lock is scoped per run like the state is.
	clusterCreatePacingLockFile = "cluster-create-pacing.lock"

	// maxClusterCreatePacingWait bounds one creation's total wait for window
	// capacity; at the bound it proceeds unpaced.
	maxClusterCreatePacingWait = 30 * time.Minute

	// clusterCreatePacingPollInterval caps how often a waiting creation
	// re-checks the window.
	clusterCreatePacingPollInterval = 15 * time.Second

	// clusterCreatePacingLockRetryInterval is the retry cadence while a pacer
	// lock is contended.
	clusterCreatePacingLockRetryInterval = 250 * time.Millisecond
)

// errPacingLockUnavailable reports that an admission did not complete within
// the pacing deadline; pace fails open on it.
var errPacingLockUnavailable = errors.New("cluster-creation pacing lock not acquired within the pacing wait bound")

// clusterCreatePacing is a sliding-window limit on cluster-creation starts.
type clusterCreatePacing struct {
	max    int
	window time.Duration
}

func (p clusterCreatePacing) enabled() bool {
	return p.max > 0 && p.window > 0
}

// parseClusterCreatePacing parses "<max>/<window>" (e.g. "12/6m"). Empty,
// "0", "off" and "disabled" all disable pacing: it is opt-in.
func parseClusterCreatePacing(value string) (clusterCreatePacing, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "off") || strings.EqualFold(value, "disabled") || value == "0" {
		return clusterCreatePacing{}, nil
	}
	count, window, found := strings.Cut(value, "/")
	if !found {
		return clusterCreatePacing{}, fmt.Errorf("expected \"<max>/<window>\" like %q, got %q", RecommendedClusterCreatePacing, value)
	}
	max, err := strconv.Atoi(strings.TrimSpace(count))
	if err != nil || max < 1 {
		return clusterCreatePacing{}, fmt.Errorf("pacing max %q: want a positive integer", count)
	}
	duration, err := time.ParseDuration(strings.TrimSpace(window))
	if err != nil || duration <= 0 {
		return clusterCreatePacing{}, fmt.Errorf("pacing window %q: want a positive duration like 6m", window)
	}
	return clusterCreatePacing{max: max, window: duration}, nil
}

// clusterCreatePacingState is the on-disk sliding window, plus the smoothing
// gate described on NextAdmission.
type clusterCreatePacingState struct {
	Starts []string `yaml:"starts,omitempty"`
	// NextAdmission is the earliest admissible start once the window has
	// filled. Without it, capacity freed as the burst ages out would be
	// refilled as another batch instead of one start at a time.
	NextAdmission string `yaml:"nextAdmission,omitempty"`
}

// clusterCreatePacer records creation starts in a shared state file and waits
// until the trailing window has capacity. now and sleep are injectable for
// tests.
type clusterCreatePacer struct {
	cfg       clusterCreatePacing
	statePath string
	lockPath  string
	now       func() time.Time
	sleep     func(ctx context.Context, d time.Duration) error
}

// clusterCreatePacingMu serializes pacer state access within one process. It is
// only ever try-locked (see lockState), so a wedged holder cannot stall the
// process.
var clusterCreatePacingMu sync.Mutex

// pacedClusterCreates memoizes admissions per cluster so the FromParam →
// AndWait hook chain paces each cluster once per process. Entries are never
// expired: a spec that deleted and recreated a cluster under the same names
// would begin the second creation unpaced.
var pacedClusterCreates sync.Map

// clusterCreatePacingKey keys the memo by resource group and name; the
// subscription is fixed per spec subprocess.
func clusterCreatePacingKey(resourceGroupName, clusterName string) string {
	return resourceGroupName + "/" + clusterName
}

// clusterCreatePaceCall is one admission through the memo.
type clusterCreatePaceCall struct {
	done chan struct{}
	err  error
}

// paceClusterCreateOnce runs admit for key at most once at a time: concurrent
// callers wait for the in-flight result, a success memoizes, and a failed
// admission is removed so a retry paces again.
func paceClusterCreateOnce(ctx context.Context, key string, admit func(context.Context) error) error {
	call := &clusterCreatePaceCall{done: make(chan struct{})}
	if existing, alreadyPaced := pacedClusterCreates.LoadOrStore(key, call); alreadyPaced {
		existing := existing.(*clusterCreatePaceCall)
		// A completed admission wins over a cancelled caller context.
		select {
		case <-existing.done:
			return existing.err
		default:
		}
		select {
		case <-existing.done:
			return existing.err
		case <-ctx.Done():
			// This caller stops waiting only; the in-flight admission and its
			// memo entry are unaffected.
			return ctx.Err()
		}
	}
	call.err = admit(ctx)
	if call.err != nil {
		// Drop a failed admission so the next pace for this key re-runs; do it
		// before closing done so a retry cannot re-store this entry.
		pacedClusterCreates.CompareAndDelete(key, call)
	}
	close(call.done)
	return call.err
}

// newClusterCreatePacer builds a pacer scoped to this run. Callers only reach
// it with a non-empty ARTIFACT_DIR — paceHCPClusterCreateStart checks first.
func newClusterCreatePacer(cfg clusterCreatePacing) *clusterCreatePacer {
	return &clusterCreatePacer{
		cfg:       cfg,
		statePath: filepath.Join(artifactDir(), clusterCreatePacingStateFile),
		lockPath:  filepath.Join(artifactDir(), clusterCreatePacingLockFile),
		now:       time.Now,
		sleep:     sleepContext,
	}
}

// paceHCPClusterCreateStart waits until a new HCP cluster creation is
// admissible, then records the start. It must run before a per-call creation
// timeout is armed, so the wait consumes the spec's budget rather than
// ClusterCreationTimeout.
func paceHCPClusterCreateStart(ctx context.Context, logger logr.Logger, resourceGroupName, clusterName string) error {
	cfg, err := parseClusterCreatePacing(os.Getenv(ClusterCreatePacingEnvvar))
	if err != nil {
		// Fail open: a malformed value disables pacing rather than failing tests.
		logger.Info("WARN: invalid cluster-creation pacing setting, pacing disabled",
			"envvar", ClusterCreatePacingEnvvar,
			"value", os.Getenv(ClusterCreatePacingEnvvar),
			"error", err)
		return nil
	}
	if !cfg.enabled() {
		return nil
	}
	// Without ARTIFACT_DIR there is no shared state to coordinate through.
	if artifactDir() == "" {
		return nil
	}
	key := clusterCreatePacingKey(resourceGroupName, clusterName)
	return paceClusterCreateOnce(ctx, key, func(ctx context.Context) error {
		return newClusterCreatePacer(cfg).pace(ctx, logger, clusterName)
	})
}

// PaceHCPClusterCreateStart is for callers that begin a creation through the
// asynchronous BeginCreateHCPCluster... helpers and arm their own provisioning
// timeout: call it before arming that timeout; the later pace inside the
// helper is then a memoized no-op.
func PaceHCPClusterCreateStart(ctx context.Context, logger logr.Logger, resourceGroupName, clusterName string) error {
	return paceHCPClusterCreateStart(ctx, logger, resourceGroupName, clusterName)
}

// pace blocks until a new cluster-creation start fits in the sliding window.
// It fails open on state-file errors and once the wait would exceed
// maxClusterCreatePacingWait; only context cancellation aborts the creation.
func (p *clusterCreatePacer) pace(ctx context.Context, logger logr.Logger, clusterName string) error {
	start := p.now()
	deadline := start.Add(maxClusterCreatePacingWait)
	waited := false
	for {
		// Checked before the admission attempt: a success must not bypass the
		// fail-open bound.
		if p.now().After(deadline) {
			logger.Info("WARN: cluster-creation pacing wait exceeded, proceeding unpaced",
				"clusterName", clusterName,
				"maxWait", maxClusterCreatePacingWait.String(),
				"max", p.cfg.max,
				"window", p.cfg.window.String())
			return nil
		}
		retryAfter, err := p.tryRecordStart(ctx, deadline)
		if err != nil {
			// Cancellation must abort the creation; every other lock or state
			// failure is fail-open (best-effort pacing, never a gate).
			if ctx.Err() != nil {
				return fmt.Errorf("aborted while waiting for cluster-creation pacing (cluster %s): %w", clusterName, err)
			}
			logger.Info("WARN: cluster-creation pacing state unavailable, proceeding unpaced",
				"clusterName", clusterName,
				"error", err)
			return nil
		}
		if retryAfter == 0 {
			if waited {
				logger.Info("paced HCP cluster creation start",
					"clusterName", clusterName,
					"waited", p.now().Sub(start).Round(time.Second).String(),
					"max", p.cfg.max,
					"window", p.cfg.window.String(),
					"reason", "spreading synchronized cluster creations that saturate ARM subscription quotas (ARO-29937)")
			}
			return nil
		}
		waited = true
		// Cap the sleep by the remaining budget: crossing the deadline would
		// let a cancellation abort a creation that should proceed unpaced.
		if remaining := deadline.Sub(p.now()); remaining > 0 {
			if err := p.sleep(ctx, min(retryAfter, clusterCreatePacingPollInterval, remaining)); err != nil {
				return fmt.Errorf("aborted while waiting for cluster-creation pacing (cluster %s): %w", clusterName, err)
			}
		} else {
			// No budget left: fail open rather than retry at the deadline.
			logger.Info("WARN: cluster-creation pacing wait exceeded, proceeding unpaced",
				"clusterName", clusterName,
				"maxWait", maxClusterCreatePacingWait.String(),
				"max", p.cfg.max,
				"window", p.cfg.window.String())
			return nil
		}
	}
}

// lockState takes the in-process mutex, then the cross-process flock, both
// non-blockingly and with retries bounded by deadline, so a stalled holder
// cannot outlast the pacing deadline or swallow cancellation. It returns a
// release callback, ctx.Err() on cancellation, or errPacingLockUnavailable.
func (p *clusterCreatePacer) lockState(ctx context.Context, deadline time.Time) (func(), error) {
	lockFile, err := os.OpenFile(p.lockPath, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return nil, fmt.Errorf("failed to open cluster-creation pacing lock file: %w", err)
	}
	for {
		if ctx.Err() != nil {
			_ = lockFile.Close()
			return nil, ctx.Err()
		}
		if p.now().After(deadline) {
			_ = lockFile.Close()
			return nil, errPacingLockUnavailable
		}

		if clusterCreatePacingMu.TryLock() {
			if lockErr := filelock.TryLock(lockFile.Fd()); lockErr == nil {
				return func() {
					if err := filelock.Unlock(lockFile.Fd()); err != nil {
						ginkgo.GinkgoLogr.Info("WARN: failed to release cluster-creation pacing lock", "error", err)
					}
					if err := lockFile.Close(); err != nil {
						ginkgo.GinkgoLogr.Info("WARN: failed to close cluster-creation pacing lock file", "error", err)
					}
					clusterCreatePacingMu.Unlock()
				}, nil
			} else if !errors.Is(lockErr, filelock.ErrLockBusy) {
				clusterCreatePacingMu.Unlock()
				_ = lockFile.Close()
				return nil, fmt.Errorf("failed to acquire cluster-creation pacing lock: %w", lockErr)
			} else {
				// Give the mutex back while waiting on the file lock, so
				// cross-process contention does not starve this process.
				clusterCreatePacingMu.Unlock()
			}
		}

		// Cap the retry sleep by the remaining budget; with no budget left,
		// fail open like the deadline check above.
		if remaining := deadline.Sub(p.now()); remaining > 0 {
			if err := p.sleep(ctx, min(clusterCreatePacingLockRetryInterval, remaining)); err != nil {
				_ = lockFile.Close()
				return nil, err
			}
		} else {
			_ = lockFile.Close()
			return nil, errPacingLockUnavailable
		}
	}
}

// tryRecordStart records a start timestamp if the window has capacity,
// returning how long to wait before retrying (zero when recorded). An error
// means fail open, unless the context was cancelled.
func (p *clusterCreatePacer) tryRecordStart(ctx context.Context, deadline time.Time) (time.Duration, error) {
	release, err := p.lockState(ctx, deadline)
	if err != nil {
		return 0, err
	}
	defer release()

	// Recheck the deadline on the read an admission is recorded with: the lock
	// acquisition can straddle the bound, and capacity that ages out during it
	// must not be persisted past the bound.
	now := p.now()
	if now.After(deadline) {
		return 0, errPacingLockUnavailable
	}

	state, err := p.readState()
	if err != nil {
		return 0, err
	}

	// A malformed gate value is dropped rather than carried forward, so it
	// re-arms predictably when the window next fills; the cap below still holds.
	nextAdmission := time.Time{}
	if state.NextAdmission != "" {
		if t, err := time.Parse(time.RFC3339, state.NextAdmission); err == nil {
			nextAdmission = t
		} else {
			state.NextAdmission = ""
		}
	}

	// Keep only starts inside the trailing window.
	recent := state.Starts[:0]
	for _, ts := range state.Starts {
		t, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			return 0, fmt.Errorf("failed to parse recorded start %q: %w", ts, err)
		}
		if now.Sub(t) < p.cfg.window {
			recent = append(recent, ts)
		}
	}

	if len(recent) < p.cfg.max && (nextAdmission.IsZero() || !now.Before(nextAdmission)) {
		// RFC3339Nano: a second-truncated timestamp would look aged out of a
		// sub-second window the instant it was written.
		recent = append(recent, now.UTC().Format(time.RFC3339Nano))
		// Arm (or advance) the smoothing gate once the window has filled; the
		// initial burst stays unpaced, everything after it is spaced.
		next := state.NextAdmission
		if !nextAdmission.IsZero() || len(recent) >= p.cfg.max {
			next = now.Add(p.cfg.window / time.Duration(p.cfg.max)).UTC().Format(time.RFC3339Nano)
		}
		if err := p.writeState(clusterCreatePacingState{Starts: recent, NextAdmission: next}); err != nil {
			return 0, err
		}
		return 0, nil
	}

	// Not admissible yet: retry once the oldest start ages out and, once
	// armed, the smoothing gate has passed.
	retryAfter := time.Duration(0)
	if len(recent) >= p.cfg.max {
		oldest, err := time.Parse(time.RFC3339, recent[0])
		if err != nil {
			return 0, fmt.Errorf("failed to parse recorded start %q: %w", recent[0], err)
		}
		retryAfter = p.cfg.window - now.Sub(oldest)
	}
	if now.Before(nextAdmission) {
		if wait := nextAdmission.Sub(now); wait > retryAfter {
			retryAfter = wait
		}
	}
	return retryAfter, nil
}

func (p *clusterCreatePacer) readState() (clusterCreatePacingState, error) {
	data, err := os.ReadFile(p.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return clusterCreatePacingState{}, nil
	}
	if err != nil {
		return clusterCreatePacingState{}, fmt.Errorf("failed to read cluster-creation pacing state file %s: %w", p.statePath, err)
	}
	var state clusterCreatePacingState
	if err := yaml.Unmarshal(data, &state); err != nil {
		return clusterCreatePacingState{}, fmt.Errorf("failed to unmarshal cluster-creation pacing state file %s: %w", p.statePath, err)
	}
	return state, nil
}

func (p *clusterCreatePacer) writeState(state clusterCreatePacingState) error {
	updated, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal cluster-creation pacing state: %w", err)
	}
	dir := filepath.Dir(p.statePath)
	tmp, err := os.CreateTemp(dir, "cluster-create-pacing-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temporary cluster-creation pacing state file: %w", err)
	}
	cleanupTemp := func() {
		if err := os.Remove(tmp.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
			ginkgo.GinkgoLogr.Info("WARN: failed to remove temporary cluster-creation pacing state file", "error", err)
		}
	}
	if _, err := tmp.Write(updated); err != nil {
		tmp.Close()
		cleanupTemp()
		return fmt.Errorf("failed to write temporary cluster-creation pacing state file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanupTemp()
		return fmt.Errorf("failed to sync temporary cluster-creation pacing state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanupTemp()
		return fmt.Errorf("failed to close temporary cluster-creation pacing state file: %w", err)
	}
	if err := os.Rename(tmp.Name(), p.statePath); err != nil {
		cleanupTemp()
		return fmt.Errorf("failed to replace cluster-creation pacing state file: %w", err)
	}
	return nil
}

// sleepContext sleeps for d, returning early with the context error if ctx is
// cancelled first.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
