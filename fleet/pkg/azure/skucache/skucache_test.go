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

package skucache

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	testingclock "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	armcomputefake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6/fake"
)

const (
	testRegion         = "eastus"
	testSubscriptionID = "22222222-2222-2222-2222-222222222222"
)

func makeVMResourceSKU(name string, capabilities map[string]string) *armcompute.ResourceSKU {
	caps := make([]*armcompute.ResourceSKUCapabilities, 0, len(capabilities))
	for k, v := range capabilities {
		caps = append(caps, &armcompute.ResourceSKUCapabilities{Name: ptr.To(k), Value: ptr.To(v)})
	}
	return &armcompute.ResourceSKU{
		Name:         ptr.To(name),
		ResourceType: ptr.To(resourceTypeVirtualMachines),
		Capabilities: caps,
	}
}

// newTestCache builds a SKUCache whose Resource SKUs calls are served by
// the official Azure SDK fake transport, so tests exercise the real
// *armcompute.ResourceSKUsClient (paging, response parsing) rather than a
// hand-rolled pager.
func newTestCache(t *testing.T, skus []*armcompute.ResourceSKU, listErr error) *SKUCache {
	t.Helper()
	srv := armcomputefake.ResourceSKUsServer{
		NewListPager: func(options *armcompute.ResourceSKUsClientListOptions) (resp azfake.PagerResponder[armcompute.ResourceSKUsClientListResponse]) {
			if listErr != nil {
				resp.AddError(listErr)
				return
			}
			resp.AddPage(http.StatusOK, armcompute.ResourceSKUsClientListResponse{
				ResourceSKUsResult: armcompute.ResourceSKUsResult{Value: skus},
			}, nil)
			return
		},
	}
	transport := armcomputefake.NewResourceSKUsServerTransport(&srv)

	return NewSKUCache(testRegion, &azfake.TokenCredential{}, &policy.ClientOptions{Transport: transport}, nil)
}

func TestSKUCacheFetch(t *testing.T) {
	tests := []struct {
		name         string
		skus         []*armcompute.ResourceSKU
		listErr      error
		wantErr      bool
		wantVCPUs    map[string]int64
		wantMemoryGB map[string]int64
		wantNICs     map[string]int64
		wantMissing  []string
	}{
		{
			name: "extracts cpu, memory, and secondary NICs",
			skus: []*armcompute.ResourceSKU{
				makeVMResourceSKU("Standard_D8ds_v5", map[string]string{
					"MemoryGB":             "32",
					"vCPUs":                "8",
					"MaxNetworkInterfaces": "4",
				}),
			},
			wantVCPUs:    map[string]int64{"Standard_D8ds_v5": 8},
			wantMemoryGB: map[string]int64{"Standard_D8ds_v5": 32},
			wantNICs:     map[string]int64{"Standard_D8ds_v5": 3},
		},
		{
			name: "skips non virtual machine resource types",
			skus: []*armcompute.ResourceSKU{
				{Name: ptr.To("Premium_LRS"), ResourceType: ptr.To("disks")},
			},
			wantMissing: []string{"Premium_LRS"},
		},
		{
			name: "skips SKUs missing capabilities",
			skus: []*armcompute.ResourceSKU{
				{Name: ptr.To("Standard_NoInfo"), ResourceType: ptr.To(resourceTypeVirtualMachines)},
			},
			wantMissing: []string{"Standard_NoInfo"},
		},
		{
			name: "MaxNetworkInterfaces of 1 produces zero secondary NICs",
			skus: []*armcompute.ResourceSKU{
				makeVMResourceSKU("Standard_B1s", map[string]string{
					"MemoryGB":             "1",
					"vCPUs":                "1",
					"MaxNetworkInterfaces": "1",
				}),
			},
			wantVCPUs:    map[string]int64{"Standard_B1s": 1},
			wantMemoryGB: map[string]int64{"Standard_B1s": 1},
			wantNICs:     map[string]int64{"Standard_B1s": 0},
		},
		{
			name:    "wraps a pager error",
			skus:    nil,
			listErr: errors.New("boom"),
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cache := newTestCache(t, test.skus, test.listErr)

			metadata, err := cache.fetchSKUMetadata(context.Background(), testSubscriptionID)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			for vmSize, wantCPU := range test.wantVCPUs {
				meta, ok := metadata[vmSize]
				require.True(t, ok, "expected %q in result", vmSize)
				assert.Equal(t, wantCPU, meta.VCPUs, "%s VCPUs", vmSize)
			}
			for vmSize, wantMem := range test.wantMemoryGB {
				meta, ok := metadata[vmSize]
				require.True(t, ok, "expected %q in result", vmSize)
				assert.Equal(t, wantMem, meta.MemoryGB, "%s MemoryGB", vmSize)
			}
			for vmSize, wantNIC := range test.wantNICs {
				meta, ok := metadata[vmSize]
				require.True(t, ok, "expected %q in result", vmSize)
				assert.Equal(t, wantNIC, meta.SecondaryNICs, "%s SecondaryNICs", vmSize)
			}
			for _, vmSize := range test.wantMissing {
				_, ok := metadata[vmSize]
				assert.False(t, ok, "expected %q to be absent", vmSize)
			}
		})
	}
}

