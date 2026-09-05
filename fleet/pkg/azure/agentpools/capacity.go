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
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"strings"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpoolspec"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

const CapacityTagPrefix = "arohcp-capacity-"

// ReadCapacityTags allows missing roles for initial adoption, but rejects
// malformed values, including omitted dimensions. Azure tag keys ignore case.
func ReadCapacityTags(tags map[string]*string) (compute.CapacityByRole, error) {
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
			capacity := compute.RoleCapacity{VCPUs: *fields["vcpus"], MemoryGiB: *fields["memoryGiB"], SwiftNICs: *fields["swiftNICs"]}
			if capacity.VCPUs < 0 || capacity.MemoryGiB < 0 || capacity.SwiftNICs < 0 {
				return nil, fmt.Errorf("negative capacity in tag %q", key)
			}
			result[role] = capacity
		}
	}
	return result, nil
}

// WriteCapacityTags merges role values into a previously read cluster's tags.
// If-Match prevents replacing changes made since that read. Callers decide
// whether values are an initial baseline or a newly converged capacity.
func WriteCapacityTags(ctx context.Context, client *armcontainerservice.ManagedClustersClient, resourceGroup, clusterName string, cluster armcontainerservice.ManagedCluster, capacities compute.CapacityByRole) error {
	existing, err := ReadCapacityTags(cluster.Tags)
	if err != nil {
		return err
	}
	tags := maps.Clone(cluster.Tags)
	if tags == nil {
		tags = map[string]*string{}
	}
	changed := false
	for role, capacity := range capacities {
		if previous, ok := existing[role]; ok && previous == capacity {
			continue
		}
		key := CapacityTagPrefix + string(role)
		for liveKey := range tags {
			if strings.EqualFold(liveKey, key) {
				key = liveKey
				break
			}
		}
		value, err := json.Marshal(capacity)
		if err != nil {
			return err
		}
		tags[key] = ptr.To(string(value))
		changed = true
	}
	if !changed {
		return nil
	}
	if cluster.ETag == nil || len(*cluster.ETag) == 0 {
		return fmt.Errorf("cluster has no ETag for capacity baseline update")
	}
	poller, err := client.BeginUpdateTags(ctx, resourceGroup, clusterName, armcontainerservice.TagsObject{Tags: tags},
		&armcontainerservice.ManagedClustersClientBeginUpdateTagsOptions{IfMatch: cluster.ETag})
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

// ObservedPoolCapacities rejects incomplete ARM/SKU data instead of recording
// a smaller baseline. Swift capacity comes from the pool's configured NIC tag.
func ObservedPoolCapacities(pools []armcontainerservice.AgentPool, metadata map[string]*skucache.SKUMetadata) (compute.CapacityByRole, error) {
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
		if swift && compute.PoolRole(PoolRole(pool)) == compute.PoolRoleWorker {
			nics, err := strconv.ParseInt(ptr.Deref(p.Tags[agentpoolspec.SwiftSecondaryNICCountTag], ""), 10, 64)
			if err != nil || nics <= 0 {
				return nil, fmt.Errorf("invalid Swift NIC count for pool %q", *pool.Name)
			}
			spec.SecondaryNICs = nics
		}
		observed = append(observed, compute.Pool{Role: compute.PoolRole(PoolRole(pool)), Name: *pool.Name, Spec: spec, MaxCount: int32(PoolMaxCount(pool)), EnableSwift: swift})
	}
	return compute.PoolCapacities(observed)
}
