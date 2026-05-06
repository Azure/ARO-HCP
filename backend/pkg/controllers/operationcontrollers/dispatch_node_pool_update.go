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

package operationcontrollers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type dispatchNodePoolUpdate struct {
	cosmosClient          database.DBClient
	clustersServiceClient ocm.ClusterServiceClientSpec
}

func NewDispatchNodePoolUpdateController(
	cosmosClient database.DBClient,
	clustersServiceClient ocm.ClusterServiceClientSpec,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &dispatchNodePoolUpdate{
		cosmosClient:          cosmosClient,
		clustersServiceClient: clustersServiceClient,
	}

	return NewGenericOperationController(
		"DispatchNodePoolUpdate",
		syncer,
		10*time.Second,
		activeOperationInformer,
		cosmosClient,
	)
}

func (c *dispatchNodePoolUpdate) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestUpdate {
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

func (c *dispatchNodePoolUpdate) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	operation, err := c.cosmosClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !c.ShouldProcess(ctx, operation) {
		return nil
	}

	ext := operation.ExternalID
	nodePool, err := c.cosmosClient.HCPClusters(ext.SubscriptionID, ext.ResourceGroupName).NodePools(ext.Parent.Name).Get(ctx, ext.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	if nodePool.ServiceProviderProperties.ActiveOperationID != "" &&
		nodePool.ServiceProviderProperties.ActiveOperationID != operation.OperationID.Name {
		logger.Info("skipping node pool update dispatch: active operation mismatch",
			"nodepool_active_operation_id", nodePool.ServiceProviderProperties.ActiveOperationID,
			"operation_name", operation.OperationID.Name)
		return nil // TODO should this be return error or nil?
	}

	csIDFromNodePool := nodePool.ServiceProviderProperties.ClusterServiceID
	if len(csIDFromNodePool.String()) == 0 {
		return utils.TrackError(fmt.Errorf("node pool %s has no ClusterServiceID", ext.Name))
	}

	csNodePoolBuilder, err := ocm.BuildCSNodePool(ctx, nodePool, true)
	if err != nil {
		return utils.TrackError(err)
	}

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
	logger.Info("dispatching PATCH node pool to Cluster Service", "cs_node_pool_href", csIDFromNodePool.String(), "node_pool_resource_id", nodePool.ID.String())
	_, err = c.clustersServiceClient.UpdateNodePool(ctx, csIDFromNodePool, csNodePoolBuilder)
	if err != nil {
		return utils.TrackError(err)
	}

	operation.InternalID = csIDFromNodePool
	_, err = c.cosmosClient.Operations(key.SubscriptionID).Replace(ctx, operation, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
