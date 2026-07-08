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
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// revokedGCRetention is how long a credential request doc stays in Cosmos
	// after creation before it is garbage-collected.
	revokedGCRetention = 48 * time.Hour
)

type revokedGC struct {
	cooldownChecker   controllerutil.CooldownChecker
	clock             utilsclock.PassiveClock
	resourcesDBClient database.ResourcesDBClient
}

var _ controllerutils.CredentialRequestSyncer = (*revokedGC)(nil)

// NewRevokedGCController returns a CredentialRequestWatchingController that
// deletes every SystemAdminCredentialRequest document 48 hours after it was
// created, regardless of the request's status.
func NewRevokedGCController(
	clock utilsclock.PassiveClock,
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	backendInformers informers.BackendInformers,
) controllerutils.Controller {
	syncer := &revokedGC{
		cooldownChecker:   controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		clock:             clock,
		resourcesDBClient: resourcesDBClient,
	}

	return controllerutils.NewCredentialRequestWatchingController(
		"SystemAdminCredentialRevokedGC",
		resourcesDBClient,
		backendInformers,
		nil,
		1*time.Hour,
		syncer,
	)
}

func (c *revokedGC) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *revokedGC) SyncOnce(ctx context.Context, key controllerutils.SystemAdminCredentialRequestKey) error {
	logger := utils.LoggerFromContext(ctx)

	credCRUD := c.resourcesDBClient.SystemAdminCredentialRequests(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	cred, err := credCRUD.Get(ctx, key.CredentialName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get SystemAdminCredentialRequest: %w", err))
	}

	// Delete every credential request once it is older than the retention window,
	// regardless of its status. A missing creation timestamp means we cannot
	// determine the age yet, so leave the document alone.
	if cred.Spec.CreationTimestamp.IsZero() {
		return nil
	}
	age := c.clock.Now().Sub(cred.Spec.CreationTimestamp.Time)
	if age < revokedGCRetention {
		return nil
	}

	if err := credCRUD.Delete(ctx, key.CredentialName); err != nil && !database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to delete credential %s: %w", key.CredentialName, err))
	}
	logger.Info("garbage-collected credential request", "credential", key.CredentialName, "age", age.String())

	return nil
}
