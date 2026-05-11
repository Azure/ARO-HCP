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
	"time"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// nodePoolClusterServiceIDClearer clears ClusterServiceID after the
// cluster-service NodePool itself has been confirmed gone. This runs after the
// deleter has already issued the delete request (ClusterServiceDeletionTimestamp
// is set); we poll cluster-service for the NodePool and, on 404, zero out the
// stored ClusterServiceID so downstream code knows the CS resource is fully gone.
type nodePoolClusterServiceIDClearer struct {
	cooldownChecker      controllerutils.CooldownChecker
	nodePoolLister       listers.NodePoolLister
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.NodePoolSyncer = (*nodePoolClusterServiceIDClearer)(nil)

func NewNodePoolClusterServiceIDClearerController(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, nodePoolLister := informers.NodePools()
	syncer := &nodePoolClusterServiceIDClearer{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		nodePoolLister:       nodePoolLister,
		resourcesDBClient:    resourcesDBClient,
		clusterServiceClient: clusterServiceClient,
	}

	return controllerutils.NewNodePoolWatchingController(
		"NodePoolDeletionClusterServiceIDClearer",
		resourcesDBClient,
		informers,
		time.Minute,
		syncer,
	)
}

func (c *nodePoolClusterServiceIDClearer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

// NeedsWork reports whether this controller has unfinished business for the
// given NodePool: deletion has been started (DeletionTimestamp), the deleter
// has already issued the CS delete (ClusterServiceDeletionTimestamp), and a
// ClusterServiceID is still recorded that needs verification before clearing.
func (c *nodePoolClusterServiceIDClearer) NeedsWork(nodePool *api.HCPOpenShiftClusterNodePool) bool {
	return nodePool.ServiceProviderProperties.DeletionTimestamp != nil &&
		nodePool.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil &&
		len(nodePool.ServiceProviderProperties.ClusterServiceID.String()) > 0
}

// SyncOnce reads the NodePool from cluster-service. If cluster-service reports
// 404, the deletion has finished and we zero out ClusterServiceID. Any other
// state means cluster-service is still draining the NodePool; we retry on the
// next sync.
func (c *nodePoolClusterServiceIDClearer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
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

	csID := nodePool.ServiceProviderProperties.ClusterServiceID
	if _, err := c.clusterServiceClient.GetNodePool(ctx, csID); err != nil {
		var ocmError *ocmerrors.Error
		if !errors.As(err, &ocmError) || ocmError.Status() != http.StatusNotFound {
			return utils.TrackError(fmt.Errorf("failed to get cluster-service NodePool: %w", err))
		}
		// 404 — cluster-service has finished deleting the NodePool, clear the ID.
		logger.Info("cluster-service NodePool gone — clearing ClusterServiceID", "clusterServiceID", csID.String())
		nodePool.ServiceProviderProperties.ClusterServiceID = api.InternalID{}
		if _, err := nodePoolCRUD.Replace(ctx, nodePool, nil); err != nil {
			return utils.TrackError(fmt.Errorf("failed to clear ClusterServiceID: %w", err))
		}
		return nil
	}

	// NodePool still exists in cluster-service; nothing to do yet.
	return nil
}
