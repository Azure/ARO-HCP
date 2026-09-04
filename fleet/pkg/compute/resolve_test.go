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

package compute

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	armcomputefake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6/fake"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const resolveTestSubscriptionID = "11111111-1111-1111-1111-111111111111"

// newResolveTestCache builds a SKUCache whose Resource SKUs calls are served
// by the official Azure SDK fake transport, mirroring
// fleet/pkg/azure/skucache's own test helper, so ResolveDesiredPools is
// exercised against the real *armcompute.ResourceSKUsClient rather than a
// hand-rolled stand-in.
func newResolveTestCache(t *testing.T, skus []*armcompute.ResourceSKU, listErr error) *skucache.SKUCache {
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

	return skucache.NewSKUCache("eastus", &azfake.TokenCredential{}, &policy.ClientOptions{Transport: transport}, nil)
}

func resolveTestSKU(name, family string, vcpus int64) *armcompute.ResourceSKU {
	return &armcompute.ResourceSKU{
		Name:         ptr.To(name),
		Family:       ptr.To(family),
		ResourceType: ptr.To("virtualMachines"),
		LocationInfo: []*armcompute.ResourceSKULocationInfo{
			{Zones: []*string{ptr.To("1"), ptr.To("2"), ptr.To("3")}},
		},
		Capabilities: []*armcompute.ResourceSKUCapabilities{
			{Name: ptr.To("vCPUs"), Value: ptr.To(strconv.FormatInt(vcpus, 10))},
			{Name: ptr.To("MemoryGB"), Value: ptr.To("64")},
			{Name: ptr.To("EphemeralOSDiskSupported"), Value: ptr.To("True")},
			{Name: ptr.To("CachedDiskBytes"), Value: ptr.To(strconv.FormatInt(200*1024*1024*1024, 10))},
		},
	}
}

func testProfile() Profile {
	return Profile{
		Tiers: []TierConfig{
			{
				Name:           "wrk",
				Role:           PoolRoleWorker,
				PoolMode:       PoolModePerZone,
				Cores:          16,
				OSDiskSizeGB:   100,
				MaxNodes:       5,
				FamilyPriority: []VMFamily{"standardEDSv6Family"},
				MaxPods:        225,
				PoolCount:      3,
			},
		},
		BudgetStrategy: SubscriptionQuotaBudget,
	}
}

func TestResolveDesiredPools(t *testing.T) {
	tests := []struct {
		name               string
		skus               []*armcompute.ResourceSKU
		skuErr             error
		quotaUsage         map[VMFamily]QuotaUsage
		quotaErr           error
		wantQuotaCalls     int
		wantErrContains    []string
		wantAvailableVCPUs map[VMFamily]int64
	}{
		{
			name: "happy path allocates pools and passes tier families to fetchQuotaUsage",
			skus: []*armcompute.ResourceSKU{
				resolveTestSKU("Standard_E16ds_v6", "standardEDSv6Family", 16),
			},
			quotaUsage: map[VMFamily]QuotaUsage{
				"standardEDSv6Family": {Limit: 1000, CurrentValue: 0},
			},
			wantQuotaCalls:     1,
			wantAvailableVCPUs: map[VMFamily]int64{"standardEDSv6Family": 1000},
		},
		{
			name:            "SKU metadata fetch error is wrapped",
			skuErr:          errors.New("resource SKUs API unavailable"),
			wantQuotaCalls:  0,
			wantErrContains: []string{"fetching SKU metadata", "resource SKUs API unavailable"},
		},
		{
			name: "budget computation error is wrapped",
			skus: []*armcompute.ResourceSKU{
				resolveTestSKU("Standard_E16ds_v6", "standardEDSv6Family", 16),
			},
			quotaErr:        errors.New("quota API unavailable"),
			wantQuotaCalls:  1,
			wantErrContains: []string{"computing family budgets", "quota API unavailable"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skuCache := newResolveTestCache(t, tt.skus, tt.skuErr)
			quotaCalls := 0
			fetchQuotaUsage := func(_ context.Context, families sets.Set[VMFamily]) (map[VMFamily]QuotaUsage, error) {
				quotaCalls++
				assert.True(t, families.Equal(sets.New[VMFamily]("standardEDSv6Family")), "expected fetchQuotaUsage to receive the tier's families, got %v", families)
				return tt.quotaUsage, tt.quotaErr
			}

			result, err := ResolveDesiredPools(utils.ContextWithLogger(context.Background(), logr.Discard()), skuCache, resolveTestSubscriptionID, testProfile(), allZones, fetchQuotaUsage)

			assert.Equal(t, tt.wantQuotaCalls, quotaCalls, "unexpected number of quota fetches")
			if len(tt.wantErrContains) > 0 {
				require.Error(t, err)
				for _, message := range tt.wantErrContains {
					assert.Contains(t, err.Error(), message)
				}
				return
			}
			require.NoError(t, err)
			assert.NotEmpty(t, result.Pools, "expected at least one pool to be allocated")
			assert.Empty(t, result.Failures)
			assert.True(t, result.FullyAllocated)
			assert.Equal(t, tt.wantAvailableVCPUs, result.AvailableVCPUs)
			assert.Contains(t, result.SKUMetadata, "Standard_E16ds_v6")
		})
	}
}

