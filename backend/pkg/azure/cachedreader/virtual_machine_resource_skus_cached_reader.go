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
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// resourceSKUsCacheSuccessFreshnessTTL is the LRUExpireCache per-entry TTL for a successful
	// cached VM SKU list. While the entry remains in the cache, ensureCached returns it without
	// contacting Azure or enqueueing a refresh. After it expires (or is LRU-evicted), the next
	// lookup is a miss and pull-through fills the cache again via SyncOnce.
	resourceSKUsCacheSuccessFreshnessTTL = 1 * time.Hour

	// resourceSKUsCacheErrorFreshnessTTL is the LRUExpireCache per-entry TTL for a cached list
	// error. While the entry remains in the cache, ensureCached returns the error without
	// contacting Azure. After it expires, the next lookup is a miss and SyncOnce retries Azure.
	resourceSKUsCacheErrorFreshnessTTL = 5 * time.Minute

	// defaultResourceSKUsCacheMaxEntries defines the maximum number of SKU cache entries. Each
	// region-filtered SKU entry is approximately 4 MB. With 30 entries, the cache may use up to
	// around 120 MB. This value can be adjusted based on actual memory usage.
	defaultResourceSKUsCacheMaxEntries = 30

	// skuResourceTypeVirtualMachines identifies virtual machine SKUs in the Azure
	// Resource SKUs API response.
	skuResourceTypeVirtualMachines = "virtualMachines"

	// FPAVirtualMachineResourceSKUsCachedReaderControllerName is the controller name used
	// for the workqueue.
	FPAVirtualMachineResourceSKUsCachedReaderControllerName = "FPAVirtualMachineResourceSKUsCachedReader"

	// resourceSKUsCachePollInterval defines the polling interval used by ensureCached
	// while waiting for a queued refresh to complete.
	resourceSKUsCachePollInterval = 100 * time.Millisecond
)

// VirtualMachineResourceSKUsCachedReader provides cached access to Microsoft.Compute VM Resource
// SKUs for the Azure region where this backend is deployed.
//
// Unlike a simple pull-through cache, it filters the Azure SKU list by region and resource type
// before caching it and also supports lookup by SKU name. Cache misses are filled by a workqueue
// worker ( Run / SyncOnce) so a caller's own context cannot cancel a fetch shared with other
// concurrent callers. Hits return whatever is currently cached (success or error) until the
// entry's TTL expires.
//
// Workqueue deduplicates queued keys, ensuring that the same key is not processed
// concurrently and helping avoid race conditions for the same cache entry.
type VirtualMachineResourceSKUsCachedReader interface {
	// ListVirtualMachineSKUs returns the cached VM SKU list for the subscription. location must
	// match the Azure location this backend is deployed in (case-insensitive); a mismatch returns
	// an error without consulting or refreshing the cache. The returned slice and its entries are
	// shared with the cache; callers must not mutate them.
	ListVirtualMachineSKUs(ctx context.Context, tenantID, subscriptionID, location string) ([]*armcompute.ResourceSKU, error)
	// GetVirtualMachineSKU looks up one VM size in the cached list. location must match the Azure
	// location this backend is deployed in (case-insensitive); a mismatch returns an error without
	// consulting or refreshing the cache. The returned value is shared with the cache; callers
	// must not mutate it.
	GetVirtualMachineSKU(ctx context.Context, tenantID, subscriptionID, location, vmSize string) (*armcompute.ResourceSKU, error)
}

type cachedComputeVMResourceSKUsEntry struct {
	// vmSKUs holds the location-filtered VM Resource SKU list when the Azure list call
	// succeeded. It is nil when the entry caches a list error.
	vmSKUs []*armcompute.ResourceSKU
	// err contains the error returned by the Resource SKUs list call when the entry caches a
	// failure. It is nil when vmSKUs was populated successfully.
	err error
}

// virtualMachineResourceSKUKey identifies a tenant/subscription pair's cached VM Resource SKUs.
// It is used, after lowercasing both fields, both as the workqueue key that drives on-demand
// refreshes and as the cache key itself, so the two always agree. Lookups are case-insensitive:
// callers passing the same tenant/subscription with different casing share one cache entry.
type virtualMachineResourceSKUKey struct {
	TenantID       string
	SubscriptionID string
}

func newVirtualMachineResourceSKUKey(tenantID, subscriptionID string) virtualMachineResourceSKUKey {
	return virtualMachineResourceSKUKey{
		TenantID:       strings.ToLower(tenantID),
		SubscriptionID: strings.ToLower(subscriptionID),
	}
}

