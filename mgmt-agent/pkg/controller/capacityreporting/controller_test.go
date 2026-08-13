// Copyright 2025 Microsoft Corporation
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
	"time"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"

	"github.com/Azure/ARO-Tools/testutil"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	capacityreportv1alpha1 "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller"
)

func TestAggregateNodesBySKU(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		nodes []*corev1.Node
		want  []capacityreportv1alpha1.NodeSKUCapacity
	}{
		{
			name:  "no nodes",
			nodes: nil,
			want:  []capacityreportv1alpha1.NodeSKUCapacity{},
		},
		{
			name: "single ready node",
			nodes: []*corev1.Node{
				workerNode("node-1", "Standard_D32s_v3", true, corev1.ResourceList{
					corev1.ResourceCPU:              resource.MustParse("32"),
					corev1.ResourceMemory:           resource.MustParse("128Gi"),
					controller.SwiftNICResourceName: resource.MustParse("8"),
				}),
			},
			want: []capacityreportv1alpha1.NodeSKUCapacity{
				{
					SKU:      "Standard_D32s_v3",
					Ready:    1,
					NotReady: 0,
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:              resource.MustParse("32"),
						corev1.ResourceMemory:           resource.MustParse("128Gi"),
						controller.SwiftNICResourceName: resource.MustParse("8"),
					},
				},
			},
		},
		{
			name: "mixed ready and not-ready same SKU",
			nodes: []*corev1.Node{
				workerNode("node-1", "Standard_D32s_v3", true, corev1.ResourceList{
					corev1.ResourceCPU:              resource.MustParse("32"),
					corev1.ResourceMemory:           resource.MustParse("128Gi"),
					controller.SwiftNICResourceName: resource.MustParse("8"),
				}),
				workerNode("node-2", "Standard_D32s_v3", false, corev1.ResourceList{
					corev1.ResourceCPU:              resource.MustParse("32"),
					corev1.ResourceMemory:           resource.MustParse("128Gi"),
					controller.SwiftNICResourceName: resource.MustParse("8"),
				}),
				workerNode("node-3", "Standard_D32s_v3", true, corev1.ResourceList{
					corev1.ResourceCPU:              resource.MustParse("32"),
					corev1.ResourceMemory:           resource.MustParse("128Gi"),
					controller.SwiftNICResourceName: resource.MustParse("8"),
				}),
			},
			want: []capacityreportv1alpha1.NodeSKUCapacity{
				{
					SKU:      "Standard_D32s_v3",
					Ready:    2,
					NotReady: 1,
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:              resource.MustParse("64"),
						corev1.ResourceMemory:           resource.MustParse("256Gi"),
						controller.SwiftNICResourceName: resource.MustParse("16"),
					},
				},
			},
		},
		{
			name: "multiple SKUs",
			nodes: []*corev1.Node{
				workerNode("node-1", "Standard_D32s_v3", true, corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("32"),
					corev1.ResourceMemory: resource.MustParse("128Gi"),
				}),
				workerNode("node-2", "Standard_D64s_v3", true, corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("64"),
					corev1.ResourceMemory: resource.MustParse("256Gi"),
				}),
			},
			want: []capacityreportv1alpha1.NodeSKUCapacity{
				{
					SKU:   "Standard_D32s_v3",
					Ready: 1,
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("32"),
						corev1.ResourceMemory: resource.MustParse("128Gi"),
					},
				},
				{
					SKU:   "Standard_D64s_v3",
					Ready: 1,
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("64"),
						corev1.ResourceMemory: resource.MustParse("256Gi"),
					},
				},
			},
		},
		{
			name: "all not-ready nodes contribute zero allocatable",
			nodes: []*corev1.Node{
				workerNode("node-1", "Standard_D32s_v3", false, corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("32"),
					corev1.ResourceMemory: resource.MustParse("128Gi"),
				}),
			},
			want: []capacityreportv1alpha1.NodeSKUCapacity{
				{
					SKU:         "Standard_D32s_v3",
					Ready:       0,
					NotReady:    1,
					Allocatable: corev1.ResourceList{},
				},
			},
		},
		{
			name: "node with no conditions is not ready",
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-no-conditions",
						Labels: map[string]string{
							workerNodeLabel: workerLabelValue,
							skuLabel:        "Standard_D32s_v3",
						},
					},
					Status: corev1.NodeStatus{
						Allocatable: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("128Gi"),
						},
					},
				},
			},
			want: []capacityreportv1alpha1.NodeSKUCapacity{
				{
					SKU:         "Standard_D32s_v3",
					Ready:       0,
					NotReady:    1,
					Allocatable: corev1.ResourceList{},
				},
			},
		},
		{
			name: "node without SKU label is skipped",
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "node-no-sku",
						Labels: map[string]string{workerNodeLabel: workerLabelValue},
					},
					Status: corev1.NodeStatus{
						Conditions: []corev1.NodeCondition{
							{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
						},
					},
				},
			},
			want: []capacityreportv1alpha1.NodeSKUCapacity{},
		},
		{
			name: "allocatable filters to tracked resources only",
			nodes: []*corev1.Node{
				workerNode("node-1", "Standard_D32s_v3", true, corev1.ResourceList{
					corev1.ResourceCPU:              resource.MustParse("32"),
					corev1.ResourceMemory:           resource.MustParse("128Gi"),
					corev1.ResourceEphemeralStorage: resource.MustParse("500Gi"),
					controller.SwiftNICResourceName: resource.MustParse("8"),
				}),
			},
			want: []capacityreportv1alpha1.NodeSKUCapacity{
				{
					SKU:   "Standard_D32s_v3",
					Ready: 1,
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:              resource.MustParse("32"),
						corev1.ResourceMemory:           resource.MustParse("128Gi"),
						controller.SwiftNICResourceName: resource.MustParse("8"),
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := aggregateNodesBySKU(tt.nodes)
			if diff := cmp.Diff(tt.want, got); len(diff) > 0 {
				t.Errorf("unexpected node capacity (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAggregatePodRequests(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		pods []*corev1.Pod
		want corev1.ResourceList
	}{
		{
			name: "no pods",
			pods: nil,
			want: corev1.ResourceList{},
		},
		{
			name: "pod in ocm- namespace",
			pods: []*corev1.Pod{
				newPod("ocm-cluster-1", "pod-1", corev1.PodRunning, corev1.ResourceList{
					corev1.ResourceCPU:              resource.MustParse("500m"),
					corev1.ResourceMemory:           resource.MustParse("4Gi"),
					controller.SwiftNICResourceName: resource.MustParse("2"),
				}),
			},
			want: corev1.ResourceList{
				corev1.ResourceCPU:              *resource.NewMilliQuantity(500, resource.DecimalSI),
				corev1.ResourceMemory:           resource.MustParse("4Gi"),
				controller.SwiftNICResourceName: resource.MustParse("2"),
			},
		},
		{
			name: "pod outside ocm- namespace excluded",
			pods: []*corev1.Pod{
				newPod("kube-system", "pod-1", corev1.PodRunning, corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				}),
			},
			want: corev1.ResourceList{},
		},
		{
			name: "succeeded and failed pods excluded",
			pods: []*corev1.Pod{
				newPod("ocm-cluster-1", "pod-done", corev1.PodSucceeded, corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				}),
				newPod("ocm-cluster-1", "pod-failed", corev1.PodFailed, corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				}),
			},
			want: corev1.ResourceList{},
		},
		{
			name: "multiple containers summed",
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "multi", Namespace: "ocm-cluster-1"},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
								corev1.ResourceCPU:              resource.MustParse("1"),
								corev1.ResourceMemory:           resource.MustParse("2Gi"),
								controller.SwiftNICResourceName: resource.MustParse("1"),
							}}},
							{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
								corev1.ResourceCPU:              resource.MustParse("250m"),
								corev1.ResourceMemory:           resource.MustParse("3Gi"),
								controller.SwiftNICResourceName: resource.MustParse("2"),
							}}},
						},
					},
					Status: corev1.PodStatus{Phase: corev1.PodRunning},
				},
			},
			want: corev1.ResourceList{
				corev1.ResourceCPU:              *resource.NewMilliQuantity(1250, resource.DecimalSI),
				corev1.ResourceMemory:           resource.MustParse("5Gi"),
				controller.SwiftNICResourceName: resource.MustParse("3"),
			},
		},
		{
			name: "SWIFT NIC requests summed across pods",
			pods: []*corev1.Pod{
				newPod("ocm-cluster-1", "pod-1", corev1.PodRunning, corev1.ResourceList{
					corev1.ResourceMemory:           resource.MustParse("4Gi"),
					controller.SwiftNICResourceName: resource.MustParse("3"),
				}),
				newPod("ocm-cluster-2", "pod-2", corev1.PodRunning, corev1.ResourceList{
					corev1.ResourceMemory:           resource.MustParse("2Gi"),
					controller.SwiftNICResourceName: resource.MustParse("3"),
				}),
			},
			want: corev1.ResourceList{
				corev1.ResourceMemory:           resource.MustParse("6Gi"),
				controller.SwiftNICResourceName: resource.MustParse("6"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := aggregatePodRequests(tt.pods)
			if diff := cmp.Diff(tt.want, got); len(diff) > 0 {
				t.Errorf("unexpected resource list (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAggregatePodMetrics(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		metrics []metricsv1beta1.PodMetrics
		want    corev1.ResourceList
	}{
		{
			name:    "no metrics",
			metrics: nil,
			want:    corev1.ResourceList{},
		},
		{
			name: "metrics in ocm- namespace",
			metrics: []metricsv1beta1.PodMetrics{
				podMetrics("ocm-cluster-1", "pod-1", corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("750m"),
					corev1.ResourceMemory: resource.MustParse("3Gi"),
				}),
			},
			want: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(750, resource.DecimalSI),
				corev1.ResourceMemory: resource.MustParse("3Gi"),
			},
		},
		{
			name: "metrics outside ocm- namespace excluded",
			metrics: []metricsv1beta1.PodMetrics{
				podMetrics("kube-system", "pod-1", corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("3Gi"),
				}),
			},
			want: corev1.ResourceList{},
		},
		{
			name: "multiple pods summed with CPU",
			metrics: []metricsv1beta1.PodMetrics{
				podMetrics("ocm-cluster-1", "pod-1", corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1500m"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				}),
				podMetrics("ocm-cluster-2", "pod-2", corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("3Gi"),
				}),
			},
			want: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(3500, resource.DecimalSI),
				corev1.ResourceMemory: resource.MustParse("5Gi"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := aggregatePodMetrics(tt.metrics)
			if diff := cmp.Diff(tt.want, got); len(diff) > 0 {
				t.Errorf("unexpected resource list (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCountHCPs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		hcps []*hypershiftv1beta1.HostedControlPlane
		want capacityreportv1alpha1.HostedControlPlaneCount
	}{
		{
			name: "no HCPs",
			hcps: nil,
			want: capacityreportv1alpha1.HostedControlPlaneCount{Ready: 0, NotReady: 0},
		},
		{
			name: "all ready",
			hcps: []*hypershiftv1beta1.HostedControlPlane{
				newHostedControlPlane("ocm-cluster-1", "hcp-1", true),
				newHostedControlPlane("ocm-cluster-2", "hcp-2", true),
			},
			want: capacityreportv1alpha1.HostedControlPlaneCount{Ready: 2, NotReady: 0},
		},
		{
			name: "mixed ready and not-ready",
			hcps: []*hypershiftv1beta1.HostedControlPlane{
				newHostedControlPlane("ocm-cluster-1", "hcp-1", true),
				newHostedControlPlane("ocm-cluster-2", "hcp-2", false),
				newHostedControlPlane("ocm-cluster-3", "hcp-3", true),
			},
			want: capacityreportv1alpha1.HostedControlPlaneCount{Ready: 2, NotReady: 1},
		},
		{
			name: "HCP without Available condition is not ready",
			hcps: []*hypershiftv1beta1.HostedControlPlane{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "hcp-1", Namespace: "ocm-cluster-1"},
					Status: hypershiftv1beta1.HostedControlPlaneStatus{
						Conditions: []metav1.Condition{
							{Type: "SomeOtherCondition", Status: metav1.ConditionTrue},
						},
					},
				},
			},
			want: capacityreportv1alpha1.HostedControlPlaneCount{Ready: 0, NotReady: 1},
		},
		{
			name: "HCP with no conditions is not ready",
			hcps: []*hypershiftv1beta1.HostedControlPlane{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "hcp-1", Namespace: "ocm-cluster-1"},
				},
			},
			want: capacityreportv1alpha1.HostedControlPlaneCount{Ready: 0, NotReady: 1},
		},
		{
			name: "HCPs in non-ocm namespaces are excluded",
			hcps: []*hypershiftv1beta1.HostedControlPlane{
				newHostedControlPlane("ocm-cluster-1", "hcp-1", true),
				newHostedControlPlane("kube-system", "hcp-2", true),
				newHostedControlPlane("default", "hcp-3", true),
				newHostedControlPlane("ocm-cluster-2", "hcp-4", false),
			},
			want: capacityreportv1alpha1.HostedControlPlaneCount{Ready: 1, NotReady: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := countHCPs(tt.hcps)
			if diff := cmp.Diff(tt.want, got); len(diff) > 0 {
				t.Errorf("unexpected HCP count (-want +got):\n%s", diff)
			}
		})
	}
}

