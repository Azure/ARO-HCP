// Copyright 2025 Microsoft Corporation
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

package operations

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/lru"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationNodePoolUpdate struct {
	statusCalculators               operationbase.OperationStatusCalculators[*arohcpv1alpha1.NodePool]
	clock                           utilsclock.PassiveClock
	resourcesDBClient               corecosmosstorage.ResourcesDBClient
	clusterServiceClient            ocm.ClusterServiceClientSpec
	nodePoolLister                  corelisters.NodePoolLister
	serviceProviderNodePoolLister   corelisters.ServiceProviderNodePoolLister
	readDesireLister                kubeapplierlisters.ReadDesireLister
	activeOperationsLister          corelisters.ActiveOperationLister
	notificationClient              *http.Client
	desiredVersionMismatchFirstSeen *lru.Cache
}

const OperationNodePoolUpdateControllerName = "OperationNodePoolUpdate"

// NewOperationNodePoolUpdateController returns a new Controller instance that
// follows an asynchronous node pool update operation to completion and updates
// the corresponding operation document in Cosmos DB.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools
//	     Request: Update
//	      Status: any non-terminal value
//
// Note that "to completion" does not imply success. An operation is considered
// complete when its status field reaches what Azure defines as a terminal value;
// any of "Succeeded", "Failed", or "Canceled". Once the operation status reaches
// a terminal value, there will be no further updates to the operation document.
func NewOperationNodePoolUpdateController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	readDesireLister kubeapplierlisters.ReadDesireLister,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
	backendInformers coreinformers.BackendInformers,
) controllerutils.Controller {
	_, nodePoolLister := backendInformers.NodePools()
	_, serviceProviderNodePoolLister := backendInformers.ServiceProviderNodePools()
	_, activeOperationsLister := backendInformers.ActiveOperations()

	desiredVersionMismatchFirstSeen := lru.New(100000)

	syncer := &operationNodePoolUpdate{
		clock:                           clock,
		resourcesDBClient:               resourcesDBClient,
		clusterServiceClient:            clusterServiceClient,
		nodePoolLister:                  nodePoolLister,
		serviceProviderNodePoolLister:   serviceProviderNodePoolLister,
		readDesireLister:                readDesireLister,
		activeOperationsLister:          activeOperationsLister,
		notificationClient:              notificationClient,
		desiredVersionMismatchFirstSeen: desiredVersionMismatchFirstSeen,
	}

	// The registry is fixed in code; invalid source registrations are startup errors.
	syncer.statusCalculators = metadataapi.Must(operationbase.NewOperationStatusCalculators[*arohcpv1alpha1.NodePool](
		&nodePoolUpdateDesiredVersionCheck{nodePoolLister: nodePoolLister, serviceProviderNodePoolLister: serviceProviderNodePoolLister, clock: clock, resourcesDBClient: resourcesDBClient, desiredVersionMismatchFirstSeen: desiredVersionMismatchFirstSeen},
		&nodePoolUpdateClusterServiceStatusCheck{},
		&nodePoolUpdateClusterServiceSpecCheck{nodePoolLister: nodePoolLister},
		&nodePoolUpdateHypershiftCheck{nodePoolLister: nodePoolLister, readDesireLister: readDesireLister},
	))

	controller := controllerutils.NewGenericOperationController(
		OperationNodePoolUpdateControllerName,
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *operationNodePoolUpdate) ShouldProcess(ctx context.Context, operation *coreapi.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != cosmosstorageutils.OperationRequestUpdate {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), coreapi.NodePoolResourceType.String()) {
		return false
	}
	return true
}

func (c *operationNodePoolUpdate) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	operation, err := c.activeOperationsLister.Get(ctx, key.SubscriptionID, key.OperationName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !c.ShouldProcess(ctx, operation) {
		return nil // no work to do
	}

	existingNodePool, err := c.nodePoolLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Parent.Name, operation.ExternalID.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.Info("node pool not found in cache, waiting")
		return nil // no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get node pool: %w", err))
	}

	if operation.ResourceID.Name != existingNodePool.ServiceProviderProperties.ActiveOperationID {
		logger.Info("node pool active operation id mismatch, returning early", "synchronizedActiveOperationID", operation.ResourceID.Name, "nodePoolActiveOperationID", existingNodePool.ServiceProviderProperties.ActiveOperationID)
		return nil
	}

	if !c.shouldReconcileOperationAndResourceStatus(existingNodePool) {
		return nil // no work to do
	}

	csResource, err := c.clusterServiceClient.GetNodePool(ctx, *existingNodePool.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get node pool from cluster service: %w", err))
	}

	operationalState, err := c.statusCalculators.CalculateOperationStatus(ctx, operation, csResource)
	if err != nil {
		return utils.TrackError(err)
	}

	var persistErr *coreapi.CloudErrorBody
	if operationalState.ProvisioningState == coreapi.ProvisioningStateFailed {
		persistErr = &coreapi.CloudErrorBody{
			Code:    operationalState.CloudErrorCode,
			Message: operationalState.Message,
		}
	}

	if !operationalState.ProvisioningState.IsTerminal() &&
		existingNodePool.ServiceProviderProperties.UpdateOperationCompletionDeadline != nil &&
		c.clock.Now().After(existingNodePool.ServiceProviderProperties.UpdateOperationCompletionDeadline.Time) {
		message := operationbase.DeadlineExceededMessage(
			"node pool update did not complete before the deadline",
			operationalState.Message,
		)
		logger.Info("update operation deadline exceeded, marking as failed",
			"deadline", existingNodePool.ServiceProviderProperties.UpdateOperationCompletionDeadline.Time,
			"message", message)
		operationalState.ProvisioningState = coreapi.ProvisioningStateFailed
		code := operationalState.CloudErrorCode
		if code == coreapi.CloudErrorCodeInternalServerError {
			code = coreapi.CloudErrorCodeDeadlineExceeded
		}
		persistErr = &coreapi.CloudErrorBody{
			Code:    code,
			Message: message,
		}
	}

	logger.Info("updating status")
	err = operationbase.UpdateOperationStatus(ctx, c.clock, c.resourcesDBClient, operation, operationalState.ProvisioningState, persistErr, operationbase.PostAsyncNotificationFn(c.notificationClient))
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		// if we have a conflict error, then we're guaranteed that our informer will eventually see an update and trigger us again.
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (c *operationNodePoolUpdate) shouldReconcileOperationAndResourceStatus(nodePool *coreapi.NodePool) bool {
	return nodePool.ServiceProviderProperties.DeletionTimestamp == nil &&
		nodePool.ServiceProviderProperties.ClusterServiceID != nil
}
