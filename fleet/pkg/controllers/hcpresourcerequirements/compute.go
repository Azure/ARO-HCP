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

package hcpresourcerequirements

import (
	"gopkg.in/inf.v0"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/Azure/ARO-HCP/fleet/pkg/scheduling"
	capacityreportv1alpha1 "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

// computeAverageRequirements computes the average resource usage and requests
// per ready HCP across all provided CapacityReports. Only scheduling-relevant
// resources (cpu, memory, swift-nic) are included.
func computeAverageRequirements(reports []*capacityreportv1alpha1.CapacityReport) (averageUsage, averageRequests corev1.ResourceList, sampleSize int) {
	totalUsage := corev1.ResourceList{}
	totalRequests := corev1.ResourceList{}
	var totalReadyHCPs int64

	for _, report := range reports {
		totalReadyHCPs += int64(len(report.Status.HostedControlPlanes.ReadyResourceIDs))

		for _, name := range scheduling.Resources() {
			if quantity, ok := report.Status.Usage[name]; ok {
				existing := totalUsage[name]
				existing.Add(quantity)
				totalUsage[name] = existing
			}
			if quantity, ok := report.Status.Requested[name]; ok {
				existing := totalRequests[name]
				existing.Add(quantity)
				totalRequests[name] = existing
			}
		}
	}

	if totalReadyHCPs == 0 {
		return nil, nil, 0
	}

	averageUsage = divideResourceList(totalUsage, totalReadyHCPs)
	averageRequests = divideResourceList(totalRequests, totalReadyHCPs)
	return averageUsage, averageRequests, int(totalReadyHCPs)
}

func divideResourceList(total corev1.ResourceList, divisor int64) corev1.ResourceList {
	if len(total) == 0 {
		return nil
	}
	result := corev1.ResourceList{}
	divisorDec := inf.NewDec(divisor, 0)
	for name, quantity := range total {
		d := quantity.AsDec()
		quotient := new(inf.Dec).QuoRound(d, divisorDec, d.Scale(), inf.RoundDown)
		result[name] = *resource.NewDecimalQuantity(*quotient, quantity.Format)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
