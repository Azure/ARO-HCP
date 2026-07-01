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
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/operationcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationRevokeCredentialsPoll struct {
	clock              utilsclock.PassiveClock
	resourcesDBClient  database.ResourcesDBClient
	readDesireLister   dblisters.ReadDesireLister
	notificationClient *http.Client
}

// NewOperationRevokeCredentialsPollController returns a Controller that follows
// a RevokeCredentials operation through its three-phase lifecycle:
//   - Phase R-1: wait for the CRR to confirm PreviousCertificatesRevoked=True
//   - Phase R-2: flip per-credential docs to Revoked, clear their private keys
//   - Phase R-3: clear the cluster sentinel, mark the operation Succeeded
func NewOperationRevokeCredentialsPollController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	readDesireLister dblisters.ReadDesireLister,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &operationRevokeCredentialsPoll{
		clock:              clock,
		resourcesDBClient:  resourcesDBClient,
		readDesireLister:   readDesireLister,
		notificationClient: notificationClient,
	}

	controller := operationcontrollers.NewGenericOperationController(
		"SystemAdminCredentialOperationRevokeCredentialsPoll",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *operationRevokeCredentialsPoll) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != api.OperationRequestRevokeCredentials {
		return false
	}
	if operation.Status != arm.ProvisioningStateDeleting {
		return false
	}
	return true
}

func (c *operationRevokeCredentialsPoll) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking revoke operation poll")

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

	revokeOpSuffix := strings.ReplaceAll(operation.OperationID.Name, "-", "")
	if len(revokeOpSuffix) > 16 {
		revokeOpSuffix = revokeOpSuffix[:16]
	}

	// Phase R-1: check if the CRR has confirmed revocation.
	cachedCRR, err := maestrohelpers.GetCachedCertificateRevocationRequestForCluster(
		ctx, c.readDesireLister,
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName,
		operation.ExternalID.Name,
		revokeOpSuffix,
	)
	if err != nil {
		return utils.TrackError(err)
	}
	if cachedCRR == nil {
		// CRR not yet mirrored; wait for next reconcile.
		logger.Info("CRR not yet mirrored")
		return nil
	}

	// Check for PreviousCertificatesRevoked condition.
	revoked := false
	for _, cond := range cachedCRR.Status.Conditions {
		if cond.Type == "PreviousCertificatesRevoked" && cond.Status == metav1.ConditionTrue {
			revoked = true
			break
		}
	}
	if !revoked {
		logger.Info("CRR has not yet confirmed revocation")
		return nil
	}

	// Phase R-2: flip all AwaitingRevocation credentials to Revoked.
	credCRUD := c.resourcesDBClient.SystemAdminCredentialRequests(
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName,
		operation.ExternalID.Name,
	)

	allRevoked := true
	iter, err := credCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list credentials: %w", err))
	}

	now := c.clock.Now()
	for _, cred := range iter.Items(ctx) {
		if !cred.Status.IsAwaitingRevocation() {
			continue
		}

		replacement := cred.DeepCopy()
		replacement.Status.SetCondition(api.SystemAdminCredentialRequestConditionRevoked, metav1.ConditionTrue, "Revoked", "Credential has been revoked")
		revokedAt := metav1.NewTime(now)
		replacement.Status.RevokedAt = &revokedAt
		// Zero out private key.
		replacement.Spec.PrivateKeyPEM = ""

		if _, err := credCRUD.Replace(ctx, replacement, nil); err != nil {
			allRevoked = false
			logger.Error(err, "failed to revoke credential", "credential", cred.ResourceID.Name)
		}
	}
	if err := iter.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("failed to iterate credentials: %w", err))
	}

	if !allRevoked {
		return fmt.Errorf("not all credentials could be revoked")
	}

	// Phase R-3: clear cluster sentinel and mark operation Succeeded.
	cluster, err := c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).Get(ctx, operation.ExternalID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	clusterReplacement := cluster.DeepCopy()
	clusterReplacement.ServiceProviderProperties.RevokeCredentialsOperationID = ""
	if _, err := c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).Replace(ctx, clusterReplacement, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to clear RevokeCredentialsOperationID: %w", err))
	}

	var notifyFn operationcontrollers.PostAsyncNotificationFunc
	if c.notificationClient != nil {
		client := c.notificationClient
		notifyFn = func(ctx context.Context, op *api.Operation) error {
			return operationcontrollers.PostAsyncNotification(ctx, client, op)
		}
	}
	err = operationcontrollers.UpdateOperationStatus(ctx, c.clock, c.resourcesDBClient, operation, arm.ProvisioningStateSucceeded, nil, notifyFn)
	if err != nil {
		return utils.TrackError(err)
	}

	logger.Info("revocation complete")
	return nil
}
