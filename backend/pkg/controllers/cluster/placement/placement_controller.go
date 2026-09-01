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
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/kuberesources"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// PlacementControllerName is the single logical name for this controller. It is
// used for the workqueue name (a Prometheus label), the context controller name,
// and log values so metrics, ctx, and log fields never drift.
const PlacementControllerName = "Placement"

// swiftNICsPerHCP is the number of SWIFT NICs a single HostedControlPlane
// consumes. Every HCP needs exactly 3 (except clusters created with the 2024
// API version, which use fewer — but that version is being removed). Using 3 as
// a flat per-HCP cost is a conservative approximation that never overbooks a
// management cluster's swift-NIC capacity.
const swiftNICsPerHCP int64 = 3

// placementSyncer selects the management cluster a newly-created HCP should be
// scheduled onto and records that intent on ServiceProviderCluster.Spec.
// ManagementClusterResourceID. Status.ManagementClusterResourceID (the observed
// placement) continues to be written by ManagementClusterPlacementSync.
type placementSyncer struct {
	serviceProviderClusterLister      corelisters.ServiceProviderClusterLister
	clusterLister                     corelisters.ClusterLister
	managementClusterLister           fleetlisters.ManagementClusterLister
	managementClusterSchedulingLister fleetlisters.ManagementClusterSchedulingLister
	cosmosClient                      corecosmosstorage.ResourcesDBClient
	fleetDBClient                     fleetcosmosstorage.FleetDBClient
	clusterServiceClient              ocm.ClusterServiceClientSpec
}

var _ controllerutils.ClusterSyncer = (*placementSyncer)(nil)

// NewPlacementController creates the scheduling controller that resolves initial
// placement for a HostedControlPlane by choosing an eligible management cluster
// with sufficient swift-NIC capacity and writing it to
// ServiceProviderCluster.Spec.ManagementClusterResourceID.
func NewPlacementController(
	cosmosClient corecosmosstorage.ResourcesDBClient,
	fleetDBClient fleetcosmosstorage.FleetDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	managementClusterLister fleetlisters.ManagementClusterLister,
	managementClusterSchedulingLister fleetlisters.ManagementClusterSchedulingLister,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	_, clusterLister := informers.Clusters()

	syncer := &placementSyncer{
		serviceProviderClusterLister:      serviceProviderClusterLister,
		clusterLister:                     clusterLister,
		managementClusterLister:           managementClusterLister,
		managementClusterSchedulingLister: managementClusterSchedulingLister,
		cosmosClient:                      cosmosClient,
		fleetDBClient:                     fleetDBClient,
		clusterServiceClient:              clusterServiceClient,
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
// Spec.ManagementClusterResourceID (scheduler intent) resolved. There is work
// whenever Spec is nil: either a fresh capacity-aware selection (Spec and Status
// both nil) or a rollout backfill from the observed Status placement (Spec nil,
// Status set).
func (c *placementSyncer) needsWork(serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	return serviceProviderCluster.Spec.ManagementClusterResourceID == nil
}

// SyncOnce resolves placement for a single HCP cluster and records it on
// ServiceProviderCluster.Spec.ManagementClusterResourceID.
func (c *placementSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.V(1).Info("ServiceProviderCluster not found in cache, skipping")
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster from cache: %w", err))
	}
	if !c.needsWork(serviceProviderCluster) {
		logger.V(1).Info("ServiceProviderCluster already has Spec.ManagementClusterResourceID, skipping")
		return nil
	}

	// Do not place — or reserve capacity for — a cluster whose deletion has already
	// been requested. The frontend records the request as
	// HCPOpenShiftCluster.ServiceProviderProperties.DeletionTimestamp, the same
	// signal every sibling creation/deletion controller gates on.
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	if err == nil && cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		logger.V(1).Info("HCP is being deleted; skipping placement")
		return nil
	}

	// Old records: the HCP was already placed by ManagementClusterPlacementSync
	// (Status.ManagementClusterResourceID mirrors the Cluster Service placement)
	// before the scheduler-intent Spec field existed, so Spec is nil while Status
	// is set. Backfill Spec from the observed Status placement rather than
	// fresh-scheduling, so downstream Cluster Service creation adopts the existing
	// placement instead of selecting a possibly different management cluster.
	if serviceProviderCluster.Status.ManagementClusterResourceID != nil {
		if err := c.setSpecPlacement(ctx, key, serviceProviderCluster.Status.ManagementClusterResourceID); err != nil {
			return err
		}
		logger.Info("backfilled management cluster placement intent from observed status",
			"managementClusterID", serviceProviderCluster.Status.ManagementClusterResourceID.String())
		return nil
	}

	// Rollout race: an old record created by a prior backend version can have a
	// Cluster Service ID assigned — and a placement already decided by Cluster
	// Service — while both Spec and Status ManagementClusterResourceID are still
	// nil (the observed-placement mirror, ManagementClusterPlacementSync, has not
	// caught up yet). Fresh-selecting here could pick a different management
	// cluster than the one Cluster Service already committed to. Instead, ask
	// Cluster Service where it placed the cluster (by the pending CS ID) and
	// backfill Spec from that.
	// This is migration behavior from CS driven placement to RP driven placement
	// and can be removed once rollout completes.
	if chosen, handled, err := c.backfillFromClusterService(ctx, key); err != nil {
		return err
	} else if handled {
		if chosen == nil {
			// A pending CS ID exists but Cluster Service has not reported a placement
			// yet: defer rather than fresh-select, to avoid diverging from the
			// placement Cluster Service will eventually report.
			logger.Info("cluster has a pending Cluster Service ID but Cluster Service has not reported a placement yet; deferring placement")
			return nil
		}
		if err := c.setSpecPlacement(ctx, key, chosen); err != nil {
			return err
		}
		logger.Info("backfilled management cluster placement intent from Cluster Service", "managementClusterID", chosen.String())
		return nil
	}

	// Fresh capacity-aware selection: gather candidate management clusters paired
	// with their scheduling documents, then let selectByCapacity perform all
	// candidate elimination and choose the emptiest eligible one.
	candidates, err := c.gatherSchedulingCandidates(ctx)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to gather scheduling candidates for %s: %w", key.HCPClusterName, err))
	}
	chosen, err := selectByCapacity(candidates)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to select management cluster for %s: %w", key.HCPClusterName, err))
	}

	// Reserve capacity on the chosen management cluster before recording the
	// placement intent, so concurrent decisions do not overbook it. A crash
	// between the reservation and the Spec write is safe: the reservation is
	// preserved while Spec is nil and a re-run may pick the same or a different
	// management cluster (stale reservations are cleaned up later).
	clusterResourceID := key.GetResourceID()
	if err := c.reservePendingAssignment(ctx, chosen, clusterResourceID); err != nil {
		return err
	}
	if err := c.setSpecPlacement(ctx, key, chosen); err != nil {
		return err
	}
	logger.Info("assigned management cluster placement", "managementClusterID", chosen.String())
	return nil
}

