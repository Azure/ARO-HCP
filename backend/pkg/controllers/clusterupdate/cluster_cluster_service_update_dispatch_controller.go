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

	"github.com/google/go-cmp/cmp"

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

// clusterClusterServiceUpdateDispatchSyncer calls Cluster Service's Cluster PATCH when
// the Cluster's dispatch-managed configuration has drifted. It reconciles a curated subset of
// fields defined by ocm.clusterUpdateDispatchConfig.
//
// On each reconcile, the Cluster's state and the live Cluster Service cluster state
// are projected into ocm.clusterUpdateDispatchConfig. When the projections from both sides
// differ, it PATCHes Cluster Service (autoscaler first, then cluster).
//
// Dispatch is paired with operation state calculation in operationcontrollers
// (operation_cluster_update_state_calculation.go): dispatch sends updates, operation state
// verifies propagation before the ARM cluster update operation succeeds.
type clusterClusterServiceUpdateDispatchSyncer struct {
	cooldownChecker      controllerutil.CooldownChecker
	clusterLister        listers.ClusterLister
	subscriptionLister   listers.SubscriptionLister
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
	_, subscriptionLister := informers.Subscriptions()
	syncer := NewClusterClusterServiceUpdateDispatchSyncer(
		resourcesDBClient,
		clusterServiceClient,
		activeOperationLister,
		clusterLister,
		subscriptionLister,
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
	subscriptionLister listers.SubscriptionLister,
) controllerutils.ClusterSyncer {
	return &clusterClusterServiceUpdateDispatchSyncer{
		cooldownChecker: controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		// We set minimumReconcileTimeCooldownChecker so that SyncOnce is not executed
		// more than once per minute.
		minimumReconcileTimeCooldownChecker: controllerutil.NewTimeBasedCooldownChecker(1 * time.Minute),
		clusterLister:                       clusterLister,
		subscriptionLister:                  subscriptionLister,
		resourcesDBClient:                   resourcesDBClient,
		clusterServiceClient:                clusterServiceClient,
	}
}

func needsWork(cluster *api.HCPOpenShiftCluster) bool {
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
	// while the resource exists and while it is not being deleted, we establish a minimum reconcile time cooldown
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
	if !needsWork(cachedCluster) {
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
	if !needsWork(cluster) {
		return nil
	}

	subscription, err := c.subscriptionLister.Get(ctx, key.SubscriptionID)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get subscription from cache: %w", err))
	}
	if subscription.Properties == nil || subscription.Properties.TenantId == nil {
		return utils.TrackError(fmt.Errorf("subscription properties or subscription tenant ID is nil"))
	}

	clusterCSID := cluster.ServiceProviderProperties.ClusterServiceID
	serviceProviderCluster, err := c.resourcesDBClient.ServiceProviderClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name).Get(ctx, api.ServiceProviderClusterResourceName)
	if database.IsNotFoundError(err) {
		// We expect the service provider cluster to be created when the cluster exists. If it doesn't exist, we wait for the next sync.
		logger.Info("service provider cluster not found, waiting")
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get service provider cluster: %w", err))
	}

	clusterServiceCluster, err := c.clusterServiceClient.GetCluster(ctx, *clusterCSID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from Cluster Service: %w", err))
	}

	// We check if the desired config coming from cosmos differs from the actual config coming from cluster service.
	// If it doesn't, we are done and don't need to dispatch an update. If it does, we need to dispatch an update to
	// cluster service. Comparison uses canonical JSON (sorted object keys at every level) so we can compare them
	// using direct string equality.
	desiredConfigJSON, err := ocm.ClusterUpdateDispatchConfigJSONFromRP(cluster, serviceProviderCluster)
	if err != nil {
		return utils.TrackError(err)
	}
	actualConfigJSON, err := ocm.ClusterUpdateDispatchConfigJSONFromCS(clusterServiceCluster)
	if err != nil {
		return utils.TrackError(err)
	}
	if desiredConfigJSON == actualConfigJSON {
		return nil
	}

	configDiff := cmp.Diff(desiredConfigJSON, actualConfigJSON)

	logger.Info("update dispatch config differs between RP and CS",
		"clusterServiceID", clusterCSID.String(),
		"desiredConfig", desiredConfigJSON,
		"actualConfig", actualConfigJSON,
		"configDiff", configDiff,
	)

	csClusterBuilder, csAutoscalerBuilder, err := ocm.BuildCSCluster(cluster.ID, *subscription.Properties.TenantId, cluster, nil, clusterServiceCluster, serviceProviderCluster)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build CS cluster: %w", err))
	}

	// We marshal the autoscaler CS builder config we are going to submit for cs cluster autoscaler update for logging purposes
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

	logger.Info("dispatched cluster autoscaler update to Cluster Service", "clusterServiceID", clusterCSID.String())

	// We marshal the cluster CS builder config we are going to submit for cs cluster update for logging purposes
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

	logger.Info("dispatched cluster update to Cluster Service", "clusterServiceID", clusterCSID.String())
	return nil
}

// marshalClusterServiceClusterUpdatePayload serializes the cluster PATCH body for logging.
func (c *clusterClusterServiceUpdateDispatchSyncer) marshalClusterServiceClusterUpdatePayload(clusterBuilder *arohcpv1alpha1.ClusterBuilder) (string, error) {
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

// marshalClusterServiceClusterAutoscalerUpdatePayload serializes the autoscaler PATCH body for logging.
func (c *clusterClusterServiceUpdateDispatchSyncer) marshalClusterServiceClusterAutoscalerUpdatePayload(autoscalerBuilder *arohcpv1alpha1.ClusterAutoscalerBuilder) (string, error) {
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
