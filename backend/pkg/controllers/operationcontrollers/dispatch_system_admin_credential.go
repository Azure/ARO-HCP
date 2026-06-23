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
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	systemadmincredhelpers "github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
)

type dispatchSystemAdminCredential struct {
	clock             utilsclock.PassiveClock
	resourcesDBClient database.ResourcesDBClient
}

// NewDispatchSystemAdminCredentialController returns a new Controller instance
// that creates a SystemAdminCredential document and stamps the operation with
// the credential's resource ID.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters
//	     Request: RequestCredential
//	      Status: Accepted (empty InternalID)
func NewDispatchSystemAdminCredentialController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &dispatchSystemAdminCredential{
		clock:             clock,
		resourcesDBClient: resourcesDBClient,
	}

	return NewGenericOperationController(
		"DispatchSystemAdminCredential",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)
}

func (c *dispatchSystemAdminCredential) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestRequestCredential {
		return false
	}
	// This dispatch controller handles the initial creation only.
	// Once InternalID is set, the poll controller takes over.
	if operation.InternalID.String() != "" {
		return false
	}
	return true
}

func (c *dispatchSystemAdminCredential) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
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

	subID := operation.ExternalID.SubscriptionID
	rgName := operation.ExternalID.ResourceGroupName
	clusterName := operation.ExternalID.Name

	// Load the cluster to verify it exists and check for revoke-in-flight.
	cluster, err := c.resourcesDBClient.HCPClusters(subID, rgName).Get(ctx, clusterName)
	if err != nil {
		return utils.TrackError(err)
	}

	if len(cluster.ServiceProviderProperties.RevokeCredentialsOperationID) > 0 {
		logger.Info("revocation in progress, canceling credential request")
		replacement := operation.DeepCopy()
		apihelpers.CancelOperation(replacement, c.clock.Now())
		_, err = c.resourcesDBClient.Operations(key.SubscriptionID).Replace(ctx, replacement, nil)
		return utils.TrackError(err)
	}

	credCRUD := c.resourcesDBClient.SystemAdminCredentials(subID, rgName, clusterName)

	// Idempotency: check if a credential with this OperationID already exists.
	iter, err := credCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list SystemAdminCredentials: %w", err))
	}
	for _, cred := range iter.Items(ctx) {
		if cred.Spec.OperationID == operation.OperationID.Name {
			logger.Info("credential already exists for this operation, re-stamping InternalID")
			credResourceIDStr := api.ToSystemAdminCredentialResourceIDString(subID, rgName, clusterName, cred.GetResourceID().Name)
			replacement := operation.DeepCopy()
			replacement.InternalID = api.Must(api.NewInternalID(credResourceIDStr))
			_, err = c.resourcesDBClient.Operations(key.SubscriptionID).Replace(ctx, replacement, nil)
			return utils.TrackError(err)
		}
	}
	if err := iter.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("failed to list SystemAdminCredentials: %w", err))
	}

	// Generate keypair and credential name.
	pubPEM, privPEM, err := systemadmincredhelpers.GenerateKeypair()
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to generate keypair: %w", err))
	}

	credName := systemadmincredhelpers.GenerateCredentialName()

	// Create the SystemAdminCredential document.
	now := c.clock.Now()
	credResourceID := api.Must(api.ToSystemAdminCredentialResourceID(subID, rgName, clusterName, credName))

	credDoc := &api.SystemAdminCredential{}
	credDoc.SetResourceID(credResourceID)
	credDoc.SetPartitionKey(strings.ToLower(subID))
	credDoc.Spec = api.SystemAdminCredentialSpec{
		Username:            systemadmincredhelpers.DefaultUsername,
		OperationID:         operation.OperationID.Name,
		ExpirationTimestamp: metav1.NewTime(now.Add(24 * time.Hour)),
		PublicKeyPEM:        string(pubPEM),
		PrivateKeyPEM:       string(privPEM),
	}
	credDoc.Status = api.SystemAdminCredentialStatus{
		Phase: api.SystemAdminCredentialPhaseRequested,
	}

	logger.Info("creating SystemAdminCredential", "credentialName", credName)
	_, err = credCRUD.Create(ctx, credDoc, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create SystemAdminCredential: %w", err))
	}

	// Stamp the operation with the credential's resource ID.
	credResourceIDStr := api.ToSystemAdminCredentialResourceIDString(subID, rgName, clusterName, credName)
	replacement := operation.DeepCopy()
	replacement.InternalID = api.Must(api.NewInternalID(credResourceIDStr))

	_, err = c.resourcesDBClient.Operations(key.SubscriptionID).Replace(ctx, replacement, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
