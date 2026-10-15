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
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
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
// hand-rolled stand-in. region is the SKUCache's configured region, which
// isLocationRestricted compares against a SKU's Location-type restrictions.
func newResolveTestCache(t *testing.T, region string, skus []*armcompute.ResourceSKU, listErr error) *skucache.SKUCache {
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

	return skucache.NewSKUCache(region, &azfake.TokenCredential{}, &policy.ClientOptions{Transport: transport}, nil)
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
			{Name: ptr.To("MaxNetworkInterfaces"), Value: ptr.To("4")},
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
				Class:          WorkerPools,
				PoolMode:       PoolModePerZone,
				Cores:          16,
				OSDiskSizeGB:   100,
				MaxNodes:       5,
				FamilyPriority: []VMFamily{"StandardEdsv6Family"},
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
				resolveTestSKU("Standard_E16ds_v6", "StandardEdsv6Family", 16),
			},
			quotaUsage: map[VMFamily]QuotaUsage{
				"StandardEdsv6Family": {Limit: 1000, CurrentValue: 0},
			},
			wantQuotaCalls:     1,
			wantAvailableVCPUs: map[VMFamily]int64{"StandardEdsv6Family": 1000},
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
				resolveTestSKU("Standard_E16ds_v6", "StandardEdsv6Family", 16),
			},
			quotaErr:        errors.New("quota API unavailable"),
			wantQuotaCalls:  1,
			wantErrContains: []string{"computing family budgets", "quota API unavailable"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skuCache := newResolveTestCache(t, "eastus", tt.skus, tt.skuErr)
			quotaCalls := 0
			fetchQuotaUsage := func(_ context.Context, families sets.Set[VMFamily]) (map[VMFamily]QuotaUsage, error) {
				quotaCalls++
				assert.True(t, families.Equal(sets.New[VMFamily]("StandardEdsv6Family")), "expected fetchQuotaUsage to receive the tier's families, got %v", families)
				return tt.quotaUsage, tt.quotaErr
			}

			result, err := ResolveDesiredPools(utils.ContextWithLogger(context.Background(), logr.Discard()), skuCache, resolveTestSubscriptionID, testProfile(), allZones, nil, fetchQuotaUsage)

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

func TestResolveDesiredPools_LocationRestrictions(t *testing.T) {
	const preferredSize = "Standard_E16ds_v6"
	const fallbackSize = "Standard_E16ds_v5"
	tests := []struct {
		name             string
		restriction      *armcompute.ResourceSKURestrictions
		restrictFallback bool
		wantSize         string
	}{
		{
			name: "restricted region in values uses fallback",
			restriction: &armcompute.ResourceSKURestrictions{
				Type:       ptr.To(armcompute.ResourceSKURestrictionsTypeLocation),
				ReasonCode: ptr.To(armcompute.ResourceSKURestrictionsReasonCodeNotAvailableForSubscription),
				Values:     []*string{nil, ptr.To("westus"), ptr.To("EASTUS")},
			},
			wantSize: fallbackSize,
		},
		{
			name: "restricted region in restriction info uses fallback",
			restriction: &armcompute.ResourceSKURestrictions{
				Type:       ptr.To(armcompute.ResourceSKURestrictionsTypeLocation),
				ReasonCode: ptr.To(armcompute.ResourceSKURestrictionsReasonCodeQuotaID),
				RestrictionInfo: &armcompute.ResourceSKURestrictionInfo{
					Locations: []*string{nil, ptr.To("eastus")},
				},
			},
			wantSize: fallbackSize,
		},
		{
			name: "restriction in another region preserves preferred SKU",
			restriction: &armcompute.ResourceSKURestrictions{
				Type:   ptr.To(armcompute.ResourceSKURestrictionsTypeLocation),
				Values: []*string{ptr.To("westus")},
				RestrictionInfo: &armcompute.ResourceSKURestrictionInfo{
					Locations: []*string{ptr.To("westus")},
				},
			},
			wantSize: preferredSize,
		},
		{
			name: "all candidates restricted reports allocation failure",
			restriction: &armcompute.ResourceSKURestrictions{
				Type:   ptr.To(armcompute.ResourceSKURestrictionsTypeLocation),
				Values: []*string{ptr.To("eastus")},
			},
			restrictFallback: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, mode := range []PoolMode{PoolModePerZone, PoolModeRegional} {
				t.Run(string(mode), func(t *testing.T) {
					preferred := resolveTestSKU(preferredSize, "StandardEdsv6Family", 16)
					preferred.Restrictions = []*armcompute.ResourceSKURestrictions{nil, {}, tt.restriction}
					fallback := resolveTestSKU(fallbackSize, "standardEDSv5Family", 16)
					if tt.restrictFallback {
						fallback.Restrictions = preferred.Restrictions
					}
					cache := newResolveTestCache(t, "eastus", []*armcompute.ResourceSKU{preferred, fallback}, nil)
					profile := testProfile()
					profile.Tiers[0].PoolMode = mode
					profile.Tiers[0].FamilyPriority = []VMFamily{"StandardEdsv6Family", "standardEDSv5Family"}
					result, err := ResolveDesiredPools(
						utils.ContextWithLogger(context.Background(), logr.Discard()),
						cache, resolveTestSubscriptionID, profile, allZones, nil,
						func(context.Context, sets.Set[VMFamily]) (map[VMFamily]QuotaUsage, error) {
							return map[VMFamily]QuotaUsage{
								"StandardEdsv6Family": {Limit: 1000},
								"standardEDSv5Family": {Limit: 1000},
							}, nil
						},
					)
					require.NoError(t, err)

					// Existing pools still need resource capacity even when new
					// allocations cannot use their SKU.
					require.Contains(t, result.SKUMetadata, preferredSize)
					capacity := result.SKUMetadata[preferredSize].ResourceList()
					assert.Equal(t, int64(16), capacity.Cpu().Value())
					assert.Equal(t, int64(64*1024*1024*1024), capacity.Memory().Value())

					if tt.wantSize == "" {
						assert.Empty(t, result.Pools)
						assert.False(t, result.FullyAllocated)
						require.Len(t, result.Failures, 1)
						assert.Equal(t, "NoEligibleSKU", result.Failures[0].Reason)
						return
					}
					assert.True(t, result.FullyAllocated)
					assert.Empty(t, result.Failures)
					wantPools := 3
					if mode == PoolModeRegional {
						wantPools = 1
					}
					require.Len(t, result.Pools, wantPools)
					for _, pool := range result.Pools {
						assert.Equal(t, tt.wantSize, pool.Spec.Size)
					}
				})
			}
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
			skuCache := newResolveTestCache(t, "eastus", []*armcompute.ResourceSKU{
				resolveTestSKU("Standard_E16ds_v6", "StandardEdsv6Family", 16),
			}, nil)
			profile := Profile{
				Tiers: []TierConfig{
					{
						Name:           "wrk",
						Class:          WorkerPools,
						PoolMode:       PoolModeRegional,
						PoolCount:      1,
						Cores:          16,
						OSDiskSizeGB:   100,
						MaxNodes:       19,
						FamilyPriority: []VMFamily{"StandardEdsv6Family"},
						MaxPods:        225,
					},
				},
				BudgetStrategy: SubscriptionQuotaBudget,
			}
			result, err := ResolveDesiredPools(utils.ContextWithLogger(context.Background(), logr.Discard()), skuCache,
				resolveTestSubscriptionID, profile, []string{"1", "2", "3"}, nil,
				func(context.Context, sets.Set[VMFamily]) (map[VMFamily]QuotaUsage, error) {
					return map[VMFamily]QuotaUsage{
						"StandardEdsv6Family": {Limit: 128, CurrentValue: tt.currentUsage},
					}, nil
				})
			require.NoError(t, err)
			require.Empty(t, result.Failures)
			require.False(t, result.FullyAllocated, "nonzero allocation is not complete")
			require.Len(t, result.Pools, 1)
			assert.Equal(t, int32(7), result.Pools[0].MaxCount, "total quota reserves one 16-vCPU surge node regardless of current usage")
			assert.Equal(t, map[VMFamily]int64{"StandardEdsv6Family": tt.wantAvailable}, result.AvailableVCPUs)
		})
	}
}

