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

package operations

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/credentialrequest"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationRequestCredentialPoll struct {
	clock              utilsclock.PassiveClock
	resourcesDBClient  corecosmosstorage.ResourcesDBClient
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
//	  SystemAdminCredentialRequest.SystemAdminCredentialRequestResourceID: set
func NewOperationRequestCredentialPollController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &operationRequestCredentialPoll{
		clock:              clock,
		resourcesDBClient:  resourcesDBClient,
		notificationClient: notificationClient,
	}

	controller := controllerutils.NewGenericOperationController(
		"SystemAdminCredentialOperationRequestCredentialPoll",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *operationRequestCredentialPoll) ShouldProcess(ctx context.Context, operation *coreapi.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != coreapi.OperationRequestSystemAdminCredentialRequest {
		return false
	}
	if operation.SystemAdminCredentialRequest == nil || operation.SystemAdminCredentialRequest.SystemAdminCredentialRequestResourceID == nil {
		return false
	}
	return true
}

func (c *operationRequestCredentialPoll) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	oldOperation, err := c.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !c.ShouldProcess(ctx, oldOperation) {
		return nil
	}

	credResourceID := oldOperation.SystemAdminCredentialRequest.SystemAdminCredentialRequestResourceID
	credName := credResourceID.Name

	// Look up the SystemAdminCredentialRequest doc.
	cred, err := c.resourcesDBClient.HCPClusters(oldOperation.ExternalID.SubscriptionID, oldOperation.ExternalID.ResourceGroupName).SystemAdminCredentialRequests(
		oldOperation.ExternalID.Name,
	).Get(ctx, credName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get SystemAdminCredentialRequest: %w", err))
	}

	// Map conditions to ARM provisioning state.
	var newOperationStatus coreapi.ProvisioningState
	var newOperationError *coreapi.CloudErrorBody

	switch {
	case credentialrequest.IsCredentialRequestPending(cred):
		newOperationStatus = coreapi.ProvisioningStateProvisioning
	case meta.IsStatusConditionTrue(cred.Status.Conditions, coreapi.SystemAdminCredentialRequestConditionIssued):
		newOperationStatus = coreapi.ProvisioningStateSucceeded
	case meta.IsStatusConditionTrue(cred.Status.Conditions, coreapi.SystemAdminCredentialRequestConditionFailed):
		newOperationStatus = coreapi.ProvisioningStateFailed
		newOperationError = &coreapi.CloudErrorBody{
			Code:    coreapi.CloudErrorCodeInternalServerError,
			Message: "Failed to provision cluster credential",
		}
		if c := meta.FindStatusCondition(cred.Status.Conditions, coreapi.SystemAdminCredentialRequestConditionFailed); c != nil {
			newOperationError.Message = c.Message
		}
	case meta.IsStatusConditionTrue(cred.Status.Conditions, coreapi.SystemAdminCredentialRequestConditionAwaitingRevocation),
		meta.IsStatusConditionTrue(cred.Status.Conditions, coreapi.SystemAdminCredentialRequestConditionRevoked):
		newOperationStatus = coreapi.ProvisioningStateCanceled
	}

	var notifyFn operationbase.PostAsyncNotificationFunc
	if c.notificationClient != nil {
		client := c.notificationClient
		notifyFn = func(ctx context.Context, op *coreapi.Operation) error {
			return operationbase.PostAsyncNotification(ctx, client, op)
		}
	}
	err = operationbase.UpdateOperationStatus(ctx, c.clock, c.resourcesDBClient, oldOperation, newOperationStatus, newOperationError, notifyFn)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
