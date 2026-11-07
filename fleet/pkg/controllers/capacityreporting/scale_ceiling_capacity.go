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
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpools"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpoolspec"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	"github.com/Azure/ARO-HCP/fleet/pkg/scheduling"
	"github.com/Azure/ARO-HCP/internal/kuberesources"
	capacityreportv1alpha1 "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

// computeMaxCapacity derives the maximum scheduling capacity from AKS agent
// pool scaling limits and per-node allocatable data. Per-node allocatable is
// collected from a successfully observed, current CapacityReport. Only absence
// of a ready-node sample in that report permits SKU bootstrap. Known observed
// contributions are retained alongside errors describing unavailable inputs.
func computeMaxCapacity(report *capacityreportv1alpha1.CapacityReport, pools []armcontainerservice.AgentPool, skuMetadata map[string]*skucache.SKUMetadata) (corev1.ResourceList, error) {
	max := corev1.ResourceList{}
	var problems []error
	workers := 0
	nodes := reportNodesBySKU(report)
	for _, pool := range pools {
		if pool.Properties == nil {
			problems = append(problems, fmt.Errorf("agent pool properties unavailable"))
			continue
		}
		if !agentpools.IsWorkerPool(pool) {
			continue
		}
		workers++
		if pool.Properties.VMSize == nil || pool.Properties.Count == nil {
			problems = append(problems, fmt.Errorf("worker pool VM size or count unavailable"))
			continue
		}
		if report == nil {
			problems = append(problems, fmt.Errorf("no usable CapacityReport observation"))
			continue
		}
		vmSize := *pool.Properties.VMSize
		maxCount := agentpools.PoolMaxCount(pool)
		if maxCount < 0 {
			problems = append(problems, fmt.Errorf("worker pool %s has negative node ceiling", vmSize))
			continue
		}
		perNode, err := perNodeAllocatable(vmSize, nodes, skuMetadata, pool.Properties.Tags)
		if err != nil {
			problems = append(problems, fmt.Errorf("worker pool %s: %w", vmSize, err))
		}
		for name, quantity := range perNode {
			addScaled(max, name, quantity, maxCount)
		}
	}
	if workers == 0 && len(problems) == 0 {
		for _, name := range scheduling.Resources() {
			max[name] = resource.MustParse("0")
		}
	}
	return max, errors.Join(problems...)
}

func reportNodesBySKU(report *capacityreportv1alpha1.CapacityReport) map[string]capacityreportv1alpha1.NodeSKUCapacity {
	nodes := map[string]capacityreportv1alpha1.NodeSKUCapacity{}
	if report != nil {
		for _, node := range report.Status.Nodes {
			nodes[node.SKU] = node
		}
	}
	return nodes
}

func needsSKUBootstrap(report *capacityreportv1alpha1.CapacityReport, pools []armcontainerservice.AgentPool) bool {
	if report == nil {
		return false
	}
	nodes := reportNodesBySKU(report)
	for _, pool := range pools {
		if !agentpools.IsWorkerPool(pool) || pool.Properties.VMSize == nil || pool.Properties.Count == nil {
			continue
		}
		node, found := nodes[*pool.Properties.VMSize]
		if !found || node.Ready == 0 {
			return true
		}
	}
	return false
}

func perNodeAllocatable(vmSize string, nodesBySKU map[string]capacityreportv1alpha1.NodeSKUCapacity, skuMetadata map[string]*skucache.SKUMetadata, tags map[string]*string) (corev1.ResourceList, error) {
	result := corev1.ResourceList{}
	var problems []error
	if node, found := nodesBySKU[vmSize]; found && node.Ready != 0 {
		if node.Ready < 0 {
			return nil, fmt.Errorf("negative ready-node count")
		}
		for _, name := range scheduling.Resources() {
			if quantity, exists := node.Allocatable[name]; exists && quantity.Sign() >= 0 {
				result[name] = divideQuantity(quantity, int64(node.Ready))
			} else {
				problems = append(problems, fmt.Errorf("observed %s allocatable unavailable", name))
			}
		}
		return result, errors.Join(problems...)
	}
	if metadata := skuMetadata[vmSize]; metadata != nil {
		for name, quantity := range metadata.ResourceList() {
			if (name == corev1.ResourceCPU || name == corev1.ResourceMemory) && quantity.Sign() >= 0 {
				result[name] = quantity
			}
		}
	}
	for _, name := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
		if _, exists := result[name]; !exists {
			problems = append(problems, fmt.Errorf("SKU %s %s unavailable", vmSize, name))
		}
	}
	if nics, present, err := agentpoolspec.SecondaryNICCount(tags); err != nil {
		problems = append(problems, err)
	} else if present {
		result[kuberesources.SwiftNICResourceName] = *resource.NewQuantity(nics, resource.DecimalSI)
	}
	return result, errors.Join(problems...)
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
