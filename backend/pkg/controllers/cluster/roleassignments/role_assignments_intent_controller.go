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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/azure"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
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
// control-plane operator, data-plane operator, and service managed identity
// role definitions required for the cluster.
//
// It does not call Azure.
//   - Keys from the cluster's operators whose identities have a fully resolved
//     Status.ManagedIdentityDetails source for that use are added.
//     Control-plane operators and the service managed identity use Managed
//     Identities Dataplane Service when
//     managedIdentitiesDataPlaneServiceAvailable is set, or hardcoded
//     identity otherwise (the same environment signal
//     FetchManagedIdentitiesInfo uses). Nil or unresolved metadata on that
//     chosen source means not ready yet; the other MSI source is not
//     consulted. They do not use ARM. Data-plane operators use ARM User
//     Assigned Identities only. The same UAMI used as both a control-plane
//     operator and a data-plane operator can therefore produce two
//     principals.
//     TargetIdentity is copied from that source. AzureResource stays nil until
//     ClusterRoleAssignmentV2 writes it after a successful ensure. Keys
//     whose identities are not yet resolved are not added.
//   - A ClientID or TenantID change on the same key updates TargetIdentity;
//     it does not deconfigure.
//   - Keys present in RoleAssignmentsV2 but no longer desired keep their row
//     when AzureResource or PendingAzureResource is set. The first transition
//     stamps DeconfigureTimestamp. A row that was never ensured is dropped.
//     If the key is desired again before the wait ends, the timestamp is
//     cleared.
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
	// managedIdentitiesDataPlaneServiceAvailable is the same environment
	// signal as FetchManagedIdentitiesInfo's fpaMIdataplaneClientBuilder != nil
	// (hardcodedIdentity == nil). When true, MSI-based TargetIdentity
	// (control-plane operators and the service managed identity) comes from
	// MetadataFromManagedIdentitiesDataplaneService. When false, the real
	// Managed Identities Data Plane is not available and it comes from
	// MetadataFromHardcodedIdentity. A nil or unresolved value on the chosen
	// source means Fetch has not resolved it yet, not that the other source
	// should be used.
	managedIdentitiesDataPlaneServiceAvailable bool
}

var _ controllerutils.ClusterSyncer = (*clusterRoleAssignmentIntentSyncer)(nil)

// NewClusterRoleAssignmentIntentController creates a cluster-watching
// controller that marks role assignment keys from the cluster's operators
// and cluster-scoped identities config once principal IDs are resolved.
// managedIdentitiesDataPlaneServiceAvailable must match
// FetchManagedIdentitiesInfo (fpaMIdataplaneClientBuilder != nil): it selects
// MetadataFromManagedIdentitiesDataplaneService versus
// MetadataFromHardcodedIdentity for MSI-based keys (control-plane operators
// and the service managed identity).
func NewClusterRoleAssignmentIntentController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clusterScopedIdentitiesConfig *azure.ClusterScopedIdentitiesConfig,
	backendInformers coreinformers.BackendInformers,
	managedIdentitiesDataPlaneServiceAvailable bool,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &clusterRoleAssignmentIntentSyncer{
		clock:                         clock,
		clusterLister:                 clusterLister,
		serviceProviderClusterLister:  serviceProviderClusterLister,
		resourcesDBClient:             resourcesDBClient,
		clusterScopedIdentitiesConfig: clusterScopedIdentitiesConfig,
		managedIdentitiesDataPlaneServiceAvailable: managedIdentitiesDataPlaneServiceAvailable,
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
	// cluster teardown. There is no need to stamp DeconfigureTimestamp here.
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
		existingServiceProviderCluster.Status.RoleAssignments,
	)
	if err != nil {
		return err
	}

	replacement := existingServiceProviderCluster.DeepCopy()
	replacement.Status.RoleAssignments = desiredRoleAssignmentsV2

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

// desiredRoleAssignmentsV2 computes the role assignment map that should be
// stored on the ServiceProviderCluster.
//
// Desired keys are each control-plane operator, data-plane operator, and
// service managed identity paired with that identity's role definitions. A
// key is added when Status.ManagedIdentityDetails has a fully resolved
// source for that use (dataplane or hardcoded for MSI-based identities,
// chosen by managedIdentitiesDataPlaneServiceAvailable; ARM for data plane).
// Missing keys whose identities are not ready are not added.
// Keys that have left the desired set are stamped for deconfigure or dropped.
// Unresolved ResourceIDs are not deconfigured.
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

