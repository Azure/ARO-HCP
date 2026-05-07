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

type operationRequestCredential struct {
	resourcesDBClient     database.ResourcesDBClient
	clustersServiceClient ocm.ClusterServiceClientSpec
	notificationClient    *http.Client
}

// NewOperationRequestCredentialController returns a new Controller instance that
// follows an asynchronous admin credential request operation to completion and
// updates the corresponding operation document in Cosmos DB.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters
//	     Request: RequestCredential
//	      Status: any non-terminal value
//	  InternalID: a Clusters Service HREF value
//
// Note that "to completion" does not imply success. An operation is considered
// complete when its status field reaches what Azure defines as a terminal value;
// any of "Succeeded", "Failed", or "Canceled". Once the operation status reaches
// a terminal value, there will be no further updates to the operation document.
func NewOperationRequestCredentialController(
	resourcesDBClient database.ResourcesDBClient,
	clustersServiceClient ocm.ClusterServiceClientSpec,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &operationRequestCredential{
		resourcesDBClient:     resourcesDBClient,
		clustersServiceClient: clustersServiceClient,
		notificationClient:    notificationClient,
	}

	controller := NewGenericOperationController(
		"OperationRequestCredential",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (opsync *operationRequestCredential) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
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

func (opsync *operationRequestCredential) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
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

	breakGlassCredential, err := opsync.clustersServiceClient.GetBreakGlassCredential(ctx, oldOperation.InternalID)
	if err != nil {
		return utils.TrackError(err)
	}

	var newOperationStatus arm.ProvisioningState
	var newOperationError *arm.CloudErrorBody

	switch status := breakGlassCredential.Status(); status {
	case cmv1.BreakGlassCredentialStatusCreated:
		newOperationStatus = arm.ProvisioningStateProvisioning
	case cmv1.BreakGlassCredentialStatusFailed:
		// XXX Cluster Service does not provide a reason for the failure,
		//     so we have no choice but to use a generic error message.
		newOperationStatus = arm.ProvisioningStateFailed
		newOperationError = &arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeInternalServerError,
			Message: "Failed to provision cluster credential",
		}
	case cmv1.BreakGlassCredentialStatusIssued:
		newOperationStatus = arm.ProvisioningStateSucceeded
	default:
		return fmt.Errorf("unhandled BreakGlassCredentialStatus '%s'", status)
	}

	if !needToPatchOperation(oldOperation, newOperationStatus, newOperationError) {
		return nil
	}

	err = patchOperation(ctx, opsync.resourcesDBClient, oldOperation, newOperationStatus, newOperationError, postAsyncNotificationFn(opsync.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
