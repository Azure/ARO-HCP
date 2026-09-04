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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
	"github.com/Azure/ARO-HCP/internal/kuberesources"
	capacityreportv1alpha1 "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
)

func workerNodeLabels() map[string]*string {
	return map[string]*string{
		compute.RoleLabel: ptr.To("worker"),
	}
}

func TestComputeMaxCapacity(t *testing.T) {
	tests := []struct {
		name        string
		report      *capacityreportv1alpha1.CapacityReport
		pools       []armcontainerservice.AgentPool
		skuMetadata map[string]*skucache.SKUMetadata
		wantMax     corev1.ResourceList
	}{
		{
			name: "scales per-node allocatable by max count including CPU",
			report: &capacityreportv1alpha1.CapacityReport{
				Status: capacityreportv1alpha1.CapacityReportStatus{
					Nodes: []capacityreportv1alpha1.NodeSKUCapacity{
						{
							SKU:   "Standard_D8ds_v5",
							Ready: 2,
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU:                 resource.MustParse("16"),
								corev1.ResourceMemory:              resource.MustParse("64Gi"),
								kuberesources.SwiftNICResourceName: resource.MustParse("4"),
							},
						},
					},
				},
			},
			pools: []armcontainerservice.AgentPool{
				{
					Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
						VMSize:            ptr.To("Standard_D8ds_v5"),
						Count:             ptr.To(int32(2)),
						EnableAutoScaling: ptr.To(true),
						MaxCount:          ptr.To(int32(5)),
						NodeLabels:        workerNodeLabels(),
					},
				},
			},
			skuMetadata: map[string]*skucache.SKUMetadata{},
			wantMax: corev1.ResourceList{
				corev1.ResourceCPU:                 resource.MustParse("40"),
				corev1.ResourceMemory:              resource.MustParse("160Gi"),
				kuberesources.SwiftNICResourceName: resource.MustParse("10"),
			},
		},
		{
			name: "falls back to sku cache when no CR sample, no CPU in fallback",
			report: &capacityreportv1alpha1.CapacityReport{
				Status: capacityreportv1alpha1.CapacityReportStatus{},
			},
			pools: []armcontainerservice.AgentPool{
				{
					Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
						VMSize: ptr.To("Standard_D16ds_v5"),
						Count:  ptr.To(int32(3)),
						Tags: map[string]*string{
							nicSecondaryCountTag: ptr.To("2"),
						},
						NodeLabels: workerNodeLabels(),
					},
				},
			},
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_D16ds_v5": {Name: "Standard_D16ds_v5", MemoryGB: 128},
			},
			wantMax: corev1.ResourceList{
				corev1.ResourceMemory:              resource.MustParse("384Gi"),
				kuberesources.SwiftNICResourceName: resource.MustParse("6"),
			},
		},
		{
			name:   "nil report falls back to sku cache",
			report: nil,
			pools: []armcontainerservice.AgentPool{
				{
					Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
						VMSize:     ptr.To("Standard_D8ds_v5"),
						Count:      ptr.To(int32(2)),
						NodeLabels: workerNodeLabels(),
					},
				},
			},
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_D8ds_v5": {Name: "Standard_D8ds_v5", MemoryGB: 32},
			},
			wantMax: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("64Gi"),
			},
		},
		{
			name: "no matching SKU in CR or cache contributes nothing",
			report: &capacityreportv1alpha1.CapacityReport{
				Status: capacityreportv1alpha1.CapacityReportStatus{},
			},
			pools: []armcontainerservice.AgentPool{
				{
					Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
						VMSize:     ptr.To("Standard_Unknown_v1"),
						Count:      ptr.To(int32(3)),
						NodeLabels: workerNodeLabels(),
					},
				},
			},
			skuMetadata: map[string]*skucache.SKUMetadata{},
			wantMax:     corev1.ResourceList{},
		},
		{
			name: "autoscaling without max count falls back to current count",
			report: &capacityreportv1alpha1.CapacityReport{
				Status: capacityreportv1alpha1.CapacityReportStatus{
					Nodes: []capacityreportv1alpha1.NodeSKUCapacity{
						{
							SKU:   "Standard_D8ds_v5",
							Ready: 1,
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("8"),
								corev1.ResourceMemory: resource.MustParse("32Gi"),
							},
						},
					},
				},
			},
			pools: []armcontainerservice.AgentPool{
				{
					Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
						VMSize:            ptr.To("Standard_D8ds_v5"),
						Count:             ptr.To(int32(2)),
						EnableAutoScaling: ptr.To(true),
						MaxCount:          nil,
						NodeLabels:        workerNodeLabels(),
					},
				},
			},
			skuMetadata: map[string]*skucache.SKUMetadata{},
			wantMax: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("16"),
				corev1.ResourceMemory: resource.MustParse("64Gi"),
			},
		},
		{
			name: "CR sample with zero ready nodes falls back to SKU cache",
			report: &capacityreportv1alpha1.CapacityReport{
				Status: capacityreportv1alpha1.CapacityReportStatus{
					Nodes: []capacityreportv1alpha1.NodeSKUCapacity{
						{
							SKU:      "Standard_D8ds_v5",
							Ready:    0,
							NotReady: 2,
							Allocatable: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("999Gi"),
							},
						},
					},
				},
			},
			pools: []armcontainerservice.AgentPool{
				{
					Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
						VMSize:     ptr.To("Standard_D8ds_v5"),
						Count:      ptr.To(int32(2)),
						NodeLabels: workerNodeLabels(),
					},
				},
			},
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_D8ds_v5": {Name: "Standard_D8ds_v5", MemoryGB: 32},
			},
			wantMax: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("64Gi"),
			},
		},
		{
			name: "malformed NIC tag drops NIC contribution but keeps SKU cache memory",
			report: &capacityreportv1alpha1.CapacityReport{
				Status: capacityreportv1alpha1.CapacityReportStatus{},
			},
			pools: []armcontainerservice.AgentPool{
				{
					Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
						VMSize: ptr.To("Standard_D16ds_v5"),
						Count:  ptr.To(int32(1)),
						Tags: map[string]*string{
							nicSecondaryCountTag: ptr.To("not-a-number"),
						},
						NodeLabels: workerNodeLabels(),
					},
				},
			},
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_D16ds_v5": {Name: "Standard_D16ds_v5", MemoryGB: 128},
			},
			wantMax: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("128Gi"),
			},
		},
		{
			name: "multiple worker pools accumulate",
			report: &capacityreportv1alpha1.CapacityReport{
				Status: capacityreportv1alpha1.CapacityReportStatus{
					Nodes: []capacityreportv1alpha1.NodeSKUCapacity{
						{
							SKU:   "Standard_D8ds_v5",
							Ready: 2,
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("16"),
								corev1.ResourceMemory: resource.MustParse("64Gi"),
							},
						},
					},
				},
			},
			pools: []armcontainerservice.AgentPool{
				{
					Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
						VMSize:     ptr.To("Standard_D8ds_v5"),
						Count:      ptr.To(int32(2)),
						NodeLabels: workerNodeLabels(),
					},
				},
				{
					Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
						VMSize:     ptr.To("Standard_D16ds_v5"),
						Count:      ptr.To(int32(1)),
						NodeLabels: workerNodeLabels(),
					},
				},
			},
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_D16ds_v5": {Name: "Standard_D16ds_v5", MemoryGB: 128},
			},
			wantMax: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("16"),
				corev1.ResourceMemory: resource.MustParse("192Gi"),
			},
		},
		{
			name: "CPU with millicores preserves precision",
			report: &capacityreportv1alpha1.CapacityReport{
				Status: capacityreportv1alpha1.CapacityReportStatus{
					Nodes: []capacityreportv1alpha1.NodeSKUCapacity{
						{
							SKU:   "Standard_D8ds_v5",
							Ready: 2,
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("15800m"),
								corev1.ResourceMemory: resource.MustParse("64Gi"),
							},
						},
					},
				},
			},
			pools: []armcontainerservice.AgentPool{
				{
					Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
						VMSize:            ptr.To("Standard_D8ds_v5"),
						Count:             ptr.To(int32(2)),
						EnableAutoScaling: ptr.To(true),
						MaxCount:          ptr.To(int32(4)),
						NodeLabels:        workerNodeLabels(),
					},
				},
			},
			skuMetadata: map[string]*skucache.SKUMetadata{},
			wantMax: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("31600m"),
				corev1.ResourceMemory: resource.MustParse("128Gi"),
			},
		},
		{
			name: "non-worker pools are excluded",
			report: &capacityreportv1alpha1.CapacityReport{
				Status: capacityreportv1alpha1.CapacityReportStatus{
					Nodes: []capacityreportv1alpha1.NodeSKUCapacity{
						{
							SKU:   "Standard_D8ds_v5",
							Ready: 2,
							Allocatable: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("16"),
								corev1.ResourceMemory: resource.MustParse("64Gi"),
							},
						},
					},
				},
			},
			pools: []armcontainerservice.AgentPool{
				{
					Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
						VMSize:     ptr.To("Standard_D8ds_v5"),
						Count:      ptr.To(int32(2)),
						NodeLabels: workerNodeLabels(),
					},
				},
				{
					Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
						VMSize: ptr.To("Standard_D8ds_v5"),
						Count:  ptr.To(int32(3)),
						NodeLabels: map[string]*string{
							compute.RoleLabel: ptr.To("system"),
						},
					},
				},
				{
					Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
						VMSize: ptr.To("Standard_D8ds_v5"),
						Count:  ptr.To(int32(1)),
						NodeLabels: map[string]*string{
							compute.RoleLabel: ptr.To("infra"),
						},
					},
				},
				{
					Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
						VMSize:     ptr.To("Standard_D8ds_v5"),
						Count:      ptr.To(int32(4)),
						NodeLabels: nil,
					},
				},
			},
			skuMetadata: map[string]*skucache.SKUMetadata{},
			wantMax: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("16"),
				corev1.ResourceMemory: resource.MustParse("64Gi"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := computeMaxCapacity(test.report, test.pools, test.skuMetadata)
			assertResourceListEqual(t, test.wantMax, actual, "Max")
		})
	}
}
