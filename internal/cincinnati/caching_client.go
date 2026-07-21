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

package cincinnati

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/blang/semver/v4"

	utilsclock "k8s.io/utils/clock"

	configv1 "github.com/openshift/api/config/v1"
)

var _ Client = (*CachingClient)(nil)

type cacheEntry struct {
	current            configv1.Release
	updates            []configv1.Release
	conditionalUpdates []configv1.ConditionalUpdate
	err                error
	expiresAt          time.Time
}

// CachingClient wraps a Client and caches GetUpdates responses for a
// configurable TTL. The cache key is derived from the URI, architecture,
// channel, and version parameters — identical queries return cached results
// without hitting the underlying client.
type CachingClient struct {
	inner Client
	clock utilsclock.PassiveClock
	ttl   time.Duration
	mu    sync.RWMutex
	cache map[string]*cacheEntry
}

// NewCachingClient returns a Client that caches responses from inner for ttl.
// The clock is used to determine expiry, allowing tests to use a fake clock.
func NewCachingClient(inner Client, clock utilsclock.PassiveClock, ttl time.Duration) *CachingClient {
	return &CachingClient{
		inner: inner,
		clock: clock,
		ttl:   ttl,
		cache: make(map[string]*cacheEntry),
	}
}

func (c *CachingClient) GetUpdates(ctx context.Context, uri *url.URL, desiredArch, currentArch, channel string, version semver.Version) (configv1.Release, []configv1.Release, []configv1.ConditionalUpdate, error) {
	key := fmt.Sprintf("%s://%s%s|%s|%s|%s|%s", uri.Scheme, uri.Host, uri.Path, desiredArch, currentArch, channel, version)

	c.mu.RLock()
	if entry, ok := c.cache[key]; ok && c.clock.Now().Before(entry.expiresAt) {
		c.mu.RUnlock()
		return entry.current, entry.updates, entry.conditionalUpdates, entry.err
	}
	c.mu.RUnlock()

	uriClone := *uri
	current, updates, conditionalUpdates, err := c.inner.GetUpdates(ctx, &uriClone, desiredArch, currentArch, channel, version)

	c.mu.Lock()
	c.cache[key] = &cacheEntry{
		current:            current,
		updates:            updates,
		conditionalUpdates: conditionalUpdates,
		err:                err,
		expiresAt:          c.clock.Now().Add(c.ttl),
	}
	c.mu.Unlock()

	return current, updates, conditionalUpdates, err
}
