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

package nodepoolcreationcontrollers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type nodePoolCreationClusterServiceCreate struct {
	cooldownChecker       controllerutil.CooldownChecker
	resourcesDBClient     database.ResourcesDBClient
	nodePoolLister        listers.NodePoolLister
	clustersServiceClient ocm.ClusterServiceClientSpec
}

func NewNodePoolCreationClusterServiceCreateController(
	resourcesDBClient database.ResourcesDBClient,
	clustersServiceClient ocm.ClusterServiceClientSpec,
	activeOperationInformer cache.SharedIndexInformer,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, nodePoolLister := informers.NodePools()
	syncer := &nodePoolCreationClusterServiceCreate{
		resourcesDBClient:     resourcesDBClient,
		nodePoolLister:        nodePoolLister,
		clustersServiceClient: clustersServiceClient,
	}

	return controllerutils.NewNodePoolWatchingController(
		"NodePoolCreationClusterServiceCreate",
		resourcesDBClient,
		informers,
		time.Minute,
		syncer,
	)
}

func (c *nodePoolCreationClusterServiceCreate) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestCreate {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), api.NodePoolResourceType.String()) {
		return false
	}
	if len(operation.InternalID.String()) > 0 {
		return false
	}
	return true
}

func (c *nodePoolCreationClusterServiceCreate) needsWork(ctx context.Context, nodePool *api.HCPOpenShiftClusterNodePool) bool {
	return nodePool.ServiceProviderProperties.DeletionTimestamp == nil &&
		(nodePool.ServiceProviderProperties.ClusterServiceID == nil || len(nodePool.ServiceProviderProperties.ClusterServiceID.String()) == 0)
}

func (c *nodePoolCreationClusterServiceCreate) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	logger := utils.LoggerFromContext(ctx)

	nodePool, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).Get(ctx, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	c.nodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	if !c.needsWork(ctx, nodePool) {
		return nil
	}

	nodePool, err = c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).Get(ctx, key.HCPNodePoolName)
	if err != nil {
		return utils.TrackError(err)
	}

	if c.needsWork(ctx, nodePool) {
		return nil
	}

	cluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(err)
	}
	if cluster.ServiceProviderProperties.ClusterServiceID == nil || len(cluster.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return utils.TrackError(fmt.Errorf("cluster %s has no ClusterServiceID", key.HCPClusterName))
	}

	clusterCSInternalID := *cluster.ServiceProviderProperties.ClusterServiceID

	// GET must target the same href POST would use: {clusterHref}/node_pools/{id} where id is
	// lowercased ARM name (see ocm.BuildCSNodePool). We reconstruct it here:
	csNodePoolHREF := ocm.GenerateAROHCPNodePoolHREF(clusterCSInternalID.ID(), strings.ToLower(key.HCPNodePoolName))
	nodePoolCSInternalID, err := api.NewInternalID(csNodePoolHREF)
	if err != nil {
		return utils.TrackError(fmt.Errorf("build node pool internal ID for adoption lookup: %w", err))
	}

	existing, err := c.findCSNodePool(ctx, nodePoolCSInternalID)
	if err != nil {
		return utils.TrackError(err)
	}

	if existing == nil {
		csNodePoolBuilder, err := ocm.BuildCSNodePool(ctx, nodePool, false)
		if err != nil {
			return utils.TrackError(err)
		}
		logger.Info("performing POST node pool to Cluster Service", "cs_node_pool_href", csNodePoolHREF, "node_pool_resource_id", nodePool.ID.String())
		_, err = c.clustersServiceClient.PostNodePool(ctx, clusterCSInternalID, csNodePoolBuilder)
		if err != nil {
			return utils.TrackError(err)
		}
	}

	logger.Info("setting node pool cluster service ID in node pool cosmos document", "node_pool_cluster_service_id", nodePoolCSInternalID.String())
	nodePool.ServiceProviderProperties.ClusterServiceID = nodePoolCSInternalID.DeepCopy() // DeepCopy() to avoid referencing the original pointer
	_, err = c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).Replace(ctx, nodePool, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

// findCSNodePool performs GetNodePool for the given Cluster Service node pool InternalID.
// It returns (nil, nil) when CS responds with 404.
func (c *nodePoolCreationClusterServiceCreate) findCSNodePool(ctx context.Context, nodePoolInternalID api.InternalID) (*arohcpv1alpha1.NodePool, error) {
	np, err := c.clustersServiceClient.GetNodePool(ctx, nodePoolInternalID)
	if err != nil {
		var ocmErr *ocmerrors.Error
		if errors.As(err, &ocmErr) && ocmErr.Status() == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}
	return np, nil
}

func (c *nodePoolCreationClusterServiceCreate) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}
