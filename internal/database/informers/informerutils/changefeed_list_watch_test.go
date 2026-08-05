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
	"errors"
	"testing"

	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestStopAndWaitForWatcherRespectsContext(t *testing.T) {
	watcher := &ChangeFeedWatcher[coreapi.HCPOpenShiftCluster, *coreapi.HCPOpenShiftCluster, struct{}]{
		done:     make(chan struct{}),
		finished: make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := stopAndWaitForWatcher(ctx, utilsclock.RealClock{}, watcher)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("stopAndWaitForWatcher() error = %v, want context.Canceled", err)
	}

	select {
	case <-watcher.done:
	default:
		t.Fatal("stopAndWaitForWatcher() did not signal the watcher to stop")
	}
}
