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

package api

import (
	"github.com/blang/semver/v4"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

const (
	// ServiceProviderClusterResourceName is the name of the ServiceProviderCluster resource.
	// ServiceProviderCluster is a singleton resource and ARM convention is to
	// use the name "default" for singleton resources.
	ServiceProviderClusterResourceName = "default"
)

// ServiceProviderCluster is used internally by controllers to track and pass information between them.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ServiceProviderCluster struct {
	// CosmosMetadata ResourceID is nested under the cluster so that association and cleanup work as expected
	// it will be the ServiceProviderCluster type and the name default
	CosmosMetadata `json:"cosmosMetadata"`

	LoadBalancerResourceID *azcorearm.ResourceID `json:"loadBalancerResourceID,omitempty"`

	// Spec contains the desired state of the cluster.
	Spec ServiceProviderClusterSpec `json:"spec,omitempty"`

	// Status contains the observed state of the cluster.
	Status ServiceProviderClusterStatus `json:"status,omitempty"`

	// Validations is a list of conditions that tracks the status of each cluster validation.
	// Each Condition Type represents a validation and it should be unique among all validations.
	// A Condition Status of True means that the validation passed successfully, and a Condition Status of False means that the validation failed.
	// The Condition Reason and Message are used to provide more details about the validation status.
	// The Condition LastTransitionTime is used to track the last time the validation transitioned from one status to another.
	Validations []Condition `json:"validations,omitempty"`
}

// ServiceProviderClusterSpec contains the desired state of the cluster.
type ServiceProviderClusterSpec struct {
	// ControlPlaneVersion contains the desired control plane version information.
	// Example JSON structure:
	// {
	//   "control_plane_version": {
	//     "desired_version": "4.19.2"
	//   }
	// }
	ControlPlaneVersion ServiceProviderClusterSpecVersion `json:"control_plane_version,omitempty"`
}

// ServiceProviderClusterSpecVersion contains the desired version information.
type ServiceProviderClusterSpecVersion struct {
	// DesiredVersion is the full version the controller has resolved and wants to upgrade to (format: x.y.z)
	// This is compared on each sync to detect when a new upgrade should be triggered.
	DesiredVersion *semver.Version `json:"desired_version,omitempty"`
}

// ServiceProviderClusterStatus contains the observed state of the cluster.
type ServiceProviderClusterStatus struct {
	// ControlPlaneVersion contains the actual control plane version information.
	// ActiveVersions contains all versions currently active in the control plane.
	// Currently, we maintain up to two versions, but this is designed to hold all active versions
	// and will be expanded to track the complete set when we start reading from Maestro.
	//
	// During an upgrade, multiple versions can be active simultaneously. For example:
	// - Simple upgrade: [vNew, vOld]
	// - Sequential upgrades before completion: [vNewest, vNewer, vNew, vOld]
	//
	// The list is ordered with the most recent version first.
	//
	// Example JSON structure:
	// {
	//   "control_plane_version": {
	//     "active_versions": [
	//       {"version": "4.19.2"},
	//       {"version": "4.19.1"}
	//     ]
	//   }
	// }
	ControlPlaneVersion ServiceProviderClusterStatusVersion `json:"control_plane_version,omitempty"`
}

// ServiceProviderClusterStatusVersion contains the actual version information.
type ServiceProviderClusterStatusVersion struct {
	// ActiveVersions is an array of versions currently active in the control plane, ordered with the most recent first.
	// During upgrades, multiple versions can be active simultaneously.
	ActiveVersions []HCPClusterActiveVersion `json:"active_versions,omitempty"`
}

// HCPClusterActiveVersion represents a single version active in the control plane.
type HCPClusterActiveVersion struct {
	// Version is the full version in x.y.z format (e.g., "4.19.2")
	Version *semver.Version `json:"version,omitempty"`
}
