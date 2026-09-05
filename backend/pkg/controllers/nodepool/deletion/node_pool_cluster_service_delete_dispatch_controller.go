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

package deletion

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

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/clusterresources"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
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
// Before dispatching the delete, this controller waits until the
// ClusterResourcesController has cleaned up all its tagged ApplyDesires for
// this NodePool, so that kube resources are torn down before the NodePool itself.
//
// The controller also caches the time the controller has first seen the
// serviceProviderProperties.deletionTimestamp being set for a nodepool. This
// is used to avoid immediately triggering deletion in scenarios where the
// nodepool was marked for deletion but the controllers were not available for
// some reason until some time afterwards.
type nodePoolClusterServiceDeleteDispatchSyncer struct {
	clock                utilsclock.PassiveClock
	nodePoolLister       corelisters.NodePoolLister
	resourcesDBClient    corecosmosstorage.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
	applyDesireLister    kubeapplierlisters.ApplyDesireLister
	// firstSeenDeletionTimestampCache is a cache that contains the time the controller
	// has first seen the serviceProviderProperties.deletionTimestamp being set
	// for a nodepool. The cache key is the lowercased node pool's resource ID and
	// the value is a time.Time in UTC indicating the first seen deletion timestamp.
	firstSeenDeletionTimestampCache *lru.Cache
}

var _ controllerutils.NodePoolSyncer = (*nodePoolClusterServiceDeleteDispatchSyncer)(nil)

func NewNodePoolClusterServiceDeleteDispatchController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, nodePoolLister := informers.NodePools()
	_, applyDesireLister := kubeApplierInformers.ApplyDesires()
	syncer := &nodePoolClusterServiceDeleteDispatchSyncer{
		clock:                           clock,
		nodePoolLister:                  nodePoolLister,
		resourcesDBClient:               resourcesDBClient,
		clusterServiceClient:            clusterServiceClient,
		applyDesireLister:               applyDesireLister,
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
func (c *nodePoolClusterServiceDeleteDispatchSyncer) NeedsWork(nodePool *coreapi.HCPOpenShiftClusterNodePool) bool {
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
	if cosmosstorageutils.IsNotFoundError(err) {
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
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get node pool: %w", err))
	}
	if !c.NeedsWork(nodePool) {
		return nil
	}

	// Wait until ClusterResourcesController has deleted all its tagged ApplyDesires for this NodePool.
	hasTaggedDesires, err := c.hasNodePoolApplyDesires(ctx, key)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to check NodePool ApplyDesires: %w", err))
	}
	if hasTaggedDesires {
		logger.Info("waiting for ClusterResourcesController to delete its ApplyDesires before dispatching CS delete")
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
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		// if we have a conflict error, then we're guaranteed that our informer will eventually see an update and trigger us again.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to stamp ClusterServiceDeletionTimestamp: %w", err))
	}
	c.firstSeenDeletionTimestampCache.Remove(cacheKey)

	return nil
}

func (c *nodePoolClusterServiceDeleteDispatchSyncer) hasNodePoolApplyDesires(ctx context.Context, key controllerutils.HCPNodePoolKey) (bool, error) {
	desires, err := c.applyDesireLister.ListForNodePool(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if err != nil {
		return false, err
	}
	for _, desire := range desires {
		if desire.Tags != nil &&
			desire.Tags[kubeapplierapi.TagControllerName] == clusterresources.ClusterResourcesControllerName {
			return true, nil
		}
	}
	return false, nil
}
