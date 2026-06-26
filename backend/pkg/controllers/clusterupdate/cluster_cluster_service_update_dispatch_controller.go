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
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
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

	// minimumReconcileTimeCooldownChecker ensures we don't hotloop from any source,
	// by ensuring that we don't reconcile more often than the cooldown time in it.
	minimumReconcileTimeCooldownChecker controllerutil.CooldownChecker
}

var _ controllerutils.ClusterSyncer = (*clusterClusterServiceUpdateDispatchSyncer)(nil)

func NewClusterClusterServiceUpdateDispatchController(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	syncer := NewClusterClusterServiceUpdateDispatchSyncer(
		resourcesDBClient,
		clusterServiceClient,
		activeOperationLister,
		clusterLister,
	)

	return controllerutils.NewClusterWatchingController(
		"ClusterClusterServiceUpdateDispatch",
		resourcesDBClient,
		informers,
		nil,
		time.Minute,
		syncer,
	)
}

func NewClusterClusterServiceUpdateDispatchSyncer(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	clusterLister listers.ClusterLister,
) controllerutils.ClusterSyncer {
	return &clusterClusterServiceUpdateDispatchSyncer{
		cooldownChecker: controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		// We set minimumReconcileTimeCooldownChecker so that SyncOnce is not executed
		// more than once per minute.
		minimumReconcileTimeCooldownChecker: controllerutil.NewTimeBasedCooldownChecker(1 * time.Minute),
		clusterLister:                       clusterLister,
		resourcesDBClient:                   resourcesDBClient,
		clusterServiceClient:                clusterServiceClient,
	}
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

	// Because this controller ends up calling Cluster Service each time it's reconciled and it's reconciled
	// while the resource exists and it is not being deleted, we establish a minimum reconcile time cooldown
	// to avoid putting too much pressure on Cluster Service.
	// TODO in the future, we could remove this cooldown checker by persisting a hash of the update dispatch configuration
	// sent to Cluster Service and checking if it has changed since the last time we sent it.
	if !c.minimumReconcileTimeCooldownChecker.CanSync(ctx, key) {
		return nil
	}

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
	clusterServiceCluster, err := c.clusterServiceClient.GetCluster(ctx, *clusterCSID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from Cluster Service: %w", err))
	}

	needsUpdate, err := ocm.ClusterUpdateDispatchConfigDiffers(cluster, clusterServiceCluster)
	if err != nil {
		return err
	}
	if !needsUpdate {
		return nil
	}

	desiredConfigJSON, err := ocm.ClusterUpdateDispatchConfigJSONFromRP(cluster)
	if err != nil {
		return err
	}
	actualConfigJSON, err := ocm.ClusterUpdateDispatchConfigJSONFromCS(clusterServiceCluster)
	if err != nil {
		return err
	}

	logger.Info("update dispatch config differs between RP and CS",
		"clusterServiceID", clusterCSID.String(),
		"desiredConfig", desiredConfigJSON,
		"actualConfig", actualConfigJSON,
	)

	csClusterBuilder, csAutoscalerBuilder, err := ocm.BuildCSCluster(cluster.ID, "", cluster, nil, clusterServiceCluster)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build CS cluster: %w", err))
	}

	clusterAutoscalerPayload, err := c.marshalClusterServiceClusterAutoscalerUpdatePayload(csAutoscalerBuilder)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to marshal Cluster Service autoscaler update payload: %w", err))
	}

	logger.Info("dispatching cluster autoscaler update to Cluster Service",
		"clusterServiceID", clusterCSID.String(),
		"clusterServiceClusterAutoscalerPayload", clusterAutoscalerPayload,
	)

	_, err = c.clusterServiceClient.UpdateClusterAutoscaler(ctx, *clusterCSID, csAutoscalerBuilder)
	if err != nil {
		var ocmError *ocmerrors.Error
		// XXX Matching an error message is brittle, but Clusters Service
		//     returns 400 Bad Request for a wide range of errors and there
		//     is no other information in the response to distinguish them.
		//
		//     If the error is indicating that a the cluster autoscaler is not in
		//     an updatable state, we return without error and retry again on the
		//     next sync. This can happen for example when the CS cluster is still in
		//     the initial creation process.
		if errors.As(err, &ocmError) && ocmError.Status() == http.StatusBadRequest &&
			strings.Contains(ocmError.Reason(), "Cluster") &&
			strings.Contains(ocmError.Reason(), "is in state") &&
			strings.Contains(ocmError.Reason(), "can't update") {
			logger.Info("Cluster Service rejected cluster autoscaler update because the cluster is not updatable. Retrying on next sync.",
				"clusterServiceID", clusterCSID.String(),
				"error", err.Error(),
			)
			return nil
		}
		return utils.TrackError(fmt.Errorf("failed to update cluster-service ClusterAutoscaler: %w", err))
	}

	clusterPayload, err := c.marshalClusterServiceClusterUpdatePayload(csClusterBuilder)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to marshal Cluster Service cluster update payload: %w", err))
	}

	logger.Info("dispatching cluster update to Cluster Service",
		"clusterServiceID", clusterCSID.String(),
		"clusterServiceClusterPayload", clusterPayload,
	)

	_, err = c.clusterServiceClient.UpdateCluster(ctx, *clusterCSID, csClusterBuilder)
	if err != nil {
		var ocmError *ocmerrors.Error
		// XXX Matching an error message is brittle, but Clusters Service
		//     returns 400 Bad Request for a wide range of errors and there
		//     is no other information in the response to distinguish them.
		//
		//     If the error is indicating that a the cluster autoscaler is not in
		//     an updatable state, we return without error and retry again on the
		//     next sync. This can happen for example when the CS cluster is still in
		//     the initial creation process.
		if errors.As(err, &ocmError) && ocmError.Status() == http.StatusBadRequest &&
			strings.Contains(ocmError.Reason(), "Cluster") &&
			strings.Contains(ocmError.Reason(), "is in state") &&
			strings.Contains(ocmError.Reason(), "can't update") {
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

func (c *clusterClusterServiceUpdateDispatchSyncer) marshalClusterServiceClusterUpdatePayload(
	clusterBuilder *arohcpv1alpha1.ClusterBuilder,
) (string, error) {
	cluster, err := clusterBuilder.Build()
	if err != nil {
		return "", err
	}

	var clusterBuffer bytes.Buffer
	if err := arohcpv1alpha1.MarshalCluster(cluster, &clusterBuffer); err != nil {
		return "", err
	}

	return clusterBuffer.String(), nil
}

func (c *clusterClusterServiceUpdateDispatchSyncer) marshalClusterServiceClusterAutoscalerUpdatePayload(
	autoscalerBuilder *arohcpv1alpha1.ClusterAutoscalerBuilder,
) (string, error) {
	autoscaler, err := autoscalerBuilder.Build()
	if err != nil {
		return "", err
	}

	var autoscalerBuffer bytes.Buffer
	if err := arohcpv1alpha1.MarshalClusterAutoscaler(autoscaler, &autoscalerBuffer); err != nil {
		return "", err
	}

	return autoscalerBuffer.String(), nil
}
