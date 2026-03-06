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

package listertesting

import (
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// nodePoolMatchesCluster checks if a node pool's resource ID belongs to the given cluster.
// Node pools are child resources of clusters, so we check the parent resource name.
func nodePoolMatchesCluster(resourceID *azcorearm.ResourceID, clusterName string) bool {
	if resourceID == nil || resourceID.Parent == nil {
		return false
	}
	return strings.EqualFold(resourceID.Parent.Name, clusterName)
}

// externalAuthMatchesCluster checks if an external auth's resource ID belongs to the given cluster.
// External auths are child resources of clusters, so we check the parent resource name.
func externalAuthMatchesCluster(resourceID *azcorearm.ResourceID, clusterName string) bool {
	if resourceID == nil || resourceID.Parent == nil {
		return false
	}
	return strings.EqualFold(resourceID.Parent.Name, clusterName)
}

// serviceProviderClusterMatchesCluster checks if a service provider cluster's resource ID belongs to the given cluster.
// Service provider clusters are child resources of clusters, so we check the parent resource name.
func serviceProviderClusterMatchesCluster(resourceID *azcorearm.ResourceID, clusterName string) bool {
	if resourceID == nil || resourceID.Parent == nil {
		return false
	}
	return strings.EqualFold(resourceID.Parent.Name, clusterName)
}
