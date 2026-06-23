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
	"fmt"
	"time"

	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// revokedRetentionDuration is how long a Revoked credential doc
	// is kept before being garbage collected. 48h is deliberately
	// longer than the certificate's 24h TTL.
	revokedRetentionDuration = 48 * time.Hour
)

// revokedGCSyncer deletes SystemAdminCredential docs whose Phase
// is Revoked and whose RevokedAt + 48h has passed.
type revokedGCSyncer struct {
	cooldownChecker controllerutil.CooldownChecker

	clock             utilsclock.PassiveClock
	resourcesDBClient database.ResourcesDBClient
}

var _ controllerutils.ClusterSyncer = (*revokedGCSyncer)(nil)

// NewRevokedGCController wires the revoked-doc GC controller.
func NewRevokedGCController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	backendInformers informers.BackendInformers,
) controllerutils.Controller {
	syncer := &revokedGCSyncer{
		cooldownChecker:   controllerutil.NewTimeBasedCooldownChecker(30 * time.Second),
		clock:             clock,
		resourcesDBClient: resourcesDBClient,
	}

	return controllerutils.NewClusterWatchingController(
		"SystemAdminCredentialRevokedGC",
		resourcesDBClient,
		backendInformers,
		nil, // no kube-applier informers needed
		1*time.Hour,
		syncer,
	)
}

func (c *revokedGCSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *revokedGCSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	credCRUD := c.resourcesDBClient.SystemAdminCredentials(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	iter, err := credCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list SystemAdminCredentials: %w", err))
	}

	now := c.clock.Now()
	for _, cred := range iter.Items(ctx) {
		if cred.Status.Phase != api.SystemAdminCredentialPhaseRevoked {
			continue
		}
		if cred.Status.RevokedAt == nil {
			// Defensive: never delete a doc we cannot age.
			continue
		}

		expiresAt := cred.Status.RevokedAt.Add(revokedRetentionDuration)
		if now.Before(expiresAt) {
			continue
		}

		credName := cred.GetResourceID().Name
		logger.Info("garbage collecting revoked credential", "credentialName", credName, "revokedAt", cred.Status.RevokedAt.Time)
		if err := credCRUD.Delete(ctx, credName); err != nil && !database.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("failed to delete revoked credential %q: %w", credName, err))
		}
	}

	if err := iter.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("error iterating SystemAdminCredentials: %w", err))
	}

	return nil
}
