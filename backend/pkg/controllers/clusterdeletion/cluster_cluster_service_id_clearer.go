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

package clusterdeletion

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

// clusterClusterServiceIDClearer clears ClusterServiceID after the
// cluster-service Cluster itself has been confirmed gone. This runs after the
// delete dispatch controller has already issued the delete request
// (ClusterServiceDeletionTimestamp is set). We poll cluster-service for the
// Cluster and, on 404, zero out the stored ClusterServiceID so downstream
// code knows the CS resource is fully gone.
type clusterClusterServiceIDClearer struct {
	clusterLister        listers.ClusterLister
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.ClusterSyncer = (*clusterClusterServiceIDClearer)(nil)

func NewClusterClusterServiceIDClearerController(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	syncer := &clusterClusterServiceIDClearer{
		clusterLister:        clusterLister,
		resourcesDBClient:    resourcesDBClient,
		clusterServiceClient: clusterServiceClient,
	}

	return controllerutils.NewClusterWatchingController(
		"ClusterDeletionClusterServiceIDClearer",
		resourcesDBClient,
		informers,
		nil,
		time.Minute,
		syncer,
	)
}

// NeedsWork reports whether this controller has unfinished business for the
// given Cluster: deletion has been started (DeletionTimestamp), the deleter
// has already issued the CS delete (ClusterServiceDeletionTimestamp), and a
// ClusterServiceID is still recorded that needs verification before clearing.
func (c *clusterClusterServiceIDClearer) NeedsWork(cluster *api.HCPOpenShiftCluster) bool {
	// TODO temporary check to skip the new deletion approach for Clusters that were created before the new approach was implemented.
	// This will be removed once all clusters whose deletion was triggered before the new approach is fully rolled out have been
	// fully deleted in all ARO-HCP permanent environments, for all regions.
	if !cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach {
		return false
	}

	return cluster.ServiceProviderProperties.DeletionTimestamp != nil &&
		cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil &&
		cluster.ServiceProviderProperties.ClusterServiceID != nil && len(cluster.ServiceProviderProperties.ClusterServiceID.String()) > 0
}

// SyncOnce reads the Cluster from cluster-service. If cluster-service reports
// 404, the deletion has finished and we zero out ClusterServiceID.
func (c *clusterClusterServiceIDClearer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
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

	csID := cluster.ServiceProviderProperties.ClusterServiceID
	_, err = c.clusterServiceClient.GetClusterStatus(ctx, *csID)
	if err != nil {
		var ocmError *ocmerrors.Error
		if !errors.As(err, &ocmError) || ocmError.Status() != http.StatusNotFound {
			return utils.TrackError(fmt.Errorf("failed to get cluster-service Cluster: %w", err))
		}
		// 404 - cluster-service has finished deleting the Cluster, clear the CS ID.
		logger.Info("cluster-service Cluster gone. Clearing ClusterServiceID", "clusterServiceID", csID.String())
		cluster.ServiceProviderProperties.ClusterServiceID = nil
		if _, err := clusterCRUD.Replace(ctx, cluster, nil); err != nil {
			return utils.TrackError(fmt.Errorf("failed to clear ClusterServiceID: %w", err))
		}
		return nil
	}

	// Cluster still exists in cluster-service. Nothing to do yet.
	return nil
}
