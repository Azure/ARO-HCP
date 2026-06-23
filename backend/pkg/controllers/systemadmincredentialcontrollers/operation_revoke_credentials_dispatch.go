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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/operationcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// operationRevokeCredentialsDispatch is controller #4 (shrunken). Its
// only responsibilities now are:
//
//  1. Validate that the cluster's RevokeCredentialsOperationID sentinel
//     still points at this operation (the customer may have superseded
//     us with a newer revoke).
//  2. Create the per-revoke SystemAdminRevocation document, named by the
//     16-char revoke suffix derived from the operation ID.
//  3. Flip the ARM operation to Deleting.
//
// The two SystemAdminRevocationWatching controllers then take over:
//   - revocationCredentialDeletionInitiator sets Spec.DeletionTimestamp
//     on every live SystemAdminCredential under the cluster, handing
//     each to the credentialDesiresCreator teardown branch.
//   - revocationDesiresCreator owns the per-revoke CRR ApplyDesire /
//     ReadDesire + revocation RBAC ApplyDesires and flips
//     SystemAdminRevocationCompleteConditionType to True when the CRR
//     confirms PreviousCertificatesRevoked and teardown finishes.
//
// operationRevokeCredentialsPoll polls the SystemAdminRevocation for
// that completion condition and drives the ARM op to Succeeded.
type operationRevokeCredentialsDispatch struct {
	clock              utilsclock.PassiveClock
	clusterLister      listers.ClusterLister
	resourcesDBClient  database.ResourcesDBClient
	notificationClient *http.Client
}

func NewOperationRevokeCredentialsDispatchController(
	clock utilsclock.PassiveClock,
	clusterLister listers.ClusterLister,
	resourcesDBClient database.ResourcesDBClient,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &operationRevokeCredentialsDispatch{
		clock:              clock,
		clusterLister:      clusterLister,
		resourcesDBClient:  resourcesDBClient,
		notificationClient: notificationClient,
	}
	return operationcontrollers.NewGenericOperationController(
		"SystemAdminCredentialRevokeDispatch",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)
}

func (c *operationRevokeCredentialsDispatch) ShouldProcess(ctx context.Context, op *api.Operation) bool {
	if op.Status.IsTerminal() {
		return false
	}
	if op.Request != database.OperationRequestRevokeCredentials {
		return false
	}
	if op.Status != arm.ProvisioningStateAccepted {
		return false
	}
	if op.ExternalID == nil {
		return false
	}
	return true
}

func (c *operationRevokeCredentialsDispatch) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)

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

	clusterRID := op.ExternalID
	cluster, err := c.clusterLister.Get(ctx, clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name)
	if database.IsNotFoundError(err) {
		// Cluster gone; close the operation as Succeeded — nothing to
		// revoke.
		return patchOperationStatus(ctx, c.clock, c.resourcesDBClient, op, arm.ProvisioningStateSucceeded, nil, c.notificationClient)
	}
	if err != nil {
		return fmt.Errorf("get cluster: %w", err)
	}

	// Validate the cluster's RevokeCredentialsOperationID sentinel still
	// points at this op. If the customer cancelled (or a stale operation
	// hits this controller), bail.
	if cluster.ServiceProviderProperties.RevokeCredentialsOperationID != op.OperationID.Name {
		logger.Info("RevokeCredentialsOperationID does not match this operation; bailing",
			"clusterSentinel", cluster.ServiceProviderProperties.RevokeCredentialsOperationID,
			"operationID", op.OperationID.Name)
		return patchOperationStatus(ctx, c.clock, c.resourcesDBClient, op, arm.ProvisioningStateCanceled, &arm.CloudErrorBody{
			Code:    arm.CloudErrorCodeConflict,
			Message: "Revoke operation superseded",
		}, c.notificationClient)
	}

	revokeSuffix := systemadmincredential.RevokeOpSuffix(op.OperationID.Name)
	revocationCRUD := c.resourcesDBClient.HCPClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName).
		SystemAdminRevocations(clusterRID.Name)
	revocationResourceID := api.Must(api.ToSystemAdminRevocationResourceID(
		clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name, revokeSuffix))
	revocation := &api.SystemAdminRevocation{
		CosmosMetadata: api.CosmosMetadata{ResourceID: revocationResourceID},
		Spec: api.SystemAdminRevocationSpec{
			OperationID: op.OperationID.Name,
			RequestedAt: metav1.NewTime(c.clock.Now()),
		},
	}
	if _, err := revocationCRUD.Create(ctx, revocation, nil); err != nil && !database.IsConflictError(err) {
		return fmt.Errorf("create SystemAdminRevocation: %w", err)
	}

	// Hand off to the revocation controllers; the poller takes the
	// operation from Deleting to Succeeded once SystemAdminRevocation
	// reports RevocationComplete=True.
	return patchOperationStatus(ctx, c.clock, c.resourcesDBClient, op, arm.ProvisioningStateDeleting, nil, c.notificationClient)
}
