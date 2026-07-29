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

package nodepoolupdate

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

// nodePoolClusterServiceUpdateDispatchSyncer calls Cluster Service's NodePool PATCH when
// the NodePool's dispatch-managed configuration has drifted. It reconciles a curated subset of
// fields defined by ocm.nodePoolUpdateDispatchConfig.
//
// On each reconcile, the NodePool's state and the live Cluster Service node pool state
// are projected into ocm.nodePoolUpdateDispatchConfig. When the projections from both sides
// differ, it PATCHes Cluster Service.
//
// Dispatch is paired with operation state calculation in operationcontrollers
// (operation_node_pool_update_state_calculation.go): dispatch sends updates, operation state
// verifies propagation before the ARM node pool update operation succeeds.
type nodePoolClusterServiceUpdateDispatchSyncer struct {
	cooldownChecker      controllerutil.CooldownChecker
	nodePoolLister       listers.NodePoolLister
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec

	// minimumReconcileTimeCooldownChecker ensures we don't hotloop from any source,
	// by ensuring that we don't reconcile more often than the cooldown time in it.
	minimumReconcileTimeCooldownChecker controllerutil.CooldownChecker
}

var _ controllerutils.NodePoolSyncer = (*nodePoolClusterServiceUpdateDispatchSyncer)(nil)

func NewNodePoolClusterServiceUpdateDispatchController(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, nodePoolLister := informers.NodePools()
	syncer := NewNodePoolClusterServiceUpdateDispatchSyncer(
		resourcesDBClient,
		clusterServiceClient,
		activeOperationLister,
		nodePoolLister,
	)

	return controllerutils.NewNodePoolWatchingController(
		"NodePoolClusterServiceUpdateDispatch",
		resourcesDBClient,
		informers,
		nil,
		time.Minute,
		syncer,
	)
}

func NewNodePoolClusterServiceUpdateDispatchSyncer(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	nodePoolLister listers.NodePoolLister,
) controllerutils.NodePoolSyncer {
	return &nodePoolClusterServiceUpdateDispatchSyncer{
		cooldownChecker: controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		// We set minimumReconcileTimeCooldownChecker so that SyncOnce is not executed
		// more than once per minute.
		minimumReconcileTimeCooldownChecker: controllerutil.NewTimeBasedCooldownChecker(1 * time.Minute),
		nodePoolLister:                      nodePoolLister,
		resourcesDBClient:                   resourcesDBClient,
		clusterServiceClient:                clusterServiceClient,
	}
}

func needsWork(nodePool *api.HCPOpenShiftClusterNodePool) bool {
	if nodePool.ServiceProviderProperties.DeletionTimestamp != nil {
		return false
	}

	csID := nodePool.ServiceProviderProperties.ClusterServiceID
	if csID == nil || len(csID.String()) == 0 {
		return false
	}

	return true
}

