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

package client

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/client-go/util/flowcontrol"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/lru"

	checkaccessv2 "github.com/Azure/checkaccess-v2-go-sdk/client"
)

func TestRateLimitedCheckAccessV2Client_CheckAccess(t *testing.T) {
	fakeAuthRequest := checkaccessv2.AuthorizationRequest{
		Actions: []checkaccessv2.ActionInfo{{Id: "Microsoft.Network/networkSecurityGroups/read"}},
	}

	t.Run("delegates to the inner client once the rate limiter admits the request", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockInner := NewMockCheckAccessV2Client(ctrl)
		wantResponse := &checkaccessv2.AuthorizationDecisionResponse{
			Value: []checkaccessv2.AuthorizationDecision{{ActionId: "test", AccessDecision: checkaccessv2.Allowed}},
		}
		mockInner.EXPECT().CheckAccess(gomock.Any(), fakeAuthRequest).Return(wantResponse, nil).Times(1)

		client := &rateLimitedCheckAccessV2Client{inner: mockInner, rateLimiter: flowcontrol.NewFakeAlwaysRateLimiter()}

		result, err := client.CheckAccess(context.Background(), fakeAuthRequest)
		require.NoError(t, err)
		assert.Same(t, wantResponse, result)
	})

	t.Run("returns the rate limiter's error without calling the inner client when throttled", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockInner := NewMockCheckAccessV2Client(ctrl)
		mockInner.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).Times(0)

		client := &rateLimitedCheckAccessV2Client{inner: mockInner, rateLimiter: flowcontrol.NewFakeNeverRateLimiter()}

		result, err := client.CheckAccess(context.Background(), fakeAuthRequest)
		require.Error(t, err)
		assert.Nil(t, result)
	})
}

func TestNewRateLimitedCheckAccessV2ClientBuilder(t *testing.T) {
	t.Run("propagates an error from the inner builder", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockInnerBuilder := NewMockCheckAccessV2ClientBuilder(ctrl)
		wantErr := fmt.Errorf("failed to build inner client")
		mockInnerBuilder.EXPECT().Build("tenant-id").Return(nil, wantErr)

		builder := NewRateLimitedCheckAccessV2ClientBuilder(mockInnerBuilder, 1, 1)

		result, err := builder.Build("tenant-id")
		require.Error(t, err)
		assert.Equal(t, wantErr, err)
		assert.Nil(t, result)
	})

	t.Run("builds a client whose rate limiter admits requests up to the given burst before throttling", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockInnerBuilder := NewMockCheckAccessV2ClientBuilder(ctrl)
		mockInnerBuilder.EXPECT().Build("tenant-id").Return(NewMockCheckAccessV2Client(ctrl), nil)
		const burst = 2

		// Use a frozen fake clock so the token bucket never refills: TryAccept uses clock.Now() to compute elapsed time since the last token consumption, and with the clock frozen, that elapsed
		// time is always zero, so exactly burst tokens are available and no more, regardless of how much real wall-clock time passes during the test.
		fakeClock := clocktesting.NewFakeClock(time.Now())
		builder := &rateLimitedCheckAccessV2ClientBuilder{
			inner: mockInnerBuilder,
			newRateLimiter: func() flowcontrol.RateLimiter {
				return flowcontrol.NewTokenBucketRateLimiterWithClock(1, burst, fakeClock)
			},
			rateLimiters: lru.New(checkAccessV2RateLimiterCacheSize),
		}

		client, err := builder.Build("tenant-id")
		require.NoError(t, err)

		rlClient, ok := client.(*rateLimitedCheckAccessV2Client)
		require.True(t, ok, "expected Build to return a *rateLimitedCheckAccessV2Client")

		for i := 0; i < burst; i++ {
			assert.True(t, rlClient.rateLimiter.TryAccept(), "expected request %d to be admitted within the burst", i)
		}
		assert.False(t, rlClient.rateLimiter.TryAccept(), "expected the request beyond the burst to find no token available")
	})
}

