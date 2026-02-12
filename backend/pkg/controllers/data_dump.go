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

package controllers

import (
	"context"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/serverutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type dataDump struct {
	cooldownChecker controllerutils.CooldownChecker
	cosmosClient    database.DBClient

	// nextDataDumpChecker ensures we don't hotloop from any source.
	nextDataDumpChecker controllerutils.CooldownChecker
}

// NewDataDumpController periodically lists all clusters and for each out when the cluster was created and its state.
func NewDataDumpController(activeOperationLister listers.ActiveOperationLister, cosmosClient database.DBClient) controllerutils.ClusterSyncer {
	c := &dataDump{
		cooldownChecker:     controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:        cosmosClient,
		nextDataDumpChecker: controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
	}

	return c
}

func (c *dataDump) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	if !c.nextDataDumpChecker.CanSync(ctx, key) {
		return nil
	}

	logger := utils.LoggerFromContext(ctx)

	if err := serverutils.DumpDataToLogger(ctx, c.cosmosClient, key.GetResourceID()); err != nil {
		// never fail, this is best effort
		logger.Error(err, "failed to dump data to logger")
	}

	return nil
}

func (c *dataDump) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}
