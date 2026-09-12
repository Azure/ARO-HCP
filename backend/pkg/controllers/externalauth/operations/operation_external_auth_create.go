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
	"slices"
	"strings"
	"time"

	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationExternalAuthCreate struct {
	clock                  utilsclock.PassiveClock
	resourcesDBClient      corecosmosstorage.ResourcesDBClient
	activeOperationsLister corelisters.ActiveOperationLister
	externalAuthLister     corelisters.ExternalAuthLister
	clusterServiceClient   ocm.ClusterServiceClientSpec
	notificationClient     *http.Client
}

// NewOperationExternalAuthCreateController returns a new Controller instance that
// follows an asynchronous external auth creation operation to completion and updates
// the corresponding operation document in Cosmos DB.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters/externalAuths
//	     Request: Create
//	      Status: any non-terminal value
//
// Note that "to completion" does not imply success. An operation is considered
// complete when its status field reaches what Azure defines as a terminal value;
// any of "Succeeded", "Failed", or "Canceled". Once the operation status reaches
// a terminal value, there will be no further updates to the operation document.
func NewOperationExternalAuthCreateController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
	backendInformers coreinformers.BackendInformers,
) controllerutils.Controller {
	_, externalAuthLister := backendInformers.ExternalAuths()
	_, activeOperationsLister := backendInformers.ActiveOperations()

	syncer := &operationExternalAuthCreate{
		clock:                  clock,
		resourcesDBClient:      resourcesDBClient,
		externalAuthLister:     externalAuthLister,
		activeOperationsLister: activeOperationsLister,
		clusterServiceClient:   clusterServiceClient,
		notificationClient:     notificationClient,
	}

	controller := controllerutils.NewGenericOperationController(
		"OperationExternalAuthCreate",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *operationExternalAuthCreate) ShouldProcess(ctx context.Context, operation *coreapi.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != cosmosstorageutils.OperationRequestCreate {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), coreapi.ExternalAuthResourceType.String()) {
		return false
	}
	return true
}

func (c *operationExternalAuthCreate) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
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

	externalAuth, err := c.externalAuthLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Parent.Name, operation.ExternalID.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.Info("external auth not found in cache, waiting")
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get external auth: %w", err)
	}

	if operation.ResourceID.Name != externalAuth.ServiceProviderProperties.ActiveOperationID {
		logger.Info("external auth active operation id mismatch, returning early", "synchronizedActiveOperationID", operation.ResourceID.Name, "externalAuthActiveOperationID", externalAuth.ServiceProviderProperties.ActiveOperationID)
		return nil
	}

	if !c.shouldReconcileOperationAndResourceStatus(externalAuth) {
		return nil
	}

	operationalState, err := c.determineOperationState(ctx, externalAuth)
	if err != nil {
		return utils.TrackError(err)
	}

	var persistErr *coreapi.CloudErrorBody
	if operationalState.ProvisioningState == coreapi.ProvisioningStateFailed {
		persistErr = &coreapi.CloudErrorBody{
			Code:    coreapi.CloudErrorCodeForMessage(operationalState.Message, coreapi.CloudErrorCodeInternalServerError),
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

func (c *operationExternalAuthCreate) shouldReconcileOperationAndResourceStatus(externalAuth *coreapi.HCPOpenShiftClusterExternalAuth) bool {
	return externalAuth.ServiceProviderProperties.DeletionTimestamp == nil && externalAuth.ServiceProviderProperties.ClusterServiceID != nil
}

func (c *operationExternalAuthCreate) determineOperationState(ctx context.Context, externalAuth *coreapi.HCPOpenShiftClusterExternalAuth) (*operationbase.OperationState, error) {
	logger := utils.LoggerFromContext(ctx)

	var errs []error
	var operationStates []*operationbase.OperationState

	if state, err := c.externalAuthClusterServiceCreateOperationState(ctx, externalAuth); err != nil {
		errs = append(errs, utils.TrackError(err))
	} else {
		operationStates = append(operationStates, state.WithSource("externalAuthClusterServiceCreate"))
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	if len(operationStates) == 0 {
		return nil, errors.New("no operation states")
	}
	slices.SortStableFunc(operationStates, operationbase.CompareOperationState)
	if operationStates[0] == nil {
		return nil, errors.New("nil operation state")
	}
	logger.Info("determined external auth create operation status", "operationStates", operationStates)
	picked, err := operationbase.PickWorstOperationState(operationStates)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	logger.Info("picked external auth create operation status", "provisioningState", picked.ProvisioningState, "message", picked.Message)
	return picked, nil
}

func (c *operationExternalAuthCreate) externalAuthClusterServiceCreateOperationState(ctx context.Context, externalAuth *coreapi.HCPOpenShiftClusterExternalAuth) (*operationbase.OperationState, error) {
	logger := utils.LoggerFromContext(ctx)
	_, err := c.clusterServiceClient.GetExternalAuth(ctx, *externalAuth.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	logger.Info("new status via cluster-service", "newStatus", coreapi.ProvisioningStateSucceeded)
	return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
}
