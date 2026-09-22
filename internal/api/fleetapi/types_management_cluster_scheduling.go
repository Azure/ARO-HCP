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

package fleetapi

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

const (
	// ConditionTypeCapacityDataCurrent indicates whether the capacity data
	// in the scheduling document is up to date. Set by
	// CapacityReportingController after evaluating the CapacityReport CR's
	// state.
	ConditionTypeCapacityDataCurrent = "CapacityDataCurrent"

	// ConditionTypeScalingDataCurrent indicates whether the scaling data
	// in the scheduling document is up to date. Set by
	// ScaleCeilingReportingController after computing max capacity.
	ConditionTypeScalingDataCurrent = "ScalingDataCurrent"
)

// ManagementClusterScheduling is a singleton child of ManagementCluster
// holding scheduling-relevant data from the actual management cluster.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ManagementClusterScheduling struct {
	// CosmosMetadata ResourceID is nested under the management cluster; it
	// will be SchedulingResourceName ("default").
	// PartitionKey holds the lowercased stamp identifier of the parent Stamp.
	coreapi.CosmosMetadata `json:"cosmosMetadata"`

	// Status contains the observed scheduling state of the management cluster.
	Status ManagementClusterSchedulingStatus `json:"status"`
}

// ManagementClusterSchedulingStatus contains the observed scheduling state of
// a management cluster.
type ManagementClusterSchedulingStatus struct {
	// Conditions is the aggregate set of conditions reported by all
	// scheduling controllers. Each controller owns its own condition type:
	// CapacityReportingController sets ConditionTypeCapacityDataCurrent,
	// ScaleCeilingReportingController sets ConditionTypeScalingDataCurrent.
	//
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedResources is the observed worker resource state, mirrored
	// from the management cluster's CapacityReport CR.
	//
	// Written by: CapacityReportingController.
	ObservedResources ObservedResources `json:"observedResources"`

	// ReadyResourceIDs lists the ARM resource IDs of the HCPs whose
	// HostedCluster on this management cluster is ready (Available condition
	// True), parsed from the CapacityReport CR's
	// Status.HostedControlPlanes.ReadyResourceIDs.
	//
	// +optional
	// Written by: CapacityReportingController.
	ReadyResourceIDs []*azcorearm.ResourceID `json:"readyResourceIDs,omitempty"`

	// NotReadyResourceIDs lists the ARM resource IDs of the HCPs whose
	// HostedCluster on this management cluster exists but is not ready
	// (Available condition not True or missing), parsed from the
	// CapacityReport CR's Status.HostedControlPlanes.NotReadyResourceIDs.
	//
	// +optional
	// Written by: CapacityReportingController.
	NotReadyResourceIDs []*azcorearm.ResourceID `json:"notReadyResourceIDs,omitempty"`

	// ScaleCeiling holds projected capacity limits derived from AKS agent
	// pool configuration and SKU data.
	//
	// Written by: ScaleCeilingReportingController.
	ScaleCeiling ScaleCeiling `json:"scaleCeiling"`

	// PendingAssignedClusters holds the ARM resource IDs of HCPs the scheduler
	// has just placed on this management cluster but whose HostedCluster is not
	// yet observed in the CapacityReport (i.e. not yet in ReadyResourceIDs or
	// NotReadyResourceIDs). Each pending entry reserves swift-NIC capacity so
	// concurrent placement decisions do not overbook a management cluster before
	// the workload shows up in the observed capacity data. Entries are removed
	// once observed (by CapacityReportingController) or when the reservation
	// becomes stale (by PendingCleanupController).
	//
	// +optional
	// Written by: PlacementController, CapacityReportingController, PendingCleanupController.
	PendingAssignedClusters []*azcorearm.ResourceID `json:"pendingAssignedClusters,omitempty"`
}

// ObservedResources reports the observed worker resource state of a
// management cluster.
type ObservedResources struct {
	// LastReportedAt is the CapacityReport CR's LastReportedAt timestamp,
	// propagated verbatim.
	//
	// +optional
	LastReportedAt *metav1.Time `json:"lastReportedAt,omitempty"`

	// Capacity is the worker capacity available at the AKS agent pools'
	// current node counts. Only scheduling-relevant resources are
	// propagated: cpu, memory, aro.openshift.io/swift-nic.
	//
	// +optional
	Capacity corev1.ResourceList `json:"capacity,omitempty"`

	// Usage is the CapacityReport CR's reported resource usage across HCP
	// workloads. Only scheduling-relevant resources are propagated:
	// cpu, memory, aro.openshift.io/swift-nic.
	//
	// +optional
	Usage corev1.ResourceList `json:"usage,omitempty"`

	// Requests is the sum of resource requests across HCP workloads,
	// mirrored from the CapacityReport CR. Only scheduling-relevant
	// resources are propagated: cpu, memory, aro.openshift.io/swift-nic.
	//
	// +optional
	Requests corev1.ResourceList `json:"requests,omitempty"`
}

// ScaleCeiling holds projected capacity limits derived from AKS agent pool
// configuration and SKU data.
type ScaleCeiling struct {
	// LastReportedAt is the time the scaling data was last computed.
	//
	// +optional
	LastReportedAt *metav1.Time `json:"lastReportedAt,omitempty"`

	// Capacity is the worker capacity available at the AKS agent pools'
	// maximum node counts.
	//
	// +optional
	Capacity corev1.ResourceList `json:"capacity,omitempty"`
}
