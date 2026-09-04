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
	"math"

	"k8s.io/apimachinery/pkg/util/sets"
)

// BudgetStrategy returns per-family vCPU limits and current usage.
// fetchQuotaUsage lazily fetches quota from the Azure Compute Usage API;
// strategies that don't need quota (e.g. UnlimitedBudget) ignore it.
type BudgetStrategy func(ctx context.Context, families sets.Set[VMFamily], fetchQuotaUsage FetchQuotaUsageFunc) (map[VMFamily]QuotaUsage, error)

// QuotaUsage holds both the limit and current usage for a VM family quota.
type QuotaUsage struct {
	Limit        int64
	CurrentValue int64
}

// SubscriptionQuotaBudget returns Azure subscription quota limits and usage.
// All subscription usage is assumed to belong to pools managed by this planner.
// Limits determine desired capacity; current usage constrains additional growth.
func SubscriptionQuotaBudget(ctx context.Context, families sets.Set[VMFamily], fetchQuotaUsage FetchQuotaUsageFunc) (map[VMFamily]QuotaUsage, error) {
	return fetchQuotaUsage(ctx, families)
}

// UnlimitedBudget returns an unconstrained budget for every requested family.
// Use in environments where quota is externally guaranteed (CI, dev).
func UnlimitedBudget(_ context.Context, families sets.Set[VMFamily], _ FetchQuotaUsageFunc) (map[VMFamily]QuotaUsage, error) {
	budgets := make(map[VMFamily]QuotaUsage, families.Len())
	for _, family := range families.UnsortedList() {
		budgets[family] = QuotaUsage{Limit: math.MaxInt64}
	}
	return budgets, nil
}
