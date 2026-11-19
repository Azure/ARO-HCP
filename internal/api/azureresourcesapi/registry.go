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

package azureresourcesapi

import (
	"path/filepath"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

const (
	DeploymentStackDesireResourceTypeName = "azureDeploymentStackDesires"
)

var (
	// ClusterScopedDeploymentStackDesireResourceType is azureDeploymentStackDesires nested directly under a Cluster.
	ClusterScopedDeploymentStackDesireResourceType = azcorearm.NewResourceType(coreapi.ProviderNamespace, filepath.Join(coreapi.ClusterResourceTypeName, DeploymentStackDesireResourceTypeName))
)

// DeploymentStackDesireResourceTypeForParent derives the nested DeploymentStackDesire
// resource type for an arbitrary parent by appending the leaf to the parent's own type.
// Only Cluster parents are valid today (see DeploymentStackDesireScope in
// azureresourcescosmosstorage); this helper exists so a future NodePool scope is a
// pure additive change. Mirrors kubeapplierapi.ApplyDesireResourceTypeForParent.
func DeploymentStackDesireResourceTypeForParent(parent *azcorearm.ResourceID) azcorearm.ResourceType {
	return azcorearm.NewResourceType(coreapi.ProviderNamespace, filepath.Join(parent.ResourceType.Type, DeploymentStackDesireResourceTypeName))
}
