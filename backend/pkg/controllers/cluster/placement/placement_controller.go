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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/kuberesources"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// PlacementControllerName is the single logical name for this controller. It is
// used for the workqueue name (a Prometheus label), the context controller name,
// and log values so metrics, ctx, and log fields never drift.
const PlacementControllerName = "Placement"

// swiftNICsPerHCP is the number of SWIFT NICs a highly-available (default)
// HostedControlPlane consumes: one NIC per control-plane replica, three
// replicas. The same value doubles as the conservative fallback whenever a
// cluster's control-plane availability cannot be determined.
const swiftNICsPerHCP int64 = 3

// singleReplicaSwiftNICsPerHCP is the number of SWIFT NICs a SingleReplica
// HostedControlPlane consumes: its single control-plane replica needs one NIC.
const singleReplicaSwiftNICsPerHCP int64 = 1

// placementRetryInterval is how long to wait before re-checking a HostedControlPlane
// that currently has no eligible management cluster with capacity. A capacity
// shortfall is an expected transient (capacity frees up as HCPs churn), so the
// scheduler re-checks on a fixed sub-30s cadence rather than error-based backoff.
const placementRetryInterval = 29 * time.Second

// swiftNICsForControlPlaneAvailability returns the number of SWIFT NICs a single
// HostedControlPlane with the given control-plane availability consumes: a
// SingleReplica control plane runs one replica and needs one NIC, while a
// highly-available (default) control plane needs the full swiftNICsPerHCP.
func swiftNICsForControlPlaneAvailability(availability coreapi.ControlPlaneAvailability) int64 {
	if availability == coreapi.SingleReplicaControlPlane {
		return singleReplicaSwiftNICsPerHCP
	}
	return swiftNICsPerHCP
}

// swiftNICsForResourceID resolves how many SWIFT NICs the HCP identified by
// clusterResourceID reserves, from its control-plane availability in the
// cluster informer cache. A cluster that cannot be resolved (nil ID, missing
// from the cache, or an unexpected lister error) reserves the conservative
// swiftNICsPerHCP maximum, so a cache miss never under-reserves capacity.
func (c *placementSyncer) swiftNICsForResourceID(ctx context.Context, clusterResourceID *azcorearm.ResourceID) int64 {
	if clusterResourceID == nil {
		return swiftNICsPerHCP
	}
	cluster, err := c.clusterLister.Get(ctx, clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName, clusterResourceID.Name)
	if err != nil {
		return swiftNICsPerHCP
	}
	return swiftNICsForControlPlaneAvailability(cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneAvailability)
}

// swiftNICReservation sums swiftNICsForResourceID over a slice of cluster
// resource IDs, skipping nil entries (a nil entry does not correspond to a
// real HCP and must not reserve capacity).
func (c *placementSyncer) swiftNICReservation(ctx context.Context, resourceIDs []*azcorearm.ResourceID) int64 {
	var total int64
	for _, resourceID := range resourceIDs {
		if resourceID == nil {
			continue
		}
		total += c.swiftNICsForResourceID(ctx, resourceID)
	}
	return total
}

// stampIdentifierFromResourceID extracts the parent stamp identifier from a
// management cluster's resource ID, or "" when it has none. This mirrors
// fleetapi.ManagementCluster.GetStampIdentifier(), which operates on a full
// ManagementCluster object; here only the bare resource ID is available (e.g.
// the chosen management cluster returned by selectByCapacity), so the two must
// be kept in sync by hand if the resource ID shape ever changes.
func stampIdentifierFromResourceID(resourceID *azcorearm.ResourceID) string {
	if resourceID == nil || resourceID.Parent == nil {
		return ""
	}
	return resourceID.Parent.Name
}

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
	enqueueAfter                      controllerutils.AfterEnqueuer
}

var _ controllerutils.ClusterSyncer = (*placementSyncer)(nil)

// NewPlacementController creates the scheduling controller that resolves initial
// placement for a HostedControlPlane by choosing an eligible management cluster
// with sufficient swift-NIC capacity and writing it to
// ServiceProviderCluster.Spec.ManagementClusterResourceID.
func NewPlacementController(
	cosmosClient corecosmosstorage.ResourcesDBClient,
	fleetDBClient fleetcosmosstorage.FleetDBClient,
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
	}

	controller := controllerutils.NewClusterWatchingController(
		PlacementControllerName,
		cosmosClient,
		informers,
		kubeApplierInformers,
		5*time.Minute, // Check every 5 minutes
		syncer,
	)

	// Assert the controller implements AfterEnqueuer so the syncer can schedule a
	// fixed-cadence requeue when placement is deferred for lack of capacity, rather
	// than relying on error-based rate-limited backoff. Panics at startup otherwise.
	if enqueuer, ok := controller.(controllerutils.AfterEnqueuer); ok {
		syncer.enqueueAfter = enqueuer
	} else {
		panic("PlacementController must implement AfterEnqueuer")
	}

	return controller
}

