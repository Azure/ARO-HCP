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
	"sort"
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
	skuCacheTTL                          = 1 * time.Hour
	capabilityNameMemoryGB               = "MemoryGB"
	capabilityNameVCPUs                  = "vCPUs"
	capabilityNameMaxNICs                = "MaxNetworkInterfaces"
	capabilityNameEphemeralOSDiskSupport = "EphemeralOSDiskSupported"
	capabilityNameNvmeDiskSizeInMiB      = "NVMeDiskSizeInMiB"
	capabilityNameCachedDiskBytes        = "CachedDiskBytes"
	capabilityNameMaxResourceVolumeMB    = "MaxResourceVolumeMB"
	capabilityNameEphemeralPlacements    = "SupportedEphemeralOSDiskPlacements"
	capabilityNameVCPUsAvailable         = "vCPUsAvailable"
	resourceTypeVirtualMachines          = "virtualMachines"

	// ephemeralPlacementResourceDisk is the SupportedEphemeralOSDiskPlacements
	// token for temp/resource-disk placement (e.g. the Ddsv5/Edsv5 families),
	// whose ephemeral OS disk is sized by MaxResourceVolumeMB rather than a
	// dedicated cache or NVMe disk.
	ephemeralPlacementResourceDisk = "ResourceDisk"
)

// SKUMetadata holds VM size characteristics from the Azure Resource SKUs API.
// Instances handed out by SKUCache are shared, cached, read-only values: callers
// must not mutate a SKUMetadata (or the map returned by SKUMetadataByVMSize).
type SKUMetadata struct {
	Name                     string
	Family                   string
	VCPUs                    int64
	MemoryGB                 int64
	SecondaryNICs            int64
	EphemeralOSDiskSupported bool
	EphemeralDiskSizeGB      int64
	Zones                    []string
	ConstrainedVCPUs         bool
}

// ResourceList constructs a Kubernetes ResourceList from the explicit fields.
// Used by capacity reporting to compute per-node allocatable resources.
func (m *SKUMetadata) ResourceList() corev1.ResourceList {
	rl := corev1.ResourceList{}
	if m.VCPUs > 0 {
		rl[corev1.ResourceCPU] = *resource.NewQuantity(m.VCPUs, resource.DecimalSI)
	}
	if m.MemoryGB > 0 {
		rl[corev1.ResourceMemory] = resource.MustParse(fmt.Sprintf("%dGi", m.MemoryGB))
	}
	if m.SecondaryNICs > 0 {
		rl[kuberesources.SwiftNICResourceName] = *resource.NewQuantity(m.SecondaryNICs, resource.DecimalSI)
	}
	return rl
}

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
	metadata  map[string]*SKUMetadata
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

// SKUMetadataByVMSize returns VM-size metadata for subscriptionID, keyed by VM
// size name, served from a per-subscription TTL cache. The returned map and its
// SKUMetadata values are shared across callers and concurrent reconciles and
// must be treated as read-only; a defensive copy would defeat the cache.
func (c *SKUCache) SKUMetadataByVMSize(ctx context.Context, subscriptionID string) (map[string]*SKUMetadata, error) {
	c.mu.Lock()
	if entry, ok := c.entries[subscriptionID]; ok && c.clock.Now().Before(entry.expiresAt) {
		c.mu.Unlock()
		return entry.metadata, nil
	}
	c.mu.Unlock()

	result, err, _ := c.group.Do(subscriptionID, func() (any, error) {
		c.mu.Lock()
		if entry, ok := c.entries[subscriptionID]; ok && c.clock.Now().Before(entry.expiresAt) {
			c.mu.Unlock()
			return entry.metadata, nil
		}
		c.mu.Unlock()

		metadata, err := c.fetchSKUMetadata(ctx, subscriptionID)
		if err != nil {
			return nil, err
		}

		c.mu.Lock()
		c.entries[subscriptionID] = skuCacheEntry{
			metadata:  metadata,
			expiresAt: c.clock.Now().Add(skuCacheTTL),
		}
		c.mu.Unlock()
		return metadata, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(map[string]*SKUMetadata), nil
}

func (c *SKUCache) fetchSKUMetadata(ctx context.Context, subscriptionID string) (map[string]*SKUMetadata, error) {
	client, err := armcompute.NewResourceSKUsClient(subscriptionID, c.credential, c.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating resource SKUs client: %w", err)
	}

	filter := fmt.Sprintf("location eq '%s'", c.region)
	pager := client.NewListPager(&armcompute.ResourceSKUsClientListOptions{
		Filter: &filter,
	})

	result := make(map[string]*SKUMetadata)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing resource SKUs: %w", err)
		}
		for _, sku := range page.Value {
			if sku == nil || sku.ResourceType == nil || *sku.ResourceType != resourceTypeVirtualMachines || sku.Name == nil {
				continue
			}
			meta := extractSKUMetadata(sku)
			if meta.HasResources() {
				result[*sku.Name] = meta
			}
		}
	}
	return result, nil
}

