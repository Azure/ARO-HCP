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

package informers

import (
	"context"
	"sync"
	"testing"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

// newSubscriptionListerWatcher returns a cache.ListerWatcher that lists subscriptions
// from the given database client and watches using an expiring watcher.
func newSubscriptionListerWatcher(dbClient database.DBClient, watchExpiry time.Duration) cache.ListerWatcher {
	return &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			iter, err := dbClient.Subscriptions().List(ctx, nil)
			if err != nil {
				return nil, err
			}

			list := &arm.SubscriptionList{}
			list.ResourceVersion = "0"
			for _, sub := range iter.Items(ctx) {
				list.Items = append(list.Items, *sub)
			}
			if err := iter.GetError(); err != nil {
				return nil, err
			}

			return list, nil
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			return NewExpiringWatcher(watchExpiry), nil
		},
	}
}

func mustParseResourceID(t *testing.T, id string) *azcorearm.ResourceID {
	t.Helper()
	rid, err := azcorearm.ParseResourceID(id)
	require.NoError(t, err)
	return rid
}

func newTestSubscription(t *testing.T, subID string, state arm.SubscriptionState) *arm.Subscription {
	t.Helper()
	return &arm.Subscription{
		ResourceID: mustParseResourceID(t, "/subscriptions/"+subID),
		State:      state,
	}
}

// eventTracker records informer events in a thread-safe way.
type eventTracker struct {
	mu      sync.Mutex
	added   []*arm.Subscription
	updated []*arm.Subscription
	deleted []*arm.Subscription
}

func (e *eventTracker) onAdd(obj interface{}) {
	sub := obj.(*arm.Subscription)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.added = append(e.added, sub)
}

func (e *eventTracker) onUpdate(oldObj, newObj interface{}) {
	sub := newObj.(*arm.Subscription)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.updated = append(e.updated, sub)
}

func (e *eventTracker) onDelete(obj interface{}) {
	if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = d.Obj
	}
	sub := obj.(*arm.Subscription)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.deleted = append(e.deleted, sub)
}

func (e *eventTracker) getAdded() []*arm.Subscription {
	e.mu.Lock()
	defer e.mu.Unlock()
	ret := make([]*arm.Subscription, len(e.added))
	copy(ret, e.added)
	return ret
}

func (e *eventTracker) getUpdated() []*arm.Subscription {
	e.mu.Lock()
	defer e.mu.Unlock()
	ret := make([]*arm.Subscription, len(e.updated))
	copy(ret, e.updated)
	return ret
}

func (e *eventTracker) getDeleted() []*arm.Subscription {
	e.mu.Lock()
	defer e.mu.Unlock()
	ret := make([]*arm.Subscription, len(e.deleted))
	copy(ret, e.deleted)
	return ret
}

func TestSharedInformerSubscriptionEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	mockDB := databasetesting.NewMockDBClient()

	// Seed the database with two initial subscriptions.
	sub1 := newTestSubscription(t, "sub-1", arm.SubscriptionStateRegistered)
	sub2 := newTestSubscription(t, "sub-2", arm.SubscriptionStateRegistered)
	_, err := mockDB.Subscriptions().Create(ctx, sub1, nil)
	require.NoError(t, err)
	_, err = mockDB.Subscriptions().Create(ctx, sub2, nil)
	require.NoError(t, err)

	// Use a short watch expiry so the reflector relists quickly.
	watchExpiry := 1 * time.Second

	lw := newSubscriptionListerWatcher(mockDB, watchExpiry)

	informer := cache.NewSharedIndexInformerWithOptions(
		lw,
		&arm.Subscription{},
		cache.SharedIndexInformerOptions{
			// No resync: we rely on watch expiry for relisting.
			ResyncPeriod: 0,
		},
	)

	tracker := &eventTracker{}
	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    tracker.onAdd,
		UpdateFunc: tracker.onUpdate,
		DeleteFunc: tracker.onDelete,
	})
	require.NoError(t, err)

	go informer.Run(ctx.Done())

	// Wait for the initial sync to complete.
	require.True(t, cache.WaitForCacheSync(ctx.Done(), informer.HasSynced), "timed out waiting for cache sync")

	// After initial list, we should have two Add events.
	require.Eventually(t, func() bool {
		return len(tracker.getAdded()) == 2
	}, 5*time.Second, 100*time.Millisecond, "expected 2 add events from initial list")
	require.Empty(t, tracker.getUpdated(), "expected no update events after initial list")
	require.Empty(t, tracker.getDeleted(), "expected no delete events after initial list")

	// Now mutate the database:
	// 1. Update sub-1 state to Warned.
	sub1Updated := newTestSubscription(t, "sub-1", arm.SubscriptionStateWarned)
	_, err = mockDB.Subscriptions().Replace(ctx, sub1Updated, nil)
	require.NoError(t, err)

	// 2. Add a new subscription sub-3.
	sub3 := newTestSubscription(t, "sub-3", arm.SubscriptionStateRegistered)
	_, err = mockDB.Subscriptions().Create(ctx, sub3, nil)
	require.NoError(t, err)

	// 3. Delete sub-2.
	err = mockDB.Subscriptions().Delete(ctx, "sub-2")
	require.NoError(t, err)

	// Wait for the watcher to expire and the reflector to relist, which will
	// trigger OnUpdate for sub-1, OnAdd for sub-3, and OnDelete for sub-2.
	require.Eventually(t, func() bool {
		return len(tracker.getUpdated()) >= 1
	}, 10*time.Second, 100*time.Millisecond, "expected update event for sub-1 after relist")

	require.Eventually(t, func() bool {
		return len(tracker.getAdded()) >= 3
	}, 10*time.Second, 100*time.Millisecond, "expected add event for sub-3 after relist")

	require.Eventually(t, func() bool {
		return len(tracker.getDeleted()) >= 1
	}, 10*time.Second, 100*time.Millisecond, "expected delete event for sub-2 after relist")

	// Verify the specific events.
	updated := tracker.getUpdated()
	require.GreaterOrEqual(t, len(updated), 1)
	found := false
	for _, u := range updated {
		if u.ResourceID.SubscriptionID == "sub-1" && u.State == arm.SubscriptionStateWarned {
			found = true
			break
		}
	}
	require.True(t, found, "expected sub-1 to be updated with state Warned")

	deleted := tracker.getDeleted()
	require.GreaterOrEqual(t, len(deleted), 1)
	found = false
	for _, d := range deleted {
		if d.ResourceID.SubscriptionID == "sub-2" {
			found = true
			break
		}
	}
	require.True(t, found, "expected sub-2 to be deleted")
}
