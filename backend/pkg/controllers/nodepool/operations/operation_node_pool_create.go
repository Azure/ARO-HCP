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

	"github.com/blang/semver/v4"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	nodepoolversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/version"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationNodePoolCreate struct {
	clock                         utilsclock.PassiveClock
	resourcesDBClient             corecosmosstorage.ResourcesDBClient
	activeOperationsLister        corelisters.ActiveOperationLister
	nodePoolLister                corelisters.NodePoolLister
	serviceProviderNodePoolLister corelisters.ServiceProviderNodePoolLister
	readDesireLister              kubeapplierlisters.ReadDesireLister
	clusterServiceClient          ocm.ClusterServiceClientSpec
	notificationClient            *http.Client
}

const OperationNodePoolCreateControllerName = "OperationNodePoolCreate"

// NewOperationNodePoolCreateController returns a new Controller instance that
// follows an asynchronous node pool creation operation to completion and updates
// the corresponding operation document in Cosmos DB.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools
//	     Request: Create
//	      Status: any non-terminal value
//
// Note that "to completion" does not imply success. An operation is considered
// complete when its status field reaches what Azure defines as a terminal value;
// any of "Succeeded", "Failed", or "Canceled". Once the operation status reaches
// a terminal value, there will be no further updates to the operation document.
func NewOperationNodePoolCreateController(
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

	syncer := &operationNodePoolCreate{
		clock:                         clock,
		resourcesDBClient:             resourcesDBClient,
		nodePoolLister:                nodePoolLister,
		serviceProviderNodePoolLister: serviceProviderNodePoolLister,
		readDesireLister:              readDesireLister,
		activeOperationsLister:        activeOperationsLister,
		clusterServiceClient:          clusterServiceClient,
		notificationClient:            notificationClient,
	}

	controller := controllerutils.NewGenericOperationController(
		OperationNodePoolCreateControllerName,
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *operationNodePoolCreate) ShouldProcess(ctx context.Context, operation *coreapi.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != cosmosstorageutils.OperationRequestCreate {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), coreapi.NodePoolResourceType.String()) {
		return false
	}

	return true
}

func (c *operationNodePoolCreate) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
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

	nodePool, err := c.nodePoolLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Parent.Name, operation.ExternalID.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.Info("node pool not found in cache, waiting")
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get node pool: %w", err)
	}

	if operation.ResourceID.Name != nodePool.ServiceProviderProperties.ActiveOperationID {
		logger.Info("node pool active operation id mismatch, returning early", "synchronizedActiveOperationID", operation.ResourceID.Name, "nodePoolActiveOperationID", nodePool.ServiceProviderProperties.ActiveOperationID)
		return nil
	}

	if !c.shouldReconcileOperationAndResourceStatus(nodePool) {
		return nil
	}

	operationalState, observationErr := c.determineOperationState(ctx, operation, nodePool)
	operationalState, err = operationbase.ApplyOperationDeadline(operationalState, observationErr, nodePool.ServiceProviderProperties.CreateOperationCompletionDeadline, c.clock.Now(), "node pool creation did not complete before the deadline")
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

func (c *operationNodePoolCreate) shouldReconcileOperationAndResourceStatus(nodePool *coreapi.NodePool) bool {
	return nodePool.ServiceProviderProperties.DeletionTimestamp == nil
}

func (c *operationNodePoolCreate) determineOperationState(ctx context.Context, operation *coreapi.Operation, nodePool *coreapi.NodePool) (*operationbase.OperationState, error) {
	logger := utils.LoggerFromContext(ctx)

	var errs []error
	var operationStates []*operationbase.OperationState

	// Check desired version resolution state first - if validation failed, fail the operation early
	existingServiceProviderNodePool, err := c.serviceProviderNodePoolLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Parent.Name, operation.ExternalID.Name)
	if err != nil {
		errs = append(errs, utils.TrackError(fmt.Errorf("failed to get service provider node pool from cache: %w", err)))
	} else {
		if operationState, err := c.desiredVersionResolutionOperationState(ctx, operation, nodePool, existingServiceProviderNodePool); err != nil {
			errs = append(errs, utils.TrackError(err))
		} else {
			operationStates = append(operationStates, operationState.WithSource("nodePoolDesiredVersionResolution"))
		}
	}

	if state, err := c.nodePoolServiceCreateOperationState(ctx, operation, nodePool); err != nil {
		errs = append(errs, utils.TrackError(err))
	} else {
		operationStates = append(operationStates, state.WithSource("clusterServiceNodePoolStatus"))
	}
	if state, err := c.hypershiftNodePoolOperationState(ctx, nodePool); err != nil {
		errs = append(errs, utils.TrackError(err))
	} else {
		operationStates = append(operationStates, state.WithSource("hypershiftNodePool"))
	}

	logger.Info("determined node pool create operation status", "operationStates", operationStates)
	state, err := operationbase.SummarizeOperationStates(operationStates, errs...)
	var logValues []any
	if state != nil {
		logValues = append(logValues, "provisioningState", state.ProvisioningState, "message", state.Message)
	}
	if err != nil {
		logValues = append(logValues, "error", err)
	}
	logger.Info("summarized operation state", logValues...)
	return state, err
}