// needsWork reports whether placement is unresolved and the cluster is neither
// deleting nor terminal. Both documents must be present.
func (c *placementSyncer) needsWork(serviceProviderCluster *coreapi.ServiceProviderCluster, cluster *coreapi.HCPOpenShiftCluster) bool {
	if serviceProviderCluster.Spec.ManagementClusterResourceID != nil {
		return false
	}
	return cluster.ServiceProviderProperties.DeletionTimestamp == nil &&
		!cluster.ServiceProviderProperties.ProvisioningState.IsTerminal()
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

	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.V(1).Info("HCP cluster not found in cache, skipping")
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	if !c.needsWork(serviceProviderCluster, cluster) {
		logger.V(1).Info("HCP placement is resolved, or the cluster is deleting or terminal; skipping placement")
		return nil
	}
	// Fresh capacity-aware selection: evaluate management clusters paired
	// with their scheduling documents, then let selectByCapacity perform all
	// candidate elimination and choose the emptiest eligible one. The new HCP
	// reserves swift NICs according to its own control-plane availability (a
	// SingleReplica control plane needs one NIC, else swiftNICsPerHCP).
	evaluations, err := c.evaluateManagementClusters(ctx)
	if err != nil {
		return err
	}
	requiredSwiftNICs := swiftNICsForControlPlaneAvailability(cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneAvailability)
	chosen, condition := selectByCapacity(evaluations, requiredSwiftNICs)
	if chosen == nil {
		if err := c.recordPlacementDecision(ctx, key, serviceProviderCluster, nil, condition); err != nil {
			return err
		}
		logger.Info("no eligible management cluster with capacity; deferring placement",
			"reason", condition.Reason, "retryAfter", placementRetryInterval.String())
		if c.enqueueAfter != nil {
			c.enqueueAfter.EnqueueAfter(key, placementRetryInterval)
		}
		return nil
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
	if err := c.recordPlacementDecision(ctx, key, serviceProviderCluster, chosen, condition); err != nil {
		return err
	}
	logger.Info("assigned management cluster placement", "managementClusterID", chosen.String())
	return nil
}

// eligibility classifies whether a management cluster is a viable placement
// candidate: eligible, definitively ineligible (a known blocker), or
// unknownEligibility when the observations needed to decide are incomplete.
type eligibility string

const (
	eligible           eligibility = "Eligible"
	ineligible         eligibility = "Ineligible"
	unknownEligibility eligibility = "Unknown"
)

// managementClusterEvaluation records a management cluster's eligibility and
// available capacity. reason explains an ineligible or unknown result.
type managementClusterEvaluation struct {
	resourceID         *azcorearm.ResourceID
	eligibility        eligibility
	reason             string
	availableResources corev1.ResourceList
}

// evaluateManagementClusters resolves eligibility and available capacity from
// cached observations. It retains ineligible and unknown results so selection
// can distinguish known exhaustion from incomplete observations.
func (c *placementSyncer) evaluateManagementClusters(ctx context.Context) ([]managementClusterEvaluation, error) {
	managementClusters, err := c.managementClusterLister.List(ctx)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to list management clusters: %w", err))
	}

	evaluations := make([]managementClusterEvaluation, 0, len(managementClusters))
	for _, managementCluster := range managementClusters {
		if managementCluster == nil || managementCluster.ResourceID == nil {
			continue
		}
		var scheduling *fleetapi.ManagementClusterScheduling
		if stampIdentifier := managementCluster.GetStampIdentifier(); stampIdentifier != "" {
			var err error
			scheduling, err = c.managementClusterSchedulingLister.Get(ctx, stampIdentifier)
			switch {
			case cosmosstorageutils.IsNotFoundError(err):
				// No capacity data cached yet: leave scheduling nil.
				scheduling = nil
			case err != nil:
				return nil, utils.TrackError(fmt.Errorf("failed to get scheduling document for management cluster %q from cache: %w", managementCluster.ResourceID.String(), err))
			}
		}

		evaluation := managementClusterEvaluation{
			resourceID:         managementCluster.ResourceID,
			availableResources: c.availableResources(ctx, scheduling),
		}
		// Policy and readiness take precedence over missing capacity observations.
		switch {
		case managementCluster.Spec.SchedulingPolicy == fleetapi.ManagementClusterSchedulingPolicyUnschedulable:
			evaluation.eligibility, evaluation.reason = ineligible, "management cluster is not schedulable"
		case managementCluster.Spec.SchedulingPolicy != fleetapi.ManagementClusterSchedulingPolicySchedulable:
			evaluation.eligibility, evaluation.reason = unknownEligibility, fmt.Sprintf("unknown scheduling policy %q", managementCluster.Spec.SchedulingPolicy)
		case meta.IsStatusConditionFalse(managementCluster.Status.Conditions, string(fleetapi.ManagementClusterConditionReady)):
			evaluation.eligibility, evaluation.reason = ineligible, "management cluster is not Ready"
		case !meta.IsStatusConditionTrue(managementCluster.Status.Conditions, string(fleetapi.ManagementClusterConditionReady)):
			evaluation.eligibility, evaluation.reason = unknownEligibility, "management cluster readiness is unknown"
		case scheduling == nil:
			evaluation.eligibility, evaluation.reason = unknownEligibility, "no scheduling/capacity data available"
		case !meta.IsStatusConditionTrue(scheduling.Status.Conditions, fleetapi.ConditionTypeCapacityDataCurrent) ||
			!meta.IsStatusConditionTrue(scheduling.Status.Conditions, fleetapi.ConditionTypeScalingDataCurrent):
			evaluation.eligibility, evaluation.reason = unknownEligibility, "scheduling/capacity data is not current"
		default:
			evaluation.eligibility = eligible
		}
		evaluations = append(evaluations, evaluation)
	}
	return evaluations, nil
}

