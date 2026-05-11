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

package operationcontrollers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
)

type dispatchRevokeCredentials struct {
	clock                 utilsclock.PassiveClock
	resourcesDBClient     database.ResourcesDBClient
	clustersServiceClient ocm.ClusterServiceClientSpec
}

// NewDispatchRevokeCredentialsController returns a new Controller instance that
// initiates an asynchronous credential revocation operation in Clusters Service.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters
//	     Request: RevokeCredentials
//	      Status: Accepted
func NewDispatchRevokeCredentialsController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	clustersServiceClient ocm.ClusterServiceClientSpec,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &dispatchRevokeCredentials{
		clock:                 clock,
		resourcesDBClient:     resourcesDBClient,
		clustersServiceClient: clustersServiceClient,
	}

	controller := NewGenericOperationController(
		"DispatchRevokeCredentials",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *dispatchRevokeCredentials) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
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
	if operation.Status != arm.ProvisioningStateAccepted {
		return false
	}
	return true
}

func (c *dispatchRevokeCredentials) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	operation, err := c.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsNotFoundError(err) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !c.ShouldProcess(ctx, operation) {
		return nil // no work to do
	}

	// Ensure the cluster's RevokeCredentialsOperationID still matches this operation's ID.

	cluster, err := c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).Get(ctx, operation.ExternalID.Name)
	if err != nil {
		return utils.TrackError(err)
	}
	if cluster.ServiceProviderProperties.RevokeCredentialsOperationID != operation.OperationID.Name {
		logger.Info("cluster RevokeCredentialsOperationID mismatch",
			"revoke_credentials_operation_id", cluster.ServiceProviderProperties.RevokeCredentialsOperationID)

		apihelpers.CancelOperation(operation, c.clock.Now())

		_, err = c.resourcesDBClient.Operations(key.SubscriptionID).Replace(ctx, operation, nil)
		if err != nil {
			return utils.TrackError(err)
		}

		return nil
	}

	// Dispatch the revocation request to Clusters Service.

	logger.Info("dispatching DELETE break_glass_credentials to Clusters Service")
	err = c.clustersServiceClient.DeleteBreakGlassCredentials(ctx, operation.InternalID)
	var ocmError *ocmerrors.Error
	if errors.As(err, &ocmError) && ocmError.Status() == http.StatusBadRequest {
		// XXX Matching an error message is brittle, but Clusters Service
		//     returns 400 Bad Request for a wide range of errors and there
		//     is no other information in the response to distinguish them.
		//
		//     If the error is indicating that a credential revocation is
		//     already in progress, dismiss it. This can happen on a retry
		//     if the previous Clusters Service call was successful but the
		//     Cosmos DB replace operation below failed.
		if strings.Contains(ocmError.Reason(), "revocation has already been requested") {
			err = nil
		}
	}
	if err != nil {
		return utils.TrackError(err)
	}

	// Update the operation status to "Deleting" to commence Clusters
	// Service polling in the "OperationRevokeCredentials" controller.

	operation.Status = arm.ProvisioningStateDeleting

	_, err = c.resourcesDBClient.Operations(key.SubscriptionID).Replace(ctx, operation, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
