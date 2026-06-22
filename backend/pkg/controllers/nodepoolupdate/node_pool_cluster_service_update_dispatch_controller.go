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

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type nodePoolClusterServiceUpdateDispatchSyncer struct {
	cooldownChecker      controllerutil.CooldownChecker
	nodePoolLister       listers.NodePoolLister
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.NodePoolSyncer = (*nodePoolClusterServiceUpdateDispatchSyncer)(nil)

func NewNodePoolClusterServiceUpdateDispatchController(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
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
		kubeApplierInformers,
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
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		nodePoolLister:       nodePoolLister,
		resourcesDBClient:    resourcesDBClient,
		clusterServiceClient: clusterServiceClient,
	}
}

func nodePoolShouldProceed(nodePool *api.HCPOpenShiftClusterNodePool) bool {
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

	cachedNodePool, err := c.nodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get node pool from cache: %w", err))
	}
	if !nodePoolShouldProceed(cachedNodePool) {
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
	if !nodePoolShouldProceed(nodePool) {
		return nil
	}

	nodePoolCSID := nodePool.ServiceProviderProperties.ClusterServiceID
	csNodePool, err := c.clusterServiceClient.GetNodePool(ctx, *nodePoolCSID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get node pool from Cluster Service: %w", err))
	}

	needsUpdate, err := ocm.NodePoolUpdateDispatchConfigDiffers(nodePool, csNodePool)
	if err != nil {
		return err
	}
	if !needsUpdate {
		return nil
	}

	desiredConfigJSON, actualConfigJSON, err := ocm.NodePoolUpdateDispatchConfigDiffJSON(nodePool, csNodePool)
	if err != nil {
		return err
	}

	logger.Info("node pool update dispatch config differs between RP and CS",
		"clusterServiceID", nodePoolCSID.String(),
		"desiredConfig", desiredConfigJSON,
		"actualConfig", actualConfigJSON,
	)

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

	logger.Info("requested cluster-service NodePool update", "clusterServiceID", nodePoolCSID.String())
	return nil
}

func (c *nodePoolClusterServiceUpdateDispatchSyncer) buildNodePoolUpdateBuilder(ctx context.Context, nodePool *api.HCPOpenShiftClusterNodePool) (*arohcpv1alpha1.NodePoolBuilder, error) {
	return ocm.BuildCSNodePool(ctx, nodePool, true)
}

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
