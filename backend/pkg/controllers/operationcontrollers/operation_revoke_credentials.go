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

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationRevokeCredentials struct {
	resourcesDBClient     database.ResourcesDBClient
	clustersServiceClient ocm.ClusterServiceClientSpec
	notificationClient    *http.Client
}

// NewOperationRevokeCredentialsController returns a new Controller instance that
// follows an asynchronous credential revocation operation to completion and updates
// the corresponding operation document in Cosmos DB.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters
//	     Request: RevokeCredentials
//	      Status: Deleting
//
// Note that "to completion" does not imply success. An operation is considered
// complete when its status field reaches what Azure defines as a terminal value;
// any of "Succeeded", "Failed", or "Canceled". Once the operation status reaches
// a terminal value, there will be no further updates to the operation document.
func NewOperationRevokeCredentialsController(
	resourcesDBClient database.ResourcesDBClient,
	clustersServiceClient ocm.ClusterServiceClientSpec,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &operationRevokeCredentials{
		resourcesDBClient:     resourcesDBClient,
		clustersServiceClient: clustersServiceClient,
		notificationClient:    notificationClient,
	}

	controller := NewGenericOperationController(
		"OperationRevokeCredentials",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (opsync *operationRevokeCredentials) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestRevokeCredentials {
		return false
	}
	// For this operation type, because there is no guarantee of break-
	// glass credentials being present in Clusters Service to signal when
	// the revocation has actually been dispatched, the operation's status
	// field is instead used for controller coordination. "Accepted" means
	// the credential revocation has not yet been dispatched to Clusters
	// Service. Once dispatched, the operation status becomes "Deleting"
	// and is ready for status polling.
	if operation.Status == arm.ProvisioningStateAccepted {
		return false
	}
	return true
}

func (opsync *operationRevokeCredentials) nextOperationStatus(ctx context.Context, operation *api.Operation) (arm.ProvisioningState, *arm.CloudErrorBody, error) {
	// XXX Error handling here is tricky. Since the operation applies to multiple
	//     Cluster Service objects, we can find a mix of successes and failures.
	//     And with only a Failed status for each object, it's difficult to make
	//     intelligent decisions like whether to retry. This is just to say the
	//     error handling policy here may need revising once Cluster Service
	//     offers more detail to accompany BreakGlassCredentialStatusFailed.

	iterator := opsync.clustersServiceClient.ListBreakGlassCredentials(operation.InternalID, "")

	for breakGlassCredential := range iterator.Items(ctx) {
		switch status := breakGlassCredential.Status(); status {
		case cmv1.BreakGlassCredentialStatusAwaitingRevocation:
			// Operation is non-terminal; no need to check the rest.
			return arm.ProvisioningStateDeleting, nil, nil
		case cmv1.BreakGlassCredentialStatusRevoked:
			// Successful revocation so far; continue looping.
		case cmv1.BreakGlassCredentialStatusExpired:
			// Expired credentials are not revoked; continue looping.
		case cmv1.BreakGlassCredentialStatusFailed:
			// XXX Cluster Service does not provide a reason for the failure,
			//     so we have no choice but to use a generic error message.
			opError := &arm.CloudErrorBody{
				Code:    arm.CloudErrorCodeInternalServerError,
				Message: "Failed to revoke cluster credential",
			}
			return arm.ProvisioningStateFailed, opError, nil
		case cmv1.BreakGlassCredentialStatusCreated,
			cmv1.BreakGlassCredentialStatusIssued:
			// These are valid statuses but we should not be seeing them
			// during a credential revocation. Don't fail but log a warning.
			logger := utils.LoggerFromContext(ctx)
			logger.Info("unexpected BreakGlassCredentialStatus", "status", status)
			// We may be in a stuck state here, but continue polling
			// in hopes of the credential eventually getting revoked.
			return arm.ProvisioningStateDeleting, nil, nil
		default:
			return "", nil, fmt.Errorf("unhandled BreakGlassCredentialStatus '%s'", status)
		}
	}

	err := iterator.GetError()
	if err != nil {
		return "", nil, fmt.Errorf("error while paging through Cluster Service query results: %w", err)
	}

	return arm.ProvisioningStateSucceeded, nil, nil
}

func (opsync *operationRevokeCredentials) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	oldOperation, err := opsync.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsNotFoundError(err) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !opsync.ShouldProcess(ctx, oldOperation) {
		return nil // no work to do
	}

	newOperationStatus, newOperationError, err := opsync.nextOperationStatus(ctx, oldOperation)
	if err != nil {
		return utils.TrackError(err)
	}
	if !needToPatchOperation(oldOperation, newOperationStatus, newOperationError) {
		return nil
	}

	// If we got this far then we know we're transitioning to a new status,
	// and the only statuses that follow Deleting are Succeeded or Failed.
	// So the logic below is for finalizing a completed operation.

	// FIXME May want a version of patchOperation that acts on a transaction
	//       so we can group these two writes together. For now, if the cluster
	//       replace fails we'll just retry later since the operation status
	//       will remain non-terminal.

	dbClient := opsync.resourcesDBClient.HCPClusters(oldOperation.ExternalID.SubscriptionID, oldOperation.ExternalID.ResourceGroupName)
	cluster, err := dbClient.Get(ctx, oldOperation.ExternalID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	// If the controller successfully clears the RevokeCredentialsOperationID field
	// in the cluster document but fails to update the operation document, then the
	// frontend is free to start a new RevokeCredentials operation, and may well do
	// so before the failed operation update is retried. Account for this by making
	// sure the field value still matches this operation's ID before clearing it.
	if cluster.ServiceProviderProperties.RevokeCredentialsOperationID == oldOperation.OperationID.Name {
		logger.Info("clearing RevokeCredentialsOperationID from cluster")
		cluster.ServiceProviderProperties.RevokeCredentialsOperationID = ""
		_, err = dbClient.Replace(ctx, cluster, nil)
		if err != nil {
			return utils.TrackError(err)
		}
	}

	logger.Info("updating status")
	err = patchOperation(ctx, opsync.resourcesDBClient, oldOperation, newOperationStatus, newOperationError, postAsyncNotificationFn(opsync.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