func TestComputeAverageHCPResourceUsage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		usage      corev1.ResourceList
		readyCount int32
		want       corev1.ResourceList
	}{
		{
			name:       "zero ready returns nil",
			usage:      corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("100Gi")},
			readyCount: 0,
			want:       nil,
		},
		{
			name: "divides evenly",
			usage: corev1.ResourceList{
				corev1.ResourceCPU:              resource.MustParse("20"),
				corev1.ResourceMemory:           resource.MustParse("100Gi"),
				controller.SwiftNICResourceName: resource.MustParse("20"),
			},
			readyCount: 10,
			want: corev1.ResourceList{
				corev1.ResourceCPU:              *resource.NewMilliQuantity(2000, resource.DecimalSI),
				corev1.ResourceMemory:           *resource.NewQuantity(10*1024*1024*1024, resource.BinarySI),
				controller.SwiftNICResourceName: *resource.NewQuantity(swiftNICsPerHCP, resource.DecimalSI),
			},
		},
		{
			name: "single ready HCP returns usage as-is with hardcoded SWIFT NIC",
			usage: corev1.ResourceList{
				corev1.ResourceCPU:              resource.MustParse("4"),
				corev1.ResourceMemory:           resource.MustParse("8Gi"),
				controller.SwiftNICResourceName: resource.MustParse("3"),
			},
			readyCount: 1,
			want: corev1.ResourceList{
				corev1.ResourceCPU:              *resource.NewMilliQuantity(4000, resource.DecimalSI),
				corev1.ResourceMemory:           *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
				controller.SwiftNICResourceName: *resource.NewQuantity(swiftNICsPerHCP, resource.DecimalSI),
			},
		},
		{
			name: "truncates fractional result",
			usage: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("10Gi"),
			},
			readyCount: 3,
			want: corev1.ResourceList{
				corev1.ResourceMemory:           *resource.NewQuantity(10*1024*1024*1024/3, resource.BinarySI),
				controller.SwiftNICResourceName: *resource.NewQuantity(swiftNICsPerHCP, resource.DecimalSI),
			},
		},
		{
			name: "millicore CPU preserves sub-core precision",
			usage: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2500m"),
				corev1.ResourceMemory: resource.MustParse("8Gi"),
			},
			readyCount: 1,
			want: corev1.ResourceList{
				corev1.ResourceCPU:              *resource.NewMilliQuantity(2500, resource.DecimalSI),
				corev1.ResourceMemory:           *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
				controller.SwiftNICResourceName: *resource.NewQuantity(swiftNICsPerHCP, resource.DecimalSI),
			},
		},
		{
			name: "millicore CPU divides across multiple HCPs",
			usage: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("7500m"),
				corev1.ResourceMemory: resource.MustParse("30Gi"),
			},
			readyCount: 3,
			want: corev1.ResourceList{
				corev1.ResourceCPU:              *resource.NewMilliQuantity(2500, resource.DecimalSI),
				corev1.ResourceMemory:           *resource.NewQuantity(10*1024*1024*1024, resource.BinarySI),
				controller.SwiftNICResourceName: *resource.NewQuantity(swiftNICsPerHCP, resource.DecimalSI),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := computeAverageHCPResourceUsage(tt.usage, tt.readyCount)
			if tt.want == nil {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}
			if diff := cmp.Diff(tt.want, got); len(diff) > 0 {
				t.Errorf("unexpected resource list (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBuildReport(t *testing.T) {
	t.Parallel()
	fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	condition := buildReportCurrentCondition(nil, metav1.ConditionTrue, capacityreportv1alpha1.ReasonDataCollected, "", fixedTime)

	tests := []struct {
		name      string
		nodes     []capacityreportv1alpha1.NodeSKUCapacity
		usage     corev1.ResourceList
		requested corev1.ResourceList
		hcpCount  capacityreportv1alpha1.HostedControlPlaneCount
	}{
		{
			name: "full_report_with_average_hcp_resource_usage",
			nodes: []capacityreportv1alpha1.NodeSKUCapacity{
				{
					SKU:   "Standard_D32s_v3",
					Ready: 2,
					Allocatable: corev1.ResourceList{
						corev1.ResourceMemory:           resource.MustParse("256Gi"),
						controller.SwiftNICResourceName: resource.MustParse("16"),
					},
				},
			},
			usage: corev1.ResourceList{
				corev1.ResourceMemory:           resource.MustParse("4Gi"),
				controller.SwiftNICResourceName: resource.MustParse("6"),
			},
			requested: corev1.ResourceList{
				corev1.ResourceMemory:           resource.MustParse("8Gi"),
				controller.SwiftNICResourceName: resource.MustParse("6"),
			},
			hcpCount: capacityreportv1alpha1.HostedControlPlaneCount{Ready: 2, NotReady: 1},
		},
		{
			name: "no_ready_hcps_omits_average_hcp_resource_usage",
			nodes: []capacityreportv1alpha1.NodeSKUCapacity{
				{
					SKU:   "Standard_D32s_v3",
					Ready: 1,
					Allocatable: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("128Gi"),
					},
				},
			},
			usage: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
			requested: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
			hcpCount: capacityreportv1alpha1.HostedControlPlaneCount{Ready: 0, NotReady: 2},
		},
		{
			name: "swift_nic_in_usage_and_requested",
			nodes: []capacityreportv1alpha1.NodeSKUCapacity{
				{
					SKU:   "Standard_D32s_v3",
					Ready: 1,
					Allocatable: corev1.ResourceList{
						corev1.ResourceMemory:           resource.MustParse("128Gi"),
						controller.SwiftNICResourceName: resource.MustParse("8"),
					},
				},
			},
			usage: corev1.ResourceList{
				corev1.ResourceMemory:           resource.MustParse("2Gi"),
				controller.SwiftNICResourceName: resource.MustParse("3"),
			},
			requested: corev1.ResourceList{
				corev1.ResourceMemory:           resource.MustParse("4Gi"),
				controller.SwiftNICResourceName: resource.MustParse("3"),
			},
			hcpCount: capacityreportv1alpha1.HostedControlPlaneCount{Ready: 1, NotReady: 0},
		},
		{
			name:      "empty_cluster",
			nodes:     nil,
			usage:     corev1.ResourceList{},
			requested: corev1.ResourceList{},
			hcpCount:  capacityreportv1alpha1.HostedControlPlaneCount{},
		},
		{
			name: "multiple_skus",
			nodes: []capacityreportv1alpha1.NodeSKUCapacity{
				{
					SKU:      "Standard_D32s_v3",
					Ready:    2,
					NotReady: 1,
					Allocatable: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("256Gi"),
					},
				},
				{
					SKU:   "Standard_D64s_v3",
					Ready: 1,
					Allocatable: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("512Gi"),
					},
				},
			},
			usage: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("100Gi"),
			},
			requested: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("200Gi"),
			},
			hcpCount: capacityreportv1alpha1.HostedControlPlaneCount{Ready: 8, NotReady: 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildReport(tt.nodes, tt.usage, tt.requested, tt.hcpCount, condition, fixedTime)
			testutil.CompareWithFixture(t, got)
		})
	}
}