const scenarioSubscriptionID = "33333333-3333-3333-3333-333333333333"

// runDesiredPoolsScenario resolves desired pools for the given SKUs and the
// usages fixture in testdata/scenarios/<dir>, under the named profile, and
// pins a golden report of the allocation. Shared by
// TestResolveDesiredPools_Scenario and TestResolveDesiredPools_Constraints so
// the harness — wiring fetchQuotaUsage, resolving, and rendering the golden —
// exists once; only the SKUs, the fixture directory, the SKUCache's region,
// and the profile vary between callers. t must be the subtest's *testing.T
// so the golden path (derived from t.Name()) lands under the caller's own
// test function.
func runDesiredPoolsScenario(t *testing.T, dir string, skus []*armcompute.ResourceSKU, region, profileName string) {
	t.Helper()
	rawUsages, err := os.ReadFile(filepath.Join("testdata", "scenarios", dir, "scenario-usages.json"))
	require.NoError(t, err, "reading usages")
	var usages []*armcompute.Usage
	require.NoError(t, json.Unmarshal(rawUsages, &usages), "decoding usages")

	skuCache := newResolveTestCache(t, region, skus, nil)

	// Mirrors quota.FetchUsage: a family matches its usage entry by exact name.
	usage := make(map[VMFamily]QuotaUsage, len(usages))
	for _, u := range usages {
		usage[VMFamily(*u.Name.Value)] = QuotaUsage{Limit: *u.Limit, CurrentValue: int64(*u.CurrentValue)}
	}
	fetchQuotaUsage := func(_ context.Context, families sets.Set[VMFamily]) (map[VMFamily]QuotaUsage, error) {
		result := make(map[VMFamily]QuotaUsage)
		for family := range families {
			if u, ok := usage[family]; ok {
				result[family] = u
			}
		}
		return result, nil
	}

	profile, ok := LookupProfile(profileName)
	require.True(t, ok, "profile %q must exist", profileName)

	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	result, err := ResolveDesiredPools(ctx, skuCache, scenarioSubscriptionID, profile, allZones, nil, fetchQuotaUsage)
	require.NoError(t, err, "resolving desired pools")

	// Quota limits as the planner sees them: only the profile's families.
	quotaUsages, err := fetchQuotaUsage(ctx, TierFamilies(profile.Tiers))
	require.NoError(t, err, "fetching quota usage")
	limits := make(map[VMFamily]int64, len(quotaUsages))
	for family, u := range quotaUsages {
		limits[family] = u.Limit
	}
	assertGolden(t, renderReport(func(w io.Writer) {
		writeAllocationInputs(w, allZones, profile.Tiers, limits, result.SKUMetadata)
		fmt.Fprintln(w, "\navailable vCPUs:")
		for _, family := range slices.Sorted(maps.Keys(result.AvailableVCPUs)) {
			fmt.Fprintf(w, "  %s:\t%d\n", family, result.AvailableVCPUs[family])
		}
		writeAllocationResult(w, result.Pools, result.Failures, result.FullyAllocated)
	}))
}

