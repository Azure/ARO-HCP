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

// Cluster-creation start pacing.
//
// e2e-parallel specs start synchronized, and each reaches its ARM-heavy phase
// (node VMs, guest CCM load-balancer ensure) roughly ten minutes later. Enough
// clusters bringing up at once saturate the subscription-level ARM read quota,
// and the guest CCM cannot ensure the router-default load balancer until the
// storm relents (ARO-29937: 20m03s from Service creation to "Ensured load
// balancer" under throttling, and still unbound 22m23s in a heavier run).
//
// The pacer spreads that wave: before a creation starts it records a timestamp
// in a state file shared by all spec subprocesses — the file-plus-flock pattern
// of the managed identities pool — and waits until fewer than max starts were
// recorded within the trailing window. Nothing is held for a cluster's
// lifetime, so a crashed spec strands nothing. Once the window has filled, a
// persisted next-admission timestamp also spaces starts at least window/max
// apart, so freed capacity refills one start at a time instead of as another
// batch.
//
// The pacer fails open — pacing is best-effort storm mitigation, never a
// correctness gate: unreadable state, an unavailable lock, or a wait exceeding
// maxClusterCreatePacingWait proceeds unpaced. Only context cancellation while
// waiting aborts the creation.

const (
	// ClusterCreatePacingEnvvar configures cluster-creation start pacing:
	// "<max>/<window>", e.g. "12/6m" allows at most 12 starts within any
	// trailing 6-minute window, shared by every spec subprocess through
	// ARTIFACT_DIR. "0", "off" or "disabled" turns pacing off. Unset falls
	// back to defaultClusterCreatePacing.
	ClusterCreatePacingEnvvar = "ARO_HCP_E2E_CLUSTER_CREATE_PACING"

	// defaultClusterCreatePacing admits an initial burst of 12 starts, then
	// one per window/max = 30s once that burst ages out. Sized against
	// e2e-parallel's 58-container CI leases: the wave spreads over ~30 minutes,
	// and runs with fewer than 12 concurrent creations are not delayed at all.
	defaultClusterCreatePacing = "12/6m"

	// clusterCreatePacingStateFile lives in ARTIFACT_DIR, next to the managed
	// identities pool state, so every spec subprocess of a run shares it;
	// without ARTIFACT_DIR there is no shared state and pacing stays off.
	clusterCreatePacingStateFile = "cluster-create-pacing-state.yaml"

	// maxClusterCreatePacingWait bounds a single creation's total wait for
	// window capacity — a safety valve so a saturated or pathological state
	// file cannot stall a spec past its budget; at the bound it proceeds
	// unpaced.
	maxClusterCreatePacingWait = 30 * time.Minute

	// clusterCreatePacingPollInterval caps how often a waiting creation
	// re-checks the window.
	clusterCreatePacingPollInterval = 15 * time.Second

	// clusterCreatePacingLockRetryInterval is the retry cadence while waiting
	// for a contended pacer lock; both lock acquisitions are non-blocking.
	clusterCreatePacingLockRetryInterval = 250 * time.Millisecond
)

// errPacingLockUnavailable reports that the pacer state lock did not become
// available within the pacing deadline; pace fails open on it.
var errPacingLockUnavailable = errors.New("cluster-creation pacing lock not acquired within the pacing wait bound")

// clusterCreatePacing is a sliding-window limit on cluster-creation starts.
type clusterCreatePacing struct {
	max    int
	window time.Duration
}

func (p clusterCreatePacing) enabled() bool {
	return p.max > 0 && p.window > 0
}

