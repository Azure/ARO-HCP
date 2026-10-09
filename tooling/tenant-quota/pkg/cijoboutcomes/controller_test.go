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

package cijoboutcomes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/ARO-HCP/internal/utils"
)

func respondRuns(t *testing.T, runs ...sippyRun) *http.Response {
	t.Helper()
	data, err := json.Marshal(sippyResponse{Rows: runs})
	require.NoError(t, err)
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data))}
}

func discoverySince(t *testing.T, request *http.Request) time.Time {
	t.Helper()
	var filter struct{ Items []map[string]string }
	require.NoError(t, json.Unmarshal([]byte(request.URL.Query().Get("filter")), &filter))
	require.Equal(t, "contains", filter.Items[0]["operatorValue"])
	require.Equal(t, "ARO-HCP", filter.Items[0]["value"])
	require.Equal(t, ">=", filter.Items[1]["operatorValue"])
	since, err := time.Parse(time.RFC3339Nano, filter.Items[1]["value"])
	require.NoError(t, err)
	return since
}

func drainIDs(w *Writer) []BuildID {
	var ids []BuildID
	for w.queue.Len() > 0 {
		id, _ := w.queue.Get()
		ids = append(ids, id)
		w.queue.Done(id)
		w.queue.Forget(id)
	}
	return ids
}

func TestDiscoveryBoundariesIndependentSortedAndUnconditional(t *testing.T) {
	w := newTestWriter(t)
	now := time.Date(2026, 10, 8, 12, 0, 0, 123, time.UTC)
	cursors := map[string]time.Time{"Presubmits": now.Add(-30 * time.Hour), "4.21": now.Add(-time.Hour)}
	w.cursor = func(_ context.Context, release string) (time.Time, error) { return cursors[release], nil }
	var sinceByRelease = map[string]time.Time{}
	failFirst := true
	w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
		release := r.URL.Query().Get("release")
		since := discoverySince(t, r)
		sinceByRelease[release] = since
		if release == "Presubmits" && failFirst {
			return nil, errors.New("Sippy unavailable")
		}
		invalid := testRun("98", since)
		invalid.URL = testRun("99", since).URL
		pending := testRun("30", since.Add(time.Hour))
		pending.OverallResult, pending.Succeeded = "R", false
		return respondRuns(t, pending, testRun("20", since), testRun("10", since), testRun("1", since.Add(-time.Nanosecond)), invalid, sippyRun{}), nil
	})
	w.batches.put(runBatch.tag("10"), struct{}{})
	w.discover(testContext(t, w), now)
	require.Equal(t, now.Add(-33*time.Hour), sinceByRelease["Presubmits"])
	require.Equal(t, now.Add(-24*time.Hour), sinceByRelease["4.21"])
	require.True(t, w.lastRepair["Presubmits"].IsZero(), "failed release must not advance repair schedule")
	require.Equal(t, now, w.lastRepair["4.21"])
	require.Equal(t, []BuildID{"10", "20", "30"}, drainIDs(w), "queue every candidate, including cached outcomes and neither failed nor succeeded runs")
	require.Equal(t, float64(now.Unix()), testutil.ToFloat64(w.metrics.discovery.WithLabelValues("4.21")))
	failFirst = false
	w.discover(testContext(t, w), now.Add(5*time.Minute))
	require.Equal(t, now.Add(-33*time.Hour), sinceByRelease["Presubmits"], "failed repair retries next poll")
	require.Equal(t, cursors["4.21"].Add(-3*time.Hour), sinceByRelease["4.21"], "incremental always overlaps the Kusto cursor")
	drainIDs(w)
	w.discover(testContext(t, w), now.Add(12*time.Hour))
	require.Equal(t, now.Add(-12*time.Hour), sinceByRelease["4.21"], "repair repeats at exactly twelve hours")
}