// TestResolveDesiredPools_Scenario exercises desire planning against Resource
// SKUs and quota usages dumped from real systems, one directory per region
// under testdata/scenarios, each with the profile its environment runs. The
// dumps hold the raw ARM responses, trimmed to the tier profiles' families and
// core counts and to the capabilities skucache reads. Pins the resolved pool
// set, available vCPUs per family, and allocation failures.
func TestResolveDesiredPools_Scenario(t *testing.T) {
	tests := []struct {
		region  string
		profile string
	}{
		{region: "uksouth", profile: ProfileProduction},
		// Quota less the running vCPUs of the subscription's other clusters.
		{region: "westus3", profile: ProfileIntegration},
	}

	for _, tt := range tests {
		t.Run(tt.region, func(t *testing.T) {
			rawSKUs, err := os.ReadFile(filepath.Join("testdata", "scenarios", tt.region, "scenario-skus.json"))
			require.NoError(t, err, "reading Resource SKUs")
			var skus []*armcompute.ResourceSKU
			require.NoError(t, json.Unmarshal(rawSKUs, &skus), "decoding Resource SKUs")
			runDesiredPoolsScenario(t, tt.region, skus, tt.region, tt.profile)
		})
	}
}

// constraintSKURestriction overlays a family-wide SKU restriction onto the
// shared base catalog (see loadConstraintSKUs), mirroring the
// armcompute.ResourceSKURestrictions shapes Azure actually returns for a
// quota/zone-restricted family. A Location restriction excludes the family
// from the region entirely (isLocationRestricted), independent of pool mode.
// A Zone restriction only removes the listed zones; PoolModeRegional tiers
// ignore zone restrictions entirely, so blocking every zone is not
// equivalent to a Location restriction for a Regional tier.
type constraintSKURestriction struct {
	family       VMFamily
	location     bool
	blockedZones []string
}

