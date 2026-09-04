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

// Package quota fetches Azure subscription vCPU quota usage for VM families.
// It has no dependency on controller machinery so it can be shared between
// the nodepool controller and standalone tools (e.g. the AKS cluster
// creation tool) without pulling in unrelated dependencies.
package quota

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"

	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

// FetchUsage queries the Azure Compute Usage API and returns quota limits and
// current usage for the specified VM families.
func FetchUsage(ctx context.Context, client *armcompute.UsageClient, region string, families sets.Set[compute.VMFamily]) (map[compute.VMFamily]compute.QuotaUsage, error) {
	pager := client.NewListPager(region, nil)
	usages := make(map[compute.VMFamily]compute.QuotaUsage, families.Len())
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing compute usages: %w", err)
		}
		for _, usage := range page.Value {
			if usage == nil || usage.Name == nil || usage.Name.Value == nil || usage.Limit == nil {
				continue
			}
			family := compute.VMFamily(*usage.Name.Value)
			if !families.Has(family) {
				continue
			}
			var currentValue int64
			if usage.CurrentValue != nil {
				currentValue = int64(*usage.CurrentValue)
			}
			usages[family] = compute.QuotaUsage{
				Limit:        *usage.Limit,
				CurrentValue: currentValue,
			}
		}
	}
	return usages, nil
}
