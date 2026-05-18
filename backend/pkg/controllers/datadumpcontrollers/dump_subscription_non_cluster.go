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

package datadumpcontrollers

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type subscriptionNonClusterDataDump struct {
	cooldownChecker   controllerutil.CooldownChecker
	resourcesDBClient database.ResourcesDBClient

	// nextDataDumpChecker ensures we don't hotloop from any source.
	nextDataDumpChecker controllerutil.CooldownChecker
}

// NewSubscriptionNonClusterDataDumpController periodically dumps data for a subscription that is NOT related to a cluster.
func NewSubscriptionNonClusterDataDumpController(
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	backendInformers informers.BackendInformers,
) controllerutils.Controller {
	syncer := &subscriptionNonClusterDataDump{
		cooldownChecker:     controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient:   resourcesDBClient,
		nextDataDumpChecker: controllerutil.NewTimeBasedCooldownChecker(4 * time.Minute),
	}

	return controllerutils.NewSubscriptionWatchingController(
		"SubscriptionNonClusterDataDump",
		backendInformers,
		5*time.Minute,
		syncer,
	)
}

func (c *subscriptionNonClusterDataDump) SyncOnce(ctx context.Context, key controllerutils.SubscriptionKey) error {
	if !c.nextDataDumpChecker.CanSync(ctx, key) {
		return nil
	}

	logger := utils.LoggerFromContext(ctx)

	cosmosCRUD, err := c.resourcesDBClient.UntypedCRUD(*key.GetResourceID())
	if err != nil {
		logger.Error(err, "failed to get cosmos CRUD")
		return nil
	}

	subscription, err := cosmosCRUD.Get(ctx, key.GetResourceID())
	if err != nil {
		logger.Error(err, "failed to get subscription")
		return nil
	}

	logger.Info(fmt.Sprintf("dumping resourceID %v", key.GetResourceID()),
		"currentResourceID", key.GetResourceID().String(),
		"content", subscription,
	)

	return nil
}

func (c *subscriptionNonClusterDataDump) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}
