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
	"fmt"

	"github.com/blang/semver/v4"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/utils"
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

	// resourceID exists to match cosmosMetadata.resourceID until we're able to transition all types to use cosmosMetadata,
	// at which point we will stop using properties.resourceId in our queries. That will be about a month from now.
	ResourceID azcorearm.ResourceID `json:"resourceId"`

	LoadBalancerResourceID *azcorearm.ResourceID `json:"loadBalancerResourceID,omitempty"`

	Spec ServiceProviderClusterSpec `json:"spec"`

	// Status contains the observed state of the cluster.
	Status ServiceProviderClusterStatus `json:"status,omitempty"`
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

	// DesiredHostedCluster is the HostedCluster that we want to exist on the management cluster.
	// We will only explicitly set the fields we care about, but serialization may store additional empty fields.
	// Once this contains the critical values, we will create it on management clusters.
	// We may or may not choose to store the actual state in status.  We may choose to store the actual state independently.
	DesiredHostedCluster *v1beta1.HostedCluster `json:"desiredHostedCluster,omitempty"`
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

	// Validations is a list of conditions that tracks the status of each cluster validation.
	// Each Condition Type represents a validation and it should be unique among all validations.
	// A Condition Status of True means that the validation passed successfully, and a Condition Status of False means that the validation failed.
	// The Condition Reason and Message are used to provide more details about the validation status.
	// The Condition LastTransitionTime is used to track the last time the validation transitioned from one status to another.
	Validations []Condition `json:"validations,omitempty"`
	// MaestroReadonlyBundles contains a list of Maestro readonly bundles references.
	// These bundles are used to retrieve particular K8s resources from the Management Cluster.
	// The reference contains a mapping between the logical name we give to the Maestro bundle internally
	// and the Maestro Bundle Name and ID at the Maestro API level.
	MaestroReadonlyBundles MaestroBundleReferenceList `json:"maestroReadonlyBundles,omitempty"`
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

type MaestroBundleReference struct {
	// Name is a logical name that represents the Maestro Bundle conceptually.
	Name MaestroBundleInternalName `json:"name"`
	// MaestroAPIMaestroBundleName is the name of the Maestro Bundle in the Maestro API.
	// It must be unique within a given Maestro Consumer Name and Maestro Source ID.
	// Maestro's ManifestWorks Go client abstraction uses Maestro Bundle Names to
	// identify the Maestro Bundle.
	MaestroAPIMaestroBundleName string `json:"maestroAPIMaestroBundleName"`
	// MaestroAPIMaestroBundleID is the ID of the Maestro Bundle in the Maestro API.
	// Returned by the Maestro API when the Maestro Bundle is first created.
	// This attribute can be unset if the Maestro Bundle reference has been created
	// but the Maestro Bundle has not been created yet.
	// Maestro's REST API Go client abstraction uses Maestro Bundle IDs to identify the Maestro Bundle.
	MaestroAPIMaestroBundleID string `json:"maestroAPIMaestroBundleID"`
}

// MaestroBundleReferenceList is a list of Maestro Bundle references.
type MaestroBundleReferenceList []*MaestroBundleReference

// Get returns a copy to the Maestro Bundle reference for a given Maestro Bundle internal name. It returns a pointer
// for a clear indication of "not found", it doesn't return a reference intended for mutation of the original list.
// If the Maestro Bundle reference identifies by name does not exist, it returns nil.
// If multiple Maestro Bundle references are found for the same internal name, it returns an error.
func (l MaestroBundleReferenceList) Get(name MaestroBundleInternalName) (*MaestroBundleReference, error) {
	var bundleReference *MaestroBundleReference

	for _, bundle := range l {
		if bundle.Name == name {
			if bundleReference != nil {
				return nil, utils.TrackError(fmt.Errorf("multiple Maestro Bundle references found for the same internal name: %s", name))
			}
			bundleReference = bundle.DeepCopy()
		}
	}
	return bundleReference, nil
}

// Set sets the Maestro Bundle reference for a given Maestro Bundle internal name.
// If the Maestro Bundle reference identifies by name does not exist, it is added.
// If the Maestro Bundle reference identifies by name already exists, it is updated.
func (l *MaestroBundleReferenceList) Set(maestroBundleReference *MaestroBundleReference) error {
	existingMaestroBundleReference, err := l.Get(maestroBundleReference.Name)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Maestro Bundle reference: %w", err))
	}
	if existingMaestroBundleReference == nil {
		*l = append(*l, maestroBundleReference)
		return nil
	}

	newMaestroBundleReference := maestroBundleReference.DeepCopy()

	for i := range *l {
		if (*l)[i].Name == maestroBundleReference.Name {
			(*l)[i] = newMaestroBundleReference
			return nil
		}
	}

	return nil
}

// MaestroBundleInternalName is a type that represents the internal name of a Maestro Bundle.
// It is used to identify the Maestro Bundle internally and to retrieve it from the MaestroBundleReferenceList.
type MaestroBundleInternalName string

const (
	// MaestroBundleInternalNameReadonlyHypershiftHostedCluster is the internal name of the Maestro Bundle that represents
	// the Cluster's Hypershift's HostedCluster K8s resource.
	MaestroBundleInternalNameReadonlyHypershiftHostedCluster MaestroBundleInternalName = "readonlyHypershiftHostedCluster"
)