func TestSKUCacheCachesPerSubscription(t *testing.T) {
	calls := 0
	srv := armcomputefake.ResourceSKUsServer{
		NewListPager: func(options *armcompute.ResourceSKUsClientListOptions) (resp azfake.PagerResponder[armcompute.ResourceSKUsClientListResponse]) {
			calls++
			resp.AddPage(http.StatusOK, armcompute.ResourceSKUsClientListResponse{
				ResourceSKUsResult: armcompute.ResourceSKUsResult{Value: []*armcompute.ResourceSKU{
					makeVMResourceSKU("Standard_D8ds_v5", map[string]string{
						"MemoryGB": "32",
						"vCPUs":    "8",
					}),
				}},
			}, nil)
			return
		},
	}
	transport := armcomputefake.NewResourceSKUsServerTransport(&srv)
	cache := NewSKUCache(testRegion, &azfake.TokenCredential{}, &policy.ClientOptions{Transport: transport}, nil)

	first, err := cache.SKUMetadataByVMSize(context.Background(), testSubscriptionID)
	require.NoError(t, err)
	second, err := cache.SKUMetadataByVMSize(context.Background(), testSubscriptionID)
	require.NoError(t, err)

	assert.Equal(t, first, second)
	assert.Equal(t, 1, calls, "the SKU list should only be fetched once for a cached subscription")
}

func TestSKUCacheRefetchesAfterTTL(t *testing.T) {
	calls := 0
	srv := armcomputefake.ResourceSKUsServer{
		NewListPager: func(options *armcompute.ResourceSKUsClientListOptions) (resp azfake.PagerResponder[armcompute.ResourceSKUsClientListResponse]) {
			calls++
			resp.AddPage(http.StatusOK, armcompute.ResourceSKUsClientListResponse{
				ResourceSKUsResult: armcompute.ResourceSKUsResult{Value: []*armcompute.ResourceSKU{
					makeVMResourceSKU("Standard_D8ds_v5", map[string]string{"MemoryGB": "32", "vCPUs": "8"}),
				}},
			}, nil)
			return
		},
	}
	transport := armcomputefake.NewResourceSKUsServerTransport(&srv)
	clock := testingclock.NewFakePassiveClock(time.Now())
	cache := NewSKUCache(testRegion, &azfake.TokenCredential{}, &policy.ClientOptions{Transport: transport}, clock)

	_, err := cache.SKUMetadataByVMSize(context.Background(), testSubscriptionID)
	require.NoError(t, err)
	_, err = cache.SKUMetadataByVMSize(context.Background(), testSubscriptionID)
	require.NoError(t, err)
	require.Equal(t, 1, calls, "second call within TTL must hit the cache")

	clock.SetTime(clock.Now().Add(skuCacheTTL + time.Second))

	_, err = cache.SKUMetadataByVMSize(context.Background(), testSubscriptionID)
	require.NoError(t, err)
	assert.Equal(t, 2, calls, "a call after TTL expiry must re-fetch")
}

