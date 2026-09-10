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

package roleassignments

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
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ClusterRoleAssignmentIntentControllerName is the single source of truth for
// this controller's name. It is used for the workqueue name (a Prometheus
// label), context/logger controller name, and log fields.
const ClusterRoleAssignmentIntentControllerName = "ClusterRoleAssignmentIntent"

// clusterRoleAssignmentIntentSyncer keeps
// ServiceProviderCluster.Status.RoleAssignmentsV2 in sync with the
// control-plane and data-plane operator identities and role definitions
// required for the cluster.
//
// It does not call Azure. It only marks desired phases:
//   - Keys from the cluster's operators whose identities have resolved
//     principal IDs are added as PendingConfigure (or left Configured /
//     PendingConfigure if already present). Keys whose identities are not yet
//     resolved are not added.
//   - ObservedIdentity is filled from resolved ClientID/TenantID/PrincipalID.
//     A ClientID or TenantID change on the same key updates ObservedIdentity
//     and sets PendingConfigure; it does not deconfigure.
//   - Keys present in RoleAssignmentsV2 but no longer desired are marked
//     PendingDeconfigure unless they are already Deconfigured. The first
//     transition stamps DeconfigureTimestamp.
//   - ResourceIDs that are still required but whose principal IDs are
//     temporarily unresolved are left as-is so a transient fetch error does
//     not deconfigure them.
//   - Cluster deletion is a no-op. Role assignments are scoped to the managed
//     resource group, so Azure deletes them in cascade when that resource
//     group is removed.
type clusterRoleAssignmentIntentSyncer struct {
	clock                         utilsclock.PassiveClock
	clusterLister                 corelisters.ClusterLister
	serviceProviderClusterLister  corelisters.ServiceProviderClusterLister
	resourcesDBClient             corecosmosstorage.ResourcesDBClient
	clusterScopedIdentitiesConfig *azure.ClusterScopedIdentitiesConfig
}

var _ controllerutils.ClusterSyncer = (*clusterRoleAssignmentIntentSyncer)(nil)

// NewClusterRoleAssignmentIntentController creates a cluster-watching
// controller that marks role assignment phases from the cluster's operators
// and cluster-scoped identities config once principal IDs are resolved.
func NewClusterRoleAssignmentIntentController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clusterScopedIdentitiesConfig *azure.ClusterScopedIdentitiesConfig,
	backendInformers coreinformers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &clusterRoleAssignmentIntentSyncer{
		clock:                         clock,
		clusterLister:                 clusterLister,
		serviceProviderClusterLister:  serviceProviderClusterLister,
		resourcesDBClient:             resourcesDBClient,
		clusterScopedIdentitiesConfig: clusterScopedIdentitiesConfig,
	}

	return controllerutils.NewClusterWatchingController(
		ClusterRoleAssignmentIntentControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		1*time.Minute,
		syncer,
	)
}

func (s *clusterRoleAssignmentIntentSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := s.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster from cache: %w", err))
	}

	// Role assignments are scoped to the managed resource group, so Azure
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

	desiredRoleAssignmentsV2, err := s.desiredRoleAssignmentsV2(
		existingCluster,
		existingServiceProviderCluster,
		existingServiceProviderCluster.Status.RoleAssignmentsV2,
	)
	if err != nil {
		return err
	}

	replacement := existingServiceProviderCluster.DeepCopy()
	replacement.Status.RoleAssignmentsV2 = desiredRoleAssignmentsV2

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

// desiredRoleAssignmentsV2 computes the role assignment map that should be
// stored on the ServiceProviderCluster.
//
// Desired keys are each control-plane and data-plane operator identity paired
// with that operator's role definitions. A key is added as PendingConfigure
// when its principal ID is resolved. Missing keys whose identities are not
// ready are not added. Keys that have left the desired set are marked
// PendingDeconfigure. Unresolved ResourceIDs are not deconfigured.
func (s *clusterRoleAssignmentIntentSyncer) desiredRoleAssignmentsV2(
	cluster *coreapi.HCPOpenShiftCluster,
	serviceProviderCluster *coreapi.ServiceProviderCluster,
	existingRoleAssignments map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus,
) (map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus, error) {
	desiredIdentities, unresolvedResourceIDs, err := s.desiredRoleAssignmentIdentities(cluster, serviceProviderCluster)
	if err != nil {
		return nil, err
	}
	return s.mergeRoleAssignments(existingRoleAssignments, desiredIdentities, unresolvedResourceIDs)
}

