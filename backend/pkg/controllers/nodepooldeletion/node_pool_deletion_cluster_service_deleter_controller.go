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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

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

// missingClusterServiceIDTimeout is how long we wait after DeletionTimestamp
// for the ClusterServiceID to appear before concluding that the corresponding
// Cluster Service NodePool was never created and we have no work to do.
const missingClusterServiceIDTimeout = 120 * time.Second

// nodePoolDeletionClusterServiceDeleter issues a Cluster Service delete for any
// NodePool whose DeletionTimestamp has been set. The frontend records the
// timestamp on the NodePool when DeleteNodePool is invoked, this controller
// picks it up and calls Cluster Service out-of-band so the frontend never has
// to block on it. Once the controller has issued the delete (or given up
// waiting for a ClusterServiceID), it stamps ClusterServiceDeletionTimestamp
// on the NodePool to record that this step is complete and avoid re-issuing
// the delete on subsequent syncs.
type nodePoolDeletionClusterServiceDeleter struct {
	clock                utilsclock.PassiveClock
	cooldownChecker      controllerutil.CooldownChecker
	nodePoolLister       listers.NodePoolLister
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.NodePoolSyncer = (*nodePoolDeletionClusterServiceDeleter)(nil)

func NewNodePoolDeletionClusterServiceDeleterController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, nodePoolLister := informers.NodePools()
	syncer := &nodePoolDeletionClusterServiceDeleter{
		clock:                clock,
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		nodePoolLister:       nodePoolLister,
		resourcesDBClient:    resourcesDBClient,
		clusterServiceClient: clusterServiceClient,
	}

	return controllerutils.NewNodePoolWatchingController(
		"NodePoolDeletionClusterServiceDeleter",
		resourcesDBClient,
		informers,
		time.Minute,
		syncer,
	)
}

func (c *nodePoolDeletionClusterServiceDeleter) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

// NeedsWork reports whether the deleter has unfinished business for the given
// NodePool: DeletionTimestamp must be set and ClusterServiceDeletionTimestamp
// must not yet be set.
func (c *nodePoolDeletionClusterServiceDeleter) NeedsWork(nodePool *api.HCPOpenShiftClusterNodePool) bool {
	return nodePool.ServiceProviderProperties.DeletionTimestamp != nil &&
		nodePool.ServiceProviderProperties.ClusterServiceDeletionTimestamp == nil
}

// SyncOnce calls Cluster Service to delete the NodePool when its DeletionTimestamp is set.
//
// If the NodePool has no ClusterServiceID yet, we may have raced cluster-service NodePool
// creation. We wait for missingClusterServiceIDTimeout from DeletionTimestamp before
// concluding the cluster-service NodePool was never created and we have nothing to delete.
//
// In either terminal case — CS delete issued or wait abandoned — we stamp
// ClusterServiceDeletionTimestamp so the next sync short-circuits.
func (c *nodePoolDeletionClusterServiceDeleter) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
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

	csID := nodePool.ServiceProviderProperties.ClusterServiceID
	deletedAt := nodePool.ServiceProviderProperties.DeletionTimestamp.Time

	if csID == nil || len(csID.String()) == 0 {
		elapsed := c.clock.Since(deletedAt)
		if elapsed < missingClusterServiceIDTimeout {
			// The frontend may still be in the middle of creating the cluster-service
			// NodePool, or the controller that does so hasn't run yet. Re-check on the
			// next sync. The resync interval and informer change events drive retries.
			return nil
		}
		logger.Info("giving up on cluster-service NodePool delete - ClusterServiceID never appeared",
			"deletionTimestamp", deletedAt)
	} else if err := c.clusterServiceClient.DeleteNodePool(ctx, *csID); err != nil {
		var ocmError *ocmerrors.Error

		switch {
		case errors.As(err, &ocmError) && ocmError.Status() == http.StatusBadRequest &&
			strings.Contains(ocmError.Reason(), "Cannot delete node pool: its parent cluster must be in a deletable state") &&
			strings.Contains(ocmError.Reason(), "Parent cluster state: 'uninstalling'"):
			// If the error is indicating that the parent cluster is already being
			// uninstalled we consider that the the nodepool is already being deleted
			// because Cluster Service on cluster deletion will end up deleting the
			// nodepools as well.
			// Matching an error message is brittle, but Clusters Service
			// returns 400 Bad Request for a wide range of errors and there
			// is no other information in the response to distinguish them.
			logger.Info("NodePool already being deleted by cluster-service via parent cluster deletion", "clusterServiceID", csID.String())
		case errors.As(err, &ocmError) && ocmError.Status() == http.StatusNotFound:
			// OCM error 404 - could be a stale CSID or a race against an in-flight CS
			// create. Wait before treating the NodePool as definitively gone
			elapsed := c.clock.Since(deletedAt)
			if elapsed < missingClusterServiceIDTimeout {
				return nil
			}
			logger.Info("cluster-service NodePool already deleted or race against in-flight CS create", "clusterServiceID", csID.String())
		default:
			return utils.TrackError(fmt.Errorf("failed to delete cluster-service NodePool: %w", err))
		}
	} else {
		logger.Info("requested cluster-service NodePool delete", "clusterServiceID", csID.String())
	}

	nodePool.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: c.clock.Now().UTC()}
	_, err = nodePoolCRUD.Replace(ctx, nodePool, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to stamp ClusterServiceDeletionTimestamp: %w", err))
	}

	return nil
}
