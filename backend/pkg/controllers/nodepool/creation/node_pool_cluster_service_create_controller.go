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

package creation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	utilsclock "k8s.io/utils/clock"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const NodePoolClusterServiceCreateControllerName = "NodePoolClusterServiceCreate"

type nodePoolClusterServiceCreateSyncer struct {
	clock                 utilsclock.PassiveClock
	resourcesDBClient     corecosmosstorage.ResourcesDBClient
	nodePoolLister        corelisters.NodePoolLister
	clusterLister         corelisters.ClusterLister
	clustersServiceClient ocm.ClusterServiceClientSpec
	notificationClient    *http.Client
}

func NewNodePoolClusterServiceCreateController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clustersServiceClient ocm.ClusterServiceClientSpec,
	notificationClient *http.Client,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, nodePoolLister := informers.NodePools()
	_, clusterLister := informers.Clusters()
	syncer := &nodePoolClusterServiceCreateSyncer{
		clock:                 clock,
		resourcesDBClient:     resourcesDBClient,
		nodePoolLister:        nodePoolLister,
		clusterLister:         clusterLister,
		clustersServiceClient: clustersServiceClient,
		notificationClient:    notificationClient,
	}

	controller := controllerutils.NewNodePoolWatchingController(
		NodePoolClusterServiceCreateControllerName,
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		time.Minute,
		syncer,
	)
	controller.AddInterestingResource(coreapi.NodePoolResourceType, "*")
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")

	return controller
}

func (c *nodePoolClusterServiceCreateSyncer) needsWork(nodePool *coreapi.NodePool) bool {
	return nodePool.ServiceProviderProperties.DeletionTimestamp == nil &&
		(nodePool.ServiceProviderProperties.ClusterServiceID == nil || len(nodePool.ServiceProviderProperties.ClusterServiceID.String()) == 0) &&
		// If there is no active create operation, either it already reached a terminal
		// state (e.g. failCreateOperation below already failed it) or none was ever
		// recorded. Either way there's nothing more for this controller to drive, and
		// retrying the Cluster Service POST would just loop forever.
		len(nodePool.ServiceProviderProperties.ActiveOperationID) > 0
}

func (c *nodePoolClusterServiceCreateSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	logger := utils.LoggerFromContext(ctx)

	nodePool, err := c.nodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if cosmosstorageutils.IsNotFoundError(err) {
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
	if cosmosstorageutils.IsNotFoundError(err) {
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
	nodePoolCSInternalID, err := metadataapi.NewInternalID(csNodePoolHREF)
	if err != nil {
		return utils.TrackError(fmt.Errorf("build node pool internal ID for adoption lookup: %w", err))
	}

	csCtx, cancel := context.WithTimeout(ctx, controllerutils.CSWriteTimeout)
	defer cancel()

	existing, err := c.findCSNodePool(csCtx, nodePoolCSInternalID)
	if err != nil {
		return utils.TrackError(err)
	}

	if existing == nil {
		csNodePoolBuilder, err := ocm.BuildCSNodePool(ctx, nodePool, false)
		if err != nil {
			return utils.TrackError(err)
		}
		logger.Info("performing POST node pool to Cluster Service", "cs_node_pool_href", csNodePoolHREF, "node_pool_resource_id", nodePool.ID.String())
		_, err = c.clustersServiceClient.PostNodePool(csCtx, clusterCSInternalID, csNodePoolBuilder)
		if c.isOCMErrorBadRequest(err) {
			logger.Error(err, "CS node pool POST returned OCM error with HTTP 400 status code", "cs_node_pool_href", csNodePoolHREF, "node_pool_resource_id", nodePool.ID.String())
			// A 400 from Cluster Service means the request is invalid as submitted (e.g. it
			// would violate a cluster-wide limit). Retrying the identical POST will never
			// succeed, so rather than returning an error here (which would requeue this key
			// forever and trip the backend's retry-hot-loop alert), fail the node pool's
			// active create operation directly with the real Cluster Service error.
			return c.failCreateOperation(ctx, key, nodePool, err)
		}
		if err != nil {
			return utils.TrackError(ocm.StripVolatileErrorFields(err))
		}
	}

	logger.Info("setting node pool cluster service ID in node pool cosmos document", "node_pool_cluster_service_id", nodePoolCSInternalID.String())
	replacement := nodePool.DeepCopy()
	replacement.ServiceProviderProperties.ClusterServiceID = nodePoolCSInternalID.DeepCopy() // DeepCopy() to avoid referencing the original pointer
	_, err = c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
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
func (c *nodePoolClusterServiceCreateSyncer) findCSNodePool(ctx context.Context, nodePoolInternalID metadataapi.InternalID) (*arohcpv1alpha1.NodePool, error) {
	np, err := c.clustersServiceClient.GetNodePool(ctx, nodePoolInternalID)
	if err != nil {
		var ocmErr *ocmerrors.Error
		if errors.As(err, &ocmErr) && ocmErr.Status() == http.StatusNotFound {
			return nil, nil
		}
		return nil, ocm.StripVolatileErrorFields(err)
	}
	return np, nil
}

func (c *nodePoolClusterServiceCreateSyncer) isOCMErrorBadRequest(err error) bool {
	var ocmErr *ocmerrors.Error
	return err != nil && errors.As(err, &ocmErr) && ocmErr.Status() == http.StatusBadRequest
}

// failCreateOperation marks the node pool's active create operation - and, transactionally,
// the node pool document itself - as Failed using csErr (a non-retriable error from Cluster
// Service) as the customer-facing reason. It returns nil on success so the caller does not
// requeue: nothing about csErr will change on retry, and needsWork will stop selecting this
// node pool again once ActiveOperationID is cleared by the terminal transition below.
func (c *nodePoolClusterServiceCreateSyncer) failCreateOperation(ctx context.Context, key controllerutils.HCPNodePoolKey, nodePool *coreapi.NodePool, csErr error) error {
	logger := utils.LoggerFromContext(ctx)

	activeOperationID := nodePool.ServiceProviderProperties.ActiveOperationID
	if len(activeOperationID) == 0 {
		// Nothing to fail; fall back to the old behavior of surfacing the error so it is
		// at least visible, even though we don't expect to hit this given needsWork now
		// requires a non-empty ActiveOperationID before calling in here.
		return utils.TrackError(ocm.StripVolatileErrorFields(csErr))
	}

	operation, err := c.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, activeOperationID)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.Info("active create operation not found, nothing to fail", "active_operation_id", activeOperationID)
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if operation.Status.IsTerminal() {
		// Someone else already finished this operation; nothing more to do.
		return nil
	}

	cloudErr := ocm.CSErrorToCloudError(csErr, nodePool.ID)
	err = operationbase.UpdateOperationStatus(ctx, c.clock, c.resourcesDBClient, operation, coreapi.ProvisioningStateFailed, cloudErr.CloudErrorBody, operationbase.PostAsyncNotificationFn(c.notificationClient))
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		// Someone else updated the operation or node pool concurrently; our informer will
		// eventually see the update, and there's nothing stale to retry here.
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	logger.Info("failed node pool create operation due to non-retriable Cluster Service error",
		"active_operation_id", activeOperationID,
		"cloud_error_code", cloudErr.Code,
		"cloud_error_message", cloudErr.Message)
	return nil
}