// desiredRoleAssignmentIdentities enumerates the RoleAssignmentsV2 keys that
// should exist for this cluster, with the TargetIdentity each key should
// target. It does not merge against existing statuses.
//
// Control-plane operators, data-plane operators, and the service managed
// identity are each paired with that identity's role definitions from
// ClusterScopedIdentitiesConfig. MSI-based identities (control plane and
// service managed identity) take TargetIdentity from dataplane or hardcoded
// metadata according to managedIdentitiesDataPlaneServiceAvailable. Data-plane
// operators take it from ARM. The same UAMI used as both can therefore yield
// two PrincipalIDs.
//
// A missing or unresolved chosen source records the ResourceID in the second
// return value so mergeRoleAssignments will not deconfigure existing keys for
// it. A nil control-plane or data-plane identity ResourceID is skipped. A nil
// service managed identity is an error.
func (s *clusterRoleAssignmentIntentSyncer) desiredRoleAssignmentIdentities(
	cluster *coreapi.HCPOpenShiftCluster,
	serviceProviderCluster *coreapi.ServiceProviderCluster,
) (map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentTargetIdentity, map[string]struct{}, error) {
	desired := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentTargetIdentity{}
	unresolvedResourceIDs := map[string]struct{}{}
	userAssignedIdentities := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities

	for operatorName, identityResourceID := range userAssignedIdentities.ControlPlaneOperators {
		if identityResourceID == nil {
			continue
		}
		target, ok, err := s.resolveMSIBasedRoleAssignmentTargetIdentity(serviceProviderCluster, identityResourceID)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			unresolvedResourceIDs[strings.ToLower(identityResourceID.String())] = struct{}{}
			continue
		}
		roleDefinitionIDs, err := controlPlaneOperatorRoleDefinitionIDsFromConfig(s.clusterScopedIdentitiesConfig, operatorName)
		if err != nil {
			return nil, nil, err
		}
		if err := addDesiredRoleAssignmentKeys(desired, identityResourceID, roleDefinitionIDs, target); err != nil {
			return nil, nil, err
		}
	}

	for operatorName, identityResourceID := range userAssignedIdentities.DataPlaneOperators {
		if len(operatorName) == 0 {
			return nil, nil, utils.TrackError(fmt.Errorf("unexpected empty operator name for data plane operator"))
		}
		if identityResourceID == nil {
			return nil, nil, utils.TrackError(fmt.Errorf("unexpected nil identity Resource ID for data plane operator %q", operatorName))
		}

		target, ok, err := resolveDataPlaneRoleAssignmentTargetIdentity(serviceProviderCluster, identityResourceID)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			unresolvedResourceIDs[strings.ToLower(identityResourceID.String())] = struct{}{}
			continue
		}
		roleDefinitionIDs, err := dataPlaneOperatorRoleDefinitionIDsFromConfig(s.clusterScopedIdentitiesConfig, operatorName)
		if err != nil {
			return nil, nil, err
		}
		if err := addDesiredRoleAssignmentKeys(desired, identityResourceID, roleDefinitionIDs, target); err != nil {
			return nil, nil, err
		}
	}

	identityResourceID := userAssignedIdentities.ServiceManagedIdentity
	if identityResourceID == nil {
		return nil, nil, utils.TrackError(fmt.Errorf("unexpected nil identity Resource ID for service managed identity"))
	}
	target, ok, err := s.resolveMSIBasedRoleAssignmentTargetIdentity(serviceProviderCluster, identityResourceID)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		unresolvedResourceIDs[strings.ToLower(identityResourceID.String())] = struct{}{}
	} else {
		roleDefinitionIDs, err := serviceManagedIdentityRoleDefinitionIDsFromConfig(s.clusterScopedIdentitiesConfig)
		if err != nil {
			return nil, nil, err
		}
		if err := addDesiredRoleAssignmentKeys(desired, identityResourceID, roleDefinitionIDs, target); err != nil {
			return nil, nil, err
		}
	}

	return desired, unresolvedResourceIDs, nil
}

