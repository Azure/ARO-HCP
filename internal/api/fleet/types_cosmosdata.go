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

package fleet

import (
	"path"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// ToManagementClusterResourceID constructs a Cosmos resource ID for a management cluster
// using the subscription, resource group, and name from the AKS resource ID.
func ToManagementClusterResourceID(subscriptionID, resourceGroupName, clusterName string) (*azcorearm.ResourceID, error) {
	return azcorearm.ParseResourceID(ToManagementClusterResourceIDString(subscriptionID, resourceGroupName, clusterName))
}

// ToManagementClusterResourceIDString returns the lowercased resource ID string
// for a management cluster derived from the AKS cluster's subscription, resource group, and name.
func ToManagementClusterResourceIDString(subscriptionID, resourceGroupName, clusterName string) string {
	return strings.ToLower(path.Join(
		"/subscriptions", subscriptionID,
		"resourceGroups", resourceGroupName,
		"providers", ManagementClusterResourceType.String(), clusterName,
	))
}

// ToManagementClusterDeploymentResourceID constructs a resource ID for a management
// cluster deployment from a stamp identifier. The deployment is a provider-level resource
// with no subscription or resource group scope.
func ToManagementClusterDeploymentResourceID(stampIdentifier string) (*azcorearm.ResourceID, error) {
	return azcorearm.ParseResourceID(ToManagementClusterDeploymentResourceIDString(stampIdentifier))
}

// ToManagementClusterDeploymentResourceIDString returns the lowercased resource ID string
// for a management cluster deployment.
func ToManagementClusterDeploymentResourceIDString(stampIdentifier string) string {
	return strings.ToLower(path.Join(
		"/providers", ManagementClusterDeploymentResourceType.String(), stampIdentifier,
	))
}
