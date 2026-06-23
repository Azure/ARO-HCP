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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
)

type dispatchSystemAdminRevoke struct {
	clock             utilsclock.PassiveClock
	resourcesDBClient database.ResourcesDBClient
}

// NewDispatchSystemAdminRevokeController returns a new Controller instance that
// initiates an asynchronous credential revocation operation by marking all active
// SystemAdminCredential documents for revocation.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters
//	     Request: RevokeCredentials
//	      Status: Accepted
func NewDispatchSystemAdminRevokeController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &dispatchSystemAdminRevoke{
		clock:             clock,
		resourcesDBClient: resourcesDBClient,
	}

	return NewGenericOperationController(
		"DispatchSystemAdminRevoke",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)
}

func (c *dispatchSystemAdminRevoke) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestRevokeCredentials {
		return false
	}
	if operation.Status != arm.ProvisioningStateAccepted {
		return false
	}
	return true
}

func (c *dispatchSystemAdminRevoke) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	operation, err := c.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !c.ShouldProcess(ctx, operation) {
		return nil
	}

	// Ensure the cluster's RevokeCredentialsOperationID still matches this operation's ID.
	cluster, err := c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).Get(ctx, operation.ExternalID.Name)
	if err != nil {
		return utils.TrackError(err)
	}
	if cluster.ServiceProviderProperties.RevokeCredentialsOperationID != operation.OperationID.Name {
		logger.Info("cluster RevokeCredentialsOperationID mismatch",
			"revoke_credentials_operation_id", cluster.ServiceProviderProperties.RevokeCredentialsOperationID)

		replacement := operation.DeepCopy()
		apihelpers.CancelOperation(replacement, c.clock.Now())

		_, err = c.resourcesDBClient.Operations(key.SubscriptionID).Replace(ctx, replacement, nil)
		if err != nil {
			return utils.TrackError(err)
		}

		return nil
	}

	subID := operation.ExternalID.SubscriptionID
	rgName := operation.ExternalID.ResourceGroupName
	clusterName := operation.ExternalID.Name

	// List all SystemAdminCredentials under the cluster and flip active ones
	// to AwaitingRevocation.
	credCRUD := c.resourcesDBClient.SystemAdminCredentials(subID, rgName, clusterName)
	iter, err := credCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list SystemAdminCredentials: %w", err))
	}

	now := metav1.NewTime(c.clock.Now())
	for _, cred := range iter.Items(ctx) {
		switch cred.Status.Phase {
		case api.SystemAdminCredentialPhaseRequested, api.SystemAdminCredentialPhaseIssued:
			replacement := cred.DeepCopy()
			replacement.Status.Phase = api.SystemAdminCredentialPhaseAwaitingRevocation
			replacement.Status.RevokedAt = &now
			_, replaceErr := credCRUD.Replace(ctx, replacement, nil)
			if replaceErr != nil {
				return utils.TrackError(fmt.Errorf("failed to mark credential for revocation: %w", replaceErr))
			}
			logger.Info("marked credential for revocation", "credentialName", cred.GetResourceID().Name)
		}
	}
	if err := iter.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("error iterating SystemAdminCredentials: %w", err))
	}

	// Move the operation to Deleting so the poll controller takes over.
	replacement := operation.DeepCopy()
	replacement.Status = arm.ProvisioningStateDeleting

	_, err = c.resourcesDBClient.Operations(key.SubscriptionID).Replace(ctx, replacement, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
