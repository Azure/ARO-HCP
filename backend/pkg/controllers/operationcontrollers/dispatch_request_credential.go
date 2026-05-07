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
	"fmt"
	"time"

	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
)

type dispatchRequestCredential struct {
	clock                 utilsclock.PassiveClock
	resourcesDBClient     database.ResourcesDBClient
	clustersServiceClient ocm.ClusterServiceClientSpec
}

// NewDispatchRequestCredentialController returns a new Controller instance that
// initiates an asynchronous admin credential request operation in Clusters Service.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters
//	     Request: RequestCredential
//	      Status: Accepted
//	  InternalID: an empty value
func NewDispatchRequestCredentialController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	clustersServiceClient ocm.ClusterServiceClientSpec,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &dispatchRequestCredential{
		clock:                 clock,
		resourcesDBClient:     resourcesDBClient,
		clustersServiceClient: clustersServiceClient,
	}

	controller := NewGenericOperationController(
		"DispatchRequestCredential",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *dispatchRequestCredential) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestRequestCredential {
		return false
	}
	if len(operation.InternalID.String()) > 0 {
		return false
	}
	return true
}

func (c *dispatchRequestCredential) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
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

	cluster, err := c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).Get(ctx, operation.ExternalID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	// Make sure the cluster document has a ClusterServiceID.
	if cluster.ServiceProviderProperties.ClusterServiceID == nil {
		return fmt.Errorf("no ClusterServiceID set")
	}

	// Cancel the operation if a revocation is in progress.
	//
	// The frontend cancels all active RequestCredential operations when
	// handling a revocation request, but it cannot do so atomically. So
	// there is a slim chance of a straggler slipping through. This is a
	// second line of defense.

	if len(cluster.ServiceProviderProperties.RevokeCredentialsOperationID) > 0 {
		logger.Info("revocation in progress, canceling operation",
			"revoke_credentials_operation_id", cluster.ServiceProviderProperties.RevokeCredentialsOperationID)

		apihelpers.CancelOperation(operation, c.clock.Now())

		_, err = c.resourcesDBClient.Operations(key.SubscriptionID).Replace(ctx, operation, nil)
		if err != nil {
			return utils.TrackError(err)
		}

		return nil
	}

	// Dispatch the credential request to Clusters Service.

	logger.Info("dispatching POST break_glass_credentials to Clusters Service")
	csBreakGlassCredential, err := c.clustersServiceClient.PostBreakGlassCredential(ctx, *cluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(err)
	}

	csBreakGlassCredentialID, err := api.NewInternalID(csBreakGlassCredential.HREF())
	if err != nil {
		return utils.TrackError(err)
	}

	// If this operation document update fails then we will abandon the credential
	// created by the Clusters Service call above and start a new credential on the
	// next retry. The abandoned credential will live on but never reach the client.
	// Its backing certificate will eventually expire or be revoked.

	operation.InternalID = csBreakGlassCredentialID

	_, err = c.resourcesDBClient.Operations(key.SubscriptionID).Replace(ctx, operation, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