func TestExtractSKUMetadata(t *testing.T) {
	tests := []struct {
		name                         string
		sku                          *armcompute.ResourceSKU
		wantVCPUs                    int64
		wantMemoryGB                 int64
		wantSecondaryNICs            int64
		wantFamily                   string
		wantEphemeralOSDiskSupported bool
		wantEphemeralDiskSizeGB      int64
		wantZones                    []string
		wantConstrainedVCPUs         bool
	}{
		{
			name: "vCPUsAvailable below vCPUs marks constrained",
			sku: makeVMResourceSKU("Standard_E32-16ds_v6", map[string]string{
				"vCPUs":          "32",
				"vCPUsAvailable": "16",
				"MemoryGB":       "256",
			}),
			wantVCPUs:            32,
			wantMemoryGB:         256,
			wantConstrainedVCPUs: true,
		},
		{
			name: "vCPUsAvailable equal to vCPUs is not constrained",
			sku: makeVMResourceSKU("Standard_E32ds_v6", map[string]string{
				"vCPUs":          "32",
				"vCPUsAvailable": "32",
				"MemoryGB":       "256",
			}),
			wantVCPUs:            32,
			wantMemoryGB:         256,
			wantConstrainedVCPUs: false,
		},
		{
			name: "all capabilities present",
			sku: makeVMResourceSKU("Standard_D16ds_v5", map[string]string{
				"MemoryGB":             "64",
				"vCPUs":                "16",
				"MaxNetworkInterfaces": "8",
			}),
			wantVCPUs:         16,
			wantMemoryGB:      64,
			wantSecondaryNICs: 7,
		},
		{
			name: "memory only",
			sku: makeVMResourceSKU("Standard_Partial", map[string]string{
				"MemoryGB": "16",
			}),
			wantMemoryGB: 16,
		},
		{
			name: "decimal memory truncates to int",
			sku: makeVMResourceSKU("Standard_Half", map[string]string{
				"MemoryGB": "3.5",
				"vCPUs":    "2",
			}),
			wantVCPUs:    2,
			wantMemoryGB: 3,
		},
		{
			name: "skips nil capability entries",
			sku: &armcompute.ResourceSKU{
				Name:         ptr.To("Standard_WithNils"),
				ResourceType: ptr.To(resourceTypeVirtualMachines),
				Capabilities: []*armcompute.ResourceSKUCapabilities{
					nil,
					{Name: ptr.To("MemoryGB"), Value: ptr.To("8")},
				},
			},
			wantMemoryGB: 8,
		},
		{
			name: "extracts family from ResourceSKU",
			sku: &armcompute.ResourceSKU{
				Name:         ptr.To("Standard_E32ds_v6"),
				ResourceType: ptr.To(resourceTypeVirtualMachines),
				Family:       ptr.To("standardEDSv6Family"),
				Capabilities: []*armcompute.ResourceSKUCapabilities{
					{Name: ptr.To("vCPUs"), Value: ptr.To("32")},
					{Name: ptr.To("MemoryGB"), Value: ptr.To("256")},
				},
			},
			wantFamily:   "standardEDSv6Family",
			wantVCPUs:    32,
			wantMemoryGB: 256,
		},
		{
			name: "extracts ephemeral OS disk support",
			sku: makeVMResourceSKU("Standard_E32ds_v6", map[string]string{
				"vCPUs":                    "32",
				"MemoryGB":                 "256",
				"EphemeralOSDiskSupported": "True",
			}),
			wantEphemeralOSDiskSupported: true,
			wantVCPUs:                    32,
			wantMemoryGB:                 256,
		},
		{
			name: "extracts NVMe disk size",
			sku: makeVMResourceSKU("Standard_E32ds_v6", map[string]string{
				"vCPUs":             "32",
				"MemoryGB":          "256",
				"NVMeDiskSizeInMiB": "1835008",
			}),
			wantEphemeralDiskSizeGB: 1792,
			wantVCPUs:               32,
			wantMemoryGB:            256,
		},
		{
			name: "falls back to CachedDiskBytes when no NVMe",
			sku: makeVMResourceSKU("Standard_D8ds_v5", map[string]string{
				"vCPUs":           "8",
				"MemoryGB":        "32",
				"CachedDiskBytes": "343597383680",
			}),
			wantEphemeralDiskSizeGB: 320,
			wantVCPUs:               8,
			wantMemoryGB:            32,
		},
		{
			name: "falls back to MaxResourceVolumeMB for ResourceDisk placement",
			sku: makeVMResourceSKU("Standard_E8ds_v5", map[string]string{
				"vCPUs":                              "8",
				"MemoryGB":                           "64",
				"EphemeralOSDiskSupported":           "True",
				"SupportedEphemeralOSDiskPlacements": "ResourceDisk",
				"MaxResourceVolumeMB":                "307200",
			}),
			wantEphemeralOSDiskSupported: true,
			wantEphemeralDiskSizeGB:      300,
			wantVCPUs:                    8,
			wantMemoryGB:                 64,
		},
		{
			name: "MaxResourceVolumeMB ignored without ResourceDisk placement",
			sku: makeVMResourceSKU("Standard_NoPlacement", map[string]string{
				"vCPUs":               "8",
				"MemoryGB":            "64",
				"MaxResourceVolumeMB": "307200",
			}),
			wantEphemeralDiskSizeGB: 0,
			wantVCPUs:               8,
			wantMemoryGB:            64,
		},
		{
			name: "NVMe takes precedence over MaxResourceVolumeMB",
			sku: makeVMResourceSKU("Standard_E8ds_v5", map[string]string{
				"vCPUs":                              "8",
				"MemoryGB":                           "64",
				"SupportedEphemeralOSDiskPlacements": "ResourceDisk",
				"MaxResourceVolumeMB":                "307200",
				"NVMeDiskSizeInMiB":                  "1835008",
			}),
			wantEphemeralDiskSizeGB: 1792,
			wantVCPUs:               8,
			wantMemoryGB:            64,
		},
		{
			name: "zones without restrictions",
			sku: &armcompute.ResourceSKU{
				Name:         ptr.To("Standard_E8ds_v5"),
				ResourceType: ptr.To(resourceTypeVirtualMachines),
				Capabilities: []*armcompute.ResourceSKUCapabilities{
					{Name: ptr.To("vCPUs"), Value: ptr.To("8")},
					{Name: ptr.To("MemoryGB"), Value: ptr.To("64")},
				},
				LocationInfo: []*armcompute.ResourceSKULocationInfo{{
					Zones: []*string{ptr.To("2"), ptr.To("1"), ptr.To("3")},
				}},
			},
			wantVCPUs:    8,
			wantMemoryGB: 64,
			wantZones:    []string{"1", "2", "3"},
		},
		{
			name: "zone restriction removes restricted zones",
			sku: &armcompute.ResourceSKU{
				Name:         ptr.To("Standard_E8ds_v5"),
				ResourceType: ptr.To(resourceTypeVirtualMachines),
				Capabilities: []*armcompute.ResourceSKUCapabilities{
					{Name: ptr.To("vCPUs"), Value: ptr.To("8")},
					{Name: ptr.To("MemoryGB"), Value: ptr.To("64")},
				},
				LocationInfo: []*armcompute.ResourceSKULocationInfo{{
					Zones: []*string{ptr.To("2"), ptr.To("1"), ptr.To("3")},
				}},
				Restrictions: []*armcompute.ResourceSKURestrictions{{
					Type: ptr.To(armcompute.ResourceSKURestrictionsTypeZone),
					RestrictionInfo: &armcompute.ResourceSKURestrictionInfo{
						Zones: []*string{ptr.To("1"), ptr.To("3")},
					},
				}},
			},
			wantVCPUs:    8,
			wantMemoryGB: 64,
			wantZones:    []string{"2"},
		},
		{
			name: "all zones restricted produces empty zones",
			sku: &armcompute.ResourceSKU{
				Name:         ptr.To("Standard_E8ds_v5"),
				ResourceType: ptr.To(resourceTypeVirtualMachines),
				Capabilities: []*armcompute.ResourceSKUCapabilities{
					{Name: ptr.To("vCPUs"), Value: ptr.To("8")},
					{Name: ptr.To("MemoryGB"), Value: ptr.To("64")},
				},
				LocationInfo: []*armcompute.ResourceSKULocationInfo{{
					Zones: []*string{ptr.To("1"), ptr.To("2"), ptr.To("3")},
				}},
				Restrictions: []*armcompute.ResourceSKURestrictions{{
					Type: ptr.To(armcompute.ResourceSKURestrictionsTypeZone),
					RestrictionInfo: &armcompute.ResourceSKURestrictionInfo{
						Zones: []*string{ptr.To("1"), ptr.To("2"), ptr.To("3")},
					},
				}},
			},
			wantVCPUs:    8,
			wantMemoryGB: 64,
			wantZones:    nil,
		},
		{
			name: "location restriction does not affect zones",
			sku: &armcompute.ResourceSKU{
				Name:         ptr.To("Standard_E8ds_v5"),
				ResourceType: ptr.To(resourceTypeVirtualMachines),
				Capabilities: []*armcompute.ResourceSKUCapabilities{
					{Name: ptr.To("vCPUs"), Value: ptr.To("8")},
					{Name: ptr.To("MemoryGB"), Value: ptr.To("64")},
				},
				LocationInfo: []*armcompute.ResourceSKULocationInfo{{
					Zones: []*string{ptr.To("1"), ptr.To("2"), ptr.To("3")},
				}},
				Restrictions: []*armcompute.ResourceSKURestrictions{{
					Type:   ptr.To(armcompute.ResourceSKURestrictionsTypeLocation),
					Values: []*string{ptr.To("uksouth")},
				}},
			},
			wantVCPUs:    8,
			wantMemoryGB: 64,
			wantZones:    []string{"1", "2", "3"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			meta := extractSKUMetadata(test.sku)
			assert.Equal(t, test.wantVCPUs, meta.VCPUs, "VCPUs")
			assert.Equal(t, test.wantMemoryGB, meta.MemoryGB, "MemoryGB")
			assert.Equal(t, test.wantSecondaryNICs, meta.SecondaryNICs, "SecondaryNICs")
			assert.Equal(t, test.wantFamily, meta.Family, "family")
			assert.Equal(t, test.wantEphemeralOSDiskSupported, meta.EphemeralOSDiskSupported, "ephemeral OS disk supported")
			assert.Equal(t, test.wantEphemeralDiskSizeGB, meta.EphemeralDiskSizeGB, "ephemeral disk size GB")
			assert.Equal(t, test.wantZones, meta.Zones, "zones")
			assert.Equal(t, test.wantConstrainedVCPUs, meta.ConstrainedVCPUs, "constrained vCPUs")
		})
	}
}
