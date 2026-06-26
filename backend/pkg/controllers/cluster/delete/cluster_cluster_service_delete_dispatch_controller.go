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

package delete

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
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// missingClusterServiceIDTimeout is how long we wait after first observing
// DeletionTimestamp for the ClusterServiceID to appear before concluding
// that the corresponding Cluster Service Cluster was never created and we have
// no work to do (or before treating a 404 from Cluster Service as definitive).
const missingClusterServiceIDTimeout = 120 * time.Second

// clusterClusterServiceDeleteDispatchSyncer issues a Cluster Service delete for any
// Cluster whose DeletionTimestamp has been set. The frontend records the
// timestamp on the Cluster when DeleteCluster is invoked, this controller
// picks it up and calls Cluster Service out-of-band so the frontend never has
// to block on it. Once the controller has issued the delete (or given up
// waiting for a ClusterServiceID), it stamps ClusterServiceDeletionTimestamp
// on the Cluster to record that this step is complete and avoid re-issuing
// the delete on subsequent syncs.
type clusterClusterServiceDeleteDispatchSyncer struct {
	clock                utilsclock.PassiveClock
	cooldownChecker      controllerutil.CooldownChecker
	clusterLister        listers.ClusterLister
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
	// firstSeenDeletionTimestampCache is a cache that contains the time the controller
	// has first seen the serviceProviderProperties.deletionTimestamp being set
	// for a cluster. The cache key is the lowercased cluster's resource ID and
	// the value is a time.Time in UTC indicating the first seen deletion timestamp.
	firstSeenDeletionTimestampCache *lru.Cache
}

var _ controllerutils.ClusterSyncer = (*clusterClusterServiceDeleteDispatchSyncer)(nil)

func NewClusterClusterServiceDeleteDispatchController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	syncer := &clusterClusterServiceDeleteDispatchSyncer{
		clock:                           clock,
		cooldownChecker:                 controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		clusterLister:                   clusterLister,
		resourcesDBClient:               resourcesDBClient,
		clusterServiceClient:            clusterServiceClient,
		firstSeenDeletionTimestampCache: lru.New(50000),
	}

	return controllerutils.NewClusterWatchingController(
		"ClusterClusterServiceDeleteDispatch",
		resourcesDBClient,
		informers,
		nil, // no kubeApplierInformers needed for deletion
		time.Minute,
		syncer,
	)
}

func (c *clusterClusterServiceDeleteDispatchSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

// NeedsWork reports whether the deleter has unfinished business for the given
// Cluster: DeletionTimestamp must be set and ClusterServiceDeletionTimestamp
// must not yet be set.
func (c *clusterClusterServiceDeleteDispatchSyncer) NeedsWork(cluster *api.HCPOpenShiftCluster) bool {
	// TODO temporary check to skip the new deletion approach for Clusters that were created before the new approach was implemented.
	// This will be removed once all clusters whose deletion was triggered before the new approach is fully rolled out have been
	// fully deleted in all ARO-HCP permanent environments, for all regions.
	if !cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach {
		return false
	}

	return cluster.ServiceProviderProperties.DeletionTimestamp != nil &&
		cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp == nil
}

// SyncOnce calls Cluster Service to delete the Cluster when its DeletionTimestamp is set.

// If the Cluster has no ClusterServiceID yet, we may have raced cluster-service Cluster
// creation. We wait for missingClusterServiceIDTimeout from when we first observed
// DeletionTimestamp before concluding the cluster-service Cluster was never created.
//
// In either terminal case - CS delete issued or wait abandoned - we stamp
// ClusterServiceDeletionTimestamp so the next sync short-circuits.
func (c *clusterClusterServiceDeleteDispatchSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cachedCluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	if !c.NeedsWork(cachedCluster) {
		return nil
	}

	// Confirm against the live document.
	clusterCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	cluster, err := clusterCRUD.Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster: %w", err))
	}
	if !c.NeedsWork(cluster) {
		return nil
	}

	clusterDeletionTimestamp := cluster.ServiceProviderProperties.DeletionTimestamp.Time
	cacheKey := strings.ToLower(cluster.ID.String())
	var firstSeenClusterDeletionTimestamp time.Time
	firstSeenEntry, ok := c.firstSeenDeletionTimestampCache.Get(cacheKey)
	if ok {
		firstSeenClusterDeletionTimestamp = firstSeenEntry.(time.Time)
	} else {
		firstSeenClusterDeletionTimestamp = c.clock.Now().UTC()
		c.firstSeenDeletionTimestampCache.Add(cacheKey, firstSeenClusterDeletionTimestamp)
	}

	csID := cluster.ServiceProviderProperties.ClusterServiceID
	if csID == nil || len(csID.String()) == 0 {
		elapsed := c.clock.Since(firstSeenClusterDeletionTimestamp)
		if elapsed < missingClusterServiceIDTimeout {
			// The frontend may still be in the middle of creating the cluster-service
			// Cluster, or the controller that does so hasn't run yet. Re-check on the
			// next sync. The resync interval and informer change events drive retries.
			return nil
		}
		logger.Info("giving up on cluster-service Cluster delete - ClusterServiceID never appeared",
			"clusterDeletionTimestamp", clusterDeletionTimestamp, "clusterFirstSeenDeletionTimestamp", firstSeenClusterDeletionTimestamp)
	} else if err := c.clusterServiceClient.DeleteCluster(ctx, *csID); err != nil {
		var ocmError *ocmerrors.Error

		switch {
		case errors.As(err, &ocmError) && ocmError.Status() == http.StatusNotFound:
			// OCM error 404 - could be a stale CSID or a race against an in-flight CS
			// create. Wait before treating the Cluster as definitively gone
			elapsed := c.clock.Since(firstSeenClusterDeletionTimestamp)
			if elapsed < missingClusterServiceIDTimeout {
				return nil
			}
			logger.Info("cluster-service Cluster already deleted or race against in-flight CS create", "clusterServiceID", csID.String())
		default:
			return utils.TrackError(fmt.Errorf("failed to delete cluster-service Cluster: %w", err))
		}
	} else {
		logger.Info("requested cluster-service Cluster delete", "clusterServiceID", csID.String())
	}

	cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: c.clock.Now().UTC()}
	_, err = clusterCRUD.Replace(ctx, cluster, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to stamp ClusterServiceDeletionTimestamp: %w", err))
	}
	c.firstSeenDeletionTimestampCache.Remove(cacheKey)

	return nil
}
