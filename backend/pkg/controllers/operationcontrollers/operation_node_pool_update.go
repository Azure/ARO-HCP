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

package operationcontrollers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/blang/semver/v4"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/lru"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/upgradecontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationNodePoolUpdate struct {
	clock                           utilsclock.PassiveClock
	resourcesDBClient               database.ResourcesDBClient
	clusterServiceClient            ocm.ClusterServiceClientSpec
	nodePoolLister                  listers.NodePoolLister
	serviceProviderNodePoolLister   listers.ServiceProviderNodePoolLister
	readDesireLister                dblisters.ReadDesireLister
	activeOperationsLister          listers.ActiveOperationLister
	notificationClient              *http.Client
	desiredVersionMismatchFirstSeen *lru.Cache
}

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
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	readDesireLister dblisters.ReadDesireLister,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
	backendInformers informers.BackendInformers,
) controllerutils.Controller {
	_, nodePoolLister := backendInformers.NodePools()
	_, serviceProviderNodePoolLister := backendInformers.ServiceProviderNodePools()
	_, activeOperationsLister := backendInformers.ActiveOperations()

	syncer := &operationNodePoolUpdate{
		clock:                           clock,
		resourcesDBClient:               resourcesDBClient,
		clusterServiceClient:            clusterServiceClient,
		nodePoolLister:                  nodePoolLister,
		serviceProviderNodePoolLister:   serviceProviderNodePoolLister,
		readDesireLister:                readDesireLister,
		activeOperationsLister:          activeOperationsLister,
		notificationClient:              notificationClient,
		desiredVersionMismatchFirstSeen: lru.New(100000),
	}

	controller := NewGenericOperationController(
		"OperationNodePoolUpdate",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *operationNodePoolUpdate) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestUpdate {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), api.NodePoolResourceType.String()) {
		return false
	}
	return true
}

func (c *operationNodePoolUpdate) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	operation, err := c.activeOperationsLister.Get(ctx, key.SubscriptionID, key.OperationName)
	if database.IsNotFoundError(err) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !c.ShouldProcess(ctx, operation) {
		return nil // no work to do
	}

	existingNodePool, err := c.nodePoolLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Parent.Name, operation.ExternalID.Name)
	if database.IsNotFoundError(err) {
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

	operationalState, err := c.determineOperationState(ctx, operation, existingNodePool)
	if err != nil {
		return utils.TrackError(err)
	}

	var persistErr *arm.CloudErrorBody
	if operationalState.ProvisioningState == arm.ProvisioningStateFailed {
		persistErr = &arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeInvalidRequestContent,
			Message: operationalState.Message,
		}
	}

	logger.Info("updating status")
	err = UpdateOperationStatus(ctx, c.clock, c.resourcesDBClient, operation, operationalState.ProvisioningState, persistErr, postAsyncNotificationFn(c.notificationClient))
	if database.IsPreconditionFailedError(err) {
		// if we have a conflict error, then we're guaranteed that our informer will eventually see an update and trigger us again.
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (c *operationNodePoolUpdate) shouldReconcileOperationAndResourceStatus(nodePool *api.HCPOpenShiftClusterNodePool) bool {
	return nodePool.ServiceProviderProperties.DeletionTimestamp == nil &&
		nodePool.ServiceProviderProperties.ClusterServiceID != nil
}

func (c *operationNodePoolUpdate) determineOperationState(ctx context.Context, operation *api.Operation, existingNodePool *api.HCPOpenShiftClusterNodePool) (*operationState, error) {
	logger := utils.LoggerFromContext(ctx)

	nodePoolCSID := existingNodePool.ServiceProviderProperties.ClusterServiceID
	existingCSNodePool, err := c.clusterServiceClient.GetNodePool(ctx, *nodePoolCSID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get node pool from cluster service: %w", err))
	}

	existingServiceProviderNodePool, err := c.serviceProviderNodePoolLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Parent.Name, operation.ExternalID.Name)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get service provider node pool from cache: %w", err))
	}

	errs := []error{}
	operationStates := []*operationState{}

	if operationState, err := c.desiredVersionResolutionOperationState(ctx, operation, existingNodePool, existingServiceProviderNodePool); err != nil {
		errs = append(errs, utils.TrackError(err))
	} else {
		operationStates = append(operationStates, operationState.withSource("nodePoolDesiredVersionResolution"))
	}
	if operationState, csErr := c.clusterServiceNodePoolStatusOperationState(ctx, operation, existingCSNodePool.Status()); csErr != nil {
		errs = append(errs, utils.TrackError(csErr))
	} else {
		operationStates = append(operationStates, operationState.withSource("clusterServiceNodePoolStatus"))
	}
	if operationState, csErr := c.clusterServiceNodePoolSpecOperationState(existingNodePool, existingCSNodePool); csErr != nil {
		errs = append(errs, utils.TrackError(csErr))
	} else {
		operationStates = append(operationStates, operationState.withSource("clusterServiceNodePoolSpec"))
	}

	if operationState, hsErr := c.hypershiftNodePoolOperationState(ctx, existingNodePool, existingCSNodePool); hsErr != nil {
		errs = append(errs, utils.TrackError(hsErr))
	} else {
		operationStates = append(operationStates, operationState.withSource("hypershiftNodePool"))
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	if len(operationStates) == 0 {
		return nil, errors.New("no operation states")
	}
	slices.SortStableFunc(operationStates, compareOperationState)
	if operationStates[0] == nil {
		return nil, errors.New("nil operation state")
	}
	logger.Info("determined node pool update operation status", "operationStates", operationStates)
	picked, err := pickWorstOperationState(operationStates)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	logger.Info("picked node pool update operation status", "provisioningState", picked.ProvisioningState, "message", picked.Message)
	return picked, nil
}

