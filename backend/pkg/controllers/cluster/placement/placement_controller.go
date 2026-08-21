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

package placement

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// PlacementControllerName is the single logical name for this controller. It is
// used for the workqueue name (a Prometheus label), the context controller name,
// and log values so metrics, ctx, and log fields never drift.
const PlacementControllerName = "Placement"

// placementWriteMaxRetries bounds the number of conflict retries when writing
// the chosen placement onto the ServiceProviderCluster. A bounded loop avoids
// hot-looping under contention; anything beyond this is left to the next resync.
const placementWriteMaxRetries = 5

// placementSyncer selects the management cluster a newly-created HCP should be
// scheduled onto and records that intent on ServiceProviderCluster.Spec.
// ManagementClusterResourceID. Status.ManagementClusterResourceID (the observed
// placement) continues to be written by ManagementClusterPlacementSync.
type placementSyncer struct {
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	managementClusterLister      fleetlisters.ManagementClusterLister
	cosmosClient                 corecosmosstorage.ResourcesDBClient
}

var _ controllerutils.ClusterSyncer = (*placementSyncer)(nil)

// NewPlacementController creates the scheduling controller that resolves initial
// placement for a HostedControlPlane by choosing an eligible management cluster
// and writing it to ServiceProviderCluster.Spec.ManagementClusterResourceID.
func NewPlacementController(
	cosmosClient corecosmosstorage.ResourcesDBClient,
	managementClusterLister fleetlisters.ManagementClusterLister,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()

	syncer := &placementSyncer{
		serviceProviderClusterLister: serviceProviderClusterLister,
		managementClusterLister:      managementClusterLister,
		cosmosClient:                 cosmosClient,
	}

	return controllerutils.NewClusterWatchingController(
		PlacementControllerName,
		cosmosClient,
		informers,
		kubeApplierInformers,
		5*time.Minute, // Check every 5 minutes
		syncer,
	)
}

// needsWork reports whether the ServiceProviderCluster still needs its
// Spec.ManagementClusterResourceID (scheduler intent) resolved.
func (c *placementSyncer) needsWork(serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	return serviceProviderCluster.Spec.ManagementClusterResourceID == nil
}

// SyncOnce resolves placement for a single HCP cluster: it selects an eligible
// management cluster and records it on ServiceProviderCluster.Spec.
func (c *placementSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	// Cheap cache check first.
	cachedServiceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.V(1).Info("ServiceProviderCluster not found in cache, skipping")
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster from cache: %w", err))
	}
	if !c.needsWork(cachedServiceProviderCluster) {
		logger.V(1).Info("ServiceProviderCluster already has Spec.ManagementClusterResourceID, skipping")
		return nil
	}

	// Gather the inputs for placement selection from cache.
	managementClusters, err := c.managementClusterLister.List(ctx)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list management clusters: %w", err))
	}
	serviceProviderClusters, err := c.serviceProviderClusterLister.List(ctx)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list service provider clusters: %w", err))
	}

	chosen, err := selectManagementCluster(managementClusters, serviceProviderClusters)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to select management cluster for %s: %w", key.HCPClusterName, err))
	}

	// Write the chosen placement onto the live document with conflict retry.
	serviceProviderClusterCRUD := c.cosmosClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	for attempt := 0; ; attempt++ {
		existingServiceProviderCluster, err := serviceProviderClusterCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		if cosmosstorageutils.IsNotFoundError(err) {
			logger.V(1).Info("ServiceProviderCluster not found in Cosmos, skipping")
			return nil
		}
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
		}
		// Re-check against the live document: another actor may have resolved
		// placement since we read the cache.
		if !c.needsWork(existingServiceProviderCluster) {
			logger.V(1).Info("ServiceProviderCluster already has Spec.ManagementClusterResourceID (live read), skipping")
			return nil
		}

		replacement := existingServiceProviderCluster.DeepCopy()
		replacement.Spec.ManagementClusterResourceID = chosen

		_, err = serviceProviderClusterCRUD.Replace(ctx, replacement, nil)
		if err == nil {
			logger.Info("assigned management cluster placement", "managementClusterID", chosen.String())
			return nil
		}
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			if attempt < placementWriteMaxRetries {
				logger.V(1).Info("conflict writing placement, retrying", "attempt", attempt)
				continue
			}
			// Give up for this round; the next resync re-evaluates.
			logger.V(1).Info("conflict writing placement, exhausted retries; will retry on next resync")
			return nil
		}
		return utils.TrackError(fmt.Errorf("failed to update ServiceProviderCluster placement: %w", err))
	}
}

// selectManagementCluster is a pure function that chooses the management cluster
// a newly-scheduled HCP should be placed on.
//
// Eligible management clusters are those whose Spec.SchedulingPolicy is
// Schedulable AND whose Status has the Ready condition set to True. Among the
// eligible clusters it returns the one with the HIGHEST number of
// ServiceProviderClusters already assigned to it (counted by
// Spec.ManagementClusterResourceID). This is intentional bin-packing: packing
// HCPs onto the fullest eligible management cluster makes placement behavior
// easy to observe in E2E. Ties (equal counts) are broken deterministically by
// the lowest management-cluster resource ID string so the selection is stable
// and unit-testable.
//
// It returns an error when no eligible management cluster is available.
func selectManagementCluster(
	managementClusters []*fleetapi.ManagementCluster,
	serviceProviderClusters []*coreapi.ServiceProviderCluster,
) (*azcorearm.ResourceID, error) {
	// Count existing placements per management cluster.
	assignedCounts := map[string]int{}
	for _, serviceProviderCluster := range serviceProviderClusters {
		if serviceProviderCluster == nil || serviceProviderCluster.Spec.ManagementClusterResourceID == nil {
			continue
		}
		assignedCounts[strings.ToLower(serviceProviderCluster.Spec.ManagementClusterResourceID.String())]++
	}

	var (
		chosen      *fleetapi.ManagementCluster
		chosenCount int
	)
	for _, managementCluster := range managementClusters {
		if managementCluster == nil || managementCluster.ResourceID == nil {
			continue
		}
		if !isEligibleManagementCluster(managementCluster) {
			continue
		}
		count := assignedCounts[strings.ToLower(managementCluster.ResourceID.String())]
		switch {
		case chosen == nil:
			chosen, chosenCount = managementCluster, count
		case count > chosenCount:
			chosen, chosenCount = managementCluster, count
		case count == chosenCount && managementCluster.ResourceID.String() < chosen.ResourceID.String():
			chosen, chosenCount = managementCluster, count
		}
	}

	if chosen == nil {
		return nil, fmt.Errorf("no eligible management cluster available for scheduling")
	}
	return chosen.ResourceID, nil
}

// isEligibleManagementCluster reports whether a management cluster can accept a
// new HCP: it must be Schedulable and Ready.
func isEligibleManagementCluster(managementCluster *fleetapi.ManagementCluster) bool {
	if managementCluster.Spec.SchedulingPolicy != fleetapi.ManagementClusterSchedulingPolicySchedulable {
		return false
	}
	return meta.IsStatusConditionTrue(managementCluster.Status.Conditions, string(fleetapi.ManagementClusterConditionReady))
}