// backfillFromClusterService handles the rollout-race migration edge case. When
// the cluster document carries a PendingClusterServiceID (a placement already
// decided by a prior backend version / Cluster Service), it resolves the
// Cluster-Service-reported provision shard back to a management cluster resource
// ID.
//
// It returns handled=true when the caller must NOT fresh-select: either the
// placement was resolved (chosen set to the management cluster resource ID) or
// Cluster Service knows the cluster but has not reported a placement yet (chosen
// nil — the caller should defer to avoid diverging from the placement Cluster
// Service will eventually report).
//
// It returns handled=false when the caller SHOULD fresh-select: there is no
// pending CS ID, or Cluster Service returns 404 (not found) for the pending CS
// ID. A 404 means no Cluster Service cluster exists for the pending ID yet. That
// is the normal case for a brand-new record too: PendingClusterServiceID
// assignment is not gated on placement, so a new cluster can carry a pending ID
// before its Cluster Service cluster is created (creation happens in
// ClusterClusterServiceCreate, which waits for Spec). It also covers the old
// rollout edge case where a prior backend recorded PendingClusterServiceID then
// crashed / lost leadership before creating the cluster in Cluster Service. In
// every 404 case there is no committed placement to preserve, so a fresh
// capacity-aware selection is safe. Every other error is transient and is
// returned so the workqueue retries.
func (c *placementSyncer) backfillFromClusterService(ctx context.Context, key controllerutils.HCPClusterKey) (chosen *azcorearm.ResourceID, handled bool, err error) {
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	pendingClusterServiceID := cluster.ServiceProviderProperties.PendingClusterServiceID
	if pendingClusterServiceID == nil {
		return nil, false, nil
	}

	chosen, err = c.resolvePlacementFromClusterService(ctx, *pendingClusterServiceID)
	if err != nil {
		// A 404 from Cluster Service means no Cluster Service cluster exists for
		// this pending ID yet. This is the normal case for a new record (the pending
		// ID is assigned before placement, and the Cluster Service cluster is only
		// created once Spec is resolved) as well as the old rollout case (a prior
		// backend recorded PendingClusterServiceID then crashed before creating it
		// in Cluster Service). Either way there is no committed placement to diverge
		// from, so fall through to a fresh capacity-aware selection instead of
		// deferring forever. Any other error is transient — propagate it so the
		// workqueue retries.
		var ocmError *ocmerrors.Error
		if errors.As(err, &ocmError) && ocmError.Status() == http.StatusNotFound {
			utils.LoggerFromContext(ctx).Info("pending Cluster Service ID has no cluster in Cluster Service (404); proceeding with fresh placement",
				"clusterServiceID", pendingClusterServiceID.String())
			return nil, false, nil
		}
		return nil, true, err
	}
	return chosen, true, nil
}