// selectByCapacity chooses the eligible cluster with the most available swift-NIC
// capacity, provided it meets requiredSwiftNICs. Ties favor the lowest resource ID.
// A known fit yields CapacityAvailable=True regardless of other unknown evaluations.
// Only when no fit exists do we collect rejection details and report Unknown or False.
//
// TODO: leverage CPU and memory as well as the average HCP resource consumption in the region for more elaborate capacity based placement decisions.
func selectByCapacity(evaluations []managementClusterEvaluation, requiredSwiftNICs int64) (*azcorearm.ResourceID, metav1.Condition) {
	var chosen *azcorearm.ResourceID
	var highestAvailable int64
	for _, evaluation := range evaluations {
		if evaluation.eligibility != eligible {
			continue
		}
		available := swiftNICCount(evaluation.availableResources)
		if available < requiredSwiftNICs {
			continue
		}
		if chosen == nil || available > highestAvailable ||
			(available == highestAvailable && evaluation.resourceID.String() < chosen.String()) {
			chosen = evaluation.resourceID
			highestAvailable = available
		}
	}
	if chosen == nil {
		return nil, noPlacementCondition(evaluations, requiredSwiftNICs)
	}
	return chosen, metav1.Condition{
		Type:    coreapi.CapacityAvailableConditionType,
		Status:  metav1.ConditionTrue,
		Reason:  coreapi.CapacityReasonAvailable,
		Message: "placed on " + chosen.Name,
	}
}

// noPlacementCondition explains a failed selection. Call only after finding no fit:
// every eligible cluster therefore lacks capacity. Any unknown eligibility prevents
// declaring capacity unavailable, even when other clusters have known blockers.
func noPlacementCondition(evaluations []managementClusterEvaluation, requiredSwiftNICs int64) metav1.Condition {
	var unknownCount, insufficientCapacityCount int
	var eliminated []string
	for _, evaluation := range evaluations {
		id := evaluation.resourceID.String()
		switch evaluation.eligibility {
		case eligible:
			insufficientCapacityCount++
			eliminated = append(eliminated, fmt.Sprintf("%s: insufficient swift-NIC capacity (available %d, need %d)", id, swiftNICCount(evaluation.availableResources), requiredSwiftNICs))
		case unknownEligibility:
			unknownCount++
			eliminated = append(eliminated, fmt.Sprintf("%s: %s", id, evaluation.reason))
		default:
			eliminated = append(eliminated, fmt.Sprintf("%s: %s", id, evaluation.reason))
		}
	}

	condition := metav1.Condition{Type: coreapi.CapacityAvailableConditionType}
	switch {
	case unknownCount > 0:
		condition.Status = metav1.ConditionUnknown
		condition.Reason = coreapi.CapacityReasonEvaluationIncomplete
		condition.Message = "capacity availability could not be established"
	case insufficientCapacityCount > 0:
		condition.Status = metav1.ConditionFalse
		condition.Reason = coreapi.CapacityReasonInsufficientCapacity
		condition.Message = fmt.Sprintf("no eligible management cluster with at least %d available swift NICs among %d management cluster(s)", requiredSwiftNICs, len(evaluations))
	default:
		condition.Status = metav1.ConditionFalse
		condition.Reason = coreapi.CapacityReasonNoEligibleManagementCluster
		condition.Message = "no management cluster is currently eligible"
	}
	if len(eliminated) > 0 {
		condition.Message += ": " + strings.Join(eliminated, " ; ")
	}
	return condition
}

