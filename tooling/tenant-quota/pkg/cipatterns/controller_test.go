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
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
)

func newTestController(t *testing.T) *Controller {
	t.Helper()
	cfg := &config.Config{
		CIJobOutcomes: config.CIJobOutcomesConfig{ClusterURI: "http://127.0.0.1:8080", IngestionURI: "http://127.0.0.1:8081", Database: "test",
			Outcomes: config.KustoTableConfig{Table: "ciJobOutcomes"}, TestNames: config.KustoTableConfig{Table: "ciTestNames"}, TestResults: config.KustoTableConfig{Table: "ciTestResults"}},
		CIFailurePatterns: config.CIFailurePatternsConfig{Results: config.KustoTableConfig{Table: "ciFailurePatterns", IngestionMapping: "ciFailurePatternsMapping"}, Interval: "10ms", CacheTTL: "1h"},
		Tenants:           []config.TenantConfig{{TenantID: "t", ServicePrincipalClientId: "c", KeyVaultSecretName: "s"}},
	}
	require.NoError(t, cfg.Validate())
	c := NewController(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.retryDelay, c.maxRetryDelay, c.initializeDelay, c.sweepPoll = time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond
	c.initialize = func(context.Context) error { return nil }
	c.readBuildIDs = func(context.Context, string, int) ([]string, error) { return nil, nil }
	c.readInputs = func(context.Context, string) ([]input, error) { return nil, nil }
	c.readResults = func(context.Context, []input, failurePatternVersion) ([]resultRow, error) { return nil, nil }
	c.submit = func(context.Context, *bytes.Buffer) error { return nil }
	t.Cleanup(c.queue.ShutDown)
	return c
}

func decodePayload(payload []byte, rows *[]resultRow) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	for {
		var row resultRow
		err := decoder.Decode(&row)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		*rows = append(*rows, row)
	}
}

type fakeDatabase struct {
	mu             sync.Mutex
	buildIDs       []string
	inputs         []input
	visible        []resultRow
	accepted       []resultRow
	submissions    int
	reads          int
	resultReads    int
	discoveryReads int
}

func (db *fakeDatabase) attach(c *Controller) {
	c.readBuildIDs = func(ctx context.Context, before string, limit int) ([]string, error) {
		db.mu.Lock()
		defer db.mu.Unlock()
		db.discoveryReads++
		distinct := map[string]bool{}
		for _, id := range db.buildIDs {
			if id != "" && (before == "" || id < before) {
				distinct[id] = true
			}
		}
		for _, i := range db.inputs {
			id := i.observation.BuildID
			if id != "" && (before == "" || id < before) {
				distinct[id] = true
			}
		}
		ids := make([]string, 0, len(distinct))
		for id := range distinct {
			ids = append(ids, id)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(ids)))
		if len(ids) > limit {
			ids = ids[:limit]
		}
		return ids, ctx.Err()
	}
	c.readInputs = func(ctx context.Context, build string) ([]input, error) {
		db.mu.Lock()
		defer db.mu.Unlock()
		db.reads++
		var inputs []input
		for _, i := range db.inputs {
			if i.observation.BuildID == build {
				inputs = append(inputs, i)
			}
		}
		return inputs, ctx.Err()
	}
	c.readResults = func(ctx context.Context, inputs []input, version failurePatternVersion) ([]resultRow, error) {
		db.mu.Lock()
		defer db.mu.Unlock()
		db.resultReads++
		keys := map[string]bool{}
		for _, i := range inputs {
			keys[envelope(i, version).ResultKey] = true
		}
		var rows []resultRow
		for _, row := range db.visible {
			if keys[row.ResultKey] {
				rows = append(rows, row)
			}
		}
		return rows, ctx.Err()
	}
	c.submit = func(ctx context.Context, payload *bytes.Buffer) error {
		db.mu.Lock()
		defer db.mu.Unlock()
		db.submissions++
		return decodePayload(payload.Bytes(), &db.accepted)
	}
}

