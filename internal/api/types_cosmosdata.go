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

func ToResourceGroupResourceIDString(subscriptionName, resourcGroupName string) string {
	return strings.ToLower(path.Join("/subscriptions", subscriptionName, "resourceGroups", resourcGroupName))
}

func ToClusterResourceID(subscriptionName, resourceGroupName, clusterName string) (*azcorearm.ResourceID, error) {
	return azcorearm.ParseResourceID(ToClusterResourceIDString(subscriptionName, resourceGroupName, clusterName))
}

func ToClusterResourceIDString(subscriptionName, resourceGroupName, clusterName string) string {
	return strings.ToLower(path.Join(
		"/subscriptions", subscriptionName,
		"resourceGroups", resourceGroupName,
		"providers", ClusterResourceType.String(), clusterName,
	))
}

func ToNodePoolResourceID(subscriptionName, resourceGroupName, clusterName, nodePoolName string) (*azcorearm.ResourceID, error) {
	return azcorearm.ParseResourceID(ToNodePoolResourceIDString(subscriptionName, resourceGroupName, clusterName, nodePoolName))
}

func ToNodePoolResourceIDString(subscriptionName, resourceGroupName, clusterName, nodePoolName string) string {
	return strings.ToLower(path.Join(
		"/subscriptions", subscriptionName,
		"resourceGroups", resourceGroupName,
		"providers", ClusterResourceType.String(), clusterName,
		NodePoolResourceType.Types[len(NodePoolResourceType.Types)-1], nodePoolName,
	))
}

func ToExternalAuthResourceIDString(subscriptionName, resourceGroupName, clusterName, externalAuthName string) string {
	return strings.ToLower(path.Join(
		"/subscriptions", subscriptionName,
		"resourceGroups", resourceGroupName,
		"providers", ClusterResourceType.String(), clusterName,
		ExternalAuthResourceType.Types[len(ExternalAuthResourceType.Types)-1], externalAuthName,
	))
}

func ToServiceProviderClusterResourceIDString(subscriptionName, resourceGroupName, clusterName string) string {
	return strings.ToLower(path.Join(
		"/subscriptions", subscriptionName,
		"resourceGroups", resourceGroupName,
		"providers", ClusterResourceType.String(), clusterName,
		ServiceProviderClusterResourceType.Types[len(ServiceProviderClusterResourceType.Types)-1], ServiceProviderClusterResourceName,
	))
}

func ToDNSReservationResourceIDString(subscriptionName, dnsReservationName string) string {
	return strings.ToLower(path.Join(
		"/subscriptions", subscriptionName,
		"providers", DNSReservationResourceType.String(), dnsReservationName,
	))
}

func ToOperationResourceIDString(subscriptionName, operationName string) string {
	return strings.ToLower(path.Join(
		"/subscriptions", subscriptionName,
		"providers", OperationStatusResourceType.String(), operationName,
	))
}

func ToDNSReservationResourceID(subscriptionName, dnsReservationName string) (*azcorearm.ResourceID, error) {
	return azcorearm.ParseResourceID(ToDNSReservationResourceIDString(subscriptionName, dnsReservationName))
}
