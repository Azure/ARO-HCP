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
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ServiceProviderCluster struct {
	// CosmosMetadata ResourceID is nested under the cluster so that association and cleanup work as expected
	// it will be the ServiceProviderCluster type and the name default
	CosmosMetadata `json:"cosmosMetadata"`

	// resourceID exists to match cosmosMetadata.resourceID until we're able to transition all types to use cosmosMetadata,
	// at which point we will stop using properties.resourceId in our queries. That will be about a month from now.
	ResourceID azcorearm.ResourceID `json:"resourceId"`

	LoadBalancerResourceID *azcorearm.ResourceID `json:"loadBalancerResourceID,omitempty"`

	// Validations is a list of conditions that tracks the status of each cluster validation.
	// Each Condition Type represents a validation and it should be unique among all validations.
	// A Condition Status of True means that the validation passed successfully, and a Condition Status of False means that the validation failed.
	// The Condition Reason and Message are used to provide more details about the validation status.
	// The Condition LastTransitionTime is used to track the last time the validation transitioned from one status to another.
	Validations []Condition `json:"validations,omitempty"`

	// TODO do we want to differentiate between the readonly bundles and the "writable" bundles?
	// There cannot be two readonly bundles with the same Name attribute.
	MaestroReadonlyBundles MaestroBundleReferenceList `json:"maestroReadonlyBundles,omitempty"`
}

type MaestroBundleReference struct {
	// Name is a logical name that represents the Maestro Bundle conceptually.
	Name MaestroBundleInternalName `json:"name"`
	// MaestroAPIMaestroBundleName is the name of the Maestro Bundle in the Maestro API.
	// It must be unique within a given Maestro Consumer Name and Maestro Source ID.
	MaestroAPIMaestroBundleName string `json:"maestroAPIMaestroBundleName"`
	// MaestroAPIMaestroBundleID is the ID of the Maestro Bundle in the Maestro API.
	// Returned by the Maestro API when the Maestro Bundle is first created.
	// TODO unsure if we need this. If we were to interact with the HTTP REST API client
	// that works with Maestro Bundle IDs. The GRPC one abstracts the Maestro Bundle
	// via ManifestWorks and usage of Maestro Bundle Names
	MaestroAPIMaestroBundleID string `json:"maestroAPIMaestroBundleID"`
}

type MaestroBundleReferenceList []MaestroBundleReference

func (l MaestroBundleReferenceList) Get(name MaestroBundleInternalName) *MaestroBundleReference {
	for _, bundle := range l {
		if bundle.Name == name {
			return &bundle
		}
	}
	return nil
}

// TODO how should we name this type to avoid confusion between the Maestro Bundle Name in the Maestro API and the
// name that we use internally which is used as the key for the managementClusterContent Cosmos resource?
type MaestroBundleInternalName string

const (
	MaestroBundleInternalNameHypershiftHostedClusterManifestWork MaestroBundleInternalName = "hypershiftHostedClusterManifestWork"
	MaestroBundleInternalNameHypershiftHostedCluster             MaestroBundleInternalName = "hypershiftHostedCluster"
)
