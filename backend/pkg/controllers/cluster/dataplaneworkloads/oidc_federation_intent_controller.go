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
// in sync with Cluster data-plane operators and
// ServiceProviderCluster.Status.ManagedIdentityDetails.
//
// It does not call Azure. The map is keyed by identity ResourceID. Each value
// holds TargetIdentity and an Operators map keyed by operator name:
//   - Pairs from
//     CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators
//     whose ManagedIdentityDetails entry has resolved
//     MetadataFromARMUserAssignedIdentitiesAPI (ClientID, PrincipalID, and
//     TenantID) are added. TargetIdentity is copied from that ARM metadata.
//     A change to those IDs on the same ResourceID updates TargetIdentity and
//     clears every operator's EnsuredIdentity, AzureResources, and
//     PendingAzureResources; it does not deconfigure. Identities that only
//     have dataplane or hardcoded-identity metadata (control plane operators
//     and the ServiceManagedIdentity) are ignored, even when the same UAMI is
//     still used as CP or SMI.
//   - An operator present on an identity that is no longer assigned to that
//     identity gets DeconfigureTimestamp on that operator entry. Operators
//     with nothing tracked to delete are dropped. The identity key is dropped
//     when Operators is empty. The executor waits 24h from the operator stamp
//     on a live cluster. Cluster deletion still stamps the request time; the
//     executor ignores the wait when DeletionTimestamp is set. This stamp
//     still happens when another operator on the same identity has unresolved
//     ARM metadata.
//   - Data-plane operators whose ARM User Assigned Identities metadata is
//     temporarily unset are left as-is so a transient fetch error does not
//     deconfigure them. Neighbors on that identity that have left
//     DataPlaneOperators are still stamped or dropped.
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
// that are listed as data-plane operators in the Cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators
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
func (s *dataPlaneOIDCFederationIntentSyncer) clusterServiceGone(cluster *coreapi.HCPOpenShiftCluster) bool {
	// TODO temporary check to skip the new deletion approach for Clusters that were created before the new deletion approach was implemented.
	// This will be removed once all clusters whose deletion was triggered before the new approach is fully rolled out have been
	// fully deleted in all ARO-HCP permanent environments, for all regions.
	if !cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach {
		return false
	}

	return cluster.ServiceProviderProperties.DeletionTimestamp != nil &&
		cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil &&
		cluster.ServiceProviderProperties.ClusterServiceID == nil
}

