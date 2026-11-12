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
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/ARO-HCP/internal/utils"
)

func drainURIs(queue workqueue.TypedRateLimitingInterface[JobURI]) []JobURI {
	var uris []JobURI
	for queue.Len() > 0 {
		uri, _ := queue.Get()
		uris = append(uris, uri)
		queue.Done(uri)
		queue.Forget(uri)
	}
	return uris
}

func TestDiscoveryWindowsAndSummary(t *testing.T) {
	w := newTestWriter(t)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	job := GCSJob{Name: "periodic-ci-Azure-ARO-HCP-test", Prefix: "logs/periodic-ci-Azure-ARO-HCP-test/"}
	cursorTime := now.Add(-time.Hour)
	cursor, err := SnowflakeLowerBound(cursorTime)
	require.NoError(t, err)
	w.jobCursor = func(context.Context, GCSJob) (string, error) { return cursor, nil }
	w.config.CIJobOutcomes.StartupSince = "2026-09-15T00:00:00Z"
	require.NoError(t, w.config.Validate())
	wantSince := w.config.CIJobOutcomes.GetStartupSince()
	wantUntil := now
	fail := true
	var logs bytes.Buffer
	logger := logr.FromSlogHandler(slog.NewJSONHandler(&logs, nil))
	ctx := utils.ContextWithLogger(t.Context(), logger)
	w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
		lower, err := SnowflakeLowerBound(wantSince)
		require.NoError(t, err)
		require.Equal(t, job.Prefix+lower, r.URL.Query().Get("startOffset"))
		upper := uint64(wantUntil.UnixMilli()-snowflakeEpochMS+1) << snowflakeShift
		require.Equal(t, job.Prefix+fmt.Sprint(upper), r.URL.Query().Get("endOffset"))
		if fail {
			return nil, errors.New("unavailable")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})
	require.Error(t, w.discoverJob(ctx, job, now))
	require.True(t, w.lastRepair[job.Prefix].IsZero())
	fail = false
	require.NoError(t, w.discoverJob(ctx, job, now))
	require.Equal(t, now, w.lastRepair[job.Prefix])
	wantSince = cursorTime.Add(-10 * time.Minute)
	wantUntil = now.Add(30 * time.Minute)
	require.NoError(t, w.discoverJob(ctx, job, wantUntil))
	wantUntil = now.Add(12 * time.Hour)
	wantSince = wantUntil.Add(-24 * time.Hour)
	require.NoError(t, w.discoverJob(ctx, job, wantUntil))
	for _, field := range []string{"since", "until", "fetched", "enqueueAttempts", "queueDepth", "duration", "startup", "incremental", "repair"} {
		require.Contains(t, logs.String(), field)
	}
	// An empty cursor must retain the lookback even outside a repair scan.
	w.jobCursor = func(context.Context, GCSJob) (string, error) { return "", nil }
	wantUntil = wantUntil.Add(30 * time.Minute)
	wantSince = wantUntil.Add(-24 * time.Hour)
	require.NoError(t, w.discoverJob(ctx, job, wantUntil))
}

