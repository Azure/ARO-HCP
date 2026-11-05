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
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationExternalAuthDelete struct {
	clock                utilsclock.PassiveClock
	resourcesDBClient    corecosmosstorage.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
	notificationClient   *http.Client
}

const OperationExternalAuthDeleteControllerName = "OperationExternalAuthDelete"

// NewOperationExternalAuthDeleteController returns a new Controller instance that
// follows an asynchronous external auth deletion operation to completion and updates
// the corresponding operation document in Cosmos DB.
//
// The controller has the following responsibilities:
//   - while the ExternalAuth Cosmos document is present, it reconciles the
//     operation status. When the ExternalAuth Cosmos document is deleted
//     (by the externalAuthDeletionController), it marks the operation as
//     Succeeded.
//
// Note that "to completion" does not imply success. An operation is considered
// complete when its status field reaches what Azure defines as a terminal value;
// any of "Succeeded", "Failed", or "Canceled". Once the operation status reaches
// a terminal value, there will be no further updates to the operation document.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters/externalAuths
//	     Request: Delete
//	      Status: any non-terminal value
func NewOperationExternalAuthDeleteController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &operationExternalAuthDelete{
		clock:                clock,
		resourcesDBClient:    resourcesDBClient,
		clusterServiceClient: clusterServiceClient,
		notificationClient:   notificationClient,
	}

	controller := controllerutils.NewGenericOperationController(
		OperationExternalAuthDeleteControllerName,
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *operationExternalAuthDelete) ShouldProcess(ctx context.Context, operation *coreapi.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != cosmosstorageutils.OperationRequestDelete {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), coreapi.ExternalAuthResourceType.String()) {
		return false
	}
	return true
}

func (c *operationExternalAuthDelete) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	operation, err := c.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}

	if !c.ShouldProcess(ctx, operation) {
		return nil // no work to do
	}

	externalAuthCRUD := c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).ExternalAuth(operation.ExternalID.Parent.Name)
	externalAuth, err := externalAuthCRUD.Get(ctx, operation.ExternalID.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.Info("external auth document deleted - completing operation")
		err = operationbase.SetDeleteOperationAsCompleted(ctx, c.clock, c.resourcesDBClient, operation, operationbase.PostAsyncNotificationFn(c.notificationClient))
		if err != nil {
			return utils.TrackError(err)
		}
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get external auth: %w", err))
	}

	if operation.ResourceID.Name != externalAuth.ServiceProviderProperties.ActiveOperationID {
		logger.Info("external auth active operation id mismatch, returning early", "synchronizedActiveOperationID", operation.ResourceID.Name, "externalAuthActiveOperationID", externalAuth.ServiceProviderProperties.ActiveOperationID)
		return nil
	}

	state, observationErr := c.externalAuthDeletionOperationState(ctx, operation, externalAuth)
	var states []*operationbase.OperationState
	if state != nil {
		states = append(states, state.WithSource("externalAuthDeletion"))
	}
	state, observationErr = operationbase.SummarizeOperationStates(states, observationErr)
	var logValues []any
	if state != nil {
		logValues = append(logValues, "provisioningState", state.ProvisioningState, "message", state.Message)
	}
	if observationErr != nil {
		logValues = append(logValues, "error", observationErr)
	}
	logger.Info("summarized operation state", logValues...)
	state, err = operationbase.ApplyOperationDeadline(state, observationErr, externalAuth.ServiceProviderProperties.DeleteOperationCompletionDeadline, c.clock.Now(), "External auth deletion did not complete within the allowed time.")
	if err != nil {
		return utils.TrackError(err)
	}
	persistErr := state.Error
	if state.ProvisioningState == coreapi.ProvisioningStateFailed && persistErr == nil {
		persistErr = &coreapi.CloudErrorBody{Code: state.CloudErrorCode, Message: state.Message}
	}
	err = operationbase.UpdateOperationStatus(ctx, c.clock, c.resourcesDBClient, operation, state.ProvisioningState, persistErr, operationbase.PostAsyncNotificationFn(c.notificationClient))
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	return utils.TrackError(err)
}

func (c *operationExternalAuthDelete) shouldReconcileOperationAndResourceStatus(externalAuth *coreapi.ExternalAuth) bool {
	return externalAuth.ServiceProviderProperties.DeletionTimestamp != nil &&
		externalAuth.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil &&
		externalAuth.ServiceProviderProperties.ClusterServiceID != nil
}

func (c *operationExternalAuthDelete) externalAuthDeletionOperationState(ctx context.Context, operation *coreapi.Operation, externalAuth *coreapi.ExternalAuth) (*operationbase.OperationState, error) {
	logger := utils.LoggerFromContext(ctx)
	if !c.shouldReconcileOperationAndResourceStatus(externalAuth) {
		return operationbase.NewOperationState(operation.Status, "Waiting for external auth deletion prerequisites"), nil
	}

	csID := externalAuth.ServiceProviderProperties.ClusterServiceID

	csCtx, cancel := context.WithTimeout(ctx, controllerutils.CSReadTimeout)
	defer cancel()

	csExternalAuth, err := c.clusterServiceClient.GetExternalAuth(csCtx, *csID)
	if err != nil {
		var ocmError *ocmerrors.Error
		if !errors.As(err, &ocmError) || ocmError.Status() != http.StatusNotFound {
			return nil, utils.TrackError(fmt.Errorf("failed to get cluster-service ExternalAuth: %w", ocm.StripVolatileErrorFields(err)))
		}
		// 404 - CS has finished deleting. externalAuthClusterServiceIDClearer will clear the ID.
		logger.Info("cluster-service ExternalAuth gone - skipping operation update", "clusterServiceID", csID.String())
		return operationbase.NewOperationState(operation.Status, "Waiting for external auth document deletion"), nil
	}

	csExternalAuthStatus := csExternalAuth.Status()

	// If the external auth is in the Ready state from CS side, we wait until the Cosmos ExternalAuth document is deleted, which
	// will be picked up by a next reconciliation of this controller and we will update the operation to Succeeded.
	if csExternalAuthStatus.State().Value() == string(operationbase.ExternalAuthStateReady) {
		logger.Info("cluster-service ExternalAuth in Ready state. Waiting until Cosmos ExternalAuth document is deleted.")
		return operationbase.NewOperationState(operation.Status, "Waiting for external auth document deletion"), nil
	}

	newOperationStatus, newOperationError, err := operationbase.ConvertExternalAuthStatus(operation, csExternalAuthStatus)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if newOperationError != nil {
		state := operationbase.NewOperationState(newOperationStatus, newOperationError.Message).WithCloudErrorCode(newOperationError.Code)
		state.Error = newOperationError
		return state, nil
	}
	return operationbase.NewOperationState(newOperationStatus, "Waiting for external auth deletion"), nil
}
