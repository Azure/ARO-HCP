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
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpoolspec"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

const CapacityTagPrefix = "arohcp-capacity-"

// ParseCapacityTags allows missing roles for initial adoption, but rejects
// malformed values, including omitted dimensions. Azure tag keys ignore case.
func ParseCapacityTags(tags map[string]*string) (compute.CapacityByRole, error) {
	result := compute.CapacityByRole{}
	for key, value := range tags {
		for _, role := range compute.CapacityRoles {
			if !strings.EqualFold(key, CapacityTagPrefix+string(role)) {
				continue
			}
			if _, duplicate := result[role]; duplicate {
				return nil, fmt.Errorf("duplicate capacity tag for %s", role)
			}
			var fields map[string]*int64
			if value == nil || len(*value) > 256 || json.Unmarshal([]byte(ptr.Deref(value, "")), &fields) != nil || len(fields) != 3 ||
				fields["vcpus"] == nil || fields["memoryGiB"] == nil || fields["swiftNICs"] == nil {
				return nil, fmt.Errorf("invalid capacity tag %q", key)
			}
			capacity := compute.RoleCapacity{VCPUs: *fields["vcpus"], MemoryBytes: *fields["memoryGiB"] << 30, SwiftNICs: *fields["swiftNICs"]}
			if capacity.VCPUs < 0 || capacity.MemoryBytes < 0 || capacity.SwiftNICs < 0 {
				return nil, fmt.Errorf("negative capacity in tag %q", key)
			}
			result[role] = capacity
		}
	}
	return result, nil
}

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
		if pool.Name == nil || pool.VMSize == nil || pool.Count == nil || pool.EnableAutoScaling == nil || (*pool.EnableAutoScaling && pool.MaxCount == nil) {
			return nil, fmt.Errorf("incomplete ARM capacity data for pool %q", ptr.Deref(pool.Name, ""))
		}
		meta := metadata[*pool.VMSize]
		if meta == nil {
			return nil, fmt.Errorf("missing SKU metadata for pool %q (%s)", *pool.Name, *pool.VMSize)
		}
		spec := compute.NewVMSpecFromSKU(meta)
		swift := ptr.Deref(pool.Tags[agentpoolspec.SwiftMultiTenancyTag], "") == agentpoolspec.SwiftMultiTenancyEnabledValue
		if swift && compute.PoolRole(RoleFromAgentPoolProfile(pool)) == compute.PoolRoleWorker {
			nics, err := strconv.ParseInt(ptr.Deref(pool.Tags[agentpoolspec.SwiftSecondaryNICCountTag], ""), 10, 64)
			if err != nil || nics <= 0 {
				return nil, fmt.Errorf("invalid Swift NIC count for pool %q", *pool.Name)
			}
			spec.SecondaryNICs = nics
		}
		maxCount := int64(*pool.Count)
		if *pool.EnableAutoScaling {
			maxCount = int64(*pool.MaxCount)
		}
		observed = append(observed, compute.Pool{Role: compute.PoolRole(RoleFromAgentPoolProfile(pool)), Name: *pool.Name, Spec: spec, MaxCount: int32(maxCount), EnableSwift: swift})
	}
	return compute.PoolCapacities(observed)
}

// ObservedAgentPoolCapacities derives capacity from the AgentPool list returned
// by the AgentPools client (the controller's live reconcile path). It rejects
// incomplete ARM/SKU data instead of undercounting current capacity. Swift
// capacity comes from the pool's configured NIC tag.
func ObservedAgentPoolCapacities(pools []armcontainerservice.AgentPool, metadata map[string]*skucache.SKUMetadata) (compute.CapacityByRole, error) {
	var observed []compute.Pool
	for _, pool := range pools {
		if !IsManagedPool(pool) {
			continue
		}
		p := pool.Properties
		if pool.Name == nil || p.VMSize == nil || p.Count == nil || p.EnableAutoScaling == nil || (*p.EnableAutoScaling && p.MaxCount == nil) {
			return nil, fmt.Errorf("incomplete ARM capacity data for pool %q", ptr.Deref(pool.Name, ""))
		}
		meta := metadata[*p.VMSize]
		if meta == nil {
			return nil, fmt.Errorf("missing SKU metadata for pool %q (%s)", *pool.Name, *p.VMSize)
		}
		spec := compute.NewVMSpecFromSKU(meta)
		swift := ptr.Deref(p.Tags[agentpoolspec.SwiftMultiTenancyTag], "") == agentpoolspec.SwiftMultiTenancyEnabledValue
		if swift && compute.PoolRole(RoleFromAgentPool(pool)) == compute.PoolRoleWorker {
			nics, err := strconv.ParseInt(ptr.Deref(p.Tags[agentpoolspec.SwiftSecondaryNICCountTag], ""), 10, 64)
			if err != nil || nics <= 0 {
				return nil, fmt.Errorf("invalid Swift NIC count for pool %q", *pool.Name)
			}
			spec.SecondaryNICs = nics
		}
		observed = append(observed, compute.Pool{Role: compute.PoolRole(RoleFromAgentPool(pool)), Name: *pool.Name, Spec: spec, MaxCount: int32(PoolMaxCount(pool)), EnableSwift: swift})
	}
	return compute.PoolCapacities(observed)
}