func TestNormalReconciliationStartsAfterInitialization(t *testing.T) {
	c := newTestController(t)
	db := &fakeDatabase{inputs: []input{sampleInput()}}
	db.attach(c)
	initializing := make(chan struct{})
	ready := make(chan struct{})
	c.initialize = func(ctx context.Context) error {
		close(initializing)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ready:
			return nil
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); c.Start(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	<-initializing
	_, allowed := c.normalReconciliationVersion()
	require.False(t, allowed)
	db.mu.Lock()
	require.Zero(t, db.discoveryReads)
	require.Zero(t, db.reads)
	db.mu.Unlock()
	close(ready)
	require.Eventually(t, func() bool {
		db.mu.Lock()
		defer db.mu.Unlock()
		return len(db.accepted) == 1
	}, time.Second, time.Millisecond)
	version, allowed := c.normalReconciliationVersion()
	require.True(t, allowed)
	require.Equal(t, compiledFailurePatternVersion(), version)
	require.Error(t, c.allowNormalReconciliation(failurePatternVersion{extractorVersion: "wrong", resultSchemaVersion: 1}))
}

func TestRealNormalQueueVisibilityLateInputsNamesAndRestart(t *testing.T) {
	c := newTestController(t)
	i := sampleInput()
	db := &fakeDatabase{inputs: []input{i, i}}
	db.attach(c)
	require.NoError(t, c.allowNormalReconciliation(compiledFailurePatternVersion()))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); c.Start(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	require.Eventually(t, func() bool { db.mu.Lock(); defer db.mu.Unlock(); return len(db.accepted) == 2 && db.reads > 2 }, 2*time.Second, time.Millisecond)
	db.mu.Lock()
	require.Equal(t, 1, db.submissions, "cache suppresses pending retry without marking completion")
	db.visible = append(db.visible, db.accepted...)
	changed := i
	changed.observation.TestName = "name arrives 🧪"
	db.inputs = []input{changed, changed, changed}
	db.mu.Unlock()
	require.Eventually(t, func() bool { db.mu.Lock(); defer db.mu.Unlock(); return len(db.accepted) == 5 }, 2*time.Second, time.Millisecond)
	db.mu.Lock()
	require.Equal(t, db.accepted[0].RawFingerprint, db.accepted[2].RawFingerprint)
	require.NotEqual(t, db.accepted[0].ResultKey, db.accepted[2].ResultKey)
	db.visible = append(db.visible, db.accepted[2])
	db.visible = append(db.visible, db.accepted[2]) // physical retry copies remain one computation
	db.mu.Unlock()
	cancel()
	<-done
	// A restarted queue reconstructs work from current raw facts, reusing visible
	// current-context rows and ignoring the obsolete missing-name computation.
	restarted := newTestController(t)
	db.attach(restarted)
	require.NoError(t, restarted.allowNormalReconciliation(compiledFailurePatternVersion()))
	require.NoError(t, restarted.scanHead(t.Context()))
	require.Equal(t, 1, restarted.queue.Len(), "discovery includes completed builds")
	db.mu.Lock()
	submissions, inputReads, resultReads := db.submissions, db.reads, db.resultReads
	db.mu.Unlock()
	require.True(t, restarted.processNext(t.Context()))
	require.Zero(t, restarted.queue.Len())
	db.mu.Lock()
	require.Equal(t, submissions, db.submissions, "worker reuses visible results after restart")
	require.Equal(t, inputReads+1, db.reads)
	require.Equal(t, resultReads+1, db.resultReads)
	db.visible = nil
	db.mu.Unlock()
	require.NoError(t, restarted.scanHead(t.Context()))
	require.Equal(t, 1, restarted.queue.Len())
	require.True(t, restarted.processNext(t.Context()))
	db.mu.Lock()
	require.Len(t, db.accepted, 8)
	db.mu.Unlock()
}

func TestReconcileRetainsRepeatedInputsAndMultiplicity(t *testing.T) {
	c := newTestController(t)
	now := time.Now()
	c.now = func() time.Time { return now }
	first := sampleInput()
	second := first
	second.observation.Text = "a different failure"
	repeated := first
	repeated.multiplicity = 3
	inputs := []input{first, second, repeated}
	version := compiledFailurePatternVersion()
	db := &fakeDatabase{}
	db.attach(c)
	c.readResults = func(_ context.Context, got []input, gotVersion failurePatternVersion) ([]resultRow, error) {
		require.Equal(t, inputs, got, "reading results must preserve input order and multiplicity")
		require.Equal(t, version, gotVersion)
		return db.visible, nil
	}

	delay, err := c.reconcileInputs(t.Context(), version, inputs)
	require.NoError(t, err)
	require.Positive(t, delay)
	require.Len(t, db.accepted, 3)
	counts := map[string]int{}
	for _, row := range db.accepted {
		counts[row.ResultKey]++
	}
	require.Equal(t, map[string]int{first.keyFor(version): 2, second.keyFor(version): 1}, counts)
	require.EqualValues(t, 1, inputs[0].multiplicity)
	require.EqualValues(t, 3, inputs[2].multiplicity)
	require.Empty(t, inputs[0].key, "preparing missing computations must not mutate fixed inputs")

	// A changed missing set (second became visible) must not resubmit first,
	// which was accepted moments ago and is still awaiting visibility.
	db.visible = []resultRow{computedRow(second)}
	delay, err = c.reconcileInputs(t.Context(), version, inputs)
	require.NoError(t, err)
	require.Positive(t, delay)
	require.Len(t, db.accepted, 3, "an accepted computation is not resubmitted when the missing set changes")

	// Once the acceptance expires unseen, both repeated missing inputs are written again.
	now = now.Add(2 * time.Hour)
	delay, err = c.reconcileInputs(t.Context(), version, inputs)
	require.NoError(t, err)
	require.Positive(t, delay)
	require.Len(t, db.accepted, 5, "both repeated missing inputs must be submitted")
	require.Equal(t, first.keyFor(version), db.accepted[3].ResultKey)
	require.Equal(t, first.keyFor(version), db.accepted[4].ResultKey)

	db.visible = append(db.visible, computedRow(first))
	delay, err = c.reconcileInputs(t.Context(), version, inputs)
	require.NoError(t, err)
	require.Zero(t, delay, "one visible computation covers each matching input")
	require.Len(t, db.accepted, 5)
}

func TestPendingCacheExpiryReadsBeforeResubmission(t *testing.T) {
	c := newTestController(t)
	i := sampleInput()
	now := time.Now()
	c.now = func() time.Time { return now }
	db := &fakeDatabase{inputs: []input{i}}
	db.attach(c)
	for range 2 {
		delay, err := c.reconcileInputs(t.Context(), compiledFailurePatternVersion(), []input{i})
		require.NoError(t, err)
		require.Positive(t, delay)
	}
	require.Equal(t, 1, db.submissions)
	now = now.Add(2 * time.Hour)
	delay, err := c.reconcileInputs(t.Context(), compiledFailurePatternVersion(), []input{i})
	require.NoError(t, err)
	require.Positive(t, delay)
	require.Equal(t, 2, db.submissions)
	db.visible = append(db.visible, db.accepted...)
	delay, err = c.reconcileInputs(t.Context(), compiledFailurePatternVersion(), []input{i})
	require.NoError(t, err)
	require.Zero(t, delay)
	require.Equal(t, 2, db.submissions)
	// Even a live cache entry cannot mask contradictory query-visible rows.
	conflict := computedRow(i)
	conflict.Findings[0].Canonical += " conflict"
	db.visible = append(db.visible, conflict)
	_, err = c.reconcileInputs(t.Context(), compiledFailurePatternVersion(), []input{i})
	require.ErrorContains(t, err, "conflicting")
}

func TestQueueRetriesErrorsAndPanicsWithoutRetiringWorker(t *testing.T) {
	previous := utilruntime.ReallyCrash
	utilruntime.ReallyCrash = false
	t.Cleanup(func() { utilruntime.ReallyCrash = previous })
	for _, panicFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "panic"}[panicFirst], func(t *testing.T) {
			c := newTestController(t)
			c.queue.ShutDown()
			c.queue = workqueue.NewTypedRateLimitingQueue(workqueue.NewTypedItemExponentialFailureRateLimiter[string](time.Millisecond, time.Millisecond))
			t.Cleanup(c.queue.ShutDown)
			require.NoError(t, c.allowNormalReconciliation(compiledFailurePatternVersion()))
			attempts := 0
			c.readInputs = func(context.Context, string) ([]input, error) {
				attempts++
				if attempts == 1 {
					if panicFirst {
						panic("injected")
					}
					return nil, errors.New("injected")
				}
				return nil, nil
			}
			c.queue.Add("build")
			require.True(t, c.processNext(t.Context()))
			require.Equal(t, 1, c.queue.NumRequeues("build"))
			require.True(t, c.processNext(t.Context()))
			require.Equal(t, 2, attempts)
			require.Zero(t, c.queue.NumRequeues("build"))
			c.normalAdmissionMu.Lock()
			require.Zero(t, c.admittedReconciliations)
			c.normalAdmissionMu.Unlock()
		})
	}
}

