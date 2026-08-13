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
					corev1.ResourceMemory:           resource.MustParse("128Gi"),
					controller.SwiftNICResourceName: resource.MustParse("8"),
				}),
				workerNode("node-2", "Standard_D32s_v3", false, corev1.ResourceList{
					corev1.ResourceMemory:           resource.MustParse("128Gi"),
					controller.SwiftNICResourceName: resource.MustParse("8"),
				}),
				workerNode("node-3", "Standard_D32s_v3", true, corev1.ResourceList{
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
					corev1.ResourceMemory: resource.MustParse("128Gi"),
				}),
				workerNode("node-2", "Standard_D64s_v3", true, corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("256Gi"),
				}),
			},
			want: []capacityreportv1alpha1.NodeSKUCapacity{
				{
					SKU:   "Standard_D32s_v3",
					Ready: 1,
					Allocatable: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("128Gi"),
					},
				},
				{
					SKU:   "Standard_D64s_v3",
					Ready: 1,
					Allocatable: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("256Gi"),
					},
				},
			},
		},
		{
			name: "all not-ready nodes contribute zero allocatable",
			nodes: []*corev1.Node{
				workerNode("node-1", "Standard_D32s_v3", false, corev1.ResourceList{
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
					corev1.ResourceMemory:           resource.MustParse("128Gi"),
					corev1.ResourceCPU:              resource.MustParse("32"),
					controller.SwiftNICResourceName: resource.MustParse("8"),
				}),
			},
			want: []capacityreportv1alpha1.NodeSKUCapacity{
				{
					SKU:   "Standard_D32s_v3",
					Ready: 1,
					Allocatable: corev1.ResourceList{
						corev1.ResourceMemory:           resource.MustParse("128Gi"),
						controller.SwiftNICResourceName: resource.MustParse("8"),
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := aggregateNodesBySKU(tt.nodes)
			if diff := cmp.Diff(tt.want, got); len(diff) > 0 {
				t.Errorf("unexpected node capacity (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAggregatePodRequests(t *testing.T) {
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
				pod("ocm-cluster-1", "pod-1", corev1.PodRunning, corev1.ResourceList{
					corev1.ResourceMemory:           resource.MustParse("4Gi"),
					controller.SwiftNICResourceName: resource.MustParse("2"),
				}),
			},
			want: corev1.ResourceList{
				corev1.ResourceMemory:           resource.MustParse("4Gi"),
				controller.SwiftNICResourceName: resource.MustParse("2"),
			},
		},
		{
			name: "pod outside ocm- namespace excluded",
			pods: []*corev1.Pod{
				pod("kube-system", "pod-1", corev1.PodRunning, corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				}),
			},
			want: corev1.ResourceList{},
		},
		{
			name: "succeeded and failed pods excluded",
			pods: []*corev1.Pod{
				pod("ocm-cluster-1", "pod-done", corev1.PodSucceeded, corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				}),
				pod("ocm-cluster-1", "pod-failed", corev1.PodFailed, corev1.ResourceList{
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
								corev1.ResourceMemory:           resource.MustParse("2Gi"),
								controller.SwiftNICResourceName: resource.MustParse("1"),
							}}},
							{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
								corev1.ResourceMemory:           resource.MustParse("3Gi"),
								controller.SwiftNICResourceName: resource.MustParse("2"),
							}}},
						},
					},
					Status: corev1.PodStatus{Phase: corev1.PodRunning},
				},
			},
			want: corev1.ResourceList{
				corev1.ResourceMemory:           resource.MustParse("5Gi"),
				controller.SwiftNICResourceName: resource.MustParse("3"),
			},
		},
		{
			name: "SWIFT NIC requests summed across pods",
			pods: []*corev1.Pod{
				pod("ocm-cluster-1", "pod-1", corev1.PodRunning, corev1.ResourceList{
					corev1.ResourceMemory:           resource.MustParse("4Gi"),
					controller.SwiftNICResourceName: resource.MustParse("3"),
				}),
				pod("ocm-cluster-2", "pod-2", corev1.PodRunning, corev1.ResourceList{
					corev1.ResourceMemory:           resource.MustParse("2Gi"),
					controller.SwiftNICResourceName: resource.MustParse("3"),
				}),
			},
			want: corev1.ResourceList{
				corev1.ResourceMemory:           resource.MustParse("6Gi"),
				controller.SwiftNICResourceName: resource.MustParse("6"),
			},
		},
		{
			name: "CPU requests filtered out",
			pods: []*corev1.Pod{
				pod("ocm-cluster-1", "pod-1", corev1.PodRunning, corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("4Gi"),
					corev1.ResourceCPU:    resource.MustParse("2"),
				}),
			},
			want: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := aggregatePodRequests(tt.pods)
			if diff := cmp.Diff(tt.want, got); len(diff) > 0 {
				t.Errorf("unexpected resource list (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAggregatePodMetrics(t *testing.T) {
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
					corev1.ResourceMemory: resource.MustParse("3Gi"),
				}),
			},
			want: corev1.ResourceList{
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
			name: "multiple pods summed",
			metrics: []metricsv1beta1.PodMetrics{
				podMetrics("ocm-cluster-1", "pod-1", corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				}),
				podMetrics("ocm-cluster-2", "pod-2", corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("3Gi"),
				}),
			},
			want: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("5Gi"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := aggregatePodMetrics(tt.metrics)
			if diff := cmp.Diff(tt.want, got); len(diff) > 0 {
				t.Errorf("unexpected resource list (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCountHCPs(t *testing.T) {
	tests := []struct {
		name string
		hcps []*hypershiftv1beta1.HostedControlPlane
		want *capacityreportv1alpha1.HostedControlPlaneCount
	}{
		{
			name: "no HCPs",
			hcps: nil,
			want: &capacityreportv1alpha1.HostedControlPlaneCount{Total: 0, Ready: 0},
		},
		{
			name: "all ready",
			hcps: []*hypershiftv1beta1.HostedControlPlane{
				hcp("ocm-cluster-1", "hcp-1", true),
				hcp("ocm-cluster-2", "hcp-2", true),
			},
			want: &capacityreportv1alpha1.HostedControlPlaneCount{Total: 2, Ready: 2},
		},
		{
			name: "mixed ready and not-ready",
			hcps: []*hypershiftv1beta1.HostedControlPlane{
				hcp("ocm-cluster-1", "hcp-1", true),
				hcp("ocm-cluster-2", "hcp-2", false),
				hcp("ocm-cluster-3", "hcp-3", true),
			},
			want: &capacityreportv1alpha1.HostedControlPlaneCount{Total: 3, Ready: 2},
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
			want: &capacityreportv1alpha1.HostedControlPlaneCount{Total: 1, Ready: 0},
		},
		{
			name: "HCP with no conditions is not ready",
			hcps: []*hypershiftv1beta1.HostedControlPlane{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "hcp-1", Namespace: "ocm-cluster-1"},
				},
			},
			want: &capacityreportv1alpha1.HostedControlPlaneCount{Total: 1, Ready: 0},
		},
		{
			name: "HCPs in non-ocm namespaces are excluded",
			hcps: []*hypershiftv1beta1.HostedControlPlane{
				hcp("ocm-cluster-1", "hcp-1", true),
				hcp("kube-system", "hcp-2", true),
				hcp("default", "hcp-3", true),
				hcp("ocm-cluster-2", "hcp-4", false),
			},
			want: &capacityreportv1alpha1.HostedControlPlaneCount{Total: 2, Ready: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countHCPs(tt.hcps)
			if got.Total != tt.want.Total || got.Ready != tt.want.Ready {
				t.Errorf("countHCPs() = {Total:%d, Ready:%d}, want {Total:%d, Ready:%d}",
					got.Total, got.Ready, tt.want.Total, tt.want.Ready)
			}
		})
	}
}

