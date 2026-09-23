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

package operations

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationRevokeCredentialsPoll struct {
	clock              utilsclock.PassiveClock
	resourcesDBClient  corecosmosstorage.ResourcesDBClient
	clusterLister      corelisters.ClusterLister
	notificationClient *http.Client
}

// NewOperationRevokeCredentialsPollController returns a Controller that follows a
// RevokeCredentials operation to completion. The dispatch controller creates a
// SystemAdminCredentialRevocation document, records it on the operation's
// SystemAdminCredentialRevocation, and moves the operation to Deleting. The dedicated revocation
// controllers then drive the revocation and delete that document when finished.
// This poll controller simply waits for the document to disappear; once it is
// gone it clears the cluster's revoke sentinel and marks the operation Succeeded.
func NewOperationRevokeCredentialsPollController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clusterLister corelisters.ClusterLister,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &operationRevokeCredentialsPoll{
		clock:              clock,
		resourcesDBClient:  resourcesDBClient,
		clusterLister:      clusterLister,
		notificationClient: notificationClient,
	}

	controller := controllerutils.NewGenericOperationController(
		"SystemAdminCredentialOperationRevokeCredentialsPoll",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *operationRevokeCredentialsPoll) ShouldProcess(ctx context.Context, operation *coreapi.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != coreapi.OperationRequestSystemAdminCredentialRevocation {
		return false
	}
	if operation.Status != coreapi.ProvisioningStateDeleting {
		return false
	}
	return true
}

func (c *operationRevokeCredentialsPoll) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking revoke operation poll")

	operation, err := c.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if cosmosstorageutils.IsNotFoundError(err) {
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
	if operation.SystemAdminCredentialRevocation == nil || operation.SystemAdminCredentialRevocation.SystemAdminCredentialRevocationResourceID == nil {
		logger.Info("waiting for revocation to be dispatched")
		return nil
	}

	revocationName := operation.SystemAdminCredentialRevocation.SystemAdminCredentialRevocationResourceID.Name
	_, err = c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).SystemAdminCredentialRevocations(
		operation.ExternalID.Name,
	).Get(ctx, revocationName)
	if err == nil {
		// Revocation still in progress.
		logger.Info("waiting for revocation to complete", "revocation", revocationName)
		return nil
	}
	if !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to get SystemAdminCredentialRevocation: %w", err))
	}

	// The revocation document is gone: revocation is complete. Clear the cluster
	// sentinel and mark the operation Succeeded.
	cachedCluster, err := c.clusterLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(err)
	}
	if err == nil && cachedCluster.ServiceProviderProperties.RevokeCredentialsOperationID == operation.OperationID.Name {
		cluster, err := c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).Get(ctx, operation.ExternalID.Name)
		if err != nil {
			return utils.TrackError(err)
		}
		clusterReplacement := cluster.DeepCopy()
		clusterReplacement.ServiceProviderProperties.RevokeCredentialsOperationID = ""
		_, err = c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).Replace(ctx, clusterReplacement, nil)
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			return nil
		}
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to clear RevokeCredentialsOperationID: %w", err))
		}
	}

	var notifyFn operationbase.PostAsyncNotificationFunc
	if c.notificationClient != nil {
		client := c.notificationClient
		notifyFn = func(ctx context.Context, op *coreapi.Operation) error {
			return operationbase.PostAsyncNotification(ctx, client, op)
		}
	}
	err = operationbase.UpdateOperationStatus(ctx, c.clock, c.resourcesDBClient, operation, coreapi.ProvisioningStateSucceeded, nil, notifyFn)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	logger.Info("revocation complete", "revocation", revocationName)
	return nil
}
