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

package denyassignments

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

// ClusterDenyAssignmentIntentControllerName is the single source of truth for
// this controller's name. It is used for the workqueue name (a Prometheus
// label), context/logger controller name, and log fields.
const ClusterDenyAssignmentIntentControllerName = "ClusterDenyAssignmentIntent"

// clusterDenyAssignmentIntentSyncer keeps
// ServiceProviderCluster.Status.DenyAssignmentsV2 in sync with the deny
// assignment types required for the cluster.
//
// It does not call Azure. Nested ExcludedIdentities is observed Azure state,
// so this controller does not insert identity rows or write ObservedIdentity.
//   - Types from denyAssignmentDefinitions whose excluded identities have
//     resolved principal IDs are added as PendingConfigure (or left Configured
//     / PendingConfigure if already present and the observed identity set
//     matches). Types whose identities are not yet resolved are not added.
//   - A Configured type whose live desired principals are missing from
//     ExcludedIdentities, whose ObservedIdentity does not match, or whose
//     ObservedPermissions do not match the live definition (including a
//     missing snapshot), is set back to PendingConfigure. A ClientID or
//     TenantID change on a still-desired key is identity drift; it does not
//     deconfigure.
//   - Identities that left the type, or whose PrincipalID changed, keep their
//     observed row. The first transition stamps DeconfigureTimestamp. If the
//     principal is desired again before the wait ends, the timestamp is cleared.
//   - Types present in DenyAssignmentsV2 but no longer in the definition set
//     are marked PendingDeconfigure unless they are already Deconfigured.
//   - Types that are still required but whose identities are temporarily
//     unresolved are left as-is so a transient fetch error does not
//     deconfigure them, except Configured types that already need an ensure
//     for the resolved subset.
//   - Cluster deletion is a no-op. Deny assignments are scoped to the managed
//     resource group, so Azure deletes them in cascade when that resource
//     group is removed.
type clusterDenyAssignmentIntentSyncer struct {
	clock                        utilsclock.PassiveClock
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
}

var _ controllerutils.ClusterSyncer = (*clusterDenyAssignmentIntentSyncer)(nil)

// NewClusterDenyAssignmentIntentController creates a cluster-watching
// controller that marks deny assignment phases from the required definition
// set once excluded identities have resolved principal IDs.
func NewClusterDenyAssignmentIntentController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	backendInformers coreinformers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &clusterDenyAssignmentIntentSyncer{
		clock:                        clock,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		resourcesDBClient:            resourcesDBClient,
	}

	return controllerutils.NewClusterWatchingController(
		ClusterDenyAssignmentIntentControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		1*time.Minute,
		syncer,
	)
}

func (s *clusterDenyAssignmentIntentSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := s.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster from cache: %w", err))
	}

	// Deny assignments are scoped to the managed resource group, so Azure
	// deletes them in cascade when that resource group is removed during
	// cluster teardown. There is no need to mark PendingDeconfigure here.
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
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

	desiredDenyAssignmentsV2, err := s.desiredDenyAssignmentsV2(
		existingCluster,
		existingServiceProviderCluster,
		existingServiceProviderCluster.Status.DenyAssignmentsV2,
	)
	if err != nil {
		return err
	}

	replacement := existingServiceProviderCluster.DeepCopy()
	replacement.Status.DenyAssignmentsV2 = desiredDenyAssignmentsV2

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

