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

const (
	// ConditionTypeReportCurrent indicates whether the most recent data
	// collection succeeded. True means the status fields reflect a successful
	// collection; False means collection failed and the data may be stale.
	ConditionTypeReportCurrent = "ReportCurrent"

	// ReasonDataCollected is set when all data sources were queried successfully.
	ReasonDataCollected = "DataCollected"
	// ReasonDataCollectionFailed is set when one or more data sources could not
	// be queried. The condition message contains the error detail.
	ReasonDataCollectionFailed = "DataCollectionFailed"
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
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +optional
	Status CapacityReportStatus `json:"status,omitempty"`
}

// CapacityReportStatus contains the observed capacity state of a management cluster.
type CapacityReportStatus struct {
	// +optional
	// +listType=map
	// +listMapKey=type
	// conditions reports the health of the capacity reporting process.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	// lastReportedAt is the timestamp of the most recent successful data collection.
	// Consumers should treat stale values (e.g. older than 5 minutes) as an indication
	// that the mgmt-agent is unable to collect data.
	LastReportedAt *metav1.Time `json:"lastReportedAt,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=sku
	// +kubebuilder:validation:MaxItems=64
	// nodes reports per-SKU node counts and allocatable resources for worker nodes.
	// Only nodes labeled aro-hcp.azure.com/role=worker are included.
	Nodes []NodeSKUCapacity `json:"nodes,omitempty"`

	// +optional
	// usage reports actual resource consumption across HCP workloads in ocm-*
	// namespaces, sourced from metrics-server PodMetrics.
	Usage corev1.ResourceList `json:"usage,omitempty"`

	// +optional
	// requested reports the sum of resource requests across regular (non-init)
	// containers of non-terminal pods (phase is not Succeeded or Failed) in ocm-*
	// namespaces. This approximates steady-state reservation and may differ from
	// actual usage.
	Requested corev1.ResourceList `json:"requested,omitempty"`

	// hostedControlPlanes reports the state of HostedControlPlane resources on
	// the management cluster. An HCP is "ready" when its Available condition is True.
	HostedControlPlanes HostedControlPlanes `json:"hostedControlPlanes"`
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

// HostedControlPlanes reports the state of HostedControlPlane resources on a
// management cluster.
type HostedControlPlanes struct {
	// +optional
	// +listType=set
	// readyResourceIDs lists the ARM resource IDs of HostedControlPlanes whose
	// Available condition is True.
	ReadyResourceIDs []string `json:"readyResourceIDs,omitempty"`
	// +optional
	// +listType=set
	// notReadyResourceIDs lists the ARM resource IDs of HostedControlPlanes whose
	// Available condition is not True or missing.
	NotReadyResourceIDs []string `json:"notReadyResourceIDs,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// CapacityReportList is a list of CapacityReport resources.
type CapacityReportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CapacityReport `json:"items"`
}
