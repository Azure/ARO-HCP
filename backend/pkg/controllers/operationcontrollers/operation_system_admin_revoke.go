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

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationSystemAdminRevoke struct {
	clock              utilsclock.PassiveClock
	resourcesDBClient  database.ResourcesDBClient
	notificationClient *http.Client
}

// NewOperationSystemAdminRevokeController returns a new Controller instance that
// follows an asynchronous credential revocation operation to completion and updates
// the corresponding operation document in Cosmos DB.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters
//	     Request: RevokeCredentials
//	      Status: Deleting
func NewOperationSystemAdminRevokeController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &operationSystemAdminRevoke{
		clock:              clock,
		resourcesDBClient:  resourcesDBClient,
		notificationClient: notificationClient,
	}

	return NewGenericOperationController(
		"OperationSystemAdminRevoke",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)
}

func (opsync *operationSystemAdminRevoke) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestRevokeCredentials {
		return false
	}
	if operation.Status == arm.ProvisioningStateAccepted {
		return false
	}
	return true
}

func (opsync *operationSystemAdminRevoke) nextOperationStatus(ctx context.Context, operation *api.Operation) (arm.ProvisioningState, *arm.CloudErrorBody, error) {
	subID := operation.ExternalID.SubscriptionID
	rgName := operation.ExternalID.ResourceGroupName
	clusterName := operation.ExternalID.Name

	credCRUD := opsync.resourcesDBClient.SystemAdminCredentials(subID, rgName, clusterName)
	iter, err := credCRUD.List(ctx, nil)
	if err != nil {
		return "", nil, fmt.Errorf("failed to list SystemAdminCredentials: %w", err)
	}

	for _, cred := range iter.Items(ctx) {
		switch cred.Status.Phase {
		case api.SystemAdminCredentialPhaseAwaitingRevocation:
			// Still waiting for revocation to complete.
			return arm.ProvisioningStateDeleting, nil, nil
		case api.SystemAdminCredentialPhaseFailed:
			opError := &arm.CloudErrorBody{
				Code:    arm.CloudErrorCodeInternalServerError,
				Message: "Failed to revoke cluster credential",
			}
			return arm.ProvisioningStateFailed, opError, nil
		case api.SystemAdminCredentialPhaseRevoked:
			// Revoked — keep checking the rest.
		case api.SystemAdminCredentialPhaseRequested, api.SystemAdminCredentialPhaseIssued:
			// These phases should not appear during revocation, but if they
			// do we stay non-terminal to let the dispatch controller catch up.
			logger := utils.LoggerFromContext(ctx)
			logger.Info("unexpected SystemAdminCredentialPhase during revocation", "phase", cred.Status.Phase)
			return arm.ProvisioningStateDeleting, nil, nil
		default:
			return "", nil, fmt.Errorf("unhandled SystemAdminCredentialPhase '%s'", cred.Status.Phase)
		}
	}

	if err := iter.GetError(); err != nil {
		return "", nil, fmt.Errorf("error iterating SystemAdminCredentials: %w", err)
	}

	return arm.ProvisioningStateSucceeded, nil, nil
}

func (opsync *operationSystemAdminRevoke) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
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

	newOperationStatus, newOperationError, err := opsync.nextOperationStatus(ctx, oldOperation)
	if err != nil {
		return utils.TrackError(err)
	}
	if !needToPatchOperation(oldOperation, newOperationStatus, newOperationError) {
		return nil
	}

	// If we are transitioning to a terminal state, clear the cluster's
	// RevokeCredentialsOperationID.
	if newOperationStatus.IsTerminal() {
		dbClient := opsync.resourcesDBClient.HCPClusters(oldOperation.ExternalID.SubscriptionID, oldOperation.ExternalID.ResourceGroupName)
		cluster, err := dbClient.Get(ctx, oldOperation.ExternalID.Name)
		if err != nil {
			return utils.TrackError(err)
		}

		if cluster.ServiceProviderProperties.RevokeCredentialsOperationID == oldOperation.OperationID.Name {
			logger.Info("clearing RevokeCredentialsOperationID from cluster")
			clusterReplacement := cluster.DeepCopy()
			clusterReplacement.ServiceProviderProperties.RevokeCredentialsOperationID = ""
			_, err = dbClient.Replace(ctx, clusterReplacement, nil)
			if err != nil {
				return utils.TrackError(err)
			}
		}
	}

	logger.Info("updating status")
	err = patchOperation(ctx, opsync.clock, opsync.resourcesDBClient, oldOperation, newOperationStatus, newOperationError, postAsyncNotificationFn(opsync.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
