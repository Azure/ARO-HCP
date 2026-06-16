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

	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// RevokedGCRetention is how long after Status.RevokedAt the
// RevokedGC controller waits before deleting a Phase=Revoked credential
// doc. Deliberately longer than the cert's 24h TTL — see PLAN.md open
// question 3.
const RevokedGCRetention = 48 * time.Hour

// revokedGC is controller #9. Cluster-watching janitor on a 1-hour
// cadence. Deletes Phase=Revoked credentials whose
// RevokedAt + RevokedGCRetention <= now. Defensive against unset
// RevokedAt: never deletes a doc we cannot age.
type revokedGC struct {
	clock             utilsclock.PassiveClock
	cooldownChecker   controllerutil.CooldownChecker
	resourcesDBClient database.ResourcesDBClient
}

func NewRevokedGCController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	syncer := &revokedGC{
		clock:             clock,
		cooldownChecker:   controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient: resourcesDBClient,
	}
	return controllerutils.NewClusterWatchingController(
		"SystemAdminCredentialRevokedGC",
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		1*time.Hour,
		syncer,
	)
}

func (c *revokedGC) CooldownChecker() controllerutil.CooldownChecker { return c.cooldownChecker }

func (c *revokedGC) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	credentialsCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).
		SystemAdminCredentials(key.HCPClusterName)
	iter, err := credentialsCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("list credentials: %w", err))
	}
	now := c.clock.Now()
	for _, cred := range iter.Items(ctx) {
		if cred == nil {
			continue
		}
		if cred.Status.Phase != api.SystemAdminCredentialPhaseRevoked {
			continue
		}
		if cred.Status.RevokedAt == nil {
			// Never delete a doc we cannot age. The revoke poller is
			// expected to always set RevokedAt at the moment of Phase
			// transition; an unset value is either a controller bug or
			// a pre-rollout doc — leave it for human triage.
			continue
		}
		if cred.Status.RevokedAt.Time.Add(RevokedGCRetention).After(now) {
			continue
		}
		if err := credentialsCRUD.Delete(ctx, cred.GetResourceID().Name); err != nil && !database.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("delete revoked credential %q: %w", cred.GetResourceID().Name, err))
		}
		logger.Info("GC'd revoked credential", "credential", cred.GetResourceID().Name)
	}
	return iter.GetError()
}
