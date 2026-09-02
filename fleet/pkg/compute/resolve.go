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
// of families. Callers differ in how they obtain a quota client (e.g. a
// short-lived client scoped to the reconciled cluster's subscription in the
// controller vs. a long-lived client in a standalone tool); ResolveDesiredPools
// only needs the result.
type FetchQuotaUsageFunc func(ctx context.Context, families sets.Set[VMFamily]) (map[VMFamily]QuotaUsage, error)

// DesiredPoolsResult bundles the outputs of ResolveDesiredPools. SKUMetadata
// and FamilyBudgets are exposed alongside Pools and Failures because some
// callers (the nodepool controller) also need them to project current
// AKS state and compute action headroom; callers that don't (aks-cluster-create)
// simply ignore those fields.
type DesiredPoolsResult struct {
	Pools         []Pool
	Failures      []AllocationFailure
	SKUMetadata   map[string]*skucache.SKUMetadata
	FamilyBudgets map[VMFamily]int64
}

// ResolveDesiredPools computes the desired node pool set for a profile: SKU
// metadata lookup, per-family vCPU budgets, SKU eligibility indexing, and
// desired-pool allocation. It is the single orchestration path shared by the
// nodepool controller (steady-state reconcile) and the aks-cluster-create
// tool (initial cluster provisioning), so both compute pools the same way.
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
	familyBudgets, err := profile.BudgetStrategy(ctx, families, fetchQuotaUsage)
	if err != nil {
		return DesiredPoolsResult{}, fmt.Errorf("computing family budgets: %w", err)
	}

	skuIndex := BuildEligibleSKUIndex(skuMetadata, zones)
	pools, failures := ComputeDesiredPools(logger, profile.Tiers, zones, familyBudgets, skuIndex)
	return DesiredPoolsResult{
		Pools:         pools,
		Failures:      failures,
		SKUMetadata:   skuMetadata,
		FamilyBudgets: familyBudgets,
	}, nil
}
