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
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	kubeinformers "k8s.io/client-go/informers"
	corelisters "k8s.io/client-go/listers/core/v1"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/Azure/ARO-Tools/testutil"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftinformers "github.com/openshift/hypershift/client/informers/externalversions"

	"github.com/Azure/ARO-HCP/internal/controllerutils"
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
			name: "allocatable passes through all resource types",
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
						corev1.ResourceEphemeralStorage: resource.MustParse("500Gi"),
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

	hcpNamespaces := sets.New("ocm-cluster-1", "ocm-cluster-2")

	tests := []struct {
		name string
		pods []*corev1.Pod
		want corev1.ResourceList
	}{
		{
			name: "no pods",
			pods: nil,
			want: newZeroFloorResourceList(),
		},
		{
			name: "pod in HCP namespace",
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
			name: "pod outside HCP namespace excluded",
			pods: []*corev1.Pod{
				newPod("kube-system", "pod-1", corev1.PodRunning, corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				}),
			},
			want: newZeroFloorResourceList(),
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
			want: newZeroFloorResourceList(),
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
				corev1.ResourceCPU:              resource.MustParse("0"),
				corev1.ResourceMemory:           resource.MustParse("6Gi"),
				controller.SwiftNICResourceName: resource.MustParse("6"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resources := collectHCPResources(tt.pods, nil, hcpNamespaces)
			totals, _ := sumHCPResources([]*hypershiftv1beta1.HostedControlPlane{
				newHostedControlPlane("ocm-cluster-1", "one", true),
				newHostedControlPlane("ocm-cluster-2", "two", true),
			}, resources)
			got := totals.Requested
			if diff := cmp.Diff(tt.want, got); len(diff) > 0 {
				t.Errorf("unexpected resource list (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAggregatePodMetrics(t *testing.T) {
	t.Parallel()

	hcpNamespaces := sets.New("ocm-cluster-1", "ocm-cluster-2")

	tests := []struct {
		name    string
		metrics []metricsv1beta1.PodMetrics
		want    corev1.ResourceList
	}{
		{
			name:    "no metrics",
			metrics: nil,
			want:    newZeroFloorResourceList(),
		},
		{
			name: "metrics in HCP namespace",
			metrics: []metricsv1beta1.PodMetrics{
				podMetrics("ocm-cluster-1", "pod-1", corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("750m"),
					corev1.ResourceMemory: resource.MustParse("3Gi"),
				}),
			},
			want: corev1.ResourceList{
				corev1.ResourceCPU:              *resource.NewMilliQuantity(750, resource.DecimalSI),
				corev1.ResourceMemory:           resource.MustParse("3Gi"),
				controller.SwiftNICResourceName: resource.MustParse("0"),
			},
		},
		{
			name: "metrics outside HCP namespace excluded",
			metrics: []metricsv1beta1.PodMetrics{
				podMetrics("kube-system", "pod-1", corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("3Gi"),
				}),
			},
			want: newZeroFloorResourceList(),
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
				corev1.ResourceCPU:              *resource.NewMilliQuantity(3500, resource.DecimalSI),
				corev1.ResourceMemory:           resource.MustParse("5Gi"),
				controller.SwiftNICResourceName: resource.MustParse("0"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resources := collectHCPResources(nil, tt.metrics, hcpNamespaces)
			totals, _ := sumHCPResources([]*hypershiftv1beta1.HostedControlPlane{
				newHostedControlPlane("ocm-cluster-1", "one", true),
				newHostedControlPlane("ocm-cluster-2", "two", true),
			}, resources)
			got := totals.Usage
			if diff := cmp.Diff(tt.want, got); len(diff) > 0 {
				t.Errorf("unexpected resource list (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCollectHostedControlPlanes(t *testing.T) {
	t.Parallel()

	armResourceID := func(sub, rg, name string) string {
		return "/subscriptions/" + sub + "/resourceGroups/" + rg + "/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/" + name
	}

	tests := []struct {
		name       string
		hcps       []*hypershiftv1beta1.HostedControlPlane
		namespaces []*corev1.Namespace
		want       capacityreportv1alpha1.HostedControlPlanes
	}{
		{
			name: "no HCPs",
			hcps: nil,
			want: capacityreportv1alpha1.HostedControlPlanes{},
		},
		{
			name: "all ready with annotated namespaces sorted by resource ID",
			hcps: []*hypershiftv1beta1.HostedControlPlane{
				newHostedControlPlane("ocm-ns-1", "cluster-b", true),
				newHostedControlPlane("ocm-ns-2", "cluster-a", true),
			},
			namespaces: []*corev1.Namespace{
				newAnnotatedNamespace("ocm-ns-1", armResourceID("sub-2", "rg-2", "cluster-b")),
				newAnnotatedNamespace("ocm-ns-2", armResourceID("sub-1", "rg-1", "cluster-a")),
			},
			want: capacityreportv1alpha1.HostedControlPlanes{
				ReadyResourceIDs: []string{
					armResourceID("sub-1", "rg-1", "cluster-a"),
					armResourceID("sub-2", "rg-2", "cluster-b"),
				},
			},
		},
		{
			name: "mixed ready and not-ready",
			hcps: []*hypershiftv1beta1.HostedControlPlane{
				newHostedControlPlane("ocm-ns-1", "cluster-a", true),
				newHostedControlPlane("ocm-ns-2", "cluster-b", false),
			},
			namespaces: []*corev1.Namespace{
				newAnnotatedNamespace("ocm-ns-1", armResourceID("sub-1", "rg-1", "cluster-a")),
				newAnnotatedNamespace("ocm-ns-2", armResourceID("sub-2", "rg-2", "cluster-b")),
			},
			want: capacityreportv1alpha1.HostedControlPlanes{
				ReadyResourceIDs: []string{
					armResourceID("sub-1", "rg-1", "cluster-a"),
				},
				NotReadyResourceIDs: []string{
					armResourceID("sub-2", "rg-2", "cluster-b"),
				},
			},
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
			namespaces: []*corev1.Namespace{
				newAnnotatedNamespace("ocm-cluster-1", armResourceID("sub-1", "rg-1", "cluster-1")),
			},
			want: capacityreportv1alpha1.HostedControlPlanes{
				NotReadyResourceIDs: []string{armResourceID("sub-1", "rg-1", "cluster-1")},
			},
		},
		{
			name: "HCP with no conditions is not ready",
			hcps: []*hypershiftv1beta1.HostedControlPlane{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "hcp-1", Namespace: "ocm-cluster-1"},
				},
			},
			namespaces: []*corev1.Namespace{
				newAnnotatedNamespace("ocm-cluster-1", armResourceID("sub-1", "rg-1", "cluster-1")),
			},
			want: capacityreportv1alpha1.HostedControlPlanes{
				NotReadyResourceIDs: []string{armResourceID("sub-1", "rg-1", "cluster-1")},
			},
		},
		{
			name: "namespace without annotation is skipped",
			hcps: []*hypershiftv1beta1.HostedControlPlane{
				newHostedControlPlane("ocm-ns-1", "cluster-a", true),
			},
			namespaces: []*corev1.Namespace{
				{ObjectMeta: metav1.ObjectMeta{Name: "ocm-ns-1"}},
			},
			want: capacityreportv1alpha1.HostedControlPlanes{},
		},
		{
			name: "namespace not found is skipped",
			hcps: []*hypershiftv1beta1.HostedControlPlane{
				newHostedControlPlane("ocm-ns-1", "cluster-a", true),
			},
			namespaces: nil,
			want:       capacityreportv1alpha1.HostedControlPlanes{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			for _, ns := range tt.namespaces {
				if err := indexer.Add(ns); err != nil {
					t.Fatal(err)
				}
			}
			nsLister := corelisters.NewNamespaceLister(indexer)
			c := &CapacityReportController{namespaceLister: nsLister}
			got := c.collectHostedControlPlanes(tt.hcps, nil)
			totals, readyTotals := newResourceTotals(), newResourceTotals()
			tt.want.Resources = &totals
			tt.want.ReadyResources = &readyTotals
			if diff := cmp.Diff(tt.want, got); len(diff) > 0 {
				t.Errorf("unexpected result (-want +got):\n%s", diff)
			}
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
				HostedControlPlanes: capacityreportv1alpha1.HostedControlPlanes{
					Resources: &capacityreportv1alpha1.HostedControlPlaneResourceTotals{
						Usage:     corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("4Gi")},
						Requested: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("8Gi")},
					},
					ReadyResources: &capacityreportv1alpha1.HostedControlPlaneResourceTotals{
						Usage:     corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("3Gi")},
						Requested: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("6Gi")},
					},
					ByClusterSizingConfiguration: []capacityreportv1alpha1.HostedControlPlanesBySize{{
						Size: "small",
						ReadyResources: capacityreportv1alpha1.HostedControlPlaneResourceTotals{
							Usage:     corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("3Gi")},
							Requested: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("6Gi")},
						},
						ReadyCount: 1,
					}},
					ReadyResourceIDs: []string{
						"/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/cluster-a",
						"/subscriptions/sub-2/resourceGroups/rg-2/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/cluster-b",
					},
					NotReadyResourceIDs: []string{
						"/subscriptions/sub-3/resourceGroups/rg-3/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/cluster-c",
					},
				},
			},
		},
		{
			name: "preserves_status_without_optional_fields",
			existingStatus: capacityreportv1alpha1.CapacityReportStatus{
				HostedControlPlanes: capacityreportv1alpha1.HostedControlPlanes{
					ReadyResourceIDs: []string{
						"/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/cluster-a",
						"/subscriptions/sub-2/resourceGroups/rg-2/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/cluster-b",
					},
				},
			},
		},
		{
			name: "preserves_hcp_resource_ids",
			existingStatus: capacityreportv1alpha1.CapacityReportStatus{
				HostedControlPlanes: capacityreportv1alpha1.HostedControlPlanes{
					ReadyResourceIDs: []string{
						"/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/cluster-a",
						"/subscriptions/sub-2/resourceGroups/rg-2/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/cluster-b",
					},
					NotReadyResourceIDs: []string{
						"/subscriptions/sub-3/resourceGroups/rg-3/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/cluster-c",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			kube := kubeinformers.NewSharedInformerFactory(nil, 0)
			hs := hypershiftinformers.NewSharedInformerFactory(nil, 0)
			collectionErr := errors.New("test error")
			metricsClient := metricsfake.NewSimpleClientset()
			metricsClient.PrependReactor("list", "pods", func(clienttesting.Action) (bool, metav1runtime.Object, error) {
				return true, nil, collectionErr
			})
			c := &CapacityReportController{
				nodeLister:      kube.Core().V1().Nodes().Lister(),
				podLister:       kube.Core().V1().Pods().Lister(),
				namespaceLister: kube.Core().V1().Namespaces().Lister(),
				hcpLister:       hs.Hypershift().V1beta1().HostedControlPlanes().Lister(),
				hcLister:        hs.Hypershift().V1beta1().HostedClusters().Lister(),
				metricsClient:   metricsClient,
			}
			got, err := c.buildCapacityReportUpdate(t.Context(), tt.existingStatus, fixedTime)
			if !errors.Is(err, collectionErr) {
				t.Fatalf("expected metrics collection failure, got %v", err)
			}
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

func newAnnotatedNamespace(name, armResourceID string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Annotations: map[string]string{
				controllerutils.HcpClusterAzureResourceIdAnnotation: armResourceID,
			},
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

func TestCapacityReportCollection(t *testing.T) {
	t.Parallel()
	legacyResourceIDs := []string{
		"/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/cluster-a",
		"/subscriptions/sub-2/resourceGroups/rg-2/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/cluster-b",
		"/subscriptions/sub-3/resourceGroups/rg-3/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/cluster-c",
	}
	tests := []struct {
		name            string
		sizes           []string
		ready           []bool
		wantReadyCPU    string
		wantSizes       []string
		wantReadyCounts []int32
		duplicateIDs    bool
		associations    []string
		missingHC       bool
		resourceIDs     []string
		requests        []corev1.ResourceList
		usage           []corev1.ResourceList
		reportedAt      time.Time
	}{
		{
			name:        "full_report",
			sizes:       []string{"", "", ""},
			ready:       []bool{true, true, false},
			resourceIDs: legacyResourceIDs,
			requests: []corev1.ResourceList{
				{
					corev1.ResourceMemory:           resource.MustParse("8Gi"),
					controller.SwiftNICResourceName: resource.MustParse("6"),
				}, {}, {},
			},
			usage: []corev1.ResourceList{
				{
					corev1.ResourceMemory:           resource.MustParse("4Gi"),
					controller.SwiftNICResourceName: resource.MustParse("6"),
				}, {}, {},
			},
			reportedAt:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			wantReadyCPU: "0",
		},
		{
			name:        "multiple_skus",
			sizes:       []string{"", "", ""},
			ready:       []bool{true, true, false},
			resourceIDs: legacyResourceIDs,
			requests: []corev1.ResourceList{
				{corev1.ResourceMemory: resource.MustParse("200Gi")}, {}, {},
			},
			usage: []corev1.ResourceList{
				{corev1.ResourceMemory: resource.MustParse("100Gi")}, {}, {},
			},
			reportedAt:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			wantReadyCPU: "0",
		},
		{name: "mixed_sizes_and_readiness", sizes: []string{"small", "small", "small", "medium", "", "medium"}, ready: []bool{true, true, false, true, true, true}, duplicateIDs: true, wantReadyCPU: "5", wantSizes: []string{"medium", "small"}, wantReadyCounts: []int32{2, 1}},
		{name: "no_ready_population", sizes: []string{"small"}, ready: []bool{false}, wantReadyCPU: "0", wantSizes: []string{"small"}, wantReadyCounts: []int32{0}},
		{name: "unattributed_ready_population", sizes: []string{""}, ready: []bool{true}, wantReadyCPU: "1"},
		{name: "empty_cluster", wantReadyCPU: "0"},
		{name: "canonical_labels_only", sizes: []string{"default", "DEFAULT", "Small", " small", "small/large", "e2e_minimal"}, ready: []bool{true, true, true, true, true, true}, wantReadyCPU: "6", wantSizes: []string{"e2e_minimal"}, wantReadyCounts: []int32{1}},
		{name: "unusable_associations", sizes: []string{"small", "small", "small", "small", "small"}, ready: []bool{true, true, true, true, true}, associations: []string{"", "cluster-1", "/cluster-2", "clusters/", "clusters/cluster-4/extra"}, wantReadyCPU: "5"},
		{name: "missing_hostedcluster", sizes: []string{"small"}, ready: []bool{true}, missingHC: true, wantReadyCPU: "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			kube := kubeinformers.NewSharedInformerFactory(nil, 0)
			hs := hypershiftinformers.NewSharedInformerFactory(nil, 0)
			var metrics []metricsv1beta1.PodMetrics
			for i, size := range tt.sizes {
				name := fmt.Sprintf("cluster-%d", i)
				namespace := "hcp-" + name
				hcp := newHostedControlPlane(namespace, name, tt.ready[i])
				hcp.Annotations = map[string]string{"hypershift.openshift.io/cluster": "clusters/" + name}
				if tt.associations != nil {
					hcp.Annotations["hypershift.openshift.io/cluster"] = tt.associations[i]
				}
				// A mirrored HCP label must not override the associated HC observation.
				hcp.Labels = map[string]string{hypershiftv1beta1.HostedClusterSizeLabel: "ignored"}
				identityName := name
				if tt.duplicateIDs && i == 1 {
					identityName = "cluster-0"
				}
				hc := &hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{
					Name: name, Namespace: "clusters",
					Labels: map[string]string{hypershiftv1beta1.HostedClusterSizeLabel: size},
				}}
				if !tt.missingHC {
					if err := hs.Hypershift().V1beta1().HostedClusters().Informer().GetIndexer().Add(hc); err != nil {
						t.Fatal(err)
					}
				}
				resourceID := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/" + identityName
				if tt.resourceIDs != nil {
					resourceID = tt.resourceIDs[i]
				}
				requests := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), controller.SwiftNICResourceName: resource.MustParse("1")}
				if tt.requests != nil {
					requests = tt.requests[i]
				}
				usage := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")}
				if tt.usage != nil {
					usage = tt.usage[i]
				}
				for _, entry := range []struct {
					indexer cache.Indexer
					object  interface{}
				}{
					{hs.Hypershift().V1beta1().HostedControlPlanes().Informer().GetIndexer(), hcp},
					{kube.Core().V1().Namespaces().Informer().GetIndexer(), newAnnotatedNamespace(namespace, resourceID)},
					{kube.Core().V1().Pods().Informer().GetIndexer(), newPod(namespace, "pod", corev1.PodRunning, requests)},
				} {
					if err := entry.indexer.Add(entry.object); err != nil {
						t.Fatal(err)
					}
				}
				metric := podMetrics(namespace, "pod", usage)
				metrics = append(metrics, metric)
			}
			metricsClient := metricsfake.NewSimpleClientset()
			metricsClient.PrependReactor("list", "pods", func(clienttesting.Action) (bool, metav1runtime.Object, error) {
				return true, &metricsv1beta1.PodMetricsList{Items: metrics}, nil
			})
			c := &CapacityReportController{
				nodeLister:      kube.Core().V1().Nodes().Lister(),
				podLister:       kube.Core().V1().Pods().Lister(),
				namespaceLister: kube.Core().V1().Namespaces().Lister(),
				hcpLister:       hs.Hypershift().V1beta1().HostedControlPlanes().Lister(),
				hcLister:        hs.Hypershift().V1beta1().HostedClusters().Lister(),
				metricsClient:   metricsClient,
			}
			reportedAt := tt.reportedAt
			if reportedAt.IsZero() {
				reportedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			}
			report, err := c.buildCapacityReportUpdate(t.Context(), capacityreportv1alpha1.CapacityReportStatus{}, reportedAt)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			var observed capacityreportv1alpha1.CapacityReport
			if err := json.Unmarshal(data, &observed); err != nil {
				t.Fatal(err)
			}
			if observed.Status.HostedControlPlanes.ReadyResources == nil {
				t.Fatal("successful collection omitted readyResources")
			}
			if got := observed.Status.HostedControlPlanes.ReadyResources.Usage.Cpu(); got.Cmp(resource.MustParse(tt.wantReadyCPU)) != 0 {
				t.Fatalf("ready CPU = %s, want %s", got, tt.wantReadyCPU)
			}
			var sizes []string
			var readyCounts []int32
			for _, bucket := range observed.Status.HostedControlPlanes.ByClusterSizingConfiguration {
				sizes = append(sizes, bucket.Size)
				readyCounts = append(readyCounts, bucket.ReadyCount)
			}
			if diff := cmp.Diff(tt.wantSizes, sizes); len(diff) > 0 {
				t.Fatalf("size buckets (-want +got): %s", diff)
			}
			if diff := cmp.Diff(tt.wantReadyCounts, readyCounts); len(diff) > 0 {
				t.Fatalf("ready counts (-want +got): %s", diff)
			}
			testutil.CompareWithFixture(t, report)
		})
	}
}
