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
	"sync"

	"k8s.io/client-go/util/flowcontrol"
	"k8s.io/utils/lru"

	checkaccessv2 "github.com/Azure/checkaccess-v2-go-sdk/client"
)

const (
	// CheckAccessV2RealFPARateLimiterQPS is the sustained request rate, in queries per second, allowed against the CheckAccessV2 API when calling it as the real FPA identity, which has a documented
	// limit of 500 requests per second. See: https://eng.ms/docs/microsoft-security/identity/auth-authz/access-control-managed-identityacmi/azure-authz-data-plane/authz-dataplane-partner-wiki/remotepdp/onboarding
	CheckAccessV2RealFPARateLimiterQPS = 500

	// CheckAccessV2RealFPARateLimiterBurst is the maximum number of CheckAccessV2 requests allowed to run back-to-back, under the real FPA identity's rate limit, before throttling starts.
	CheckAccessV2RealFPARateLimiterBurst = 500

	// CheckAccessV2InsecureARMPermissionsManagerRateLimiterQPS is the sustained request rate, in queries per second, allowed against the CheckAccessV2 API when calling it as the (insecure,
	// non-production) ARMPermissionsManager identity, which has a documented limit of 25 requests per 5 seconds (25/5 = 5 QPS).
	CheckAccessV2InsecureARMPermissionsManagerRateLimiterQPS = 5

	// CheckAccessV2InsecureARMPermissionsManagerRateLimiterBurst is the maximum number of CheckAccessV2 requests allowed to run back-to-back, under the ARMPermissionsManager identity's rate limit,
	// before throttling starts, matching its 25-request burst window.
	CheckAccessV2InsecureARMPermissionsManagerRateLimiterBurst = 25

	// checkAccessV2RateLimiterCacheSize bounds how many distinct tenants' rate limiters rateLimitedCheckAccessV2ClientBuilder caches at once. 10000 is comfortably enough to cache every distinct
	// Azure tenant expected to have HCP clusters validated by a single backend replica, while still capping worst-case memory growth instead of letting the cache grow for the lifetime of the
	// process. Once the limit is exceeded, building a client for a new tenant evicts the least-recently-used tenant's rate limiter.
	checkAccessV2RateLimiterCacheSize = 10000
)

// rateLimitedCheckAccessV2Client wraps a CheckAccessV2Client so that every CheckAccess call first waits for a token from a shared flowcontrol.RateLimiter. This throttles our outgoing call rate to stay under
// the CheckAccessV2 API's rate limit, instead of reacting to HTTP 429 responses after the fact.
type rateLimitedCheckAccessV2Client struct {
	inner       CheckAccessV2Client
	rateLimiter flowcontrol.RateLimiter
}

var _ CheckAccessV2Client = (*rateLimitedCheckAccessV2Client)(nil)

// CheckAccess waits for a token from the shared rate limiter (returning its error, e.g. from context cancellation, without calling the API if one isn't available) and then delegates to the inner client.
func (c *rateLimitedCheckAccessV2Client) CheckAccess(ctx context.Context, authzReq checkaccessv2.AuthorizationRequest) (*checkaccessv2.AuthorizationDecisionResponse, error) {
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, err
	}
	return c.inner.CheckAccess(ctx, authzReq)
}

// CreateAuthorizationRequest is local request construction rather than an API call, so it is not subject to rate limiting; it delegates directly to the inner client.
func (c *rateLimitedCheckAccessV2Client) CreateAuthorizationRequest(resourceID string, actions []string, jwtToken string) (*checkaccessv2.AuthorizationRequest, error) {
	return c.inner.CreateAuthorizationRequest(resourceID, actions, jwtToken)
}