// resolvePlacementFromClusterService asks Cluster Service where it already placed
// a cluster (by its Cluster Service ID) and maps the reported provision shard
// back to a management cluster resource ID. It returns (nil, nil) when Cluster
// Service has not yet reported a provision shard, or when no known management
// cluster matches it yet — in both cases the caller should retry later rather
// than fresh-select.
func (c *placementSyncer) resolvePlacementFromClusterService(ctx context.Context, clusterServiceID metadataapi.InternalID) (*azcorearm.ResourceID, error) {
	csShard, err := c.clusterServiceClient.GetClusterProvisionShard(ctx, clusterServiceID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get provision shard from Cluster Service for %q: %w", clusterServiceID.String(), err))
	}
	if len(csShard.HREF()) == 0 {
		return nil, nil // provision shard not yet allocated by Cluster Service
	}
	provisionShardID, err := metadataapi.NewInternalID(csShard.HREF())
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse provision shard href %q: %w", csShard.HREF(), err))
	}
	managementCluster, err := c.managementClusterLister.GetByCSProvisionShardID(ctx, provisionShardID.ID())
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil, nil // provision shard not yet mapped to a known management cluster
	}
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to resolve provision shard %q to a management cluster: %w", provisionShardID.ID(), err))
	}
	return managementCluster.ResourceID, nil
}

// schedulingCandidate pairs a management cluster with its scheduling document.
// scheduling is nil when the management cluster has no stamp identifier or no
// scheduling document in the cache yet; selectByCapacity records that as an
// elimination reason.
type schedulingCandidate struct {
	managementCluster *fleetapi.ManagementCluster
	scheduling        *fleetapi.ManagementClusterScheduling
}