// TestRateLimitedCheckAccessV2ClientBuilder_Build exercises Build's per-tenant rate limiter caching behavior (independent of NewRateLimitedCheckAccessV2ClientBuilder's real, qps/burst-driven
// flowcontrol.NewTokenBucketRateLimiter), by constructing rateLimitedCheckAccessV2ClientBuilder directly with a fake, fully-controllable newRateLimiter.
func TestRateLimitedCheckAccessV2ClientBuilder_Build(t *testing.T) {
	newTestBuilder := func(newRateLimiter func() flowcontrol.RateLimiter, inner CheckAccessV2ClientBuilder, cacheSize int) *rateLimitedCheckAccessV2ClientBuilder {
		return &rateLimitedCheckAccessV2ClientBuilder{
			inner:          inner,
			newRateLimiter: newRateLimiter,
			rateLimiters:   lru.New(cacheSize),
		}
	}

	t.Run("gives each tenant its own independent rate limiter", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockInnerBuilder := NewMockCheckAccessV2ClientBuilder(ctrl)
		mockInnerClientA := NewMockCheckAccessV2Client(ctrl)
		mockInnerClientB := NewMockCheckAccessV2Client(ctrl)
		mockInnerBuilder.EXPECT().Build("tenant-a").Return(mockInnerClientA, nil)
		mockInnerBuilder.EXPECT().Build("tenant-b").Return(mockInnerClientB, nil)
		// tenant-a's rate limiter denies every request, but tenant-b's own rate limiter still admits its request.
		mockInnerClientA.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).Times(0)
		wantResponse := &checkaccessv2.AuthorizationDecisionResponse{}
		mockInnerClientB.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).Return(wantResponse, nil).Times(1)

		newRateLimiterCalls := 0
		builder := newTestBuilder(func() flowcontrol.RateLimiter {
			newRateLimiterCalls++
			if newRateLimiterCalls == 1 {
				return flowcontrol.NewFakeNeverRateLimiter()
			}
			return flowcontrol.NewFakeAlwaysRateLimiter()
		}, mockInnerBuilder, 2)

		clientA, err := builder.Build("tenant-a")
		require.NoError(t, err)
		clientB, err := builder.Build("tenant-b")
		require.NoError(t, err)

		_, errA := clientA.CheckAccess(context.Background(), checkaccessv2.AuthorizationRequest{})
		require.Error(t, errA)
		resultB, errB := clientB.CheckAccess(context.Background(), checkaccessv2.AuthorizationRequest{})
		require.NoError(t, errB)
		assert.Same(t, wantResponse, resultB)
	})

	t.Run("reuses the same tenant's rate limiter across repeated Build calls instead of creating a new one each time", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockInnerBuilder := NewMockCheckAccessV2ClientBuilder(ctrl)
		mockInnerClientFirst := NewMockCheckAccessV2Client(ctrl)
		mockInnerClientSecond := NewMockCheckAccessV2Client(ctrl)
		mockInnerBuilder.EXPECT().Build("tenant-a").Return(mockInnerClientFirst, nil)
		mockInnerBuilder.EXPECT().Build("tenant-a").Return(mockInnerClientSecond, nil)
		// The second client, for the same tenant, should still be throttled by the rate limiter created (and cached) on the first Build call.
		mockInnerClientSecond.EXPECT().CheckAccess(gomock.Any(), gomock.Any()).Times(0)

		newRateLimiterCalls := 0
		builder := newTestBuilder(func() flowcontrol.RateLimiter {
			newRateLimiterCalls++
			return flowcontrol.NewFakeNeverRateLimiter()
		}, mockInnerBuilder, 2)

		_, err := builder.Build("tenant-a")
		require.NoError(t, err)
		clientSecond, err := builder.Build("tenant-a")
		require.NoError(t, err)

		_, err = clientSecond.CheckAccess(context.Background(), checkaccessv2.AuthorizationRequest{})
		require.Error(t, err)
		assert.Equal(t, 1, newRateLimiterCalls, "expected newRateLimiter to be called only once for the same tenant")
	})

	t.Run("bounds the cache by evicting the least-recently-used tenant's rate limiter once the cache's capacity is exceeded", func(t *testing.T) {
		// This test only exercises the LRU eviction mechanics generically, so it uses a small cache size instead of the real checkAccessV2RateLimiterCacheSize (10000), which would otherwise
		// require needlessly building 10000+ tenants' clients just to fill the cache.
		const testCacheSize = 3

		ctrl := gomock.NewController(t)
		mockInnerBuilder := NewMockCheckAccessV2ClientBuilder(ctrl)
		mockInnerBuilder.EXPECT().Build(gomock.Any()).Return(NewMockCheckAccessV2Client(ctrl), nil).AnyTimes()

		newRateLimiterCalls := 0
		builder := newTestBuilder(func() flowcontrol.RateLimiter {
			newRateLimiterCalls++
			return flowcontrol.NewFakeAlwaysRateLimiter()
		}, mockInnerBuilder, testCacheSize)

		// Fill the cache to its capacity with distinct tenants, then request one more (tenant-0, the least-recently-used, since it was inserted first and never re-accessed): this evicts tenant-0's
		// cached rate limiter to make room.
		for i := range testCacheSize {
			_, err := builder.Build(fmt.Sprintf("tenant-%d", i))
			require.NoError(t, err)
		}
		require.Equal(t, testCacheSize, builder.rateLimiters.Len())
		require.Equal(t, testCacheSize, newRateLimiterCalls)

		_, err := builder.Build("tenant-overflow")
		require.NoError(t, err)
		assert.Equal(t, testCacheSize, builder.rateLimiters.Len(), "expected the cache to stay at its capacity rather than growing")

		// Rebuilding tenant-0's client now has to create a brand new rate limiter, since its cached entry was evicted.
		_, err = builder.Build("tenant-0")
		require.NoError(t, err)
		assert.Equal(t, testCacheSize+2, newRateLimiterCalls, "expected a new rate limiter to be created for tenant-0 after its cache entry was evicted")
	})
}

