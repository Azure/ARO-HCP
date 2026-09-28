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

// ClusterDenyAssignmentIntentControllerName is the single source of truth for
// this controller's name. It is used for the workqueue name (a Prometheus
// label), context/logger controller name, and log fields.
const ClusterDenyAssignmentIntentControllerName = "ClusterDenyAssignmentIntent"

// clusterDenyAssignmentIntentSyncer keeps
// ServiceProviderCluster.Status.DenyAssignmentsOverManagedResourceGroup in sync with the deny
// assignment types required for the cluster.
//
// It does not call Azure.
//   - Types from denyAssignmentDefinitions whose excluded identities have a
//     fully resolved principal ID on Status.ManagedIdentityDetails are added.
//     Control-plane operators and the service managed identity use
//     MetadataFromManagedIdentitiesDataplaneService when
//     managedIdentitiesDataPlaneServiceAvailable is set, or
//     MetadataFromHardcodedIdentity otherwise. Data-plane operators use
//     MetadataFromARMUserAssignedIdentitiesAPI. The other source is not
//     consulted. Nested ExcludedIdentities rows are inserted for those
//     principals (EnsuredIdentity stays nil until ClusterDenyAssignment
//     writes it after a PUT). Types whose identities are not yet resolved
//     are not added.
//   - ExcludedIdentities is keyed by PrincipalID. TargetIdentity stores the
//     resource path, client ID, and tenant ID. A change to those fields on
//     the same principal overwrites TargetIdentity and leaves EnsuredIdentity
//     set. A principal that left the type keeps its row when EnsuredIdentity
//     is set. The first transition stamps DeconfigureTimestamp. A row that
//     was never ensured is dropped. If the principal is desired again before
//     the wait ends, the timestamp is cleared.
//   - Types present in DenyAssignmentsOverManagedResourceGroup but no longer in the definition set
//     get type DeconfigureTimestamp when Azure IDs are still tracked, and are
//     dropped when there is nothing to delete.
//   - Types that are still required but whose identities are temporarily
//     unresolved are left as-is so a transient fetch error does not
//     deconfigure them. Neighbors that have left the type are still stamped
//     or dropped.
//   - Cluster deletion is a no-op. Deny assignments are scoped to the managed
//     resource group, so Azure deletes them in cascade when that resource
//     group is removed.
type clusterDenyAssignmentIntentSyncer struct {
	clock                        utilsclock.PassiveClock
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	// managedIdentitiesDataPlaneServiceAvailable is the same environment
	// signal as FetchManagedIdentitiesInfo's fpaMIdataplaneClientBuilder != nil
	// (hardcodedIdentity == nil). When true, MSI-based excluded principals
	// (control-plane operators and the service managed identity) come from
	// MetadataFromManagedIdentitiesDataplaneService. When false, the real
	// Managed Identities Data Plane is not available and they come from
	// MetadataFromHardcodedIdentity. A nil or unresolved value on the chosen
	// source means Fetch has not resolved it yet, not that the other source
	// should be used.
	managedIdentitiesDataPlaneServiceAvailable bool
}

var _ controllerutils.ClusterSyncer = (*clusterDenyAssignmentIntentSyncer)(nil)

