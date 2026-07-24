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

	"golang.org/x/sync/singleflight"

	lrucache "k8s.io/apimachinery/pkg/util/cache"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// resourceSKUsCacheSuccessFreshnessTTL is how long a successful cached SKU list may be
	// returned without refreshing from Azure when a new request arrives. Also used as the
	// LRUExpireCache per-entry TTL for successful entries.
	resourceSKUsCacheSuccessFreshnessTTL = 20 * time.Minute
	// resourceSKUsCacheErrorFreshnessTTL is how long a cached list error may be returned
	// without retrying Azure when a new request arrives. Also used as the LRUExpireCache
	// per-entry TTL for error entries.
	resourceSKUsCacheErrorFreshnessTTL = 5 * time.Minute
	// defaultResourceSKUsCacheMaxEntries is the LRU capacity for per-subscription SKU cache entries.
	// Testing with one Resource SKUs cache entry per subscription, filtered by the region, results in
	// an approximate size of 4 MB per entry. With defaultResourceSKUsCacheMaxEntries = 30, the cache
	// could reach approximately 120 MB in the worst case. This value can be adjusted after measuring
	// the actual memory footprint with real filtered lists.
	defaultResourceSKUsCacheMaxEntries = 30

	virtualMachinesResourceType = "virtualMachines"
)

// resourceSKUsClientBuilder builds subscription-scoped Resource SKUs clients (e.g. FPA builder).
type resourceSKUsClientBuilder interface {
	ResourceSKUsClient(tenantID string, subscriptionID string) (azureclient.ResourceSKUsClient, error)
}

// ResourceSKUsCachedReader exposes cached reads of Microsoft.Compute Resource SKUs for virtualMachines,
// scoped to the single Azure location (region) this backend is deployed in.
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
}

// resourceSKUsCachedReader wraps a Resource SKUs client builder. List results are cached in memory
// per subscription ID with a TTL, LRU eviction, and singleflight deduplication. All lookups are
// scoped to a single, fixed Azure location set at construction time.
type resourceSKUsCachedReader struct {
	clientBuilder resourceSKUsClientBuilder
	// location is the Azure location (region) this backend is deployed in. Every Resource SKUs
	// list call is filtered to this location, since we only care about SKUs available where the
	// service itself runs.
	location string
	cache    *lrucache.LRUExpireCache
	sfGroup  singleflight.Group
}

// NewResourceSKUsCachedReader returns a ResourceSKUsCachedReader that caches VM Resource SKUs
// lists from clients created by clientBuilder, filtered to location. Cache keys are lowercased
// subscription IDs.
func NewResourceSKUsCachedReader(clientBuilder resourceSKUsClientBuilder, location string) ResourceSKUsCachedReader {
	return newResourceSKUsCachedReader(clientBuilder, defaultResourceSKUsCacheMaxEntries, utilsclock.RealClock{}, location)
}

func newResourceSKUsCachedReader(clientBuilder resourceSKUsClientBuilder, maxEntries int, clock utilsclock.PassiveClock, location string) *resourceSKUsCachedReader {
	return &resourceSKUsCachedReader{
		clientBuilder: clientBuilder,
		location:      location,
		cache:         lrucache.NewLRUExpireCacheWithClock(maxEntries, clock),
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
	return nil, utils.TrackError(fmt.Errorf("VM size %q not found in Resource SKUs for subscription %q in location %q", vmSize, subscriptionID, c.location))
}

func (c *resourceSKUsCachedReader) ensureCached(ctx context.Context, tenantID, subscriptionID string) (*cachedResourceSKUsEntry, error) {
	cacheKey := strings.ToLower(subscriptionID)
	if value, ok := c.cache.Get(cacheKey); ok {
		return value.(*cachedResourceSKUsEntry), nil
	}

	// Detach cancel/deadline from the caller so the singleflight winner cannot
	// poison concurrent callers or cache a context-cancellation error.
	azureCtx := context.WithoutCancel(ctx)

	v, _, _ := c.sfGroup.Do(cacheKey, func() (interface{}, error) {
		// Re-check after winning singleflight in case another caller filled the cache.
		if value, ok := c.cache.Get(cacheKey); ok {
			return value, nil
		}

		skus, listErr := c.listVirtualMachineSKUsFromAzure(azureCtx, tenantID, subscriptionID)
		entry := &cachedResourceSKUsEntry{
			skus: skus,
			err:  listErr,
		}
		ttl := resourceSKUsCacheSuccessFreshnessTTL
		if listErr != nil {
			ttl = resourceSKUsCacheErrorFreshnessTTL
		}
		c.cache.Add(cacheKey, entry, ttl)
		return entry, nil
	})
	return v.(*cachedResourceSKUsEntry), nil
}

// resourceSKUsListFilterForLocation builds the OData filter that scopes a Resource SKUs list
// call to a single Azure location, e.g. "location eq 'eastus'".
func resourceSKUsListFilterForLocation(location string) string {
	return fmt.Sprintf("location eq '%s'", location)
}

func (c *resourceSKUsCachedReader) listVirtualMachineSKUsFromAzure(ctx context.Context, tenantID, subscriptionID string) ([]*armcompute.ResourceSKU, error) {
	client, err := c.clientBuilder.ResourceSKUsClient(tenantID, subscriptionID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create Resource SKUs client: %w", err))
	}

	pager := client.NewListPager(&armcompute.ResourceSKUsClientListOptions{
		Filter: ptr.To(resourceSKUsListFilterForLocation(c.location)),
	})
	var skus []*armcompute.ResourceSKU
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to list Resource SKUs for subscription %q in location %q: %w", subscriptionID, c.location, err))
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
