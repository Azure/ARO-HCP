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
	// usage reports consumption across HCP namespaces from PodMetrics, except
	// aro.openshift.io/swift-nic usage, which equals its requested allocation.
	Usage corev1.ResourceList `json:"usage,omitempty"`

	// +optional
	// requested sums regular-container requests for Pending and Running pods in
	// HCP namespaces, excluding init containers and pod overhead. This approximates
	// steady-state reservation and may differ from actual usage.
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
// management cluster. Resource totals measure HCP workloads; overall identity lists
// and per-size ready counts deduplicate ARM resource IDs by exact, case-sensitive
// string equality, rather than counting measured HCP objects. Legacy reports may
// omit resources, readyResources and byClusterSizingConfiguration. Failed collections preserve the
// existing payload, including absent fields.
// +structType=atomic
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

	// +optional
	// resources reports usage and requests for all HCPs, including those without
	// usable size attribution. Nil means not reported, not zero. Successful
	// collections by new collectors always include this object, even with no HCPs,
	// and dual-write the same totals to the legacy status.usage and status.requested.
	Resources *HostedControlPlaneResourceTotals `json:"resources,omitempty"`

	// +optional
	// readyResources reports usage and requests for all ready HCPs, including
	// those without usable size attribution. Nil means not reported, not zero.
	// Successful collections by new collectors always include this object, even
	// with no ready HCPs.
	ReadyResources *HostedControlPlaneResourceTotals `json:"readyResources,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=size
	// byClusterSizingConfiguration groups HCPs by the associated HostedCluster's observed
	// hypershift.openshift.io/hosted-cluster-size label. The HCP's
	// hypershift.openshift.io/cluster annotation identifies that HC as namespace/name.
	// Missing associations or noncanonical/reserved labels contribute only to overall
	// accounting, so named buckets are not exhaustive. If no HCP has usable attribution,
	// this list is omitted or empty; that alone does not indicate a legacy report.
	ByClusterSizingConfiguration []HostedControlPlanesBySize `json:"byClusterSizingConfiguration,omitempty"`
}

// HostedControlPlaneResourceTotals reports resource usage and requests for an HCP
// population. Successful collections include CPU, memory and aro.openshift.io/swift-nic
// in both maps, with zero values when the measured population is empty.
type HostedControlPlaneResourceTotals struct {
	// +kubebuilder:validation:MaxProperties=16
	// usage reports consumption from PodMetrics, except aro.openshift.io/swift-nic
	// usage, which equals its requested allocation.
	Usage corev1.ResourceList `json:"usage"`
	// +kubebuilder:validation:MaxProperties=16
	// requested sums regular-container requests for Pending and Running pods,
	// excluding init containers and pod overhead.
	Requested corev1.ResourceList `json:"requested"`
}

// HostedControlPlanesBySize reports HCPs with the same observed HostedCluster size.
// Resource totals measure the bucket's ready HCP workloads; readyCount counts distinct
// ready ARM resource IDs, not measured HCP objects.
type HostedControlPlanesBySize struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9_.]*[a-z0-9])?$`
	// +kubebuilder:validation:XValidation:rule="self != 'default'",message="default is reserved for overall accounting"
	// size is the canonical lowercase observed label value, excluding default.
	Size string `json:"size"`
	// readyResources reports usage and requests for ready HCPs in this size
	// bucket. This object is required even when no HCP in the bucket is ready;
	// an empty ready population is reported with zero resource totals.
	ReadyResources HostedControlPlaneResourceTotals `json:"readyResources"`
	// +kubebuilder:validation:Minimum=0
	// readyCount counts distinct ARM resource IDs of HCPs in this size bucket
	// whose Available condition is True, using exact, case-sensitive string
	// equality. It is zero when no HCP in the bucket is ready.
	ReadyCount int32 `json:"readyCount"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// CapacityReportList is a list of CapacityReport resources.
type CapacityReportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CapacityReport `json:"items"`
}
