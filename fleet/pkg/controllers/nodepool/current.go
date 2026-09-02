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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpools"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpoolspec"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

// currentPoolStates projects live AKS agent pools into []PoolState for
// use by findNextAction. Only pools with a role label (system, infra,
// worker) are included. SKU metadata is used to populate Family and
// VCPUsPerNode from the pool's VMSize.
func currentPoolStates(pools []armcontainerservice.AgentPool, skuMetadata map[string]*skucache.SKUMetadata) []PoolState {
	var result []PoolState
	for _, pool := range pools {
		if !agentpools.IsManagedPool(pool) {
			continue
		}
		if pool.Properties == nil || pool.Name == nil || pool.Properties.VMSize == nil || pool.Properties.OSDiskSizeGB == nil {
			continue
		}

		role := compute.PoolRole(agentpools.PoolRole(pool))

		var zones []string
		for _, z := range pool.Properties.AvailabilityZones {
			if z != nil {
				zones = append(zones, *z)
			}
		}

		var provisioningState string
		if pool.Properties.ProvisioningState != nil {
			provisioningState = *pool.Properties.ProvisioningState
		}

		var autoScaling bool
		if pool.Properties.EnableAutoScaling != nil {
			autoScaling = *pool.Properties.EnableAutoScaling
		}

		var count int32
		if pool.Properties.Count != nil {
			count = *pool.Properties.Count
		}

		var etag string
		if pool.Properties.ETag != nil {
			etag = *pool.Properties.ETag
		}

		vmSize := *pool.Properties.VMSize
		// Fallback spec with VCPUs==0 when SKU metadata is unavailable. The
		// zero-vCPU sentinel is intentional: computeFamilyHeadroom skips such
		// pools so their quota is not misattributed, and unresolvedSKUSizes
		// surfaces them for operator visibility.
		spec := compute.VMSpec{Size: vmSize}
		if meta, ok := skuMetadata[vmSize]; ok {
			spec = compute.NewVMSpecFromSKU(meta)
		}

		labels := make(map[string]string, len(pool.Properties.NodeLabels))
		for k, v := range pool.Properties.NodeLabels {
			if v != nil {
				labels[k] = *v
			}
		}

		var taints []string
		for _, t := range pool.Properties.NodeTaints {
			if t != nil {
				taints = append(taints, *t)
			}
		}

		var maxPods int32
		if pool.Properties.MaxPods != nil {
			maxPods = *pool.Properties.MaxPods
		}

		enableSwift := hasSwiftTags(pool)

		result = append(result, PoolState{
			Pool: compute.Pool{
				Role:              role,
				Name:              *pool.Name,
				Spec:              spec,
				AvailabilityZones: zones,
				MaxCount:          int32(agentpools.PoolMaxCount(pool)),
				OSDiskSizeGB:      *pool.Properties.OSDiskSizeGB,
				MaxPods:           maxPods,
				Labels:            labels,
				Taints:            taints,
				EnableSwift:       enableSwift,
			},
			ETag:               etag,
			ProvisioningState:  provisioningState,
			AutoScalingEnabled: autoScaling,
			Count:              count,
		})
	}
	return result
}

// unresolvedSKUSizes returns the VM sizes of current pools whose SKU metadata
// could not be resolved (VCPUs projected as 0). Such pools are excluded from
// quota headroom in computeFamilyHeadroom, so the controller surfaces them for
// operator visibility.
func unresolvedSKUSizes(current []PoolState) []string {
	var sizes []string
	for _, pool := range current {
		if pool.Spec.VCPUs == 0 {
			sizes = append(sizes, pool.Spec.Size)
		}
	}
	return sizes
}

func hasSwiftTags(pool armcontainerservice.AgentPool) bool {
	if pool.Properties == nil || pool.Properties.Tags == nil {
		return false
	}
	v := pool.Properties.Tags[agentpoolspec.SwiftMultiTenancyTag]
	return v != nil && *v == agentpoolspec.SwiftMultiTenancyEnabledValue
}
