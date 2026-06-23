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
	"fmt"
	"net/http"
	"time"

	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationSystemAdminCredential struct {
	clock              utilsclock.PassiveClock
	resourcesDBClient  database.ResourcesDBClient
	notificationClient *http.Client
}

// NewOperationSystemAdminCredentialController returns a new Controller instance that
// follows an asynchronous admin credential request operation to completion and
// updates the corresponding operation document in Cosmos DB.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters
//	     Request: RequestCredential
//	      Status: any non-terminal value
//	  InternalID: a SystemAdminCredential resource ID
func NewOperationSystemAdminCredentialController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &operationSystemAdminCredential{
		clock:              clock,
		resourcesDBClient:  resourcesDBClient,
		notificationClient: notificationClient,
	}

	return NewGenericOperationController(
		"OperationSystemAdminCredential",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)
}

func (opsync *operationSystemAdminCredential) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestRequestCredential {
		return false
	}
	if len(operation.InternalID.String()) == 0 {
		return false
	}
	return true
}

func (opsync *operationSystemAdminCredential) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	oldOperation, err := opsync.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !opsync.ShouldProcess(ctx, oldOperation) {
		return nil
	}

	// Parse the SystemAdminCredential resource ID from the operation's InternalID.
	credResourceID, err := azcorearm.ParseResourceID(oldOperation.InternalID.String())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to parse InternalID as resource ID: %w", err))
	}

	cred, err := opsync.resourcesDBClient.SystemAdminCredentials(
		credResourceID.SubscriptionID,
		credResourceID.ResourceGroupName,
		credResourceID.Parent.Name,
	).Get(ctx, credResourceID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	var newOperationStatus arm.ProvisioningState
	var newOperationError *arm.CloudErrorBody

	switch phase := cred.Status.Phase; phase {
	case api.SystemAdminCredentialPhaseRequested:
		newOperationStatus = arm.ProvisioningStateProvisioning
	case api.SystemAdminCredentialPhaseFailed:
		newOperationStatus = arm.ProvisioningStateFailed
		newOperationError = &arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeInternalServerError,
			Message: "Failed to provision cluster credential",
		}
	case api.SystemAdminCredentialPhaseIssued:
		newOperationStatus = arm.ProvisioningStateSucceeded
	default:
		return fmt.Errorf("unhandled SystemAdminCredentialPhase '%s'", phase)
	}

	if !needToPatchOperation(oldOperation, newOperationStatus, newOperationError) {
		return nil
	}

	err = patchOperation(ctx, opsync.clock, opsync.resourcesDBClient, oldOperation, newOperationStatus, newOperationError, postAsyncNotificationFn(opsync.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
