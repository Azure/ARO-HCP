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

package kubeapplier

import (
	"path"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api"
)

// ToClusterScopedApplyDesireResourceIDString returns the resource ID string for an ApplyDesire
// nested directly under a cluster.
func ToClusterScopedApplyDesireResourceIDString(subscriptionName, resourceGroupName, clusterName, applyDesireName string,
) string {
	return strings.ToLower(path.Join(
		"/subscriptions", subscriptionName,
		"resourceGroups", resourceGroupName,
		"providers", api.ClusterResourceType.String(), clusterName,
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
		"providers", api.ClusterResourceType.String(), clusterName,
		api.NodePoolResourceTypeName, nodePoolName,
		ApplyDesireResourceTypeName, applyDesireName,
	))
}

// ToClusterScopedDeleteDesireResourceIDString returns the resource ID string for a DeleteDesire
// nested directly under a cluster.
func ToClusterScopedDeleteDesireResourceIDString(subscriptionName, resourceGroupName, clusterName, deleteDesireName string,
) string {
	return strings.ToLower(path.Join(
		"/subscriptions", subscriptionName,
		"resourceGroups", resourceGroupName,
		"providers", api.ClusterResourceType.String(), clusterName,
		DeleteDesireResourceTypeName, deleteDesireName,
	))
}

// ToNodePoolScopedDeleteDesireResourceIDString returns the resource ID string for a DeleteDesire
// nested under a node pool under a cluster.
func ToNodePoolScopedDeleteDesireResourceIDString(subscriptionName, resourceGroupName, clusterName, nodePoolName, deleteDesireName string,
) string {
	return strings.ToLower(path.Join(
		"/subscriptions", subscriptionName,
		"resourceGroups", resourceGroupName,
		"providers", api.ClusterResourceType.String(), clusterName,
		api.NodePoolResourceTypeName, nodePoolName,
		DeleteDesireResourceTypeName, deleteDesireName,
	))
}

// ToClusterScopedReadDesireResourceIDString returns the resource ID string for a ReadDesire
// nested directly under a cluster.
func ToClusterScopedReadDesireResourceIDString(subscriptionName, resourceGroupName, clusterName, readDesireName string,
) string {
	return strings.ToLower(path.Join(
		"/subscriptions", subscriptionName,
		"resourceGroups", resourceGroupName,
		"providers", api.ClusterResourceType.String(), clusterName,
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
		"providers", api.ClusterResourceType.String(), clusterName,
		api.NodePoolResourceTypeName, nodePoolName,
		ReadDesireResourceTypeName, readDesireName,
	))
}