// parseClusterCreatePacing parses "<max>/<window>" (e.g. "12/6m"). An empty
// value yields the default. "0", "off" and "disabled" (case-insensitive)
// disable pacing.
func parseClusterCreatePacing(value string) (clusterCreatePacing, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = defaultClusterCreatePacing
	}
	if strings.EqualFold(value, "off") || strings.EqualFold(value, "disabled") || value == "0" {
		return clusterCreatePacing{}, nil
	}
	count, window, found := strings.Cut(value, "/")
	if !found {
		return clusterCreatePacing{}, fmt.Errorf("expected \"<max>/<window>\" like %q, got %q", defaultClusterCreatePacing, value)
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

// clusterCreatePacingState is the on-disk sliding window plus refill
// smoothing: RFC3339Nano timestamps of recent starts and the earliest
// admissible time for the next start once the window has filled. It is only
// read and written while holding the flock, mirroring leasedIdentityPoolState.
type clusterCreatePacingState struct {
	Starts []string `yaml:"starts,omitempty"`
	// NextAdmission spaces starts once the window has filled: no new start is
	// admissible before this timestamp, so freed capacity refills one start
	// per window/max instead of as a batch. Empty until the window first fills.
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

// clusterCreatePacingMu serializes pacer state access within one process,
// mirroring the in-process half of leasedIdentityPoolState's locking. It is
// acquired non-blockingly (try-lock with bounded retries) inside lockState, so
// a wedged goroutine holding it cannot stall the process forever.
var clusterCreatePacingMu sync.Mutex

// pacedClusterCreates memoizes admissions per cluster so the FromParam →
// AndWait hook chain paces each cluster exactly once per process; each spec
// subprocess is its own process, so cross-process coordination still happens
// through the state file. The value is a clusterCreatePaceCall: concurrent
// paces of the same cluster wait for the in-flight admission, and a failed
// admission is dropped for the retry to re-pace.
var pacedClusterCreates sync.Map

// clusterCreatePacingKey identifies a cluster for the in-process memo: the
// subscription is fixed per spec subprocess, so resource group and name
// suffice. Keying by name alone would let a same-named cluster in another
// resource group piggyback on another's admission.
func clusterCreatePacingKey(resourceGroupName, clusterName string) string {
	return resourceGroupName + "/" + clusterName
}

// clusterCreatePaceCall is one admission through the memo: done closes when
// the admission finishes, and err carries its result to every waiter.
type clusterCreatePaceCall struct {
	done chan struct{}
	err  error
}

// paceClusterCreateOnce runs admit for key at most once at a time: concurrent
// callers with the same key wait for the in-flight result, a completed success
// memoizes (later calls are no-ops), and a failed admission is removed so a
// retry — e.g. after a cancellation — paces again.
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
		// A failed admission must not memoize: drop it so the next pace for
		// this key re-runs. CompareAndDelete leaves any newer call alone.
		// Remove before closing done so a retry that observed the failure
		// re-paces instead of re-storing this entry.
		pacedClusterCreates.CompareAndDelete(key, call)
	}
	close(call.done)
	return call.err
}

func newClusterCreatePacer(cfg clusterCreatePacing) *clusterCreatePacer {
	return &clusterCreatePacer{
		cfg:       cfg,
		statePath: filepath.Join(artifactDir(), clusterCreatePacingStateFile),
		lockPath:  filepath.Join(os.TempDir(), "cluster-create-pacing.lock"),
		now:       time.Now,
		sleep:     sleepContext,
	}
}

// paceHCPClusterCreateStart waits until starting a new HCP cluster creation is
// admissible under the shared sliding-window limiter, then records the start.
// It must be called before a per-call creation timeout is armed (in both the
// CreateHCPClusterFromParam wrappers and the CreateHCPClusterAndWait functions
// they call), so the wait consumes the spec's budget, not
// ClusterCreationTimeout.
func paceHCPClusterCreateStart(ctx context.Context, logger logr.Logger, resourceGroupName, clusterName string) error {
	cfg, err := parseClusterCreatePacing(os.Getenv(ClusterCreatePacingEnvvar))
	if err != nil {
		// Fail open: a malformed value disables pacing rather than failing tests.
		logger.Info(fmt.Sprintf("WARN: invalid %s, cluster-creation pacing disabled: %v", ClusterCreatePacingEnvvar, err))
		return nil
	}
	if !cfg.enabled() {
		return nil
	}
	// Pacing coordinates spec subprocesses through ARTIFACT_DIR; without it
	// there is nothing to coordinate through, so pacing stays off.
	if artifactDir() == "" {
		return nil
	}
	key := clusterCreatePacingKey(resourceGroupName, clusterName)
	return paceClusterCreateOnce(ctx, key, func(ctx context.Context) error {
		return newClusterCreatePacer(cfg).pace(ctx, logger, clusterName)
	})
}

// PaceHCPClusterCreateStart is the exported pacing entry point for callers
// that begin a creation through the asynchronous BeginCreateHCPCluster...
// helpers while arming a provisioning timeout of their own: call it on the
// context *before* the timeout is armed, so the pacing wait consumes the
// spec's budget; the later pace inside the helper becomes a memoized no-op.
func PaceHCPClusterCreateStart(ctx context.Context, logger logr.Logger, resourceGroupName, clusterName string) error {
	return paceHCPClusterCreateStart(ctx, logger, resourceGroupName, clusterName)
}

