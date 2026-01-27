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

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationRevokeCredentials struct {
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
	notificationClient   *http.Client
}

func NewOperationRevokeCredentialsSynchronizer(
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	notificationClient *http.Client,
) OperationSynchronizer {
	return &operationRevokeCredentials{
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
		notificationClient:   notificationClient,
	}
}

func (opsync *operationRevokeCredentials) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestRevokeCredentials {
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

	iterator := opsync.clusterServiceClient.ListBreakGlassCredentials(operation.InternalID, "")

	for breakGlassCredential := range iterator.Items(ctx) {
		// An expired credential is as good as a removed credential
		// for this operation, regardless of the credential status.
		if breakGlassCredential.ExpirationTimestamp().After(time.Now()) {
			switch status := breakGlassCredential.Status(); status {
			case cmv1.BreakGlassCredentialStatusAwaitingRevocation:
				// Operation is non-terminal; no need to check the rest.
				return arm.ProvisioningStateDeleting, nil, nil
			case cmv1.BreakGlassCredentialStatusRevoked:
				// Successful revocation so far; continue looping.
			case cmv1.BreakGlassCredentialStatusFailed:
				// XXX Cluster Service does not provide a reason for the failure,
				//     so we have no choice but to use a generic error message.
				opError := &arm.CloudErrorBody{
					Code:    arm.CloudErrorCodeInternalServerError,
					Message: "Failed to revoke cluster credential",
				}
				return arm.ProvisioningStateFailed, opError, nil
			default:
				return "", nil, fmt.Errorf("unhandled BreakGlassCredentialStatus '%s'", status)
			}
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

	operation, err := opsync.cosmosClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !opsync.ShouldProcess(ctx, operation) {
		return nil // no work to do
	}

	opStatus, opError, err := opsync.nextOperationStatus(ctx, operation)
	if err != nil {
		return utils.TrackError(err)
	}

	logger.Info("updating status")
	err = database.PatchOperationDocument(ctx, opsync.cosmosClient, operation, opStatus, opError, PostAsyncNotification(opsync.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
