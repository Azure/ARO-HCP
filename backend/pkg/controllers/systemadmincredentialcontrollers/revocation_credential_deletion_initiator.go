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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// revocationCredentialDeletionInitiator is the first of two
// SystemAdminRevocationWatching controllers. When a SystemAdminRevocation
// document is created or mutated, it lists every live SystemAdminCredential
// under the parent cluster directly from Cosmos (not the lister cache —
// a stale list could miss a credential created moments earlier and
// silently leave it un-revoked) and sets Spec.DeletionTimestamp on each.
//
// Credentials whose Spec.DeletionTimestamp is already set are skipped —
// some other revoke operation or the cluster-deletion path already
// initiated their teardown. credentialDesiresCreator's teardown branch
// drives the rest; the credential-deletion finalizer removes the
// credential doc once teardown is complete.
type revocationCredentialDeletionInitiator struct {
	clock             utilsclock.PassiveClock
	cooldownChecker   controllerutil.CooldownChecker
	resourcesDBClient database.ResourcesDBClient
}

func NewRevocationCredentialDeletionInitiatorController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	backendInformers informers.BackendInformers,
) controllerutils.Controller {
	syncer := &revocationCredentialDeletionInitiator{
		clock:             clock,
		cooldownChecker:   controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient: resourcesDBClient,
	}
	return controllerutils.NewSystemAdminRevocationWatchingController(
		"SystemAdminRevocationCredentialDeletionInitiator",
		resourcesDBClient,
		backendInformers,
		30*time.Second,
		syncer,
	)
}

func (c *revocationCredentialDeletionInitiator) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *revocationCredentialDeletionInitiator) SyncOnce(ctx context.Context, key controllerutils.HCPSystemAdminRevocationKey) error {
	logger := utils.LoggerFromContext(ctx)

	revocationCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).
		SystemAdminRevocations(key.HCPClusterName)
	revocation, err := revocationCRUD.Get(ctx, key.HCPSystemAdminRevocationName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("get SystemAdminRevocation: %w", err))
	}
	_ = revocation // currently no per-revocation gating; presence is enough.

	credentialsCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).
		SystemAdminCredentials(key.HCPClusterName)
	// Use a fresh Cosmos List rather than the lister cache: a credential
	// created seconds before the revoke would otherwise be missed.
	iter, err := credentialsCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("list credentials: %w", err))
	}
	now := metav1.NewTime(c.clock.Now())
	for _, credential := range iter.Items(ctx) {
		if credential == nil {
			continue
		}
		if credential.Spec.DeletionTimestamp != nil {
			continue
		}
		replacement := credential.DeepCopy()
		replacement.Spec.DeletionTimestamp = &now
		if _, err := credentialsCRUD.Replace(ctx, replacement, nil); database.IsPreconditionFailedError(err) {
			// Another writer beat us; informer re-enqueue or this controller's
			// resync will retry the credential.
			continue
		} else if err != nil {
			return utils.TrackError(fmt.Errorf("set DeletionTimestamp on credential %q: %w", credential.GetResourceID().Name, err))
		}
		logger.Info("revocation initiated credential deletion", "credential", credential.GetResourceID().Name)
	}
	if err := iter.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("iterate credentials: %w", err))
	}
	return nil
}
