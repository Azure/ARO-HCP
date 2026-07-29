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

	"k8s.io/apimachinery/pkg/util/rand"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// GenerateRandomClusterClusterServiceHREF returns a Cluster Service cluster HREF
// with a random 10-character ID, matching the cluster-service mock PostCluster behavior.
func GenerateRandomClusterClusterServiceHREF() string {
	return ocm.GenerateOCMCommercialClusterHREF(rand.String(10))
}

// CalculateClusterServiceIDFromNodePoolResourceID returns the Cluster Service internal ID
// for a node pool, derived from the parent cluster's stored ClusterServiceID.
func CalculateClusterServiceIDFromNodePoolResourceID(
	ctx context.Context,
	resourcesDBClient database.ResourcesDBClient,
	resourceIDString string,
) (string, error) {
	resourceID, err := azcorearm.ParseResourceID(resourceIDString)
	if err != nil {
		return "", utils.TrackError(err)
	}
	if !strings.EqualFold(resourceID.ResourceType.String(), api.NodePoolResourceType.String()) {
		return "", utils.TrackError(fmt.Errorf("resource %s is not a node pool", resourceIDString))
	}
	if resourceID.Parent == nil {
		return "", utils.TrackError(fmt.Errorf("node pool %s has no parent cluster", resourceIDString))
	}
	clusterCSID, err := clusterClusterServiceID(ctx, resourcesDBClient, resourceID)
	if err != nil {
		return "", err
	}
	return ocm.GenerateAROHCPNodePoolHREF(clusterCSID.ID(), strings.ToLower(resourceID.Name)), nil
}

// CalculateClusterServiceIDFromExternalAuthResourceID returns the Cluster Service internal ID
// for an external auth, derived from the parent cluster's stored ClusterServiceID.
func CalculateClusterServiceIDFromExternalAuthResourceID(
	ctx context.Context,
	resourcesDBClient database.ResourcesDBClient,
	resourceIDString string,
) (string, error) {
	resourceID, err := azcorearm.ParseResourceID(resourceIDString)
	if err != nil {
		return "", utils.TrackError(err)
	}
	if !strings.EqualFold(resourceID.ResourceType.String(), api.ExternalAuthResourceType.String()) {
		return "", utils.TrackError(fmt.Errorf("resource %s is not an external auth", resourceIDString))
	}
	if resourceID.Parent == nil {
		return "", utils.TrackError(fmt.Errorf("external auth %s has no parent cluster", resourceIDString))
	}
	clusterCSID, err := clusterClusterServiceID(ctx, resourcesDBClient, resourceID)
	if err != nil {
		return "", err
	}
	return ocm.GenerateAROHCPExternalAuthHREF(clusterCSID.ID(), resourceID.Name), nil
}

// StampRandomClusterServiceID assigns a random Cluster Service cluster HREF to the cluster document.
func StampRandomClusterServiceID(
	ctx context.Context,
	resourcesDBClient database.ResourcesDBClient,
	clusterResourceIDString string,
) error {
	return SetClusterServiceID(ctx, resourcesDBClient, clusterResourceIDString, GenerateRandomClusterClusterServiceHREF())
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
