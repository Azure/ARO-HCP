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

	clustersClient, err := armcontainerservice.NewManagedClustersClient(aksResourceID.SubscriptionID, s.credential, s.armClientOptions)
	if err != nil {
		return utils.TrackError(err)
	}
	cluster, err := clustersClient.Get(ctx, aksResourceID.ResourceGroupName, aksResourceID.Name, nil)
	if err != nil {
		return utils.TrackError(err)
	}
	capacityBaseline, err := agentpools.ReadCapacityTags(cluster.Tags)
	if err != nil {
		return utils.TrackError(err)
	}
	if cluster.Properties == nil || cluster.Properties.ProvisioningState == nil || *cluster.Properties.ProvisioningState != "Succeeded" {
		return utils.TrackError(fmt.Errorf("AKS cluster configuration is not ready for pool reconciliation"))
	}

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
	if len(failures) > 0 {
		return utils.TrackError(fmt.Errorf("tier allocation failed: %s", compute.FailureSummary(failures)))
	}
	desiredCapacity, err := compute.PoolCapacities(desiredPools)
	if err != nil {
		return utils.TrackError(err)
	}
	capacityFloor, err := desiredCapacity.TransitionFloor(capacityBaseline, resolved.FullyAllocated)
	if err != nil {
		return utils.TrackError(fmt.Errorf("rejecting desired plan: %w", err))
	}
	current, err := currentPoolStates(pools, resolved.SKUMetadata)
	if err != nil {
		return utils.TrackError(err)
	}
	managedCount := 0
	for _, pool := range pools {
		if agentpools.IsManagedPool(pool) {
			managedCount++
		}
	}
	if len(current) != managedCount {
		return utils.TrackError(fmt.Errorf("incomplete ARM configuration for managed pools"))
	}
	if unresolved := unresolvedSKUSizes(current); len(unresolved) > 0 {
		return utils.TrackError(fmt.Errorf("cannot protect capacity with unresolved SKU metadata: %v", unresolved))
	}
	observedCapacity, err := agentpools.ObservedPoolCapacities(pools, resolved.SKUMetadata)
	if err != nil {
		return utils.TrackError(err)
	}

	if configurationConverged(desiredPools, current) {
		if err := observedCapacity.ValidateAgainstBaseline(desiredCapacity); err != nil {
			return utils.TrackError(fmt.Errorf("converged pools have unexpected capacity: %w", err))
		}
		logger.Info("pool configuration converged", "capacity", desiredCapacity)
		return utils.TrackError(agentpools.WriteCapacityTags(ctx, clustersClient, aksResourceID.ResourceGroupName, aksResourceID.Name, cluster.ManagedCluster, desiredCapacity))
	}

	networkConfig := networkConfigFromPools(pools)
	action := findNextAction(desiredPools, current, resolved.AvailableVCPUs, capacityFloor, networkConfig)

	logReconcileAudit(logger, desiredPools, current, action, failures)

	if action == nil {
		return utils.TrackError(fmt.Errorf("pool configuration has not converged; no safe action is currently available"))
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
	logger = logger.WithValues(
		"desiredPoolCount", len(desired),
		"currentPoolCount", len(current),
		"desiredPools", desired,
		"currentPools", current,
	)

	if action != nil {
		logger = action.AddLoggerValues(logger)
	} else {
		logger = logger.WithValues("actionType", "None")
	}

	if len(failures) > 0 {
		logger = logger.WithValues("allocationFailures", failures)
	}

	logger.Info("reconcile audit")
}