// gatherSchedulingCandidates lists management clusters and pairs each with its
// scheduling document read from the informer cache. It performs no elimination —
// that is selectByCapacity's job — so every non-nil management cluster is
// returned, with a nil scheduling document when none is cached.
func (c *placementSyncer) gatherSchedulingCandidates(ctx context.Context) ([]schedulingCandidate, error) {
	managementClusters, err := c.managementClusterLister.List(ctx)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to list management clusters: %w", err))
	}

	candidates := make([]schedulingCandidate, 0, len(managementClusters))
	for _, managementCluster := range managementClusters {
		if managementCluster == nil || managementCluster.ResourceID == nil {
			continue
		}
		candidate := schedulingCandidate{managementCluster: managementCluster}
		// The scheduling document is a singleton child fetched by the parent stamp
		// identifier. Without a stamp identifier there is nothing to fetch; leave
		// scheduling nil and let selectByCapacity report the reason.
		if stampIdentifier := managementCluster.GetStampIdentifier(); stampIdentifier != "" {
			scheduling, err := c.managementClusterSchedulingLister.Get(ctx, stampIdentifier)
			switch {
			case cosmosstorageutils.IsNotFoundError(err):
				// No capacity data cached yet: leave scheduling nil.
			case err != nil:
				return nil, utils.TrackError(fmt.Errorf("failed to get scheduling document for management cluster %q from cache: %w", managementCluster.ResourceID.String(), err))
			default:
				candidate.scheduling = scheduling
			}
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

// managementClusterCandidate pairs an eligible management cluster's resource ID
// with its computed available swift-NIC capacity.
type managementClusterCandidate struct {
	resourceID *azcorearm.ResourceID
	available  int64
}

// selectByCapacity is a pure function that performs ALL candidate elimination and
// capacity-based selection in one place, so the scheduling decision lives in a
// single unit-testable function rather than being split across the reconcile.
//
// From the gathered (management cluster, scheduling document) pairs it eliminates
// ineligible candidates — recording a human-readable reason for each — then, among
// the candidates whose available swift-NIC capacity is at least swiftNICsPerHCP,
// returns the one with the HIGHEST available capacity (spread: place each new HCP
// on the emptiest management cluster so load is distributed evenly rather than
// concentrated). Ties are broken deterministically by the lowest resource ID
// string so the selection is stable.
//
// When nothing fits it returns an error that enumerates why every candidate was
// eliminated, so the decision can be debugged from the error alone.
//
// TODO: leverage CPU and memory as well as the average HCP resource consumption in the region for more elaborate capacity based placement decisions.
func selectByCapacity(candidates []schedulingCandidate) (*azcorearm.ResourceID, error) {
	var chosen managementClusterCandidate
	found := false
	var eliminated []string

	for _, candidate := range candidates {
		managementCluster := candidate.managementCluster
		if managementCluster == nil || managementCluster.ResourceID == nil {
			continue
		}
		id := managementCluster.ResourceID.String()
		if reason := ineligibilityReason(managementCluster, candidate.scheduling); reason != "" {
			eliminated = append(eliminated, fmt.Sprintf("%s: %s", id, reason))
			continue
		}
		available := computeAvailableSwiftNICs(candidate.scheduling)
		if available < swiftNICsPerHCP {
			eliminated = append(eliminated, fmt.Sprintf("%s: insufficient swift-NIC capacity (available %d, need %d)", id, available, swiftNICsPerHCP))
			continue
		}

		fit := managementClusterCandidate{resourceID: managementCluster.ResourceID, available: available}
		switch {
		case !found:
			chosen = fit
			found = true
		case fit.available > chosen.available:
			chosen = fit
		case fit.available == chosen.available && fit.resourceID.String() < chosen.resourceID.String():
			chosen = fit
		}
	}

	if !found {
		// Only append the elimination reasons when there are any; otherwise the
		// message would end with a dangling ": " (e.g. zero candidates, or every
		// candidate skipped for a nil ResourceID before a reason was recorded).
		if len(eliminated) == 0 {
			return nil, utils.TrackError(fmt.Errorf("no eligible management cluster with at least %d available swift NICs among %d candidate(s)",
				swiftNICsPerHCP, len(candidates)))
		}
		return nil, utils.TrackError(fmt.Errorf("no eligible management cluster with at least %d available swift NICs among %d candidate(s): %s",
			swiftNICsPerHCP, len(candidates), strings.Join(eliminated, "; ")))
	}
	return chosen.resourceID, nil
}

// ineligibilityReason returns a human-readable reason a management cluster cannot
// accept a new HCP, or "" when it is eligible. A candidate is eligible only when
// it is Schedulable, Ready, has a stamp identifier, and has an observed scheduling
// document (the source of its swift-NIC capacity).
func ineligibilityReason(managementCluster *fleetapi.ManagementCluster, scheduling *fleetapi.ManagementClusterScheduling) string {
	if managementCluster.Spec.SchedulingPolicy != fleetapi.ManagementClusterSchedulingPolicySchedulable {
		return fmt.Sprintf("scheduling policy is %q, not %q", managementCluster.Spec.SchedulingPolicy, fleetapi.ManagementClusterSchedulingPolicySchedulable)
	}
	if !meta.IsStatusConditionTrue(managementCluster.Status.Conditions, string(fleetapi.ManagementClusterConditionReady)) {
		return "management cluster is not Ready"
	}
	if managementCluster.GetStampIdentifier() == "" {
		return "management cluster has no stamp identifier"
	}
	if scheduling == nil {
		return "no scheduling/capacity data available"
	}
	return ""
}

// computeAvailableSwiftNICs returns the swift-NIC capacity still available on a
// management cluster:
//
//	available = ScaleCeiling.Capacity[swift-nic]
//	          - ObservedResources.Usage[swift-nic]
//	          - len(NotReadyResourceIDs) * swiftNICsPerHCP
//	          - len(PendingAssignedClusters) * swiftNICsPerHCP
//
// Ready HCPs are already reflected in Usage, so they are not reserved again.
// NotReady HCPs may not yet consume their NICs, so each reserves swiftNICsPerHCP.
// Pending (just-scheduled, not-yet-observed) HCPs likewise reserve
// swiftNICsPerHCP. Capacity is bounded against the ScaleCeiling (max node count)
// so the estimate reflects the worst case. Empty/nil list entries do not
// correspond to a real HCP and are not counted toward the reservation.
func computeAvailableSwiftNICs(scheduling *fleetapi.ManagementClusterScheduling) int64 {
	ceiling := swiftNICCount(scheduling.Status.ScaleCeiling.Capacity)
	usage := swiftNICCount(scheduling.Status.ObservedResources.Usage)
	notReady := countNonEmpty(scheduling.Status.NotReadyResourceIDs) * swiftNICsPerHCP
	pending := countNonNilResourceIDs(scheduling.Status.PendingAssignedClusters) * swiftNICsPerHCP
	return ceiling - usage - notReady - pending
}

// countNonNilResourceIDs counts the non-nil entries of a resource ID slice; a
// nil entry does not correspond to a real HCP and must not reserve capacity.
func countNonNilResourceIDs(ids []*azcorearm.ResourceID) int64 {
	var count int64
	for _, id := range ids {
		if id != nil {
			count++
		}
	}
	return count
}

// countNonEmpty counts the non-empty entries of a string slice.
func countNonEmpty(values []string) int64 {
	var count int64
	for _, value := range values {
		if value != "" {
			count++
		}
	}
	return count
}

// swiftNICCount returns the swift-NIC quantity in a ResourceList as an int64,
// or 0 when the resource is absent.
func swiftNICCount(resources corev1.ResourceList) int64 {
	quantity, ok := resources[kuberesources.SwiftNICResourceName]
	if !ok {
		return 0
	}
	return quantity.Value()
}

// reservePendingAssignment adds clusterResourceID to the chosen management
// cluster's PendingAssignedClusters list (idempotently). On a write conflict it
// returns an error so the workqueue retries the whole reconcile with backoff.
func (c *placementSyncer) reservePendingAssignment(ctx context.Context, managementClusterResourceID, clusterResourceID *azcorearm.ResourceID) error {
	if managementClusterResourceID.Parent == nil {
		return utils.TrackError(fmt.Errorf("management cluster resource ID %q has no parent stamp", managementClusterResourceID.String()))
	}
	stampIdentifier := managementClusterResourceID.Parent.Name
	schedulingCRUD := c.fleetDBClient.Stamps().ManagementClusters(stampIdentifier).Scheduling()

	existing, err := schedulingCRUD.Get(ctx, fleetapi.SchedulingResourceName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get scheduling document for management cluster %q: %w", managementClusterResourceID.String(), err))
	}
	for _, pending := range existing.Status.PendingAssignedClusters {
		if controllerutil.ResourceIDsEqual(pending, clusterResourceID) {
			return nil // already reserved
		}
	}

	updated := existing.DeepCopy()
	// TODO: also increase the estimated resource utilization for this reservation
	// (e.g. reflect the swift-NIC cost of the pending HCP in
	// ObservedResources.Usage) so capacity accounting reflects the pending
	// assignment directly, not only indirectly via the PendingAssignedClusters
	// count.
	updated.Status.PendingAssignedClusters = append(updated.Status.PendingAssignedClusters, coreapi.DeepCopyResourceID(clusterResourceID))
	if _, err := schedulingCRUD.Replace(ctx, updated, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to reserve pending assignment on management cluster %q: %w", managementClusterResourceID.String(), err))
	}
	return nil
}

// setSpecPlacement records the chosen management cluster on
// ServiceProviderCluster.Spec.ManagementClusterResourceID. The base document is
// read from the informer cache (not a live Cosmos Get); the cached copy carries
// the etag that guards the optimistic Replace, so a stale cache can only produce
// a write conflict — never a lost update. On such a conflict it returns an error
// so the workqueue retries the whole reconcile with backoff.
func (c *placementSyncer) setSpecPlacement(ctx context.Context, key controllerutils.HCPClusterKey, chosen *azcorearm.ResourceID) error {
	existing, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster from cache: %w", err))
	}
	if !c.needsWork(existing) {
		return nil
	}

	replacement := existing.DeepCopy()
	replacement.Spec.ManagementClusterResourceID = coreapi.DeepCopyResourceID(chosen)
	spcCRUD := c.cosmosClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if _, err := spcCRUD.Replace(ctx, replacement, nil); err != nil {
		// A precondition failure means we lost an optimistic-concurrency race: another
		// writer updated the ServiceProviderCluster after our cached base was read.
		// Do not treat this as an error — re-erroring would only add noise and a
		// redundant requeue. The next reconcile re-reads a fresh base and the
		// needsWork gate short-circuits if Spec is now already set.
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			return nil
		}
		return utils.TrackError(fmt.Errorf("failed to update ServiceProviderCluster placement: %w", err))
	}
	return nil
}