func (s *clusterRoleAssignmentIntentSyncer) mergeRoleAssignments(
	existing map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus,
	desiredIdentities map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentObservedIdentity,
	unresolvedResourceIDs map[string]struct{},
) (map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus, error) {
	next := make(map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus, len(desiredIdentities)+len(existing))

	for key, observed := range desiredIdentities {
		existingStatus, hasExisting := existing[key]
		if hasExisting && existingStatus == nil {
			return nil, utils.TrackError(fmt.Errorf("RoleAssignmentsV2 has a nil status for resource ID %s principal ID %s role definition resource ID %s", key.ResourceID, key.PrincipalID, key.RoleDefinitionResourceID))
		}
		if !hasExisting {
			next[key] = &coreapi.RoleAssignmentStatus{
				Phase:            coreapi.RoleAssignmentPhasePendingConfigure,
				ObservedIdentity: observed.DeepCopy(),
			}
			continue
		}

		status := existingStatus.DeepCopy()
		switch existingStatus.Phase {
		case coreapi.RoleAssignmentPhasePendingDeconfigure,
			coreapi.RoleAssignmentPhaseDeconfigured:
			status.Phase = coreapi.RoleAssignmentPhasePendingConfigure
			status.DeconfigureTimestamp = nil
			status.ObservedIdentity = observed.DeepCopy()
		default:
			if !roleAssignmentObservedIdentitiesEqual(existingStatus.ObservedIdentity, observed) {
				status.Phase = coreapi.RoleAssignmentPhasePendingConfigure
				status.DeconfigureTimestamp = nil
				status.ObservedIdentity = observed.DeepCopy()
			}
		}
		next[key] = status
	}

	for key, existingStatus := range existing {
		if existingStatus == nil {
			return nil, utils.TrackError(fmt.Errorf("RoleAssignmentsV2 has a nil status for resource ID %s principal ID %s role definition resource ID %s", key.ResourceID, key.PrincipalID, key.RoleDefinitionResourceID))
		}
		if _, stillDesired := desiredIdentities[key]; stillDesired {
			continue
		}
		if _, unresolved := unresolvedResourceIDs[key.ResourceID]; unresolved {
			next[key] = existingStatus.DeepCopy()
			continue
		}

		status := existingStatus.DeepCopy()
		switch existingStatus.Phase {
		case coreapi.RoleAssignmentPhasePendingDeconfigure,
			coreapi.RoleAssignmentPhaseDeconfigured:
			// Leave as-is. Restamping DeconfigureTimestamp would reset the
			// 24h wait. A Deconfigured entry is terminal until desired again.
		default:
			status.Phase = coreapi.RoleAssignmentPhasePendingDeconfigure
			requestedAt := metav1.NewTime(s.clock.Now())
			status.DeconfigureTimestamp = &requestedAt
		}
		next[key] = status
	}

	if len(next) == 0 {
		return nil, nil
	}
	return next, nil
}

func roleAssignmentObservedIdentitiesEqual(a, b *coreapi.RoleAssignmentObservedIdentity) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.ClientID == b.ClientID && a.TenantID == b.TenantID && a.PrincipalID == b.PrincipalID
}

func (s *clusterRoleAssignmentIntentSyncer) desiredRoleAssignmentIdentities(
	cluster *coreapi.HCPOpenShiftCluster,
	serviceProviderCluster *coreapi.ServiceProviderCluster,
) (map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentObservedIdentity, map[string]struct{}, error) {
	desired := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentObservedIdentity{}
	unresolvedResourceIDs := map[string]struct{}{}
	userAssignedIdentities := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities

	for operatorName, identityResourceID := range userAssignedIdentities.ControlPlaneOperators {
		if identityResourceID == nil {
			continue
		}
		principalID, ok := controlPlaneOperatorPrincipalID(serviceProviderCluster, identityResourceID)
		if !ok {
			unresolvedResourceIDs[strings.ToLower(identityResourceID.String())] = struct{}{}
			continue
		}
		roleDefinitionIDs, err := controlPlaneOperatorRoleDefinitionIDsFromConfig(s.clusterScopedIdentitiesConfig, operatorName)
		if err != nil {
			unresolvedResourceIDs[strings.ToLower(identityResourceID.String())] = struct{}{}
			return nil, nil, err
		}
		observed := resolveRoleAssignmentObservedIdentity(serviceProviderCluster, identityResourceID, principalID)
		addDesiredRoleAssignmentKeys(desired, identityResourceID, principalID, roleDefinitionIDs, observed)
	}

	for operatorName, identityResourceID := range userAssignedIdentities.DataPlaneOperators {
		if identityResourceID == nil {
			continue
		}
		principalID, ok := dataPlaneOperatorPrincipalID(serviceProviderCluster, identityResourceID)
		if !ok {
			unresolvedResourceIDs[strings.ToLower(identityResourceID.String())] = struct{}{}
			continue
		}
		roleDefinitionIDs, err := dataPlaneOperatorRoleDefinitionIDsFromConfig(s.clusterScopedIdentitiesConfig, operatorName)
		if err != nil {
			unresolvedResourceIDs[strings.ToLower(identityResourceID.String())] = struct{}{}
			return nil, nil, err
		}
		observed := resolveRoleAssignmentObservedIdentity(serviceProviderCluster, identityResourceID, principalID)
		addDesiredRoleAssignmentKeys(desired, identityResourceID, principalID, roleDefinitionIDs, observed)
	}

	return desired, unresolvedResourceIDs, nil
}