func TestRetainStatusWithCondition(t *testing.T) {
	t.Parallel()
	fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	lastReported := metav1.NewTime(fixedTime.Add(-30 * time.Second))

	tests := []struct {
		name           string
		existingStatus capacityreportv1alpha1.CapacityReportStatus
	}{
		{
			name:           "empty_status_produces_valid_required_fields",
			existingStatus: capacityreportv1alpha1.CapacityReportStatus{},
		},
		{
			name: "preserves_full_existing_status",
			existingStatus: capacityreportv1alpha1.CapacityReportStatus{
				Conditions: []metav1.Condition{
					{
						Type:               capacityreportv1alpha1.ConditionTypeReportCurrent,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: lastReported,
					},
				},
				LastReportedAt: &lastReported,
				Nodes: []capacityreportv1alpha1.NodeSKUCapacity{
					{
						SKU:      "Standard_D32s_v3",
						Ready:    2,
						NotReady: 1,
						Allocatable: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("256Gi"),
						},
					},
				},
				Usage: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
				Requested: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
				HostedControlPlanes: capacityreportv1alpha1.HostedControlPlaneCount{
					Ready:    5,
					NotReady: 1,
				},
				AverageHCPResourceUsage: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("800Mi"),
				},
			},
		},
		{
			name: "preserves_status_without_optional_fields",
			existingStatus: capacityreportv1alpha1.CapacityReportStatus{
				HostedControlPlanes: capacityreportv1alpha1.HostedControlPlaneCount{
					Ready:    3,
					NotReady: 0,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := retainStatusWithCondition(tt.existingStatus, metav1.ConditionFalse, capacityreportv1alpha1.ReasonDataCollectionFailed, "test error", fixedTime)
			testutil.CompareWithFixture(t, got)
		})
	}
}