func TestDiscoveryPagesEnqueueUnconditionally(t *testing.T) {
	w := newTestWriter(t)
	job := GCSJob{Name: "periodic-ci-Azure-ARO-HCP-test", Prefix: "logs/periodic-ci-Azure-ARO-HCP-test/"}
	idTime, err := BuildIDTime(string(testBuildID))
	require.NoError(t, err)
	pages := 0
	w.jobCursor = func(context.Context, GCSJob) (string, error) { return string(testBuildID), nil }
	w.batches.put(discoveryTag(testJobURI), struct{}{})
	w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
		pages++
		body := fmt.Sprintf(`{"prefixes":[%q],"nextPageToken":"next"}`, job.Prefix+string(testBuildID)+"/")
		if r.URL.Query().Get("pageToken") == "next" {
			body = `{}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	require.NoError(t, w.discoverJob(testContext(t, w), job, idTime.Add(time.Hour)))
	require.Equal(t, 2, pages)
	require.Equal(t, []JobURI{testJobURI}, drainURIs(w.discoveryQueue))
	require.NoError(t, w.discoverJob(testContext(t, w), job, idTime.Add(time.Hour)))
	require.Equal(t, []JobURI{testJobURI}, drainURIs(w.discoveryQueue))
}

func TestEmptyCursorDiscoversArrivalsOnRepeatedPolls(t *testing.T) {
	w := newTestWriter(t)
	ctx := testContext(t, w)
	startup := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	job := GCSJob{Name: "periodic-ci-Azure-ARO-HCP-test", Prefix: "logs/periodic-ci-Azure-ARO-HCP-test/"}
	for poll := range 3 {
		now := startup.Add(time.Duration(poll) * w.config.CIJobOutcomes.GetDiscoveryInterval())
		var want []JobURI
		body := `{}`
		if poll > 0 {
			id, err := SnowflakeLowerBound(now.Add(-time.Minute))
			require.NoError(t, err)
			prefix := job.Prefix + id + "/"
			body = fmt.Sprintf(`{"prefixes":[%q]}`, prefix)
			want = []JobURI{JobURI("gs://" + w.gcs.Bucket + "/" + strings.TrimSuffix(prefix, "/"))}
		}
		w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
			lower, err := SnowflakeLowerBound(now.Add(-w.config.CIJobOutcomes.GetWindow()))
			require.NoError(t, err)
			require.Equal(t, job.Prefix+lower, r.URL.Query().Get("startOffset"))
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		require.NoError(t, w.discoverJob(ctx, job, now))
		require.Equal(t, want, drainURIs(w.discoveryQueue), "poll %d must discover arrivals while Kusto has no cursor", poll)
		require.Equal(t, startup, w.lastRepair[job.Prefix], "later polls must not require another repair")
	}
}

type discoveryDelayQueue struct {
	workqueue.TypedRateLimitingInterface[JobURI]
	delays []time.Duration
}

func (q *discoveryDelayQueue) AddAfter(_ JobURI, delay time.Duration) {
	q.delays = append(q.delays, delay)
}

func TestDiscoveryWorkerRetriesAcceptanceUntilLiveRowVisible(t *testing.T) {
	w := newTestWriter(t)
	ctx := testContext(t, w)
	queue := &discoveryDelayQueue{TypedRateLimitingInterface: w.discoveryQueue}
	w.discoveryQueue = queue
	now := time.Now()
	w.batches.now = func() time.Time { return now }
	checks, submissions := 0, 0
	visible := false
	w.discoveredExists = func(context.Context, JobURI) (bool, error) {
		checks++
		return visible, nil
	}
	w.submitDiscovery = func(context.Context, []DiscoveredJob, string) error {
		submissions++
		return nil
	}
	for _, step := range []struct {
		advance             time.Duration
		visible             bool
		checks, submissions int
		delayed             bool
	}{
		{checks: 1, submissions: 1, delayed: true},
		{advance: time.Minute, checks: 1, submissions: 1, delayed: true},
		{advance: w.batches.ttl, checks: 2, submissions: 2, delayed: true},
		{advance: w.batches.ttl, visible: true, checks: 3, submissions: 2},
		{visible: true, checks: 4, submissions: 2},
	} {
		now = now.Add(step.advance)
		visible = step.visible
		queue.delays = nil
		queue.Add(testJobURI)
		require.True(t, w.processNextDiscovery(ctx))
		require.Equal(t, step.checks, checks)
		require.Equal(t, step.submissions, submissions)
		require.Zero(t, queue.NumRequeues(testJobURI), "acceptance visibility is an expected delay, not an error retry")
		if step.delayed {
			require.Equal(t, []time.Duration{15 * time.Minute}, queue.delays)
		} else {
			require.Empty(t, queue.delays, "a live discovery row must stop the retry chain")
			require.Zero(t, w.batches.size(), "live rows must not enter the acceptance cache")
		}
	}
}

func TestRepairNeverNarrowsOlderCursorOrStartupWindow(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	for _, age := range []time.Duration{time.Hour, 48 * time.Hour} {
		w := newTestWriter(t)
		job := GCSJob{Name: "periodic-ci-Azure-ARO-HCP-test", Prefix: "logs/periodic-ci-Azure-ARO-HCP-test/"}
		cursor, err := SnowflakeLowerBound(now.Add(-age))
		require.NoError(t, err)
		w.jobCursor = func(context.Context, GCSJob) (string, error) { return cursor, nil }
		w.config.CIJobOutcomes.StartupSince = now.Format(time.RFC3339)
		require.NoError(t, w.config.Validate())
		want := now.Add(-max(24*time.Hour, age+10*time.Minute))
		w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
			lower, err := SnowflakeLowerBound(want)
			require.NoError(t, err)
			require.Equal(t, job.Prefix+lower, r.URL.Query().Get("startOffset"))
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		})
		require.NoError(t, w.discoverJob(testContext(t, w), job, now))
	}
}

func TestPendingScanNoAgeLimitAndRejectsInvalidKeys(t *testing.T) {
	w := newTestWriter(t)
	w.pendingJobs = func(context.Context) ([]JobURI, error) {
		return []JobURI{testJobURI, testJobURI, "bad", testJobURI + "/"}, nil
	}
	w.discoverPending(testContext(t, w))
	require.Equal(t, []JobURI{testJobURI}, drainURIs(w.queue))
	w.pendingJobs = func(context.Context) ([]JobURI, error) { return []JobURI{testJobURI}, errors.New("partial results") }
	w.discoverPending(testContext(t, w))
	require.Empty(t, drainURIs(w.queue), "a partial query cannot be treated as a successful scan")
}

func recoverPanics(t *testing.T) {
	t.Helper()
	previous := utilruntime.ReallyCrash
	utilruntime.ReallyCrash = false
	t.Cleanup(func() { utilruntime.ReallyCrash = previous })
}

func TestWorkersSurvivePanicsAndUseExpectedDelays(t *testing.T) {
	recoverPanics(t)
	for _, discovery := range []bool{false, true} {
		w := newTestWriter(t)
		queue := workqueue.NewTypedRateLimitingQueue(workqueue.NewTypedItemExponentialFailureRateLimiter[JobURI](time.Millisecond, time.Millisecond))
		t.Cleanup(queue.ShutDown)
		var calls atomic.Int32
		reconcile := func(context.Context, JobURI) (time.Duration, error) {
			if calls.Add(1) < 3 {
				panic("transient panic")
			}
			return time.Hour, nil
		}
		if discovery {
			w.discoveryQueue.ShutDown()
			w.discoveryQueue = queue
		} else {
			w.queue.ShutDown()
			w.queue = queue
		}
		ctx, cancel := context.WithCancel(testContext(t, w))
		queue.Add(testJobURI)
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			for w.processNextQueue(ctx, queue, reconcile) {
			}
		}()
		require.Eventually(t, func() bool { return calls.Load() >= 3 && queue.NumRequeues(testJobURI) == 0 }, time.Second, time.Millisecond)
		require.Equal(t, int32(3), calls.Load())
		cancel()
		queue.ShutDown()
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("worker did not stop")
		}
	}
}

func TestStartSharesLifetimeAndJoinsBothControllers(t *testing.T) {
	w := newTestWriter(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	initializations, closes := 0, 0
	w.initialize = func(context.Context) error { initializations++; return nil }
	w.client.Transport = ingestionTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})
	processingEntered, discoveryEntered := make(chan struct{}), make(chan struct{})
	processingExited, discoveryExited := make(chan struct{}), make(chan struct{})
	w.completion = func(ctx context.Context, _ *http.Client, _ string) (*prowCompletion, error) {
		name, ok := utils.ControllerNameFromContext(ctx)
		require.True(t, ok)
		require.Equal(t, CIJobOutcomesControllerName, name)
		close(processingEntered)
		<-ctx.Done()
		close(processingExited)
		return nil, ctx.Err()
	}
	w.discoveredExists = func(ctx context.Context, _ JobURI) (bool, error) {
		name, ok := utils.ControllerNameFromContext(ctx)
		require.True(t, ok)
		require.Equal(t, CIJobDiscoveryControllerName, name)
		close(discoveryEntered)
		<-ctx.Done()
		close(discoveryExited)
		return false, ctx.Err()
	}
	w.closeClients = func() {
		closes++
		select {
		case <-processingExited:
		default:
			t.Error("clients closed before processing exited")
		}
		select {
		case <-discoveryExited:
		default:
			t.Error("clients closed before discovery exited")
		}
	}
	w.queue.Add(testJobURI)
	w.discoveryQueue.Add(testJobURI)
	finished := make(chan struct{})
	go func() { w.Start(ctx); close(finished) }()
	for _, entered := range []chan struct{}{processingEntered, discoveryEntered} {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not start")
		}
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not join workers")
	}
	require.Equal(t, 1, initializations)
	require.Equal(t, 1, closes)
	require.Zero(t, w.queue.NumRequeues(testJobURI))
	require.Zero(t, w.discoveryQueue.NumRequeues(testJobURI))
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
		t.Fatal("transient initialization stopped Start")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("initialization retry ignored cancellation")
	}
}

func TestDiscoveryContinuesAcrossJobErrorsAndPanic(t *testing.T) {
	recoverPanics(t)
	w := newTestWriter(t)
	now := time.Now().UTC()
	jobs := []GCSJob{
		{Name: "periodic-ci-Azure-ARO-HCP-one", Prefix: "logs/periodic-ci-Azure-ARO-HCP-one/"},
		{Name: "periodic-ci-Azure-ARO-HCP-two", Prefix: "logs/periodic-ci-Azure-ARO-HCP-two/"},
		{Name: "periodic-ci-Azure-ARO-HCP-three", Prefix: "logs/periodic-ci-Azure-ARO-HCP-three/"},
	}
	w.jobCursor = func(_ context.Context, job GCSJob) (string, error) {
		switch job.Name {
		case jobs[0].Name:
			panic("cursor panic")
		case jobs[1].Name:
			return "", errors.New("query unavailable")
		default:
			return "", nil
		}
	}
	listedRoots := 0
	w.client.Transport = ingestionTransport(func(r *http.Request) (*http.Response, error) {
		body := `{}`
		prefix := r.URL.Query().Get("prefix")
		for _, root := range gcsJobRoots {
			if prefix == root.Prefix+root.Name {
				listedRoots++
			}
		}
		if prefix == "logs/periodic-ci-Azure-ARO-HCP-" {
			body = fmt.Sprintf(`{"prefixes":[%q,%q,%q]}`, jobs[0].Prefix, jobs[1].Prefix, jobs[2].Prefix)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	w.discover(testContext(t, w), now)
	require.Equal(t, 4, listedRoots)
	require.True(t, w.lastRepair[jobs[0].Prefix].IsZero())
	require.True(t, w.lastRepair[jobs[1].Prefix].IsZero())
	require.Equal(t, now, w.lastRepair[jobs[2].Prefix])
}
