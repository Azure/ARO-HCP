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
	"testing"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"

	"github.com/Azure/ARO-HCP/internal/kuberesources"
	capacityreportv1alpha1 "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

func TestScaleCeilingRequiresCompleteWorkerPools(t *testing.T) {
	for _, test := range []string{"valid", "missing CPU", "missing memory", "missing NIC", "missing max count", "missing VM size", "missing count", "negative count"} {
		t.Run(test, func(t *testing.T) {
			report := &capacityreportv1alpha1.CapacityReport{Status: capacityreportv1alpha1.CapacityReportStatus{Nodes: []capacityreportv1alpha1.NodeSKUCapacity{
				{SKU: "complete", Ready: 1, Allocatable: pressureResources("4", "16Gi", "2")},
				{SKU: "incomplete", Ready: 1, Allocatable: pressureResources("4", "16Gi", "2")},
			}}}
			pools := []armcontainerservice.AgentPool{
				{Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{NodeLabels: workerNodeLabels(), VMSize: ptr.To("complete"), Count: ptr.To(int32(1)), EnableAutoScaling: ptr.To(true), MaxCount: ptr.To(int32(10))}},
				{Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{NodeLabels: workerNodeLabels(), VMSize: ptr.To("incomplete"), Count: ptr.To(int32(1)), EnableAutoScaling: ptr.To(true), MaxCount: ptr.To(int32(10))}},
			}
			switch test {
			case "missing CPU":
				delete(report.Status.Nodes[1].Allocatable, corev1.ResourceCPU)
			case "missing memory":
				delete(report.Status.Nodes[1].Allocatable, corev1.ResourceMemory)
			case "missing NIC":
				delete(report.Status.Nodes[1].Allocatable, kuberesources.SwiftNICResourceName)
			case "missing max count":
				pools[1].Properties.MaxCount = nil
			case "missing VM size":
				pools[1].Properties.VMSize = nil
			case "missing count":
				pools[1].Properties.Count = nil
			case "negative count":
				pools[1].Properties.Count = ptr.To(int32(-1))
			}
			err := validateScaleCeilingInputs(report, pools, nil)
			if test == "valid" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err, "one complete pool must not hide another pool's missing data")
		})
	}
}
