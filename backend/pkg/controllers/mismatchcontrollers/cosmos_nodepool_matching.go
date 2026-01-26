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

package mismatchcontrollers

import (
	"context"
	"fmt"
	"net/http"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type cosmosNodePoolMatching struct {
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

// NewCosmosNodePoolMatchingController periodically looks for mismatched cluster-service and cosmos nodepool
func NewCosmosNodePoolMatchingController(cosmosClient database.DBClient, clusterServiceClient ocm.ClusterServiceClientSpec) controllerutils.ClusterSyncer {
	c := &cosmosNodePoolMatching{
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
	}

	return c
}

func (c *cosmosNodePoolMatching) getAllCosmosObjs(ctx context.Context, keyObj controllerutils.HCPClusterKey) (map[string]*api.HCPOpenShiftClusterNodePool, []*api.HCPOpenShiftClusterNodePool, error) {
	clusterServiceIDToNodePool := map[string]*api.HCPOpenShiftClusterNodePool{}
	ret := []*api.HCPOpenShiftClusterNodePool{}

	allNodePools, err := c.cosmosClient.HCPClusters(keyObj.SubscriptionID, keyObj.ResourceGroupName).NodePools(keyObj.HCPClusterName).List(ctx, nil)
	if err != nil {
		return nil, nil, utils.TrackError(err)
	}

	for _, nodePool := range allNodePools.Items(ctx) {
		ret = append(ret, nodePool)
		existingCluster, exists := clusterServiceIDToNodePool[nodePool.ServiceProviderProperties.ClusterServiceID.String()]
		if exists {
			return nil, nil, utils.TrackError(fmt.Errorf("duplicate obj found: %s, owned by %q and %q", nodePool.ID.String(), existingCluster.ID.String(), nodePool.ID.String()))
		}
		clusterServiceIDToNodePool[nodePool.ServiceProviderProperties.ClusterServiceID.String()] = nodePool
	}
	if err := allNodePools.GetError(); err != nil {
		return nil, nil, utils.TrackError(err)
	}

	return clusterServiceIDToNodePool, ret, nil
}

func (c *cosmosNodePoolMatching) getAllClusterServiceObjs(ctx context.Context, clusterServiceClusterID api.InternalID) (map[string]*arohcpv1alpha1.NodePool, []*arohcpv1alpha1.NodePool, error) {
	clusterServiceIDToNodePool := map[string]*arohcpv1alpha1.NodePool{}
	ret := []*arohcpv1alpha1.NodePool{}

	nodePoolIterator := c.clusterServiceClient.ListNodePools(clusterServiceClusterID, "")
	for nodePool := range nodePoolIterator.Items(ctx) {
		ret = append(ret, nodePool)
		existingCluster, exists := clusterServiceIDToNodePool[nodePool.HREF()]
		if exists {
			return nil, nil, utils.TrackError(fmt.Errorf("duplicate obj found: %s, owned by %q and %q", nodePool.HREF(), existingCluster.ID(), nodePool.ID()))
		}
		clusterServiceIDToNodePool[nodePool.HREF()] = nodePool
	}
	if err := nodePoolIterator.GetError(); err != nil {
		return nil, nil, utils.TrackError(err)
	}

	return clusterServiceIDToNodePool, ret, nil
}

func (c *cosmosNodePoolMatching) synchronizeAllNodes(ctx context.Context, keyObj controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cluster, err := c.cosmosClient.HCPClusters(keyObj.SubscriptionID, keyObj.ResourceGroupName).Get(ctx, keyObj.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // no work to do
	}
	if err != nil {
		return utils.TrackError(err)
	}

	clusterServiceIDToCosmosNodePools, allCosmosNodePools, err := c.getAllCosmosObjs(ctx, keyObj)
	if err != nil {
		return utils.TrackError(err)
	}

	clusterServiceIDToClusterServiceNodePools, allClusterServiceNodePools, err := c.getAllClusterServiceObjs(ctx, cluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(err)
	}

	// now make sure that we can find a matching clusterservice cluster for all cosmos clusters
	for _, cosmosNodePool := range allCosmosNodePools {
		_, exists := clusterServiceIDToClusterServiceNodePools[cosmosNodePool.ServiceProviderProperties.ClusterServiceID.String()]
		if !exists {
			logger.Info("cosmos nodePool doesn't have matching cluster-service nodePool",
				"cosmosResourceID", cosmosNodePool.ID,
				"clusterServiceID", cosmosNodePool.ServiceProviderProperties.ClusterServiceID,
			)
		}
	}

	for _, clusterServiceNodePool := range allClusterServiceNodePools {
		_, exists := clusterServiceIDToCosmosNodePools[clusterServiceNodePool.HREF()]
		if !exists {
			logger.Info("cluster service nodePool doesn't have matching cosmos nodePool",
				"clusterServiceID", clusterServiceNodePool.HREF(),
			)
		}
	}

	// after reporting, do the cleanup
	for _, cosmosNodePool := range allCosmosNodePools {
		_, exists := clusterServiceIDToClusterServiceNodePools[cosmosNodePool.ServiceProviderProperties.ClusterServiceID.String()]
		if !exists {
			logger.Info("deleting cosmos nodepool", "cosmosResourceID", cosmosNodePool.ID)
			if err := controllerutils.DeleteRecursively(ctx, c.cosmosClient, cosmosNodePool.ID); err != nil {
				return utils.TrackError(err)
			}
		}
	}

	return nil
}

func (c *cosmosNodePoolMatching) SyncOnce(ctx context.Context, keyObj controllerutils.HCPClusterKey) error {
	syncErr := c.synchronizeAllNodes(ctx, keyObj)
	return utils.TrackError(syncErr)
}
