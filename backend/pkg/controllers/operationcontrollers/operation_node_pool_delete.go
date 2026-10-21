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

package operationcontrollers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationNodePoolDelete struct {
	clock                utilsclock.PassiveClock
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
	notificationClient   *http.Client
}

// NewOperationNodePoolDeleteController returns a new Controller instance that
// follows an asynchronous node pool deletion operation to completion and updates
// the corresponding operation document in Cosmos DB.
//
// The controller has the following responsibilities:
//   - While the NodePool Cosmos document is present, it reconciles the
//     operation and the node pool status.
//   - When the NodePool Cosmos document is deleted (by the nodePoolDeletionController),
//     it marks the operation as Succeeded. It also cleans up child
//     resources. TODO Note: This last part is handled by other controllers too but
//     because the SetDeleteOperationAsCompleted is still reused by other operations
//     that have not been migrated to asynchronous flow yet this remains.
//
// Note that "to completion" does not imply success. An operation is considered
// complete when its status field reaches what Azure defines as a terminal value;
// any of "Succeeded", "Failed", or "Canceled". Once the operation status reaches
// a terminal value, there will be no further updates to the operation document.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools
//	     Request: Delete
//	      Status: any non-terminal value
func NewOperationNodePoolDeleteController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &operationNodePoolDelete{
		clock:                clock,
		resourcesDBClient:    resourcesDBClient,
		clusterServiceClient: clusterServiceClient,
		notificationClient:   notificationClient,
	}

	controller := NewGenericOperationController(
		"OperationNodePoolDelete",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *operationNodePoolDelete) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestDelete {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), api.NodePoolResourceType.String()) {
		return false
	}
	return true
}

func (c *operationNodePoolDelete) legacyShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestDelete {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), api.NodePoolResourceType.String()) {
		return false
	}
	return true
}

func (c *operationNodePoolDelete) legacySynchronizeOperation(ctx context.Context, operation *api.Operation) error {
	logger := utils.LoggerFromContext(ctx)

	if !c.legacyShouldProcess(ctx, operation) {
		return nil // no work to do
	}

	nodePoolStatus, err := c.clusterServiceClient.GetNodePoolStatus(ctx, operation.InternalID)
	var ocmGetNodePoolError *ocmerrors.Error
	if err != nil && errors.As(err, &ocmGetNodePoolError) && ocmGetNodePoolError.Status() == http.StatusNotFound {
		logger.Info("node pool was deleted")

		err = SetDeleteOperationAsCompleted(ctx, c.clock, c.resourcesDBClient, operation, postAsyncNotificationFn(c.notificationClient))
		if err != nil {
			return utils.TrackError(err)
		}
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	newOperationStatus, newOperationError, err := convertNodePoolStatus(operation, nodePoolStatus)
	if err != nil {
		return utils.TrackError(err)
	}

	err = UpdateOperationStatus(ctx, c.clock, c.resourcesDBClient, operation, newOperationStatus, newOperationError, postAsyncNotificationFn(c.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (c *operationNodePoolDelete) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	operation, err := c.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsNotFoundError(err) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}

	// TODO remove this once migration of node pool deletion from frontend to backend is fully completed.
	if !operation.UsesNewNodePoolDeletionApproach {
		return c.legacySynchronizeOperation(ctx, operation)
	}

	// From here, we know it uses the new deletion approach.

	if !c.ShouldProcess(ctx, operation) {
		return nil // no work to do
	}

	nodePoolCRUD := c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).NodePools(operation.ExternalID.Parent.Name)
	nodePool, err := nodePoolCRUD.Get(ctx, operation.ExternalID.Name)
	if database.IsNotFoundError(err) {
		logger.Info("node pool document deleted - completing operation")
		err = SetDeleteOperationAsCompleted(ctx, c.clock, c.resourcesDBClient, operation, postAsyncNotificationFn(c.notificationClient))
		if err != nil {
			return utils.TrackError(err)
		}
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get node pool: %w", err))
	}

	if !c.shouldReconcileOperationAndResourceStatus(nodePool) {
		return nil
	}
	err = c.reconcileOperationAndResourceStatus(ctx, operation, nodePool)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (c *operationNodePoolDelete) shouldReconcileOperationAndResourceStatus(nodePool *api.HCPOpenShiftClusterNodePool) bool {
	return nodePool.ServiceProviderProperties.DeletionTimestamp != nil &&
		nodePool.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil &&
		nodePool.ServiceProviderProperties.ClusterServiceID != nil
}

func (c *operationNodePoolDelete) reconcileOperationAndResourceStatus(ctx context.Context, operation *api.Operation, nodePool *api.HCPOpenShiftClusterNodePool) error {
	logger := utils.LoggerFromContext(ctx)

	nodePoolCSID := nodePool.ServiceProviderProperties.ClusterServiceID

	nodePoolStatus, err := c.clusterServiceClient.GetNodePoolStatus(ctx, *nodePoolCSID)
	if err != nil {
		var ocmError *ocmerrors.Error
		if !errors.As(err, &ocmError) || ocmError.Status() != http.StatusNotFound {
			return utils.TrackError(fmt.Errorf("failed to get cluster-service NodePool status: %w", err))
		}
		// 404 - CS has finished deleting. nodePoolClusterServiceIDClearer will clear the ID.
		logger.Info("cluster-service NodePool gone - skipping operation update", "clusterServiceID", nodePoolCSID.String())
		return nil
	}

	// If the node pool is in the Ready state from CS side, we wait until the Cosmos NodePool document is deleted, which
	// will be picked up by a next reconciliation of this controller and we will update the operation to Succeeded.
	if nodePoolStatus.State().NodePoolStateValue() == string(NodePoolStateReady) {
		logger.Info("cluster-service NodePool in Ready state. Waiting until Cosmos NodePool document is deleted.")
		return nil
	}

	newOperationStatus, newOperationError, err := convertNodePoolStatus(operation, nodePoolStatus)
	if err != nil {
		return utils.TrackError(err)
	}

	err = UpdateOperationStatus(ctx, c.clock, c.resourcesDBClient, operation, newOperationStatus, newOperationError, postAsyncNotificationFn(c.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