func TestAdmissionDrainAndCancellationJoin(t *testing.T) {
	previous := utilruntime.ReallyCrash
	utilruntime.ReallyCrash = false
	t.Cleanup(func() { utilruntime.ReallyCrash = previous })
	c := newTestController(t)
	require.NoError(t, c.allowNormalReconciliation(compiledFailurePatternVersion()))
	entered := make(chan struct{})
	release := make(chan struct{})
	c.readInputs = func(ctx context.Context, _ string) ([]input, error) {
		close(entered)
		select {
		case <-release:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	workDone := make(chan struct{})
	go func() { defer close(workDone); _, _ = c.reconcile(ctx, "build") }()
	<-entered
	drainDone := make(chan error, 1)
	go func() { drainDone <- c.pauseNormalReconciliationAndWait(ctx) }()
	require.Eventually(t, func() bool { _, allowed := c.normalReconciliationVersion(); return !allowed }, time.Second, time.Millisecond)
	select {
	case <-drainDone:
		t.Fatal("drain completed with reconcile in flight")
	default:
	}
	delay, err := c.reconcile(ctx, "another")
	require.NoError(t, err)
	require.Positive(t, delay)
	cancel()
	<-workDone
	select {
	case <-drainDone:
	case <-time.After(time.Second):
		t.Fatal("drain did not cancel")
	}
	c.normalAdmissionMu.Lock()
	require.Zero(t, c.admittedReconciliations)
	require.False(t, c.normalReconciliationAllowed)
	c.normalAdmissionMu.Unlock()

	c2 := newTestController(t)
	var attempts atomic.Int32
	var closed atomic.Bool
	c2.initialize = func(context.Context) error {
		if attempts.Add(1) == 1 {
			panic("initialization")
		}
		return nil
	}
	c2.closeClients = func() { closed.Store(true) }
	runCtx, stop := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); c2.Start(runCtx) }()
	require.Eventually(t, func() bool { return attempts.Load() >= 2 }, time.Second, time.Millisecond)
	stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("workers did not join")
	}
	require.True(t, closed.Load())
}

