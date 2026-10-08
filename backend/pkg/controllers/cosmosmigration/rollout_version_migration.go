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

package cosmosmigration

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const CosmosRolloutVersionMigrationControllerName = "CosmosRolloutVersionMigration"

type cosmosRolloutVersionMigrationController struct {
	fleetDBClient fleetcosmosstorage.FleetDBClient
	// The workqueue serializes each channel, so Load and Store need not be atomic together.
	completedRollouts sync.Map
	cooldown          controllerutil.CooldownChecker
}

// NewCosmosRolloutVersionMigrationController rewrites each rollout once per process
// to persist the version profile supplied by read normalization.
func NewCosmosRolloutVersionMigrationController(
	fleetDBClient fleetcosmosstorage.FleetDBClient,
	fleetInformers fleetinformers.FleetInformers,
	resyncDuration time.Duration,
) controllerutils.Controller {
	syncer := &cosmosRolloutVersionMigrationController{
		fleetDBClient: fleetDBClient,
		cooldown:      controllerutil.NewTimeBasedCooldownChecker(resyncDuration),
	}
	return controllerutils.NewControlPlaneVersionRolloutWatchingController(
		CosmosRolloutVersionMigrationControllerName, fleetInformers, resyncDuration, syncer)
}

func (c *cosmosRolloutVersionMigrationController) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldown
}

func (c *cosmosRolloutVersionMigrationController) SyncOnce(ctx context.Context, key controllerutils.ControlPlaneVersionRolloutKey) error {
	name := key.YStreamChannel
	if _, alreadyDone := c.completedRollouts.Load(name); alreadyDone {
		return nil
	}
	logger := utils.LoggerFromContext(ctx)
	crud := c.fleetDBClient.ControlPlaneVersionRollouts()
	const maxRetries = 3
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		old, err := crud.Get(ctx, name)
		if cosmosstorageutils.IsNotFoundError(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to get rollout %q: %w", name, err)
		}
		// Always rewrite, even if the normalized profile is already present. Re-read
		// on conflict to preserve concurrent rollout decisions; Replace validates it.
		_, err = crud.Replace(ctx, old.DeepCopy(), old, nil)
		if cosmosstorageutils.IsNotFoundError(err) {
			return nil
		}
		if err == nil {
			c.completedRollouts.Store(name, struct{}{})
			logger.Info("cosmos rollout version migration completed successfully")
			return nil
		}
		if !cosmosstorageutils.IsConflictError(err) && !cosmosstorageutils.IsPreconditionFailedError(err) {
			return fmt.Errorf("failed to replace rollout %q: %w", name, err)
		}
		lastErr = err
		if attempt < maxRetries {
			logger.Info("conflict on rollout replace, retrying", "attempt", attempt, "maxRetries", maxRetries)
		}
	}
	return fmt.Errorf("failed to replace rollout %q after %d attempts due to conflict/precondition failure: %w", name, maxRetries, lastErr)
}

// MigrateAllRolloutVersionsOrDie is the one-shot integration-test trigger. Production
// uses the rollout watcher; both paths reconcile each key through SyncOnce.
func MigrateAllRolloutVersionsOrDie(ctx context.Context, fleetDBClient fleetcosmosstorage.FleetDBClient) {
	logger := utils.LoggerFromContext(ctx)
	c := &cosmosRolloutVersionMigrationController{fleetDBClient: fleetDBClient}
	iterator, err := fleetDBClient.GlobalListers().ControlPlaneVersionRollouts().List(ctx, nil)
	if err != nil {
		logger.Error(err, "failed to list rollouts")
		panic(err)
	}
	for _, rollout := range iterator.Items(ctx) {
		key := controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: rollout.ResourceID.Name}
		if err := c.SyncOnce(ctx, key); err != nil {
			logger.Error(err, "cosmos rollout version migration failed", "rollout", rollout.ResourceID)
			panic(err)
		}
	}
	if err := iterator.GetError(); err != nil {
		logger.Error(err, "failed to iterate rollouts")
		panic(err)
	}
}
