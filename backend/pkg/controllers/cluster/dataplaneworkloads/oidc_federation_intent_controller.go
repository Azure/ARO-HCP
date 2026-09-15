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
// It does not call Azure. It only marks desired federation entries:
//   - Unique ResourceIDs from
//     CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators
//     whose ManagedIdentityDetails entry has resolved
//     MetadataFromARMUserAssignedIdentitiesAPI (ClientID, PrincipalID, and
//     TenantID) are added with TargetIdentity copied from that ARM metadata
//     (or left as-is when already present with the same TargetIdentity).
//     A change to those IDs on the same ResourceID updates TargetIdentity and
//     clears EnsuredIdentity, AzureResources, and PendingAzureResources; it
//     does not deconfigure. Identities that only have dataplane or
//     hardcoded-identity metadata (control plane operators and the
//     ServiceManagedIdentity) are ignored, even when the same UAMI is still
//     used as CP or SMI.
//   - Identities present in ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation
//     but no longer among those data-plane operator identities get
//     DeconfigureTimestamp stamped on first transition. Entries with nothing
//     tracked to delete are dropped: the executor removes the key after
//     successful deconfigure, and this loop also prunes leftover empty
//     documents. The executor waits 24h from DeconfigureTimestamp on a live
//     cluster. Cluster deletion still stamps the request time; the executor
//     ignores the wait when DeletionTimestamp is set.
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
// Only data-plane operator identities are considered: unique ResourceIDs from
// DataPlaneOperators whose ManagedIdentityDetails entry has resolved
// MetadataFromARMUserAssignedIdentitiesAPI metadata source.
//
// The map is keyed by lowercased ResourceID. A change to ClientID, PrincipalID,
// or TenantID on the same ResourceID means that the identity itself has changed (for example, it has been deleted and recreated with the same
// resource ID), and it needs to be oidc federated again: the first
// loop updates TargetIdentity and clears EnsuredIdentity, AzureResources, and
// PendingAzureResources. The second loop deconfigures only when the ResourceID
// left DataPlaneOperators.
//
// The first transition to deconfigure stamps DeconfigureTimestamp. The DataPlaneOIDCFederation controller waits
// 24h from that event to start the actual deconfiguration process. Already deconfiguring entries
// keep their existing stamp so the wait is not reset.
func (s *dataPlaneOIDCFederationIntentSyncer) desiredDataPlaneOIDCFederationStatus(
	ctx context.Context,
	dataPlaneOperators map[string]*azcorearm.ResourceID,
	existingManagedIdentityDetails map[string]*coreapi.ManagedIdentityMetadata,
	existingManagedIdentityDataplaneOIDCFederationStatus map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus,
) (map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus, error) {
	logger := utils.LoggerFromContext(ctx)

	desiredDataPlaneOperatorIdentityResourceIDs := s.uniqueDataPlaneOperatorIdentityResourceIDs(dataPlaneOperators)
	desiredManagedIdentityDataplaneOIDCFederationStatus := make(map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus)

	resourceIDsWithResolvedIdentityMetadata := make(map[string]struct{})
	resourceIDsWithUnresolvedIdentityMetadata := make(map[string]struct{})

	// First loop: data-plane operators that currently have resolved ARM User
	// Assigned Identities metadata in ManagedIdentityDetails. These are the
	// ResourceIDs that should be considered for data plane oidc federation configuration/deconfiguration. Missing entries in ManagedIdentityDetails
	// get TargetIdentity set. Entries with the same TargetIdentity are left as-is except
	// DeconfigureTimestamp is cleared if they were draining. A change to
	// ClientID, PrincipalID, or TenantID updates TargetIdentity and clears
	// EnsuredIdentity and the FIC lists (the UAMI was recreated, so child FICs
	// are already gone). Entries that were deconfiguring and
	// are desired again are flipped back by clearing DeconfigureTimestamp.
	for resourceIDKey := range desiredDataPlaneOperatorIdentityResourceIDs {
		metadata, hasMetadata := existingManagedIdentityDetails[resourceIDKey]
		if !hasMetadata {
			// FetchManagedIdentitiesInfo controller has not written this identity yet.
			resourceIDsWithUnresolvedIdentityMetadata[resourceIDKey] = struct{}{}
			continue
		}
		if metadata == nil {
			return nil, utils.TrackError(fmt.Errorf("ManagedIdentityDetails has a nil metadata entry for resource ID %s", resourceIDKey))
		}
		targetIdentity, ok := s.targetIdentityFromARMUserAssignedIdentities(metadata)
		if !ok {
			// ARM User Assigned Identities metadata is not resolved. That happens
			// when the FetchManagedIdentitiesInfo controller has not written this identity yet, when the metadata source
			// has not been queried yet, when RetrievalError is set, or when the identity does not exist anymore in Azure.
			// Do not deconfigure existing entries for this ResourceID in the second loop.
			resourceIDsWithUnresolvedIdentityMetadata[resourceIDKey] = struct{}{}
			continue
		}
		resourceIDsWithResolvedIdentityMetadata[resourceIDKey] = struct{}{}

		existing, hasExisting := existingManagedIdentityDataplaneOIDCFederationStatus[resourceIDKey]
		if hasExisting && existing == nil {
			return nil, utils.TrackError(fmt.Errorf("ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation has a nil status for resource ID %s", resourceIDKey))
		}
		if !hasExisting {
			// If the identity is not yet tracked for data plane oidc federation configuration/deconfiguration, set it with the TargetIdentity.
			desiredManagedIdentityDataplaneOIDCFederationStatus[resourceIDKey] = &coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				TargetIdentity: targetIdentity,
			}
			continue
		}

		next := existing.DeepCopy()
		// To determine if the identity has changed, we can compare the TargetIdentity of the existing entry with the new TargetIdentity using the
		// != operator because it will compare the values of the fields of the TargetIdentity struct, which are all string values.
		identityChanged := existing.TargetIdentity != targetIdentity
		next.TargetIdentity = targetIdentity
		// If the identity is already tracked for data plane oidc federation configuration/deconfiguration, ensure the DeconfigureTimestamp is cleared.
		// This covers the case where the identity is desired again after being deconfigured while still being in the process of deconfiguration.
		next.DeconfigureTimestamp = nil
		// If TargetIdentity has changed, the UAMI was recreated. Child FICs are
		// gone with the old resource, so clear EnsuredIdentity and the FIC lists.
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
			next.EnsuredIdentity = nil
			next.AzureResources = nil
			next.PendingAzureResources = nil
		}
		desiredManagedIdentityDataplaneOIDCFederationStatus[resourceIDKey] = next
	}

	// Second loop: federation ResourceIDs that the first loop did not mark as
	// still desired. Deconfigure only when the identity is gone from
	// DataPlaneOperators. ARM metadata on a control-plane or SMI entry for the
	// same ResourceID is not enough to keep federation. If the identity is still
	// a data-plane operator but ARM IDs are unset, keep the existing entry. A
	// transient Azure error in FetchManagedIdentitiesInfo must not start
	// deconfiguration.
	for resourceIDKey, existing := range existingManagedIdentityDataplaneOIDCFederationStatus {
		if existing == nil {
			return nil, utils.TrackError(fmt.Errorf("ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation has a nil status for resource ID %s", resourceIDKey))
		}
		//  If the key is still desired, keep the existing entry.
		if _, stillDesired := resourceIDsWithResolvedIdentityMetadata[resourceIDKey]; stillDesired {
			continue
		}
		// ARM User Assigned Identities metadata is unresolved for this
		// ResourceID. The identity is still a data-plane operator, so it is
		// absent from resolvedResourceIDs even though it has not left the
		// desired set. Keep the existing status. A transient fetch failure
		// must not start deconfiguration.
		if _, unresolved := resourceIDsWithUnresolvedIdentityMetadata[resourceIDKey]; unresolved {
			desiredManagedIdentityDataplaneOIDCFederationStatus[resourceIDKey] = existing.DeepCopy()
			continue
		}

		// This ResourceID is no longer desired: it left DataPlaneOperators,
		// or Cluster Service is gone and the desired set is empty. Start
		// deconfiguration unless it is already in progress. Drop leftover
		// entries with nothing tracked to delete. ClientID, PrincipalID, or
		// TenantID changes are handled in the first loop on this same key
		// and must not deconfigure.
		if existing.DeconfigureTimestamp != nil {
			// If the identity is already marked for deconfiguration, keep the existing entry.
			desiredManagedIdentityDataplaneOIDCFederationStatus[resourceIDKey] = existing.DeepCopy()
			continue
		}
		// TODO is this correct, or should we keep the entry and start a 24h deconfigure? is race condition possible here if
		// we do what the current code codes?
		// Empty FederatedIdentityCredential lists mean nothing exists in Azure to delete: never
		// configured, or the identity was recreated and child FederatedIdentityCredential resources are already gone.
		// Drop the map entry completely instead of starting a 24h deconfigure.
		if len(existing.AzureResources) == 0 && len(existing.PendingAzureResources) == 0 {
			continue
		}
		// If the identity has Azure resources or pending Azure resources, mark it for deconfiguration.
		next := existing.DeepCopy()
		deconfigureRequestedAt := metav1.NewTime(s.clock.Now())
		next.DeconfigureTimestamp = &deconfigureRequestedAt
		desiredManagedIdentityDataplaneOIDCFederationStatus[resourceIDKey] = next
	}

	if len(desiredManagedIdentityDataplaneOIDCFederationStatus) == 0 {
		return nil, nil
	}

	return desiredManagedIdentityDataplaneOIDCFederationStatus, nil
}

// uniqueDataPlaneOperatorIdentityResourceIDs returns the unique data-plane operator
// identity ResourceIDs, keyed by the fully lowercased string form. Empty operator
// names and nil ResourceIDs are skipped.
func (s *dataPlaneOIDCFederationIntentSyncer) uniqueDataPlaneOperatorIdentityResourceIDs(dataPlaneOperators map[string]*azcorearm.ResourceID) map[string]*azcorearm.ResourceID {
	unique := make(map[string]*azcorearm.ResourceID, len(dataPlaneOperators))
	for operatorName, resourceID := range dataPlaneOperators {
		if len(operatorName) == 0 || resourceID == nil {
			continue
		}
		unique[strings.ToLower(resourceID.String())] = resourceID
	}
	return unique
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