// mergeRoleAssignments builds the next RoleAssignmentsV2 map from existing
// statuses, currently desired keys, and identities that are not ready yet.
//
// Desired keys are kept or added. A missing key becomes a new status with
// TargetIdentity set. An existing key has DeconfigureTimestamp cleared and
// TargetIdentity replaced. Metadata changes on a still-desired key do not
// stamp deconfigure.
//
// Keys that left the desired set are handled in the second loop. A key whose
// ResourceID is still unresolved is copied unchanged so a missing source does
// not deconfigure. A key that already has DeconfigureTimestamp keeps that
// stamp. A key with no AzureResource or PendingAzureResource is dropped. Any
// other leftover key is stamped with DeconfigureTimestamp from the controller
// clock for the executor to delete.
func (s *clusterRoleAssignmentIntentSyncer) mergeRoleAssignments(
	existing map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus,
	desiredIdentities map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentTargetIdentity,
	unresolvedResourceIDs map[string]struct{},
) (map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus, error) {
	// Sized for the first loop. The second loop may add leftover keys that
	// are still draining or unresolved.
	desiredRoleAssignments := make(map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus, len(desiredIdentities))
	deconfigureRequestedAt := metav1.NewTime(s.clock.Now())

	// First loop: keys that are currently desired. Missing keys become a new
	// status with TargetIdentity. Existing keys have DeconfigureTimestamp
	// cleared and TargetIdentity replaced. A ClientID or TenantID change on
	// the same key does not deconfigure.
	for key, target := range desiredIdentities {
		existingStatus, hasExisting := existing[key]
		if hasExisting && existingStatus == nil {
			return nil, utils.TrackError(fmt.Errorf("RoleAssignmentsV2 has a nil status for resource ID %s principal ID %s role definition resource ID %s", key.ResourceID, key.PrincipalID, key.RoleDefinitionResourceID))
		}
		if !hasExisting {
			desiredRoleAssignments[key] = &coreapi.RoleAssignmentStatus{
				TargetIdentity: target.DeepCopy(),
			}
			continue
		}

		status := existingStatus.DeepCopy()
		status.DeconfigureTimestamp = nil
		status.TargetIdentity = target.DeepCopy()
		desiredRoleAssignments[key] = status
	}

	// Second loop: keys the first loop did not keep as desired. Deconfigure
	// only when the identity has left the desired set. If the ResourceID is
	// still unresolved, the identity has not left; copy the existing status
	// so a missing source does not deconfigure.
	for key, existingStatus := range existing {
		if existingStatus == nil {
			return nil, utils.TrackError(fmt.Errorf("RoleAssignmentsV2 has a nil status for resource ID %s principal ID %s role definition resource ID %s", key.ResourceID, key.PrincipalID, key.RoleDefinitionResourceID))
		}
		if _, stillDesired := desiredIdentities[key]; stillDesired {
			continue
		}
		if _, unresolved := unresolvedResourceIDs[key.ResourceID]; unresolved {
			desiredRoleAssignments[key] = existingStatus.DeepCopy()
			continue
		}

		// Already stamped: keep the timestamp so the 24h wait is not reset.
		if existingStatus.DeconfigureTimestamp != nil {
			desiredRoleAssignments[key] = existingStatus.DeepCopy()
			continue
		}
		// Never ensured in Azure: drop instead of stamping deconfigure.
		if existingStatus.AzureResource == nil && existingStatus.PendingAzureResource == nil {
			continue
		}

		// Stamped: request deconfigure.
		status := existingStatus.DeepCopy()
		stamp := deconfigureRequestedAt
		status.DeconfigureTimestamp = &stamp
		desiredRoleAssignments[key] = status
	}

	if len(desiredRoleAssignments) == 0 {
		return nil, nil
	}
	return desiredRoleAssignments, nil
}

// addDesiredRoleAssignmentKeys inserts one RoleAssignmentsV2 key per role
// definition for identityResourceID. ResourceID is lowercased. PrincipalID
// comes from target. A nil role definition ID is an error. Multiple role
// definitions share the same TargetIdentity.
func addDesiredRoleAssignmentKeys(
	desired map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentTargetIdentity,
	identityResourceID *azcorearm.ResourceID,
	roleDefinitionIDs []*azcorearm.ResourceID,
	target *coreapi.RoleAssignmentTargetIdentity,
) error {
	for _, roleDefinitionID := range roleDefinitionIDs {
		if roleDefinitionID == nil {
			return utils.TrackError(fmt.Errorf("unexpected nil role definition Resource ID for identity %s", identityResourceID))
		}
		key := coreapi.RoleAssignmentKey{
			ResourceID:               strings.ToLower(identityResourceID.String()),
			PrincipalID:              target.PrincipalID,
			RoleDefinitionResourceID: roleDefinitionID.String(),
		}
		desired[key] = target
	}
	return nil
}