// TestRateLimitedCheckAccessV2ClientBuilder_GetOrCreateRateLimiter exercises the per-tenant rate limiter cache directly, via getOrCreateRateLimiter, rather than through Build, since it doesn't need
// to involve the inner CheckAccessV2ClientBuilder at all.
func TestRateLimitedCheckAccessV2ClientBuilder_GetOrCreateRateLimiter(t *testing.T) {
	// flowcontrol.NewFakeAlwaysRateLimiter returns a pointer to a zero-size struct, so Go's runtime may give every instance the same address, defeating NotSame/Same identity checks below.
	// flowcontrol.NewFakeNeverRateLimiter allocates a non-zero-size struct (it embeds a sync.WaitGroup), so distinct calls reliably produce distinct addresses.
	newFakeRateLimiter := func() flowcontrol.RateLimiter { return flowcontrol.NewFakeNeverRateLimiter() }

	// newTestBuilder constructs a rateLimitedCheckAccessV2ClientBuilder directly (bypassing NewRateLimitedCheckAccessV2ClientBuilder's fixed checkAccessV2RateLimiterCacheSize) so these tests can
	// exercise the cache's LRU eviction mechanics with a small, deliberately-chosen maxCachedTenants instead of having to fill checkAccessV2RateLimiterCacheSize entries.
	newTestBuilder := func(newRateLimiter func() flowcontrol.RateLimiter, maxCachedTenants int) *rateLimitedCheckAccessV2ClientBuilder {
		return &rateLimitedCheckAccessV2ClientBuilder{
			newRateLimiter: newRateLimiter,
			rateLimiters:   lru.New(maxCachedTenants),
		}
	}

	t.Run("creates and caches a new rate limiter for a tenant that hasn't been seen before", func(t *testing.T) {
		calls := 0
		builder := newTestBuilder(func() flowcontrol.RateLimiter {
			calls++
			return newFakeRateLimiter()
		}, 10)

		rateLimiter := builder.getOrCreateRateLimiter("tenant-a")

		require.NotNil(t, rateLimiter)
		assert.Equal(t, 1, calls)
		assert.Equal(t, 1, builder.rateLimiters.Len())
	})

	t.Run("returns the same cached rate limiter on repeated calls for the same tenant, without calling newRateLimiter again", func(t *testing.T) {
		calls := 0
		builder := newTestBuilder(func() flowcontrol.RateLimiter {
			calls++
			return newFakeRateLimiter()
		}, 10)

		first := builder.getOrCreateRateLimiter("tenant-a")
		second := builder.getOrCreateRateLimiter("tenant-a")

		assert.Same(t, first, second)
		assert.Equal(t, 1, calls, "expected newRateLimiter to be called only once for the same tenant")
		assert.Equal(t, 1, builder.rateLimiters.Len())
	})

	t.Run("gives distinct tenants distinct rate limiters", func(t *testing.T) {
		builder := newTestBuilder(newFakeRateLimiter, 10)

		rateLimiterA := builder.getOrCreateRateLimiter("tenant-a")
		rateLimiterB := builder.getOrCreateRateLimiter("tenant-b")

		assert.NotSame(t, rateLimiterA, rateLimiterB)
		assert.Equal(t, 2, builder.rateLimiters.Len())
	})

	t.Run("evicts the least-recently-used tenant once the cache's entry-count capacity is reached", func(t *testing.T) {
		calls := map[string]int{}
		var currentTenant string
		builder := newTestBuilder(func() flowcontrol.RateLimiter {
			calls[currentTenant]++
			return newFakeRateLimiter()
		}, 2)
		getOrCreate := func(tenantID string) flowcontrol.RateLimiter {
			currentTenant = tenantID
			return builder.getOrCreateRateLimiter(tenantID)
		}

		rateLimiterA1 := getOrCreate("tenant-a")
		getOrCreate("tenant-b")
		require.Equal(t, 2, builder.rateLimiters.Len())

		// tenant-c is a third distinct tenant, exceeding maxSize, so tenant-a (the least-recently-used, since it was inserted first and never re-accessed) is evicted.
		getOrCreate("tenant-c")
		assert.Equal(t, 2, builder.rateLimiters.Len(), "cache should still be at maxSize after eviction")

		// Re-requesting tenant-a's rate limiter now has to create a brand new one, since its cached entry was evicted.
		rateLimiterA2 := getOrCreate("tenant-a")
		assert.NotSame(t, rateLimiterA1, rateLimiterA2)
		assert.Equal(t, 2, calls["tenant-a"], "expected a second rate limiter to be created for tenant-a after eviction")
	})

	t.Run("accessing a tenant protects it from eviction, evicting the truly least-recently-used tenant instead", func(t *testing.T) {
		builder := newTestBuilder(newFakeRateLimiter, 2)

		rateLimiterA := builder.getOrCreateRateLimiter("tenant-a")
		builder.getOrCreateRateLimiter("tenant-b")
		// Touch tenant-a again so tenant-b becomes the least-recently-used entry instead.
		builder.getOrCreateRateLimiter("tenant-a")

		// tenant-c exceeds maxSize, so tenant-b (now least-recently-used) is evicted, not tenant-a.
		builder.getOrCreateRateLimiter("tenant-c")

		rateLimiterAAgain := builder.getOrCreateRateLimiter("tenant-a")
		assert.Same(t, rateLimiterA, rateLimiterAAgain, "tenant-a should still be cached and not require a new rate limiter")
	})

	// This test is best-effort: launching concurrentCallers goroutines gives the race it's checking for (two callers both missing the unlocked Get and racing to acquire rateLimitersMu) a good
	// chance to occur, but the Go scheduler offers no guarantee that any of them actually run concurrently before the lock is taken. If they don't, the test still passes trivially (and
	// harmlessly) without having exercised the race at all.
	t.Run("creates only one rate limiter for a tenant even when getOrCreateRateLimiter is called concurrently for it", func(t *testing.T) {
		var newRateLimiterCalls atomic.Int32
		builder := newTestBuilder(func() flowcontrol.RateLimiter {
			newRateLimiterCalls.Add(1)
			return newFakeRateLimiter()
		}, 10)
		const concurrentCallers = 50

		var wg sync.WaitGroup
		results := make([]flowcontrol.RateLimiter, concurrentCallers)
		for i := range concurrentCallers {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				results[i] = builder.getOrCreateRateLimiter("tenant-a")
			}(i)
		}
		wg.Wait()

		assert.Equal(t, int32(1), newRateLimiterCalls.Load(), "expected newRateLimiter to be called exactly once despite concurrent callers")
		for _, result := range results {
			assert.Same(t, results[0], result, "expected every concurrent caller to observe the same cached rate limiter")
		}
	})
}
