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

package kubeapplierapi

import (
	"path/filepath"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
)

const (
	ApplyDesireResourceTypeName = "applyDesires"
	ReadDesireResourceTypeName  = "readDesires"
)

// nestedResourceType is a small wrapper over azcorearm.NewResourceType that joins the
// nested path segments under our provider namespace, keeping the var declarations below
// short enough to read at a glance.
func nestedResourceType(parts ...string) azcorearm.ResourceType {
	return azcorearm.NewResourceType(coreapi.ProviderNamespace, filepath.Join(parts...))
}

var (
	// ClusterScopedApplyDesireResourceType is applyDesires nested directly under a Cluster.
	ClusterScopedApplyDesireResourceType = nestedResourceType(coreapi.ClusterResourceTypeName, ApplyDesireResourceTypeName)
	// NodePoolScopedApplyDesireResourceType is applyDesires nested under a NodePool under a Cluster.
	NodePoolScopedApplyDesireResourceType = nestedResourceType(coreapi.ClusterResourceTypeName, coreapi.NodePoolResourceTypeName, ApplyDesireResourceTypeName)

	// ClusterScopedReadDesireResourceType is readDesires nested directly under a Cluster.
	ClusterScopedReadDesireResourceType = nestedResourceType(coreapi.ClusterResourceTypeName, ReadDesireResourceTypeName)
	// NodePoolScopedReadDesireResourceType is readDesires nested under a NodePool under a Cluster.
	NodePoolScopedReadDesireResourceType = nestedResourceType(coreapi.ClusterResourceTypeName, coreapi.NodePoolResourceTypeName, ReadDesireResourceTypeName)

	// SystemAdminCredentialRequestScopedApplyDesireResourceType is applyDesires nested under a SystemAdminCredentialRequest under a Cluster.
	SystemAdminCredentialRequestScopedApplyDesireResourceType = nestedResourceType(coreapi.ClusterResourceTypeName, coreapi.SystemAdminCredentialRequestResourceTypeName, ApplyDesireResourceTypeName)
	// SystemAdminCredentialRequestScopedReadDesireResourceType is readDesires nested under a SystemAdminCredentialRequest under a Cluster.
	SystemAdminCredentialRequestScopedReadDesireResourceType = nestedResourceType(coreapi.ClusterResourceTypeName, coreapi.SystemAdminCredentialRequestResourceTypeName, ReadDesireResourceTypeName)

	// SystemAdminCredentialRevocationScopedApplyDesireResourceType is applyDesires nested under a SystemAdminCredentialRevocation under a Cluster.
	SystemAdminCredentialRevocationScopedApplyDesireResourceType = nestedResourceType(coreapi.ClusterResourceTypeName, coreapi.SystemAdminCredentialRevocationResourceTypeName, ApplyDesireResourceTypeName)
	// SystemAdminCredentialRevocationScopedReadDesireResourceType is readDesires nested under a SystemAdminCredentialRevocation under a Cluster.
	SystemAdminCredentialRevocationScopedReadDesireResourceType = nestedResourceType(coreapi.ClusterResourceTypeName, coreapi.SystemAdminCredentialRevocationResourceTypeName, ReadDesireResourceTypeName)

	// ManagementClusterScopedApplyDesireResourceType is applyDesires nested under a ManagementCluster under a Stamp.
	ManagementClusterScopedApplyDesireResourceType = nestedResourceType(fleetapi.StampResourceTypeName, fleetapi.ManagementClusterResourceTypeName, ApplyDesireResourceTypeName)
	// ManagementClusterScopedReadDesireResourceType is readDesires nested under a ManagementCluster under a Stamp.
	ManagementClusterScopedReadDesireResourceType = nestedResourceType(fleetapi.StampResourceTypeName, fleetapi.ManagementClusterResourceTypeName, ReadDesireResourceTypeName)
)

// ApplyDesireResourceTypeForParent derives the nested ApplyDesire resource type for
// an arbitrary parent by appending the applyDesires leaf to the parent's own type.
func ApplyDesireResourceTypeForParent(parent *azcorearm.ResourceID) azcorearm.ResourceType {
	return nestedResourceType(parent.ResourceType.Type, ApplyDesireResourceTypeName)
}

// ReadDesireResourceTypeForParent is the ReadDesire parallel of ApplyDesireResourceTypeForParent.
func ReadDesireResourceTypeForParent(parent *azcorearm.ResourceID) azcorearm.ResourceType {
	return nestedResourceType(parent.ResourceType.Type, ReadDesireResourceTypeName)
}
