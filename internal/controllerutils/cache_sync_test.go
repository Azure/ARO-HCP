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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

type recordingCacheSyncer struct {
	calls chan string
}

func (syncer *recordingCacheSyncer) MakeKey(resourceID *azcorearm.ResourceID) string {
	return resourceID.String()
}
func (syncer *recordingCacheSyncer) CooldownChecker() CooldownChecker { return nil }
func (syncer *recordingCacheSyncer) SyncOnce(_ context.Context, key string) error {
	syncer.calls <- key
	return nil
}

func TestWatchingControllerWaitsForEveryCache(t *testing.T) {
	for _, cancelBeforeSync := range []bool{false, true} {
		t.Run(map[bool]string{false: "delayed-cache", true: "cancel-before-sync"}[cancelBeforeSync], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			controller, clusterID, _ := newTestWatchingController()
			calls := make(chan string, 1)
			controller.syncer = &recordingCacheSyncer{calls: calls}
			var sourceReady, dependencyReady, observed atomic.Bool
			controller.AddCacheSyncs(sourceReady.Load, func() bool {
				observed.Store(true)
				return dependencyReady.Load()
			})
			controller.EnqueueResourceIDAdd(clusterID, true)
			done := make(chan struct{})
			go func() { defer close(done); controller.Run(ctx, 1) }()
			sourceReady.Store(true)
			require.Eventually(t, observed.Load, 5*time.Second, time.Millisecond)
			require.Empty(t, calls, "the enqueueing cache alone cannot release workers")
			if !cancelBeforeSync {
				dependencyReady.Store(true)
				select {
				case key := <-calls:
					require.Equal(t, clusterID.String(), key)
				case <-time.After(5 * time.Second):
					t.Fatal("worker did not start after all caches synced")
				}
			}
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("cache wait ignored cancellation")
			}
			require.True(t, controller.queue.ShuttingDown())
			require.Empty(t, calls)
		})
	}
}