func TestCacheBoundsAndMetricsRegistration(t *testing.T) {
	now := time.Now()
	cache := newSubmissionCache(2, time.Minute)
	cache.add("a", now)
	cache.add("b", now)
	require.True(t, cache.contains("a", now))
	cache.add("c", now)
	require.False(t, cache.contains("b", now))
	require.True(t, cache.contains("a", now))
	require.False(t, cache.contains("a", now.Add(time.Minute)))
	require.LessOrEqual(t, len(cache.entries), 2)
	c := newTestController(t)
	registry := prometheus.NewRegistry()
	require.NoError(t, c.RegisterMetrics(registry))
	require.Error(t, c.RegisterMetrics(registry))
}

func TestFatalPanicPolicyIsNotSwallowed(t *testing.T) {
	previous := utilruntime.ReallyCrash
	utilruntime.ReallyCrash = true
	t.Cleanup(func() { utilruntime.ReallyCrash = previous })
	c := newTestController(t)
	require.NoError(t, c.allowNormalReconciliation(compiledFailurePatternVersion()))
	c.readInputs = func(context.Context, string) ([]input, error) { panic("fatal policy") }
	c.queue.Add("build")
	require.Panics(t, func() { c.processNext(t.Context()) })
	c.normalAdmissionMu.Lock()
	require.Zero(t, c.admittedReconciliations)
	c.normalAdmissionMu.Unlock()
}