func (c *operationNodePoolUpdate) desiredVersionResolutionOperationState(ctx context.Context, operation *api.Operation, existingNodePool *api.HCPOpenShiftClusterNodePool, existingServiceProviderNodePool *api.ServiceProviderNodePool) (*operationState, error) {
	resultingDesiredVersion := existingServiceProviderNodePool.Spec.NodePoolVersion.DesiredVersion
	if resultingDesiredVersion == nil {
		return nil, utils.TrackError(fmt.Errorf("service provider node pool has no desired version"))
	}

	customerDesiredVersion := semver.MustParse(existingNodePool.Properties.Version.ID)

	operationID := strings.ToLower(operation.ResourceID.String())
	// If the operation is cancelled, its desiredVersionMismatchFirstSeen entry is never
	// explicitly removed. This is safe because operation.ResourceID is unique per operation,
	// so stale entries won't cause false matches for newer operations and will eventually
	// be evicted by the LRU.
	if customerDesiredVersion.EQ(*resultingDesiredVersion) {
		c.desiredVersionMismatchFirstSeen.Remove(operationID)
		return newOperationState(arm.ProvisioningStateSucceeded, ""), nil
	}

	nodePoolKey := controllerutils.HCPNodePoolKey{
		SubscriptionID:    operation.ExternalID.SubscriptionID,
		ResourceGroupName: operation.ExternalID.ResourceGroupName,
		HCPClusterName:    operation.ExternalID.Parent.Name,
		HCPNodePoolName:   operation.ExternalID.Name,
	}

	controllerCRUD := c.resourcesDBClient.HCPClusters(nodePoolKey.SubscriptionID, nodePoolKey.ResourceGroupName).NodePools(nodePoolKey.HCPClusterName).Controllers(nodePoolKey.HCPNodePoolName)
	controllerDoc, getControllerErr := controllerCRUD.Get(ctx, upgradecontrollers.NodepoolVersionControllerName)
	if getControllerErr != nil {
		return nil, utils.TrackError(getControllerErr)
	}

	intentFailedCondition := apimeta.FindStatusCondition(controllerDoc.Status.Conditions, api.ControllerConditionTypeIntentFailed)

	if intentFailedCondition == nil {
		return newOperationState(arm.ProvisioningStateAccepted, "customer desired version not yet calculated"), nil
	}
	// Customer desired version differs from the service provider resolved version, and the
	// NodePoolVersion controller has not yet set IntentFailed (VersionUpgradeNotAccepted)
	// for this version. Stay Accepted while resolution runs; fail once elapsed exceeds
	// 129s from the first time this process observed the mismatch for this operation.
	// This avoids immediately failing long-running operations after controller restarts
	// and is double the relistDuration of the nodepool and serviceProviderNodePool informers.
	// This will not solve all the edge cases, but it will give enough time to the other controllers to act.
	if intentFailedCondition.Status != metav1.ConditionTrue || intentFailedCondition.Reason != api.VersionUpgradeNotAcceptedReason {
		pending := newOperationState(arm.ProvisioningStateAccepted, "customer desired version does not match resolved desired version")
		firstSeen, ok := c.desiredVersionMismatchFirstSeen.Get(operationID)
		if !ok {
			c.desiredVersionMismatchFirstSeen.Add(operationID, c.clock.Now())
			return pending, nil
		}
		if c.clock.Since(firstSeen.(time.Time)) <= 129*time.Second {
			return pending, nil
		}
		msg := fmt.Sprintf(
			"timed out after 129s waiting for resolution of desired version from '%s' node pool version",
			existingNodePool.Properties.Version.ID,
		)
		c.desiredVersionMismatchFirstSeen.Remove(operationID)
		return newOperationState(arm.ProvisioningStateFailed, msg), nil
	}
	c.desiredVersionMismatchFirstSeen.Remove(operationID)
	return newOperationState(arm.ProvisioningStateFailed, intentFailedCondition.Message), nil
}

func (c *operationNodePoolUpdate) clusterServiceNodePoolStatusOperationState(ctx context.Context, operation *api.Operation, existingCSNodePoolStatus *arohcpv1alpha1.NodePoolStatus) (*operationState, error) {
	logger := utils.LoggerFromContext(ctx)
	newOperationStatus, opError, err := convertNodePoolStatus(operation, existingCSNodePoolStatus)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	logger.Info("new status via cluster-service", "newStatus", newOperationStatus, "newOperationError", opError)
	msg := ""
	if opError != nil {
		msg = opError.Message
	}
	return newOperationState(newOperationStatus, msg), nil
}
