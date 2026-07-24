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

	"github.com/google/uuid"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/operationcontrollers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
)

type dispatchRequestCredential struct {
	clock             utilsclock.PassiveClock
	resourcesDBClient database.ResourcesDBClient
}

// NewDispatchRequestCredentialController returns a Controller that creates a
// SystemAdminCredential Cosmos document when a RequestCredential operation is
// first dispatched. It generates the RSA keypair in-process, writes the
// credential document, and stamps Operation.InternalID so downstream
// controllers can find it.
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
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &dispatchRequestCredential{
		clock:             clock,
		resourcesDBClient: resourcesDBClient,
	}

	controller := operationcontrollers.NewGenericOperationController(
		"SystemAdminCredentialDispatchRequestCredential",
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
	if operation.Request != api.OperationRequestRequestCredential {
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

	// Cancel the operation if a revocation is in progress.
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

	// Idempotency: check if a credential doc for this operation already exists.
	operationIDStr := operation.OperationID.Name
	credCRUD := c.resourcesDBClient.SystemAdminCredentialRequests(
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName,
		operation.ExternalID.Name,
	)

	// List existing credentials and check for one matching this operation.
	iter, err := credCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list SystemAdminCredentialRequests: %w", err))
	}
	for _, cred := range iter.Items(ctx) {
		if cred.Spec.OperationID == operationIDStr {
			// Credential already exists for this operation. Just stamp InternalID.
			credResourceID := cred.ResourceID
			internalID, err := api.NewInternalID(credResourceID.String())
			if err != nil {
				return utils.TrackError(fmt.Errorf("failed to parse credential resource ID: %w", err))
			}
			replacement := operation.DeepCopy()
			replacement.InternalID = internalID
			_, err = c.resourcesDBClient.Operations(key.SubscriptionID).Replace(ctx, replacement, nil)
			if err != nil {
				return utils.TrackError(err)
			}
			return nil
		}
	}
	if err := iter.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("failed to iterate SystemAdminCredentialRequests: %w", err))
	}

	if operation.CertificateRequest == "" {
		return fmt.Errorf("operation %s has no CertificateRequest", operation.OperationID.Name)
	}

	// Generate a credential name: first 16 hex chars of a new UUID.
	credName := strings.ReplaceAll(uuid.New().String(), "-", "")[:16]

	// Determine the username from the operation's client identity.
	username := operation.ClientID
	if username == "" {
		username = "system-admin"
	}
	username = fmt.Sprintf("system:customer-break-glass:%s", username)

	// Build the credential resource ID.
	credResourceID, err := api.ToSystemAdminCredentialRequestResourceID(
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName,
		operation.ExternalID.Name,
		credName,
	)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build credential resource ID: %w", err))
	}

	// Create the credential document.
	newCred := &api.SystemAdminCredentialRequest{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   credResourceID,
			PartitionKey: strings.ToLower(operation.ExternalID.SubscriptionID),
		},
		Spec: api.SystemAdminCredentialRequestSpec{
			Username:              username,
			CreationTimestamp:     metav1.NewTime(c.clock.Now()),
			ExpirationTimestamp:   metav1.NewTime(c.clock.Now().Add(24 * time.Hour)),
			OperationID:           operationIDStr,
			CertificateRequestPEM: operation.CertificateRequest,
		},
		Status: api.SystemAdminCredentialRequestStatus{},
	}

	_, err = credCRUD.Create(ctx, newCred, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create SystemAdminCredentialRequest: %w", err))
	}

	// Stamp Operation.InternalID so downstream controllers can find the credential.
	internalID, err := api.NewInternalID(credResourceID.String())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to parse credential resource ID: %w", err))
	}

	replacement := operation.DeepCopy()
	replacement.InternalID = internalID

	_, err = c.resourcesDBClient.Operations(key.SubscriptionID).Replace(ctx, replacement, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	logger.Info("dispatched SystemAdminCredential",
		"credential_name", credName,
		"credential_resource_id", credResourceID.String())

	return nil
}
