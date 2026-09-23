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
	"runtime"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

// fakeChangeFeedClient is a minimal cosmosstorageutils.ChangeFeedClient for the
// expiry-timer test. It reports zero feed ranges so ChangeFeedWatcher.Run spawns
// no feed-range reader goroutines — those drive their poll loop with the
// real-time wait.UntilWithContext timer, which would defeat the fake clock. With
// no feed ranges the only remaining goroutine is the expiry timer, which uses the
// injected clock and can therefore be driven deterministically.
type fakeChangeFeedClient struct{}

func (fakeChangeFeedClient) ReadFeedRanges(ctx context.Context, options *azcosmos.FeedRangesOptions) ([]azcosmos.FeedRange, error) {
	return nil, nil
}

func (fakeChangeFeedClient) ReadChangeFeed(ctx context.Context, options *azcosmos.ChangeFeedOptions) (azcosmos.ChangeFeedResponse, error) {
	// Never reached in this test: with no feed ranges, no reader calls this.
	return azcosmos.ChangeFeedResponse{}, nil
}

// waitForClockWaiter blocks until at least one waiter is registered on the fake
// clock (i.e. the expiry goroutine has reached its clock.After call), yielding
// the scheduler rather than sleeping so the expiry stays driven purely by the
// fake clock. The real-time deadline is only a test-failure guard, not a
// synchronization mechanism for the expiry itself.
func waitForClockWaiter(t *testing.T, fakeClock *clocktesting.FakeClock) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !fakeClock.HasWaiters() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the expiry timer to register on the fake clock")
		}
		runtime.Gosched()
	}
}

// TestChangeFeedWatcher_ExpiryTimerTriggersRelist covers the jittered watch-expiry
// timer added in changefeed_list_watch.go. Using a fake clock and a deterministic
// jitterFn set directly on the private field (jitterFn is intentionally not an
// exported option — same-package tests inject it this way), it asserts that:
//
//  1. the expiry honors the jittered duration (no relist before jitterFn(maxWatchDuration)); and
//  2. once the fake clock advances past that deadline the watcher emits a
//     watch.Error carrying an HTTP 410 Gone / StatusReasonExpired status, which is
//     exactly the event the Reflector consumes to trigger a relist.
//
// The whole timeline is driven by fakeClock.Step — no real time.Sleep is used to
// reach expiry — so the test is deterministic and race-free.
func TestChangeFeedWatcher_ExpiryTimerTriggersRelist(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const (
		maxWatchDuration = 30 * time.Minute
		// fixedJitter is a deterministic, additive jitter (matching production's
		// additive-only defaultJitter) so the expiry fires at a precisely known
		// time — maxWatchDuration + fixedJitter — under the fake clock.
		fixedJitter = 5 * time.Minute
	)

	fakeClock := clocktesting.NewFakeClock(time.Now())

	// Construct via the same-package unexported constructor. Passing nil defaults
	// jitterFn to defaultJitter, exactly as production does.
	w := newChangeFeedWatcher[coreapi.HCPOpenShiftCluster, *coreapi.HCPOpenShiftCluster, cosmosstorageutils.GenericDocument[coreapi.HCPOpenShiftCluster]](
		nil,       // desiredResourceTypes: unused, no items are delivered
		fakeClock, // clock: drives the expiry timer deterministically
		fakeChangeFeedClient{},
		fakeClock.Now(), // startFrom
		maxWatchDuration,
		nil, // shouldDeliverItemFn: unused
		"",  // cosmosContainerName: suppress objectMetadata
		nil, // jitterFn: overridden on the private field just below
	)
	// deads2k's design: jitterFn is deliberately a private field with no exported
	// setter, so same-package unit tests inject a deterministic value directly.
	// Set it before Run starts so the expiry goroutine reads it race-free.
	w.jitterFn = func(d time.Duration) time.Duration { return d + fixedJitter }

	go w.Run(ctx)
	t.Cleanup(func() {
		w.Stop()
		// Wait for Run and its children to fully return before the test ends so
		// no goroutine logs through a torn-down test context.
		<-w.Finished()
	})

	// Ensure the expiry goroutine has registered its clock.After waiter before we
	// advance the clock; otherwise Step would advance past a not-yet-registered
	// deadline and the timer would never fire.
	waitForClockWaiter(t, fakeClock)

	// Advancing to the un-jittered base duration must NOT expire the watch: the
	// jittered deadline is maxWatchDuration + fixedJitter. This proves jitterFn is
	// actually applied to maxWatchDuration.
	fakeClock.Step(maxWatchDuration)
	select {
	case evt := <-w.ResultChan():
		t.Fatalf("watch expired before the jittered deadline; unexpected event: %+v", evt)
	default:
	}

	// Cross the jittered deadline. The expiry goroutine should now emit the relist
	// signal.
	fakeClock.Step(fixedJitter + time.Minute)

	select {
	case evt, ok := <-w.ResultChan():
		if !ok {
			t.Fatal("ResultChan closed before delivering the expiry event")
		}
		if evt.Type != watch.Error {
			t.Fatalf("event Type = %v, want %v (watch.Error)", evt.Type, watch.Error)
		}
		status, ok := evt.Object.(*metav1.Status)
		if !ok {
			t.Fatalf("event Object = %T, want *metav1.Status", evt.Object)
		}
		if status.Code != http.StatusGone {
			t.Errorf("status.Code = %d, want %d (Gone)", status.Code, http.StatusGone)
		}
		if status.Reason != metav1.StatusReasonExpired {
			t.Errorf("status.Reason = %q, want %q", status.Reason, metav1.StatusReasonExpired)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the watch to expire and emit a relist signal")
	}
}