// desiredDataPlaneOIDCFederationStatus computes the data plane oidc federation map that should
// be stored on the ServiceProviderCluster.
//
// Desired assignments are operator-to-identity pairs from DataPlaneOperators whose
// ManagedIdentityDetails entry has resolved MetadataFromARMUserAssignedIdentitiesAPI.
// The map is keyed by lowercased identity ResourceID. Operators is keyed by operator
// name. A change to ClientID, PrincipalID, or TenantID on the same ResourceID updates
// TargetIdentity and clears every operator's EnsuredIdentity and FIC lists. An operator
// that left this identity is stamped for deconfigure. Already deconfiguring operators
// keep their stamp so the wait is not reset.
//
// Three loops: (1) invert DataPlaneOperators into operators-by-identity, recording
// ResourceIDs whose ARM metadata is not resolved yet; (2) upsert identities that
// still have at least one resolved desired operator and stamp operators that left
// that identity; (3) existing Cosmos keys not produced by loop 2: when ARM is
// unresolved, keep still-assigned operators and stamp or drop ones that left;
// otherwise stamp or drop every operator. Loop 1 must finish before any stamp so
// an identity's desired operator set is complete.
func (s *dataPlaneOIDCFederationIntentSyncer) desiredDataPlaneOIDCFederationStatus(
	ctx context.Context,
	dataPlaneOperators map[string]*azcorearm.ResourceID,
	existingManagedIdentityDetails map[string]*coreapi.ManagedIdentityMetadata,
	existingManagedIdentityDataplaneOIDCFederationStatus map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus,
) (map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus, error) {
	logger := utils.LoggerFromContext(ctx)

	// desiredDataPlaneOperatorsByIdentity is every DataPlaneOperators assignment,
	// including identities whose ARM metadata is not resolved yet. Loop 2 skips
	// unresolved keys. Loop 3 uses the same map to stamp operators that have
	// left a still-assigned unresolved identity.
	desiredDataPlaneOperatorsByIdentity := map[string]map[string]struct{}{}
	resourceIDsWithUnresolvedIdentityMetadata := make(map[string]struct{})

	// Loop 1: invert DataPlaneOperators (operator -> identity). Record every
	// assignment, then skip unresolved ARM TargetIdentity so loop 2 does not
	// upsert those identities. Remember them so loop 3 does not treat "still
	// assigned, metadata missing" as fully unused. Do not stamp here: later
	// operators in this map may still be assigned to the same identity.
	for operatorName, resourceID := range dataPlaneOperators {
		if len(operatorName) == 0 {
			return nil, utils.TrackError(fmt.Errorf("data-plane operator %s has an empty name", operatorName))
		}
		if resourceID == nil {
			return nil, utils.TrackError(fmt.Errorf("data-plane operator %s has a nil resource ID", operatorName))
		}

		resourceIDKey := strings.ToLower(resourceID.String())
		if desiredDataPlaneOperatorsByIdentity[resourceIDKey] == nil {
			desiredDataPlaneOperatorsByIdentity[resourceIDKey] = map[string]struct{}{}
		}
		desiredDataPlaneOperatorsByIdentity[resourceIDKey][operatorName] = struct{}{}

		metadata, hasMetadata := existingManagedIdentityDetails[resourceIDKey]
		if !hasMetadata {
			// FetchManagedIdentitiesInfo controller has not written this identity yet.
			resourceIDsWithUnresolvedIdentityMetadata[resourceIDKey] = struct{}{}
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
			// Loop 3 keeps still-assigned operators on this ResourceID and stamps
			// operators that have left it.
			resourceIDsWithUnresolvedIdentityMetadata[resourceIDKey] = struct{}{}
			continue
		}
	}

	desiredManagedIdentityDataplaneOIDCFederationStatus := make(map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus)
	deconfigureRequestedAt := metav1.NewTime(s.clock.Now())

	// Loop 2: identities that still have at least one DataPlaneOperators assignment
	// with resolved ARM metadata. Write TargetIdentity, add missing operator rows,
	// clear DeconfigureTimestamp when an operator is desired again, then stamp or
	// drop operators on this identity that are no longer in that desired set.
	for resourceIDKey, desiredOperators := range desiredDataPlaneOperatorsByIdentity {
		if _, unresolved := resourceIDsWithUnresolvedIdentityMetadata[resourceIDKey]; unresolved {
			continue
		}
		metadata := existingManagedIdentityDetails[resourceIDKey]
		targetIdentity, ok := s.targetIdentityFromARMUserAssignedIdentities(metadata)
		if !ok {
			return nil, utils.TrackError(fmt.Errorf("ManagedIdentityDetails has unresolved ARM metadata for resource ID %s", resourceIDKey))
		}

		existing, hasExisting := existingManagedIdentityDataplaneOIDCFederationStatus[resourceIDKey]
		if hasExisting && existing == nil {
			return nil, utils.TrackError(fmt.Errorf("ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation has a nil status for resource ID %s", resourceIDKey))
		}

		var next *coreapi.ManagedIdentityDataplaneOIDCFederationStatus
		if !hasExisting {
			// If the identity is not yet tracked for data plane oidc federation configuration/deconfiguration, set it with the TargetIdentity
			next = &coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				TargetIdentity: targetIdentity,
				Operators:      map[string]*coreapi.DataplaneOIDCFederationOperatorStatus{},
			}
		} else {
			next = existing.DeepCopy()
			// To determine if the identity has changed, we can compare the TargetIdentity of the existing entry with the new TargetIdentity using the
			// != operator because it will compare the values of the fields of the TargetIdentity struct, which are all string values.
			identityChanged := existing.TargetIdentity != targetIdentity
			next.TargetIdentity = targetIdentity

			if next.Operators == nil {
				next.Operators = map[string]*coreapi.DataplaneOIDCFederationOperatorStatus{}
			}
			if identityChanged {
				logger.Info("data-plane OIDC federation identity instance changed. Clearing ensured identity and FIC lists",
					"managedIdentityResourceID", resourceIDKey,
					"previousTargetClientID", existing.TargetIdentity.ClientID,
					"previousTargetPrincipalID", existing.TargetIdentity.PrincipalID,
					"previousTargetTenantID", existing.TargetIdentity.TenantID,
					"targetClientID", targetIdentity.ClientID,
					"targetPrincipalID", targetIdentity.PrincipalID,
					"targetTenantID", targetIdentity.TenantID,
				)
				// ClientID/PrincipalID/TenantID changed on this ResourceID (identity
				// recreated). Keep operator rows; drop ensured snapshot and FIC lists
				// so the executor federates the new instance.
				for _, operatorStatus := range next.Operators {
					operatorStatus.EnsuredIdentity = nil
					operatorStatus.AzureResources = nil
					operatorStatus.PendingAzureResources = nil
					operatorStatus.DeconfigureTimestamp = nil
				}
			}
		}

		// Ensure a row for every still-desired operator on this identity. A
		// returning operator must not keep a drain stamp from a previous
		// assignment to this identity.
		for operatorName := range desiredOperators {
			if next.Operators[operatorName] == nil {
				next.Operators[operatorName] = &coreapi.DataplaneOIDCFederationOperatorStatus{}
				continue
			}
			next.Operators[operatorName].DeconfigureTimestamp = nil
		}

		s.stampOrDropUndesiredOperators(next, desiredOperators, deconfigureRequestedAt)
		if len(next.Operators) == 0 {
			continue
		}
		desiredManagedIdentityDataplaneOIDCFederationStatus[resourceIDKey] = next
	}

	// Loop 3: existing Cosmos identity keys that loop 2 did not emit. Those are
	// either still referenced by DataPlaneOperators with unresolved ARM metadata
	// (keep still-assigned operators so a transient fetch does not deconfigure
	// them; stamp or drop operators that have left) or no longer referenced at
	// all (stamp or drop every operator; drop the identity when Operators is
	// empty).
	for resourceIDKey, existing := range existingManagedIdentityDataplaneOIDCFederationStatus {
		if existing == nil {
			return nil, utils.TrackError(fmt.Errorf("ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation has a nil status for resource ID %s", resourceIDKey))
		}
		if _, alreadyDesired := desiredManagedIdentityDataplaneOIDCFederationStatus[resourceIDKey]; alreadyDesired {
			continue
		}
		// Still assigned on this UAMI, but ARM TargetIdentity is not ready, so
		// loop 2 skipped the identity. Do not update TargetIdentity or add
		// operator rows. Keep operators that DataPlaneOperators still maps
		// here so a transient fetch does not deconfigure them.
		//
		// Stamp or drop operators that have left this identity. Desiredness
		// comes from DataPlaneOperators, not from ClientID/PrincipalID/TenantID.
		// The status row already has the FIC IDs to delete. Copying the identity
		// unchanged would leave those operators unstamped until ARM resolves.
		// The executor would neither ensure them (they are not assigned) nor
		// deconfigure them (no DeconfigureTimestamp). Their FICs would stay on
		// this UAMI, and would never drain if ARM never comes back. Drop the
		// identity when that leaves Operators empty.
		// TODO is this the approach we want to take or do we prefer to wait
		// until the metadata is resolved?
		if _, unresolved := resourceIDsWithUnresolvedIdentityMetadata[resourceIDKey]; unresolved {
			next := existing.DeepCopy()
			s.stampOrDropUndesiredOperators(next, desiredDataPlaneOperatorsByIdentity[resourceIDKey], deconfigureRequestedAt)
			if len(next.Operators) == 0 {
				continue
			}
			desiredManagedIdentityDataplaneOIDCFederationStatus[resourceIDKey] = next
			continue
		}

		next := existing.DeepCopy()
		s.stampOrDropAllOperatorsForIdentity(next, deconfigureRequestedAt)
		if len(next.Operators) == 0 {
			continue
		}
		desiredManagedIdentityDataplaneOIDCFederationStatus[resourceIDKey] = next
	}

	if len(desiredManagedIdentityDataplaneOIDCFederationStatus) == 0 {
		return nil, nil
	}

	return desiredManagedIdentityDataplaneOIDCFederationStatus, nil
}

