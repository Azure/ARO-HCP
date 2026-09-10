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

package dataplaneworkloads

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// DataPlaneOIDCFederationIntentControllerName is the single source of
// truth for this controller's name. It is used for the workqueue name (a
// Prometheus label), context/logger controller name, and log fields.
const DataPlaneOIDCFederationIntentControllerName = "DataPlaneOIDCFederationIntent"

// dataPlaneOIDCFederationIntentSyncer keeps
// ServiceProviderCluster.Status.ManagedIdentitiesWithDataPlaneOIDCFederation
// in sync with ServiceProviderCluster.Status.ManagedIdentityDetails.
//
// It does not call Azure. It only marks desired phases:
//   - Data-plane operator identities in ManagedIdentityDetails
//     (MSIBasedDetails false) with non-nil ClientID, PrincipalID, and TenantID
//     are added as PendingConfigure (or left Configured / PendingConfigure if
//     already present). MSI-based identities (control plane operators and the
//     ServiceManagedIdentity, MSIBasedDetails true) are ignored, even when the
//     same UAMI is still used as CP or SMI.
//   - Identities present in ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation
//     but no longer among those data-plane details entries are marked
//     PendingDeconfigure unless they are already Deconfigured. The first
//     transition stamps DeconfigureTimestamp to now (when deconfigure was
//     requested). The executor waits 24h from that time on a live cluster.
//     Cluster deletion still stamps the request time; the executor ignores
//     the wait when DeletionTimestamp is set.
//   - Data-plane identities still in ManagedIdentityDetails whose ClientID,
//     PrincipalID, or TenantID is temporarily unset are left as-is so a
//     transient fetch error does not deconfigure them.
type dataPlaneOIDCFederationIntentSyncer struct {
	clock                        utilsclock.PassiveClock
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
}

var _ controllerutils.ClusterSyncer = (*dataPlaneOIDCFederationIntentSyncer)(nil)

// NewDataPlaneOIDCFederationIntentController creates a cluster-watching
// controller that marks data-plane OIDC federation phases from resolved
// managed identity details.
func NewDataPlaneOIDCFederationIntentController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	backendInformers coreinformers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &dataPlaneOIDCFederationIntentSyncer{
		clock:                        clock,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		resourcesDBClient:            resourcesDBClient,
	}

	return controllerutils.NewClusterWatchingController(
		DataPlaneOIDCFederationIntentControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		1*time.Minute,
		syncer,
	)
}

func (s *dataPlaneOIDCFederationIntentSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := s.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster from cache: %w", err))
	}

	existingServiceProviderCluster, err := s.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		// The ServiceProviderCluster has not been created yet. The dedicated
		// CreateServiceProviderCluster controller creates it; we pick it up on a
		// later requeue once it exists.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	existingManagedIdentityDetails := existingServiceProviderCluster.Status.ManagedIdentityDetails
	// Deconfigure only after Cluster Service is gone. PendingClusterServiceID is
	// a create-time reservation and is not cleared on delete, so it is not part
	// of this signal.
	if s.clusterServiceGone(existingCluster) {
		// Treat deletion as an empty desired identity set so in-flight federation
		// is marked PendingDeconfigure and the executor can remove Azure FICs.
		existingManagedIdentityDetails = nil
	}

	desiredManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation, err := s.desiredDataPlaneOIDCFederationStatus(
		existingManagedIdentityDetails,
		existingServiceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation,
	)
	if err != nil {
		return err
	}

	replacement := existingServiceProviderCluster.DeepCopy()
	replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = desiredManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation

	if equality.Semantic.DeepEqual(existingServiceProviderCluster, replacement) {
		return nil
	}

	_, err = s.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}

	return nil
}

// clusterServiceGone reports whether Cluster Service no longer has a cluster
// for this HCP, so data-plane OIDC federation can be torn down.
//
// ClusterServiceID still set means ClusterDeletionClusterServiceIDClearer has
// not confirmed a CS 404 yet. That check is the "CS existed and is not gone"
// signal; this function never reaches the timestamp check while the ID is set.
//
// ClusterServiceID already nil is ambiguous: either CS was never created, or
// the clearer already dropped the ID after a 404. ClusterServiceDeletionTimestamp
// distinguishes those. While it is unset, ClusterClusterServiceDeleteDispatch is
// still in its 120s wait for an in-flight CS create to write ClusterServiceID.
// Once it is set, dispatch has issued DeleteCluster or concluded CS was never
// created. PendingClusterServiceID is not used: it is a create-time reservation
// and is not cleared on delete.
//
// UsesNewClusterDeletionApproach is required because dispatch never stamps
// ClusterServiceDeletionTimestamp for pre-existing in-flight deletes. Those
// clusters skip the 120s wait. Their ClusterServiceID is typically still set,
// so the ID check already holds teardown; the fallback only matters when that
// ID was never assigned.
func (s *dataPlaneOIDCFederationIntentSyncer) clusterServiceGone(cluster *coreapi.HCPOpenShiftCluster) bool {
	// TODO temporary check to skip the new deletion approach for Clusters that were created before the new approach was implemented.
	// This will be removed once all clusters whose deletion was triggered before the new approach is fully rolled out have been
	// fully deleted in all ARO-HCP permanent environments, for all regions.
	if !cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach {
		return false
	}

	return cluster.ServiceProviderProperties.DeletionTimestamp != nil &&
		cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil &&
		cluster.ServiceProviderProperties.ClusterServiceID == nil
}