func TestStartupSinceDiscoveryLifecycle(t *testing.T) {
	w := newTestWriter(t)
	w.config.CIJobOutcomes.StartupSince = "2026-09-15T00:00:00Z"
	require.NoError(t, w.config.Validate())
	startupSince := w.config.CIJobOutcomes.GetStartupSince()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	cursor := now.Add(-time.Hour)
	w.cursor = func(context.Context, string) (time.Time, error) { return cursor, nil }
	wantSince := startupSince
	fail := true
	w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, wantSince, discoverySince(t, r))
		if fail {
			return nil, errors.New("Sippy unavailable")
		}
		return respondRuns(t, testRun("123", startupSince)), nil
	})
	ctx := testContext(t, w)
	require.Error(t, w.discoverRelease(ctx, "Presubmits", now))
	require.True(t, w.lastRepair["Presubmits"].IsZero(), "failure must retain the startup override")
	fail = false
	now = now.Add(5 * time.Minute)
	require.NoError(t, w.discoverRelease(ctx, "Presubmits", now))
	require.Equal(t, []BuildID{"123"}, drainIDs(w), "September 15 runs must be queued even with an October 8 cursor")
	require.Equal(t, now, w.lastRepair["Presubmits"])

	wantSince = cursor.Add(-3 * time.Hour)
	require.NoError(t, w.discoverRelease(ctx, "Presubmits", now.Add(5*time.Minute)))
	require.Empty(t, drainIDs(w), "normal polls must ignore the startup override")
	require.Equal(t, now, w.lastRepair["Presubmits"])

	repairAt := now.Add(12 * time.Hour)
	wantSince = repairAt.Add(-24 * time.Hour)
	require.NoError(t, w.discoverRelease(ctx, "Presubmits", repairAt))
	require.Empty(t, drainIDs(w), "later repairs must use the normal lookback")
	require.Equal(t, repairAt, w.lastRepair["Presubmits"])

	wantSince = startupSince
	require.NoError(t, w.discoverRelease(ctx, "4.21", repairAt))
	require.Equal(t, []BuildID{"123"}, drainIDs(w), "each release has its own startup scan")
	restarted := newTestWriter(t)
	restarted.config = w.config
	restarted.cursor = w.cursor
	restarted.client.Transport = w.client.Transport
	require.NoError(t, restarted.discoverRelease(testContext(t, restarted), "Presubmits", repairAt))
	require.Equal(t, []BuildID{"123"}, drainIDs(restarted), "restart must repeat the configured startup override")
}

func TestStartupSincePreservesEarlierLookback(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		cursor time.Time
		want   time.Time
	}{
		{name: "older cursor", cursor: now.Add(-48 * time.Hour), want: now.Add(-51 * time.Hour)},
		{name: "repair window", cursor: now.Add(-time.Hour), want: now.Add(-24 * time.Hour)},
		{name: "empty cursor", want: now.Add(-24 * time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newTestWriter(t)
			w.config.CIJobOutcomes.StartupSince = now.Format(time.RFC3339)
			require.NoError(t, w.config.Validate())
			w.cursor = func(context.Context, string) (time.Time, error) { return tc.cursor, nil }
			w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
				require.Equal(t, tc.want, discoverySince(t, r), "startupSince must only extend the lookback")
				return respondRuns(t), nil
			})
			require.NoError(t, w.discoverRelease(testContext(t, w), "Presubmits", now))
		})
	}
}

func TestEmptyCursorAlwaysUsesWindow(t *testing.T) {
	w := newTestWriter(t)
	now := time.Now().UTC()
	w.lastRepair["Presubmits"] = now
	w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, now.Add(-24*time.Hour), discoverySince(t, r))
		return respondRuns(t), nil
	})
	require.NoError(t, w.discoverRelease(testContext(t, w), "Presubmits", now))
}

func TestOutOfOrderRunRecoveredByIncrementalOverlapAndRepair(t *testing.T) {
	w := newTestWriter(t)
	now := time.Now().UTC()
	cursor := now.Add(-time.Hour)
	w.cursor = func(context.Context, string) (time.Time, error) { return cursor, nil }
	w.lastRepair["Presubmits"] = now
	older := testRun("123", now.Add(-2*time.Hour))
	oldest := testRun("456", now.Add(-5*time.Hour))
	w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
		var runs []sippyRun
		for _, run := range []sippyRun{older, oldest} {
			if !run.Timestamp.Before(discoverySince(t, r)) {
				runs = append(runs, run)
			}
		}
		return respondRuns(t, runs...), nil
	})
	require.NoError(t, w.discoverRelease(testContext(t, w), "Presubmits", now.Add(5*time.Minute)))
	require.Equal(t, []BuildID{"123"}, drainIDs(w))
	require.NoError(t, w.discoverRelease(testContext(t, w), "Presubmits", now.Add(12*time.Hour)))
	require.Equal(t, []BuildID{"456", "123"}, drainIDs(w))
}

