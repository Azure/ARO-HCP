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

	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/operationcontrollers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationRevokeCredentialsPoll struct {
	clock              utilsclock.PassiveClock
	resourcesDBClient  database.ResourcesDBClient
	notificationClient *http.Client
}

// NewOperationRevokeCredentialsPollController returns a Controller that follows a
// RevokeCredentials operation to completion. The dispatch controller creates a
// SystemAdminCredentialRevocation document, records it on the operation's
// InternalID, and moves the operation to Deleting. The dedicated revocation
// controllers then drive the revocation and delete that document when finished.
// This poll controller simply waits for the document to disappear; once it is
// gone it clears the cluster's revoke sentinel and marks the operation Succeeded.
func NewOperationRevokeCredentialsPollController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &operationRevokeCredentialsPoll{
		clock:              clock,
		resourcesDBClient:  resourcesDBClient,
		notificationClient: notificationClient,
	}

	controller := operationcontrollers.NewGenericOperationController(
		"SystemAdminCredentialOperationRevokeCredentialsPoll",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *operationRevokeCredentialsPoll) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != api.OperationRequestRevokeCredentials {
		return false
	}
	if operation.Status != arm.ProvisioningStateDeleting {
		return false
	}
	return true
}

func (c *operationRevokeCredentialsPoll) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking revoke operation poll")

	operation, err := c.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !c.ShouldProcess(ctx, operation) {
		return nil
	}

	// The dispatch controller records the revocation document's resource ID on
	// the operation. Until it does, there is nothing to wait on yet.
	if len(operation.InternalID.String()) == 0 {
		logger.Info("waiting for revocation to be dispatched")
		return nil
	}

	revocationName := operation.InternalID.ID()
	_, err = c.resourcesDBClient.SystemAdminCredentialRevocations(
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName,
		operation.ExternalID.Name,
	).Get(ctx, revocationName)
	if err == nil {
		// Revocation still in progress.
		logger.Info("waiting for revocation to complete", "revocation", revocationName)
		return nil
	}
	if !database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to get SystemAdminCredentialRevocation: %w", err))
	}

	// The revocation document is gone: revocation is complete. Clear the cluster
	// sentinel and mark the operation Succeeded.
	cluster, err := c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).Get(ctx, operation.ExternalID.Name)
	if err != nil && !database.IsNotFoundError(err) {
		return utils.TrackError(err)
	}
	if err == nil && cluster.ServiceProviderProperties.RevokeCredentialsOperationID == operation.OperationID.Name {
		clusterReplacement := cluster.DeepCopy()
		clusterReplacement.ServiceProviderProperties.RevokeCredentialsOperationID = ""
		if _, err := c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).Replace(ctx, clusterReplacement, nil); err != nil {
			return utils.TrackError(fmt.Errorf("failed to clear RevokeCredentialsOperationID: %w", err))
		}
	}

	var notifyFn operationcontrollers.PostAsyncNotificationFunc
	if c.notificationClient != nil {
		client := c.notificationClient
		notifyFn = func(ctx context.Context, op *api.Operation) error {
			return operationcontrollers.PostAsyncNotification(ctx, client, op)
		}
	}
	if err := operationcontrollers.UpdateOperationStatus(ctx, c.clock, c.resourcesDBClient, operation, arm.ProvisioningStateSucceeded, nil, notifyFn); err != nil {
		return utils.TrackError(err)
	}

	logger.Info("revocation complete", "revocation", revocationName)
	return nil
}