func TestStartCancelsInflightBeforeClosingClients(t *testing.T) {
	c := newTestController(t)
	require.NoError(t, c.allowNormalReconciliation(compiledFailurePatternVersion()))
	entered := make(chan struct{})
	returned := make(chan struct{})
	c.readBuildIDs = func(context.Context, string, int) ([]string, error) {
		return []string{sampleInput().observation.BuildID}, nil
	}
	c.readInputs = func(ctx context.Context, build string) ([]input, error) {
		close(entered)
		<-ctx.Done()
		close(returned)
		return nil, ctx.Err()
	}
	var closed atomic.Bool
	c.closeClients = func() {
		select {
		case <-returned:
			closed.Store(true)
		default:
			t.Error("clients closed before reconcile returned")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); c.Start(ctx) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("worker did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("inflight reconcile did not cancel and join")
	}
	require.True(t, closed.Load())
}

func drainDiscoveredBuildIDs(t *testing.T, c *Controller) []string {
	t.Helper()
	ids := make([]string, 0, c.queue.Len())
	for c.queue.Len() > 0 {
		id, shutdown := c.queue.Get()
		require.False(t, shutdown)
		ids = append(ids, id)
		c.queue.Forget(id)
		c.queue.Done(id)
	}
	return ids
}

func newBuildIDs(count int) []string {
	ids := make([]string, count)
	for index := range ids {
		ids[index] = fmt.Sprintf("build-%04d", index)
	}
	return ids
}

// newest returns the n highest IDs, newest first.
func newest(ids []string, n int) []string {
	out := make([]string, 0, n)
	for index := len(ids) - 1; index >= 0 && len(out) < n; index-- {
		out = append(out, ids[index])
	}
	return out
}

func TestHeadScanAlwaysReadsNewestPageOnly(t *testing.T) {
	c := newTestController(t)
	db := &fakeDatabase{}
	want := newBuildIDs(3 * discoveryPageSize)
	// Duplicate and shuffled metadata exercise the fixture's distinct descending discovery.
	for index := range want {
		db.buildIDs = append(db.buildIDs, want[len(want)-1-index])
	}
	db.buildIDs = append(db.buildIDs, want...)
	db.attach(c)
	c.readInputs = func(context.Context, string) ([]input, error) {
		t.Error("discovery must not read evidence")
		return nil, errors.New("evidence unavailable")
	}
	c.readResults = func(context.Context, []input, failurePatternVersion) ([]resultRow, error) {
		t.Error("discovery must not read results")
		return nil, errors.New("results unavailable")
	}
	require.NoError(t, c.allowNormalReconciliation(compiledFailurePatternVersion()))
	for range 3 {
		require.NoError(t, c.scanHead(t.Context()))
		require.Equal(t, newest(want, headPageSize), drainDiscoveredBuildIDs(t, c), "every poll restarts at the newest build")
	}
	require.Equal(t, 3, db.discoveryReads)
	require.Empty(t, c.sweepBefore, "head polls never move the sweep cursor")
}

func TestHeadScanSeesNewBuildDuringSweep(t *testing.T) {
	c := newTestController(t)
	db := &fakeDatabase{buildIDs: newBuildIDs(3 * discoveryPageSize)}
	db.attach(c)
	require.NoError(t, c.allowNormalReconciliation(compiledFailurePatternVersion()))
	require.NoError(t, c.sweepStep(t.Context()))
	drainDiscoveredBuildIDs(t, c)
	require.NotEmpty(t, c.sweepBefore, "sweep is mid-history")
	cursor := c.sweepBefore
	db.mu.Lock()
	db.buildIDs = append(db.buildIDs, "build-9999")
	db.mu.Unlock()
	require.NoError(t, c.scanHead(t.Context()))
	require.Equal(t, "build-9999", drainDiscoveredBuildIDs(t, c)[0], "new build is first in line without waiting for the sweep")
	require.Equal(t, cursor, c.sweepBefore)
}

func TestSweepWalksNewestToOldestWhenIdleAndRests(t *testing.T) {
	for _, count := range []int{2 * discoveryPageSize, 2*discoveryPageSize + 3} {
		t.Run(fmt.Sprintf("%d-builds", count), func(t *testing.T) {
			c := newTestController(t)
			clock := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
			c.now = func() time.Time { return clock }
			want := newBuildIDs(count)
			db := &fakeDatabase{buildIDs: want}
			db.attach(c)
			c.readInputs = func(context.Context, string) ([]input, error) {
				t.Error("discovery must not read evidence")
				return nil, errors.New("evidence unavailable")
			}
			require.NoError(t, c.allowNormalReconciliation(compiledFailurePatternVersion()))
			var got []string
			for {
				require.NoError(t, c.sweepStep(t.Context()))
				page := drainDiscoveredBuildIDs(t, c)
				got = append(got, page...)
				if len(page) < discoveryPageSize {
					break
				}
				require.Equal(t, page[len(page)-1], c.sweepBefore)
			}
			require.Equal(t, newest(want, count), got, "sweep visits every build exactly once, newest first")
			require.Empty(t, c.sweepBefore)
			reads := db.discoveryReads

			clock = clock.Add(sweepRest - time.Second)
			require.NoError(t, c.sweepStep(t.Context()))
			require.Equal(t, reads, db.discoveryReads, "sweep rests after reaching the oldest build")
			clock = clock.Add(time.Second)
			require.NoError(t, c.sweepStep(t.Context()))
			require.Equal(t, newest(want, discoveryPageSize), drainDiscoveredBuildIDs(t, c), "next sweep starts again at the newest build")

			restarted := newTestController(t)
			db.attach(restarted)
			require.NoError(t, restarted.allowNormalReconciliation(compiledFailurePatternVersion()))
			require.NoError(t, restarted.sweepStep(t.Context()))
			require.Equal(t, newest(want, discoveryPageSize), drainDiscoveredBuildIDs(t, restarted), "restart sweeps immediately from the newest build")
			require.Zero(t, db.reads)
			require.Zero(t, db.resultReads)
		})
	}
}

func TestSweepBoundsOutstandingWorkIncludingPendingVisibility(t *testing.T) {
	c := newTestController(t)
	db := &fakeDatabase{buildIDs: newBuildIDs(3 * discoveryPageSize)}
	db.attach(c)
	require.NoError(t, c.allowNormalReconciliation(compiledFailurePatternVersion()))

	// A full page that is queued blocks the next page.
	for index := range discoveryPageSize {
		c.queue.Add(fmt.Sprintf("queued-%d", index))
	}
	require.NoError(t, c.sweepStep(t.Context()))
	require.Zero(t, db.discoveryReads, "sweep does not add a page while a page is queued")
	drainDiscoveredBuildIDs(t, c)

	// A page whose results are not visible yet is not in the ready queue, but it is still outstanding.
	for index := range discoveryPageSize {
		c.pendingDelay(fmt.Sprintf("awaiting-visibility-%d", index), time.Second)
	}
	require.Zero(t, c.queue.Len())
	require.NoError(t, c.sweepStep(t.Context()))
	require.Zero(t, db.discoveryReads, "sweep does not add a page while a page awaits visibility")

	// Once part of that page becomes visible there is room for exactly one more page.
	c.forgetPending("awaiting-visibility-0")
	require.NoError(t, c.sweepStep(t.Context()))
	require.Equal(t, 1, db.discoveryReads)
	require.Equal(t, discoveryPageSize, c.queue.Len())
	require.NoError(t, c.sweepStep(t.Context()))
	require.Equal(t, 1, db.discoveryReads, "outstanding work stays under two pages")
}

func TestSweepCountsBuildsFailingBeforeFirstSubmission(t *testing.T) {
	c := newTestController(t)
	db := &fakeDatabase{buildIDs: newBuildIDs(3 * discoveryPageSize)}
	db.attach(c)
	c.readInputs = func(context.Context, string) ([]input, error) { return nil, errors.New("kusto unavailable") }
	require.NoError(t, c.allowNormalReconciliation(compiledFailurePatternVersion()))
	require.NoError(t, c.sweepStep(t.Context()))
	require.Equal(t, 1, db.discoveryReads)
	for range discoveryPageSize {
		require.True(t, c.processNext(t.Context()))
	}
	require.NoError(t, c.sweepStep(t.Context()))
	require.Equal(t, 1, db.discoveryReads, "builds delayed after a failure still count as outstanding")
}

func TestDiscoveryFailureAndCancellationPreserveSweepCursor(t *testing.T) {
	for _, scenario := range []string{"query failure", "cancelled before query", "cancelled during query", "cancelled empty page"} {
		t.Run(scenario, func(t *testing.T) {
			c := newTestController(t)
			require.NoError(t, c.allowNormalReconciliation(compiledFailurePatternVersion()))
			const cursor = "build-0100"
			c.sweepBefore = cursor
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			c.readBuildIDs = func(ctx context.Context, before string, limit int) ([]string, error) {
				calls++
				require.Equal(t, cursor, before)
				require.Equal(t, discoveryPageSize, limit)
				if scenario == "query failure" {
					return []string{"build-0099"}, errors.New("failed page")
				}
				cancel()
				if scenario == "cancelled empty page" {
					return nil, nil
				}
				return []string{"build-0099"}, nil
			}
			if scenario == "cancelled before query" {
				cancel()
			}
			require.Error(t, c.sweepStep(ctx))
			require.Equal(t, cursor, c.sweepBefore)
			require.Zero(t, c.queue.Len(), "failed or cancelled page must not schedule returned IDs")
			if scenario == "cancelled before query" {
				require.Zero(t, calls)
				return
			}
			require.Equal(t, 1, calls)
			c.readBuildIDs = func(context.Context, string, int) ([]string, error) { return []string{"build-0099"}, nil }
			require.NoError(t, c.sweepStep(t.Context()), "next step can retry the unchanged cursor")
			require.Equal(t, []string{"build-0099"}, drainDiscoveredBuildIDs(t, c))
			require.Empty(t, c.sweepBefore)
		})
	}
}

func TestClosedDiscoveryGateDoesNotQueryOrMoveCursor(t *testing.T) {
	c := newTestController(t)
	c.sweepBefore = "build-0100"
	c.readBuildIDs = func(context.Context, string, int) ([]string, error) {
		t.Fatal("closed admission must not query discovery")
		return nil, nil
	}
	require.NoError(t, c.scanHead(t.Context()))
	require.NoError(t, c.sweepStep(t.Context()))
	require.Equal(t, "build-0100", c.sweepBefore)
	require.Zero(t, c.queue.Len())
}

func TestPendingDelayBacksOffAndResets(t *testing.T) {
	c := newTestController(t)
	c.maxRetryDelay = 40 * time.Millisecond
	var got []time.Duration
	for range 6 {
		got = append(got, c.pendingDelay("build", 5*time.Millisecond))
	}
	require.Equal(t, []time.Duration{5, 10, 20, 40, 40, 40}, scaleMillis(got))
	require.Equal(t, 5*time.Millisecond, c.pendingDelay("other", 5*time.Millisecond), "builds back off independently")
	c.forgetPending("build")
	require.Equal(t, 5*time.Millisecond, c.pendingDelay("build", 5*time.Millisecond), "visible work restarts at the base delay")
}

func scaleMillis(delays []time.Duration) []time.Duration {
	out := make([]time.Duration, len(delays))
	for index, d := range delays {
		out[index] = d / time.Millisecond
	}
	return out
}

func TestStartSweepsHistoryBeyondNewestPage(t *testing.T) {
	c := newTestController(t)
	want := newBuildIDs(2*discoveryPageSize + 1)
	db := &fakeDatabase{buildIDs: want}
	db.attach(c)
	var mu sync.Mutex
	seen := map[string]bool{}
	read := c.readInputs
	c.readInputs = func(ctx context.Context, build string) ([]input, error) {
		mu.Lock()
		seen[build] = true
		mu.Unlock()
		return read(ctx, build)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); c.Start(ctx) }()
	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(seen) == len(want) }, 5*time.Second, time.Millisecond,
		"head polls cover only the newest page; the sweep reaches the rest")
	cancel()
	<-done
}
