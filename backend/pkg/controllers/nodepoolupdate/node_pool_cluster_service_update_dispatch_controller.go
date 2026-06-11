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

package nodepoolupdate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type nodePoolClusterServiceUpdateDispatchSyncer struct {
	cooldownChecker      controllerutil.CooldownChecker
	nodePoolLister       listers.NodePoolLister
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.NodePoolSyncer = (*nodePoolClusterServiceUpdateDispatchSyncer)(nil)

func NewNodePoolClusterServiceUpdateDispatchController(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, nodePoolLister := informers.NodePools()
	syncer := &nodePoolClusterServiceUpdateDispatchSyncer{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		nodePoolLister:       nodePoolLister,
		resourcesDBClient:    resourcesDBClient,
		clusterServiceClient: clusterServiceClient,
	}

	return controllerutils.NewNodePoolWatchingController(
		"NodePoolClusterServiceUpdateDispatch",
		resourcesDBClient,
		informers,
		time.Minute,
		syncer,
	)
}

func shouldProceed(nodePool *api.HCPOpenShiftClusterNodePool) bool {
	if nodePool.ServiceProviderProperties.DeletionTimestamp != nil {
		return false
	}

	csID := nodePool.ServiceProviderProperties.ClusterServiceID
	if csID == nil || len(csID.String()) == 0 {
		return false
	}

	return true
}

func (c *nodePoolClusterServiceUpdateDispatchSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *nodePoolClusterServiceUpdateDispatchSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	logger := utils.LoggerFromContext(ctx)

	cachedNodePool, err := c.nodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get node pool from cache: %w", err))
	}
	if !shouldProceed(cachedNodePool) {
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
	if !shouldProceed(nodePool) {
		return nil
	}

	csNodePoolBuilder, err := ocm.BuildCSNodePool(ctx, nodePool, true)
	if err != nil {
		return err
	}

	nodePoolCSID := nodePool.ServiceProviderProperties.ClusterServiceID

	_, err = c.clusterServiceClient.UpdateNodePool(ctx, *nodePoolCSID, csNodePoolBuilder)
	if err != nil {
		var ocmError *ocmerrors.Error

		switch {
		case errors.As(err, &ocmError) && ocmError.Status() == http.StatusBadRequest &&
			strings.Contains(ocmError.Reason(), "Node pools can only be") &&
			strings.Contains(ocmError.Reason(), "on clusters in an updatable state"):
			logger.Info("Cluster Service rejected node pool update because its parent cluster is not updatable. Retrying on next sync.",
				"clusterServiceID", nodePoolCSID.String(),
				"error", err.Error(),
			)
			return nil
		case errors.As(err, &ocmError) && ocmError.Status() == http.StatusBadRequest &&
			strings.Contains(ocmError.Reason(), "Node pool can only be updated in 'ready' state"):
			logger.Info("Cluster Service rejected node pool update because it is not updatable. Retrying on next sync.",
				"clusterServiceID", nodePoolCSID.String(),
				"error", err.Error(),
			)
			return nil
		default:
			return utils.TrackError(fmt.Errorf("failed to update cluster-service NodePool: %w", err))
		}
	}

	logger.Info("requested cluster-service NodePool update", "clusterServiceID", nodePoolCSID.String())

	return nil
}
