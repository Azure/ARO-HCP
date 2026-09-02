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

package nodepool

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpools"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/quota"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	NodePoolControllerName = "NodePoolController"

	nodePoolResyncPeriod = 30 * time.Minute
)

type nodePoolSyncer struct {
	managementClusterLister fleetlisters.ManagementClusterLister
	profile                 compute.Profile
	zones                   []string
	region                  string
	agentPoolClientFactory  func(subscriptionID string) (*armcontainerservice.AgentPoolsClient, error)
	credential              azcore.TokenCredential
	armClientOptions        *azcorearm.ClientOptions
	skuCache                *skucache.SKUCache
	enqueueAfter            controllerutils.AfterEnqueuer
}

func NewNodePoolController(
	managementClusterInformer cache.SharedIndexInformer,
	managementClusterLister fleetlisters.ManagementClusterLister,
	fleetDBClient fleetcosmosstorage.FleetDBClient,
	profile compute.Profile,
	zones []string,
	region string,
	credential azcore.TokenCredential,
	clientOptions *policy.ClientOptions,
) fleetcontrollers.Controller {
	agentPoolClientFactory, armClientOptions := agentpools.NewClientFactory(credential, clientOptions)

	syncer := &nodePoolSyncer{
		managementClusterLister: managementClusterLister,
		profile:                 profile,
		zones:                   zones,
		region:                  region,
		agentPoolClientFactory:  agentPoolClientFactory,
		credential:              credential,
		armClientOptions:        armClientOptions,
		skuCache:                skucache.NewSKUCache(region, credential, clientOptions, nil),
	}

	controller := fleetcontrollers.NewManagementClusterWatchingController(
		NodePoolControllerName,
		fleetDBClient,
		managementClusterInformer,
		nodePoolResyncPeriod,
		syncer,
	)

	syncer.enqueueAfter = controller

	return controller
}

func (s *nodePoolSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return nil
}

// usageFetcher returns a compute.FetchQuotaUsageFunc that lazily creates a
// usage client for subscriptionID. The client is per-subscription because the
// syncer is region-scoped and reconciles clusters across subscriptions.
func (s *nodePoolSyncer) usageFetcher(subscriptionID string) compute.FetchQuotaUsageFunc {
	return func(ctx context.Context, families sets.Set[compute.VMFamily]) (map[compute.VMFamily]compute.QuotaUsage, error) {
		client, err := armcompute.NewUsageClient(subscriptionID, s.credential, s.armClientOptions)
		if err != nil {
			return nil, fmt.Errorf("creating usage client: %w", err)
		}
		return quota.FetchUsage(ctx, client, s.region, families)
	}
}

func (s *nodePoolSyncer) SyncOnce(ctx context.Context, key fleetcontrollers.ManagementClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	managementCluster, err := s.managementClusterLister.Get(ctx, key.StampIdentifier)
	if err != nil {
		return utils.TrackError(err)
	}

	aksResourceID := managementCluster.Status.AKSResourceID
	if aksResourceID == nil {
		logger.V(1).Info("management cluster has no AKS resource ID yet, will retry on next resync")
		return nil
	}

	logger = logger.WithValues(
		"managementClusterResourceID", managementCluster.ResourceID.String(),
		"aksResourceID", aksResourceID.String(),
	)
	ctx = utils.ContextWithLogger(ctx, logger)

	pools, err := agentpools.ListAgentPools(ctx, s.agentPoolClientFactory, aksResourceID)
	if err != nil {
		return utils.TrackError(err)
	}

	resolved, err := compute.ResolveDesiredPools(ctx, s.skuCache, aksResourceID.SubscriptionID, s.profile, s.zones,
		s.usageFetcher(aksResourceID.SubscriptionID))
	if err != nil {
		return utils.TrackError(err)
	}
	desiredPools, failures := resolved.Pools, resolved.Failures

	current := currentPoolStates(pools, resolved.SKUMetadata)

	if unresolved := unresolvedSKUSizes(current); len(unresolved) > 0 {
		logger.Info("current pools have unresolved SKU metadata; excluded from quota headroom", "vmSizes", unresolved)
	}

	networkConfig := networkConfigFromPools(pools)
	action := findNextAction(desiredPools, current, resolved.FamilyBudgets, networkConfig)

	logReconcileAudit(logger, desiredPools, current, action, failures)

	if action == nil {
		if len(desiredPools) == 0 && len(current) > 0 {
			logger.Info("desired pool set is empty, preserving existing pools to prevent accidental teardown")
		}
		return nil
	}

	agentPoolClient, err := s.agentPoolClientFactory(aksResourceID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("creating agent pool client: %w", err))
	}

	execCtx := ExecuteContext{
		Client:        agentPoolClient,
		AKSResourceID: aksResourceID,
	}
	requeueDelay, err := action.execute(ctx, execCtx)
	if err != nil {
		return utils.TrackError(err)
	}

	if requeueDelay != nil {
		logger.Info("requeuing", "requeueDelay", *requeueDelay)
		s.enqueueAfter.EnqueueAfter(key, *requeueDelay)
	}

	return nil
}

func logReconcileAudit(logger logr.Logger, desired []compute.Pool, current []PoolState, action Action, failures []compute.AllocationFailure) {
	keysAndValues := []any{
		"desiredPoolCount", len(desired),
		"currentPoolCount", len(current),
	}

	if action != nil {
		keysAndValues = append(keysAndValues,
			"actionType", string(action.kind()),
			"actionPool", action.poolName(),
			"actionVMSize", action.vmSize(),
			"actionZone", action.zone(),
		)
	} else {
		keysAndValues = append(keysAndValues, "actionType", "None")
	}

	if len(failures) > 0 {
		keysAndValues = append(keysAndValues, "allocationFailures", failures)
	}

	logger.Info("reconcile audit", keysAndValues...)

	// Full pool state is verbose (VMSpec, labels, taints per pool); keep it at
	// V(1) so the Info line above stays terse on every resync.
	logger.V(1).Info("reconcile audit detail", "desiredPools", desired, "currentPools", current)
}