func (c *operationNodePoolCreate) desiredVersionResolutionOperationState(ctx context.Context, operation *coreapi.Operation, existingNodePool *coreapi.NodePool, existingServiceProviderNodePool *coreapi.ServiceProviderNodePool) (*operationbase.OperationState, error) {
	logger := utils.LoggerFromContext(ctx)

	// Get the NodePoolVersion controller document to check IntentFailed condition
	nodePoolKey := controllerutils.HCPNodePoolKey{
		SubscriptionID:    operation.ExternalID.SubscriptionID,
		ResourceGroupName: operation.ExternalID.ResourceGroupName,
		HCPClusterName:    operation.ExternalID.Parent.Name,
		HCPNodePoolName:   operation.ExternalID.Name,
	}

	controllerCRUD := c.resourcesDBClient.HCPClusters(nodePoolKey.SubscriptionID, nodePoolKey.ResourceGroupName).NodePools(nodePoolKey.HCPClusterName).Controllers(nodePoolKey.HCPNodePoolName)
	controllerDoc, getControllerErr := controllerCRUD.Get(ctx, nodepoolversion.NodepoolVersionControllerName)
	if getControllerErr != nil {
		return nil, utils.TrackError(getControllerErr)
	}

	intentFailedCondition := apimeta.FindStatusCondition(controllerDoc.Status.Conditions, coreapi.ControllerConditionTypeIntentFailed)

	// On create, the resulting desired version in the service provider node pool is not initially set.
	// The NodePoolVersion controller sets this based on the customer desired version upon successful validation.
	// If the resulting desired version is nil, it means the NodePoolVersion controller either hasn't completed
	// processing the version or the version validation failed.
	resultingDesiredVersion := existingServiceProviderNodePool.Spec.NodePoolVersion.DesiredVersion
	if resultingDesiredVersion == nil {
		// Check if version validation failed
		if intentFailedCondition != nil && intentFailedCondition.Status == metav1.ConditionTrue &&
			intentFailedCondition.Reason == coreapi.VersionUpgradeNotAcceptedReason {
			return operationbase.NewFailedOperationState(coreapi.CloudErrorCodeInvalidRequestContent, intentFailedCondition.Message, nil), nil
		}
		// Otherwise, stay in an Accepted state while the NodePoolVersion controller is still processing the version.
		return operationbase.NewOperationState(coreapi.ProvisioningStateAccepted, "Waiting for version to be accepted"), nil
	}

	// Sanity check: the resulting desired version should match the customer desired version
	customerDesiredVersion := semver.MustParse(existingNodePool.Properties.Version.ID)
	if !customerDesiredVersion.EQ(*resultingDesiredVersion) {
		logger.Error(fmt.Errorf("desired version mismatch on create"),
			"customerDesiredVersion", customerDesiredVersion.String(),
			"resultingDesiredVersion", resultingDesiredVersion.String(),
		)
		return operationbase.NewFailedOperationState(
			coreapi.CloudErrorCodeInternalServerError,
			fmt.Sprintf("Could not resolve version %s", customerDesiredVersion.String()),
			nil), nil
	}

	return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
}

func (c *operationNodePoolCreate) nodePoolServiceCreateOperationState(ctx context.Context, operation *coreapi.Operation, nodePool *coreapi.NodePool) (*operationbase.OperationState, error) {
	if id := nodePool.ServiceProviderProperties.ClusterServiceID; id == nil || id.String() == "" {
		return operationbase.NewOperationState(coreapi.ProvisioningStateAccepted, "Waiting for Cluster Service node pool creation"), nil
	}
	logger := utils.LoggerFromContext(ctx)
	csCtx, cancel := context.WithTimeout(ctx, controllerutils.CSReadTimeout)
	defer cancel()

	csNodePoolStatus, err := c.clusterServiceClient.GetNodePoolStatus(csCtx, *nodePool.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	newOperationStatus, newOperationError, err := operationbase.ConvertNodePoolStatus(operation, csNodePoolStatus)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	logger.Info("new status via cluster-service", "newStatus", newOperationStatus, "newOperationError", newOperationError)
	state := operationbase.NewOperationState(newOperationStatus, operationbase.NodePoolServiceOperationMessage(csNodePoolStatus, newOperationError))
	if newOperationError != nil {
		state.WithCloudErrorCode(newOperationError.Code)
	}
	return state, nil
}