func TestReportCurrentCondition(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(30 * time.Second)
	t2 := t1.Add(30 * time.Second)

	tests := []struct {
		name               string
		existingConditions []metav1.Condition
		status             metav1.ConditionStatus
		now                time.Time
		wantTransitionTime time.Time
	}{
		{
			name:               "no existing condition uses current time",
			existingConditions: nil,
			status:             metav1.ConditionTrue,
			now:                t1,
			wantTransitionTime: t1,
		},
		{
			name: "same status preserves existing transition time",
			existingConditions: []metav1.Condition{
				{
					Type:               capacityreportv1alpha1.ConditionTypeReportCurrent,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(t0),
				},
			},
			status:             metav1.ConditionTrue,
			now:                t2,
			wantTransitionTime: t0,
		},
		{
			name: "status change uses current time",
			existingConditions: []metav1.Condition{
				{
					Type:               capacityreportv1alpha1.ConditionTypeReportCurrent,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(t0),
				},
			},
			status:             metav1.ConditionFalse,
			now:                t1,
			wantTransitionTime: t1,
		},
		{
			name: "different condition type is ignored",
			existingConditions: []metav1.Condition{
				{
					Type:               "SomeOtherCondition",
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.NewTime(t0),
				},
			},
			status:             metav1.ConditionTrue,
			now:                t1,
			wantTransitionTime: t1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildReportCurrentCondition(tt.existingConditions, tt.status, capacityreportv1alpha1.ReasonDataCollected, "", tt.now)
			if got.LastTransitionTime == nil {
				t.Fatal("LastTransitionTime is nil")
			}
			if !got.LastTransitionTime.Time.Equal(tt.wantTransitionTime) {
				t.Errorf("LastTransitionTime = %v, want %v", got.LastTransitionTime.Time, tt.wantTransitionTime)
			}
		})
	}
}

