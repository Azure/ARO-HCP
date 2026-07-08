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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
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

var _ controllerutils.CredentialRequestSyncer = (*issuanceObserver)(nil)

// NewIssuanceObserverController returns a CredentialRequestWatchingController
// that observes the mirrored CSR from the ReadDesire and transitions
// individual SystemAdminCredentialRequest documents from Pending → Issued (or Failed).
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

	return controllerutils.NewCredentialRequestWatchingController(
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

func (c *issuanceObserver) SyncOnce(ctx context.Context, key controllerutils.SystemAdminCredentialRequestKey) error {
	// Get the specific credential request from Cosmos.
	credCRUD := c.resourcesDBClient.SystemAdminCredentialRequests(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	cred, err := credCRUD.Get(ctx, key.CredentialName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get SystemAdminCredentialRequest: %w", err))
	}

	// Only process credentials that are pending.
	if !cred.Status.IsPending() {
		return nil
	}

	return c.observeCSR(ctx, key, cred, key.CredentialName, credCRUD)
}

func (c *issuanceObserver) observeCSR(
	ctx context.Context,
	key controllerutils.SystemAdminCredentialRequestKey,
	cred *api.SystemAdminCredentialRequest,
	credName string,
	credCRUD database.ResourceCRUD[api.SystemAdminCredentialRequest, *api.SystemAdminCredentialRequest],
) error {
	logger := utils.LoggerFromContext(ctx)
	cachedCSR, err := kubeapplierhelpers.GetCachedCSRForSystemAdminCredentialRequest(
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
	meta.SetStatusCondition(&replacement.Status.Conditions, metav1.Condition{
		Type:    api.SystemAdminCredentialRequestConditionIssued,
		Status:  metav1.ConditionTrue,
		Reason:  "Issued",
		Message: "CSR has been signed",
	})
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
	meta.SetStatusCondition(&replacement.Status.Conditions, metav1.Condition{
		Type:    api.SystemAdminCredentialRequestConditionFailed,
		Status:  metav1.ConditionTrue,
		Reason:  "CSRFailed",
		Message: message,
	})

	_, err := credCRUD.Replace(ctx, replacement, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to update credential to Failed: %w", err))
	}
	return nil
}
