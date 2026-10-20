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

package datadump

import (
	"context"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	corelisters "github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type csStateDump struct {
	clusterLister corelisters.ClusterLister
	csClient      ocm.ClusterServiceClientSpec

	// Gate informer changes as well as periodic resyncs. Ordinary client traffic
	// supplies frequent observations even during active operations.
	nextDumpChecker controllerutil.CooldownChecker
}

const CSStateDumpControllerName = "CSStateDump"
const csStateDumpPeriod = 30 * time.Minute

// NewCSStateDumpController backfills observations when normal reconciliation
// does not read CS state. The shared client's logging decorator emits snapshots.
func NewCSStateDumpController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clusterLister corelisters.ClusterLister,
	backendInformers coreinformers.BackendInformers,
	csClient ocm.ClusterServiceClientSpec,
) controllerutils.Controller {
	syncer := &csStateDump{
		clusterLister:   clusterLister,
		csClient:        csClient,
		nextDumpChecker: controllerutil.NewTimeBasedCooldownChecker(csStateDumpPeriod),
	}

	controller := controllerutils.NewClusterWatchingController(
		CSStateDumpControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		csStateDumpPeriod,
		syncer,
	)
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(coreapi.NodePoolResourceType, "*")
	return controller
}

func (c *csStateDump) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	if !c.nextDumpChecker.CanSync(ctx, key) {
		return nil
	}
	logger := utils.LoggerFromContext(ctx)
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		logger.Error(err, "failed to get cluster from informer cache for CS state dump")
		return nil // best effort
	}
	csID := cluster.ServiceProviderProperties.ClusterServiceID
	if csID == nil || len(csID.String()) == 0 {
		return nil
	}

	if _, err := c.csClient.GetCluster(ctx, *csID); err != nil {
		logger.Error(err, "failed to get cluster from cluster-service for CS state dump")
	}
	// Consume the entire list: the decorator logs each returned document, including
	// CS node pools that have no Cosmos counterpart. No extra per-node-pool GETs.
	nodePools := c.csClient.ListNodePools(*csID, "")
	for range nodePools.Items(ctx) {
	}
	if err := nodePools.GetError(); err != nil {
		logger.Error(err, "failed to list node pools from cluster-service for CS state dump")
	}
	return nil
}
