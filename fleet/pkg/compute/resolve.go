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
	"fmt"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// FetchQuotaUsageFunc fetches per-family vCPU quota usage for the given set
// of families. The shadow controller scopes the client to the observed
// cluster's subscription; allocation itself only needs the returned usage.
type FetchQuotaUsageFunc func(ctx context.Context, families sets.Set[VMFamily]) (map[VMFamily]QuotaUsage, error)

// DesiredPoolsResult bundles the proposed pools with SKU metadata and quota
// headroom needed to project live AKS pools and simulate their transition.
type DesiredPoolsResult struct {
	Pools    []Pool
	Failures []AllocationFailure
	// FullyAllocated means every configured tier reached its node target,
	// including the configured number of pools clamped to available zones.
	FullyAllocated bool
	SKUMetadata    map[string]*skucache.SKUMetadata

	// AvailableVCPUs is the unused quota per family before accounting for
	// unused autoscaler reservations in current pools.
	AvailableVCPUs map[VMFamily]int64
}

// ResolveDesiredPools computes the desired node pool set for a profile: SKU
// metadata lookup, per-family vCPU budgets, SKU eligibility indexing, and
// desired-pool allocation. The shadow nodepool controller uses this path;
// aks-cluster-create continues provisioning and reconciling its static config.
func ResolveDesiredPools(
	ctx context.Context,
	skuCache *skucache.SKUCache,
	subscriptionID string,
	profile Profile,
	zones []string,
	fetchQuotaUsage FetchQuotaUsageFunc,
) (DesiredPoolsResult, error) {
	logger := utils.LoggerFromContext(ctx)

	skuMetadata, err := skuCache.SKUMetadataByVMSize(ctx, subscriptionID)
	if err != nil {
		return DesiredPoolsResult{}, fmt.Errorf("fetching SKU metadata: %w", err)
	}

	families := TierFamilies(profile.Tiers)
	quotaUsages, err := profile.BudgetStrategy(ctx, families, fetchQuotaUsage)
	if err != nil {
		return DesiredPoolsResult{}, fmt.Errorf("computing family budgets: %w", err)
	}
	familyLimits := make(map[VMFamily]int64, len(quotaUsages))
	availableVCPUs := make(map[VMFamily]int64, len(quotaUsages))
	for family, usage := range quotaUsages {
		familyLimits[family] = usage.Limit
		availableVCPUs[family] = max(usage.Limit-usage.CurrentValue, 0)
	}

	skuIndex := BuildEligibleSKUIndex(skuMetadata)
	pools, failures, fullyAllocated := ComputeDesiredPools(logger, profile.Tiers, zones, familyLimits, skuIndex)
	return DesiredPoolsResult{
		Pools:          pools,
		Failures:       failures,
		FullyAllocated: fullyAllocated,
		SKUMetadata:    skuMetadata,
		AvailableVCPUs: availableVCPUs,
	}, nil
}