// rateLimitedCheckAccessV2ClientBuilder wraps a CheckAccessV2ClientBuilder so that every client it builds for a given tenant ID shares that tenant's own rateLimiter, throttling the CheckAccessV2 call rate
// per tenant rather than across all tenants/callers, since the CheckAccessV2 API's documented rate limits apply per calling application/tenant/region, not to the caller as a whole. See: https://eng.ms/docs/microsoft-security/identity/auth-authz/access-control-managed-identityacmi/azure-authz-data-plane/authz-dataplane-partner-wiki/remotepdp/onboarding. Per-tenant rate limiters
// are kept in a size-bounded, least-recently-used rateLimiters cache (see checkAccessV2RateLimiterCacheSize) so memory use stays bounded regardless of how many distinct tenants are seen over the
// process's lifetime: once the cache's capacity is reached, caching a new tenant evicts the least-recently-used tenant's rate limiter to make room.
type rateLimitedCheckAccessV2ClientBuilder struct {
	inner          CheckAccessV2ClientBuilder
	newRateLimiter func() flowcontrol.RateLimiter
	// rateLimiters caches each tenant's own flowcontrol.RateLimiter, keyed by tenant ID (string) and storing a flowcontrol.RateLimiter as the value.
	rateLimiters *lru.Cache
	// rateLimitersMu guards access to rateLimiters, ensuring that only one goroutine can add or evict a tenant's rate limiter at a time.
	rateLimitersMu sync.Mutex
}

var _ CheckAccessV2ClientBuilder = (*rateLimitedCheckAccessV2ClientBuilder)(nil)

// NewRateLimitedCheckAccessV2ClientBuilder wraps inner so that every CheckAccessV2Client it builds for a given tenant ID shares a rate limiter dedicated to that tenant, throttling calls to CheckAccess to
// stay within the CheckAccessV2 API's per-tenant rate limit. Each distinct tenant ID lazily gets its own flowcontrol.NewTokenBucketRateLimiter(qps, burst), created at most once (the first time Build
// is invoked for it) and cached from then on. The per-tenant rate limiter cache holds at most checkAccessV2RateLimiterCacheSize entries, evicting the least-recently-used tenant once exceeded. The
// returned builder must be reused across calls (rather than reconstructed per Build call) for the per-tenant caching to take effect.
func NewRateLimitedCheckAccessV2ClientBuilder(inner CheckAccessV2ClientBuilder, qps float32, burst int) CheckAccessV2ClientBuilder {
	return &rateLimitedCheckAccessV2ClientBuilder{
		inner: inner,
		newRateLimiter: func() flowcontrol.RateLimiter {
			return flowcontrol.NewTokenBucketRateLimiter(qps, burst)
		},
		rateLimiters: lru.New(checkAccessV2RateLimiterCacheSize),
	}
}

func (b *rateLimitedCheckAccessV2ClientBuilder) Build(tenantID string) (CheckAccessV2Client, error) {
	inner, err := b.inner.Build(tenantID)
	if err != nil {
		return nil, err
	}
	return &rateLimitedCheckAccessV2Client{
		inner:       inner,
		rateLimiter: b.getOrCreateRateLimiter(tenantID),
	}, nil
}

// getOrCreateRateLimiter returns the rate limiter cached for tenantID, marking it as the most-recently-used entry. If tenantID isn't cached yet, getOrCreateRateLimiter creates its rate limiter via
// b.newRateLimiter and caches it, letting the underlying LRU cache evict the least-recently-used tenant's rate limiter if it's already at capacity.
func (b *rateLimitedCheckAccessV2ClientBuilder) getOrCreateRateLimiter(tenantID string) flowcontrol.RateLimiter {
	if rateLimiter, ok := b.rateLimiters.Get(tenantID); ok {
		return rateLimiter.(flowcontrol.RateLimiter)
	}

	b.rateLimitersMu.Lock()
	defer b.rateLimitersMu.Unlock()

	// Re-check now that we hold the lock: another goroutine may have created and cached tenantID's rate limiter between our unlocked Get above and acquiring the lock.
	if rateLimiter, ok := b.rateLimiters.Get(tenantID); ok {
		return rateLimiter.(flowcontrol.RateLimiter)
	}

	rateLimiter := b.newRateLimiter()
	b.rateLimiters.Add(tenantID, rateLimiter)
	return rateLimiter
}
