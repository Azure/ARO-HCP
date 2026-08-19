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
	"path"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
)

// ToClusterScopedApplyDesireResourceIDString returns the resource ID string for an ApplyDesire
// nested directly under a cluster.
func ToClusterScopedApplyDesireResourceIDString(subscriptionName, resourceGroupName, clusterName, applyDesireName string,
) string {
	return strings.ToLower(path.Join(
		"/subscriptions", subscriptionName,
		"resourceGroups", resourceGroupName,
		"providers", coreapi.ClusterResourceType.String(), clusterName,
		ApplyDesireResourceTypeName, applyDesireName,
	))
}

// ToNodePoolScopedApplyDesireResourceIDString returns the resource ID string for an ApplyDesire
// nested under a node pool under a cluster.
func ToNodePoolScopedApplyDesireResourceIDString(subscriptionName, resourceGroupName, clusterName, nodePoolName, applyDesireName string,
) string {
	return strings.ToLower(path.Join(
		"/subscriptions", subscriptionName,
		"resourceGroups", resourceGroupName,
		"providers", coreapi.ClusterResourceType.String(), clusterName,
		coreapi.NodePoolResourceTypeName, nodePoolName,
		ApplyDesireResourceTypeName, applyDesireName,
	))
}

// ToClusterScopedReadDesireResourceIDString returns the resource ID string for a ReadDesire
// nested directly under a cluster.
func ToClusterScopedReadDesireResourceIDString(subscriptionName, resourceGroupName, clusterName, readDesireName string,
) string {
	return strings.ToLower(path.Join(
		"/subscriptions", subscriptionName,
		"resourceGroups", resourceGroupName,
		"providers", coreapi.ClusterResourceType.String(), clusterName,
		ReadDesireResourceTypeName, readDesireName,
	))
}

// ToNodePoolScopedReadDesireResourceIDString returns the resource ID string for a ReadDesire
// nested under a node pool under a cluster.
func ToNodePoolScopedReadDesireResourceIDString(subscriptionName, resourceGroupName, clusterName, nodePoolName, readDesireName string,
) string {
	return strings.ToLower(path.Join(
		"/subscriptions", subscriptionName,
		"resourceGroups", resourceGroupName,
		"providers", coreapi.ClusterResourceType.String(), clusterName,
		coreapi.NodePoolResourceTypeName, nodePoolName,
		ReadDesireResourceTypeName, readDesireName,
	))
}

// ToSystemAdminCredentialRequestScopedApplyDesireResourceIDString returns the resource ID string for an ApplyDesire
// nested under a SystemAdminCredentialRequest under a cluster.
func ToSystemAdminCredentialRequestScopedApplyDesireResourceIDString(subscriptionName, resourceGroupName, clusterName, credentialRequestName, applyDesireName string,
) string {
	return strings.ToLower(path.Join(
		"/subscriptions", subscriptionName,
		"resourceGroups", resourceGroupName,
		"providers", coreapi.ClusterResourceType.String(), clusterName,
		coreapi.SystemAdminCredentialRequestResourceTypeName, credentialRequestName,
		ApplyDesireResourceTypeName, applyDesireName,
	))
}

// ToSystemAdminCredentialRequestScopedReadDesireResourceIDString returns the resource ID string for a ReadDesire
// nested under a SystemAdminCredentialRequest under a cluster.
func ToSystemAdminCredentialRequestScopedReadDesireResourceIDString(subscriptionName, resourceGroupName, clusterName, credentialRequestName, readDesireName string,
) string {
	return strings.ToLower(path.Join(
		"/subscriptions", subscriptionName,
		"resourceGroups", resourceGroupName,
		"providers", coreapi.ClusterResourceType.String(), clusterName,
		coreapi.SystemAdminCredentialRequestResourceTypeName, credentialRequestName,
		ReadDesireResourceTypeName, readDesireName,
	))
}

// ToSystemAdminCredentialRevocationScopedApplyDesireResourceIDString returns the resource ID string for an ApplyDesire
// nested under a SystemAdminCredentialRevocation under a cluster.
func ToSystemAdminCredentialRevocationScopedApplyDesireResourceIDString(subscriptionName, resourceGroupName, clusterName, revocationName, applyDesireName string,
) string {
	return strings.ToLower(path.Join(
		"/subscriptions", subscriptionName,
		"resourceGroups", resourceGroupName,
		"providers", coreapi.ClusterResourceType.String(), clusterName,
		coreapi.SystemAdminCredentialRevocationResourceTypeName, revocationName,
		ApplyDesireResourceTypeName, applyDesireName,
	))
}

// ToSystemAdminCredentialRevocationScopedReadDesireResourceIDString returns the resource ID string for a ReadDesire
// nested under a SystemAdminCredentialRevocation under a cluster.
func ToSystemAdminCredentialRevocationScopedReadDesireResourceIDString(subscriptionName, resourceGroupName, clusterName, revocationName, readDesireName string,
) string {
	return strings.ToLower(path.Join(
		"/subscriptions", subscriptionName,
		"resourceGroups", resourceGroupName,
		"providers", coreapi.ClusterResourceType.String(), clusterName,
		coreapi.SystemAdminCredentialRevocationResourceTypeName, revocationName,
		ReadDesireResourceTypeName, readDesireName,
	))
}

// ToManagementClusterScopedApplyDesireResourceIDString returns the resource ID string for an ApplyDesire
// nested under a ManagementCluster under a Stamp.
func ToManagementClusterScopedApplyDesireResourceIDString(stampIdentifier, applyDesireName string) string {
	return strings.ToLower(path.Join(
		fleetapi.ToManagementClusterResourceIDString(stampIdentifier),
		ApplyDesireResourceTypeName, applyDesireName,
	))
}

// ToManagementClusterScopedReadDesireResourceIDString returns the resource ID string for a ReadDesire
// nested under a ManagementCluster under a Stamp.
func ToManagementClusterScopedReadDesireResourceIDString(stampIdentifier, readDesireName string) string {
	return strings.ToLower(path.Join(
		fleetapi.ToManagementClusterResourceIDString(stampIdentifier),
		ReadDesireResourceTypeName, readDesireName,
	))
}
