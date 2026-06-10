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

package clusterupdate

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

type clusterClusterServiceUpdateDispatchSyncer struct {
	cooldownChecker      controllerutil.CooldownChecker
	clusterLister        listers.ClusterLister
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.ClusterSyncer = (*clusterClusterServiceUpdateDispatchSyncer)(nil)

func NewClusterClusterServiceUpdateDispatchController(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	syncer := &clusterClusterServiceUpdateDispatchSyncer{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		clusterLister:        clusterLister,
		resourcesDBClient:    resourcesDBClient,
		clusterServiceClient: clusterServiceClient,
	}

	return controllerutils.NewClusterWatchingController(
		"ClusterClusterServiceUpdateDispatch",
		resourcesDBClient,
		informers,
		nil,
		time.Minute,
		syncer,
	)
}

func clusterShouldProceed(cluster *api.HCPOpenShiftCluster) bool {
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return false
	}

	csID := cluster.ServiceProviderProperties.ClusterServiceID
	if csID == nil || len(csID.String()) == 0 {
		return false
	}

	return true
}

func (c *clusterClusterServiceUpdateDispatchSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *clusterClusterServiceUpdateDispatchSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cachedCluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	if !clusterShouldProceed(cachedCluster) {
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
	if !clusterShouldProceed(cluster) {
		return nil
	}

	clusterCSID := cluster.ServiceProviderProperties.ClusterServiceID
	oldClusterServiceCluster, err := c.clusterServiceClient.GetCluster(ctx, *clusterCSID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from Cluster Service: %w", err))
	}

	csClusterBuilder, csAutoscalerBuilder, err := ocm.BuildCSCluster(cluster.ID, "", cluster, nil, oldClusterServiceCluster)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build CS cluster: %w", err))
	}

	logger.Info("dispatching cluster autoscaler update to Cluster Service", "clusterServiceID", clusterCSID.String())

	_, err = c.clusterServiceClient.UpdateClusterAutoscaler(ctx, *clusterCSID, csAutoscalerBuilder)
	if err != nil {
		var ocmError *ocmerrors.Error
		if errors.As(err, &ocmError) && ocmError.Status() == http.StatusBadRequest &&
			strings.Contains(ocmError.Reason(), "not in an updatable state") {
			logger.Info("Cluster Service rejected cluster autoscaler update because the cluster is not updatable. Retrying on next sync.",
				"clusterServiceID", clusterCSID.String(),
				"error", err.Error(),
			)
			return nil
		}
		return utils.TrackError(fmt.Errorf("failed to update cluster-service ClusterAutoscaler: %w", err))
	}

	logger.Info("dispatching cluster update to Cluster Service", "clusterServiceID", clusterCSID.String())

	_, err = c.clusterServiceClient.UpdateCluster(ctx, *clusterCSID, csClusterBuilder)
	if err != nil {
		var ocmError *ocmerrors.Error
		if errors.As(err, &ocmError) && ocmError.Status() == http.StatusBadRequest &&
			strings.Contains(ocmError.Reason(), "not in an updatable state") {
			logger.Info("Cluster Service rejected cluster update because the cluster is not updatable. Retrying on next sync.",
				"clusterServiceID", clusterCSID.String(),
				"error", err.Error(),
			)
			return nil
		}
		return utils.TrackError(fmt.Errorf("failed to update cluster-service Cluster: %w", err))
	}

	logger.Info("requested cluster-service Cluster update", "clusterServiceID", clusterCSID.String())

	return nil
}