func addDesiredRoleAssignmentKeys(
	desired map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentObservedIdentity,
	identityResourceID *azcorearm.ResourceID,
	principalID string,
	roleDefinitionIDs []*azcorearm.ResourceID,
	observed *coreapi.RoleAssignmentObservedIdentity,
) {
	for _, roleDefinitionID := range roleDefinitionIDs {
		if roleDefinitionID == nil {
			continue
		}
		key := coreapi.RoleAssignmentKey{
			ResourceID:       strings.ToLower(identityResourceID.String()),
			PrincipalID:      principalID,
			RoleDefinitionResourceID: roleDefinitionID.String(),
		}
		desired[key] = observed
	}
}

func controlPlaneOperatorRoleDefinitionIDsFromConfig(config *azure.ClusterScopedIdentitiesConfig, operatorName string) ([]*azcorearm.ResourceID, error) {
	operatorIdentity, ok := config.ControlPlaneOperatorsIdentities[azure.ClusterOperatorIdentifier(operatorName)]
	if !ok || operatorIdentity == nil {
		return nil, utils.TrackError(fmt.Errorf("no control plane operator identity configuration for operator %q", operatorName))
	}
	roleDefinitionIDs := operatorIdentity.RoleDefinitionsResourceIDs()
	if len(roleDefinitionIDs) == 0 {
		return nil, utils.TrackError(fmt.Errorf("no role definitions configured for control plane operator %q", operatorName))
	}
	return roleDefinitionIDs, nil
}

func dataPlaneOperatorRoleDefinitionIDsFromConfig(config *azure.ClusterScopedIdentitiesConfig, operatorName string) ([]*azcorearm.ResourceID, error) {
	operatorIdentity, ok := config.DataPlaneOperatorsIdentities[azure.ClusterOperatorIdentifier(operatorName)]
	if !ok || operatorIdentity == nil {
		return nil, utils.TrackError(fmt.Errorf("no data plane operator identity configuration for operator %q", operatorName))
	}
	roleDefinitionIDs := operatorIdentity.RoleDefinitionsResourceIDs()
	if len(roleDefinitionIDs) == 0 {
		return nil, utils.TrackError(fmt.Errorf("no role definitions configured for data plane operator %q", operatorName))
	}
	return roleDefinitionIDs, nil
}

// resolveRoleAssignmentObservedIdentity returns the identity generation snapshot
// for one role assignment. PrincipalID always comes from the same maps as the
// RoleAssignmentsV2 key. ClientID is copied from those maps when present.
// TenantID is only on ARM metadata, so ARM overwrites the snapshot when it is
// fully resolved and its PrincipalID matches.
func resolveRoleAssignmentObservedIdentity(serviceProviderCluster *coreapi.ServiceProviderCluster, identityResourceID *azcorearm.ResourceID, principalID string) *coreapi.RoleAssignmentObservedIdentity {
	observed := &coreapi.RoleAssignmentObservedIdentity{
		PrincipalID: principalID,
	}
	key := strings.ToLower(identityResourceID.String())

	msi := serviceProviderCluster.Status.MSIManagedIdentities
	if identity := msi.ControlPlaneOperatorsIdentities[key]; identity != nil && identity.ClientID != nil {
		observed.ClientID = *identity.ClientID
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
