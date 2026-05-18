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

package datadumpcontrollers

import (
	"context"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/serverutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type billingDump struct {
	cooldownChecker   controllerutil.CooldownChecker
	resourcesDBClient database.ResourcesDBClient
	billingDBClient   database.BillingDBClient

	// nextDumpChecker ensures we don't hotloop from any source.
	nextDumpChecker controllerutil.CooldownChecker
}

// NewBillingDumpController periodically dumps billing documents for each cluster.
func NewBillingDumpController(
	resourcesDBClient database.ResourcesDBClient,
	billingDBClient database.BillingDBClient,
	activeOperationLister listers.ActiveOperationLister,
	backendInformers informers.BackendInformers,
) controllerutils.Controller {
	syncer := &billingDump{
		cooldownChecker:   controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient: resourcesDBClient,
		billingDBClient:   billingDBClient,
		nextDumpChecker:   controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
	}

	return controllerutils.NewClusterWatchingController(
		"BillingDump",
		resourcesDBClient,
		backendInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *billingDump) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	if !c.nextDumpChecker.CanSync(ctx, key) {
		return nil
	}

	logger := utils.LoggerFromContext(ctx)

	if err := serverutils.DumpBillingToLogger(ctx, c.resourcesDBClient, c.billingDBClient, key.GetResourceID()); err != nil {
		// never fail, this is best effort
		logger.Error(err, "failed to dump billing to logger")
	}

	return nil
}

func (c *billingDump) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}
