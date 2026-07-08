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

package systemadmincredentialcontrollers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/operationcontrollers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationRequestCredentialPoll struct {
	clock              utilsclock.PassiveClock
	resourcesDBClient  database.ResourcesDBClient
	notificationClient *http.Client
}

// NewOperationRequestCredentialPollController returns a Controller that
// maps the SystemAdminCredentialRequest's conditions to ARM provisioning state. It
// replaces the old cluster-service-based OperationRequestCredentialController.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters
//	     Request: RequestCredential
//	      Status: any non-terminal value
//	  InternalID: a SystemAdminCredential resource ID
func NewOperationRequestCredentialPollController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &operationRequestCredentialPoll{
		clock:              clock,
		resourcesDBClient:  resourcesDBClient,
		notificationClient: notificationClient,
	}

	controller := operationcontrollers.NewGenericOperationController(
		"SystemAdminCredentialOperationRequestCredentialPoll",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *operationRequestCredentialPoll) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != api.OperationRequestRequestCredential {
		return false
	}
	if len(operation.InternalID.String()) == 0 {
		return false
	}
	return true
}

func (c *operationRequestCredentialPoll) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	oldOperation, err := c.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !c.ShouldProcess(ctx, oldOperation) {
		return nil
	}

	// Parse the credential resource ID from Operation.InternalID.
	credResourceID := oldOperation.InternalID
	credName := credResourceID.ID()

	// Look up the SystemAdminCredentialRequest doc.
	cred, err := c.resourcesDBClient.SystemAdminCredentialRequests(
		oldOperation.ExternalID.SubscriptionID,
		oldOperation.ExternalID.ResourceGroupName,
		oldOperation.ExternalID.Name,
	).Get(ctx, credName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get SystemAdminCredentialRequest: %w", err))
	}

	// Map conditions to ARM provisioning state.
	var newOperationStatus arm.ProvisioningState
	var newOperationError *arm.CloudErrorBody

	switch {
	case cred.Status.IsPending():
		newOperationStatus = arm.ProvisioningStateProvisioning
	case cred.Status.IsIssued():
		newOperationStatus = arm.ProvisioningStateSucceeded
	case cred.Status.IsFailed():
		newOperationStatus = arm.ProvisioningStateFailed
		newOperationError = &arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeInternalServerError,
			Message: "Failed to provision cluster credential",
		}
		// If there's a condition with more detail, use it.
		if c := meta.FindStatusCondition(cred.Status.Conditions, api.SystemAdminCredentialRequestConditionFailed); c != nil {
			newOperationError.Message = c.Message
		}
	case cred.Status.IsAwaitingRevocation(), cred.Status.IsRevoked():
		// Credential was revoked before issuance completed. Cancel the operation.
		newOperationStatus = arm.ProvisioningStateCanceled
	}

	var notifyFn operationcontrollers.PostAsyncNotificationFunc
	if c.notificationClient != nil {
		client := c.notificationClient
		notifyFn = func(ctx context.Context, op *api.Operation) error {
			return operationcontrollers.PostAsyncNotification(ctx, client, op)
		}
	}
	err = operationcontrollers.UpdateOperationStatus(ctx, c.clock, c.resourcesDBClient, oldOperation, newOperationStatus, newOperationError, notifyFn)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
