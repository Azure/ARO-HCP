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

package agentpools

import (
	"fmt"
	"strconv"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpoolspec"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

// ObservedPoolCapacities derives capacity from inline agent pool profiles
// embedded in the ManagedCluster response. It rejects incomplete ARM/SKU data
// instead of recording a smaller baseline. Swift capacity comes from the
// pool's configured NIC tag.
func ObservedPoolCapacities(pools []*armcontainerservice.ManagedClusterAgentPoolProfile, metadata map[string]*skucache.SKUMetadata) (compute.CapacityByRole, error) {
	var observed []compute.Pool
	for _, pool := range pools {
		if pool == nil {
			return nil, fmt.Errorf("nil pool profile in cluster response")
		}
		if !IsManagedPoolProfile(pool) {
			continue
		}
		role := compute.PoolRole(RoleFromAgentPoolProfile(pool))
		if pool.Name == nil || pool.VMSize == nil || pool.Count == nil || pool.EnableAutoScaling == nil || (*pool.EnableAutoScaling && pool.MaxCount == nil) {
			return nil, fmt.Errorf("incomplete ARM capacity data for pool %q", ptr.Deref(pool.Name, ""))
		}
		meta := metadata[*pool.VMSize]
		if meta == nil {
			return nil, fmt.Errorf("missing SKU metadata for pool %q (%s)", *pool.Name, *pool.VMSize)
		}
		spec := compute.NewVMSpecFromSKU(meta)
		swiftEnabled := ptr.Deref(pool.Tags[agentpoolspec.SwiftMultiTenancyTag], "") == agentpoolspec.SwiftMultiTenancyEnabledValue
		// we only care about Swift capacity for worker pools - the system pool is swift enabled by necessity
		// but we don't use the NICs.
		if swiftEnabled && role == compute.PoolRoleWorker {
			nics, err := strconv.ParseInt(ptr.Deref(pool.Tags[agentpoolspec.SwiftSecondaryNICCountTag], ""), 10, 64)
			if err != nil || nics <= 0 {
				return nil, fmt.Errorf("invalid Swift NIC count for pool %q", *pool.Name)
			}
			spec.SecondaryNICs = nics
		}
		maxCount := int64(*pool.Count)
		if *pool.EnableAutoScaling && pool.MaxCount != nil {
			maxCount = int64(*pool.MaxCount)
		}
		observed = append(observed, compute.Pool{Role: role, Name: *pool.Name, Spec: spec, MaxCount: int32(maxCount), EnableSwift: swiftEnabled})
	}
	return compute.PoolCapacities(observed)
}
