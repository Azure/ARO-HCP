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

package creation

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type clusterPendingClusterServiceIDAssignSyncer struct {
	clusterLister     corelisters.ClusterLister
	resourcesDBClient corecosmosstorage.ResourcesDBClient
}

var _ controllerutils.ClusterSyncer = (*clusterPendingClusterServiceIDAssignSyncer)(nil)

const ClusterPendingClusterServiceIDAssignControllerName = "ClusterPendingClusterServiceIDAssign"

func NewClusterPendingClusterServiceIDAssignController(resourcesDBClient corecosmosstorage.ResourcesDBClient, backendInformers coreinformers.BackendInformers) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	syncer := &clusterPendingClusterServiceIDAssignSyncer{
		clusterLister:     clusterLister,
		resourcesDBClient: resourcesDBClient,
	}

	return controllerutils.NewClusterWatchingController(
		ClusterPendingClusterServiceIDAssignControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		time.Minute,
		syncer,
	)
}

func (c *clusterPendingClusterServiceIDAssignSyncer) needsWork(cluster *coreapi.HCPOpenShiftCluster) bool {
	return cluster.ServiceProviderProperties.DeletionTimestamp == nil &&
		cluster.ServiceProviderProperties.PendingClusterServiceID == nil &&
		(cluster.ServiceProviderProperties.ClusterServiceID == nil ||
			len(cluster.ServiceProviderProperties.ClusterServiceID.String()) == 0)
}

func (c *clusterPendingClusterServiceIDAssignSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	if !c.needsWork(cluster) {
		return nil
	}

	cluster, err = c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	uid := ocm.NewCSClusterUID()
	pendingID, err := metadataapi.NewInternalID(fmt.Sprintf("/api/aro_hcp/v1alpha1/clusters/%s", uid))
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create PendingClusterServiceID: %w", err))
	}

	logger.Info("Assigning PendingClusterServiceID", "pendingClusterServiceID", pendingID.String())
	cluster.ServiceProviderProperties.PendingClusterServiceID = &pendingID
	_, err = c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Replace(ctx, cluster, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace Cluster: %w", err))
	}

	return nil
}
