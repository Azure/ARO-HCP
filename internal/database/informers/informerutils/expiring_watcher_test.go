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

package informerutils

import (
	"context"
	"net/http"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

// noopJitter is injected by these tests so watch expiry timings stay
// deterministic; production code uses defaultJitter.
func noopJitter(d time.Duration) time.Duration { return d }

func TestExpiringWatcher_SendsExpiredEvent(t *testing.T) {
	ctx := context.Background()
	w := newExpiringWatcher(ctx, 50*time.Millisecond, noopJitter)
	defer w.Stop()

	select {
	case evt, ok := <-w.ResultChan():
		if !ok {
			t.Fatal("ResultChan closed without sending an event")
		}
		if evt.Type != watch.Error {
			t.Fatalf("expected Error event type, got %v", evt.Type)
		}
		status, ok := evt.Object.(*metav1.Status)
		if !ok {
			t.Fatalf("expected *metav1.Status, got %T", evt.Object)
		}
		if status.Code != http.StatusGone {
			t.Fatalf("expected status code %d, got %d", http.StatusGone, status.Code)
		}
		if status.Reason != metav1.StatusReasonExpired {
			t.Fatalf("expected reason %q, got %q", metav1.StatusReasonExpired, status.Reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for expired event")
	}
}

func TestExpiringWatcher_ContextCancelDuringDelivery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Use a short expiry so the timer fires quickly, but never read from
	// ResultChan — this simulates the prior hang where the goroutine blocked
	// forever trying to send the expired event.
	w := newExpiringWatcher(ctx, 10*time.Millisecond, noopJitter)

	// Wait for the timer to fire and the goroutine to block on send.
	time.Sleep(50 * time.Millisecond)

	// Cancel the context — the goroutine should unblock and close ResultChan.
	cancel()

	select {
	case _, ok := <-w.ResultChan():
		if ok {
			t.Fatal("expected ResultChan to be closed, but received an event")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine did not exit after context cancellation — ResultChan never closed")
	}
}

func TestExpiringWatcher_StopBeforeExpiry(t *testing.T) {
	ctx := context.Background()
	w := newExpiringWatcher(ctx, 10*time.Minute, noopJitter)

	// Stop immediately — the goroutine should exit and close ResultChan.
	w.Stop()

	select {
	case _, ok := <-w.ResultChan():
		if ok {
			t.Fatal("expected ResultChan to be closed, but received an event")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine did not exit after Stop — ResultChan never closed")
	}
}

func TestExpiringWatcher_ContextCancelBeforeExpiry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	w := newExpiringWatcher(ctx, 10*time.Minute, noopJitter)

	cancel()

	select {
	case _, ok := <-w.ResultChan():
		if ok {
			t.Fatal("expected ResultChan to be closed, but received an event")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine did not exit after context cancellation — ResultChan never closed")
	}
}

func TestExpiringWatcher_StopDuringDelivery(t *testing.T) {
	ctx := context.Background()

	// Short expiry, never read from ResultChan.
	w := newExpiringWatcher(ctx, 10*time.Millisecond, noopJitter)

	// Wait for timer to fire and goroutine to block on send.
	time.Sleep(50 * time.Millisecond)

	// Stop should unblock the goroutine.
	w.Stop()

	select {
	case _, ok := <-w.ResultChan():
		if ok {
			t.Fatal("expected ResultChan to be closed, but received an event")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine did not exit after Stop — ResultChan never closed")
	}
}
