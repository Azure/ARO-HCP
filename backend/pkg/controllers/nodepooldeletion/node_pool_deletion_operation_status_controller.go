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

package nodepooldeletion

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/operationcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// nodePoolDeletionOperationStatusController polls Cluster Service for the
// NodePool status during deletion and updates the active operation's
// provisioning state accordingly. It activates after the CS delete has been
// issued (ClusterServiceDeletionTimestamp set) and stops once the
// ClusterServiceID is cleared (404 handled by nodePoolClusterServiceIDClearer).
type nodePoolDeletionOperationStatusController struct {
	cooldownChecker      controllerutil.CooldownChecker
	nodePoolLister       listers.NodePoolLister
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
	notificationClient   *http.Client
}

var _ controllerutils.NodePoolSyncer = (*nodePoolDeletionOperationStatusController)(nil)

func NewNodePoolDeletionOperationStatusController(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	notificationClient *http.Client,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, nodePoolLister := informers.NodePools()
	syncer := &nodePoolDeletionOperationStatusController{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		nodePoolLister:       nodePoolLister,
		resourcesDBClient:    resourcesDBClient,
		clusterServiceClient: clusterServiceClient,
		notificationClient:   notificationClient,
	}

	return controllerutils.NewNodePoolWatchingController(
		"NodePoolDeletionOperationStatus",
		resourcesDBClient,
		informers,
		time.Minute,
		syncer,
	)
}

func (c *nodePoolDeletionOperationStatusController) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *nodePoolDeletionOperationStatusController) NeedsWork(nodePool *api.HCPOpenShiftClusterNodePool) bool {
	return nodePool.ServiceProviderProperties.DeletionTimestamp != nil &&
		nodePool.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil &&
		nodePool.ServiceProviderProperties.ClusterServiceID != nil && len(nodePool.ServiceProviderProperties.ClusterServiceID.String()) > 0 &&
		nodePool.ServiceProviderProperties.ActiveOperationID != ""
}

func (c *nodePoolDeletionOperationStatusController) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
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

	activeOpID := nodePool.ServiceProviderProperties.ActiveOperationID

	operation, err := c.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, activeOpID)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get active operation: %w", err))
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), api.NodePoolResourceType.String()) {
		return nil
	}
	if operation.Request != database.OperationRequestDelete {
		return nil
	}
	if operation.Status.IsTerminal() {
		return nil
	}

	csID := nodePool.ServiceProviderProperties.ClusterServiceID
	nodePoolStatus, err := c.clusterServiceClient.GetNodePoolStatus(ctx, *csID)
	if err != nil {
		var ocmError *ocmerrors.Error
		if !errors.As(err, &ocmError) || ocmError.Status() != http.StatusNotFound {
			return utils.TrackError(fmt.Errorf("failed to get cluster-service NodePool status: %w", err))
		}
		// 404 - CS has finished deleting. nodePoolClusterServiceIDClearer will clear the ID.
		logger.Info("cluster-service NodePool gone - skipping operation update", "clusterServiceID", csID.String())
		return nil
	}

	newOperationStatus, newOperationError, err := operationcontrollers.ConvertNodePoolStatus(operation, nodePoolStatus)
	if err != nil {
		return utils.TrackError(err)
	}

	// We call UpdateOperationStatus which performs the following:
	// - Update the operation status
	// - Update the resource provisioningStatus
	// - Depending on the calculated status, it may also unset the ActiveOperationID of the resource
	// - Notifies the operation owner (if the operation's notificationuri is set)
	// - Creates a Cosmos DB transaction to include all db operations in a single atomic operation.
	// Note: UpdateOperationStatus first retrieves the resource from cosmos so no writes of the resource after
	// this call should be performed, because otherwise we would always fail as we retrieve it in the beginning of the
	// controller too and we would have an outdated ETag if UpdateOperationStatus changes the resource and then the posterior write in our
	// controller would fail.
	err = operationcontrollers.UpdateOperationStatus(ctx, c.resourcesDBClient, operation, newOperationStatus, newOperationError, operationcontrollers.PostAsyncNotificationFn(c.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