func TestResolveDesiredPools_UsageDoesNotChangeDesiredPools(t *testing.T) {
	tests := []struct {
		name          string
		currentUsage  int64
		wantAvailable int64
	}{
		{name: "empty", currentUsage: 0, wantAvailable: 128},
		{name: "three running nodes", currentUsage: 48, wantAvailable: 80},
		{name: "five running nodes", currentUsage: 80, wantAvailable: 48},
		{name: "quota exhausted", currentUsage: 128, wantAvailable: 0},
		{name: "quota exceeded", currentUsage: 144, wantAvailable: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skuCache := newResolveTestCache(t, []*armcompute.ResourceSKU{
				resolveTestSKU("Standard_E16ds_v6", "standardEDSv6Family", 16),
			}, nil)
			profile := Profile{
				Tiers: []TierConfig{
					{
						Name:           "wrk",
						Role:           PoolRoleWorker,
						PoolMode:       PoolModeRegional,
						PoolCount:      1,
						Cores:          16,
						OSDiskSizeGB:   100,
						MaxNodes:       19,
						FamilyPriority: []VMFamily{"standardEDSv6Family"},
						MaxPods:        225,
					},
				},
				BudgetStrategy: SubscriptionQuotaBudget,
			}
			result, err := ResolveDesiredPools(utils.ContextWithLogger(context.Background(), logr.Discard()), skuCache,
				resolveTestSubscriptionID, profile, []string{"1", "2", "3"},
				func(context.Context, sets.Set[VMFamily]) (map[VMFamily]QuotaUsage, error) {
					return map[VMFamily]QuotaUsage{
						"standardEDSv6Family": {Limit: 128, CurrentValue: tt.currentUsage},
					}, nil
				})
			require.NoError(t, err)
			require.Empty(t, result.Failures)
			require.False(t, result.FullyAllocated, "nonzero allocation is not complete")
			require.Len(t, result.Pools, 1)
			assert.Equal(t, int32(7), result.Pools[0].MaxCount, "total quota reserves one 16-vCPU surge node regardless of current usage")
			assert.Equal(t, map[VMFamily]int64{"standardEDSv6Family": tt.wantAvailable}, result.AvailableVCPUs)
		})
	}
}

const productionSubscriptionID = "bc9d60c7-95e2-4e49-8100-85b9cfcb23a0"