func extractSKUMetadata(sku *armcompute.ResourceSKU) *SKUMetadata {
	meta := &SKUMetadata{
		Name: *sku.Name,
	}

	if sku.Family != nil {
		meta.Family = *sku.Family
	}

	// The SKU list is filtered to a single region (see fetchSKUMetadata), so
	// LocationInfo has exactly one entry for that region; index 0 is safe.
	if len(sku.LocationInfo) > 0 && sku.LocationInfo[0] != nil {
		restrictedZones := collectRestrictedZones(sku)
		for _, zone := range sku.LocationInfo[0].Zones {
			if zone == nil {
				continue
			}
			if _, restricted := restrictedZones[*zone]; restricted {
				continue
			}
			meta.Zones = append(meta.Zones, *zone)
		}
		sort.Strings(meta.Zones)
	}

	if vcpus, ok := lookupInt64(sku, capabilityNameVCPUs); ok {
		meta.VCPUs = vcpus
		if available, ok := lookupInt64(sku, capabilityNameVCPUsAvailable); ok && available < vcpus {
			meta.ConstrainedVCPUs = true
		}
	}
	if memoryGB, ok := lookupFloat64(sku, capabilityNameMemoryGB); ok {
		meta.MemoryGB = int64(memoryGB)
	}
	if maxNICs, ok := lookupInt64(sku, capabilityNameMaxNICs); ok && maxNICs > 1 {
		meta.SecondaryNICs = maxNICs - 1
	}

	if supported, ok := lookupBool(sku, capabilityNameEphemeralOSDiskSupport); ok {
		meta.EphemeralOSDiskSupported = supported
	}

	if nvmeMiB, ok := lookupInt64(sku, capabilityNameNvmeDiskSizeInMiB); ok && nvmeMiB > 0 {
		meta.EphemeralDiskSizeGB = nvmeMiB / 1024
	} else if cachedBytes, ok := lookupInt64(sku, capabilityNameCachedDiskBytes); ok && cachedBytes > 0 {
		meta.EphemeralDiskSizeGB = cachedBytes / (1024 * 1024 * 1024)
	} else if resourceMB, ok := lookupInt64(sku, capabilityNameMaxResourceVolumeMB); ok && resourceMB > 0 && supportsResourceDiskPlacement(sku) {
		// Temp/resource-disk placement: the ephemeral OS disk lives on the
		// resource (temp) disk, whose size is MaxResourceVolumeMB. Azure's own
		// detection divides this MB value by 1024 to get the GiB ceiling.
		meta.EphemeralDiskSizeGB = resourceMB / 1024
	}

	return meta
}

// HasResources reports whether at least one resource field is populated.
func (m *SKUMetadata) HasResources() bool {
	return m.VCPUs > 0 || m.MemoryGB > 0 || m.SecondaryNICs > 0
}

func collectRestrictedZones(sku *armcompute.ResourceSKU) map[string]struct{} {
	restricted := make(map[string]struct{})
	for _, restriction := range sku.Restrictions {
		if restriction == nil || restriction.Type == nil {
			continue
		}
		if *restriction.Type != armcompute.ResourceSKURestrictionsTypeZone {
			continue
		}
		if restriction.RestrictionInfo == nil {
			continue
		}
		for _, zone := range restriction.RestrictionInfo.Zones {
			if zone != nil {
				restricted[*zone] = struct{}{}
			}
		}
	}
	return restricted
}

// supportsResourceDiskPlacement reports whether the SKU lists ResourceDisk as
// a supported ephemeral OS disk placement. The capability value is a
// comma-separated list (e.g. "CacheDisk,ResourceDisk").
func supportsResourceDiskPlacement(sku *armcompute.ResourceSKU) bool {
	raw, ok := lookupCapability(sku, capabilityNameEphemeralPlacements)
	if !ok {
		return false
	}
	for placement := range strings.SplitSeq(raw, ",") {
		if strings.EqualFold(strings.TrimSpace(placement), ephemeralPlacementResourceDisk) {
			return true
		}
	}
	return false
}

// lookupCapability returns the trimmed raw value of the named capability, or
// ("", false) if the SKU has no such capability.
func lookupCapability(sku *armcompute.ResourceSKU, capabilityName string) (string, bool) {
	for _, capability := range sku.Capabilities {
		if capability == nil || capability.Name == nil || capability.Value == nil {
			continue
		}
		if strings.EqualFold(*capability.Name, capabilityName) {
			return strings.TrimSpace(*capability.Value), true
		}
	}
	return "", false
}

func lookupBool(sku *armcompute.ResourceSKU, capabilityName string) (bool, bool) {
	raw, ok := lookupCapability(sku, capabilityName)
	if !ok {
		return false, false
	}
	return strings.EqualFold(raw, "true"), true
}

func lookupFloat64(sku *armcompute.ResourceSKU, capabilityName string) (float64, bool) {
	raw, ok := lookupCapability(sku, capabilityName)
	if !ok {
		return 0, false
	}
	val, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}
	return val, true
}

func lookupInt64(sku *armcompute.ResourceSKU, capabilityName string) (int64, bool) {
	raw, ok := lookupCapability(sku, capabilityName)
	if !ok {
		return 0, false
	}
	val, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return val, true
}
