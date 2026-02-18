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

package api

import (
	"github.com/blang/semver/v4"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

const (
	// ServiceProviderNodePoolResourceName is the name of the ServiceProviderNodePool resource.
	// ServiceProviderNodePool is a singleton resource and ARM convention is to
	// use the name "default" for singleton resources.
	ServiceProviderNodePoolResourceName = "default"
)

// ServiceProviderNodePool is used internally by controllers to track and pass information between them.
type ServiceProviderNodePool struct {
	// CosmosMetadata ResourceID is nested under the cluster so that association and cleanup work as expected
	// it will be the ServiceProviderNodePool type and the name default
	CosmosMetadata `json:"cosmosMetadata"`

	// resourceID exists to match cosmosMetadata.resourceID until we're able to transition all types to use cosmosMetadata,
	// at which point we will stop using properties.resourceId in our queries. That will be about a month from now.
	ResourceID azcorearm.ResourceID `json:"resourceId"`

	// Spec contains the desired state of the nodepool
	Spec ServiceProviderNodePoolSpec `json:"spec,omitempty"`

	// Status contains the observed state of the nodepool
	Status ServiceProviderNodePoolStatus `json:"status,omitempty"`
}

// ServiceProviderNodePoolSpec contains the desired state of the nodepool.
type ServiceProviderNodePoolSpec struct {
	// NodePoolVersion contains the desired node pool version information.
	// Example JSON structure:
	// {
	//   "nodepool_version": {
	//     "desired_version": "4.19.2"
	//   }
	// }
	NodePoolVersion ServiceProviderNodePoolSpecVersion `json:"control_plane_version,omitempty"`
}

// ServiceProviderNodePoolSpecVersion contains the desired version information.
type ServiceProviderNodePoolSpecVersion struct {
	// DesiredVersion is the full version the controller wants to upgrade to (format: x.y.z)
	DesiredVersion *semver.Version `json:"desired_version,omitempty"`
}

// ServiceProviderNodePoolStatus contains the observed state of the node pool.
type ServiceProviderNodePoolStatus struct {
	NodePoolVersion ServiceProviderNodePoolStatusVersion `json:"nodepool_version,omitempty"`
}

// ServiceProviderNodePoolStatusVersion contains the actual version information.
type ServiceProviderNodePoolStatusVersion struct {
	// ActiveVersions is an array of versions currently active in the nodepool, ordered with the most recent first.
	// During upgrades, multiple versions can be active simultaneously.
	ActiveVersion *semver.Version `json:"active_version,omitempty"`
}
