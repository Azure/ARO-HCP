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

package databasetesting

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/database"
)

// MockLockClient implements database.LockClientInterface for testing.
type MockLockClient struct {
	defaultTTL time.Duration
	locks      map[string]bool
	mu         sync.Mutex
}

// NewMockLockClient creates a new mock lock client.
func NewMockLockClient(defaultTTL time.Duration) *MockLockClient {
	return &MockLockClient{
		defaultTTL: defaultTTL,
		locks:      make(map[string]bool),
	}
}

func (c *MockLockClient) GetDefaultTimeToLive() time.Duration {
	return c.defaultTTL
}

func (c *MockLockClient) SetRetryAfterHeader(header http.Header) {
	header.Set("Retry-After", fmt.Sprintf("%d", int(c.defaultTTL.Seconds())))
}

func (c *MockLockClient) AcquireLock(ctx context.Context, id string, timeout *time.Duration) (*azcosmos.ItemResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.locks[id] {
		return nil, nil
	}
	c.locks[id] = true
	return &azcosmos.ItemResponse{}, nil
}

func (c *MockLockClient) TryAcquireLock(ctx context.Context, id string) (*azcosmos.ItemResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.locks[id] {
		return nil, nil
	}
	c.locks[id] = true
	return &azcosmos.ItemResponse{}, nil
}

func (c *MockLockClient) HoldLock(ctx context.Context, item *azcosmos.ItemResponse) (context.Context, database.StopHoldLock) {
	cancelCtx, cancel := context.WithCancel(ctx)
	return cancelCtx, func() *azcosmos.ItemResponse {
		cancel()
		return item
	}
}

func (c *MockLockClient) RenewLock(ctx context.Context, item *azcosmos.ItemResponse) (*azcosmos.ItemResponse, error) {
	return item, nil
}

func (c *MockLockClient) ReleaseLock(ctx context.Context, item *azcosmos.ItemResponse) error {
	return nil
}

var _ database.LockClientInterface = &MockLockClient{}

// MockLocksDBClient implements database.LocksDBClient for unit testing.
type MockLocksDBClient struct {
	mu   sync.RWMutex
	lock database.LockClientInterface
}

// NewMockLocksDBClient returns a LocksDBClient backed by an in-memory lock implementation.
func NewMockLocksDBClient() *MockLocksDBClient {
	return &MockLocksDBClient{
		lock: NewMockLockClient(10),
	}
}

// SetLockClient replaces the lock implementation (e.g. for middleware tests).
func (m *MockLocksDBClient) SetLockClient(lock database.LockClientInterface) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lock = lock
}

// LockClient returns the configured lock client, or nil if unset.
func (m *MockLocksDBClient) LockClient() database.LockClientInterface {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lock
}

var _ database.LocksDBClient = &MockLocksDBClient{}