// Test helpers

func workerNode(name, sku string, ready bool, allocatable corev1.ResourceList) *corev1.Node {
	readyStatus := corev1.ConditionFalse
	if ready {
		readyStatus = corev1.ConditionTrue
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				workerNodeLabel: workerLabelValue,
				skuLabel:        sku,
			},
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: readyStatus},
			},
			Allocatable: allocatable,
		},
	}
}

func newPod(namespace, name string, phase corev1.PodPhase, requests corev1.ResourceList) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Resources: corev1.ResourceRequirements{Requests: requests}},
			},
		},
		Status: corev1.PodStatus{Phase: phase},
	}
}

func podMetrics(namespace, name string, usage corev1.ResourceList) metricsv1beta1.PodMetrics {
	return metricsv1beta1.PodMetrics{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Containers: []metricsv1beta1.ContainerMetrics{
			{Usage: usage},
		},
	}
}

func newHostedControlPlane(namespace, name string, available bool) *hypershiftv1beta1.HostedControlPlane {
	status := metav1.ConditionFalse
	if available {
		status = metav1.ConditionTrue
	}
	return &hypershiftv1beta1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Status: hypershiftv1beta1.HostedControlPlaneStatus{
			Conditions: []metav1.Condition{
				{Type: string(hypershiftv1beta1.HostedControlPlaneAvailable), Status: status},
			},
		},
	}
}
