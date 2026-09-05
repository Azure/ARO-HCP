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
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

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
// ServiceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation
// in sync with Cluster data-plane operators and
// ServiceProviderCluster.Status.ManagedIdentityDetails.
//
// It does not call Azure. It only marks desired phases:
//   - Unique ResourceIDs from
//     CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators
//     whose ManagedIdentityDetails entry has resolved
//     MetadataFromARMUserAssignedIdentitiesAPI (ClientID, PrincipalID, and
//     TenantID) are added as PendingConfigure (or left Configured /
//     PendingConfigure if already present). Identities that only have
//     dataplane or hardcoded-identity metadata (control plane operators and the
//     ServiceManagedIdentity) are ignored, even when the same UAMI is still
//     used as CP or SMI.
//   - Identities present in ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation
//     but no longer among those data-plane operator identities are marked
//     PendingDeconfigure unless they are already Deconfigured. The first
//     transition stamps DeconfigureTimestamp to now (when deconfigure was
//     requested). The executor waits 24h from that time on a live cluster.
//     Cluster deletion still stamps the request time; the executor ignores
//     the wait when DeletionTimestamp is set.
//   - Data-plane operators whose ARM User Assigned Identities metadata is
//     temporarily unset are left as-is so a transient fetch error does not
//     deconfigure them.
type dataPlaneOIDCFederationIntentSyncer struct {
	clock                        utilsclock.PassiveClock
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
}

var _ controllerutils.ClusterSyncer = (*dataPlaneOIDCFederationIntentSyncer)(nil)