// stampOrDropAllOperatorsForIdentity stamps DeconfigureTimestamp on every operator that still
// has tracked FICs, and drops operators with nothing to delete. Used when this
// identity has no remaining desired data-plane operators (loop 3). Already
// draining operators keep their stamp so the 24h wait is not reset.
func (s *dataPlaneOIDCFederationIntentSyncer) stampOrDropAllOperatorsForIdentity(
	status *coreapi.ManagedIdentityDataplaneOIDCFederationStatus,
	deconfigureRequestedAt metav1.Time,
) {
	for operatorName, operatorStatus := range status.Operators {
		if operatorStatus.DeconfigureTimestamp != nil {
			continue
		}
		if len(operatorStatus.AzureResources) == 0 && len(operatorStatus.PendingAzureResources) == 0 {
			delete(status.Operators, operatorName)
			continue
		}
		stamp := deconfigureRequestedAt
		operatorStatus.DeconfigureTimestamp = &stamp
	}

	if len(status.Operators) == 0 {
		status.Operators = nil
	}
}

// stampOrDropUndesiredOperators stamps DeconfigureTimestamp on operators that
// are not in desiredOperators and still have tracked FICs. Operators with
// nothing to delete are dropped. Desired operators are left as-is. Already
// draining operators keep their stamp so the 24h wait is not reset.
func (s *dataPlaneOIDCFederationIntentSyncer) stampOrDropUndesiredOperators(
	status *coreapi.ManagedIdentityDataplaneOIDCFederationStatus,
	desiredOperators map[string]struct{},
	deconfigureRequestedAt metav1.Time,
) {
	for operatorName, operatorStatus := range status.Operators {
		if _, desired := desiredOperators[operatorName]; desired {
			continue
		}
		if operatorStatus.DeconfigureTimestamp != nil {
			continue
		}
		if len(operatorStatus.AzureResources) == 0 && len(operatorStatus.PendingAzureResources) == 0 {
			delete(status.Operators, operatorName)
			continue
		}
		stamp := deconfigureRequestedAt
		operatorStatus.DeconfigureTimestamp = &stamp
	}

	if len(status.Operators) == 0 {
		status.Operators = nil
	}
}

