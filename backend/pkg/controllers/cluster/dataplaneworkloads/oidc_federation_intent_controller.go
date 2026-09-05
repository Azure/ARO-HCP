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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
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
// in sync with the desired Cluster data-plane operator identities and
// ServiceProviderCluster.Status.ManagedIdentityDetails.
//
// It does not call Azure. The map is keyed by (identity ResourceID, operator
// name):
//   - Desired assignments are DataPlaneOperators pairs whose ManagedIdentityDetails
//     entry has resolved MetadataFromARMUserAssignedIdentitiesAPI (ClientID,
//     PrincipalID, and TenantID). TargetIdentity is copied from that ARM metadata
//     onto every assignment that shares that ResourceID. When an assignment's
//     TargetIdentity does not match those ARM IDs, EnsuredIdentity, AzureResources,
//     and PendingAzureResources are cleared on that assignment, including a
//     draining neighbor. Assignments whose TargetIdentity already matches are left
//     as-is. That identity-instance change does not deconfigure. Identities that only have managed identities dataplane or
//     hardcoded-identity metadata (control-plane operators and the
//     ServiceManagedIdentity) are ignored, even when the same User-Assigned
//     Managed Identity is still used as a control-plane or service managed
//     identity.
//   - An existing assignment is unmapped when its operator is no longer mapped to
//     that identity in DataPlaneOperators (removed, or moved to another identity).
//     If AzureResources or PendingAzureResources still list Azure FederatedIdentityCredential resources, DeconfigureTimestamp
//     is set so DataPlaneOIDCFederation can delete them. If both lists are empty,
//     the assignment is dropped. On an existing cluster the executor waits 24 hours from
//     the stamp. This is still the case even when the Cluster is marked as deletion. In that case the executor
//     skips the wait when the Cluster'sDeletionTimestamp is set. Unmapped operators are stamped
//     or dropped even when the same identity, still mapped from another
//     operator, has unresolved ARM metadata.
//   - Data-plane operators whose ARM User Assigned Identities metadata is
//     temporarily unset are left as-is so a transient fetch error does not
//     deconfigure them. Neighbors on that identity that have left DataPlaneOperators
//     are still stamped or dropped.
type dataPlaneOIDCFederationIntentSyncer struct {
	clock                        utilsclock.PassiveClock
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
}

var _ controllerutils.ClusterSyncer = (*dataPlaneOIDCFederationIntentSyncer)(nil)

// NewDataPlaneOIDCFederationIntentController creates a cluster-watching
// controller that marks for data-plane OIDC federation entries from the
// ServiceProviderCluster.Status.ManagedIdentityDetails map that have
// metadata resolved from the MetadataFromARMUserAssignedIdentitiesAPI source and
// that are listed as desired data-plane operators in the Cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators
// map. It also marks for deconfiguration entries that have left the data-plane operators set and are not desired anymore.
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
	logger := utils.LoggerFromContext(ctx)

	existingCluster, err := s.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster from cache: %w", err))
	}

	existingServiceProviderCluster, err := s.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		// The ServiceProviderCluster has not been created yet. We will pick it up on a
		// later requeue once it exists.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	desiredDataplaneOperators := existingCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators
	// When Cluster Service's Cluster is gone during the cluster deletion process, we mark all data-plane operator identities for deconfiguration.
	if s.clusterServiceGone(existingCluster) {
		// Treat deletion as an empty desired identity set so in-flight federation
		// is marked for deconfigure and the DataPlaneOIDCFederation controller can deconfigure the data-plane operator identities.
		desiredDataplaneOperators = nil
	}

	desiredManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation, err := s.desiredDataPlaneOIDCFederationStatus(
		ctx,
		desiredDataplaneOperators,
		existingServiceProviderCluster.Status.ManagedIdentityDetails,
		existingServiceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation,
	)
	if err != nil {
		return err
	}

	replacement := existingServiceProviderCluster.DeepCopy()
	replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = desiredManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation
	if !controllerutil.NeedsUpdate(existingServiceProviderCluster, replacement) {
		return nil
	}

	logger.Info("Updating serviceProviderCluster managed identities with data plane workloads oidc federation map",
		"managedIdentitiesWithDataPlaneWorkloadsOIDCFederation", replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation,
	)
	_, err = s.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}

	return nil
}