func (c *nodePoolClusterServiceUpdateDispatchSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *nodePoolClusterServiceUpdateDispatchSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	logger := utils.LoggerFromContext(ctx)

	// Because this controller ends up calling Cluster Service each time it's reconciled and it's reconciled
	// while the resource exists and while it is not being deleted, we establish a minimum reconcile time cooldown
	// to avoid putting too much pressure on Cluster Service.
	// TODO in the future, we could remove this cooldown checker by persisting a hash of the update dispatch configuration
	// sent to Cluster Service and checking if it has changed since the last time we sent it.
	if !c.minimumReconcileTimeCooldownChecker.CanSync(ctx, key) {
		return nil
	}

	cachedNodePool, err := c.nodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get node pool from cache: %w", err))
	}
	if !needsWork(cachedNodePool) {
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
	if !needsWork(nodePool) {
		return nil
	}

	nodePoolCSID := nodePool.ServiceProviderProperties.ClusterServiceID
	csNodePool, err := c.clusterServiceClient.GetNodePool(ctx, *nodePoolCSID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get node pool from Cluster Service: %w", err))
	}

	// We check if the desired config coming from cosmos differs from the actual config coming from cluster service.
	// If it doesn't, we are done and don't need to dispatch an update. If it does, we need to dispatch an update to
	// cluster service. Comparison uses canonical JSON (sorted object keys at every level) so we can compare them
	// using direct string equality. For the particular case of the node drain-timeout attribute,
	// normalization is applied when RP has no override.
	desiredConfigJSON, actualConfigJSON, err := ocm.NodePoolUpdateDispatchConfigDiffJSON(nodePool, csNodePool)
	if err != nil {
		return err
	}
	if desiredConfigJSON == actualConfigJSON {
		return nil
	}

	configDiff := cmp.Diff(desiredConfigJSON, actualConfigJSON)

	logger.Info("node pool update dispatch config differs between RP and CS",
		"clusterServiceID", nodePoolCSID.String(),
		"desiredConfig", desiredConfigJSON,
		"actualConfig", actualConfigJSON,
		"configDiff", configDiff,
	)

	// We marshal the node pool CS builder config we are going to submit for cs node pool update for logging purposes
	nodePoolPayload, err := c.marshalClusterServiceNodePoolUpdatePayload(ctx, nodePool)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to marshal Cluster Service node pool update payload: %w", err))
	}

	logger.Info("dispatching node pool update to Cluster Service",
		"clusterServiceID", nodePoolCSID.String(),
		"clusterServiceNodePoolPayload", nodePoolPayload,
	)

	csNodePoolBuilder, err := c.buildNodePoolUpdateBuilder(ctx, nodePool)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build CS node pool builder: %w", err))
	}

	_, err = c.clusterServiceClient.UpdateNodePool(ctx, *nodePoolCSID, csNodePoolBuilder)
	if err != nil {
		var ocmError *ocmerrors.Error
		// XXX Matching an error message is brittle, but Clusters Service
		//     returns 400 Bad Request for a wide range of errors and there
		//     is no other information in the response to distinguish them.
		//
		//     If the error is indicating that a the node pool is not in
		//     an updatable state because the parent cluster is not in an updatable state,
		//     we return without error and retry again on the next sync. This can happen for
		//     example when the CS cluster is being updated.
		if errors.As(err, &ocmError) && ocmError.Status() == http.StatusBadRequest &&
			strings.Contains(ocmError.Reason(), "Node pools can only be") &&
			strings.Contains(ocmError.Reason(), "on clusters in an updatable state") &&
			strings.Contains(ocmError.Reason(), "cluster requested is in") &&
			strings.Contains(ocmError.Reason(), "state.") {
			logger.Info("Cluster Service rejected node pool update because the node pool's parent cluster is not updatable. Retrying on next sync.",
				"clusterServiceID", nodePoolCSID.String(),
				"error", err.Error(),
			)
			return nil
		}
		// XXX Matching an error message is brittle, but Clusters Service
		//     returns 400 Bad Request for a wide range of errors and there
		//     is no other information in the response to distinguish them.
		//
		//     If the error is indicating that a the node pool is not in
		//     an updatable state, we return without error and retry again on the
		//     next sync. This can happen for example when the CS node pool is still in
		//     the initial creation process.
		if errors.As(err, &ocmError) && ocmError.Status() == http.StatusBadRequest &&
			strings.Contains(ocmError.Reason(), "Node pool can only be updated in") &&
			strings.Contains(ocmError.Reason(), "the node pool requested is in") &&
			strings.Contains(ocmError.Reason(), "state.") {
			logger.Info("Cluster Service rejected node pool update because the node pool is not updatable. Retrying on next sync.",
				"clusterServiceID", nodePoolCSID.String(),
				"error", err.Error(),
			)
			return nil
		}
		return utils.TrackError(fmt.Errorf("failed to update cluster-service NodePool: %w", err))
	}

	logger.Info("dispatched node pool update to Cluster Service", "clusterServiceID", nodePoolCSID.String())
	return nil
}

// buildNodePoolUpdateBuilder builds the Cluster Service node pool PATCH payload from RP desired state.
func (c *nodePoolClusterServiceUpdateDispatchSyncer) buildNodePoolUpdateBuilder(ctx context.Context, nodePool *api.HCPOpenShiftClusterNodePool) (*arohcpv1alpha1.NodePoolBuilder, error) {
	return ocm.BuildCSNodePool(ctx, nodePool, true)
}

// marshalClusterServiceNodePoolUpdatePayload serializes the CS node pool PATCH body for logging.
func (c *nodePoolClusterServiceUpdateDispatchSyncer) marshalClusterServiceNodePoolUpdatePayload(ctx context.Context, nodePool *api.HCPOpenShiftClusterNodePool) (string, error) {
	builder, err := c.buildNodePoolUpdateBuilder(ctx, nodePool)
	if err != nil {
		return "", err
	}

	csNodePool, err := builder.Build()
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := arohcpv1alpha1.MarshalNodePool(csNodePool, &buf); err != nil {
		return "", err
	}

	return buf.String(), nil
}
