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
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// PendingCleanupControllerName is the single logical name for this controller.
const PendingCleanupControllerName = "PendingCleanup"

const pendingCleanupResyncPeriod = 10 * time.Minute

// pendingCleanupSyncer periodically sweeps each management cluster's
// ManagementClusterScheduling.Status.PendingAssignedClusters and removes stale
// reservations. For each entry it determines the referenced cluster's effective
// placement — the observed Status.ManagementClusterResourceID when set (Cluster
// Service reality), otherwise the Spec intent — and:
//   - keeps it when the effective placement points at this management cluster;
//   - keeps it when the effective placement is nil (placement still in progress);
//   - removes it when the effective placement points at a different management
//     cluster, or when the ServiceProviderCluster no longer exists.
//
// CapacityReportingController removes reservations once the HCP is observed
// (Ready/NotReady); this controller handles the reservations that never get
// observed (e.g. placement retried onto a different management cluster, or the
// cluster was deleted before it showed up).
type pendingCleanupSyncer struct {
	serviceProviderClusterLister      corelisters.ServiceProviderClusterLister
	managementClusterSchedulingLister fleetlisters.ManagementClusterSchedulingLister
	fleetDBClient                     fleetcosmosstorage.FleetDBClient
}

var _ controllerutils.ManagementClusterSyncer = (*pendingCleanupSyncer)(nil)

// NewPendingCleanupController creates the management-cluster-keyed controller
// that garbage-collects stale PendingAssignedClusters reservations.
func NewPendingCleanupController(
	fleetDBClient fleetcosmosstorage.FleetDBClient,
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister,
	fleetInformers fleetinformers.FleetInformers,
) controllerutils.Controller {
	_, managementClusterSchedulingLister := fleetInformers.ManagementClusterSchedulings()
	syncer := &pendingCleanupSyncer{
		serviceProviderClusterLister:      serviceProviderClusterLister,
		managementClusterSchedulingLister: managementClusterSchedulingLister,
		fleetDBClient:                     fleetDBClient,
	}

	return controllerutils.NewManagementClusterWatchingController(
		PendingCleanupControllerName,
		fleetDBClient,
		fleetInformers,
		pendingCleanupResyncPeriod,
		syncer,
	)
}

// CooldownChecker returns nil: the resync period governs the sweep cadence.
func (c *pendingCleanupSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return nil
}

// needsWork reports whether the scheduling document has any pending assignment
// reservations to sweep. It mirrors the canonical needsWork predicate used by the
// sibling cluster controllers (placement, creation): a pure check on the fetched
// document with no I/O.
func (c *pendingCleanupSyncer) needsWork(scheduling *fleetapi.ManagementClusterScheduling) bool {
	return len(scheduling.Status.PendingAssignedClusters) > 0
}

// SyncOnce sweeps one management cluster's PendingAssignedClusters list. On a
// transient failure it returns an error so the workqueue retries with backoff.
func (c *pendingCleanupSyncer) SyncOnce(ctx context.Context, key controllerutils.ManagementClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	managementClusterResourceID := key.GetResourceID()

	// Read the scheduling document from the informer cache (not a live CRUD Get).
	// The cached copy carries the etag that guards the optimistic Replace below, so
	// a stale cache can only produce a write conflict (412) that re-enqueues the
	// key — never a lost update.
	existing, err := c.managementClusterSchedulingLister.Get(ctx, key.StampIdentifier)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get scheduling document for management cluster %q from cache: %w", key.StampIdentifier, err))
	}
	if !c.needsWork(existing) {
		return nil
	}

	kept := make([]*azcorearm.ResourceID, 0, len(existing.Status.PendingAssignedClusters))
	for _, pending := range existing.Status.PendingAssignedClusters {
		keep, err := c.shouldKeepPending(ctx, pending, managementClusterResourceID)
		if err != nil {
			return err
		}
		if keep {
			kept = append(kept, pending)
		}
	}

	updated := existing.DeepCopy()
	if len(kept) == 0 {
		updated.Status.PendingAssignedClusters = nil
	} else {
		updated.Status.PendingAssignedClusters = kept
	}

	// Skip the write when the sweep changed nothing. NeedsUpdate is a semantic
	// deep-equality check that ignores cosmos-managed fields (etag) and ResourceID
	// representation differences, so an unchanged pending list never triggers a
	// spurious Replace.
	if !controllerutil.NeedsUpdate(existing, updated) {
		return nil
	}

	// The write path stays a live, etag-guarded Replace via the CRUD client; the
	// base document (and its etag) came from the cache read above.
	schedulingCRUD := c.fleetDBClient.Stamps().ManagementClusters(key.StampIdentifier).Scheduling()
	if _, err := schedulingCRUD.Replace(ctx, updated, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to update scheduling document for management cluster %q: %w", key.StampIdentifier, err))
	}
	removed := len(existing.Status.PendingAssignedClusters) - len(kept)
	logger.Info("cleaned up stale pending assignments", "removed", removed, "remaining", len(kept))
	return nil
}

// shouldKeepPending decides whether a single pending reservation entry (a
// cluster ARM resource ID) should be retained on managementClusterResourceID.
func (c *pendingCleanupSyncer) shouldKeepPending(ctx context.Context, pending, managementClusterResourceID *azcorearm.ResourceID) (bool, error) {
	if pending == nil {
		return false, nil
	}

	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, pending.SubscriptionID, pending.ResourceGroupName, pending.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		// The ServiceProviderCluster no longer exists: drop the reservation.
		return false, nil
	}
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster for pending assignment %q: %w", pending.String(), err))
	}

	// The observed placement (Status) is Cluster Service reality and takes
	// precedence; fall back to the scheduler intent (Spec) when Status is unset.
	effectivePlacement := serviceProviderCluster.Status.ManagementClusterResourceID
	if effectivePlacement == nil {
		effectivePlacement = serviceProviderCluster.Spec.ManagementClusterResourceID
	}
	if effectivePlacement == nil {
		// Placement still in progress: keep the reservation so capacity stays held.
		return true, nil
	}
	// Keep only when the effective placement still points at this management cluster.
	return controllerutil.ResourceIDsEqual(effectivePlacement, managementClusterResourceID), nil
}
