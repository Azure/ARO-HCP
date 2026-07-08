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
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
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
	"github.com/Azure/ARO-HCP/internal/utils"
)

type revocationCompletion struct {
	cooldownChecker   controllerutil.CooldownChecker
	clock             utilsclock.PassiveClock
	resourcesDBClient database.ResourcesDBClient
	readDesireLister  dblisters.ReadDesireLister
}

var _ controllerutils.RevocationSyncer = (*revocationCompletion)(nil)

// NewRevocationCompletionController returns a RevocationWatchingController that
// observes the mirrored CertificateRevocationRequest (created by the
// revocation-desires controller) and drives a revocation to completion. It marks
// CertificatesRevoked once the hosted cluster confirms the previously-issued
// certificates are revoked and, once the credential requests have also been
// marked for deletion, marks the revocation Complete and stamps its
// DeleteTimestamp so the deletion controller can tear everything down.
//
// This logic is intentionally separate from the revocation-desires controller so
// that desire creation and revocation completion are independent concerns.
func NewRevocationCompletionController(
	clock utilsclock.PassiveClock,
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	backendInformers informers.BackendInformers,
	readDesireLister dblisters.ReadDesireLister,
) controllerutils.Controller {
	syncer := &revocationCompletion{
		cooldownChecker:   controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		clock:             clock,
		resourcesDBClient: resourcesDBClient,
		readDesireLister:  readDesireLister,
	}

	return controllerutils.NewRevocationWatchingController(
		"SystemAdminCredentialRevocationCompletion",
		resourcesDBClient,
		backendInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *revocationCompletion) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *revocationCompletion) SyncOnce(ctx context.Context, key controllerutils.SystemAdminCredentialRevocationKey) error {
	logger := utils.LoggerFromContext(ctx)

	revocationCRUD := c.resourcesDBClient.SystemAdminCredentialRevocations(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	revocation, err := revocationCRUD.Get(ctx, key.RevocationName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get SystemAdminCredentialRevocation: %w", err))
	}

	// Once the revocation is marked for deletion the deletion controller owns it.
	if revocation.Status.DeleteTimestamp != nil {
		return nil
	}

	// Observe the mirrored CRR to confirm the certificates have been revoked.
	if !revocation.Status.IsCertificatesRevoked() {
		cachedCRR, err := maestrohelpers.GetCachedCertificateRevocationRequestForCluster(
			ctx, c.readDesireLister,
			key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, revocation.Spec.RevokeOpSuffix)
		if err != nil {
			return utils.TrackError(err)
		}
		if cachedCRR == nil {
			logger.Info("waiting for CertificateRevocationRequest to be mirrored back from the hosted cluster")
			return nil
		}
		revoked := false
		for _, cond := range cachedCRR.Status.Conditions {
			if cond.Type == "PreviousCertificatesRevoked" && cond.Status == metav1.ConditionTrue {
				revoked = true
				break
			}
		}
		if !revoked {
			logger.Info("waiting for the hosted cluster to confirm certificate revocation")
			return nil
		}
		replacement := revocation.DeepCopy()
		meta.SetStatusCondition(&replacement.Status.Conditions, metav1.Condition{
			Type:    api.SystemAdminCredentialRevocationConditionCertificatesRevoked,
			Status:  metav1.ConditionTrue,
			Reason:  "CertificatesRevoked",
			Message: "Hosted cluster confirmed previously-issued certificates are revoked",
		})
		if _, err := revocationCRUD.Replace(ctx, replacement, nil); err != nil {
			if database.IsPreconditionFailedError(err) {
				return nil
			}
			return utils.TrackError(fmt.Errorf("failed to set CertificatesRevoked condition: %w", err))
		}
		revocation = replacement
	}

	// The revocation is complete once the certificates are revoked and every
	// credential request has been marked for deletion. Stamp the DeleteTimestamp
	// so the deletion controller can tear the desires down and remove the doc.
	if revocation.Status.IsCertificatesRevoked() && revocation.Status.IsCredentialsMarkedForDeletion() {
		replacement := revocation.DeepCopy()
		meta.SetStatusCondition(&replacement.Status.Conditions, metav1.Condition{
			Type:    api.SystemAdminCredentialRevocationConditionComplete,
			Status:  metav1.ConditionTrue,
			Reason:  "Complete",
			Message: "Revocation is complete and ready for teardown",
		})
		now := metav1.NewTime(c.clock.Now())
		replacement.Status.DeleteTimestamp = &now
		if _, err := revocationCRUD.Replace(ctx, replacement, nil); err != nil {
			if database.IsPreconditionFailedError(err) {
				return nil
			}
			return utils.TrackError(fmt.Errorf("failed to mark revocation complete: %w", err))
		}
		logger.Info("revocation complete, marked for deletion")
	}

	return nil
}
