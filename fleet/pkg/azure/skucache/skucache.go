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

// Package skucache caches per-VM-size scheduling-relevant resource data from
// the Azure Resource SKUs API, keyed by subscription ID.
package skucache

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"

	"github.com/Azure/ARO-HCP/internal/kuberesources"
)

const (
	skuCacheTTL                 = 1 * time.Hour
	capabilityNameMemoryGB      = "MemoryGB"
	capabilityNameVCPUs         = "vCPUs"
	capabilityNameMaxNICs       = "MaxNetworkInterfaces"
	resourceTypeVirtualMachines = "virtualMachines"
)

// SKUCache resolves scheduling-relevant resource data for a VM size from the
// Azure Resource SKUs API. It is keyed by subscription ID; the region is fixed
// per cache instance since one fleet controller instance serves a single region.
type SKUCache struct {
	region           string
	credential       azcore.TokenCredential
	armClientOptions *azcorearm.ClientOptions
	clock            utilsclock.PassiveClock

	mu      sync.Mutex
	entries map[string]skuCacheEntry
	group   singleflight.Group
}

type skuCacheEntry struct {
	data      map[string]corev1.ResourceList
	expiresAt time.Time
}

// NewSKUCache returns a SKUCache for region, authenticating Resource
// SKUs API calls with credential. clientOptions and clock may be nil;
// nil clock defaults to the real wall clock.
func NewSKUCache(region string, credential azcore.TokenCredential, clientOptions *policy.ClientOptions, clock utilsclock.PassiveClock) *SKUCache {
	if clientOptions == nil {
		clientOptions = &policy.ClientOptions{}
	}
	if clock == nil {
		clock = utilsclock.RealClock{}
	}
	return &SKUCache{
		region:           region,
		credential:       credential,
		armClientOptions: &azcorearm.ClientOptions{ClientOptions: *clientOptions},
		clock:            clock,
		entries:          make(map[string]skuCacheEntry),
	}
}

func (c *SKUCache) SKUResourcesByVMSize(ctx context.Context, subscriptionID string) (map[string]corev1.ResourceList, error) {
	c.mu.Lock()
	if entry, ok := c.entries[subscriptionID]; ok && c.clock.Now().Before(entry.expiresAt) {
		c.mu.Unlock()
		return entry.data, nil
	}
	c.mu.Unlock()

	result, err, _ := c.group.Do(subscriptionID, func() (any, error) {
		c.mu.Lock()
		if entry, ok := c.entries[subscriptionID]; ok && c.clock.Now().Before(entry.expiresAt) {
			c.mu.Unlock()
			return entry.data, nil
		}
		c.mu.Unlock()

		skuResources, err := c.fetchSKUResources(ctx, subscriptionID)
		if err != nil {
			return nil, err
		}

		c.mu.Lock()
		c.entries[subscriptionID] = skuCacheEntry{
			data:      skuResources,
			expiresAt: c.clock.Now().Add(skuCacheTTL),
		}
		c.mu.Unlock()
		return skuResources, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(map[string]corev1.ResourceList), nil
}

func (c *SKUCache) fetchSKUResources(ctx context.Context, subscriptionID string) (map[string]corev1.ResourceList, error) {
	client, err := armcompute.NewResourceSKUsClient(subscriptionID, c.credential, c.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating resource SKUs client: %w", err)
	}

	filter := fmt.Sprintf("location eq '%s'", c.region)
	pager := client.NewListPager(&armcompute.ResourceSKUsClientListOptions{
		Filter: &filter,
	})

	skuResources := make(map[string]corev1.ResourceList)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing resource SKUs: %w", err)
		}
		for _, sku := range page.Value {
			if sku == nil || sku.ResourceType == nil || *sku.ResourceType != resourceTypeVirtualMachines || sku.Name == nil {
				continue
			}
			resources := extractSKUResources(sku)
			if len(resources) > 0 {
				skuResources[*sku.Name] = resources
			}
		}
	}
	return skuResources, nil
}

func extractSKUResources(sku *armcompute.ResourceSKU) corev1.ResourceList {
	resources := corev1.ResourceList{}

	if memoryQuantity, ok := lookupQuantityGi(sku, capabilityNameMemoryGB); ok {
		resources[corev1.ResourceMemory] = memoryQuantity
	}
	if vcpus, ok := lookupInt64(sku, capabilityNameVCPUs); ok {
		resources[corev1.ResourceCPU] = *resource.NewQuantity(vcpus, resource.DecimalSI)
	}
	if maxNICs, ok := lookupInt64(sku, capabilityNameMaxNICs); ok && maxNICs > 1 {
		// One NIC is the primary (host) NIC; only the rest are available for Swift pod networking.
		resources[kuberesources.SwiftNICResourceName] = *resource.NewQuantity(maxNICs-1, resource.DecimalSI)
	}

	return resources
}

func lookupQuantityGi(sku *armcompute.ResourceSKU, capabilityName string) (resource.Quantity, bool) {
	for _, capability := range sku.Capabilities {
		if capability == nil || capability.Name == nil || capability.Value == nil {
			continue
		}
		if strings.EqualFold(*capability.Name, capabilityName) {
			quantity, err := resource.ParseQuantity(strings.TrimSpace(*capability.Value) + "Gi")
			if err != nil {
				return resource.Quantity{}, false
			}
			return quantity, true
		}
	}
	return resource.Quantity{}, false
}

func lookupInt64(sku *armcompute.ResourceSKU, capabilityName string) (int64, bool) {
	for _, capability := range sku.Capabilities {
		if capability == nil || capability.Name == nil || capability.Value == nil {
			continue
		}
		if strings.EqualFold(*capability.Name, capabilityName) {
			val, err := strconv.ParseInt(strings.TrimSpace(*capability.Value), 10, 64)
			if err != nil {
				return 0, false
			}
			return val, true
		}
	}
	return 0, false
}
