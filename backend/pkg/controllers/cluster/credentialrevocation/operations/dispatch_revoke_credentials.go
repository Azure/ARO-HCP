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
	"strings"
	"time"

	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type dispatchRevokeCredentials struct {
	clock             utilsclock.PassiveClock
	resourcesDBClient corecosmosstorage.ResourcesDBClient
}

// NewDispatchRevokeCredentialsController returns a Controller that handles the
// first step of a RevokeCredentials operation: it creates a single
// SystemAdminCredentialRevocation document nested under the cluster, records its
// resource ID on SystemAdminCredentialRevocation, and moves the operation to
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
//	  SystemAdminCredentialRevocation: nil
func NewDispatchRevokeCredentialsController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &dispatchRevokeCredentials{
		clock:             clock,
		resourcesDBClient: resourcesDBClient,
	}

	controller := controllerutils.NewGenericOperationController(
		"SystemAdminCredentialDispatchRevokeCredentials",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *dispatchRevokeCredentials) ShouldProcess(ctx context.Context, operation *coreapi.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != coreapi.OperationRequestSystemAdminCredentialRevocation {
		return false
	}
	if operation.Status != coreapi.ProvisioningStateAccepted {
		return false
	}
	return true
}

func (c *dispatchRevokeCredentials) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking revoke operation")

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

	revocationCRUD := c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).SystemAdminCredentialRevocations(
		operation.ExternalID.Name,
	)

	revocationResourceID, err := coreapi.ToSystemAdminCredentialRevocationResourceID(
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName,
		operation.ExternalID.Name,
		revokeOpSuffix,
	)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build revocation resource ID: %w", err))
	}

	// Create the revocation document if it does not already exist.
	if _, err := revocationCRUD.Get(ctx, revokeOpSuffix); cosmosstorageutils.IsNotFoundError(err) {
		newRevocation := &coreapi.SystemAdminCredentialRevocation{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   revocationResourceID,
				PartitionKey: strings.ToLower(operation.ExternalID.SubscriptionID),
			},
			Spec: coreapi.SystemAdminCredentialRevocationSpec{
				OperationID:    operation.OperationID.Name,
				RevokeOpSuffix: revokeOpSuffix,
			},
		}
		if _, err := revocationCRUD.Create(ctx, newRevocation, nil); err != nil && !cosmosstorageutils.IsConflictError(err) {
			return utils.TrackError(fmt.Errorf("failed to create SystemAdminCredentialRevocation: %w", err))
		}
	} else if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get SystemAdminCredentialRevocation: %w", err))
	}

	replacement := operation.DeepCopy()
	replacement.SystemAdminCredentialRevocation = &coreapi.OperationSystemAdminCredentialRevocation{
		SystemAdminCredentialRevocationResourceID: revocationResourceID,
	}
	replacement.Status = coreapi.ProvisioningStateDeleting
	replacement.LastTransitionTime = c.clock.Now()
	if _, err := c.resourcesDBClient.Operations(key.SubscriptionID).Replace(ctx, replacement, nil); err != nil {
		return utils.TrackError(err)
	}

	logger.Info("dispatched revocation", "revokeOpSuffix", revokeOpSuffix, "revocation_resource_id", revocationResourceID.String())
	return nil
}
