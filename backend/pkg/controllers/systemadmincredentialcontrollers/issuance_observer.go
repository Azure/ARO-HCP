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

package systemadmincredentialcontrollers

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	systemadmincredhelpers "github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// issuanceObserverSyncer watches the CSR ReadDesire for each
// SystemAdminCredential in Phase=Requested. When the mirrored CSR
// shows a populated .status.certificate, it flips the credential
// to Issued. When the CSR is denied or failed, it flips to Failed.
type issuanceObserverSyncer struct {
	cooldownChecker controllerutil.CooldownChecker

	resourcesDBClient database.ResourcesDBClient
	readDesireLister  dblisters.ReadDesireLister
}

var _ controllerutils.ClusterSyncer = (*issuanceObserverSyncer)(nil)

// NewIssuanceObserverController wires the SystemAdminCredential
// issuance observer as a cluster-watching controller.
func NewIssuanceObserverController(
	resourcesDBClient database.ResourcesDBClient,
	readDesireLister dblisters.ReadDesireLister,
	backendInformers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	syncer := &issuanceObserverSyncer{
		cooldownChecker:   controllerutil.NewTimeBasedCooldownChecker(30 * time.Second),
		resourcesDBClient: resourcesDBClient,
		readDesireLister:  readDesireLister,
	}

	return controllerutils.NewClusterWatchingController(
		"SystemAdminCredentialIssuanceObserver",
		resourcesDBClient,
		backendInformers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *issuanceObserverSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *issuanceObserverSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	credCRUD := c.resourcesDBClient.SystemAdminCredentials(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	iter, err := credCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list SystemAdminCredentials: %w", err))
	}

	for _, cred := range iter.Items(ctx) {
		if cred.Status.Phase != api.SystemAdminCredentialPhaseRequested {
			continue
		}

		credName := cred.GetResourceID().Name
		desireName := systemadmincredhelpers.DesireNameCSR(credName)

		csr, err := maestrohelpers.GetCachedCSRForSystemAdminCredential(
			ctx, c.readDesireLister,
			key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, desireName,
		)
		if err != nil {
			return utils.TrackError(err)
		}
		if csr == nil {
			// ReadDesire not created yet or not observed yet
			continue
		}

		// Check for denial or failure conditions
		for _, condition := range csr.Status.Conditions {
			if condition.Type == "Denied" || condition.Type == "Failed" {
				logger.Info("CSR denied or failed", "credentialName", credName, "conditionType", condition.Type, "reason", condition.Reason)
				replacement := cred.DeepCopy()
				replacement.Status.Phase = api.SystemAdminCredentialPhaseFailed
				if _, err := credCRUD.Replace(ctx, replacement, nil); err != nil {
					return utils.TrackError(fmt.Errorf("failed to mark credential as failed: %w", err))
				}
				continue
			}
		}

		// Check for issued certificate
		if len(csr.Status.Certificate) == 0 {
			continue
		}

		logger.Info("CSR certificate issued", "credentialName", credName)
		replacement := cred.DeepCopy()
		replacement.Status.Phase = api.SystemAdminCredentialPhaseIssued
		replacement.Status.SignedCertificate = base64.StdEncoding.EncodeToString(csr.Status.Certificate)
		if _, err := credCRUD.Replace(ctx, replacement, nil); err != nil {
			return utils.TrackError(fmt.Errorf("failed to mark credential as issued: %w", err))
		}
	}

	if err := iter.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("error iterating SystemAdminCredentials: %w", err))
	}

	return nil
}
