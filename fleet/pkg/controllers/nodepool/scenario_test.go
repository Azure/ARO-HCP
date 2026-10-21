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

package nodepool

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	armcomputefake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

// newScenarioSKUCache builds a SKUCache backed by the official Azure SDK
// fake transport, serving skus for every Resource SKUs list call regardless
// of filter. Mirrors compute's newResolveTestCache so both packages validate
// the same planner inputs against the real *armcompute.ResourceSKUsClient.
func newScenarioSKUCache(t *testing.T, region string, skus []*armcompute.ResourceSKU) *skucache.SKUCache {
	t.Helper()
	srv := armcomputefake.ResourceSKUsServer{
		NewListPager: func(options *armcompute.ResourceSKUsClientListOptions) (resp azfake.PagerResponder[armcompute.ResourceSKUsClientListResponse]) {
			resp.AddPage(http.StatusOK, armcompute.ResourceSKUsClientListResponse{
				ResourceSKUsResult: armcompute.ResourceSKUsResult{Value: skus},
			}, nil)
			return
		},
	}
	transport := armcomputefake.NewResourceSKUsServerTransport(&srv)
	return skucache.NewSKUCache(region, &azfake.TokenCredential{}, &policy.ClientOptions{Transport: transport}, nil)
}

// scenarioQuotaUsageFetcher mirrors compute's runDesiredPoolsScenario: a
// family matches its usage entry by exact name.
func scenarioQuotaUsageFetcher(usages []*armcompute.Usage) compute.FetchQuotaUsageFunc {
	usage := make(map[compute.VMFamily]compute.QuotaUsage, len(usages))
	for _, u := range usages {
		usage[compute.VMFamily(*u.Name.Value)] = compute.QuotaUsage{Limit: *u.Limit, CurrentValue: int64(*u.CurrentValue)}
	}
	return func(_ context.Context, families sets.Set[compute.VMFamily]) (map[compute.VMFamily]compute.QuotaUsage, error) {
		result := make(map[compute.VMFamily]compute.QuotaUsage)
		for family := range families {
			if u, ok := usage[family]; ok {
				result[family] = u
			}
		}
		return result, nil
	}
}

// TestShadowSyncOnceScenario runs the shadow controller's planning pipeline
// (compute.ResolveDesiredPools, currentPoolStates, simulateAndTrace — the
// same calls SyncOnce makes internally) against Resource SKUs, quota usages,
// and the agent pools of a management cluster dumped from real systems, one
// directory per region under compute/testdata/scenarios, each with the
// profile its environment runs. The dumps hold the raw ARM responses,
// trimmed to the fields the planner reads. The SKUs and usages are shared
// with TestResolveDesiredPools_Scenario. Pins the full migration from each
// cluster's captured topology to its desired state.
//
// SyncOnce's own ARM-orchestration layer (cluster lookup, provisioning-state
// checks, error handling) is exercised separately by TestShadowSyncOnceReadOnly
// and friends in controller_test.go, so this test calls the planning and
// simulation steps directly instead of going through a fake HTTP transport.
func TestShadowSyncOnceScenario(t *testing.T) {
	tests := []struct {
		region  string
		profile string
	}{
		{region: "uksouth", profile: compute.ProfileProduction},
		// Quota less the running vCPUs of the subscription's other clusters.
		{region: "westus3", profile: compute.ProfileIntegration},
	}

	for _, tt := range tests {
		t.Run(tt.region, func(t *testing.T) {
			scenario := filepath.Join("..", "..", "compute", "testdata", "scenarios", tt.region)
			rawSKUs, err := os.ReadFile(filepath.Join(scenario, "scenario-skus.json"))
			require.NoError(t, err, "reading Resource SKUs")
			var skus []*armcompute.ResourceSKU
			require.NoError(t, json.Unmarshal(rawSKUs, &skus), "decoding Resource SKUs")

			rawUsages, err := os.ReadFile(filepath.Join(scenario, "scenario-usages.json"))
			require.NoError(t, err, "reading usages")
			var usages []*armcompute.Usage
			require.NoError(t, json.Unmarshal(rawUsages, &usages), "decoding usages")

			rawPools, err := os.ReadFile(filepath.Join(scenario, "scenario-agentpools.json"))
			require.NoError(t, err, "reading agent pools")
			var pools []armcontainerservice.AgentPool
			require.NoError(t, json.Unmarshal(rawPools, &pools), "decoding agent pools")

			profile, ok := compute.LookupProfile(tt.profile)
			require.True(t, ok, "profile %q must exist", tt.profile)

			skuCache := newScenarioSKUCache(t, tt.region, skus)
			resolved, err := compute.ResolveDesiredPools(testSyncOnceContext(), skuCache, syncOnceTestSubscriptionID, profile,
				[]string{"1", "2", "3"}, poolZonesByRole(pools), scenarioQuotaUsageFetcher(usages))
			require.NoError(t, err, "resolving desired pools")

			current, err := currentPoolStates(pools, resolved.SKUMetadata)
			require.NoError(t, err, "projecting current pool states")

			tr, err := simulateAndTrace(resolved.Pools, current, resolved.AvailableVCPUs, resolved.FullyAllocated, nodePoolSimulationMaxCycles)
			require.NoError(t, err, "simulating projection")
			require.Equal(t, "converged", tr.Outcome)
			compareGolden(t, formatTrace(tr))
		})
	}
}

