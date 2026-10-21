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
	corev1 "k8s.io/api/core/v1"

	"github.com/Azure/ARO-HCP/fleet/pkg/scheduling"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	capacityreportv1alpha1 "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

func filterSchedulingResources(source corev1.ResourceList) corev1.ResourceList {
	if len(source) == 0 {
		return nil
	}
	filtered := corev1.ResourceList{}
	for _, name := range scheduling.Resources() {
		if quantity, ok := source[name]; ok {
			filtered[name] = quantity
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

// ComputeObservedResources derives scheduling-relevant observed resources from
// a CapacityReport CR: node capacity, HCP workload usage/requests. Only
// scheduling-relevant resources (cpu, memory, swift-nic) are propagated.
func ComputeObservedResources(report *capacityreportv1alpha1.CapacityReport) fleetapi.ObservedResources {
	capacity := corev1.ResourceList{}
	for _, node := range report.Status.Nodes {
		for _, name := range scheduling.Resources() {
			if quantity, ok := node.Allocatable[name]; ok {
				existing := capacity[name]
				existing.Add(quantity)
				capacity[name] = existing
			}
		}
	}

	usage := filterSchedulingResources(report.Status.Usage)
	requests := filterSchedulingResources(report.Status.Requested)

	return fleetapi.ObservedResources{
		LastReportedAt: report.Status.LastReportedAt,
		Capacity:       capacity,
		Usage:          usage,
		Requests:       requests,
	}
}
