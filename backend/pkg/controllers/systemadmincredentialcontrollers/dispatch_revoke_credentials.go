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

package systemadmincredentialcontrollers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/operationcontrollers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type dispatchRevokeCredentials struct {
	clock             utilsclock.PassiveClock
	resourcesDBClient database.ResourcesDBClient
}

// NewDispatchRevokeCredentialsController returns a Controller that handles the
// first step of a RevokeCredentials operation: it creates a single
// SystemAdminCredentialRevocation document nested under the cluster, records its
// resource ID on the operation's InternalID, and moves the operation to
// Deleting. The actual revocation work (marking credential requests for
// deletion, driving the CertificateRevocationRequest desires, and tearing them
// down) is performed by the dedicated SystemAdminCredentialRevocation
// controllers. The operation completes once the revocation document is gone.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters
//	     Request: RevokeCredentials
//	      Status: Accepted
//	  InternalID: an empty value
func NewDispatchRevokeCredentialsController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &dispatchRevokeCredentials{
		clock:             clock,
		resourcesDBClient: resourcesDBClient,
	}

	controller := operationcontrollers.NewGenericOperationController(
		"SystemAdminCredentialDispatchRevokeCredentials",
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
	if operation.Request != api.OperationRequestRevokeCredentials {
		return false
	}
	if operation.Status != arm.ProvisioningStateAccepted {
		return false
	}
	return true
}

func (c *dispatchRevokeCredentials) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking revoke operation")

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

	cluster, err := c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).Get(ctx, operation.ExternalID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	// Verify the operation matches the cluster's revoke sentinel.
	if cluster.ServiceProviderProperties.RevokeCredentialsOperationID != operation.OperationID.Name {
		logger.Info("operation does not match cluster's RevokeCredentialsOperationID, skipping")
		return nil
	}

	// A revoke operation ID is a UUID; derive a short, stable suffix used to
	// name the revocation document and its CRR objects.
	revokeOpSuffix := strings.ReplaceAll(operation.OperationID.Name, "-", "")
	if len(revokeOpSuffix) > 16 {
		revokeOpSuffix = revokeOpSuffix[:16]
	}

	revocationCRUD := c.resourcesDBClient.SystemAdminCredentialRevocations(
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName,
		operation.ExternalID.Name,
	)

	revocationResourceID, err := api.ToSystemAdminCredentialRevocationResourceID(
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName,
		operation.ExternalID.Name,
		revokeOpSuffix,
	)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build revocation resource ID: %w", err))
	}

	// Create the revocation document if it does not already exist.
	if _, err := revocationCRUD.Get(ctx, revokeOpSuffix); database.IsNotFoundError(err) {
		newRevocation := &api.SystemAdminCredentialRevocation{
			CosmosMetadata: api.CosmosMetadata{
				ResourceID:   revocationResourceID,
				PartitionKey: strings.ToLower(operation.ExternalID.SubscriptionID),
			},
			Spec: api.SystemAdminCredentialRevocationSpec{
				OperationID:    operation.OperationID.Name,
				RevokeOpSuffix: revokeOpSuffix,
			},
		}
		if _, err := revocationCRUD.Create(ctx, newRevocation, nil); err != nil && !database.IsConflictError(err) {
			return utils.TrackError(fmt.Errorf("failed to create SystemAdminCredentialRevocation: %w", err))
		}
	} else if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get SystemAdminCredentialRevocation: %w", err))
	}

	// Record the revocation's resource ID on the operation and move it to
	// Deleting so the poll controller waits for the revocation to complete.
	internalID, err := api.NewInternalID(revocationResourceID.String())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to parse revocation resource ID: %w", err))
	}

	replacement := operation.DeepCopy()
	replacement.InternalID = internalID
	replacement.Status = arm.ProvisioningStateDeleting
	replacement.LastTransitionTime = c.clock.Now()
	if _, err := c.resourcesDBClient.Operations(key.SubscriptionID).Replace(ctx, replacement, nil); err != nil {
		return utils.TrackError(err)
	}

	logger.Info("dispatched revocation", "revokeOpSuffix", revokeOpSuffix, "revocation_resource_id", revocationResourceID.String())
	return nil
}