// constraintSKURestriction and constraintSKUSpec mirror the compute
// package's test helpers of the same name (pkg/compute/resolve_test.go):
// every named condition shares one SKU catalog
// (compute/testdata/scenarios/constraints-skus-base.json), and each
// scenario here names only its own deviation from it. Duplicated rather
// than imported since _test.go helpers are not importable across packages.
type constraintSKURestriction struct {
	family       compute.VMFamily
	location     bool
	blockedZones []string
}

type constraintSKUSpec struct {
	exclude      []string
	restrictions []constraintSKURestriction
	zones        map[string][]string
}

// loadConstraintSKUsJSON builds the raw Resource SKUs JSON for a named
// constraint condition from the shared base catalog, applying spec's
// exclusions, zone overrides, and restrictions. See
// compute.loadConstraintSKUs for the overlay semantics.
func loadConstraintSKUsJSON(t *testing.T, region string, spec constraintSKUSpec) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "compute", "testdata", "scenarios", "constraints-skus-base.json"))
	require.NoError(t, err, "reading constraint SKU base catalog")
	var skus []*armcompute.ResourceSKU
	require.NoError(t, json.Unmarshal(raw, &skus), "decoding constraint SKU base catalog")

	excluded := sets.New(spec.exclude...)
	restrictionsByFamily := make(map[compute.VMFamily]constraintSKURestriction, len(spec.restrictions))
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
		if r, ok := restrictionsByFamily[compute.VMFamily(*sku.Family)]; ok {
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
	out, err := json.Marshal(result)
	require.NoError(t, err, "encoding constraint SKUs")
	return out
}

