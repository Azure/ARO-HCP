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
	"path"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

type CosmosMetadata = arm.CosmosMetadata

// The ToXxxResourceIDString helpers form ARM resource ID strings by
// delegating to the parent helper and appending the final segment. The
// segment uses the leaf type name (Types[len-1]) for nested types so that
// the provider namespace is added exactly once at the top of the chain.
// Authoring each level via its parent makes it impossible to accidentally
// produce a non-canonical key — which the cache uses to look up objects.

func ToResourceGroupResourceIDString(subscriptionName, resourceGroupName string) string {
	return strings.ToLower(path.Join("/subscriptions", subscriptionName, "resourceGroups", resourceGroupName))
}

func ToResourceGroupResourceID(subscriptionID, resourceGroupName string) (*azcorearm.ResourceID, error) {
	return azcorearm.ParseResourceID(ToResourceGroupResourceIDString(subscriptionID, resourceGroupName))
}

func ToClusterResourceID(subscriptionName, resourceGroupName, clusterName string) (*azcorearm.ResourceID, error) {
	return azcorearm.ParseResourceID(ToClusterResourceIDString(subscriptionName, resourceGroupName, clusterName))
}

func ToClusterResourceIDString(subscriptionName, resourceGroupName, clusterName string) string {
	return strings.ToLower(path.Join(
		ToResourceGroupResourceIDString(subscriptionName, resourceGroupName),
		"providers", ClusterResourceType.String(), clusterName,
	))
}

func ToNodePoolResourceID(subscriptionName, resourceGroupName, clusterName, nodePoolName string) (*azcorearm.ResourceID, error) {
	return azcorearm.ParseResourceID(ToNodePoolResourceIDString(subscriptionName, resourceGroupName, clusterName, nodePoolName))
}

func ToNodePoolResourceIDString(subscriptionName, resourceGroupName, clusterName, nodePoolName string) string {
	return strings.ToLower(path.Join(
		ToClusterResourceIDString(subscriptionName, resourceGroupName, clusterName),
		leafTypeName(NodePoolResourceType), nodePoolName,
	))
}

func ToExternalAuthResourceID(subscriptionName, resourceGroupName, clusterName, externalAuthName string) (*azcorearm.ResourceID, error) {
	return azcorearm.ParseResourceID(ToExternalAuthResourceIDString(subscriptionName, resourceGroupName, clusterName, externalAuthName))
}

func ToExternalAuthResourceIDString(subscriptionName, resourceGroupName, clusterName, externalAuthName string) string {
	return strings.ToLower(path.Join(
		ToClusterResourceIDString(subscriptionName, resourceGroupName, clusterName),
		leafTypeName(ExternalAuthResourceType), externalAuthName,
	))
}

func ToSystemAdminCredentialRequestResourceID(subscriptionName, resourceGroupName, clusterName, credentialName string) (*azcorearm.ResourceID, error) {
	return azcorearm.ParseResourceID(ToSystemAdminCredentialRequestResourceIDString(subscriptionName, resourceGroupName, clusterName, credentialName))
}

func ToSystemAdminCredentialRequestResourceIDString(subscriptionName, resourceGroupName, clusterName, credentialName string) string {
	return strings.ToLower(path.Join(
		ToClusterResourceIDString(subscriptionName, resourceGroupName, clusterName),
		SystemAdminCredentialRequestResourceTypeName, credentialName,
	))
}

func ToServiceProviderClusterResourceIDString(subscriptionName, resourceGroupName, clusterName string) string {
	return strings.ToLower(path.Join(
		ToClusterResourceIDString(subscriptionName, resourceGroupName, clusterName),
		leafTypeName(ServiceProviderClusterResourceType), ServiceProviderClusterResourceName,
	))
}

func ToOperationResourceIDString(subscriptionName, operationName string) string {
	return strings.ToLower(path.Join(
		"/subscriptions", subscriptionName,
		"providers", OperationStatusResourceType.String(), operationName,
	))
}

func ToManagementClusterContentResourceIDString(subscriptionName, resourceGroupName, clusterName, managementClusterContentName string) string {
	return strings.ToLower(path.Join(
		ToClusterResourceIDString(subscriptionName, resourceGroupName, clusterName),
		ManagementClusterContentResourceTypeName, managementClusterContentName,
	))
}

func ToServiceProviderNodePoolResourceIDString(subscriptionName, resourceGroupName, clusterName, nodePoolName string) string {
	return strings.ToLower(path.Join(
		ToNodePoolResourceIDString(subscriptionName, resourceGroupName, clusterName, nodePoolName),
		leafTypeName(ServiceProviderNodePoolResourceType), ServiceProviderNodePoolResourceName,
	))
}

// leafTypeName returns the trailing segment of an ARM ResourceType (the
// part after the last slash). Using it in the per-level helpers prevents
// callers from accidentally embedding the full `namespace/type/...` form
// twice in the same ID — see the original ToServiceProviderNodePoolResourceIDString
// bug.
func leafTypeName(rt azcorearm.ResourceType) string {
	return rt.Types[len(rt.Types)-1]
}
