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
	"net/http"
	"time"

	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/operationcontrollers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
)

// operationRequestCredentialPoll is controller #2. Replaces
// operation_request_credential.go: instead of calling cluster-service
// GetBreakGlassCredential, it looks up the linked SystemAdminCredential
// document and maps Status.Phase to an ARM ProvisioningState.
type operationRequestCredentialPoll struct {
	clock              utilsclock.PassiveClock
	resourcesDBClient  database.ResourcesDBClient
	notificationClient *http.Client
}

func NewOperationRequestCredentialPollController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &operationRequestCredentialPoll{
		clock:              clock,
		resourcesDBClient:  resourcesDBClient,
		notificationClient: notificationClient,
	}
	return operationcontrollers.NewGenericOperationController(
		"SystemAdminCredentialRequestPoll",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)
}

func (c *operationRequestCredentialPoll) ShouldProcess(ctx context.Context, op *api.Operation) bool {
	if op.Status.IsTerminal() {
		return false
	}
	if op.Request != database.OperationRequestRequestCredential {
		return false
	}
	if len(op.InternalID.String()) == 0 {
		return false
	}
	// Accept both SystemAdminCredential IDs (normal path) and legacy
	// cluster-service HREF IDs so that handleLegacyOperation can
	// explicitly fail the latter instead of leaving them non-terminal.
	return true
}

func (c *operationRequestCredentialPoll) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	op, err := c.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get operation: %w", err)
	}
	if !c.ShouldProcess(ctx, op) {
		return nil
	}

	// Legacy operations created by cluster-service carry an HREF-style
	// InternalID (e.g. /api/clusters_mgmt/v1/clusters/...) that is not
	// a SystemAdminCredential ARM resource ID. The dispatcher skips them
	// (InternalID is already set) and this poller cannot poll them. Fail
	// them explicitly so they do not remain non-terminal forever.
	if op.InternalID.Kind() != api.SystemAdminCredentialKind {
		return patchOperationStatus(ctx, c.clock, c.resourcesDBClient, op, arm.ProvisioningStateFailed, &arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeInternalServerError,
			Message: "Legacy credential operation is not supported by the new credential flow; please retry",
		}, c.notificationClient)
	}

	credRID, err := azcorearm.ParseResourceID(op.InternalID.String())
	if err != nil {
		return fmt.Errorf("parse InternalID as ARM resource ID: %w", err)
	}
	clusterRID := credRID.Parent
	if clusterRID == nil {
		return fmt.Errorf("credential resource ID has no parent cluster: %s", credRID.String())
	}

	credential, err := c.resourcesDBClient.HCPClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName).
		SystemAdminCredentials(clusterRID.Name).Get(ctx, credRID.Name)
	if database.IsNotFoundError(err) {
		// Dispatch has not yet persisted the credential (or the GC
		// already swept it); no-op.
		return nil
	}
	if err != nil {
		return fmt.Errorf("get credential: %w", err)
	}

	newStatus, newErrBody := mapCredentialPhaseToARMStatus(credential)

	// When the credential is Issued the operation would transition to
	// Succeeded, but OperationResult also needs ServingCABundle to build
	// a kubeconfig. If it is not yet available, keep the operation at
	// Provisioning so the frontend doesn't try to build a kubeconfig
	// with a missing CA.
	if newStatus == arm.ProvisioningStateSucceeded {
		spc, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, clusterRID)
		if err != nil {
			return fmt.Errorf("get ServiceProviderCluster: %w", err)
		}
		if spc.Status.ServingCABundle == "" {
			// CA bundle not synced yet — stay at Provisioning.
			return nil
		}
	}

	return patchOperationStatus(ctx, c.clock, c.resourcesDBClient, op, newStatus, newErrBody, c.notificationClient)
}

// mapCredentialPhaseToARMStatus translates the credential's Phase to an
// ARM ProvisioningState the customer's OperationResult sees.
func mapCredentialPhaseToARMStatus(credential *api.SystemAdminCredential) (arm.ProvisioningState, *arm.CloudErrorBody) {
	switch credential.Status.Phase {
	case api.SystemAdminCredentialPhaseRequested, api.SystemAdminCredentialPhaseAwaitingRevocation:
		return arm.ProvisioningStateProvisioning, nil
	case api.SystemAdminCredentialPhaseIssued:
		return arm.ProvisioningStateSucceeded, nil
	case api.SystemAdminCredentialPhaseFailed:
		return arm.ProvisioningStateFailed, &arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeInternalServerError,
			Message: "Failed to provision cluster credential",
		}
	case api.SystemAdminCredentialPhaseRevoked:
		// Customer should not see a freshly-issued credential land on
		// Revoked under a request operation — but if it does, surface
		// as Failed so the customer can retry.
		return arm.ProvisioningStateFailed, &arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeConflict,
			Message: "Credential was revoked before issuance completed",
		}
	}
	// Default: still provisioning.
	return arm.ProvisioningStateProvisioning, nil
}
