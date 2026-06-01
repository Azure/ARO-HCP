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

package nodepoolupdatecontrollers

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
	utilsclock "k8s.io/utils/clock"
)

type dispatchNodePoolUpdateSyncer struct {
	clock                utilsclock.PassiveClock
	cooldownChecker      controllerutil.CooldownChecker
	nodePoolLister       listers.NodePoolLister
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

func NewDispatchNodePoolUpdateController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, nodePoolLister := informers.NodePools()
	syncer := &dispatchNodePoolUpdateSyncer{
		clock:                clock,
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		nodePoolLister:       nodePoolLister,
		resourcesDBClient:    resourcesDBClient,
		clusterServiceClient: clusterServiceClient,
	}

	return controllerutils.NewNodePoolWatchingController(
		"DispatchNodePoolUpdate",
		resourcesDBClient,
		informers,
		time.Minute,
		syncer,
	)
}

func (c *dispatchNodePoolUpdateSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

// func (c *dispatchNodePoolUpdateSyncer) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
// 	if operation.Status.IsTerminal() {
// 		return false
// 	}
// 	if operation.Request != database.OperationRequestUpdate {
// 		return false
// 	}
// 	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), api.NodePoolResourceType.String()) {
// 		return false
// 	}
// 	if len(operation.InternalID.String()) > 0 {
// 		return false
// 	}

// 	return true
// }

func (c *dispatchNodePoolUpdateSyncer) NeedsWork(nodePool *api.HCPOpenShiftClusterNodePool) bool {
	// TODO some other condition???
	// TODO should we log if it doesn't have CSID?
	return nodePool.ServiceProviderProperties.DeletionTimestamp == nil && nodePool.ServiceProviderProperties.ClusterServiceID != nil
}

func (c *dispatchNodePoolUpdateSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	logger := utils.LoggerFromContext(ctx)

	cachedNodePool, err := c.nodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get node pool from cache: %w", err))
	}
	if !c.NeedsWork(cachedNodePool) {
		return nil
	}

	// Confirm against the live document. The cache can lag behind a write that
	// just set DeletionTimestamp, populated ClusterServiceID, or stamped
	// ClusterServiceDeletionTimestamp.
	nodePoolCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	nodePool, err := nodePoolCRUD.Get(ctx, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get node pool: %w", err))
	}
	if !c.NeedsWork(nodePool) {
		return nil
	}

	csNodePoolBuilder, err := ocm.BuildCSNodePool(ctx, nodePool, true)
	if err != nil {
		return utils.TrackError(err)
	}
	nodePoolCSID := nodePool.ServiceProviderProperties.ClusterServiceID

	// TODO it could happen that CS nodepool would be in another state than `Ready`, I can think of:
	// 1. CS is in a different state than `Ready`, maybe because of a change in CS itself, or because it's in another state for some reason (error, ...)?
	// 2. The update call to CS succeeds but the controller fails to update the operation id afterwards. In that case the next UpdateNodePool
	//    call would fail be if CS returns the NodePool is not in ready state, until the update completes
	// In that case what would occur is that the update call would fail because of the CS nodepool not being in ready state
	// It's unavoidable that after CS update call succeeds there might be a failure on the cosmos operation update. In that case, the next UpdateNodePool
	// call would keep failing until the nodepool becomes ready again (update fully completes, ...), which means the
	// operation would remain active until that occurs.
	// We could add a similar check to what request credential controller does to check if we get a specific 400 with an error message
	// that indicates that the nodepool is in "updating" state on CS side but that technically doesn't cover case 1 if somehow CS can end up in a
	// 'updating' state not triggered by this controller.
	logger.Info("dispatching PATCH node pool to Cluster Service", "cs_node_pool_href", nodePoolCSID, "node_pool_resource_id", nodePool.ID.String())
	_, err = c.clusterServiceClient.UpdateNodePool(ctx, *nodePoolCSID, csNodePoolBuilder)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