// clusterServiceGone reports whether Cluster Service's Cluster is no longer there as
// part of the deletion process of this cluster.

// UsesNewClusterDeletionApproach being set to true is required because the old deletion approach
// never stamped ClusterServiceDeletionTimestamp and there might be pre-existing in-flifght deletes with the
// old approach. That means that we never deconfigure clusters that have been marked for deletion with the old approach. Although in those
// cases the cluster service side should perform the deconfiguration on its side.
func (s *dataPlaneOIDCFederationIntentSyncer) clusterServiceGone(cluster *coreapi.Cluster) bool {
	return cluster.ServiceProviderProperties.DeletionTimestamp != nil &&
		cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil &&
		cluster.ServiceProviderProperties.ClusterServiceID == nil
}

// desiredDataPlaneOIDCFederationStatus computes the data plane oidc federation map that should
// be stored on the ServiceProviderCluster.
//
// Desired assignments are operator-to-identity pairs from DataPlaneOperators whose
// ManagedIdentityDetails entry has resolved MetadataFromARMUserAssignedIdentitiesAPI.
// The map is keyed by (lowercased identity ResourceID, operator name). When an
// assignment's TargetIdentity does not match ARM ClientID, PrincipalID, or TenantID
// for that ResourceID, EnsuredIdentity and FIC lists are cleared on that
// assignment. An operator whose DataPlaneOperators entry no longer points at
// that identity (entry removed, or pointing at a different identity) is stamped
// for deconfigure.
// Already deconfiguring assignments keep their stamp so the wait is not reset.
//
// Invert DataPlaneOperators must finish before leftover diffs Cosmos against
// that inverted set. Desired upsert emits still-desired assignments whose ARM
// metadata is resolved. Leftover walks Cosmos keys upsert did not emit: keep
// still-assigned operators when ARM is unresolved, otherwise stamp-or-omit
// leavers. Leftover applies the same identity-instance clear as upsert before
// stamp-or-omit whenever ManagedIdentityDetails has resolved ARM metadata for
// that identity, whether or not another operator still references it: an
// identity can be recreated with the same ResourceID after its last data-plane
// operator left while still being tracked for another reason (control-plane
// operator, ServiceManagedIdentity), and its stale FIC tracking must still be
// cleared before the 24h deconfigure wait, or deconfigure would later delete
// against the new instance using ResourceIDs tracked for the old one.
func (s *dataPlaneOIDCFederationIntentSyncer) desiredDataPlaneOIDCFederationStatus(
	ctx context.Context,
	dataPlaneOperators map[string]*azcorearm.ResourceID,
	existingManagedIdentityDetails map[string]*coreapi.ManagedIdentityMetadata,
	existingManagedIdentityDataplaneOIDCFederationStatus map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus,
) (map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus, error) {
	// desiredDataPlaneOperatorsByIdentityResourceID is the set of identity ResourceIDs that have at
	// least one desired operator in DataPlaneOperators. For each entry, it contains the set of data plane operators
	// that are referencing that identity. Leftover uses it to classify each Cosmos
	// key as still-desired (keep as-is, including when ARM is unresolved) or a
	// leaver (stamp or omit).
	// The key of the map is the identity resource id. The value of the map is
	// a map whose key is the operator name.
	desiredDataPlaneOperatorsByIdentityResourceID := map[string]map[string]struct{}{}
	// desiredResourceIDsWithUnresolvedIdentityMetadata is a set of identity ResourceIDs
	// whose ARM metadata is currently not resolved.
	desiredResourceIDsWithUnresolvedIdentityMetadata := make(map[string]struct{})

	// Invert DataPlaneOperators (operator -> identity). Record every
	// assignment, then skip unresolved ARM TargetIdentity so desired upsert
	// does not add those assignments. Remember them so leftover merge does
	// not treat "still assigned, metadata missing" as fully unused.
	for operatorName, resourceID := range dataPlaneOperators {
		if len(operatorName) == 0 {
			return nil, utils.TrackError(fmt.Errorf("data-plane operator %s has an empty name", operatorName))
		}
		if resourceID == nil {
			return nil, utils.TrackError(fmt.Errorf("data-plane operator %s has a nil resource ID", operatorName))
		}

		resourceIDKey := strings.ToLower(resourceID.String())
		if desiredDataPlaneOperatorsByIdentityResourceID[resourceIDKey] == nil {
			desiredDataPlaneOperatorsByIdentityResourceID[resourceIDKey] = map[string]struct{}{}
		}
		desiredDataPlaneOperatorsByIdentityResourceID[resourceIDKey][operatorName] = struct{}{}

		metadata, hasMetadata := existingManagedIdentityDetails[resourceIDKey]
		if !hasMetadata {
			// FetchManagedIdentitiesInfo controller has not written this identity yet.
			desiredResourceIDsWithUnresolvedIdentityMetadata[resourceIDKey] = struct{}{}
			continue
		}
		if metadata == nil {
			return nil, utils.TrackError(fmt.Errorf("ManagedIdentityDetails has a nil metadata entry for resource ID %s", resourceIDKey))
		}
		_, ok := s.targetIdentityFromARMUserAssignedIdentities(metadata)
		if !ok {
			// ARM User Assigned Identities metadata is not resolved. That happens
			// when the FetchManagedIdentitiesInfo controller has not written this identity yet, when the metadata source
			// has not been queried yet, when RetrievalError is set, or when the identity does not exist anymore in Azure.
			// Leftover merge keeps still-assigned operators on this ResourceID and stamps
			// operators that have left it.
			desiredResourceIDsWithUnresolvedIdentityMetadata[resourceIDKey] = struct{}{}
			continue
		}
	}

	desiredManagedIdentityDataplaneOIDCFederationStatus := make(map[coreapi.DataplaneOIDCFederationAssignmentKey]*coreapi.DataplaneOIDCFederationAssignmentStatus)
	deconfigureRequestedAt := metav1.NewTime(s.clock.Now())

	// Desired upsert: still-desired operators whose identity has resolved ARM
	// metadata. Copy the Cosmos assignment so the cache is not mutated, or
	// create one when the assignment is new. Write TargetIdentity, clear FIC
	// tracking when TargetIdentity does not match ARM, and clear
	// DeconfigureTimestamp when the operator is desired again.
	for resourceIDKey, desiredOperators := range desiredDataPlaneOperatorsByIdentityResourceID {
		if _, unresolved := desiredResourceIDsWithUnresolvedIdentityMetadata[resourceIDKey]; unresolved {
			continue
		}
		metadata := existingManagedIdentityDetails[resourceIDKey]
		targetIdentity, ok := s.targetIdentityFromARMUserAssignedIdentities(metadata)
		if !ok {
			return nil, utils.TrackError(fmt.Errorf("ManagedIdentityDetails has unresolved ARM metadata for resource ID %s", resourceIDKey))
		}

		for operatorName := range desiredOperators {
			key := coreapi.DataplaneOIDCFederationAssignmentKey{
				IdentityResourceID: resourceIDKey,
				OperatorName:       operatorName,
			}
			existing, hasExisting := existingManagedIdentityDataplaneOIDCFederationStatus[key]
			if hasExisting && existing == nil {
				return nil, utils.TrackError(fmt.Errorf("ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation has a nil status for assignment %s", key.String()))
			}
			var desiredOIDCFederationAssignmentStatus *coreapi.DataplaneOIDCFederationAssignmentStatus
			if !hasExisting {
				desiredOIDCFederationAssignmentStatus = &coreapi.DataplaneOIDCFederationAssignmentStatus{}
			} else {
				desiredOIDCFederationAssignmentStatus = existing.DeepCopy()
				s.clearAssignmentFICTrackingIfTargetIdentityChanged(ctx, key, desiredOIDCFederationAssignmentStatus, targetIdentity)
			}
			desiredOIDCFederationAssignmentStatus.TargetIdentity = targetIdentity
			desiredOIDCFederationAssignmentStatus.DeconfigureTimestamp = nil
			desiredManagedIdentityDataplaneOIDCFederationStatus[key] = desiredOIDCFederationAssignmentStatus
		}
	}

	// Leftover: Cosmos keys upsert did not emit. Keep a still-assigned operator
	// when ARM is unresolved so a transient fetch does not deconfigure it.
	// A leaver is a Cosmos assignment (identity, operator) whose
	// DataPlaneOperators entry no longer points at that identity: the operator
	// key was removed, or it now points at a different identity. Stamp DeconfigureTimestamp when FICs remain,
	// or omit the key when both lists are empty. Whenever this identity has
	// resolved ARM metadata, whether or not another operator still references
	// it, clear FIC tracking first if TargetIdentity does not match that ARM
	// snapshot (identity instance changed).
	// Leavers are stamped immediately, without waiting for ARM metadata to resolve:
	// unresolved can mean the identity was deleted and will never resolve, so
	// gating the stamp on resolution would leak this Cosmos entry forever. If ARM
	// later reveals the identity was recreated instead, clearAssignmentFICTracking-
	// IfTargetIdentityChanged cancels the premature stamp and this entry is dropped.
	for key, existing := range existingManagedIdentityDataplaneOIDCFederationStatus {
		if existing == nil {
			return nil, utils.TrackError(fmt.Errorf("ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation has a nil status for assignment %s", key.String()))
		}
		if _, alreadyEmitted := desiredManagedIdentityDataplaneOIDCFederationStatus[key]; alreadyEmitted {
			continue
		}

		desiredOIDCFederationAssignmentStatus := existing.DeepCopy()

		_, identityAndOperatorAssignmentStillDesired := desiredDataPlaneOperatorsByIdentityResourceID[key.IdentityResourceID][key.OperatorName]

		if identityAndOperatorAssignmentStillDesired {
			// The operator is desired again: cancel any in-flight deconfiguration,
			// otherwise a DeconfigureTimestamp stamped while this operator was a
			// leaver would keep ticking and the executor would delete FICs for an
			// assignment that is desired again.
			desiredOIDCFederationAssignmentStatus.DeconfigureTimestamp = nil
		}

		// identityAndOperatorAssignmentStillDesired == true implies identityMetadataResolved == false
		// here because the desired upsert loop already processed and emitted any resolved, still-desired
		// identity and operator assignment before this loop runs.
		if identityAndOperatorAssignmentStillDesired {
			// Still assigned, ARM not ready. Copy Cosmos as-is.
			desiredManagedIdentityDataplaneOIDCFederationStatus[key] = desiredOIDCFederationAssignmentStatus
			continue
		}

		// Leaver: Cosmos still has this (identity, operator) pair, but
		// DataPlaneOperators no longer maps that operator to that identity.
		// Still-desired plus resolved ARM already continued (upsert).
		// Still-desired plus unresolved ARM already continued (keep).
		// What remains (this operator is not desired on this identity):
		//   - the identity has resolved ARM metadata, whether another operator
		//     still maps to it or none does (last operator left, or cluster
		//     deletion emptied the set): clear FIC tracking if the instance
		//     changed, then stamp or omit (after a clear, omit because lists
		//     are empty). A recreated identity can still carry resolved ARM
		//     metadata here even with no current operator, e.g. when it is
		//     also a control-plane operator or the ServiceManagedIdentity, so
		//     this is not limited to "another operator still uses it".
		//   - the identity has no ManagedIdentityDetails entry, or its ARM
		//     metadata is unresolved: stamp or omit with Cosmos as-is, no
		//     identity-instance clear (nothing to compare TargetIdentity
		//     against).
		// targetIdentity is empty when the identity metadata is unresolved.
		var targetIdentity coreapi.DataplaneOIDCFederationIdentityInstance
		identityMetadataResolved := false
		if metadata, hasMetadata := existingManagedIdentityDetails[key.IdentityResourceID]; hasMetadata {
			targetIdentity, identityMetadataResolved = s.targetIdentityFromARMUserAssignedIdentities(metadata)
		}
		if identityMetadataResolved {
			s.clearAssignmentFICTrackingIfTargetIdentityChanged(ctx, key, desiredOIDCFederationAssignmentStatus, targetIdentity)
		}
		if kept := s.stampOrDropAssignment(desiredOIDCFederationAssignmentStatus, deconfigureRequestedAt); kept != nil {
			desiredManagedIdentityDataplaneOIDCFederationStatus[key] = kept
		}
	}

	if len(desiredManagedIdentityDataplaneOIDCFederationStatus) == 0 {
		return nil, nil
	}

	return desiredManagedIdentityDataplaneOIDCFederationStatus, nil
}