// FPAVirtualMachineResourceSKUsCachedReaderController wraps a FirstPartyApplicationClientBuilder.
type FPAVirtualMachineResourceSKUsCachedReaderController struct {
	name string

	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder
	// location is the Azure location (region) this backend is deployed in. Every Resource SKUs
	// list call is filtered to this location, since we only care about SKUs available where the
	// service itself runs.
	location string
	// cache maps a virtualMachineResourceSKUKey to a *cachedComputeVMResourceSKUsEntry holding
	// the location-filtered VM Resource SKU list and/or the cached list error for that
	// tenant/subscription.
	cache *lrucache.LRUExpireCache
	// queue is where incoming work is placed to de-dup refresh requests for the same
	// tenant/subscription.
	queue workqueue.TypedRateLimitingInterface[virtualMachineResourceSKUKey]
}

// NewFPAVirtualMachineResourceSKUsCachedReaderController returns a
// FPAVirtualMachineResourceSKUsCachedReaderController that caches VM Resource SKUs lists from
// clients created by azureFPAClientBuilder, filtered to location. Cache keys are the
// tenant/subscription ID pair, compared case-insensitively (stored lowercased). The returned
// controller must be started with Run before any caller relying on a cache miss being refreshed
// will receive a response; callers hitting an already-warm cache do not require Run to have been
// called.
func NewFPAVirtualMachineResourceSKUsCachedReaderController(azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder, location string) *FPAVirtualMachineResourceSKUsCachedReaderController {
	return newFPAVirtualMachineResourceSKUsCachedReaderController(azureFPAClientBuilder, defaultResourceSKUsCacheMaxEntries, utilsclock.RealClock{}, location)
}

func newFPAVirtualMachineResourceSKUsCachedReaderController(azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder, maxEntries int, clock utilsclock.PassiveClock, location string) *FPAVirtualMachineResourceSKUsCachedReaderController {
	name := FPAVirtualMachineResourceSKUsCachedReaderControllerName
	return &FPAVirtualMachineResourceSKUsCachedReaderController{
		name:                  name,
		azureFPAClientBuilder: azureFPAClientBuilder,
		location:              location,
		cache:                 lrucache.NewLRUExpireCacheWithClock(maxEntries, clock),
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[virtualMachineResourceSKUKey](),
			workqueue.TypedRateLimitingQueueConfig[virtualMachineResourceSKUKey]{
				Name: name,
			},
		),
	}
}

// ListVirtualMachineSKUs returns the cached VM Resource SKU list for the subscription. location
// must match the Azure location this backend is deployed in (case-insensitive); a mismatch
// returns an error without consulting or refreshing the cache. The returned slice and its
// elements are shared with the cache; callers must not mutate them.
func (c *FPAVirtualMachineResourceSKUsCachedReaderController) ListVirtualMachineSKUs(ctx context.Context, tenantID, subscriptionID, location string) ([]*armcompute.ResourceSKU, error) {
	if err := c.checkLocation(location); err != nil {
		return nil, err
	}
	entry, err := c.ensureCached(ctx, tenantID, subscriptionID)
	if err != nil {
		return nil, err
	}
	if entry.err != nil {
		return nil, entry.err
	}
	return entry.vmSKUs, nil
}