// availableResources returns the resources still available on a management
// cluster: ScaleCeiling.Capacity minus the higher of ObservedResources.Usage
// and ObservedResources.Requests (per resource), with swift-NIC further
// reduced by the per-cluster reservation for each NotReady or Pending HCP.
// A nil scheduling document yields an empty ResourceList
//
//	available = ScaleCeiling.Capacity
//	          - max(ObservedResources.Usage, ObservedResources.Requests)
//	          - sum(swiftNICsForResourceID(each NotReadyResourceIDs entry))    (swift-nic only)
//	          - sum(swiftNICsForResourceID(each PendingAssignedClusters entry)) (swift-nic only)
//
// Usage reflects actual consumption (from live metrics); Requests reflects
// what the scheduler has already committed the node to, which kube-scheduler
// enforces regardless of real usage. A resource whose pods run hotter than
// their requests (Usage > Requests) is under-reported by Requests alone, and
// a resource that is requested but idle (Requests > Usage) is over-reported by
// Usage alone — taking the higher of the two never understates consumption.
// For swift-NIC, Usage and Requests are always equal by construction (NICs
// have no real utilization metric), so this is a no-op there.
//
// NotReady and Pending HCPs each reserve their own swift-NIC count, resolved
// per-cluster from control-plane availability via swiftNICsForResourceID (1
// for SingleReplica, else swiftNICsPerHCP); nil list entries reserve nothing.
// Capacity is bounded against the ScaleCeiling (max node count), reflecting
// the worst case.
func (c *placementSyncer) availableResources(ctx context.Context, scheduling *fleetapi.ManagementClusterScheduling) corev1.ResourceList {
	if scheduling == nil {
		return corev1.ResourceList{}
	}
	available := scheduling.Status.ScaleCeiling.Capacity.DeepCopy()
	if available == nil {
		available = corev1.ResourceList{}
	}
	usage := scheduling.Status.ObservedResources.Usage
	requests := scheduling.Status.ObservedResources.Requests
	for name := range sets.KeySet(usage).Union(sets.KeySet(requests)) {
		consumed := usage[name]
		if requested := requests[name]; requested.Cmp(consumed) > 0 {
			consumed = requested
		}
		quantity := available[name]
		quantity.Sub(consumed)
		available[name] = quantity
	}

	swiftNICReservation := c.swiftNICReservation(ctx, scheduling.Status.NotReadyResourceIDs) + c.swiftNICReservation(ctx, scheduling.Status.PendingAssignedClusters)
	if swiftNICReservation != 0 {
		quantity := available[kuberesources.SwiftNICResourceName]
		quantity.Sub(*resource.NewQuantity(swiftNICReservation, resource.DecimalSI))
		available[kuberesources.SwiftNICResourceName] = quantity
	}

	return available
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
	stampIdentifier := stampIdentifierFromResourceID(managementClusterResourceID)
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
	// entries.
	updated.Status.PendingAssignedClusters = append(updated.Status.PendingAssignedClusters, coreapi.DeepCopyResourceID(clusterResourceID))
	if _, err := schedulingCRUD.Replace(ctx, updated, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to reserve pending assignment on management cluster %q: %w", managementClusterResourceID.String(), err))
	}
	return nil
}

// recordPlacementDecision persists a placement decision on the ServiceProviderCluster: it sets
// the CapacityAvailable condition always, and Spec.ManagementClusterResourceID as well when
// chosen is non-nil (a resolved placement). Writing both on the SAME Replace keeps the
// condition and the resolved placement consistent — one update, not a follow-up
// reconcile.
func (c *placementSyncer) recordPlacementDecision(ctx context.Context, key controllerutils.HCPClusterKey, serviceProviderCluster *coreapi.ServiceProviderCluster, chosen *azcorearm.ResourceID, condition metav1.Condition) error {
	updated := serviceProviderCluster.DeepCopy()
	if chosen != nil {
		updated.Spec.ManagementClusterResourceID = coreapi.DeepCopyResourceID(chosen)
	}
	if updated.Status.Placement == nil {
		updated.Status.Placement = &coreapi.ServiceProviderClusterPlacementStatus{}
	}
	meta.SetStatusCondition(&updated.Status.Placement.Conditions, condition)
	if !controllerutil.NeedsUpdate(serviceProviderCluster, updated) {
		return nil
	}

	spcCRUD := c.cosmosClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if _, err := spcCRUD.Replace(ctx, updated, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to record ServiceProviderCluster placement: %w", err))
	}
	return nil
}