// pace blocks until a new cluster-creation start fits in the sliding window.
// It fails open on state-file errors and once the total wait would exceed
// maxClusterCreatePacingWait; the bound is checked before each admission
// attempt (so a start is never recorded past it) and caps every poll sleep.
// Only context cancellation arriving while the wait is inside its budget can
// abort the creation.
func (p *clusterCreatePacer) pace(ctx context.Context, logger logr.Logger, clusterName string) error {
	start := p.now()
	deadline := start.Add(maxClusterCreatePacingWait)
	waited := false
	for {
		// Check the deadline before attempting an admission, never only
		// after: tryRecordStart returning success (retryAfter == 0) must not
		// bypass the fail-open bound.
		if p.now().After(deadline) {
			logger.Info(fmt.Sprintf("WARN: cluster-creation pacing wait exceeded %s for cluster %s, proceeding unpaced", maxClusterCreatePacingWait, clusterName))
			return nil
		}
		retryAfter, err := p.tryRecordStart(ctx, deadline)
		if err != nil {
			// Cancellation must abort the creation; every other lock or state
			// failure is fail-open (best-effort pacing, never a gate).
			if ctx.Err() != nil {
				return fmt.Errorf("aborted while waiting for cluster-creation pacing (cluster %s): %w", clusterName, err)
			}
			logger.Info(fmt.Sprintf("WARN: cluster-creation pacing state unavailable, proceeding unpaced for cluster %s: %v", clusterName, err))
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
		// Cap the sleep by the remaining budget as well as the poll interval:
		// an uncapped sleep can cross the deadline, where a caller
		// cancellation aborts a creation that should proceed unpaced.
		if remaining := deadline.Sub(p.now()); remaining > 0 {
			if err := p.sleep(ctx, min(retryAfter, clusterCreatePacingPollInterval, remaining)); err != nil {
				return fmt.Errorf("aborted while waiting for cluster-creation pacing (cluster %s): %w", clusterName, err)
			}
		} else {
			// No budget left: fail open here rather than retrying an
			// admission at the deadline.
			logger.Info(fmt.Sprintf("WARN: cluster-creation pacing wait exceeded %s for cluster %s, proceeding unpaced", maxClusterCreatePacingWait, clusterName))
			return nil
		}
	}
}

// lockState acquires the pacer locks — first the in-process mutex, then the
// cross-process flock — without ever blocking indefinitely: both acquisitions
// are non-blocking, retried on clusterCreatePacingLockRetryInterval with the
// retry sleep capped by the remaining budget, so a stalled lock holder can
// neither stall a creation past the pacing deadline nor swallow its
// cancellation. It returns a release callback when both locks are held;
// ctx.Err() when the context is cancelled first, and errPacingLockUnavailable
// when the deadline passes first.
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
				// Contended file lock: give the in-process mutex back while
				// waiting, so other pacer calls in this process are not
				// starved by cross-process contention.
				clusterCreatePacingMu.Unlock()
			}
		}

		// Cap the retry sleep by the remaining budget so it cannot cross the
		// deadline, where a caller cancellation would abort a creation that
		// should proceed unpaced; with no budget left, fail open like the
		// deadline check above.
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

// tryRecordStart records a start timestamp if the trailing window has
// capacity and the refill-smoothing gate has passed. It returns the duration
// after which the caller should retry (zero when the start was recorded). A
// cancelled context aborts; any other error — state-file trouble or the lock
// not becoming free within the pacing deadline — means fail-open, which pace
// treats as proceeding unpaced.
func (p *clusterCreatePacer) tryRecordStart(ctx context.Context, deadline time.Time) (time.Duration, error) {
	release, err := p.lockState(ctx, deadline)
	if err != nil {
		return 0, err
	}
	defer release()

	now := p.now()

	state, err := p.readState()
	if err != nil {
		return 0, err
	}

	// The refill-smoothing gate: once armed (the window has filled once), no
	// start is admissible before NextAdmission. A malformed value disarms
	// smoothing for this attempt and is dropped rather than carried forward,
	// so the gate re-arms predictably once the window next fills; the
	// trailing-window cap below still holds.
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
		// RFC3339Nano keeps sub-second precision: a truncated timestamp would
		// let a start recorded in the same second as "now" appear already aged
		// out of a sub-second window. Parsing accepts fractional seconds, so
		// prior RFC3339 records stay readable.
		recent = append(recent, now.UTC().Format(time.RFC3339Nano))
		// Arm (or advance) the smoothing gate once the window has filled: the
		// initial burst itself stays unpaced, but everything after it is
		// spaced at least window/max apart, so freed capacity refills one
		// start at a time.
		next := state.NextAdmission
		if !nextAdmission.IsZero() || len(recent) >= p.cfg.max {
			next = now.Add(p.cfg.window / time.Duration(p.cfg.max)).UTC().Format(time.RFC3339Nano)
		}
		if err := p.writeState(clusterCreatePacingState{Starts: recent, NextAdmission: next}); err != nil {
			return 0, err
		}
		return 0, nil
	}

	// Not admissible yet: retry once the oldest recorded start ages out and,
	// once armed, the smoothing gate has passed. While the window is full
	// every recorded start is inside it, so the aging wait is positive.
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