// NewClusterDenyAssignmentIntentController creates a cluster-watching
// controller that marks deny assignment types from the required definition
// set once excluded identities have resolved principal IDs on
// Status.ManagedIdentityDetails.
// managedIdentitiesDataPlaneServiceAvailable must match
// FetchManagedIdentitiesInfo (fpaMIdataplaneClientBuilder != nil): it selects
// MetadataFromManagedIdentitiesDataplaneService versus
// MetadataFromHardcodedIdentity for MSI-based identities (control-plane
// operators and the service managed identity).
func NewClusterDenyAssignmentIntentController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	backendInformers coreinformers.BackendInformers,
	managedIdentitiesDataPlaneServiceAvailable bool,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &clusterDenyAssignmentIntentSyncer{
		clock:                        clock,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		resourcesDBClient:            resourcesDBClient,
		managedIdentitiesDataPlaneServiceAvailable: managedIdentitiesDataPlaneServiceAvailable,
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
	// cluster teardown. There is no need to mark type deconfigure here.
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
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

	desiredDenyAssignmentsOverManagedResourceGroup, err := s.desiredDenyAssignmentsOverManagedResourceGroup(
		existingCluster,
		existingServiceProviderCluster,
		existingServiceProviderCluster.Status.DenyAssignmentsOverManagedResourceGroup,
	)
	if err != nil {
		return err
	}

	replacement := existingServiceProviderCluster.DeepCopy()
	replacement.Status.DenyAssignmentsOverManagedResourceGroup = desiredDenyAssignmentsOverManagedResourceGroup
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

// desiredDenyAssignmentsOverManagedResourceGroup computes the deny assignment map that should be
// stored on the ServiceProviderCluster.
//
// Required types are the deny assignment definitions for the cluster. A type
// is added when its excluded identities have resolved principal IDs. Missing
// types whose identities are not ready are not added. Types that have left
// the definition set are stamped for deconfigure or dropped.
func (s *clusterDenyAssignmentIntentSyncer) desiredDenyAssignmentsOverManagedResourceGroup(
	cluster *coreapi.Cluster,
	serviceProviderCluster *coreapi.ServiceProviderCluster,
	existingDenyAssignments map[string]*coreapi.DenyAssignmentStatus,
) (map[string]*coreapi.DenyAssignmentStatus, error) {
	definitionsByType := denyAssignmentDefinitionsByType(cluster)
	requiredTypes := RequiredDenyAssignmentTypes(cluster)
	deconfigureRequestedAt := metav1.NewTime(s.clock.Now())

	desired := make(map[string]*coreapi.DenyAssignmentStatus, len(requiredTypes)+len(existingDenyAssignments))
	stillRequired := make(map[string]struct{}, len(requiredTypes))

	// First loop: types that are currently required. Add the type when
	// identities are resolved and insert nested rows for desired principals.
	// Types already draining are flipped back to desired. If identities are
	// not ready, existing required types are kept so a transient fetch error
	// does not deconfigure them.
	for denyAssignmentType := range requiredTypes {
		existing, hasExisting := existingDenyAssignments[denyAssignmentType]
		if hasExisting && existing == nil {
			return nil, utils.TrackError(fmt.Errorf("DenyAssignmentsOverManagedResourceGroup has a nil status for type %s", denyAssignmentType))
		}

		definition := definitionsByType[denyAssignmentType]
		if definition == nil {
			return nil, utils.TrackError(fmt.Errorf("no definition for deny assignment type %s", denyAssignmentType))
		}
		desiredIdentities, unresolvedResourceIDs, err := s.desiredExcludedIdentities(cluster, serviceProviderCluster, definition)
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
				merged, mergeErr := s.mergeExcludedIdentities(existing.ExcludedIdentities, desiredIdentities, unresolvedResourceIDs, deconfigureRequestedAt)
				if mergeErr != nil {
					return nil, mergeErr
				}
				next.ExcludedIdentities = merged
				desired[denyAssignmentType] = next
			}
			continue
		}

		stillRequired[denyAssignmentType] = struct{}{}
		if !hasExisting {
			next := &coreapi.DenyAssignmentStatus{}
			merged, mergeErr := s.mergeExcludedIdentities(nil, desiredIdentities, unresolvedResourceIDs, deconfigureRequestedAt)
			if mergeErr != nil {
				return nil, mergeErr
			}
			next.ExcludedIdentities = merged
			desired[denyAssignmentType] = next
			continue
		}

		next := existing.DeepCopy()
		next.DeconfigureTimestamp = nil
		merged, mergeErr := s.mergeExcludedIdentities(existing.ExcludedIdentities, desiredIdentities, unresolvedResourceIDs, deconfigureRequestedAt)
		if mergeErr != nil {
			return nil, mergeErr
		}
		next.ExcludedIdentities = merged
		desired[denyAssignmentType] = next
	}

	// Second loop: types that the first loop did not mark as still required.
	// Deconfigure only when the type has left the definition set. If the type
	// is still required but identities are unresolved, the first loop already
	// copied it into desired.
	for denyAssignmentType, existing := range existingDenyAssignments {
		if existing == nil {
			return nil, utils.TrackError(fmt.Errorf("DenyAssignmentsOverManagedResourceGroup has a nil status for type %s", denyAssignmentType))
		}
		if _, required := stillRequired[denyAssignmentType]; required {
			continue
		}

		next := existing.DeepCopy()
		if !typeHasTrackedAzureResources(next) {
			continue
		}
		if next.DeconfigureTimestamp == nil {
			stamp := deconfigureRequestedAt
			next.DeconfigureTimestamp = &stamp
		}
		desired[denyAssignmentType] = next
	}

	if len(desired) == 0 {
		return nil, nil
	}
	return desired, nil
}

