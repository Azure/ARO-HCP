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

package capacityreporting

import (
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpools"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpoolspec"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
	"github.com/Azure/ARO-HCP/fleet/pkg/scheduling"
	"github.com/Azure/ARO-HCP/internal/kuberesources"
	capacityreportv1alpha1 "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

// nicSecondaryCountTag mirrors agentpoolspec.SwiftSecondaryNICCountTag, the
// same tag key dev-infrastructure/modules/aks/pool.bicep sets, carrying the
// configured per-node secondary NIC count. Used as a fallback when no
// CapacityReport sample exists for a pool's SKU.
const nicSecondaryCountTag = agentpoolspec.SwiftSecondaryNICCountTag

// computeMaxCapacity derives the maximum scheduling capacity from AKS agent
// pool scaling limits and per-node allocatable data. Per-node allocatable is
// preferred from the CapacityReport CR (live observation); the SKU cache
// provides a fallback when no CR sample exists.
func computeMaxCapacity(report *capacityreportv1alpha1.CapacityReport, pools []armcontainerservice.AgentPool, skuMetadata map[string]*skucache.SKUMetadata) corev1.ResourceList {
	var nodesBySKU map[string]capacityreportv1alpha1.NodeSKUCapacity
	if report != nil {
		nodesBySKU = make(map[string]capacityreportv1alpha1.NodeSKUCapacity, len(report.Status.Nodes))
		for _, node := range report.Status.Nodes {
			nodesBySKU[node.SKU] = node
		}
	}

	max := corev1.ResourceList{}

	for _, pool := range pools {
		if pool.Properties == nil || pool.Properties.VMSize == nil || pool.Properties.Count == nil {
			continue
		}
		if agentpools.PoolRole(pool) != string(compute.PoolRoleWorker) {
			continue
		}
		vmSize := *pool.Properties.VMSize
		maxCount := agentpools.PoolMaxCount(pool)

		perNode := perNodeAllocatable(vmSize, nodesBySKU, skuMetadata, pool.Properties.Tags)
		if len(perNode) == 0 {
			continue
		}

		for name, quantity := range perNode {
			addScaled(max, name, quantity, maxCount)
		}
	}

	return max
}

// perNodeAllocatable returns the per-node scheduling-relevant allocatable
// resources for vmSize. Returns nil when no data is available.
//
// Two data sources, in priority order:
//  1. CapacityReport CR (live allocatable from ready nodes) — provides cpu,
//     memory, and swift-nic based on actual node observations. Total
//     allocatable is divided by the number of ready nodes.
//  2. Azure SKU cache + pool tags (static specs) — provides cpu and memory
//     from the VM size's SKU capabilities, and swift-nic from the pool's
//     aks-nic-secondary-count tag (overrides SKU-derived NIC count).
func perNodeAllocatable(vmSize string, nodesBySKU map[string]capacityreportv1alpha1.NodeSKUCapacity, skuMetadata map[string]*skucache.SKUMetadata, tags map[string]*string) corev1.ResourceList {
	// Prefer live allocatable from CapacityReport — reflects actual
	// kubelet-reported resources including system reservations.
	if node, found := nodesBySKU[vmSize]; found && node.Ready > 0 {
		result := corev1.ResourceList{}
		ready := int64(node.Ready)
		for _, name := range scheduling.Resources() {
			if quantity, exists := node.Allocatable[name]; exists {
				result[name] = divideQuantity(quantity, ready)
			}
		}
		if len(result) > 0 {
			return result
		}
	}

	// Fallback to SKU specs — no ready nodes yet, use theoretical per-VM
	// capacity from the Azure Resource SKUs API.
	result := corev1.ResourceList{}
	if meta, found := skuMetadata[vmSize]; found {
		skuRL := meta.ResourceList()
		for _, name := range scheduling.Resources() {
			if quantity, exists := skuRL[name]; exists {
				result[name] = quantity
			}
		}
	}
	// Pool tag overrides SKU-derived NIC count — our bicep sets the actual
	// configured secondary NIC count which may differ from the SKU maximum.
	if tagValue, found := tags[nicSecondaryCountTag]; found && tagValue != nil {
		if count, err := strconv.ParseInt(*tagValue, 10, 64); err == nil {
			result[kuberesources.SwiftNICResourceName] = *resource.NewQuantity(count, resource.DecimalSI)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// divideQuantity divides q by n. For BinarySI quantities (memory), the
// result is rounded down to the nearest Ki (1024 bytes) so that the
// serialized form retains a human-readable binary suffix. For DecimalSI
// quantities (CPU millicores, NIC counts) the result uses MilliValue to
// preserve millicore precision.
func divideQuantity(q resource.Quantity, n int64) resource.Quantity {
	if n <= 0 {
		return *resource.NewQuantity(0, q.Format)
	}
	if q.Format == resource.BinarySI {
		raw := q.Value() / n
		raw = (raw / 1024) * 1024
		return *resource.NewQuantity(raw, resource.BinarySI)
	}
	return *resource.NewMilliQuantity(q.MilliValue()/n, q.Format)
}

// addScaled adds perNode×count to list[name].
func addScaled(list corev1.ResourceList, name corev1.ResourceName, perNode resource.Quantity, count int64) {
	var scaled resource.Quantity
	if perNode.Format == resource.BinarySI {
		scaled = *resource.NewQuantity(perNode.Value()*count, perNode.Format)
	} else {
		scaled = *resource.NewMilliQuantity(perNode.MilliValue()*count, perNode.Format)
	}
	existing := list[name]
	existing.Add(scaled)
	list[name] = existing
}