// desiredDenyAssignmentsV2 computes the deny assignment map that should be
// stored on the ServiceProviderCluster.
//
// Required types are the deny assignment definitions for the cluster. A type
// is added as PendingConfigure when its excluded identities have resolved
// principal IDs. Missing types whose identities are not ready are not added.
// Types that have left the definition set are marked PendingDeconfigure.
// Nested ExcludedIdentities rows are not created here; only DeconfigureTimestamp
// is stamped or cleared on rows ClusterDenyAssignmentV2 already wrote.
func (s *clusterDenyAssignmentIntentSyncer) desiredDenyAssignmentsV2(
	cluster *coreapi.HCPOpenShiftCluster,
	serviceProviderCluster *coreapi.ServiceProviderCluster,
	existingDenyAssignments map[string]*coreapi.DenyAssignmentStatus,
) (map[string]*coreapi.DenyAssignmentStatus, error) {
	definitionsByType := denyAssignmentDefinitionsByType(cluster)
	requiredTypes := requiredDenyAssignmentTypes(cluster)

	desired := make(map[string]*coreapi.DenyAssignmentStatus, len(requiredTypes)+len(existingDenyAssignments))
	stillRequired := make(map[string]struct{}, len(requiredTypes))

	// First loop: types that are currently required. Add PendingConfigure when
	// identities are resolved. Types already PendingConfigure are left as-is at
	// the type level. Configured types stay Configured unless live desired
	// principals are missing from ExcludedIdentities, ObservedIdentity drifted,
	// or ObservedPermissions do not match the live definition.
	// Types already PendingDeconfigure or Deconfigured are flipped back to
	// PendingConfigure because they are desired again. If identities are not
	// ready, existing required types are kept so a transient fetch error does
	// not deconfigure them.
	for denyAssignmentType := range requiredTypes {
		existing, hasExisting := existingDenyAssignments[denyAssignmentType]
		if hasExisting && existing == nil {
			return nil, utils.TrackError(fmt.Errorf("DenyAssignmentsV2 has a nil status for type %s", denyAssignmentType))
		}

		definition := definitionsByType[denyAssignmentType]
		if definition == nil {
			return nil, utils.TrackError(fmt.Errorf("no definition for deny assignment type %s", denyAssignmentType))
		}
		desiredIdentities, unresolvedResourceIDs, err := desiredExcludedIdentities(cluster, serviceProviderCluster, definition)
		if err != nil {
			if hasExisting {
				stillRequired[denyAssignmentType] = struct{}{}
				desired[denyAssignmentType] = existing.DeepCopy()
			}
			continue
		}
		identitiesReady := len(unresolvedResourceIDs) == 0

		if !identitiesReady {
			if hasExisting {
				stillRequired[denyAssignmentType] = struct{}{}
				next := existing.DeepCopy()
				merged, needEnsure, mergeErr := s.mergeExcludedIdentities(existing.ExcludedIdentities, desiredIdentities, unresolvedResourceIDs)
				if mergeErr != nil {
					return nil, mergeErr
				}
				next.ExcludedIdentities = merged
				if needEnsure && existing.Phase == coreapi.DenyAssignmentPhaseConfigured {
					next.Phase = coreapi.DenyAssignmentPhasePendingConfigure
				}
				desired[denyAssignmentType] = next
			}
			continue
		}

		stillRequired[denyAssignmentType] = struct{}{}
		if !hasExisting {
			desired[denyAssignmentType] = &coreapi.DenyAssignmentStatus{
				Phase: coreapi.DenyAssignmentPhasePendingConfigure,
			}
			continue
		}

		next := existing.DeepCopy()
		merged, needEnsure, mergeErr := s.mergeExcludedIdentities(existing.ExcludedIdentities, desiredIdentities, unresolvedResourceIDs)
		if mergeErr != nil {
			return nil, mergeErr
		}
		next.ExcludedIdentities = merged
		switch existing.Phase {
		case coreapi.DenyAssignmentPhasePendingDeconfigure,
			coreapi.DenyAssignmentPhaseDeconfigured:
			next.Phase = coreapi.DenyAssignmentPhasePendingConfigure
		case coreapi.DenyAssignmentPhaseConfigured:
			if needEnsure || !observedPermissionsMatch(existing.ObservedPermissions, definition) {
				next.Phase = coreapi.DenyAssignmentPhasePendingConfigure
			}
		}
		desired[denyAssignmentType] = next
	}

	// Second loop: types that the first loop did not mark as still required.
	// Deconfigure only when the type has left the definition set. If the type
	// is still required but identities are unresolved, the first loop already
	// copied it into desired.
	for denyAssignmentType, existing := range existingDenyAssignments {
		if existing == nil {
			return nil, utils.TrackError(fmt.Errorf("DenyAssignmentsV2 has a nil status for type %s", denyAssignmentType))
		}
		if _, required := stillRequired[denyAssignmentType]; required {
			continue
		}

		next := existing.DeepCopy()
		switch existing.Phase {
		case coreapi.DenyAssignmentPhasePendingDeconfigure,
			coreapi.DenyAssignmentPhaseDeconfigured:
			// Leave as-is. A Deconfigured entry is terminal until the type is
			// required again.
		default:
			next.Phase = coreapi.DenyAssignmentPhasePendingDeconfigure
		}
		desired[denyAssignmentType] = next
	}

	if len(desired) == 0 {
		return nil, nil
	}
	return desired, nil
}

