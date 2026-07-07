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

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const NodePoolClusterServiceCreateControllerName = "NodePoolClusterServiceCreate"

type nodePoolClusterServiceCreateSyncer struct {
	cooldownChecker       controllerutil.CooldownChecker
	resourcesDBClient     database.ResourcesDBClient
	nodePoolLister        listers.NodePoolLister
	clusterLister         listers.ClusterLister
	clustersServiceClient ocm.ClusterServiceClientSpec
}

func NewNodePoolClusterServiceCreateController(
	resourcesDBClient database.ResourcesDBClient,
	clustersServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, nodePoolLister := informers.NodePools()
	_, clusterLister := informers.Clusters()
	syncer := &nodePoolClusterServiceCreateSyncer{
		cooldownChecker:       controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient:     resourcesDBClient,
		nodePoolLister:        nodePoolLister,
		clusterLister:         clusterLister,
		clustersServiceClient: clustersServiceClient,
	}

	return controllerutils.NewNodePoolWatchingController(
		NodePoolClusterServiceCreateControllerName,
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		time.Minute,
		syncer,
	)
}

func (c *nodePoolClusterServiceCreateSyncer) needsWork(nodePool *api.HCPOpenShiftClusterNodePool) bool {
	return nodePool.ServiceProviderProperties.DeletionTimestamp == nil &&
		(nodePool.ServiceProviderProperties.ClusterServiceID == nil || len(nodePool.ServiceProviderProperties.ClusterServiceID.String()) == 0)
}

func (c *nodePoolClusterServiceCreateSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	logger := utils.LoggerFromContext(ctx)

	nodePool, err := c.nodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	if !c.needsWork(nodePool) {
		return nil
	}

	// For the NodePool, we retrieve from the actual database because we are about to use its data to interact with cluster-service.
	nodePool, err = c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).Get(ctx, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	if !c.needsWork(nodePool) {
		return nil
	}

	// For the Cluster, we retrieve from the cache because we are not about to use its data to interact with cluster-service. At
	// the moment we only use the ClusterServiceID to interact with cluster-service, which shouldn't change over time once set.
	// If at some point this controller evolves to use other Cluster properties that will be sent to cluster-service and that
	// can change over time, we will need to retrieve from the database instead.
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
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
		if c.isOCMErrorBadRequest(err) {
			logger.Error(err, "CS node pool POST returned OCM error with HTTP 400 status code", "cs_node_pool_href", csNodePoolHREF, "node_pool_resource_id", nodePool.ID.String())
		}
		if err != nil {
			return utils.TrackError(err)
		}
	}

	logger.Info("setting node pool cluster service ID in node pool cosmos document", "node_pool_cluster_service_id", nodePoolCSInternalID.String())
	replacement := nodePool.DeepCopy()
	replacement.ServiceProviderProperties.ClusterServiceID = nodePoolCSInternalID.DeepCopy() // DeepCopy() to avoid referencing the original pointer
	_, err = c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).Replace(ctx, replacement, nil)
	if database.IsPreconditionFailedError(err) {
		// if we have a conflict error, then we're guaranteed that our informer will eventually see an update and trigger us again.
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

// findCSNodePool performs GetNodePool for the given Cluster Service node pool InternalID.
// It returns (nil, nil) when CS responds with 404.
func (c *nodePoolClusterServiceCreateSyncer) findCSNodePool(ctx context.Context, nodePoolInternalID api.InternalID) (*arohcpv1alpha1.NodePool, error) {
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

func (c *nodePoolClusterServiceCreateSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *nodePoolClusterServiceCreateSyncer) isOCMErrorBadRequest(err error) bool {
	var ocmErr *ocmerrors.Error
	return err != nil && errors.As(err, &ocmErr) && ocmErr.Status() == http.StatusBadRequest
}
