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

package controllerutils

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"k8s.io/client-go/util/workqueue"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-HCP/internal/utils"
)

type typedControllerKey struct {
	Name string
}

func (k typedControllerKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues("key_name", k.Name)
}

func TestTypedControllerSyncOnce(t *testing.T) {
	wantKey := typedControllerKey{Name: "direct"}
	wantErr := errors.New("sync failed")
	ctx := utils.ContextWithLogger(t.Context(), logr.Discard())
	reconcileTotal := testReconcileTotal()
	c := NewTypedController("direct", func(gotCtx context.Context, key typedControllerKey) error {
		require.Same(t, ctx, gotCtx)
		require.Empty(t, cmp.Diff(wantKey, key))
		return wantErr
	}, reconcileTotal)
	t.Cleanup(c.queue.ShutDown)

	require.ErrorIs(t, c.SyncOnce(ctx, wantKey), wantErr)
	require.Zero(t, c.queue.Len())
	require.Zero(t, c.queue.NumRequeues(wantKey))
	require.Zero(t, testutil.ToFloat64(reconcileTotal.WithLabelValues("direct")))
}

func TestTypedControllerQueue(t *testing.T) {
	c := NewTypedController("queue", func(context.Context, typedControllerKey) error { return nil }, nil)
	c.queue.ShutDown()
	clock := clocktesting.NewFakeClock(time.Unix(123, 0))
	c.queue = workqueue.NewTypedRateLimitingQueueWithConfig(
		workqueue.DefaultTypedControllerRateLimiter[typedControllerKey](),
		workqueue.TypedRateLimitingQueueConfig[typedControllerKey]{Clock: clock},
	)
	t.Cleanup(c.queue.ShutDown)
	key := typedControllerKey{Name: "queued"}

	c.Enqueue(key)
	c.Enqueue(key)
	require.Equal(t, 1, c.queue.Len())
	got, shutdown := c.queue.Get()
	require.False(t, shutdown)
	require.Empty(t, cmp.Diff(key, got))
	c.Enqueue(key)
	c.Enqueue(key)
	require.Zero(t, c.queue.Len(), "an in-flight key must not be processed concurrently")
	c.queue.Done(got)
	require.Equal(t, 1, c.queue.Len())
	got, shutdown = c.queue.Get()
	require.False(t, shutdown)
	require.Empty(t, cmp.Diff(key, got))
	c.queue.Done(got)
	require.Zero(t, c.queue.Len())
	require.Zero(t, c.queue.NumRequeues(key))

	c.EnqueueAfter(key, time.Second)
	// The delaying queue has one heartbeat ticker and one pending-item timer.
	require.Eventually(t, func() bool { return clock.Waiters() == 2 }, 5*time.Second, time.Millisecond)
	clock.Step(time.Second - time.Nanosecond)
	require.Zero(t, c.queue.Len())
	clock.Step(time.Nanosecond)
	require.Eventually(t, func() bool { return c.queue.Len() == 1 }, 5*time.Second, time.Millisecond)
	got, shutdown = c.queue.Get()
	require.False(t, shutdown)
	require.Empty(t, cmp.Diff(key, got))
	c.queue.Done(got)
	require.Zero(t, c.queue.NumRequeues(key))

	c.EnqueueAfter(key, 0)
	require.Equal(t, 1, c.queue.Len())
}