func TestComputeAverageFootprint(t *testing.T) {
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
				corev1.ResourceMemory:           resource.MustParse("100Gi"),
				controller.SwiftNICResourceName: resource.MustParse("20"),
			},
			readyCount: 10,
			want: corev1.ResourceList{
				corev1.ResourceMemory:           *resource.NewQuantity(10*1024*1024*1024, resource.BinarySI),
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeAverageFootprint(tt.usage, tt.readyCount)
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
	fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		nodes     []capacityreportv1alpha1.NodeSKUCapacity
		usage     corev1.ResourceList
		requested corev1.ResourceList
		hcpCount  capacityreportv1alpha1.HostedControlPlaneCount
	}{
		{
			name: "full_report_with_average_footprint",
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
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
			requested: corev1.ResourceList{
				corev1.ResourceMemory:           resource.MustParse("8Gi"),
				controller.SwiftNICResourceName: resource.MustParse("6"),
			},
			hcpCount: capacityreportv1alpha1.HostedControlPlaneCount{Total: 3, Ready: 2},
		},
		{
			name: "zero_ready_hcps_omits_average_footprint",
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
			hcpCount: capacityreportv1alpha1.HostedControlPlaneCount{Total: 2, Ready: 0},
		},
		{
			name: "swift_nic_copied_from_requested_to_usage",
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
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
			requested: corev1.ResourceList{
				corev1.ResourceMemory:           resource.MustParse("4Gi"),
				controller.SwiftNICResourceName: resource.MustParse("3"),
			},
			hcpCount: capacityreportv1alpha1.HostedControlPlaneCount{Total: 1, Ready: 1},
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
			hcpCount: capacityreportv1alpha1.HostedControlPlaneCount{Total: 10, Ready: 8},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildReport(tt.nodes, tt.usage, tt.requested, tt.hcpCount, fixedTime)
			testutil.CompareWithFixture(t, got)
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

func pod(namespace, name string, phase corev1.PodPhase, requests corev1.ResourceList) *corev1.Pod {
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

func hcp(namespace, name string, available bool) *hypershiftv1beta1.HostedControlPlane {
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