func controlPlaneOperatorRoleDefinitionIDsFromConfig(config *azure.ClusterScopedIdentitiesConfig, operatorName string) ([]*azcorearm.ResourceID, error) {
	// Here we assume that if the operator name exists in the cluster payload, the cluster scoped identities config always contains
	// that operator name as defined in it. If that's not the case we return an error.
	operatorIdentity, ok := config.ControlPlaneOperatorsIdentities[azure.ClusterOperatorIdentifier(operatorName)]
	if !ok {
		return nil, utils.TrackError(fmt.Errorf("no control plane operator identity configuration for operator %q", operatorName))
	}

	if operatorIdentity == nil {
		return nil, utils.TrackError(fmt.Errorf("control plane operator identity configuration for operator %q exists but is nil", operatorName))
	}

	// Here we assume that the operator identity always contains at least one role definition associated to it in the cluster scoped identities config.
	// If that's not the case we return an error.
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

func serviceManagedIdentityRoleDefinitionIDsFromConfig(config *azure.ClusterScopedIdentitiesConfig) ([]*azcorearm.ResourceID, error) {
	if config.ServiceManagedIdentity == nil {
		return nil, utils.TrackError(fmt.Errorf("no service managed identity configuration"))
	}
	roleDefinitionIDs := config.ServiceManagedIdentity.RoleDefinitionsResourceIDs()
	if len(roleDefinitionIDs) == 0 {
		return nil, utils.TrackError(fmt.Errorf("no role definitions configured for the service managed identity"))
	}
	return roleDefinitionIDs, nil
}

// resolveMSIBasedRoleAssignmentTargetIdentity returns the identity generation
// snapshot for an MSI-based role assignment (control-plane operators and the
// service managed identity). Which Cosmos source applies is the process
// environment, the same signal FetchManagedIdentitiesInfo uses:
// MetadataFromManagedIdentitiesDataplaneService when
// managedIdentitiesDataPlaneServiceAvailable is true, otherwise
// MetadataFromHardcodedIdentity. A nil pointer or unresolved
// value on that source means Fetch has not resolved it yet. The other MSI
// source is not consulted. ARM is ignored even when the same UAMI is also a
// data-plane operator. ok is false when the entry is missing or the chosen
// source is not fully resolved.
func (s *clusterRoleAssignmentIntentSyncer) resolveMSIBasedRoleAssignmentTargetIdentity(serviceProviderCluster *coreapi.ServiceProviderCluster, identityResourceID *azcorearm.ResourceID) (*coreapi.RoleAssignmentTargetIdentity, bool, error) {
	metadata, ok, err := managedIdentityMetadata(serviceProviderCluster, identityResourceID)
	if err != nil || !ok {
		return nil, false, err
	}
	source := metadata.MetadataFromHardcodedIdentity
	if s.managedIdentitiesDataPlaneServiceAvailable {
		source = metadata.MetadataFromManagedIdentitiesDataplaneService
	}
	target, ok := roleAssignmentTargetIdentityFromMetadataValue(source)
	return target, ok, nil
}

// resolveDataPlaneRoleAssignmentTargetIdentity returns the identity
// generation snapshot for a data-plane operator role assignment from ARM
// User Assigned Identities. Dataplane and hardcoded metadata are ignored
// even when the same UAMI is also a control-plane operator. ok is false when
// the entry is missing or ARM is not fully resolved.
func resolveDataPlaneRoleAssignmentTargetIdentity(serviceProviderCluster *coreapi.ServiceProviderCluster, identityResourceID *azcorearm.ResourceID) (*coreapi.RoleAssignmentTargetIdentity, bool, error) {
	metadata, ok, err := managedIdentityMetadata(serviceProviderCluster, identityResourceID)
	if err != nil || !ok {
		return nil, false, err
	}
	target, ok := roleAssignmentTargetIdentityFromMetadataValue(metadata.MetadataFromARMUserAssignedIdentitiesAPI)
	return target, ok, nil
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

func roleAssignmentTargetIdentityFromMetadataValue(value *coreapi.IdentityMetadataValue) (*coreapi.RoleAssignmentTargetIdentity, bool) {
	if value == nil || !value.HasResolvedIdentityInformation() {
		return nil, false
	}
	return &coreapi.RoleAssignmentTargetIdentity{
		ClientID:    ptr.Deref(value.ClientID, ""),
		TenantID:    ptr.Deref(value.TenantID, ""),
		PrincipalID: ptr.Deref(value.PrincipalID, ""),
	}, true
}