// productionEDSv5SKU builds an EDSv5 Resource SKU as it appears in uksouth:
// ephemeral OS disk is supported only on the temp/resource disk (no cache or
// NVMe disk capability), so its size comes from MaxResourceVolumeMB. This is
// the exact shape that motivated the extractSKUMetadata ResourceDisk fallback.
func productionEDSv5SKU(name string, vcpus, memoryGB, maxResourceMB, maxNICs int64) *armcompute.ResourceSKU {
	return &armcompute.ResourceSKU{
		Name:         ptr.To(name),
		Family:       ptr.To("standardEDSv5Family"),
		ResourceType: ptr.To("virtualMachines"),
		LocationInfo: []*armcompute.ResourceSKULocationInfo{
			{Zones: []*string{ptr.To("1"), ptr.To("2"), ptr.To("3")}},
		},
		Capabilities: []*armcompute.ResourceSKUCapabilities{
			{Name: ptr.To("vCPUs"), Value: ptr.To(strconv.FormatInt(vcpus, 10))},
			{Name: ptr.To("MemoryGB"), Value: ptr.To(strconv.FormatInt(memoryGB, 10))},
			{Name: ptr.To("MaxNetworkInterfaces"), Value: ptr.To(strconv.FormatInt(maxNICs, 10))},
			{Name: ptr.To("EphemeralOSDiskSupported"), Value: ptr.To("True")},
			{Name: ptr.To("SupportedEphemeralOSDiskPlacements"), Value: ptr.To("ResourceDisk")},
			{Name: ptr.To("MaxResourceVolumeMB"), Value: ptr.To(strconv.FormatInt(maxResourceMB, 10))},
		},
	}
}

// TestResolveDesiredPools_ProductionScenario exercises desire planning against
// the real uksouth production shape with the production profile: the intended
// EDSv6 worker family is absent from the region, so allocation falls back to
// EDSv5 (temp-disk ephemeral). Values are the real subscription's SKUs and
// quota (EDSv5 limit 1440 / used 584; ESv3 limit 100). Pins the resolved pool
// set, available vCPUs per family, and allocation failures.
func TestResolveDesiredPools_ProductionScenario(t *testing.T) {
	skuCache := newResolveTestCache(t, []*armcompute.ResourceSKU{
		productionEDSv5SKU("Standard_E8ds_v5", 8, 64, 307200, 4),
		productionEDSv5SKU("Standard_E16ds_v5", 16, 128, 614400, 8),
		productionEDSv5SKU("Standard_E32ds_v5", 32, 256, 1228800, 8),
	}, nil)

	// EDSv6 is absent from usage (unavailable in-region); the planner falls
	// back through eFamilyPriority to EDSv5. ESv3 has budget but is never
	// reached because EDSv5 satisfies every tier first.
	fetchQuotaUsage := func(_ context.Context, families sets.Set[VMFamily]) (map[VMFamily]QuotaUsage, error) {
		usage := map[VMFamily]QuotaUsage{
			"standardEDSv5Family": {Limit: 1440, CurrentValue: 584},
			"standardESv3Family":  {Limit: 100},
		}
		result := make(map[VMFamily]QuotaUsage)
		for family := range families {
			if u, ok := usage[family]; ok {
				result[family] = u
			}
		}
		return result, nil
	}

	profile, ok := LookupProfile(ProfileProduction)
	require.True(t, ok, "production profile must exist")

	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	result, err := ResolveDesiredPools(ctx, skuCache, productionSubscriptionID, profile, allZones, fetchQuotaUsage)
	require.NoError(t, err, "resolving desired pools")

	golden := struct {
		FullyAllocated bool                `json:"fullyAllocated"`
		AvailableVCPUs map[VMFamily]int64  `json:"availableVCPUs"`
		Pools          []Pool              `json:"pools"`
		Failures       []AllocationFailure `json:"failures,omitempty"`
	}{
		FullyAllocated: result.FullyAllocated,
		AvailableVCPUs: result.AvailableVCPUs,
		Pools:          result.Pools,
		Failures:       result.Failures,
	}
	b, err := json.MarshalIndent(golden, "", "  ")
	require.NoError(t, err)
	assertGolden(t, string(b)+"\n")
}