// mergeExcludedIdentities inserts desired identity rows and stamps or clears
// DeconfigureTimestamp. It does not write EnsuredIdentity.
// desiredIdentities are principals that should be exempt.
// unresolvedResourceIDs must not be deconfigured (transient principal fetch).
func (s *clusterDenyAssignmentIntentSyncer) mergeExcludedIdentities(
	existing map[string]*coreapi.DenyAssignmentExcludedIdentityStatus,
	desiredIdentities map[string]*coreapi.DenyAssignmentTargetIdentity,
	unresolvedResourceIDs map[string]struct{},
	deconfigureRequestedAt metav1.Time,
) (map[string]*coreapi.DenyAssignmentExcludedIdentityStatus, error) {
	next := make(map[string]*coreapi.DenyAssignmentExcludedIdentityStatus, len(existing)+len(desiredIdentities))

	for principalID, target := range desiredIdentities {
		existingStatus, hasExisting := existing[principalID]
		if hasExisting && existingStatus == nil {
			return nil, utils.TrackError(fmt.Errorf("ExcludedIdentities has a nil status for principal ID %s", principalID))
		}
		if !hasExisting {
			next[principalID] = &coreapi.DenyAssignmentExcludedIdentityStatus{
				TargetIdentity: target,
			}
			continue
		}

		status := existingStatus.DeepCopy()
		status.DeconfigureTimestamp = nil
		status.TargetIdentity = target
		next[principalID] = status
	}

	for principalID, existingStatus := range existing {
		if existingStatus == nil {
			return nil, utils.TrackError(fmt.Errorf("ExcludedIdentities has a nil status for principal ID %s", principalID))
		}
		if _, stillDesired := desiredIdentities[principalID]; stillDesired {
			continue
		}
		if existingStatus.TargetIdentity != nil {
			if _, unresolved := unresolvedResourceIDs[strings.ToLower(existingStatus.TargetIdentity.ResourceID.String())]; unresolved {
				next[principalID] = existingStatus.DeepCopy()
				continue
			}
		}

		if existingStatus.DeconfigureTimestamp != nil {
			next[principalID] = existingStatus.DeepCopy()
			continue
		}
		if existingStatus.EnsuredIdentity == nil {
			continue
		}
		status := existingStatus.DeepCopy()
		stamp := deconfigureRequestedAt
		status.DeconfigureTimestamp = &stamp
		next[principalID] = status
	}

	if len(next) == 0 {
		return nil, nil
	}
	return next, nil
}

func typeHasTrackedAzureResources(status *coreapi.DenyAssignmentStatus) bool {
	return status != nil && (status.AzureResource != nil || status.PendingAzureResource != nil)
}

// desiredExcludedIdentities returns the principals this deny assignment type
// should exclude, keyed by principal ID, and the resource IDs whose chosen
// ManagedIdentityDetails source is not ready yet. The value is the
// TargetIdentity to store on that row.
//
// Control-plane operators and the service managed identity take PrincipalID
// from dataplane or hardcoded metadata according to
// managedIdentitiesDataPlaneServiceAvailable. Data-plane operators take it
// from ARM. The same user-assigned identity used as both can therefore yield
// two principal IDs. A missing or unresolved chosen source records the
// resource ID in the second return value so mergeExcludedIdentities will not
// deconfigure existing rows for it. A nil metadata entry is an error.
func (s *clusterDenyAssignmentIntentSyncer) desiredExcludedIdentities(
	cluster *coreapi.Cluster,
	serviceProviderCluster *coreapi.ServiceProviderCluster,
	definition *denyAssignmentDefinition,
) (map[string]*coreapi.DenyAssignmentTargetIdentity, map[string]struct{}, error) {
	identities := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities
	desired := map[string]*coreapi.DenyAssignmentTargetIdentity{}
	unresolvedResourceIDs := map[string]struct{}{}

	for _, operatorName := range definition.controlPlaneOperators {
		resourceID, ok := identities.ControlPlaneOperators[operatorName]
		if !ok {
			return nil, nil, utils.TrackError(fmt.Errorf("control plane operator %q not found in cluster identity configuration", operatorName))
		}
		target, resolved, err := s.resolveMSIBasedExcludedTargetIdentity(serviceProviderCluster, resourceID)
		if err != nil {
			return nil, nil, err
		}
		if !resolved {
			unresolvedResourceIDs[strings.ToLower(resourceID.String())] = struct{}{}
			continue
		}
		desired[target.PrincipalID] = target
	}

	for _, operatorName := range definition.dataPlaneOperators {
		resourceID, ok := identities.DataPlaneOperators[operatorName]
		if !ok {
			return nil, nil, utils.TrackError(fmt.Errorf("data plane operator %q not found in cluster identity configuration", operatorName))
		}
		target, resolved, err := resolveDataPlaneExcludedTargetIdentity(serviceProviderCluster, resourceID)
		if err != nil {
			return nil, nil, err
		}
		if !resolved {
			unresolvedResourceIDs[strings.ToLower(resourceID.String())] = struct{}{}
			continue
		}
		desired[target.PrincipalID] = target
	}

	if definition.includeServiceManagedID {
		resourceID := identities.ServiceManagedIdentity
		target, resolved, err := s.resolveMSIBasedExcludedTargetIdentity(serviceProviderCluster, resourceID)
		if err != nil {
			return nil, nil, err
		}
		if !resolved {
			unresolvedResourceIDs[strings.ToLower(resourceID.String())] = struct{}{}
		} else {
			desired[target.PrincipalID] = target
		}
	}

	return desired, unresolvedResourceIDs, nil
}

