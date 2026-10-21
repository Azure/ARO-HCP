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

package base

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/internal/api"
	fleetapi "github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/controllerutils"
)

type alwaysAllowCooldown struct{}

func (alwaysAllowCooldown) CanSync(context.Context, any) bool {
	return true
}

type neverAllowCooldown struct{}

func (neverAllowCooldown) CanSync(context.Context, any) bool {
	return false
}

func TestStampKeyFromObject(t *testing.T) {
	tests := []struct {
		name      string
		obj       any
		wantKey   StampKey
		wantError bool
	}{
		{
			name: "Stamp",
			obj: func() *fleetapi.Stamp {
				rid, _ := fleetapi.ToStampResourceID("abc")
				return &fleetapi.Stamp{
					CosmosMetadata: api.CosmosMetadata{ResourceID: rid},
				}
			}(),
			wantKey: StampKey{StampIdentifier: "abc"},
		},
		{
			name: "ManagementCluster",
			obj: func() *fleetapi.ManagementCluster {
				rid, _ := fleetapi.ToManagementClusterResourceID("xyz")
				return &fleetapi.ManagementCluster{
					CosmosMetadata: api.CosmosMetadata{ResourceID: rid},
				}
			}(),
			wantKey: StampKey{StampIdentifier: "xyz"},
		},
		{
			name:      "wrong type",
			obj:       "not a fleet object",
			wantError: true,
		},
		{
			name: "Stamp with nil resource ID",
			obj: &fleetapi.Stamp{
				CosmosMetadata: api.CosmosMetadata{ResourceID: nil},
			},
			wantError: true,
		},
		{
			name: "ManagementCluster with nil resource ID",
			obj: &fleetapi.ManagementCluster{
				CosmosMetadata: api.CosmosMetadata{ResourceID: nil},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, err := StampKeyFromObject(tt.obj)
			if tt.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if key != tt.wantKey {
				t.Errorf("got key %v, want %v", key, tt.wantKey)
			}
		})
	}
}

func TestStampKeyGetResourceID(t *testing.T) {
	key := StampKey{StampIdentifier: "s1"}
	rid := key.GetResourceID()
	if rid == nil {
		t.Fatal("expected non-nil resource ID")
	}
	want := "/providers/microsoft.redhatopenshift/stamps/s1"
	if rid.String() != want {
		t.Errorf("got %q, want %q", rid.String(), want)
	}
}

func testStamp(stampID string, etag azcore.ETag) *fleetapi.Stamp {
	rid, _ := fleetapi.ToStampResourceID(stampID)
	s := &fleetapi.Stamp{
		CosmosMetadata: api.CosmosMetadata{ResourceID: rid},
	}
	s.SetEtag(etag)
	return s
}

func TestStampHandleAdd(t *testing.T) {
	tests := []struct {
		name         string
		obj          any
		wantQueueLen int
		wantKey      string
	}{
		{
			name:         "Stamp enqueues",
			obj:          testStamp("s1", "etag-1"),
			wantQueueLen: 1,
			wantKey:      "s1",
		},
		{
			name: "ManagementCluster enqueues",
			obj: func() *fleetapi.ManagementCluster {
				rid, _ := fleetapi.ToManagementClusterResourceID("s2")
				return &fleetapi.ManagementCluster{
					CosmosMetadata: api.CosmosMetadata{ResourceID: rid},
				}
			}(),
			wantQueueLen: 1,
			wantKey:      "s2",
		},
		{
			name:         "invalid object does not enqueue",
			obj:          "not a fleet object",
			wantQueueLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[StampKey]())
			defer queue.ShutDown()

			controller := &StampWatchingController{queue: queue}
			controller.handleAdd(tt.obj)

			if queue.Len() != tt.wantQueueLen {
				t.Fatalf("expected queue length %d, got %d", tt.wantQueueLen, queue.Len())
			}
			if tt.wantQueueLen > 0 {
				key, _ := queue.Get()
				if key.StampIdentifier != tt.wantKey {
					t.Errorf("got %q, want %q", key.StampIdentifier, tt.wantKey)
				}
			}
		})
	}
}

func TestStampHandleUpdate(t *testing.T) {
	tests := []struct {
		name         string
		oldObj       any
		newObj       any
		cooldown     controllerutils.CooldownChecker
		wantQueueLen int
	}{
		{
			name:         "etag changed enqueues despite cooldown rejection",
			oldObj:       testStamp("s1", "etag-1"),
			newObj:       testStamp("s1", "etag-2"),
			cooldown:     neverAllowCooldown{},
			wantQueueLen: 1,
		},
		{
			name:         "etag unchanged and cooldown allows enqueues",
			oldObj:       testStamp("s1", "etag-1"),
			newObj:       testStamp("s1", "etag-1"),
			cooldown:     alwaysAllowCooldown{},
			wantQueueLen: 1,
		},
		{
			name:         "etag unchanged and cooldown rejects does not enqueue",
			oldObj:       testStamp("s1", "etag-1"),
			newObj:       testStamp("s1", "etag-1"),
			cooldown:     neverAllowCooldown{},
			wantQueueLen: 0,
		},
		{
			name:         "invalid objects do not enqueue",
			oldObj:       "not a stamp",
			newObj:       "also not a stamp",
			cooldown:     alwaysAllowCooldown{},
			wantQueueLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[StampKey]())
			defer queue.ShutDown()

			controller := &StampWatchingController{queue: queue, cooldown: tt.cooldown}
			controller.handleUpdate(tt.oldObj, tt.newObj)

			if queue.Len() != tt.wantQueueLen {
				t.Fatalf("expected queue length %d, got %d", tt.wantQueueLen, queue.Len())
			}
		})
	}
}

