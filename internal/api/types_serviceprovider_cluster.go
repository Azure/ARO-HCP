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
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

const (
	// ServiceProviderClusterResourceName is the name of the ServiceProviderCluster resource.
	// ServiceProviderCluster is a singleton resource and ARM convention is to
	// use the name "default" for singleton resources.
	ServiceProviderClusterResourceName = "default"
)

// ServiceProviderCluster is used internally by controllers to track and pass information between them.
type ServiceProviderCluster struct {
	// CosmosMetadata ResourceID is nested under the cluster so that association and cleanup work as expected
	// it will be the ServiceProviderCluster type and the name default
	CosmosMetadata `json:"cosmosMetadata"`

	// resourceID exists to match cosmosMetadata.resourceID until we're able to transition all types to use cosmosMetadata,
	// at which point we will stop using properties.resourceId in our queries. That will be about a month from now.
	ResourceID azcorearm.ResourceID `json:"resourceId"`

	LoadBalancerResourceID *azcorearm.ResourceID `json:"loadBalancerResourceID,omitempty"`

	// Version tracks the cluster control plane version information.
	// Example JSON structure:
	// {
	//   "desired_minor": "4.19",
	//   "desired_full_version": "4.19.2",
	//   "channelGroup": "stable",
	//   "status": [
	//     {"desired": "4.19.2", "actual": "4.19.1", "lastTransitionTime": "2026-01-30T10:00:00Z"},
	//     {"desired": "4.19.1", "actual": "4.19.1", "lastTransitionTime": "2026-01-15T08:30:00Z"}
	//   ]
	// }
	Version *HCPClusterVersion `json:"version,omitempty"`

	// Validations is a list of conditions that tracks the status of each cluster validation.
	// Each Condition Type represents a validation and it should be unique among all validations.
	// A Condition Status of True means that the validation passed successfully, and a Condition Status of False means that the validation failed.
	// The Condition Reason and Message are used to provide more details about the validation status.
	// The Condition LastTransitionTime is used to track the last time the validation transitioned from one status to another.
	Validations []Condition `json:"validations,omitempty"`
}

// HCPClusterVersion represents the OpenShift Container Platform version information for a cluster.
type HCPClusterVersion struct {
	// DesiredMinor is the user's desired minor version in x.y format (e.g., "4.19")
	DesiredMinor string `json:"desired_minor,omitempty"`

	// DesiredFullVersion is the controller's resolved full version with z-stream in x.y.z format (e.g., "4.19.2")
	DesiredFullVersion string `json:"desired_full_version,omitempty"`

	// ChannelGroup is the channel group of the version (e.g., "stable", "candidate", "fast")
	ChannelGroup string `json:"channelGroup,omitempty"`

	// Status is an array of version reconciliation states, ordered with the most recent first
	Status []HCPClusterVersionStatus `json:"status,omitempty"`
}

// HCPClusterVersionStatus represents a single reconciliation state for a version upgrade.
type HCPClusterVersionStatus struct {
	// Desired is the version that was being reconciled to at that time (format: x.y.z)
	Desired string `json:"desired,omitempty"`

	// Actual is the actual state of the world as seen by the controller (format: x.y.z)
	// Can be empty, indicating we haven't yet reconciled the target version or obtained the actual state.
	Actual string `json:"actual,omitempty"`

	// LastTransitionTime is the timestamp when this version decision was made
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
}
