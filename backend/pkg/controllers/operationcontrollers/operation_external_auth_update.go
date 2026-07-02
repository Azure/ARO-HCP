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

	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationExternalAuthUpdate struct {
	clock                  utilsclock.PassiveClock
	resourcesDBClient      database.ResourcesDBClient
	clusterServiceClient   ocm.ClusterServiceClientSpec
	externalAuthLister     listers.ExternalAuthLister
	readDesireLister       dblisters.ReadDesireLister
	activeOperationsLister listers.ActiveOperationLister
	notificationClient     *http.Client
}

// NewOperationExternalAuthUpdateController returns a new Controller instance that
// follows an asynchronous external auth update operation to completion and updates
// the corresponding operation document in Cosmos DB.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters/externalAuths
//	     Request: Update
//	      Status: any non-terminal value
//
// Note that "to completion" does not imply success. An operation is considered
// complete when its status field reaches what Azure defines as a terminal value;
// any of "Succeeded", "Failed", or "Canceled". Once the operation status reaches
// a terminal value, there will be no further updates to the operation document.
func NewOperationExternalAuthUpdateController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	readDesireLister dblisters.ReadDesireLister,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
	backendInformers informers.BackendInformers,
) controllerutils.Controller {
	_, externalAuthLister := backendInformers.ExternalAuths()
	_, activeOperationsLister := backendInformers.ActiveOperations()

	syncer := &operationExternalAuthUpdate{
		clock:                  clock,
		resourcesDBClient:      resourcesDBClient,
		clusterServiceClient:   clusterServiceClient,
		externalAuthLister:     externalAuthLister,
		readDesireLister:       readDesireLister,
		activeOperationsLister: activeOperationsLister,
		notificationClient:     notificationClient,
	}

	controller := NewGenericOperationController(
		"OperationExternalAuthUpdate",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *operationExternalAuthUpdate) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestUpdate {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), api.ExternalAuthResourceType.String()) {
		return false
	}
	return true
}

func (c *operationExternalAuthUpdate) shouldReconcileOperationAndResourceStatus(ea *api.HCPOpenShiftClusterExternalAuth) bool {
	return ea.ServiceProviderProperties.DeletionTimestamp == nil &&
		ea.ServiceProviderProperties.ClusterServiceID != nil
}

func (c *operationExternalAuthUpdate) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
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

	existingExternalAuth, err := c.externalAuthLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Parent.Name, operation.ExternalID.Name)
	if database.IsNotFoundError(err) {
		logger.Info("external auth not found in cache, waiting")
		return nil // no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get external auth: %w", err))
	}

	if operation.ResourceID.Name != existingExternalAuth.ServiceProviderProperties.ActiveOperationID {
		logger.Info("external auth active operation id mismatch, returning early", "synchronizedActiveOperationID", operation.ResourceID.Name, "externalAuthActiveOperationID", existingExternalAuth.ServiceProviderProperties.ActiveOperationID)
		return nil
	}

	if !c.shouldReconcileOperationAndResourceStatus(existingExternalAuth) {
		return nil // no work to do
	}

	operationalState, err := c.determineOperationState(ctx, operation, existingExternalAuth)
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
	if err := UpdateOperationStatus(ctx, c.clock, c.resourcesDBClient, operation, operationalState.ProvisioningState, persistErr, postAsyncNotificationFn(c.notificationClient)); err != nil {
		return utils.TrackError(err)
	}
	return nil
}

func (c *operationExternalAuthUpdate) determineOperationState(ctx context.Context, operation *api.Operation, existingExternalAuth *api.HCPOpenShiftClusterExternalAuth) (*operationState, error) {
	logger := utils.LoggerFromContext(ctx)

	externalAuthCSID := existingExternalAuth.ServiceProviderProperties.ClusterServiceID
	csExternalAuth, err := c.clusterServiceClient.GetExternalAuth(ctx, *externalAuthCSID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get external auth from cluster service: %w", err))
	}

	errs := []error{}
	operationStates := []*operationState{}

	if operationState, csErr := c.clusterServiceExternalAuthSpecOperationState(existingExternalAuth, csExternalAuth); csErr != nil {
		errs = append(errs, utils.TrackError(csErr))
	} else {
		operationStates = append(operationStates, operationState.withSource("clusterServiceExternalAuthSpec"))
	}

	if operationState, hsErr := c.hypershiftExternalAuthOperationState(ctx, operation, existingExternalAuth); hsErr != nil {
		errs = append(errs, utils.TrackError(hsErr))
	} else {
		operationStates = append(operationStates, operationState.withSource("hypershiftExternalAuth"))
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
	logger.Info("determined external auth update operation status", "operationStates", operationStates)
	picked, err := pickWorstOperationState(operationStates)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	logger.Info("picked external auth update operation status", "picked", picked)
	return picked, nil
}
