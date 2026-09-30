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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"

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

	nodePoolResyncPeriod        = 30 * time.Minute
	nodePoolSimulationMaxCycles = 1000
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
}

// NewNodePoolController observes live AKS pools and reports advisory simulations.
// It never changes agent pools, cluster tags, or scheduling capacity. The
// management-controller wrapper reports only this observer's health.
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

	return fleetcontrollers.NewManagementClusterWatchingController(
		NodePoolControllerName,
		fleetDBClient,
		managementClusterInformer,
		nodePoolResyncPeriod,
		syncer,
	)
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
	observedAt := time.Now().UTC()
	logger := utils.LoggerFromContext(ctx).WithValues(
		"stampIdentifier", key.StampIdentifier,
		"profile", s.profile.Tiers,
		"zones", s.zones,
		"region", s.region,
		"observedAt", observedAt.Format(time.RFC3339Nano),
		"shadowOnly", true,
	)

	managementCluster, err := s.managementClusterLister.Get(ctx, key.StampIdentifier)
	if err != nil {
		return utils.TrackError(err)
	}
	logger = logger.WithValues("managementClusterResourceID", managementCluster.ResourceID.String())

	aksResourceID := managementCluster.Status.AKSResourceID
	if aksResourceID == nil {
		logShadowReport(logger, trace{Outcome: "waiting", Reason: "management cluster has no AKS resource ID yet"}, nil)
		return nil
	}

	logger = logger.WithValues(
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
	if marker := cluster.Tags[agentpools.ProvisioningTagKey]; marker != nil && *marker == agentpools.ProvisioningTagValue {
		logShadowReport(logger, trace{Outcome: "waiting", Reason: "cluster provisioning marker is present"}, nil)
		return nil
	}
	if cluster.Properties == nil || cluster.Properties.ProvisioningState == nil || *cluster.Properties.ProvisioningState != "Succeeded" {
		logShadowReport(logger, trace{Outcome: "waiting", Reason: "AKS cluster provisioning state is not Succeeded"}, nil)
		return nil
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
	observedCapacity, err := agentpools.ObservedAgentPoolCapacities(pools, resolved.SKUMetadata)
	if err != nil {
		return utils.TrackError(err)
	}
	// A failed required tier abandons the projection. An optional tier that
	// lost its quota race still leaves a usable partial plan, which the simulator
	// runs against the preserved current capacity baseline (FullyAllocated=false).
	if compute.RequiredTierFailed(failures) {
		outcome := "rejected"
		reason := "tier allocation failed: " + compute.FailureSummary(failures)
		if pool := firstInProgressPool(current); pool != nil {
			outcome = "waiting"
			reason = fmt.Sprintf("pool %s has an operation in progress; %s", pool.Name, reason)
		}
		logShadowReport(logger, trace{
			Desired: desiredPools, Initial: current,
			FamilyBudgets:  resolved.AvailableVCPUs,
			FullyAllocated: resolved.FullyAllocated, InitialCapacity: observedCapacity,
			Outcome: outcome, Reason: reason,
		}, failures)
		return nil
	}

	projection, err := simulateAndTrace(desiredPools, current, resolved.AvailableVCPUs, resolved.FullyAllocated, nodePoolSimulationMaxCycles)
	if err != nil {
		return utils.TrackError(fmt.Errorf("simulating node pool projection: %w", err))
	}
	logShadowReport(logger, projection, failures)

	return nil
}

func logShadowReport(logger logr.Logger, projection trace, failures []compute.AllocationFailure) {
	logger.Info("shadow node pool projection; actions are not executed and scheduling capacity is unchanged",
		"assumptions", "projected transitions are conditional on successful actions and unchanged external conditions",
		"desiredPools", projection.Desired,
		"currentPools", projection.Initial,
		"fullyAllocated", projection.FullyAllocated,
		"allocationFailures", failures,
		"outcome", projection.Outcome,
		"reason", projection.Reason,
		"projectedTrace", formatTrace(projection),
	)
}