// clearAssignmentFICTrackingIfTargetIdentityChanged clears EnsuredIdentity and
// FIC lists on next when TargetIdentity does not match targetIdentity. Azure
// already deleted child FICs with the old instance. Do not deconfigure: a
// delayed Delete would target the new instance and the same deterministic FIC
// names.
func (s *dataPlaneOIDCFederationIntentSyncer) clearAssignmentFICTrackingIfTargetIdentityChanged(
	ctx context.Context,
	key coreapi.DataplaneOIDCFederationAssignmentKey,
	desiredOIDCFederationAssignmentStatus *coreapi.DataplaneOIDCFederationAssignmentStatus,
	targetIdentity coreapi.DataplaneOIDCFederationIdentityInstance,
) {
	logger := utils.LoggerFromContext(ctx)
	if desiredOIDCFederationAssignmentStatus.TargetIdentity == targetIdentity {
		return
	}
	logger.Info("data-plane OIDC federation identity instance changed. Clearing ensured identity and FIC lists",
		"managedIdentityResourceID", key.IdentityResourceID,
		"operatorName", key.OperatorName,
		"previousTargetClientID", desiredOIDCFederationAssignmentStatus.TargetIdentity.ClientID,
		"previousTargetPrincipalID", desiredOIDCFederationAssignmentStatus.TargetIdentity.PrincipalID,
		"previousTargetTenantID", desiredOIDCFederationAssignmentStatus.TargetIdentity.TenantID,
		"targetClientID", targetIdentity.ClientID,
		"targetPrincipalID", targetIdentity.PrincipalID,
		"targetTenantID", targetIdentity.TenantID,
	)
	desiredOIDCFederationAssignmentStatus.EnsuredIdentity = nil
	desiredOIDCFederationAssignmentStatus.AzureResources = nil
	desiredOIDCFederationAssignmentStatus.PendingAzureResources = nil
	desiredOIDCFederationAssignmentStatus.DeconfigureTimestamp = nil
}