// NewDataPlaneOIDCFederationIntentController creates a cluster-watching
// controller that marks data-plane OIDC federation phases from
// ManagedIdentityDetails ARM User Assigned Identities metadata for identities
// listed as data-plane operators.
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

	dataPlaneOperators := existingCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators
	// Deconfigure only after Cluster Service is gone. PendingClusterServiceID is
	// a create-time reservation and is not cleared on delete, so it is not part
	// of this signal.
	if s.clusterServiceGone(existingCluster) {
		// Treat deletion as an empty desired identity set so in-flight federation
		// is marked PendingDeconfigure and the executor can remove Azure FICs.
		dataPlaneOperators = nil
	}

	desiredManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation, err := s.desiredDataPlaneOIDCFederationStatus(
		dataPlaneOperators,
		existingServiceProviderCluster.Status.ManagedIdentityDetails,
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
// Only data-plane operator identities are considered: unique ResourceIDs from
// DataPlaneOperators whose ManagedIdentityDetails entry has resolved
// MetadataFromARMUserAssignedIdentitiesAPI. Control-plane operators and the
// ServiceManagedIdentity are skipped even when they have ARM metadata, so a
// UAMI that is still CP or SMI does not keep data-plane OIDC federation after
// the last data-plane operator leaves it.
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
	dataPlaneOperators map[string]*azcorearm.ResourceID,
	existingManagedIdentityDetails map[string]*coreapi.ManagedIdentityMetadata,
	existingFederation map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus,
) (map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus, error) {
	desiredDataPlaneOperatorResourceIDs := uniqueDataPlaneOperatorResourceIDs(dataPlaneOperators)
	desired := make(map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus, len(desiredDataPlaneOperatorResourceIDs)+len(existingFederation))

	resolvedKeys := make(map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]struct{}, len(desiredDataPlaneOperatorResourceIDs))
	unresolvedResourceIDs := map[string]struct{}{}

	// First loop: data-plane operators that currently have resolved ARM User
	// Assigned Identities metadata in ManagedIdentityDetails. These are the
	// keys that should be federated. Missing keys become PendingConfigure. Keys
	// already PendingConfigure or Configured are left as-is. Keys already
	// PendingDeconfigure or Deconfigured are flipped back to PendingConfigure
	// because they are desired again.
	for resourceIDKey := range desiredDataPlaneOperatorResourceIDs {
		metadata, hasMetadata := existingManagedIdentityDetails[resourceIDKey]
		if !hasMetadata {
			// FetchManagedIdentitiesInfo has not written this identity yet.
			unresolvedResourceIDs[resourceIDKey] = struct{}{}
			continue
		}
		if metadata == nil {
			return nil, utils.TrackError(fmt.Errorf("ManagedIdentityDetails has a nil metadata entry for resource ID %s", resourceIDKey))
		}
		key, ok := s.dataplaneOIDCFederationKeyFromARMUserAssignedIdentities(metadata)
		if !ok {
			// ARM User Assigned Identities metadata is not resolved. That happens
			// when FetchManagedIdentitiesInfo has not written this identity yet,
			// ARM was not queried this pass (MetadataFromARMUserAssignedIdentitiesAPI
			// is nil), RetrievalError is set, or a successful Get returned empty
			// ClientID, PrincipalID, or TenantID. Do not deconfigure existing
			// entries for this ResourceID in the second loop.
			unresolvedResourceIDs[resourceIDKey] = struct{}{}
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
	// desired. Deconfigure only when the identity is gone from DataPlaneOperators,
	// or when ClientID/PrincipalID/TenantID changed (the first loop recorded the
	// new key; this loop sees the old key). ARM metadata on a control-plane or
	// SMI entry for the same ResourceID is not enough to keep federation. If the
	// identity is still a data-plane operator but those IDs are unset, keep the
	// existing entry. A transient Azure error in FetchManagedIdentitiesInfo
	// must not start deconfiguration.
	for key, existing := range existingFederation {
		if existing == nil {
			return nil, utils.TrackError(fmt.Errorf("ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation has a nil status for resource ID %s", key.ResourceID))
		}
		// If the key is still desired, keep the existing entry.
		if _, stillDesired := resolvedKeys[key]; stillDesired {
			continue
		}
		// The first loop could not build a current key for this ResourceID
		// because ARM User Assigned Identities metadata is unresolved. The
		// federation key includes ClientID, PrincipalID, and TenantID, so
		// the existing entry is absent from resolvedKeys even though the
		// identity is still a data-plane operator. Keep the existing status.
		// a transient fetch failure must not start deconfiguration.
		if _, unresolved := unresolvedResourceIDs[strings.ToLower(key.ResourceID)]; unresolved {
			desired[key] = existing.DeepCopy()
			continue
		}

		// This key is no longer desired: the identity left DataPlaneOperators,
		// or ClientID/PrincipalID/TenantID changed and the first loop already
		// recorded the new key. Start deconfiguration unless it is already in
		// progress or complete.
		next := existing.DeepCopy()
		switch existing.Phase {
		case coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure,
			coreapi.ManagedIdentityDataplaneOIDCFederationPhaseDeconfigured:
			// Leave as-is. Restamping DeconfigureTimestamp would reset the
			// live-cluster 24h wait. A Deconfigured entry is terminal.
		default:
			// PendingConfigure or Configured: first request to deconfigure.
			// Stamp DeconfigureTimestamp from the controller clock so the
			// executor can wait 24h on a live cluster.
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

// uniqueDataPlaneOperatorResourceIDs returns the unique data-plane operator
// identity ResourceIDs, keyed by the fully lowercased string form. Empty operator
// names and nil ResourceIDs are skipped.
func uniqueDataPlaneOperatorResourceIDs(dataPlaneOperators map[string]*azcorearm.ResourceID) map[string]*azcorearm.ResourceID {
	unique := make(map[string]*azcorearm.ResourceID, len(dataPlaneOperators))
	for operatorName, resourceID := range dataPlaneOperators {
		if len(operatorName) == 0 || resourceID == nil {
			continue
		}
		unique[strings.ToLower(resourceID.String())] = resourceID
	}
	return unique
}

// dataplaneOIDCFederationKeyFromARMUserAssignedIdentities returns the federation
// map key for metadata when ResourceID is set and
// MetadataFromARMUserAssignedIdentitiesAPI has ClientID, PrincipalID, and
// TenantID. ResourceID is lowercased because ARM resource IDs are
// case-insensitive. ok is false when ARM User Assigned Identities metadata is
// not fully resolved. Dataplane and hardcoded-identity metadata are ignored:
// data-plane OIDC federation uses the ARM User Assigned Identities IDs.
func (s *dataPlaneOIDCFederationIntentSyncer) dataplaneOIDCFederationKeyFromARMUserAssignedIdentities(metadata *coreapi.ManagedIdentityMetadata) (coreapi.ManagedIdentityDataplaneOIDCFederationKey, bool) {
	arm := metadata.MetadataFromARMUserAssignedIdentitiesAPI
	if arm == nil || !arm.HasResolvedIdentityInformation() {
		return coreapi.ManagedIdentityDataplaneOIDCFederationKey{}, false
	}
	return coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  strings.ToLower(metadata.ResourceID.String()),
		ClientID:    ptr.Deref(arm.ClientID, ""),
		PrincipalID: ptr.Deref(arm.PrincipalID, ""),
		TenantID:    ptr.Deref(arm.TenantID, ""),
	}, true
}