// TestShadowSyncOnceConstraints runs the shadow controller's planning
// pipeline against a fixed set of named quota and SKU-restriction conditions
// (Resource SKUs, quota usages, and agent pools), all running
// ProfileProduction per config/config.yaml's fleet.nodePoolPlanning default.
// Scenario directories are shared with TestResolveDesiredPools_Constraints in
// the compute package. Each fixture is currently trimmed from a real
// subscription, but the condition it pins — not its provenance — is what the
// test guards. Every named condition here resolves without allocation
// failures (see the matching TestResolveDesiredPools_Constraints fixture),
// so the shadow controller converges in every case.
//
// Each scenario is named after the condition it pins rather than its source
// region. `region` still carries a real Azure region name, required for
// isLocationRestricted to evaluate a condition's Location-type restrictions
// correctly.
//
// Like TestShadowSyncOnceScenario, this calls the planning and simulation
// steps directly rather than through SyncOnce's fake-HTTP-transport path;
// SyncOnce's ARM-orchestration layer is covered separately.
//
// canadacentral and switzerlandnorth dumps existed but were dropped: both were
// byte-identical to each other and nearly identical to australiaeast/
// tight_quota_margin (same SKU catalog, topology, and outcome, differing only
// in a quota number), so they added no coverage beyond it.
func TestShadowSyncOnceConstraints(t *testing.T) {
	tests := []struct {
		dir    string
		region string
		spec   constraintSKUSpec
	}{
		{dir: "tight_quota_margin", region: "australiaeast", spec: constraintSKUSpec{
			exclude: []string{
				"Standard_E16ds_v7", "Standard_E32ds_v7", "Standard_E4ds_v7", "Standard_E8ds_v7",
			},
		}},
		{dir: "single_zone_blocked_family", region: "brazilsouth", spec: constraintSKUSpec{
			restrictions: []constraintSKURestriction{
				{family: "standardEDSv5Family", blockedZones: []string{"2"}},
				{family: "StandardEdsv7Family", blockedZones: []string{"1", "3"}},
			},
		}},
		{dir: "unrestricted_baseline", region: "centralindia", spec: constraintSKUSpec{
			exclude: []string{"Standard_E16ds_v7", "Standard_E32ds_v7", "Standard_E4ds_v7", "Standard_E8ds_v7"},
		}},
		// With nothing running yet, the allocated part of the profile can converge.
		{dir: "fresh_cluster_quota_exhausted", region: "eastus", spec: constraintSKUSpec{
			restrictions: []constraintSKURestriction{
				{family: "standardEDSv5Family", location: true, blockedZones: []string{"1", "2", "3"}},
				{family: "StandardEdsv6Family", blockedZones: []string{"1", "2", "3"}},
				{family: "standardESv3Family", location: true, blockedZones: []string{"1", "2", "3"}},
			},
		}},
		{dir: "location_blocked_family", region: "eastus2", spec: constraintSKUSpec{
			restrictions: []constraintSKURestriction{
				{family: "standardEDSv4Family", location: true, blockedZones: []string{"1", "2", "3"}},
				{family: "standardEDSv5Family", location: true, blockedZones: []string{"1", "2", "3"}},
				{family: "StandardEdsv6Family", blockedZones: []string{"2"}},
			},
		}},
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
			scenario := filepath.Join("..", "..", "compute", "testdata", "scenarios", tt.dir)
			skuBytes := loadConstraintSKUsJSON(t, tt.region, tt.spec)
			var skus []*armcompute.ResourceSKU
			require.NoError(t, json.Unmarshal(skuBytes, &skus), "decoding Resource SKUs")

			rawUsages, err := os.ReadFile(filepath.Join(scenario, "scenario-usages.json"))
			require.NoError(t, err, "reading usages")
			var usages []*armcompute.Usage
			require.NoError(t, json.Unmarshal(rawUsages, &usages), "decoding usages")

			rawPools, err := os.ReadFile(filepath.Join(scenario, "scenario-agentpools.json"))
			require.NoError(t, err, "reading agent pools")
			var pools []armcontainerservice.AgentPool
			require.NoError(t, json.Unmarshal(rawPools, &pools), "decoding agent pools")

			profile, ok := compute.LookupProfile(compute.ProfileProduction)
			require.True(t, ok, "profile %q must exist", compute.ProfileProduction)

			skuCache := newScenarioSKUCache(t, tt.region, skus)
			resolved, err := compute.ResolveDesiredPools(testSyncOnceContext(), skuCache, syncOnceTestSubscriptionID, profile,
				[]string{"1", "2", "3"}, poolZonesByRole(pools), scenarioQuotaUsageFetcher(usages))
			require.NoError(t, err, "resolving desired pools")

			current, err := currentPoolStates(pools, resolved.SKUMetadata)
			require.NoError(t, err, "projecting current pool states")

			tr, err := simulateAndTrace(resolved.Pools, current, resolved.AvailableVCPUs, resolved.FullyAllocated, nodePoolSimulationMaxCycles)
			require.NoError(t, err, "simulating projection")
			require.Equal(t, "converged", tr.Outcome)
			compareGolden(t, formatTrace(tr))
		})
	}
}
