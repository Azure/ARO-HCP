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
	"net/http"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

const (
	ProviderNamespace               = "Microsoft.RedHatOpenShift"
	ProviderNamespaceDisplay        = "Azure Red Hat OpenShift"
	ClusterResourceTypeName         = "hcpOpenShiftClusters"
	NodePoolResourceTypeName        = "nodePools"
	OperationResultResourceTypeName = "hcpOperationResults"
	OperationStatusResourceTypeName = "hcpOperationStatuses"
	ResourceTypeDisplay             = "Hosted Control Plane (HCP) OpenShift Clusters"
)

var (
	ClusterResourceType   = azcorearm.NewResourceType(ProviderNamespace, ClusterResourceTypeName)
	NodePoolResourceType  = azcorearm.NewResourceType(ProviderNamespace, ClusterResourceTypeName+"/"+NodePoolResourceTypeName)
	PreflightResourceType = azcorearm.NewResourceType(ProviderNamespace, "deployments/preflight")
)

type VersionedHCPOpenShiftCluster interface {
	Normalize(*HCPOpenShiftCluster)
	ValidateStatic(current VersionedHCPOpenShiftCluster, updating bool, request *http.Request) *arm.CloudError
}

type VersionedHCPOpenShiftClusterNodePool interface {
	Normalize(*HCPOpenShiftClusterNodePool)
	ValidateStatic(current VersionedHCPOpenShiftClusterNodePool, cluster *HCPOpenShiftCluster, updating bool, request *http.Request) *arm.CloudError
}

type Version interface {
	fmt.Stringer

	// Resource Types
	// Passing a nil pointer creates a resource with default values.
	NewHCPOpenShiftCluster(*HCPOpenShiftCluster) VersionedHCPOpenShiftCluster
	NewHCPOpenShiftClusterNodePool(*HCPOpenShiftClusterNodePool) VersionedHCPOpenShiftClusterNodePool

	// Response Marshaling
	MarshalHCPOpenShiftCluster(*HCPOpenShiftCluster) ([]byte, error)
	MarshalHCPOpenShiftClusterNodePool(*HCPOpenShiftClusterNodePool) ([]byte, error)
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
