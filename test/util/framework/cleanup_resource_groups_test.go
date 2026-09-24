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

package framework

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunResourceGroupCleanupBoundsConcurrency(t *testing.T) {
	t.Parallel()

	resourceGroups := make([]string, 12)
	for i := range resourceGroups {
		resourceGroups[i] = fmt.Sprintf("resource-group-%d", i)
	}

	const concurrency = 3
	release := make(chan struct{})
	ready := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() {
		close(release)
	})
	var active atomic.Int64
	var maximum atomic.Int64
	var started atomic.Int64

	result := make(chan error, 1)
	go func() {
		result <- runResourceGroupCleanup(context.Background(), resourceGroups, concurrency, func(context.Context, string) error {
			current := active.Add(1)
			defer active.Add(-1)
			if started.Add(1) == concurrency {
				close(ready)
			}

			for {
				observed := maximum.Load()
				if current <= observed || maximum.CompareAndSwap(observed, current) {
					break
				}
			}

			<-release
			return nil
		})
	}()

	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cleanup workers to start")
	}
	releaseOnce.Do(func() {
		close(release)
	})

	if err := <-result; err != nil {
		t.Fatalf("expected cleanup to succeed: %v", err)
	}
	if got := maximum.Load(); got != concurrency {
		t.Fatalf("expected maximum concurrency %d, got %d", concurrency, got)
	}
}

func TestRunResourceGroupCleanupContinuesAfterFailures(t *testing.T) {
	t.Parallel()

	resourceGroups := []string{"one", "two", "three", "four"}
	var lock sync.Mutex
	attempted := map[string]bool{}

	err := runResourceGroupCleanup(context.Background(), resourceGroups, 2, func(_ context.Context, resourceGroupName string) error {
		lock.Lock()
		attempted[resourceGroupName] = true
		lock.Unlock()

		if resourceGroupName == "two" || resourceGroupName == "four" {
			return errors.New("cleanup failed")
		}
		return nil
	})

	if err == nil {
		t.Fatal("expected failed cleanups to be returned")
	}
	for _, resourceGroupName := range resourceGroups {
		if !attempted[resourceGroupName] {
			t.Errorf("expected resource group %q to be attempted", resourceGroupName)
		}
	}
	for _, resourceGroupName := range []string{"two", "four"} {
		if !strings.Contains(err.Error(), fmt.Sprintf("resource group %q", resourceGroupName)) {
			t.Errorf("expected error to identify resource group %q: %v", resourceGroupName, err)
		}
	}
}
