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

package msicredentials

import (
	"context"
	"fmt"
	"time"

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

// MSIBasedOperatorCredentialsIntentControllerName is the single source of
// truth for this controller's name. It is used for the workqueue name (a
// Prometheus label), context/logger controller name, and log fields.
const MSIBasedOperatorCredentialsIntentControllerName = "MSIBasedOperatorCredentialsIntent"

// msiBasedOperatorCredentialsIntentSyncer keeps
// ServiceProviderCluster.Status.MSIBasedOperatorCredentials in sync with
// Cluster control-plane operators and
// ServiceProviderCluster.Status.ManagedIdentityDetails.
//
// It does not call Azure. It only marks desired phases:
//   - Operators from
//     CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators
//     whose ManagedIdentityDetails entry has resolved ClientID, PrincipalID,
//     and TenantID from MetadataFromHardcodedIdentity (hardcoded-identity
//     environments) or MetadataFromManagedIdentitiesDataplaneService (real
//     Managed Identities Data Plane) are added as PendingConfigure (or left
//     Configured / PendingConfigure if already present with the same
//     ObservedIdentity). ObservedIdentity is copied from that metadata. A
//     change to ResourceID, ClientID, PrincipalID, or TenantID on the same
//     operator updates ObservedIdentity and sets PendingConfigure; it does
//     not deconfigure. Replacement overwrites the same Key Vault secret.
//   - Operators present in MSIBasedOperatorCredentials but no longer among
//     those control-plane operators are marked PendingDeconfigure unless they
//     are already Deconfigured.
//   - Control-plane operators whose MSI-dataplane metadata is temporarily
//     unset are left as-is so a transient fetch error does not deconfigure them.
type msiBasedOperatorCredentialsIntentSyncer struct {
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
}

var _ controllerutils.ClusterSyncer = (*msiBasedOperatorCredentialsIntentSyncer)(nil)

// NewMSIBasedOperatorCredentialsIntentController creates a cluster-watching
// controller that marks MSI-based operator credentials phases from
// ManagedIdentityDetails metadata for control-plane operators.
func NewMSIBasedOperatorCredentialsIntentController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	backendInformers coreinformers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &msiBasedOperatorCredentialsIntentSyncer{
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		resourcesDBClient:            resourcesDBClient,
	}

	return controllerutils.NewClusterWatchingController(
		MSIBasedOperatorCredentialsIntentControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		1*time.Minute,
		syncer,
	)
}

func (s *msiBasedOperatorCredentialsIntentSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
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

	controlPlaneOperators := existingCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators
	// Deconfigure only after Cluster Service is gone. PendingClusterServiceID is
	// a create-time reservation and is not cleared on delete, so it is not part
	// of this signal.
	if s.clusterServiceGone(existingCluster) {
		controlPlaneOperators = nil
	}

	desiredMSIBasedOperatorCredentials, err := s.desiredMSIBasedOperatorCredentials(
		controlPlaneOperators,
		existingServiceProviderCluster,
		existingServiceProviderCluster.Status.MSIBasedOperatorCredentials,
	)
	if err != nil {
		return err
	}

	replacement := existingServiceProviderCluster.DeepCopy()
	replacement.Status.MSIBasedOperatorCredentials = desiredMSIBasedOperatorCredentials

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

// clusterServiceGone reports whether Cluster Service no longer has a cluster
// for this HCP, so MSI-based operator credentials can be torn down.
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
func (s *msiBasedOperatorCredentialsIntentSyncer) clusterServiceGone(cluster *coreapi.HCPOpenShiftCluster) bool {
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

// desiredMSIBasedOperatorCredentials computes the credentials map that should
// be stored on the ServiceProviderCluster.
//
// Required operators are the control-plane operators on the cluster payload.
// An operator is added as PendingConfigure when its identity has resolved
// ClientID and PrincipalID. Missing operators whose identities are not ready
// are not added. Operators that have left the control-plane set are marked
// PendingDeconfigure.
func (s *msiBasedOperatorCredentialsIntentSyncer) desiredMSIBasedOperatorCredentials(
	controlPlaneOperators map[string]*azcorearm.ResourceID,
	serviceProviderCluster *coreapi.ServiceProviderCluster,
	existingCredentials map[string]*coreapi.MSIBasedOperatorCredentialsStatus,
) (map[string]*coreapi.MSIBasedOperatorCredentialsStatus, error) {
	desired := make(map[string]*coreapi.MSIBasedOperatorCredentialsStatus, len(controlPlaneOperators)+len(existingCredentials))
	resolvedOperators := make(map[string]struct{}, len(controlPlaneOperators))
	unresolvedOperators := map[string]struct{}{}

	for operatorName, identityResourceID := range controlPlaneOperators {
		if len(operatorName) == 0 || identityResourceID == nil {
			continue
		}

		observedIdentity, ok := resolvedMSIBasedOperatorIdentity(serviceProviderCluster, identityResourceID)
		if !ok {
			unresolvedOperators[operatorName] = struct{}{}
			continue
		}
		resolvedOperators[operatorName] = struct{}{}

		existing, hasExisting := existingCredentials[operatorName]
		if hasExisting && existing == nil {
			return nil, utils.TrackError(fmt.Errorf("MSIBasedOperatorCredentials has a nil status for operator %s", operatorName))
		}
		if !hasExisting {
			desired[operatorName] = &coreapi.MSIBasedOperatorCredentialsStatus{
				Phase:            coreapi.MSIBasedOperatorCredentialsPhasePendingConfigure,
				ObservedIdentity: observedIdentity,
			}
			continue
		}

		next := existing.DeepCopy()
		identityChanged := !observedIdentityEqual(existing.ObservedIdentity, observedIdentity)
		next.ObservedIdentity = observedIdentity
		switch existing.Phase {
		case coreapi.MSIBasedOperatorCredentialsPhasePendingDeconfigure,
			coreapi.MSIBasedOperatorCredentialsPhaseDeconfigured:
			next.Phase = coreapi.MSIBasedOperatorCredentialsPhasePendingConfigure
		default:
			if identityChanged {
				next.Phase = coreapi.MSIBasedOperatorCredentialsPhasePendingConfigure
			}
		}
		desired[operatorName] = next
	}

	for operatorName, existing := range existingCredentials {
		if existing == nil {
			return nil, utils.TrackError(fmt.Errorf("MSIBasedOperatorCredentials has a nil status for operator %s", operatorName))
		}
		if _, stillDesired := resolvedOperators[operatorName]; stillDesired {
			continue
		}
		if _, unresolved := unresolvedOperators[operatorName]; unresolved {
			desired[operatorName] = existing.DeepCopy()
			continue
		}

		next := existing.DeepCopy()
		switch existing.Phase {
		case coreapi.MSIBasedOperatorCredentialsPhasePendingDeconfigure,
			coreapi.MSIBasedOperatorCredentialsPhaseDeconfigured:
		default:
			next.Phase = coreapi.MSIBasedOperatorCredentialsPhasePendingDeconfigure
		}
		desired[operatorName] = next
	}

	if len(desired) == 0 {
		return nil, nil
	}
	return desired, nil
}
