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
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
)

type dispatchRequestCredential struct {
	clock              utilsclock.PassiveClock
	resourcesDBClient  corecosmosstorage.ResourcesDBClient
	clusterLister      corelisters.ClusterLister
	notificationClient *http.Client
}

const SystemAdminCredentialDispatchRequestCredentialControllerName = "SystemAdminCredentialDispatchRequestCredential"

// NewDispatchRequestCredentialController returns a Controller that creates a
// SystemAdminCredentialRequest Cosmos document when a RequestCredential operation is
// first dispatched. It reserves the credential resource ID while Accepted, then
// creates or adopts that document before moving the operation to Provisioning.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters
//	     Request: OperationRequestSystemAdminCredentialRequest ("RequestCredential")
//	      Status: Accepted
func NewDispatchRequestCredentialController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clusterLister corelisters.ClusterLister,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &dispatchRequestCredential{
		clock:              clock,
		resourcesDBClient:  resourcesDBClient,
		clusterLister:      clusterLister,
		notificationClient: notificationClient,
	}

	controller := controllerutils.NewGenericOperationController(
		SystemAdminCredentialDispatchRequestCredentialControllerName,
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *dispatchRequestCredential) ShouldProcess(ctx context.Context, operation *coreapi.Operation) bool {
	if operation.Status != coreapi.ProvisioningStateAccepted {
		return false
	}
	if operation.Request != coreapi.OperationRequestSystemAdminCredentialRequest {
		return false
	}
	if operation.SystemAdminCredentialRequest == nil {
		return false
	}
	return true
}

func (c *dispatchRequestCredential) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

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
	if expired, err := operationutils.ExpireCredentialOperation(ctx, c.clock, c.resourcesDBClient, operation, operationutils.PostAsyncNotificationFn(c.notificationClient)); expired || err != nil {
		return err
	}

	cluster, err := c.clusterLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	// Cancel the operation if a revocation is in progress.
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil || len(cluster.ServiceProviderProperties.RevokeCredentialsOperationID) > 0 || cluster.ServiceProviderProperties.SystemAdminCredentialRevocationResourceID != nil {
		logger.Info("revocation in progress, canceling operation",
			"revoke_credentials_operation_id", cluster.ServiceProviderProperties.RevokeCredentialsOperationID)

		replacement := operation.DeepCopy()
		apihelpers.CancelOperation(replacement, c.clock.Now())

		_, err = c.resourcesDBClient.Operations(key.SubscriptionID).Replace(ctx, replacement, nil)
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			return nil
		}
		if err != nil {
			return utils.TrackError(err)
		}

		return nil
	}

	operationIDStr := operation.OperationID.Name
	credCRUD := c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).SystemAdminCredentialRequests(
		operation.ExternalID.Name,
	)

	credResourceID := operation.SystemAdminCredentialRequest.SystemAdminCredentialRequestResourceID
	if credResourceID == nil {
		// Adopt random names written by older backends before reserving a new ID.
		iter, err := credCRUD.List(ctx, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to list SystemAdminCredentialRequests: %w", err))
		}
		for _, cred := range iter.Items(ctx) {
			if cred.Spec.OperationID == operationIDStr {
				credResourceID = cred.ResourceID
				break
			}
		}
		if err := iter.GetError(); err != nil {
			return utils.TrackError(fmt.Errorf("failed to iterate SystemAdminCredentialRequests: %w", err))
		}

		if credResourceID == nil {
			if operation.SystemAdminCredentialRequest.CertificateSigningRequest == "" {
				return fmt.Errorf("operation %s has no CertificateSigningRequest", operation.OperationID.Name)
			}
			credName := fmt.Sprintf("%x", sha256.Sum256([]byte(operationIDStr)))[:16]
			credResourceID, err = coreapihelpers.ToSystemAdminCredentialRequestResourceID(
				operation.ExternalID.SubscriptionID,
				operation.ExternalID.ResourceGroupName,
				operation.ExternalID.Name,
				credName,
			)
			if err != nil {
				return utils.TrackError(fmt.Errorf("failed to build credential resource ID: %w", err))
			}
		}
		replacement := operation.DeepCopy()
		replacement.SystemAdminCredentialRequest.SystemAdminCredentialRequestResourceID = credResourceID
		_, err = c.resourcesDBClient.Operations(key.SubscriptionID).Replace(ctx, replacement, nil)
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			return nil
		}
		return err
	}

	// Recover an interrupted dispatch using the identity already persisted above.
	credName := credResourceID.Name
	if existing, err := credCRUD.Get(ctx, credName); cosmosstorageutils.IsNotFoundError(err) {
		if operation.SystemAdminCredentialRequest.CertificateSigningRequest == "" {
			return fmt.Errorf("operation %s has no CertificateSigningRequest", operation.OperationID.Name)
		}
		newCred := &coreapi.SystemAdminCredentialRequest{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   credResourceID,
				PartitionKey: strings.ToLower(operation.ExternalID.SubscriptionID),
			},
			Spec: coreapi.SystemAdminCredentialRequestSpec{
				CreationTimestamp:            metav1.NewTime(c.clock.Now()),
				ExpirationTimestamp:          metav1.NewTime(c.clock.Now().Add(24 * time.Hour)),
				OperationID:                  operationIDStr,
				CertificateSigningRequestPEM: operation.SystemAdminCredentialRequest.CertificateSigningRequest,
			},
			Status: coreapi.SystemAdminCredentialRequestStatus{},
		}

		_, err = credCRUD.Create(ctx, newCred, nil)
		if cosmosstorageutils.IsConflictError(err) {
			return nil // Re-read and verify operation identity on the next reconcile.
		} else if err != nil {
			return utils.TrackError(fmt.Errorf("failed to create SystemAdminCredentialRequest: %w", err))
		}
	} else if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get SystemAdminCredentialRequest: %w", err))
	} else if existing.Spec.OperationID != operationIDStr {
		return fmt.Errorf("credential name belongs to another operation")
	}

	replacement := operation.DeepCopy()
	replacement.Status = coreapi.ProvisioningStateProvisioning
	replacement.LastTransitionTime = c.clock.Now()

	_, err = c.resourcesDBClient.Operations(key.SubscriptionID).Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	logger.Info("dispatched SystemAdminCredential", "credential_name", credName, "credential_resource_id", credResourceID.String())

	return nil
}
