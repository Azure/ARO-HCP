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

package cachedreader

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
	"golang.org/x/sync/singleflight"

	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// resourceSKUsCacheSuccessFreshnessTTL is how long a successful cached SKU list may be
	// returned without refreshing from Azure when a new request arrives.
	resourceSKUsCacheSuccessFreshnessTTL = 20 * time.Minute
	// resourceSKUsCacheErrorFreshnessTTL is how long a cached list error may be returned
	// without retrying Azure when a new request arrives.
	resourceSKUsCacheErrorFreshnessTTL = 5 * time.Minute
	// resourceSKUsCacheEntryExpiryTTL is how long the expirable LRU keeps an entry before
	// automatically removing it, independent of freshness checks on read.
	resourceSKUsCacheEntryExpiryTTL = 20 * time.Minute
	// defaultResourceSKUsCacheMaxEntries is the LRU capacity for per-subscription SKU cache entries.
	defaultResourceSKUsCacheMaxEntries = 30

	virtualMachinesResourceType = "virtualMachines"
)

// resourceSKUsClientBuilder builds subscription-scoped Resource SKUs clients (e.g. FPA builder).
type resourceSKUsClientBuilder interface {
	ResourceSKUsClient(tenantID string, subscriptionID string) (azureclient.ResourceSKUsClient, error)
}

// ResourceSKUsCachedReader exposes cached reads of Microsoft.Compute Resource SKUs for virtualMachines.
type ResourceSKUsCachedReader interface {
	// ListVirtualMachineSKUs returns the cached VM Resource SKU list for the subscription.
	ListVirtualMachineSKUs(ctx context.Context, tenantID, subscriptionID string) ([]*armcompute.ResourceSKU, error)
	// GetVirtualMachineSKU looks up one VM size in the cached list.
	GetVirtualMachineSKU(ctx context.Context, tenantID, subscriptionID, vmSize string) (*armcompute.ResourceSKU, error)
}

type cachedResourceSKUsEntry struct {
	skus []*armcompute.ResourceSKU
	// err contains the error returned by the Resource SKUs list call. nil if the list succeeded.
	err error
	// lastUpdate is the wall-clock instant (UTC) when this cache entry was written.
	lastUpdate time.Time
}

// resourceSKUsCachedReader wraps a Resource SKUs client builder. List results are cached in memory
// per subscription ID with a TTL, LRU eviction, and singleflight deduplication.
type resourceSKUsCachedReader struct {
	clientBuilder resourceSKUsClientBuilder
	clock         utilsclock.PassiveClock
	cache         *expirable.LRU[string, *cachedResourceSKUsEntry]
	sfGroup       singleflight.Group
}

// NewResourceSKUsCachedReader returns a ResourceSKUsCachedReader that caches VM Resource SKUs
// lists from clients created by clientBuilder. Cache keys are lowercased subscription IDs.
func NewResourceSKUsCachedReader(clientBuilder resourceSKUsClientBuilder) ResourceSKUsCachedReader {
	return newResourceSKUsCachedReader(clientBuilder, defaultResourceSKUsCacheMaxEntries, utilsclock.RealClock{})
}

func newResourceSKUsCachedReader(clientBuilder resourceSKUsClientBuilder, maxEntries int, clock utilsclock.PassiveClock) *resourceSKUsCachedReader {
	var noEvictionCallback expirable.EvictCallback[string, *cachedResourceSKUsEntry]
	return &resourceSKUsCachedReader{
		clientBuilder: clientBuilder,
		clock:         clock,
		cache:         expirable.NewLRU(maxEntries, noEvictionCallback, resourceSKUsCacheEntryExpiryTTL),
	}
}

// ListVirtualMachineSKUs returns the cached VM Resource SKU list for the subscription.
func (c *resourceSKUsCachedReader) ListVirtualMachineSKUs(ctx context.Context, tenantID, subscriptionID string) ([]*armcompute.ResourceSKU, error) {
	entry, err := c.ensureCached(ctx, tenantID, subscriptionID)
	if err != nil {
		return nil, err
	}
	if entry.err != nil {
		return nil, entry.err
	}
	return deepCopyResourceSKUSlice(entry.skus)
}

