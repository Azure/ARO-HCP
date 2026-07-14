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

package integrationutils

import (
	"context"
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// DeriveClusterServiceID returns the Cluster Service internal ID to persist for the
// given ARM resource, matching backend controller conventions.
func DeriveClusterServiceID(
	ctx context.Context,
	resourcesDBClient database.ResourcesDBClient,
	resourceIDString string,
) (string, error) {
	resourceID, err := azcorearm.ParseResourceID(resourceIDString)
	if err != nil {
		return "", utils.TrackError(err)
	}

	switch {
	case strings.EqualFold(resourceID.ResourceType.String(), api.ClusterResourceType.String()):
		return ocm.GenerateAROHCPClusterHREF(resourceID.Name), nil

	case strings.EqualFold(resourceID.ResourceType.String(), api.NodePoolResourceType.String()):
		if resourceID.Parent == nil {
			return "", utils.TrackError(fmt.Errorf("node pool %s has no parent cluster", resourceIDString))
		}
		clusterCSID, err := clusterClusterServiceID(ctx, resourcesDBClient, resourceID)
		if err != nil {
			return "", err
		}
		return ocm.GenerateAROHCPNodePoolHREF(clusterCSID.ID(), strings.ToLower(resourceID.Name)), nil

	case strings.EqualFold(resourceID.ResourceType.String(), api.ExternalAuthResourceType.String()):
		if resourceID.Parent == nil {
			return "", utils.TrackError(fmt.Errorf("external auth %s has no parent cluster", resourceIDString))
		}
		clusterCSID, err := clusterClusterServiceID(ctx, resourcesDBClient, resourceID)
		if err != nil {
			return "", err
		}
		return ocm.GenerateAROHCPExternalAuthHREF(clusterCSID.ID(), resourceID.Name), nil

	default:
		return "", utils.TrackError(fmt.Errorf("resource %s: setClusterServiceID supports clusters, node pools, and external auths only", resourceIDString))
	}
}

// EnsureParentClusterServiceID stamps a derived Cluster Service internal ID onto
// a child resource's parent cluster when it doesn't already have one. This is used
// when a child resource create requires the parent cluster to have a ClusterServiceID.
func EnsureParentClusterServiceID(
	ctx context.Context,
	resourcesDBClient database.ResourcesDBClient,
	childResourceIDString string,
) error {
	childResourceID, err := azcorearm.ParseResourceID(childResourceIDString)
	if err != nil {
		return utils.TrackError(err)
	}

	switch {
	case strings.EqualFold(childResourceID.ResourceType.String(), api.NodePoolResourceType.String()),
		strings.EqualFold(childResourceID.ResourceType.String(), api.ExternalAuthResourceType.String()):
		if childResourceID.Parent == nil {
			return utils.TrackError(fmt.Errorf("resource %s has no parent cluster", childResourceIDString))
		}
		cluster, err := resourcesDBClient.HCPClusters(childResourceID.SubscriptionID, childResourceID.ResourceGroupName).
			Get(ctx, childResourceID.Parent.Name)
		if database.IsNotFoundError(err) {
			return nil
		}
		if err != nil {
			return utils.TrackError(err)
		}
		if cluster.ServiceProviderProperties.ClusterServiceID != nil &&
			len(cluster.ServiceProviderProperties.ClusterServiceID.String()) > 0 {
			return nil
		}
		clusterServiceID, err := DeriveClusterServiceID(ctx, resourcesDBClient, childResourceID.Parent.String())
		if err != nil {
			return err
		}
		return SetClusterServiceID(ctx, resourcesDBClient, childResourceID.Parent.String(), clusterServiceID)
	default:
		return nil
	}
}

func clusterClusterServiceID(ctx context.Context, resourcesDBClient database.ResourcesDBClient, childResourceID *azcorearm.ResourceID) (api.InternalID, error) {
	cluster, err := resourcesDBClient.HCPClusters(childResourceID.SubscriptionID, childResourceID.ResourceGroupName).
		Get(ctx, childResourceID.Parent.Name)
	if err != nil {
		return api.InternalID{}, utils.TrackError(err)
	}
	if cluster.ServiceProviderProperties.ClusterServiceID == nil ||
		len(cluster.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return api.InternalID{}, utils.TrackError(fmt.Errorf(
			"cluster %s has no clusterServiceID; set cluster clusterServiceID before child resource %s",
			childResourceID.Parent.Name,
			childResourceID.Name,
		))
	}
	return *cluster.ServiceProviderProperties.ClusterServiceID, nil
}

// SetClusterServiceID updates the Cosmos document for a cluster, node pool, or external auth
// with the given Cluster Service internal ID.
func SetClusterServiceID(
	ctx context.Context,
	resourcesDBClient database.ResourcesDBClient,
	resourceIDString string,
	clusterServiceID string,
) error {
	resourceID, err := azcorearm.ParseResourceID(resourceIDString)
	if err != nil {
		return utils.TrackError(err)
	}

	csInternalID, err := api.NewInternalID(clusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("invalid clusterServiceID %q: %w", clusterServiceID, err))
	}

	switch {
	case strings.EqualFold(resourceID.ResourceType.String(), api.ClusterResourceType.String()):
		return setClusterClusterServiceID(ctx, resourcesDBClient, resourceID, csInternalID)

	case strings.EqualFold(resourceID.ResourceType.String(), api.NodePoolResourceType.String()):
		return setNodePoolClusterServiceID(ctx, resourcesDBClient, resourceID, csInternalID)

	case strings.EqualFold(resourceID.ResourceType.String(), api.ExternalAuthResourceType.String()):
		return setExternalAuthClusterServiceID(ctx, resourcesDBClient, resourceID, csInternalID)

	default:
		return utils.TrackError(fmt.Errorf("resource %s: setClusterServiceID supports clusters, node pools, and external auths only", resourceIDString))
	}
}