// resolveMSIBasedExcludedTargetIdentity returns the target identity for an
// MSI-based deny assignment exclusion (control-plane operators and the
// service managed identity). Which Cosmos source applies is the process
// environment, the same signal FetchManagedIdentitiesInfo uses:
// MetadataFromManagedIdentitiesDataplaneService when
// managedIdentitiesDataPlaneServiceAvailable is true, otherwise
// MetadataFromHardcodedIdentity. A nil pointer or unresolved value on that
// source means Fetch has not resolved it yet. The other MSI source is not
// consulted. ARM is ignored even when the same user-assigned identity is also
// a data-plane operator. resolved is false when the entry is missing or the
// chosen source is not fully resolved.
func (s *clusterDenyAssignmentIntentSyncer) resolveMSIBasedExcludedTargetIdentity(serviceProviderCluster *coreapi.ServiceProviderCluster, identityResourceID *azcorearm.ResourceID) (*coreapi.DenyAssignmentTargetIdentity, bool, error) {
	metadata, ok, err := managedIdentityMetadata(serviceProviderCluster, identityResourceID)
	if err != nil || !ok {
		return nil, false, err
	}
	source := metadata.MetadataFromHardcodedIdentity
	// TODO given that DenyAssignments are only created when the real FPA exist and that matches the availability
	// of the Managed Identities Data Plane, do we prefer to just remove this and have as the only source the
	// Managed Identities Data Plane, as well as remoe the s.managedIdentitiesDataPlaneServiceAvailable attribute to
	// simplify?
	if s.managedIdentitiesDataPlaneServiceAvailable {
		source = metadata.MetadataFromManagedIdentitiesDataplaneService
	}
	target, resolved := targetIdentityFromMetadataValue(identityResourceID, source)
	return target, resolved, nil
}

// resolveDataPlaneExcludedTargetIdentity returns the target identity for a
// data-plane operator deny assignment exclusion from ARM User Assigned
// Identities. Dataplane and hardcoded metadata are ignored even when the
// same user-assigned identity is also a control-plane operator. resolved is
// false when the entry is missing or ARM is not fully resolved.
func resolveDataPlaneExcludedTargetIdentity(serviceProviderCluster *coreapi.ServiceProviderCluster, identityResourceID *azcorearm.ResourceID) (*coreapi.DenyAssignmentTargetIdentity, bool, error) {
	metadata, ok, err := managedIdentityMetadata(serviceProviderCluster, identityResourceID)
	if err != nil || !ok {
		return nil, false, err
	}
	target, resolved := targetIdentityFromMetadataValue(identityResourceID, metadata.MetadataFromARMUserAssignedIdentitiesAPI)
	return target, resolved, nil
}

func managedIdentityMetadata(serviceProviderCluster *coreapi.ServiceProviderCluster, identityResourceID *azcorearm.ResourceID) (*coreapi.ManagedIdentityMetadata, bool, error) {
	key := strings.ToLower(identityResourceID.String())
	metadata, hasMetadata := serviceProviderCluster.Status.ManagedIdentityDetails[key]
	if !hasMetadata {
		return nil, false, nil
	}
	if metadata == nil {
		return nil, false, utils.TrackError(fmt.Errorf("ManagedIdentityDetails has a nil metadata entry for resource ID %s", key))
	}
	return metadata, true, nil
}

func targetIdentityFromMetadataValue(identityResourceID *azcorearm.ResourceID, value *coreapi.IdentityMetadataValue) (*coreapi.DenyAssignmentTargetIdentity, bool) {
	if identityResourceID == nil || value == nil || !coreapihelpers.IdentityMetadataValueHasResolvedIdentityInformation(value) {
		return nil, false
	}
	return &coreapi.DenyAssignmentTargetIdentity{
		ResourceID:  identityResourceID,
		ClientID:    *value.ClientID,
		TenantID:    *value.TenantID,
		PrincipalID: *value.PrincipalID,
	}, true
}