// constraintSKUSpec names one named condition's deviation from the shared
// base SKU catalog (testdata/scenarios/constraints-skus-base.json): exclude
// drops SKU names the condition's source subscription never offered at all
// (e.g. a generation not yet available in that region); restrictions
// overlays the quota/zone restrictions Azure reports there.
type constraintSKUSpec struct {
	exclude      []string
	restrictions []constraintSKURestriction
	// zones overrides the base catalog's zone list for specific SKU names
	// (keyed by name), for the rare case where a condition's source region
	// offers a SKU in fewer zones than the catalog's maximum, with no formal
	// restriction reported (e.g. a constrained-vCPU SKU the test-tenant
	// region only ever had in one zone).
	zones map[string][]string
}

// loadConstraintSKUs builds the Resource SKUs for a named constraint
// condition from the shared base catalog, applying spec's exclusions and
// restrictions. region is the Location restriction's value; isLocationRestricted
// compares it against the SKUCache's own configured region, so both must
// agree for a Location restriction to take effect.
func loadConstraintSKUs(t *testing.T, region string, spec constraintSKUSpec) []*armcompute.ResourceSKU {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "scenarios", "constraints-skus-base.json"))
	require.NoError(t, err, "reading constraint SKU base catalog")
	var skus []*armcompute.ResourceSKU
	require.NoError(t, json.Unmarshal(raw, &skus), "decoding constraint SKU base catalog")

	excluded := sets.New(spec.exclude...)
	restrictionsByFamily := make(map[VMFamily]constraintSKURestriction, len(spec.restrictions))
	for _, r := range spec.restrictions {
		restrictionsByFamily[r.family] = r
	}

	result := make([]*armcompute.ResourceSKU, 0, len(skus))
	for _, sku := range skus {
		if excluded.Has(*sku.Name) {
			continue
		}
		if override, ok := spec.zones[*sku.Name]; ok {
			zones := make([]*string, len(override))
			for i, zone := range override {
				zones[i] = ptr.To(zone)
			}
			sku.LocationInfo[0].Zones = zones
		}
		if r, ok := restrictionsByFamily[VMFamily(*sku.Family)]; ok {
			if r.location {
				sku.Restrictions = append(sku.Restrictions, &armcompute.ResourceSKURestrictions{
					Type:       ptr.To(armcompute.ResourceSKURestrictionsTypeLocation),
					ReasonCode: ptr.To(armcompute.ResourceSKURestrictionsReasonCodeNotAvailableForSubscription),
					Values:     []*string{ptr.To(region)},
				})
			}
			if len(r.blockedZones) > 0 {
				zones := make([]*string, len(r.blockedZones))
				for i, zone := range r.blockedZones {
					zones[i] = ptr.To(zone)
				}
				sku.Restrictions = append(sku.Restrictions, &armcompute.ResourceSKURestrictions{
					Type:            ptr.To(armcompute.ResourceSKURestrictionsTypeZone),
					ReasonCode:      ptr.To(armcompute.ResourceSKURestrictionsReasonCodeNotAvailableForSubscription),
					RestrictionInfo: &armcompute.ResourceSKURestrictionInfo{Zones: zones},
				})
			}
		}
		result = append(result, sku)
	}
	return result
}