func setClusterClusterServiceID(
	ctx context.Context,
	resourcesDBClient database.ResourcesDBClient,
	resourceID *azcorearm.ResourceID,
	csInternalID api.InternalID,
) error {
	cluster, err := resourcesDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(ctx, resourceID.Name)
	if err != nil {
		return utils.TrackError(err)
	}
	if cluster.ServiceProviderProperties.ClusterServiceID != nil &&
		cluster.ServiceProviderProperties.ClusterServiceID.String() == csInternalID.String() {
		return nil
	}
	cluster.ServiceProviderProperties.ClusterServiceID = csInternalID.DeepCopy()
	_, err = resourcesDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Replace(ctx, cluster, nil)
	return utils.TrackError(err)
}

func setNodePoolClusterServiceID(
	ctx context.Context,
	resourcesDBClient database.ResourcesDBClient,
	resourceID *azcorearm.ResourceID,
	csInternalID api.InternalID,
) error {
	if resourceID.Parent == nil {
		return utils.TrackError(fmt.Errorf("node pool resource %s has no parent cluster", resourceID.String()))
	}
	nodePool, err := resourcesDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).
		NodePools(resourceID.Parent.Name).Get(ctx, resourceID.Name)
	if err != nil {
		return utils.TrackError(err)
	}
	if nodePool.ServiceProviderProperties.ClusterServiceID != nil &&
		nodePool.ServiceProviderProperties.ClusterServiceID.String() == csInternalID.String() {
		return nil
	}
	nodePool.ServiceProviderProperties.ClusterServiceID = csInternalID.DeepCopy()
	_, err = resourcesDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).
		NodePools(resourceID.Parent.Name).Replace(ctx, nodePool, nil)
	return utils.TrackError(err)
}

func setExternalAuthClusterServiceID(
	ctx context.Context,
	resourcesDBClient database.ResourcesDBClient,
	resourceID *azcorearm.ResourceID,
	csInternalID api.InternalID,
) error {
	if resourceID.Parent == nil {
		return utils.TrackError(fmt.Errorf("external auth resource %s has no parent cluster", resourceID.String()))
	}
	externalAuth, err := resourcesDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).
		ExternalAuth(resourceID.Parent.Name).Get(ctx, resourceID.Name)
	if err != nil {
		return utils.TrackError(err)
	}
	if externalAuth.ServiceProviderProperties.ClusterServiceID != nil &&
		externalAuth.ServiceProviderProperties.ClusterServiceID.String() == csInternalID.String() {
		return nil
	}
	externalAuth.ServiceProviderProperties.ClusterServiceID = csInternalID.DeepCopy()
	_, err = resourcesDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).
		ExternalAuth(resourceID.Parent.Name).Replace(ctx, externalAuth, nil)
	return utils.TrackError(err)
}