type fakeStampSyncer struct {
	called []StampKey
	err    error
}

func (f *fakeStampSyncer) SyncOnce(_ context.Context, key StampKey) error {
	f.called = append(f.called, key)
	return f.err
}

func TestStampProcessNext(t *testing.T) {
	tests := []struct {
		name           string
		syncErr        error
		wantContinue   bool
		wantSyncCalled int
	}{
		{
			name:           "success returns true",
			syncErr:        nil,
			wantContinue:   true,
			wantSyncCalled: 1,
		},
		{
			name:           "error requeues and returns true",
			syncErr:        errors.New("sync failed"),
			wantContinue:   true,
			wantSyncCalled: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[StampKey]())
			defer queue.ShutDown()

			syncer := &fakeStampSyncer{err: tt.syncErr}
			controller := &StampWatchingController{
				name:   "test",
				syncer: syncer,
				queue:  queue,
			}

			queue.Add(StampKey{StampIdentifier: "s1"})

			ok := controller.processNext(context.Background())
			if ok != tt.wantContinue {
				t.Fatalf("expected processNext to return %v, got %v", tt.wantContinue, ok)
			}
			if len(syncer.called) != tt.wantSyncCalled {
				t.Fatalf("expected %d SyncOnce calls, got %d", tt.wantSyncCalled, len(syncer.called))
			}
		})
	}
}

func TestStampProcessNextShutdownReturnsFalse(t *testing.T) {
	queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[StampKey]())
	queue.ShutDown()

	syncer := &fakeStampSyncer{}
	controller := &StampWatchingController{
		name:   "test",
		syncer: syncer,
		queue:  queue,
	}

	ok := controller.processNext(context.Background())
	if ok {
		t.Fatal("expected processNext to return false on shutdown")
	}
	if len(syncer.called) != 0 {
		t.Fatalf("expected no SyncOnce calls on shutdown, got %d", len(syncer.called))
	}
}

// capturingStampNotifier captures the event handler functions registered via AddEventHandlerWithOptions.
type capturingStampNotifier struct {
	addFunc    func(any)
	updateFunc func(any, any)
}

func (n *capturingStampNotifier) AddEventHandlerWithOptions(handler cache.ResourceEventHandler, opts cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error) {
	f, ok := handler.(cache.ResourceEventHandlerFuncs)
	if !ok {
		return nil, fmt.Errorf("expected ResourceEventHandlerFuncs, got %T", handler)
	}
	n.addFunc = f.AddFunc
	n.updateFunc = f.UpdateFunc
	return nil, nil
}

func TestStampQueueForInformers(t *testing.T) {
	tests := []struct {
		name         string
		run          func(t *testing.T, c *StampWatchingController, n *capturingStampNotifier)
		wantQueueLen int
		wantKey      string
	}{
		{
			name: "Add handler enqueues object",
			run: func(t *testing.T, c *StampWatchingController, n *capturingStampNotifier) {
				if n.addFunc == nil {
					t.Fatal("expected addFunc to be registered")
				}
				n.addFunc(testStamp("s1", "etag-1"))
			},
			wantQueueLen: 1,
			wantKey:      "s1",
		},
		{
			name: "Update handler enqueues on etag change",
			run: func(t *testing.T, c *StampWatchingController, n *capturingStampNotifier) {
				if n.updateFunc == nil {
					t.Fatal("expected updateFunc to be registered")
				}
				n.updateFunc(
					testStamp("s1", "etag-a"),
					testStamp("s1", "etag-b"),
				)
			},
			wantQueueLen: 1,
			wantKey:      "s1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := NewStampWatchingController("test", &fakeStampSyncer{}, StampWatchingControllerConfig{})
			controller.cooldown = alwaysAllowCooldown{}

			n := &capturingStampNotifier{}
			if err := controller.QueueForInformers(time.Minute, n); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			tt.run(t, controller, n)

			if controller.queue.Len() != tt.wantQueueLen {
				t.Fatalf("expected queue length %d, got %d", tt.wantQueueLen, controller.queue.Len())
			}
			if tt.wantQueueLen > 0 {
				key, _ := controller.queue.Get()
				if key.StampIdentifier != tt.wantKey {
					t.Errorf("got %q, want %q", key.StampIdentifier, tt.wantKey)
				}
			}
		})
	}
}