// GetVirtualMachineSKU looks up one VM size in the cached list.
func (c *resourceSKUsCachedReader) GetVirtualMachineSKU(ctx context.Context, tenantID, subscriptionID, vmSize string) (*armcompute.ResourceSKU, error) {
	skus, err := c.ListVirtualMachineSKUs(ctx, tenantID, subscriptionID)
	if err != nil {
		return nil, err
	}
	for _, sku := range skus {
		if sku != nil && sku.Name != nil && *sku.Name == vmSize {
			return sku, nil
		}
	}
	return nil, utils.TrackError(fmt.Errorf("VM size %q not found in Resource SKUs for subscription %q", vmSize, subscriptionID))
}

func (c *resourceSKUsCachedReader) ensureCached(ctx context.Context, tenantID, subscriptionID string) (*cachedResourceSKUsEntry, error) {
	cacheKey := strings.ToLower(subscriptionID)
	if entry, ok := c.getFreshEntry(cacheKey); ok {
		return entry, nil
	}

	// Detach cancel/deadline from the caller so the singleflight winner cannot
	// poison concurrent callers or cache a context-cancellation error.
	azureCtx := context.WithoutCancel(ctx)

	v, _, _ := c.sfGroup.Do(cacheKey, func() (interface{}, error) {
		// Re-check after winning singleflight in case another caller filled the cache.
		if entry, ok := c.getFreshEntry(cacheKey); ok {
			return entry, nil
		}

		skus, listErr := c.listVirtualMachineSKUsFromAzure(azureCtx, tenantID, subscriptionID)
		entry := &cachedResourceSKUsEntry{
			skus:       skus,
			err:        listErr,
			lastUpdate: c.clock.Now().UTC(),
		}
		c.cache.Add(cacheKey, entry)
		return entry, nil
	})
	return v.(*cachedResourceSKUsEntry), nil
}

func (c *resourceSKUsCachedReader) getFreshEntry(cacheKey string) (*cachedResourceSKUsEntry, bool) {
	entry, ok := c.cache.Get(cacheKey)
	if !ok {
		return nil, false
	}
	if c.isStale(entry) {
		return nil, false
	}
	return entry, true
}

// isStale applies success/error freshness TTLs using the injectable clock so failed
// list results are refreshed sooner than successes. This is independent of
// resourceSKUsCacheEntryExpiryTTL used by the expirable LRU for auto-eviction.
func (c *resourceSKUsCachedReader) isStale(entry *cachedResourceSKUsEntry) bool {
	ttl := resourceSKUsCacheSuccessFreshnessTTL
	if entry.err != nil {
		ttl = resourceSKUsCacheErrorFreshnessTTL
	}
	return c.clock.Since(entry.lastUpdate) > ttl
}

func (c *resourceSKUsCachedReader) listVirtualMachineSKUsFromAzure(ctx context.Context, tenantID, subscriptionID string) ([]*armcompute.ResourceSKU, error) {
	client, err := c.clientBuilder.ResourceSKUsClient(tenantID, subscriptionID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create Resource SKUs client: %w", err))
	}

	pager := client.NewListPager(nil)
	var skus []*armcompute.ResourceSKU
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to list Resource SKUs for subscription %q: %w", subscriptionID, err))
		}
		for _, sku := range page.Value {
			if sku == nil || sku.ResourceType == nil || *sku.ResourceType != virtualMachinesResourceType {
				continue
			}
			skus = append(skus, sku)
		}
	}
	return skus, nil
}

// deepCopyResourceSKUSlice returns a deep copy of the SKU slice so callers cannot
// mutate nested fields of values held in the cache.
func deepCopyResourceSKUSlice(skus []*armcompute.ResourceSKU) ([]*armcompute.ResourceSKU, error) {
	if skus == nil {
		return nil, nil
	}
	out := make([]*armcompute.ResourceSKU, 0, len(skus))
	for _, sku := range skus {
		copied, err := deepCopyResourceSKU(sku)
		if err != nil {
			return nil, err
		}
		out = append(out, copied)
	}
	return out, nil
}

func deepCopyResourceSKU(sku *armcompute.ResourceSKU) (*armcompute.ResourceSKU, error) {
	if sku == nil {
		return nil, nil
	}
	raw, err := json.Marshal(sku)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to marshal ResourceSKU for deep copy: %w", err))
	}
	var copied armcompute.ResourceSKU
	if err := json.Unmarshal(raw, &copied); err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to unmarshal ResourceSKU for deep copy: %w", err))
	}
	return &copied, nil
}

var _ ResourceSKUsCachedReader = (*resourceSKUsCachedReader)(nil)
