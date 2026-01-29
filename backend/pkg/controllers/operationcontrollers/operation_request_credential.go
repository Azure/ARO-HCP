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

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationRequestCredential struct {
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
	notificationClient   *http.Client
}

func NewOperationRequestCredentialSynchronizer(
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	notificationClient *http.Client,
) OperationSynchronizer {
	return &operationRequestCredential{
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
		notificationClient:   notificationClient,
	}
}

func (opsync *operationRequestCredential) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestRequestCredential {
		return false
	}
	return true
}

func (opsync *operationRequestCredential) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
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

	breakGlassCredential, err := opsync.clusterServiceClient.GetBreakGlassCredential(ctx, operation.InternalID)
	if err != nil {
		return utils.TrackError(err)
	}

	var opStatus arm.ProvisioningState
	var opError *arm.CloudErrorBody

	switch status := breakGlassCredential.Status(); status {
	case cmv1.BreakGlassCredentialStatusCreated:
		opStatus = arm.ProvisioningStateProvisioning
	case cmv1.BreakGlassCredentialStatusFailed:
		// XXX Cluster Service does not provide a reason for the failure,
		//     so we have no choice but to use a generic error message.
		opStatus = arm.ProvisioningStateFailed
		opError = &arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeInternalServerError,
			Message: "Failed to provision cluster credential",
		}
	case cmv1.BreakGlassCredentialStatusIssued:
		opStatus = arm.ProvisioningStateSucceeded
	default:
		return fmt.Errorf("unhandled BreakGlassCredentialStatus '%s'", status)
	}

	logger.Info("updating status")
	err = database.PatchOperationDocument(ctx, opsync.cosmosClient, operation, opStatus, opError, PostAsyncNotification(opsync.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