// desiredDataPlaneOIDCFederationStatus computes the federation map that should
// be stored on the ServiceProviderCluster.
//
// Only data-plane operator identities are considered: ManagedIdentityDetails
// entries with MSIBasedDetails false. MSI-based entries (control plane
// operators and the ServiceManagedIdentity) are skipped so a UAMI that is
// still CP or SMI does not keep data-plane OIDC federation after the last
// data-plane operator leaves it.
//
// The map key is ResourceID + ClientID + PrincipalID + TenantID, so a change to
// any of those IDs is a new key. Both loops together cover identity rotation:
// the first loop adds PendingConfigure for the new key, and the second loop
// marks the old key PendingDeconfigure.
//
// The first transition to PendingDeconfigure stamps DeconfigureTimestamp from
// the controller clock (when deconfigure was requested). The executor waits
// 24h from that event on a live cluster. Already PendingDeconfigure entries
// keep their existing stamp so the wait is not reset.
func (s *dataPlaneOIDCFederationIntentSyncer) desiredDataPlaneOIDCFederationStatus(
	existingManagedIdentityDetails map[coreapi.ManagedIdentityDetailsKey]*coreapi.ManagedIdentityDetails,
	existingFederation map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus,
) (map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus, error) {
	desired := make(map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus, len(existingManagedIdentityDetails)+len(existingFederation))

	resolvedKeys := make(map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]struct{}, len(existingManagedIdentityDetails))
	unresolvedResourceIDs := map[string]struct{}{}

	// First loop: data-plane identities that are currently resolved in
	// ManagedIdentityDetails. These are the keys that should be federated.
	// Missing keys become PendingConfigure. Keys already PendingConfigure or
	// Configured are left as-is. Keys already PendingDeconfigure or
	// Deconfigured are flipped back to PendingConfigure because they are
	// desired again.
	for detailsKey, details := range existingManagedIdentityDetails {
		// MSIBasedDetails true is a control-plane operator or the
		// ServiceManagedIdentity. Data-plane OIDC federation is only for
		// data-plane operator identities (MSIBasedDetails false). Skipping
		// here also means a UAMI that is still CP or SMI does not keep
		// federation after the last data-plane operator leaves it.
		if detailsKey.MSIBasedDetails {
			continue
		}
		key, ok := details.AsDataplaneOIDCFederationKey()
		if !ok {
			// ClientID, PrincipalID, or TenantID is not set. This can be a
			// transient fetch failure, so do not deconfigure existing entries
			// for this ResourceID in the second loop.
			unresolvedResourceIDs[strings.ToLower(details.ResourceID.String())] = struct{}{}
			continue
		}
		resolvedKeys[key] = struct{}{}

		existing, hasExisting := existingFederation[key]
		if hasExisting && existing == nil {
			return nil, utils.TrackError(fmt.Errorf("ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation has a nil status for resource ID %s", key.ResourceID))
		}
		if !hasExisting {
			desired[key] = &coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				Phase: coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure,
			}
			continue
		}

		next := existing.DeepCopy()
		switch existing.Phase {
		case coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure,
			coreapi.ManagedIdentityDataplaneOIDCFederationPhaseDeconfigured:
			next.Phase = coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure
			next.DeconfigureTimestamp = nil
		}
		desired[key] = next
	}

	// Second loop: federation keys that the first loop did not mark as still
	// desired. Deconfigure only when the data-plane identity is gone from
	// ManagedIdentityDetails, or when ClientID/PrincipalID/TenantID changed
	// (the first loop recorded the new key; this loop sees the old key).
	// An MSI-based entry for the same ResourceID is not enough to keep
	// federation. If the data-plane identity is still in ManagedIdentityDetails
	// but those IDs are unset, keep the existing entry. A transient Azure
	// error in the fetch controller must not start deconfiguration.
	for key, existing := range existingFederation {
		if existing == nil {
			return nil, utils.TrackError(fmt.Errorf("ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation has a nil status for resource ID %s", key.ResourceID))
		}
		if _, stillDesired := resolvedKeys[key]; stillDesired {
			continue
		}
		if _, unresolved := unresolvedResourceIDs[strings.ToLower(key.ResourceID)]; unresolved {
			desired[key] = existing.DeepCopy()
			continue
		}
		next := existing.DeepCopy()
		switch existing.Phase {
		case coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure,
			coreapi.ManagedIdentityDataplaneOIDCFederationPhaseDeconfigured:
			// Already on the deconfigure path; leave as-is so a live-cluster
			// 24h wait is not reset, and a Deconfigured entry stays terminal.
		default:
			next.Phase = coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure
			requestedAt := metav1.NewTime(s.clock.Now())
			next.DeconfigureTimestamp = &requestedAt
		}
		desired[key] = next
	}

	if len(desired) == 0 {
		return nil, nil
	}
	return desired, nil
}
