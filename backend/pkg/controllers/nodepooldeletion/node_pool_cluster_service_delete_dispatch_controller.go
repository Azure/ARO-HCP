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
	"k8s.io/utils/lru"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// missingClusterServiceIDTimeout is how long we wait after first observing
// DeletionTimestamp for the ClusterServiceID to appear before concluding
// that the corresponding Cluster Service Node Pool was never created and we have
// no work to do (or before treating a 404 from Cluster Service as definitive).
const missingClusterServiceIDTimeout = 120 * time.Second

// nodePoolClusterServiceDeleteDispatchSyncer issues a Cluster Service delete for any
// NodePool whose DeletionTimestamp has been set. The frontend records the
// timestamp on the NodePool when DeleteNodePool is invoked, this controller
// picks it up and calls Cluster Service out-of-band so the frontend never has
// to block on it. Once the controller has issued the delete (or given up
// waiting for a ClusterServiceID), it stamps ClusterServiceDeletionTimestamp
// on the NodePool to record that this step is complete and avoid re-issuing
// the delete on subsequent syncs.
// The controller also caches the time the controller has first seen the
// serviceProviderProperties.deletionTimestamp being set for a nodepool. This
// is used to avoid immediately triggering deletion in scenarios where the
// nodepool was marked for deletion but the controllers were not available for
// some reason until some time afterwards.
type nodePoolClusterServiceDeleteDispatchSyncer struct {
	clock                utilsclock.PassiveClock
	nodePoolLister       listers.NodePoolLister
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
	// firstSeenDeletionTimestampCache is a cache that contains the time the controller
	// has first seen the serviceProviderProperties.deletionTimestamp being set
	// for a nodepool. The cache key is the lowercased node pool's resource ID and
	// the value is a time.Time in UTC indicating the first seen deletion timestamp.
	firstSeenDeletionTimestampCache *lru.Cache
}

var _ controllerutils.NodePoolSyncer = (*nodePoolClusterServiceDeleteDispatchSyncer)(nil)

func NewNodePoolClusterServiceDeleteDispatchController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, nodePoolLister := informers.NodePools()
	syncer := &nodePoolClusterServiceDeleteDispatchSyncer{
		clock:                           clock,
		nodePoolLister:                  nodePoolLister,
		resourcesDBClient:               resourcesDBClient,
		clusterServiceClient:            clusterServiceClient,
		firstSeenDeletionTimestampCache: lru.New(50000),
	}

	return controllerutils.NewNodePoolWatchingController(
		"NodePoolClusterServiceDeleteDispatch",
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		time.Minute,
		syncer,
	)
}

// NeedsWork reports whether the deleter has unfinished business for the given
// NodePool: DeletionTimestamp must be set and ClusterServiceDeletionTimestamp
// must not yet be set.
func (c *nodePoolClusterServiceDeleteDispatchSyncer) NeedsWork(nodePool *api.HCPOpenShiftClusterNodePool) bool {
	// TODO temporary check to skip the new deletion approach for NodePools that were created before the new approach was implemented.
	// This will be removed once all nodepools whose deletion was triggered before the new approach is fully rolled out have been
	// fully deleted in all ARO-HCP permanent environments, for all regions.
	if !nodePool.ServiceProviderProperties.UsesNewNodePoolDeletionApproach {
		return false
	}

	return nodePool.ServiceProviderProperties.DeletionTimestamp != nil &&
		nodePool.ServiceProviderProperties.ClusterServiceDeletionTimestamp == nil
}

// SyncOnce calls Cluster Service to delete the NodePool when its DeletionTimestamp is set.
//
// If the NodePool has no ClusterServiceID yet, we may have raced cluster-service NodePool
// creation. We wait for missingClusterServiceIDTimeout from when we first observed
// DeletionTimestamp before concluding the cluster-service NodePool was never created.
//
// In either terminal case - CS delete issued or wait abandoned - we stamp
// ClusterServiceDeletionTimestamp so the next sync short-circuits.
func (c *nodePoolClusterServiceDeleteDispatchSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
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

	// We check if we have seen the deletion marker being set for this node pool.
	// If we don't we start tracking it in the cache.
	nodePoolDeletionTimestamp := nodePool.ServiceProviderProperties.DeletionTimestamp.Time
	cacheKey := strings.ToLower(nodePool.ID.String())
	var firstSeenNodePoolDeletionTimestamp time.Time
	firstSeenEntry, ok := c.firstSeenDeletionTimestampCache.Get(cacheKey)
	if ok {
		firstSeenNodePoolDeletionTimestamp = firstSeenEntry.(time.Time)
	} else {
		firstSeenNodePoolDeletionTimestamp = c.clock.Now().UTC()
		c.firstSeenDeletionTimestampCache.Add(cacheKey, firstSeenNodePoolDeletionTimestamp)
	}

	csID := nodePool.ServiceProviderProperties.ClusterServiceID
	if csID == nil || len(csID.String()) == 0 {
		elapsed := c.clock.Since(firstSeenNodePoolDeletionTimestamp)
		if elapsed < missingClusterServiceIDTimeout {
			// The frontend may still be in the middle of creating the cluster-service
			// NodePool, or the controller that does so hasn't run yet. Re-check on the
			// next sync. The resync interval and informer change events drive retries.
			return nil
		}
		logger.Info("giving up on cluster-service NodePool delete - ClusterServiceID never appeared",
			"nodePoolDeletionTimestamp", nodePoolDeletionTimestamp, "nodePoolFirstSeenDeletionTimestamp", firstSeenNodePoolDeletionTimestamp)
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
			elapsed := c.clock.Since(firstSeenNodePoolDeletionTimestamp)
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

	replacement := nodePool.DeepCopy()
	replacement.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: c.clock.Now().UTC()}
	_, err = nodePoolCRUD.Replace(ctx, replacement, nil)
	if database.IsPreconditionFailedError(err) {
		// if we have a conflict error, then we're guaranteed that our informer will eventually see an update and trigger us again.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to stamp ClusterServiceDeletionTimestamp: %w", err))
	}
	c.firstSeenDeletionTimestampCache.Remove(cacheKey)

	return nil
}
