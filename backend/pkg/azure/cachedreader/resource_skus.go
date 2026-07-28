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

	lrucache "k8s.io/apimachinery/pkg/util/cache"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
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

	// skuResourceTypeVirtualMachines is the Azure Resource SKUs API's ResourceType value
	// identifying virtual machine size SKUs, used to filter the SKU list response.
	skuResourceTypeVirtualMachines = "virtualMachines"
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
	name string

	clientBuilder resourceSKUsClientBuilder
	// location is the Azure location (region) this backend is deployed in. Every Resource SKUs
	// list call is filtered to this location, since we only care about SKUs available where the
	// service itself runs.
	location string
	// cache maps a lowercased subscription ID (strings.ToLower(subscriptionID)) to a
	// *cachedResourceSKUsEntry holding the location-filtered VM Resource SKU list and/or the
	// cached list error for that subscription.
	cache *lrucache.LRUExpireCache

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[SKUKey]
}

const cacheControllerKey = "ResourceSKUsCachedReader"

type SKUKey struct {
	TenantID       string
	SubscriptionID string
}

// NewResourceSKUsCachedReader returns a ResourceSKUsCachedReader that caches VM Resource SKUs
// lists from clients created by clientBuilder, filtered to location. Cache keys are lowercased
// subscription IDs.
func NewResourceSKUsCachedReader(clientBuilder resourceSKUsClientBuilder, location string) ResourceSKUsCachedReader {
	return newResourceSKUsCachedReader(clientBuilder, defaultResourceSKUsCacheMaxEntries, utilsclock.RealClock{}, location)
}

func newResourceSKUsCachedReader(clientBuilder resourceSKUsClientBuilder, maxEntries int, clock utilsclock.PassiveClock, location string) *resourceSKUsCachedReader {
	return &resourceSKUsCachedReader{
		name:          cacheControllerKey,
		clientBuilder: clientBuilder,
		location:      location,
		cache:         lrucache.NewLRUExpireCacheWithClock(maxEntries, clock),
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[SKUKey](),
			workqueue.TypedRateLimitingQueueConfig[SKUKey]{
				Name: cacheControllerKey,
			},
		)}
}

func (c *resourceSKUsCachedReader) Run(ctx context.Context, threadiness int) {
	// don't let panics crash the process
	defer utilruntime.HandleCrash()
	// make sure the work queue is shutdown which will trigger workers to end
	defer c.queue.ShutDown()

	ctx = utils.ContextWithControllerName(ctx, c.name)
	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting")

	// start up your worker threads based on threadiness.  Some controllers
	// have multiple kinds of workers
	for i := 0; i < threadiness; i++ {
		// runWorker will loop until "something bad" happens.  The .Until will
		// then rekick the worker after one second
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	logger.Info("Started workers")

	// wait until we're told to stop
	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *resourceSKUsCachedReader) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem deals with one item off the queue.  It returns false
// when it's time to quit.
func (c *resourceSKUsCachedReader) processNextWorkItem(ctx context.Context) bool {
	ref, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(ref)

	logger := utils.LoggerFromContext(ctx)
	logger = utils.AddLoggerValues(logger, ref)
	ctx = utils.ContextWithLogger(ctx, logger)

	controllerutils.ReconcileTotal.WithLabelValues(c.name).Inc()
	err := c.SyncOnce(ctx, ref)
	if err == nil {
		c.queue.Forget(ref)
		return true
	}

	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", ref)
	c.queue.AddRateLimited(ref)

	return true
}

func (c *resourceSKUsCachedReader) SyncOnce(ctx context.Context, ref SKUKey) error {
	cacheKey := strings.ToLower(ref.SubscriptionID)
	if _, ok := c.cache.Get(cacheKey); ok {
		return nil
	}

	skus, listErr := c.listVirtualMachineSKUsFromAzure(ctx, ref.TenantID, ref.SubscriptionID)
	entry := &cachedResourceSKUsEntry{
		skus: skus,
		err:  listErr,
	}
	ttl := resourceSKUsCacheSuccessFreshnessTTL
	if listErr != nil {
		ttl = resourceSKUsCacheErrorFreshnessTTL
	}
	c.cache.Add(cacheKey, entry, ttl)

	return nil
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

	// calls are deduplicated by the workqueue and the SyncOnce only does work when the cache is empty.
	skuKey := SKUKey{TenantID: tenantID, SubscriptionID: subscriptionID}
	c.queue.Add(skuKey)

	// fancier notification is definitely possible, but requires wiring primitives
	var value interface{}
	err := wait.PollUntilContextCancel(ctx, 100*time.Millisecond, true, func(ctx context.Context) (done bool, err error) {
		var ok bool
		value, ok = c.cache.Get(cacheKey)
		if ok {
			return true, nil
		}
		return false, nil
	})
	if value != nil {
		return value.(*cachedResourceSKUsEntry), nil
	}
	return nil, err
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
			if sku == nil || sku.ResourceType == nil || *sku.ResourceType != skuResourceTypeVirtualMachines {
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