// targetIdentityFromARMUserAssignedIdentities returns the TargetIdentity
// snapshot when MetadataFromARMUserAssignedIdentitiesAPI has ClientID,
// PrincipalID, and TenantID. ok is false when ARM User Assigned Identities
// metadata is not fully resolved.
func (s *dataPlaneOIDCFederationIntentSyncer) targetIdentityFromARMUserAssignedIdentities(metadata *coreapi.ManagedIdentityMetadata) (coreapi.DataplaneOIDCFederationIdentityInstance, bool) {
	metadataFromARMUserAssignedIdentitiesAPI := metadata.MetadataFromARMUserAssignedIdentitiesAPI
	if metadataFromARMUserAssignedIdentitiesAPI == nil || !metadataFromARMUserAssignedIdentitiesAPI.HasResolvedIdentityInformation() {
		return coreapi.DataplaneOIDCFederationIdentityInstance{}, false
	}
	return coreapi.DataplaneOIDCFederationIdentityInstance{
		ClientID:    *metadataFromARMUserAssignedIdentitiesAPI.ClientID,
		PrincipalID: *metadataFromARMUserAssignedIdentitiesAPI.PrincipalID,
		TenantID:    *metadataFromARMUserAssignedIdentitiesAPI.TenantID,
	}, true
}
