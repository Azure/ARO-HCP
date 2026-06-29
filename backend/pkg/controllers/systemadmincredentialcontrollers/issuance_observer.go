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
	"encoding/base64"
	"fmt"
	"time"

	certificatesv1 "k8s.io/api/certificates/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type issuanceObserver struct {
	cooldownChecker   controllerutil.CooldownChecker
	clock             utilsclock.PassiveClock
	resourcesDBClient database.ResourcesDBClient
	readDesireLister  dblisters.ReadDesireLister
}

var _ controllerutils.ClusterSyncer = (*issuanceObserver)(nil)

// NewIssuanceObserverController returns a ClusterWatchingController that
// observes the mirrored CSR from the ReadDesire and transitions
// SystemAdminCredentialRequest documents from Requested → Issued (or Failed).
func NewIssuanceObserverController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	backendInformers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	readDesireLister dblisters.ReadDesireLister,
) controllerutils.Controller {
	syncer := &issuanceObserver{
		cooldownChecker:   controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		clock:             clock,
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

func (c *issuanceObserver) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *issuanceObserver) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.
		AddSubscriptionID(key.SubscriptionID).
		AddResourceGroup(key.ResourceGroupName).
		AddHCPClusterName(key.HCPClusterName)...)
	ctx = utils.ContextWithLogger(ctx, logger)

	// List all SystemAdminCredentialRequests for this cluster.
	credCRUD := c.resourcesDBClient.SystemAdminCredentialRequests(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	iter, err := credCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list SystemAdminCredentialRequests: %w", err))
	}

	for _, cred := range iter.Items(ctx) {
		// Only process credentials that are pending.
		if !cred.Status.IsPending() {
			continue
		}

		credName := cred.ResourceID.Name
		if err := c.observeCSR(ctx, key, cred, credName, credCRUD); err != nil {
			return err
		}
	}
	if err := iter.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("failed to iterate SystemAdminCredentialRequests: %w", err))
	}

	return nil
}

func (c *issuanceObserver) observeCSR(
	ctx context.Context,
	key controllerutils.HCPClusterKey,
	cred *api.SystemAdminCredentialRequest,
	credName string,
	credCRUD database.ResourceCRUD[api.SystemAdminCredentialRequest, *api.SystemAdminCredentialRequest],
) error {
	logger := utils.LoggerFromContext(ctx)

	cachedCSR, err := maestrohelpers.GetCachedCSRForSystemAdminCredentialRequest(
		ctx, c.readDesireLister,
		key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, credName,
	)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cached CSR for credential %s: %w", credName, err))
	}
	if cachedCSR == nil {
		// CSR not yet mirrored; wait for next reconcile.
		return nil
	}

	// Check for denial or failure.
	for _, cond := range cachedCSR.Status.Conditions {
		if cond.Type == certificatesv1.CertificateDenied && cond.Status == "True" {
			logger.Info("CSR denied", "credential", credName, "reason", cond.Reason)
			return c.failCredential(ctx, cred, credCRUD, fmt.Sprintf("CSR denied: %s", cond.Message))
		}
		if cond.Type == certificatesv1.CertificateFailed && cond.Status == "True" {
			logger.Info("CSR failed", "credential", credName, "reason", cond.Reason)
			return c.failCredential(ctx, cred, credCRUD, fmt.Sprintf("CSR failed: %s", cond.Message))
		}
	}

	// Check for signed certificate.
	if len(cachedCSR.Status.Certificate) == 0 {
		// Certificate not yet signed; wait for next reconcile.
		return nil
	}

	// Certificate is available. Transition to Issued.
	signedCert := base64.StdEncoding.EncodeToString(cachedCSR.Status.Certificate)

	replacement := cred.DeepCopy()
	replacement.Status.SetCondition(api.SystemAdminCredentialRequestConditionIssued, metav1.ConditionTrue, "Issued", "CSR has been signed")
	replacement.Status.SignedCertificate = signedCert

	_, err = credCRUD.Replace(ctx, replacement, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to update credential to Issued: %w", err))
	}

	logger.Info("credential issued", "credential", credName)
	return nil
}

func (c *issuanceObserver) failCredential(
	ctx context.Context,
	cred *api.SystemAdminCredentialRequest,
	credCRUD database.ResourceCRUD[api.SystemAdminCredentialRequest, *api.SystemAdminCredentialRequest],
	message string,
) error {
	replacement := cred.DeepCopy()
	replacement.Status.SetCondition(api.SystemAdminCredentialRequestConditionFailed, metav1.ConditionTrue, "CSRFailed", message)

	_, err := credCRUD.Replace(ctx, replacement, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to update credential to Failed: %w", err))
	}
	return nil
}
