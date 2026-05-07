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
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/serverutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type clusterRecursiveDataDump struct {
	cooldownChecker   controllerutils.CooldownChecker
	resourcesDBClient database.ResourcesDBClient

	// nextDataDumpChecker ensures we don't hotloop from any source.
	nextDataDumpChecker controllerutils.CooldownChecker
}

// NewClusterRecursiveDataDumpController periodically lists all clusters and logs when the cluster was created and its state.
func NewClusterRecursiveDataDumpController(
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	backendInformers informers.BackendInformers,
) controllerutils.Controller {
	syncer := &clusterRecursiveDataDump{
		cooldownChecker:     controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient:   resourcesDBClient,
		nextDataDumpChecker: controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
	}

	controller := controllerutils.NewClusterWatchingController(
		"DataDump",
		resourcesDBClient,
		backendInformers,
		1*time.Minute,
		syncer,
	)

	return controller
}

func (c *clusterRecursiveDataDump) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	if !c.nextDataDumpChecker.CanSync(ctx, key) {
		return nil
	}

	logger := utils.LoggerFromContext(ctx)

	if err := serverutils.DumpDataToLogger(ctx, c.resourcesDBClient, key.GetResourceID()); err != nil {
		// never fail, this is best effort
		logger.Error(err, "failed to dump data to logger")
	}

	return nil
}

func (c *clusterRecursiveDataDump) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}