// mergeExcludedIdentities stamps or clears DeconfigureTimestamp on observed
// Azure rows. It does not insert keys or write ObservedIdentity.
// desiredIdentities are principals that should be exempt.
// unresolvedResourceIDs must not be deconfigured (transient principal fetch).
// needEnsure is true when a desired principal is missing from the observed
// map or ObservedIdentity does not match, so type Phase should become
// PendingConfigure.
func (s *clusterDenyAssignmentIntentSyncer) mergeExcludedIdentities(
	existing map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedIdentityStatus,
	desiredIdentities map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedObservedIdentity,
	unresolvedResourceIDs map[string]struct{},
) (map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedIdentityStatus, bool, error) {
	next := make(map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedIdentityStatus, len(existing))
	needEnsure := false

	for key, observed := range desiredIdentities {
		existingStatus, hasExisting := existing[key]
		if hasExisting && existingStatus == nil {
			return nil, false, utils.TrackError(fmt.Errorf("ExcludedIdentities has a nil status for resource ID %s principal ID %s", key.ResourceID, key.PrincipalID))
		}
		if !hasExisting {
			needEnsure = true
			continue
		}

		status := existingStatus.DeepCopy()
		if existingStatus.DeconfigureTimestamp != nil {
			status.DeconfigureTimestamp = nil
		}
		if !observedIdentitiesEqual(existingStatus.ObservedIdentity, observed) {
			needEnsure = true
		}
		next[key] = status
	}

	for key, existingStatus := range existing {
		if existingStatus == nil {
			return nil, false, utils.TrackError(fmt.Errorf("ExcludedIdentities has a nil status for resource ID %s principal ID %s", key.ResourceID, key.PrincipalID))
		}
		if _, stillDesired := desiredIdentities[key]; stillDesired {
			continue
		}
		if _, unresolved := unresolvedResourceIDs[key.ResourceID]; unresolved {
			next[key] = existingStatus.DeepCopy()
			continue
		}

		status := existingStatus.DeepCopy()
		if existingStatus.DeconfigureTimestamp == nil {
			requestedAt := metav1.NewTime(s.clock.Now())
			status.DeconfigureTimestamp = &requestedAt
		}
		next[key] = status
	}

	if len(next) == 0 {
		return nil, needEnsure, nil
	}
	return next, needEnsure, nil
}

func observedIdentitiesEqual(a, b *coreapi.DenyAssignmentExcludedObservedIdentity) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.ClientID == b.ClientID && a.TenantID == b.TenantID && a.PrincipalID == b.PrincipalID
}

func desiredExcludedIdentities(
	cluster *coreapi.HCPOpenShiftCluster,
	serviceProviderCluster *coreapi.ServiceProviderCluster,
	definition *denyAssignmentDefinition,
) (map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedObservedIdentity, map[string]struct{}, error) {
	identityResourceIDs, err := collectExcludedPrincipalIDs(cluster, definition)
	if err != nil {
		return nil, nil, err
	}

	desired := make(map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedObservedIdentity, len(identityResourceIDs))
	unresolvedResourceIDs := map[string]struct{}{}
	for _, identityResourceID := range identityResourceIDs {
		principalID, err := resolvePrincipalID(serviceProviderCluster, identityResourceID)
		if err != nil {
			unresolvedResourceIDs[strings.ToLower(identityResourceID.String())] = struct{}{}
			continue
		}
		key := denyAssignmentExcludedIdentityKey(identityResourceID, principalID)
		desired[key] = resolveObservedIdentity(serviceProviderCluster, identityResourceID, principalID)
	}
	return desired, unresolvedResourceIDs, nil
}

// resolveObservedIdentity returns the identity generation snapshot for one
// excluded principal. PrincipalID always comes from the same maps as the
// ExcludedIdentities key. ClientID is copied from those maps when present.
// TenantID is only on ARM metadata, so ARM overwrites the snapshot when it is
// fully resolved and its PrincipalID matches.
func resolveObservedIdentity(serviceProviderCluster *coreapi.ServiceProviderCluster, identityResourceID *azcorearm.ResourceID, principalID string) *coreapi.DenyAssignmentExcludedObservedIdentity {
	observed := &coreapi.DenyAssignmentExcludedObservedIdentity{
		PrincipalID: principalID,
	}
	key := strings.ToLower(identityResourceID.String())

	msi := serviceProviderCluster.Status.MSIManagedIdentities
	if identity := msi.ControlPlaneOperatorsIdentities[key]; identity != nil && identity.ClientID != nil {
		observed.ClientID = *identity.ClientID
	}
	if smi := msi.ServiceManagedIdentity; smi != nil && smi.ResourceID != nil &&
		strings.ToLower(smi.ResourceID.String()) == key && smi.ClientID != nil {
		observed.ClientID = *smi.ClientID
	}
	if identity := serviceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.Identities[key]; identity != nil && identity.ClientID != nil {
		observed.ClientID = *identity.ClientID
	}

	metadata := serviceProviderCluster.Status.ManagedIdentityDetails[key]
	if metadata != nil && metadata.MetadataFromARMUserAssignedIdentitiesAPI != nil &&
		metadata.MetadataFromARMUserAssignedIdentitiesAPI.HasResolvedIdentityInformation() &&
		ptr.Deref(metadata.MetadataFromARMUserAssignedIdentitiesAPI.PrincipalID, "") == principalID {
		arm := metadata.MetadataFromARMUserAssignedIdentitiesAPI
		observed.ClientID = ptr.Deref(arm.ClientID, "")
		observed.TenantID = ptr.Deref(arm.TenantID, "")
		observed.PrincipalID = ptr.Deref(arm.PrincipalID, principalID)
	}

	return observed
}

func requiredDenyAssignmentTypes(cluster *coreapi.HCPOpenShiftCluster) map[string]struct{} {
	defs := denyAssignmentDefinitions(cluster)
	required := make(map[string]struct{}, len(defs))
	for _, definition := range defs {
		required[definition.denyAssignmentType] = struct{}{}
	}
	return required
}
