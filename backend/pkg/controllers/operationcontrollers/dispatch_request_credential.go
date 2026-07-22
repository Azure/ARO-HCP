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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

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
//
// Safe write order after POST:
//  1. Create ClusterAdminCredential document keyed by CS break-glass credential ID
//  2. Set Operation.InternalID
//
// On retry, if a ClusterAdminCredential document already exists for this operation ID, POST is skipped and
// InternalID is linked from the ClusterAdminCredential document.
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

		replacement := operation.DeepCopy()
		apihelpers.CancelOperation(replacement, c.clock.Now())

		_, err = c.resourcesDBClient.Operations(key.SubscriptionID).Replace(ctx, replacement, nil)
		if err != nil {
			return utils.TrackError(err)
		}

		return nil
	}

	operationName := operation.OperationID.Name

	// We first list all the cluster admin credential documents under the cluster and we look for the one that has the operation ID.
	// This covers the case where the operation is retried and we find the admin credential document that was created by the previous retry but the
	// Operation document failed to update.
	// TODO as of now we have the CS credential ID as the name of the resource ID of the ClusterAdminCredential document. Is this what we want? From
	// CS side that's a KSUID. An alternative would be to use the operation ID as the name of the resource ID of the ClusterAdminCredential document. That
	// would somehow couple it to the operation. In that case we would be able to get the admin credential document directly by using the operation ID.
	existingAdminCredential, err := c.findClusterAdminCredentialByOperationName(ctx, cluster.ID, operationName)
	if err != nil {
		return utils.TrackError(err)
	}

	var csBreakGlassCredentialID api.InternalID
	if existingAdminCredential != nil {
		logger.Info("found existing ClusterAdminCredential from cosmos DB", "admin_credential_resource_id", existingAdminCredential.ResourceID.String())
		csBreakGlassCredentialID = existingAdminCredential.ClusterServiceInternalID
	} else {
		logger.Info("dispatching POST break_glass_credentials to Clusters Service")
		csBreakGlassCredential, err := c.clustersServiceClient.PostBreakGlassCredential(ctx, *cluster.ServiceProviderProperties.ClusterServiceID)
		if err != nil {
			return utils.TrackError(err)
		}
		logger.Info("dispatched POST break_glass_credentials to Clusters Service", "cs_break_glass_credential_href", csBreakGlassCredential.HREF())

		csBreakGlassCredentialID, err = api.NewInternalID(csBreakGlassCredential.HREF())
		if err != nil {
			return utils.TrackError(err)
		}

		desiredClusterAdminCredential, err := database.NewClusterAdminCredential(cluster.ID, csBreakGlassCredentialID, operationName)
		if err != nil {
			return utils.TrackError(err)
		}
		if status := csBreakGlassCredential.Status(); status != "" {
			convertedStatus, err := ocm.ConvertCStoClusterAdminCredentialStatus(status)
			if err != nil {
				return utils.TrackError(err)
			}
			desiredClusterAdminCredential.Status = convertedStatus
		}
		if !csBreakGlassCredential.ExpirationTimestamp().IsZero() {
			desiredClusterAdminCredential.ExpirationTimestamp = csBreakGlassCredential.ExpirationTimestamp()
		}

		// Create the cluster admin credential document before setting Operation.InternalID in
		// the Operation document. If the create here succeeds and the Operation
		// replace below fails, the next retry finds the admin credential document
		// by operation ID and skips a second POST. If create fails then we will
		// abandon the credential created by the Clusters Service call above and
		// start a new credential on the next retry. The abandoned credential will
		// live on but never reach the client. Its backing certificate will
		// eventually expire or be revoked. At the moment of writing this (2026-07-21)
		// CS has a minimum expiration time of 10m10s after creation, and a maximum
		// expiration time of 24 hours after creation.
		_, err = c.getOrCreateClusterAdminCredential(ctx, desiredClusterAdminCredential)
		if err != nil {
			return utils.TrackError(err)
		}
	}

	replacement := operation.DeepCopy()
	replacement.InternalID = csBreakGlassCredentialID
	_, err = c.resourcesDBClient.Operations(key.SubscriptionID).Replace(ctx, replacement, nil)
	if database.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

// getOrCreateClusterAdminCredential creates the document, or returns the
// existing one on conflict.
func (c *dispatchRequestCredential) getOrCreateClusterAdminCredential(ctx context.Context, cred *api.ClusterAdminCredential) (*api.ClusterAdminCredential, error) {
	clusterResourceID := cred.ResourceID.Parent
	crud := c.resourcesDBClient.HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName).AdminCredentials(clusterResourceID.Name)

	created, err := crud.Create(ctx, cred, nil)
	if err == nil {
		return created, nil
	}

	if !database.IsConflictError(err) {
		return nil, utils.TrackError(err)
	}

	existing, err := crud.Get(ctx, cred.ResourceID.Name)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	return existing, nil
}

// findClusterAdminCredentialByOperationName lists admin credentials under the
// cluster and returns the one whose OperationID matches. Returns nil if none.
// An error is returned if multiple admin credential documents are found for the same operation name.
func (c *dispatchRequestCredential) findClusterAdminCredentialByOperationName(ctx context.Context, clusterResourceID *azcorearm.ResourceID, operationName string) (*api.ClusterAdminCredential, error) {
	crud := c.resourcesDBClient.HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName).AdminCredentials(clusterResourceID.Name)

	iter, err := crud.List(ctx, nil)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	var found *api.ClusterAdminCredential
	for _, item := range iter.Items(ctx) {
		if item.OperationID == operationName {
			if found != nil {
				return nil, utils.TrackError(fmt.Errorf("multiple ClusterAdminCredential docs found for operation %s", operationName))
			}
			found = item
		}
	}
	if err := iter.GetError(); err != nil {
		return nil, utils.TrackError(err)
	}
	return found, nil
}
