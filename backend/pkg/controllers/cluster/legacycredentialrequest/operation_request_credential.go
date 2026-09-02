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

package legacycredentialrequest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationRequestCredential struct {
	clock                 utilsclock.PassiveClock
	resourcesDBClient     corecosmosstorage.ResourcesDBClient
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
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clustersServiceClient ocm.ClusterServiceClientSpec,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &operationRequestCredential{
		clock:                 clock,
		resourcesDBClient:     resourcesDBClient,
		clustersServiceClient: clustersServiceClient,
		notificationClient:    notificationClient,
	}

	controller := controllerutils.NewGenericOperationController(
		"OperationRequestCredential",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (opsync *operationRequestCredential) ShouldProcess(ctx context.Context, operation *coreapi.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != cosmosstorageutils.OperationRequestSystemAdminCredentialRequest {
		return false
	}
	if len(operation.InternalID.String()) == 0 {
		return false
	}
	if operation.SystemAdminCredentialRequest != nil {
		return false
	}
	return true
}

func (opsync *operationRequestCredential) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	oldOperation, err := opsync.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !opsync.ShouldProcess(ctx, oldOperation) {
		return nil // no work to do
	}

	breakGlassCredential, err := opsync.clustersServiceClient.GetBreakGlassCredential(ctx, oldOperation.InternalID)

	// This is for an observed case where a credential request was dispatched, but for
	// some reason took a full day for the Cluster Service credential endpoint to show
	// status "issued". By that time the backing certificate had already expired. Logs
	// showed Cluster Service transitioning the credential status to "issued" and then
	// immediately purging it, such that further polling returned a 404 Not Found. The
	// backend controller never saw the state transition. If this happens again, catch
	// it and fail the operation to avoid a hot loop.
	var ocmError *ocmerrors.Error
	if errors.As(err, &ocmError) && ocmError.Status() == http.StatusNotFound {
		logger.Info("credential endpoint vanished, terminating operation")
		err = operationbase.PatchOperation(ctx, opsync.clock, opsync.resourcesDBClient, oldOperation,
			coreapi.ProvisioningStateFailed,
			&coreapi.CloudErrorBody{
				Code:    coreapi.CloudErrorCodeInternalServerError,
				Message: "Failed to provision cluster credential",
			},
			operationbase.PostAsyncNotificationFn(opsync.notificationClient))
		if err != nil {
			return utils.TrackError(err)
		}
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	var newOperationStatus coreapi.ProvisioningState
	var newOperationError *coreapi.CloudErrorBody

	switch status := breakGlassCredential.Status(); status {
	case cmv1.BreakGlassCredentialStatusCreated:
		newOperationStatus = coreapi.ProvisioningStateProvisioning
	case cmv1.BreakGlassCredentialStatusFailed:
		// XXX Cluster Service does not provide a reason for the failure,
		//     so we have no choice but to use a generic error message.
		newOperationStatus = coreapi.ProvisioningStateFailed
		newOperationError = &coreapi.CloudErrorBody{
			Code:    coreapi.CloudErrorCodeInternalServerError,
			Message: "Failed to provision cluster credential",
		}
	case cmv1.BreakGlassCredentialStatusIssued:
		newOperationStatus = coreapi.ProvisioningStateSucceeded
	default:
		return fmt.Errorf("unhandled BreakGlassCredentialStatus '%s'", status)
	}

	if !operationbase.NeedToPatchOperation(oldOperation, newOperationStatus, newOperationError) {
		return nil
	}

	err = operationbase.PatchOperation(ctx, opsync.clock, opsync.resourcesDBClient, oldOperation, newOperationStatus, newOperationError, operationbase.PostAsyncNotificationFn(opsync.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
