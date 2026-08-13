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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// CapacityReport is a cluster-scoped resource that reports capacity data
// collected from local Kubernetes APIs on a management cluster. It is written
// by the mgmt-agent capacity reporting controller and consumed by kube-applier's
// ReadDesire mechanism.
type CapacityReport struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// +optional
	Status CapacityReportStatus `json:"status,omitempty,omitzero"`
}

// CapacityReportStatus contains the observed capacity state of a management cluster.
type CapacityReportStatus struct {
	// +optional
	// lastReportedAt is the timestamp of the most recent successful data collection.
	// Consumers should treat stale values (e.g. older than 5 minutes) as an indication
	// that the mgmt-agent is unable to collect data.
	LastReportedAt *metav1.Time `json:"lastReportedAt,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=sku
	// nodes reports per-SKU node counts and allocatable resources for worker nodes.
	// Only nodes labeled aro-hcp.azure.com/role=worker are included.
	Nodes []NodeSKUCapacity `json:"nodes,omitempty"`

	// +optional
	// usage reports actual resource consumption across HCP workloads in ocm-*
	// namespaces. Memory is sourced from metrics-server PodMetrics. SWIFT NIC
	// usage equals pod requests — NICs are discrete, non-compressible resources
	// where requested == consumed.
	Usage corev1.ResourceList `json:"usage,omitempty"`

	// +optional
	// requested reports the sum of resource requests across regular (non-init)
	// containers of non-terminal pods in ocm-* namespaces. This approximates steady-state
	// reservation and may differ from actual usage.
	Requested corev1.ResourceList `json:"requested,omitempty"`

	// +optional
	// hostedControlPlanes reports the total and ready HostedControlPlane count on
	// the management cluster. An HCP is "ready" when its Available condition is True.
	HostedControlPlanes *HostedControlPlaneCount `json:"hostedControlPlanes,omitempty"`

	// +optional
	// averageHCPFootprint is the per-HCP average of actual resource usage, derived
	// as usage divided by hcps.ready. Omitted when hcps.ready is zero.
	AverageHCPFootprint corev1.ResourceList `json:"averageHCPFootprint,omitempty"`
}

// NodeSKUCapacity reports the number of ready and not-ready worker nodes for a
// given VM SKU, along with the total allocatable resources across ready nodes.
type NodeSKUCapacity struct {
	// sku is the VM instance type from the node.kubernetes.io/instance-type label.
	SKU string `json:"sku"`
	// ready is the number of worker nodes of this SKU that have a Ready condition of True.
	Ready int32 `json:"ready"`
	// notReady is the number of worker nodes of this SKU that are not Ready.
	NotReady int32 `json:"notReady"`
	// allocatable is the sum of node.status.allocatable across ready nodes of this
	// SKU. NotReady nodes contribute zero to this total.
	Allocatable corev1.ResourceList `json:"allocatable,omitempty"`
}

// HostedControlPlaneCount reports the total and ready HostedControlPlane count on a
// management cluster.
type HostedControlPlaneCount struct {
	// total is the count of all HostedControlPlane resources on the management cluster.
	Total int32 `json:"total"`
	// ready is the count of HostedControlPlane resources whose Available condition is True.
	Ready int32 `json:"ready"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// CapacityReportList is a list of CapacityReport resources.
type CapacityReportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty,omitzero"`
	Items           []CapacityReport `json:"items"`
}
