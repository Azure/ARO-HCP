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

// Package listertesting provides slice-backed and database-backed test
// implementations of the kube-applier listers.
package listertesting

import (
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// underCluster reports whether desireID is nested under the given HCPOpenShiftCluster.
// This covers both cluster-scoped desires (.../clusters/<c>/<desireType>/<n>) and
// node-pool-scoped desires (.../clusters/<c>/nodePools/<np>/<desireType>/<n>).
func underCluster(desireID *azcorearm.ResourceID, subscriptionID, resourceGroupName, clusterName string) bool {
	if desireID == nil {
		return false
	}
	for cur := desireID; cur != nil; cur = cur.Parent {
		if !strings.EqualFold(cur.ResourceType.Namespace, api.ProviderNamespace) ||
			!strings.EqualFold(cur.ResourceType.Type, api.ClusterResourceType.Type) {
			continue
		}
		return strings.EqualFold(cur.SubscriptionID, subscriptionID) &&
			strings.EqualFold(cur.ResourceGroupName, resourceGroupName) &&
			strings.EqualFold(cur.Name, clusterName)
	}
	return false
}

// underNodePool reports whether desireID is nested directly under the given NodePool.
// Cluster-scoped desires (no NodePool ancestor) return false.
func underNodePool(
	desireID *azcorearm.ResourceID,
	subscriptionID, resourceGroupName, clusterName, nodePoolName string,
) bool {
	if desireID == nil {
		return false
	}
	for cur := desireID; cur != nil; cur = cur.Parent {
		if !strings.EqualFold(cur.ResourceType.Namespace, api.ProviderNamespace) ||
			!strings.EqualFold(cur.ResourceType.Type, api.NodePoolResourceType.Type) {
			continue
		}
		// cur is the NodePool ancestor; cur.Parent should be the cluster.
		if cur.Parent == nil {
			return false
		}
		return strings.EqualFold(cur.SubscriptionID, subscriptionID) &&
			strings.EqualFold(cur.ResourceGroupName, resourceGroupName) &&
			strings.EqualFold(cur.Parent.Name, clusterName) &&
			strings.EqualFold(cur.Name, nodePoolName)
	}
	return false
}

// resourceIDOf returns the ResourceID of a *Desire-shaped object via its embedded
// arm.CosmosMetadata. Used by the slice-backed listers.
func resourceIDOf(desire any) *azcorearm.ResourceID {
	if a, ok := desire.(arm.CosmosMetadataAccessor); ok {
		return a.GetResourceID()
	}
	return nil
}