// stampOrDropAssignment stamps DeconfigureTimestamp on an assignment that still
// has tracked FICs, and drops an assignment with nothing to delete. Already
// draining assignments keep their stamp so the 24h wait is not reset.
func (s *dataPlaneOIDCFederationIntentSyncer) stampOrDropAssignment(
	status *coreapi.DataplaneOIDCFederationAssignmentStatus,
	deconfigureRequestedAt metav1.Time,
) *coreapi.DataplaneOIDCFederationAssignmentStatus {
	if status.DeconfigureTimestamp != nil {
		return status
	}
	if len(status.AzureResources) == 0 && len(status.PendingAzureResources) == 0 {
		return nil
	}
	stamp := deconfigureRequestedAt
	status.DeconfigureTimestamp = &stamp
	return status
}

// targetIdentityFromARMUserAssignedIdentities returns the TargetIdentity
// snapshot when MetadataFromARMUserAssignedIdentitiesAPI has ClientID,
// PrincipalID, and TenantID. ok is false when ARM User Assigned Identities
// metadata is not fully resolved.
func (s *dataPlaneOIDCFederationIntentSyncer) targetIdentityFromARMUserAssignedIdentities(metadata *coreapi.ManagedIdentityMetadata) (coreapi.DataplaneOIDCFederationIdentityInstance, bool) {
	metadataFromARMUserAssignedIdentitiesAPI := metadata.MetadataFromARMUserAssignedIdentitiesAPI
	if metadataFromARMUserAssignedIdentitiesAPI == nil || !coreapihelpers.IdentityMetadataValueHasResolvedIdentityInformation(metadataFromARMUserAssignedIdentitiesAPI) {
		return coreapi.DataplaneOIDCFederationIdentityInstance{}, false
	}
	return coreapi.DataplaneOIDCFederationIdentityInstance{
		ClientID:    *metadataFromARMUserAssignedIdentitiesAPI.ClientID,
		PrincipalID: *metadataFromARMUserAssignedIdentitiesAPI.PrincipalID,
		TenantID:    *metadataFromARMUserAssignedIdentitiesAPI.TenantID,
	}, true
}