// GetVirtualMachineSKU looks up one VM size in the cached list. location must match the Azure
// location this backend is deployed in (case-insensitive); a mismatch returns an error without
// consulting or refreshing the cache. The returned value is shared with the cache; callers must
// not mutate it.
func (c *FPAVirtualMachineResourceSKUsCachedReaderController) GetVirtualMachineSKU(ctx context.Context, tenantID, subscriptionID, location, vmSize string) (*armcompute.ResourceSKU, error) {
	skus, err := c.ListVirtualMachineSKUs(ctx, tenantID, subscriptionID, location)
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

// checkLocation returns an error when location does not match the backend's deployed Azure
// location. Cache keys and Azure list filtering remain scoped to c.location only; this check
// prepares the public API for callers that pass a cluster/resource location without requiring
// per-location cache internals yet.
func (c *FPAVirtualMachineResourceSKUsCachedReaderController) checkLocation(location string) error {
	if strings.EqualFold(location, c.location) {
		return nil
	}
	return utils.TrackError(fmt.Errorf("location %q is not supported by this backend (deployed in %q)", location, c.location))
}

// ensureCached returns the cache entry for the tenant/subscription pair.
//
// On a cache hit (success or error entry still within its TTL), the entry is returned as-is
// without renewing TTL and without enqueueing a refresh.
//
// On a miss (no entry, or the previous entry expired / was LRU-evicted), this queues a refresh
// and polls using ctx, the caller's own context, so an individual caller's cancellation or
// timeout only stops that caller's wait; it can never cancel or affect the underlying Azure call,
// which runs under the controller's own Run context and may be shared with other concurrent
// callers refreshing the same subscription.
func (c *FPAVirtualMachineResourceSKUsCachedReaderController) ensureCached(ctx context.Context, tenantID, subscriptionID string) (*cachedComputeVMResourceSKUsEntry, error) {
	skuKey := newVirtualMachineResourceSKUKey(tenantID, subscriptionID)
	if value, ok := c.cache.Get(skuKey); ok {
		return value.(*cachedComputeVMResourceSKUsEntry), nil
	}

	// Enqueue the key for SyncOnce to refresh the cache when it is empty.
	c.queue.Add(skuKey)

	var value interface{}
	err := wait.PollUntilContextCancel(ctx, resourceSKUsCachePollInterval, true, func(context.Context) (bool, error) {
		var ok bool
		value, ok = c.cache.Get(skuKey)
		if ok {
			return true, nil
		}
		// covers the potential edge case of lru eviction or ttl expired for some reason
		c.queue.Add(skuKey)
		return false, nil
	})
	if value != nil {
		return value.(*cachedComputeVMResourceSKUsEntry), nil
	}
	return nil, err
}

// SyncOnce fills the cache entry for ref when the cache does not already have one, then always
// returns nil. ensureCached only enqueues when the cache is empty for the key, so under normal
// operation SyncOnce runs on a miss.
//
// Only SyncOnce writes into the cache. Successful lists are stored with
// resourceSKUsCacheSuccessFreshnessTTL; list errors (including context cancellation/deadline
// errors) are stored with resourceSKUsCacheErrorFreshnessTTL so repeat callers fail fast until
// the short TTL elapses and ensureCached treats the key as a miss again.
func (c *FPAVirtualMachineResourceSKUsCachedReaderController) SyncOnce(ctx context.Context, ref virtualMachineResourceSKUKey) error {
	ref = newVirtualMachineResourceSKUKey(ref.TenantID, ref.SubscriptionID)
	// A dirty requeue after concurrent ensureCached Adds can schedule SyncOnce again once the
	// first refresh has already populated the cache. Skip Azure in that case. ensureCached only
	// enqueues on a miss, so a warm entry means there is nothing to fill.
	if _, ok := c.cache.Get(ref); ok {
		return nil
	}
	skus, listErr := c.listVirtualMachineSKUsFromAzure(ctx, ref.TenantID, ref.SubscriptionID)
	entry := &cachedComputeVMResourceSKUsEntry{
		vmSKUs: skus,
		err:    listErr,
	}
	ttl := resourceSKUsCacheSuccessFreshnessTTL
	if listErr != nil {
		ttl = resourceSKUsCacheErrorFreshnessTTL
	}
	c.cache.Add(ref, entry, ttl)

	return nil
}

// resourceSKUsListFilterForLocation builds the OData filter that scopes a Resource SKUs list
// call to a single Azure location, e.g. "location eq 'eastus'".
func resourceSKUsListFilterForLocation(location string) string {
	return fmt.Sprintf("location eq '%s'", location)
}

func (c *FPAVirtualMachineResourceSKUsCachedReaderController) listVirtualMachineSKUsFromAzure(ctx context.Context, tenantID, subscriptionID string) ([]*armcompute.ResourceSKU, error) {
	client, err := c.azureFPAClientBuilder.ResourceSKUsClient(tenantID, subscriptionID)
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

// Run starts the controller's worker pool. Cache misses observed by ensureCached are not
// refreshed until Run has been called; callers should only be relied upon once the controller is
// running (typically under leader election, alongside the backend's other controllers).
func (c *FPAVirtualMachineResourceSKUsCachedReaderController) Run(ctx context.Context, threadiness int) {
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

func (c *FPAVirtualMachineResourceSKUsCachedReaderController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem deals with one item off the queue.  It returns false
// when it's time to quit.
func (c *FPAVirtualMachineResourceSKUsCachedReaderController) processNextWorkItem(ctx context.Context) bool {
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

var _ VirtualMachineResourceSKUsCachedReader = (*FPAVirtualMachineResourceSKUsCachedReaderController)(nil)