// TestResolveDesiredPools_Constraints exercises layout planning against a
// fixed set of named quota and SKU-restriction conditions, all running
// ProfileProduction per config/config.yaml's fleet.nodePoolPlanning default.
// Every condition shares one SKU catalog (constraints-skus-base.json,
// deduplicated from the regions each condition was originally sampled from);
// loadConstraintSKUs overlays only each condition's own deviation from it.
// The condition it pins — not its provenance — is what the test guards: a
// failure here means the profile cannot fully allocate against that
// quota/restriction shape, whether the fixture is sourced from a live
// subscription or written by hand.
//
// Each scenario is named after the condition it pins rather than its source
// region. `region` still carries a real Azure region name, required for
// isLocationRestricted to evaluate a condition's Location-type restrictions
// correctly.
//
// canadacentral and switzerlandnorth dumps existed but were dropped: both were
// byte-identical to each other and nearly identical to australiaeast/
// tight_quota_margin (same SKU catalog, topology, and outcome, differing only
// in a quota number), so they added no coverage beyond it.
func TestResolveDesiredPools_Constraints(t *testing.T) {
	tests := []struct {
		dir    string
		region string
		spec   constraintSKUSpec
	}{
		// Expect full allocation despite the thinnest standardEDSv5Family quota margin of the sampled regions — the margin that once deadlocked infra against worker quota.
		{dir: "tight_quota_margin", region: "australiaeast", spec: constraintSKUSpec{
			exclude: []string{
				"Standard_E16ds_v7", "Standard_E32ds_v7", "Standard_E4ds_v7", "Standard_E8ds_v7",
			},
		}},
		// Expect full allocation: the planner steers the one blocked zone's pool onto a family that is still offered there.
		{dir: "single_zone_blocked_family", region: "brazilsouth", spec: constraintSKUSpec{
			restrictions: []constraintSKURestriction{
				{family: "standardEDSv5Family", blockedZones: []string{"2"}},
				{family: "StandardEdsv7Family", blockedZones: []string{"1", "3"}},
			},
		}},
		// Expect full allocation with no failures — the control case nothing should ever fail against.
		{dir: "unrestricted_baseline", region: "centralindia", spec: constraintSKUSpec{
			exclude: []string{"Standard_E16ds_v7", "Standard_E32ds_v7", "Standard_E4ds_v7", "Standard_E8ds_v7"},
		}},
		// Expect partial allocation: sys/inf/wrk16 allocate from scratch, but wrk32 reports InsufficientQuota.
		{dir: "fresh_cluster_quota_exhausted", region: "eastus", spec: constraintSKUSpec{
			restrictions: []constraintSKURestriction{
				{family: "standardEDSv5Family", location: true, blockedZones: allZones},
				{family: "StandardEdsv6Family", blockedZones: []string{"1", "2", "3"}},
				{family: "standardESv3Family", location: true, blockedZones: allZones},
			},
		}},
		// Expect full allocation: the planner routes around a family blocked region-wide onto an eligible family and SKU.
		{dir: "location_blocked_family", region: "eastus2", spec: constraintSKUSpec{
			restrictions: []constraintSKURestriction{
				{family: "standardEDSv4Family", location: true, blockedZones: allZones},
				{family: "standardEDSv5Family", location: true, blockedZones: allZones},
				{family: "StandardEdsv6Family", blockedZones: []string{"2"}},
			},
		}},
		// Expect full allocation: the planner routes around two blocked zones onto families still offered there.
		{dir: "zone_blocked_family", region: "westeurope", spec: constraintSKUSpec{
			exclude: []string{"Standard_E16ds_v7", "Standard_E32ds_v7", "Standard_E4ds_v7", "Standard_E8ds_v7"},
			restrictions: []constraintSKURestriction{
				{family: "standardEDSv4Family", blockedZones: []string{"2", "3"}},
				{family: "standardEDSv5Family", blockedZones: []string{"2", "3"}},
			},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.dir, func(t *testing.T) {
			skus := loadConstraintSKUs(t, tt.region, tt.spec)
			runDesiredPoolsScenario(t, tt.dir, skus, tt.region, ProfileProduction)
		})
	}
}