func TestTypedControllerRetryAndForget(t *testing.T) {
	key := typedControllerKey{Name: "retry"}
	syncErr := errors.New("retry me")
	attempts := 0
	reconcileTotal := testReconcileTotal()
	c := NewTypedController("retry", func(_ context.Context, got typedControllerKey) error {
		require.Empty(t, cmp.Diff(key, got))
		attempts++
		return syncErr
	}, reconcileTotal)
	c.queue.ShutDown()
	clock := clocktesting.NewFakeClock(time.Unix(123, 0))
	c.queue = workqueue.NewTypedRateLimitingQueueWithConfig(
		workqueue.NewTypedItemExponentialFailureRateLimiter[typedControllerKey](time.Second, time.Minute),
		workqueue.TypedRateLimitingQueueConfig[typedControllerKey]{Clock: clock},
	)
	t.Cleanup(c.queue.ShutDown)
	ctx := utils.ContextWithLogger(t.Context(), logr.Discard())
	c.Enqueue(key)

	for attempt := 1; attempt <= 2; attempt++ {
		require.True(t, c.processNextWorkItem(ctx))
		require.Equal(t, attempt, c.queue.NumRequeues(key))
		require.Equal(t, float64(attempt), testutil.ToFloat64(reconcileTotal.WithLabelValues("retry")))
		require.Eventually(t, func() bool { return clock.Waiters() == 2 }, 5*time.Second, time.Millisecond)
		delay := time.Duration(attempt) * time.Second
		clock.Step(delay - time.Nanosecond)
		require.Zero(t, c.queue.Len())
		clock.Step(time.Nanosecond)
		require.Eventually(t, func() bool { return c.queue.Len() == 1 }, 5*time.Second, time.Millisecond)
	}

	syncErr = nil
	require.True(t, c.processNextWorkItem(ctx))
	require.Equal(t, 3, attempts)
	require.Zero(t, c.queue.NumRequeues(key))
	require.Zero(t, c.queue.Len())
	require.Equal(t, float64(3), testutil.ToFloat64(reconcileTotal.WithLabelValues("retry")))
	c.queue.ShutDown()
	require.False(t, c.processNextWorkItem(ctx))
	require.Equal(t, 3, attempts)
}

func TestTypedControllerWorkerContext(t *testing.T) {
	const name = "typed-worker"
	logs := make(chan string, 2)
	logger := funcr.NewJSON(func(obj string) {
		if strings.Contains(obj, `"msg":"reconcile"`) {
			logs <- obj
		}
	}, funcr.Options{})
	ctx, cancel := context.WithCancel(utils.ContextWithLogger(t.Context(), logger))
	t.Cleanup(cancel)
	type observation struct {
		Key            typedControllerKey
		ControllerName string
		HasName        bool
	}
	observations := make(chan observation, 2)
	c := NewTypedController(name, func(ctx context.Context, key typedControllerKey) error {
		controllerName, ok := utils.ControllerNameFromContext(ctx)
		utils.LoggerFromContext(ctx).Info("reconcile")
		observations <- observation{Key: key, ControllerName: controllerName, HasName: ok}
		return nil
	}, nil)
	keys := []typedControllerKey{{Name: "first"}, {Name: "second"}}
	for _, key := range keys {
		c.Enqueue(key)
	}
	done := make(chan struct{})
	go func() {
		c.Run(ctx, 1)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Run did not return after cancellation")
		}
	})

	for _, key := range keys {
		select {
		case got := <-observations:
			require.Empty(t, cmp.Diff(observation{Key: key, ControllerName: name, HasName: true}, got))
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not reconcile")
		}
		logLine := <-logs
		require.Equal(t, 1, strings.Count(logLine, `"key_name"`), "key fields must not accumulate across reconciliations")
		var fields map[string]any
		require.NoError(t, json.Unmarshal([]byte(logLine), &fields))
		require.Empty(t, cmp.Diff(map[string]any{
			"logger": "", "level": float64(0), "msg": "reconcile",
			"controller_name": name, "key_name": key.Name,
		}, fields))
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	require.True(t, c.queue.ShuttingDown())
}

func TestTypedControllerCacheSync(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel while waiting", true: "wait before working"}[ready], func(t *testing.T) {
			ctx, cancel := context.WithCancel(utils.ContextWithLogger(t.Context(), logr.Discard()))
			t.Cleanup(cancel)
			var synced atomic.Bool
			checked := make(chan struct{}, 1)
			called := make(chan struct{}, 1)
			c := NewTypedController("cache", func(context.Context, string) error {
				called <- struct{}{}
				return nil
			}, nil)
			c.AddCacheSyncs(func() bool {
				select {
				case checked <- struct{}{}:
				default:
				}
				return synced.Load()
			})
			c.Enqueue("key")
			done := make(chan struct{})
			go func() {
				c.Run(ctx, 1)
				close(done)
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("Run did not return after cancellation")
				}
			})
			select {
			case <-checked:
			case <-time.After(5 * time.Second):
				t.Fatal("cache sync was not checked")
			}
			require.Equal(t, 1, c.queue.Len())
			require.Empty(t, called)
			if ready {
				synced.Store(true)
				select {
				case <-called:
				case <-time.After(5 * time.Second):
					t.Fatal("worker did not start after cache sync")
				}
			}
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Run did not return after cancellation")
			}
			require.True(t, c.queue.ShuttingDown())
			require.Empty(t, called)
		})
	}
}