func TestMetadataMissLooksUpExactIDAcrossReleases(t *testing.T) {
	w := newTestWriter(t)
	w.metadata = newTTLCache[BuildID, runMetadata](1, 15*time.Minute)
	seedRun(w, "123")
	seedRun(w, "evict")
	var releases []string
	w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
		release := r.URL.Query().Get("release")
		releases = append(releases, release)
		var filter struct{ Items []map[string]string }
		require.NoError(t, json.Unmarshal([]byte(r.URL.Query().Get("filter")), &filter))
		require.Equal(t, []map[string]string{{"columnField": "prow_id", "operatorValue": "=", "value": "123"}}, filter.Items)
		if release == "Presubmits" {
			return respondRuns(t, testRun("1234", time.Now())), nil
		}
		return respondRuns(t, testRun("123", time.Now())), nil
	})
	metadata, err := w.lookupMetadata(testContext(t, w), "123")
	require.NoError(t, err)
	require.Equal(t, "123", metadata.run.ProwID)
	require.Equal(t, "4.21", metadata.release)
	require.Equal(t, []string{"Presubmits", "4.21"}, releases)
	_, err = w.lookupMetadata(testContext(t, w), "123")
	require.NoError(t, err)
	require.Len(t, releases, 2, "metadata cache hit must avoid Sippy")
}

func TestOnlyTerminalMetadataIsCached(t *testing.T) {
	for _, source := range []string{"discovery", "lookup", "one-shot"} {
		for _, result := range []string{"R", "", "f", "unknown", "S", "F", "I", "U", "N", "n", "A"} {
			t.Run(source+"/"+result, func(t *testing.T) {
				w := newTestWriter(t)
				now := time.Now().UTC()
				run := testRun("123", now)
				run.OverallResult = result
				w.client.Transport = ingestionTransport(func(*http.Request) (*http.Response, error) {
					return respondRuns(t, run), nil
				})
				ctx := testContext(t, w)
				switch source {
				case "discovery":
					require.NoError(t, w.discoverRelease(ctx, "Presubmits", now))
					require.Equal(t, []BuildID{"123"}, drainIDs(w), "pending runs must still be queued")
				case "lookup":
					metadata, err := w.lookupMetadata(ctx, "123")
					require.NoError(t, err)
					require.Equal(t, run, metadata.run, "pending metadata remains usable by independent sources")
				case "one-shot":
					ids, err := w.selectOnce(ctx, OneShotOptions{Releases: []string{"Presubmits"}, Since: now, Until: now.Add(time.Hour), Limit: 1})
					require.NoError(t, err)
					require.Equal(t, []BuildID{"123"}, ids, "pending runs must still be selected")
				}
				_, cached := w.metadata.get("123")
				require.Equal(t, run.hasTerminalOutcome(), cached)
			})
		}
	}
}

func recoverPanics(t *testing.T) {
	t.Helper()
	previous := utilruntime.ReallyCrash
	utilruntime.ReallyCrash = false
	t.Cleanup(func() { utilruntime.ReallyCrash = previous })
}

func TestDiscoveryPanicRetriesAndDoesNotBlockOtherRelease(t *testing.T) {
	recoverPanics(t)
	w := newTestWriter(t)
	panicking := true
	w.cursor = func(_ context.Context, release string) (time.Time, error) {
		if release == "Presubmits" && panicking {
			panic("discovery")
		}
		return time.Time{}, nil
	}
	w.client.Transport = ingestionTransport(func(*http.Request) (*http.Response, error) { return respondRuns(t), nil })
	now := time.Now()
	w.discover(testContext(t, w), now)
	require.True(t, w.lastRepair["Presubmits"].IsZero())
	require.Equal(t, now, w.lastRepair["4.21"])
	panicking = false
	w.discover(testContext(t, w), now.Add(time.Minute))
	require.Equal(t, now.Add(time.Minute), w.lastRepair["Presubmits"])
}

