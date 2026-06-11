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
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationNodePoolUpdate struct {
	clock                utilsclock.PassiveClock
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
	readDesireLister     dblisters.ReadDesireLister
	notificationClient   *http.Client
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
) controllerutils.Controller {
	syncer := &operationNodePoolUpdate{
		clock:                clock,
		resourcesDBClient:    resourcesDBClient,
		clusterServiceClient: clusterServiceClient,
		readDesireLister:     readDesireLister,
		notificationClient:   notificationClient,
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

	operation, err := c.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsNotFoundError(err) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !c.ShouldProcess(ctx, operation) {
		return nil // no work to do
	}

	cosmosNewOperationState, err := c.determineOperationStatus(ctx, operation)
	if err != nil {
		return utils.TrackError(err)
	}
	logger.Info("new status via cosmos", "newStatus", cosmosNewOperationState.provisioningState, "newOperationMessage", cosmosNewOperationState.message)

	nodePoolStatus, err := c.clusterServiceClient.GetNodePoolStatus(ctx, operation.InternalID)
	if err != nil {
		return utils.TrackError(err)
	}
	newOperationStatus, opError, err := convertNodePoolStatus(operation, nodePoolStatus)
	if err != nil {
		return utils.TrackError(err)
	}
	logger.Info("new status via cluster-service", "newStatus", newOperationStatus, "newOperationError", opError)

	if newOperationStatus == arm.ProvisioningStateSucceeded && cosmosNewOperationState.provisioningState != arm.ProvisioningStateSucceeded {
		return fmt.Errorf("cosmos operation status is %q, but cluster-service operation status is %q", cosmosNewOperationState.provisioningState, newOperationStatus)
	}

	logger.Info("updating status")
	err = UpdateOperationStatus(ctx, c.clock, c.resourcesDBClient, operation, newOperationStatus, opError, postAsyncNotificationFn(c.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (c *operationNodePoolUpdate) determineOperationStatus(ctx context.Context, operation *api.Operation) (*operationState, error) {
	logger := utils.LoggerFromContext(ctx)

	errs := []error{}
	operationStates := []*operationState{}

	clusterName := operation.ExternalID.Parent.Name
	nodePoolName := operation.ExternalID.Name
	nodePool, err := c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).NodePools(clusterName).Get(ctx, nodePoolName)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get node pool from cosmos: %w", err))
	}
	if currState, err := c.hypershiftNodePoolOperationState(ctx, nodePool); err != nil {
		errs = append(errs, utils.TrackError(err))
	} else {
		operationStates = append(operationStates, currState)
	}

	// Here in the future we could add a check that checks something in the cosmos resource itself, like:
	//	if currState, err := c.cosmosOperationState(ctx, nodePool); err != nil {
	//		errs = append(errs, utils.TrackError(err))
	//	} else {
	//		operationStates = append(operationStates, currState)
	//	}

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
	logger.Info("picked node pool update operation status", "provisioningState", picked.provisioningState, "message", picked.message)
	return picked, nil
}
