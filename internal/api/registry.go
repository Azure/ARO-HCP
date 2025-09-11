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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	validator "github.com/go-playground/validator/v10"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

const (
	ProviderNamespace               = "Microsoft.RedHatOpenShift"
	ProviderNamespaceDisplay        = "Azure Red Hat OpenShift"
	ClusterResourceTypeName         = "hcpOpenShiftClusters"
	VersionResourceTypeName         = "hcpOpenShiftVersions"
	NodePoolResourceTypeName        = "nodePools"
	ExternalAuthResourceTypeName    = "externalAuths"
	OperationResultResourceTypeName = "hcpOperationResults"
	OperationStatusResourceTypeName = "hcpOperationStatuses"
	ResourceTypeDisplay             = "Hosted Control Plane (HCP) OpenShift Clusters"
)

var (
	ClusterResourceType      = azcorearm.NewResourceType(ProviderNamespace, ClusterResourceTypeName)
	NodePoolResourceType     = azcorearm.NewResourceType(ProviderNamespace, ClusterResourceTypeName+"/"+NodePoolResourceTypeName)
	ExternalAuthResourceType = azcorearm.NewResourceType(ProviderNamespace, ClusterResourceTypeName+"/"+ExternalAuthResourceTypeName)
	PreflightResourceType    = azcorearm.NewResourceType(ProviderNamespace, "deployments/preflight")
	VersionResourceType      = azcorearm.NewResourceType(ProviderNamespace, "locations/"+VersionResourceTypeName)
)

type Resource interface {
	NewVersioned(versionedInterface Version) VersionedResource
}

type VersionedResource interface {
	GetVersion() Version
}

type VersionedCreatableResource[T any] interface {
	VersionedResource
	Normalize(*T)
	GetVisibility(path string) (VisibilityFlags, bool)
	ValidateVisibility(current VersionedCreatableResource[T], updating bool) []arm.CloudErrorBody
}

type VersionedHCPOpenShiftCluster interface {
	VersionedCreatableResource[HCPOpenShiftCluster]
	ValidateStatic(current VersionedHCPOpenShiftCluster, updating bool) *arm.CloudError
}

type VersionedHCPOpenShiftClusterNodePool interface {
	VersionedCreatableResource[HCPOpenShiftClusterNodePool]
	ValidateStatic(current VersionedHCPOpenShiftClusterNodePool, cluster *HCPOpenShiftCluster, updating bool) *arm.CloudError
}

type VersionedHCPOpenShiftClusterExternalAuth interface {
	VersionedCreatableResource[HCPOpenShiftClusterExternalAuth]
	ValidateStatic(current VersionedHCPOpenShiftClusterExternalAuth, updating bool) *arm.CloudError
}

type VersionedHCPOpenShiftVersion VersionedResource

type Version interface {
	fmt.Stringer

	GetValidator() *validator.Validate

	// Resource Types
	// Passing a nil pointer creates a resource with default values.
	NewHCPOpenShiftCluster(*HCPOpenShiftCluster) VersionedHCPOpenShiftCluster
	NewHCPOpenShiftClusterNodePool(*HCPOpenShiftClusterNodePool) VersionedHCPOpenShiftClusterNodePool
	NewHCPOpenShiftClusterExternalAuth(*HCPOpenShiftClusterExternalAuth) VersionedHCPOpenShiftClusterExternalAuth
	NewHCPOpenShiftVersion(*HCPOpenShiftVersion) VersionedHCPOpenShiftVersion

	// Response Marshaling
	MarshalHCPOpenShiftClusterAdminCredential(*HCPOpenShiftClusterAdminCredential) ([]byte, error)
}

// apiRegistry is the map of registered API versions
var apiRegistry = map[string]Version{}

func Register(version Version) {
	apiRegistry[version.String()] = version
}

func Lookup(key string) (version Version, ok bool) {
	version, ok = apiRegistry[key]
	return
}