func TestWorkerSurvivesPanicAndRetriesWithoutLimit(t *testing.T) {
	recoverPanics(t)
	w := newTestWriter(t)
	w.queue.ShutDown()
	w.queue = workqueue.NewTypedRateLimitingQueue(workqueue.NewTypedItemExponentialFailureRateLimiter[BuildID](time.Millisecond, time.Millisecond))
	ctx, cancel := context.WithCancel(testContext(t, w))
	defer cancel()
	seedRun(w, "123")
	var calls atomic.Int32
	w.jobDetail = func(ctx context.Context, _ *http.Client, _ string) (runDetail, error) {
		if calls.Add(1) <= 5 {
			panic("artifact failure")
		}
		return runDetail{}, nil
	}
	w.queue.Add("123")
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for w.processNext(ctx) {
		}
	}()
	require.Eventually(t, func() bool { _, ok := w.batches.get(runBatch.tag("123")); return ok }, 5*time.Second, time.Millisecond)
	require.Equal(t, int32(6), calls.Load())
	seedRun(w, "456")
	w.queue.Add("456")
	require.Eventually(t, func() bool { _, ok := w.batches.get(runBatch.tag("456")); return ok }, 5*time.Second, time.Millisecond)
	cancel()
	w.queue.ShutDown()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestStartJoinsBeforeClosingClients(t *testing.T) {
	w := newTestWriter(t)
	workers := 1
	w.config.CIJobOutcomes.Workers = &workers
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	w.initialize = func(context.Context) error { return nil }
	w.client.Transport = ingestionTransport(func(*http.Request) (*http.Response, error) { return respondRuns(t), nil })
	entered, exited, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	seedRun(w, "123")
	w.jobDetail = func(ctx context.Context, _ *http.Client, _ string) (runDetail, error) {
		name, ok := utils.ControllerNameFromContext(ctx)
		require.True(t, ok)
		require.Equal(t, CIJobOutcomesControllerName, name)
		_, hasDeadline := ctx.Deadline()
		require.False(t, hasDeadline, "reconcile must not have an overall timeout")
		close(entered)
		<-ctx.Done()
		close(exited)
		return runDetail{}, ctx.Err()
	}
	w.closeClients = func() {
		select {
		case <-exited:
		default:
			t.Error("clients closed before worker exited")
		}
		close(closed)
	}
	w.queue.Add("123")
	finished := make(chan struct{})
	go func() { w.Start(ctx); close(finished) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not join workers")
	}
	select {
	case <-closed:
	default:
		t.Fatal("clients not closed")
	}
	require.Zero(t, w.queue.NumRequeues("123"), "cancelled reconcile must not retry")
}

func TestInitializationFailureWaitsForCancellation(t *testing.T) {
	w := newTestWriter(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan struct{})
	w.initialize = func(context.Context) error { close(entered); return errors.New("schema unavailable") }
	finished := make(chan struct{})
	go func() { w.Start(ctx); close(finished) }()
	<-entered
	select {
	case <-finished:
		t.Fatal("transient initialization failure stopped Start")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("initialization retry ignored cancellation")
	}
}

func TestInitializationRetriesThenStarts(t *testing.T) {
	w := newTestWriter(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	attempts := 0
	w.initialize = func(context.Context) error {
		attempts++
		if attempts == 1 {
			return errors.New("mapping migration not yet deployed")
		}
		return nil
	}
	w.client.Transport = ingestionTransport(func(*http.Request) (*http.Response, error) {
		cancel()
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"rows":[]}`))}, nil
	})
	finished := make(chan struct{})
	go func() { w.Start(ctx); close(finished) }()
	select {
	case <-finished:
		require.Equal(t, 2, attempts)
	case <-time.After(10 * time.Second):
		t.Fatal("initialization was not retried")
	}
}

func TestCursorQueryFiltersAndEscapes(t *testing.T) {
	w := newTestWriter(t)
	w.config.CIJobOutcomes.JobFilter = "job' | take 0"
	statement := cursorQuery(w.config.CIJobOutcomes, "release' | take 0").String()
	require.Contains(t, statement, "sippyRelease == ")
	require.Contains(t, statement, "jobName contains ")
	require.Contains(t, statement, "finishedAt > datetime(0001-01-01")
	require.Contains(t, statement, "max(startedAt)")
	require.False(t, strings.Contains(statement, "== 'release' | take 0'"), "release must be escaped by KQL builder")
}

func TestBatchTagsAndExtentQueries(t *testing.T) {
	w := newTestWriter(t)
	seen := map[string]bool{}
	for _, kind := range batchKinds {
		tag := kind.tag("123")
		require.False(t, seen[tag], "each batch needs its own immutable tag")
		seen[tag] = true
		query := extentTagQuery(w.target(kind).Table, tag).String()
		require.Equal(t, `.show table ["`+w.target(kind).Table+`"] extents where tags has "ingest-by:`+tag+`" | project ExtentId`, query)
	}
	require.Equal(t, "run-123", runTag("123"), "retain existing run tags")
	require.Equal(t, "tests-123", testsTag("123"), "retain existing test tags")
	require.Equal(t, w.target(testsBatch), w.target(observabilityTestsBatch))
	require.Equal(t, w.target(namesBatch), w.target(observabilityNamesBatch))
	query := extentTagQuery(`table"] | take 0`, `tag" | take 0`).String()
	require.Equal(t, `.show table ["table\"] | take 0"] extents where tags has "ingest-by:tag\" | take 0" | project ExtentId`, query)
}
