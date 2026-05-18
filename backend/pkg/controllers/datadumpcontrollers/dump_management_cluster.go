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
	"fmt"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	dbinformers "github.com/Azure/ARO-HCP/internal/database/informers"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type managementClusterDataDump struct {
	cooldownChecker         controllerutil.CooldownChecker
	managementClusterLister dblisters.ManagementClusterLister

	nextDataDumpChecker controllerutil.CooldownChecker
}

// NewManagementClusterDataDumpController periodically dumps management cluster data.
func NewManagementClusterDataDumpController(
	fleetDBClient database.FleetDBClient,
	managementClusterLister dblisters.ManagementClusterLister,
	fleetInformers dbinformers.FleetInformers,
) controllerutils.Controller {
	syncer := &managementClusterDataDump{
		cooldownChecker:         controllerutil.NewTimeBasedCooldownChecker(4 * time.Minute),
		managementClusterLister: managementClusterLister,
		nextDataDumpChecker:     controllerutil.NewTimeBasedCooldownChecker(4 * time.Minute),
	}

	return controllerutils.NewManagementClusterWatchingController(
		"ManagementClusterDataDump",
		fleetDBClient,
		fleetInformers,
		5*time.Minute,
		syncer,
	)
}

func (c *managementClusterDataDump) SyncOnce(ctx context.Context, key controllerutils.ManagementClusterKey) error {
	if !c.nextDataDumpChecker.CanSync(ctx, key) {
		return nil
	}

	logger := utils.LoggerFromContext(ctx)

	mc, err := c.managementClusterLister.Get(ctx, key.StampIdentifier)
	if err != nil {
		logger.Error(err, "failed to get management cluster")
		return nil
	}

	logger.Info(fmt.Sprintf("dumping resourceID %v", mc.CosmosMetadata.ResourceID),
		"currentResourceID", mc.CosmosMetadata.ResourceID.String(),
		"content", mc,
	)

	return nil
}

func (c *managementClusterDataDump) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}
