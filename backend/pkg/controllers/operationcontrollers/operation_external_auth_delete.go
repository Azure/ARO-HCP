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
	"strings"
	"time"

	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationExternalAuthDelete struct {
	clock                utilsclock.PassiveClock
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
	notificationClient   *http.Client
}

// NewOperationExternalAuthDeleteController returns a new Controller instance that
// follows an asynchronous external auth deletion operation to completion and updates
// the corresponding operation document in Cosmos DB.
//
// The controller has the following responsibilities:
//   - For legacy operations (UsesNewExternalAuthDeletionApproach == false):
//     it polls Cluster Service until the external auth is gone (404), then
//     marks the operation as Succeeded and cleans up the Cosmos document.
//   - For new-approach operations (UsesNewExternalAuthDeletionApproach == true):
//     while the ExternalAuth Cosmos document is present, it reconciles the
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
	resourcesDBClient database.ResourcesDBClient,
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

	controller := NewGenericOperationController(
		"OperationExternalAuthDelete",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *operationExternalAuthDelete) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestDelete {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), api.ExternalAuthResourceType.String()) {
		return false
	}
	return true
}

func (c *operationExternalAuthDelete) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	operation, err := c.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsNotFoundError(err) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}

	// TODO remove this once migration of external auth deletion from frontend to backend is fully completed.
	if !operation.UsesNewExternalAuthDeletionApproach {
		return c.legacySynchronizeOperation(ctx, operation)
	}

	// From here, we know it uses the new deletion approach.

	if !c.ShouldProcess(ctx, operation) {
		return nil // no work to do
	}

	externalAuthCRUD := c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).ExternalAuth(operation.ExternalID.Parent.Name)
	externalAuth, err := externalAuthCRUD.Get(ctx, operation.ExternalID.Name)
	if database.IsNotFoundError(err) {
		logger.Info("external auth document deleted - completing operation")
		err = SetDeleteOperationAsCompleted(ctx, c.clock, c.resourcesDBClient, operation, postAsyncNotificationFn(c.notificationClient))
		if err != nil {
			return utils.TrackError(err)
		}
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get external auth: %w", err))
	}

	if !c.shouldReconcileOperationAndResourceStatus(externalAuth) {
		return nil
	}
	err = c.reconcileOperationAndResourceStatus(ctx, operation, externalAuth)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (c *operationExternalAuthDelete) shouldReconcileOperationAndResourceStatus(externalAuth *api.HCPOpenShiftClusterExternalAuth) bool {
	return externalAuth.ServiceProviderProperties.DeletionTimestamp != nil &&
		externalAuth.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil &&
		externalAuth.ServiceProviderProperties.ClusterServiceID != nil
}

func (c *operationExternalAuthDelete) reconcileOperationAndResourceStatus(ctx context.Context, operation *api.Operation, externalAuth *api.HCPOpenShiftClusterExternalAuth) error {
	logger := utils.LoggerFromContext(ctx)

	csID := externalAuth.ServiceProviderProperties.ClusterServiceID

	csExternalAuth, err := c.clusterServiceClient.GetExternalAuth(ctx, *csID)
	if err != nil {
		var ocmError *ocmerrors.Error
		if !errors.As(err, &ocmError) || ocmError.Status() != http.StatusNotFound {
			return utils.TrackError(fmt.Errorf("failed to get cluster-service ExternalAuth: %w", err))
		}
		// 404 - CS has finished deleting. externalAuthClusterServiceIDClearer will clear the ID.
		logger.Info("cluster-service ExternalAuth gone - skipping operation update", "clusterServiceID", csID.String())
		return nil
	}

	csExternalAuthStatus := csExternalAuth.Status()

	// If the external auth is in the Ready state from CS side, we wait until the Cosmos ExternalAuth document is deleted, which
	// will be picked up by a next reconciliation of this controller and we will update the operation to Succeeded.
	if csExternalAuthStatus.State().Value() == string(ExternalAuthStateReady) {
		logger.Info("cluster-service ExternalAuth in Ready state. Waiting until Cosmos ExternalAuth document is deleted.")
		return nil
	}

	newOperationStatus, newOperationError, err := convertExternalAuthStatus(operation, csExternalAuthStatus)
	if err != nil {
		return utils.TrackError(err)
	}

	err = UpdateOperationStatus(ctx, c.clock, c.resourcesDBClient, operation, newOperationStatus, newOperationError, postAsyncNotificationFn(c.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
