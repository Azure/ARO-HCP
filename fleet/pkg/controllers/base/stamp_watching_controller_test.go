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
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
)

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

func TestStampSyncerAdapterMakeKey(t *testing.T) {
	adapter := &genericStampSyncer{}
	tests := []struct {
		name       string
		resourceID string
		wantKey    string
	}{
		{
			name:       "stamp resource ID",
			resourceID: "/providers/microsoft.redhatopenshift/stamps/s1",
			wantKey:    "s1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rid, err := azcorearm.ParseResourceID(tt.resourceID)
			if err != nil {
				t.Fatalf("failed to parse resource ID: %v", err)
			}
			key := adapter.MakeKey(rid)
			if key.StampIdentifier != tt.wantKey {
				t.Errorf("got %q, want %q", key.StampIdentifier, tt.wantKey)
			}
		})
	}
}

type recordingStampSyncer struct {
	mu     sync.Mutex
	called []StampKey
}

func (s *recordingStampSyncer) SyncOnce(_ context.Context, key StampKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.called = append(s.called, key)
	return nil
}

func (s *recordingStampSyncer) calledKeys() []StampKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]StampKey{}, s.called...)
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

func testStamp(stampID string, etag azcore.ETag) *fleetapi.Stamp {
	rid, _ := fleetapi.ToStampResourceID(stampID)
	s := &fleetapi.Stamp{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: rid},
	}
	s.SetEtag(etag)
	return s
}

func TestStampQueueForInformers(t *testing.T) {
	tests := []struct {
		name    string
		fire    func(n *capturingStampNotifier)
		wantKey string
	}{
		{
			name: "add handler enqueues stamp",
			fire: func(n *capturingStampNotifier) {
				n.addFunc(testStamp("s1", "etag-1"))
			},
			wantKey: "s1",
		},
		{
			name: "add management cluster resolves stamp via parent walk",
			fire: func(n *capturingStampNotifier) {
				rid, _ := fleetapi.ToManagementClusterResourceID("s2")
				n.addFunc(&coreapi.CosmosMetadata{ResourceID: rid})
			},
			wantKey: "s2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syncer := &recordingStampSyncer{}
			controller := NewStampWatchingController("test", syncer, StampWatchingControllerConfig{})

			notifier := &capturingStampNotifier{}
			if err := controller.QueueForInformers(time.Minute, notifier); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			tt.fire(notifier)

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				defer close(done)
				controller.Run(ctx, 1)
			}()
			t.Cleanup(func() {
				cancel()
				<-done
			})

			require.Eventually(t, func() bool {
				return len(syncer.calledKeys()) > 0
			}, 5*time.Second, 10*time.Millisecond, "syncer should have been called")

			keys := syncer.calledKeys()
			require.Equal(t, tt.wantKey, keys[0].StampIdentifier)
		})
	}
}
