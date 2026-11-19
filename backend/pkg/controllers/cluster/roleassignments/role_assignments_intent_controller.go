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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/azure"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/azureresourcescosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/azureresourcesinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ClusterRoleAssignmentsIntentControllerName is the single source of truth for
// this controller's name. It is used for the workqueue name (a Prometheus
// label), context/logger controller name, and log fields.
const ClusterRoleAssignmentsIntentControllerName = "ClusterRoleAssignmentsIntent"

// roleAssignmentDeconfigureDelay is how long a leftover key is kept in
// RoleAssignmentsOverManagedResourceGroup (and therefore in the StackDesire
// template ClusterRoleAssignments publishes) after DeconfigureTimestamp is
// stamped, before ClusterRoleAssignmentIntent drops it from the map.
const roleAssignmentDeconfigureDelay = 24 * time.Hour

// clusterRoleAssignmentsIntentSyncer keeps
// ServiceProviderCluster.Status.RoleAssignmentsOverManagedResourceGroup in sync with the
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
//     TargetIdentity is copied from that source. Keys whose identities are not
//     yet resolved are not added.
//   - A ClientID or TenantID change on the same key updates TargetIdentity;
//     it does not deconfigure.
//   - Keys present in RoleAssignmentsOverManagedResourceGroup but no longer desired keep their row
//     for roleAssignmentDeconfigureDelay (24h) from the first transition,
//     which stamps DeconfigureTimestamp, so ClusterRoleAssignments keeps
//     rendering them into the published StackDesire template during that
//     window. Once the wait elapses the row is dropped. If the key is desired
//     again before the wait ends, the timestamp is cleared.
//   - ResourceIDs that are still required but whose principal IDs are
//     temporarily unresolved are left as-is so a transient fetch error does
//     not deconfigure them.
//   - Cluster deletion is a no-op. Role assignments are scoped to the managed
//     resource group, so Azure deletes them in cascade when that resource
//     group is removed.
type clusterRoleAssignmentsIntentSyncer struct {
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

var _ controllerutils.ClusterSyncer = (*clusterRoleAssignmentsIntentSyncer)(nil)

// NewClusterRoleAssignmentsIntentController creates a cluster-watching
// controller that marks role assignment keys from the cluster's operators
// and cluster-scoped identities config once principal IDs are resolved.
// managedIdentitiesDataPlaneServiceAvailable must match
// FetchManagedIdentitiesInfo (fpaMIdataplaneClientBuilder != nil): it selects
// MetadataFromManagedIdentitiesDataplaneService versus
// MetadataFromHardcodedIdentity for MSI-based keys (control-plane operators
// and the service managed identity).
func NewClusterRoleAssignmentsIntentController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	azureResourcesDBClient azureresourcescosmosstorage.AzureResourcesDBClient,
	backendInformers coreinformers.BackendInformers,
	azureResourcesInformers azureresourcesinformers.AzureResourcesInformers,
	clusterScopedIdentitiesConfig *azure.ClusterScopedIdentitiesConfig,
	managedIdentitiesDataPlaneServiceAvailable bool,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()
	deploymentStackDesireInformer, _ := azureResourcesInformers.DeploymentStackDesires()

	syncer := &clusterRoleAssignmentsIntentSyncer{
		clock:                         clock,
		clusterLister:                 clusterLister,
		serviceProviderClusterLister:  serviceProviderClusterLister,
		resourcesDBClient:             resourcesDBClient,
		clusterScopedIdentitiesConfig: clusterScopedIdentitiesConfig,
		managedIdentitiesDataPlaneServiceAvailable: managedIdentitiesDataPlaneServiceAvailable,
	}

	controller := controllerutils.NewClusterWatchingController(
		ClusterRoleAssignmentsIntentControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		1*time.Minute,
		syncer,
	)

	// AddInterestingResource only filters events, it does not subscribe to them.
	// Wire the DeploymentStackDesire informer as an actual event source so that
	// the stack desires controller writing an observed Azure status back onto the
	// desire re-triggers this controller, which consumes that status to advance its
	// stamp/drop state machine. Resync is 0: the periodic resync is done once on the
	// cluster (key) resource inside NewClusterWatchingController. This watch only
	// needs to fire on actual DeploymentStackDesire changes.
	if err := controller.QueueForChildInformers(0, deploymentStackDesireInformer); err != nil {
		panic(err) // coding error
	}
	controller.AddInterestingResource(azureresourcesapi.ClusterScopedDeploymentStackDesireResourceType, azureresourcesapi.RoleAssignmentsDeploymentStackDesireName)

	return controller
}

func (s *clusterRoleAssignmentsIntentSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
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

	desiredRoleAssignments, err := s.desiredRoleAssignments(
		existingCluster,
		existingServiceProviderCluster,
		existingServiceProviderCluster.Status.RoleAssignmentsOverManagedResourceGroup,
	)
	if err != nil {
		return err
	}

	replacement := existingServiceProviderCluster.DeepCopy()
	replacement.Status.RoleAssignmentsOverManagedResourceGroup = desiredRoleAssignments

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

// desiredRoleAssignments computes the role assignment map that should be
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
func (s *clusterRoleAssignmentsIntentSyncer) desiredRoleAssignments(
	cluster *coreapi.Cluster,
	serviceProviderCluster *coreapi.ServiceProviderCluster,
	existingRoleAssignments map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus,
) (map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus, error) {
	desiredIdentities, unresolvedResourceIDs, err := s.desiredRoleAssignmentIdentities(cluster, serviceProviderCluster)
	if err != nil {
		return nil, err
	}
	return s.mergeRoleAssignments(existingRoleAssignments, desiredIdentities, unresolvedResourceIDs)
}

// desiredRoleAssignmentIdentities enumerates the RoleAssignment keys that
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
func (s *clusterRoleAssignmentsIntentSyncer) desiredRoleAssignmentIdentities(
	cluster *coreapi.Cluster,
	serviceProviderCluster *coreapi.ServiceProviderCluster,
) (map[coreapi.RoleAssignmentKey]desiredRoleAssignmentIdentityTarget, map[string]struct{}, error) {
	desired := map[coreapi.RoleAssignmentKey]desiredRoleAssignmentIdentityTarget{}
	unresolvedResourceIDs := map[string]struct{}{}
	userAssignedIdentities := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities

	for operatorName, identityResourceID := range userAssignedIdentities.ControlPlaneOperators {
		if identityResourceID == nil {
			continue
		}
		target, ok, err := coreapihelpers.ResolveMSIBasedRoleAssignmentTargetIdentity(&serviceProviderCluster.Status, identityResourceID, s.managedIdentitiesDataPlaneServiceAvailable)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			unresolvedResourceIDs[strings.ToLower(identityResourceID.String())] = struct{}{}
			continue
		}
		roleDefinitionIDs, err := ControlPlaneOperatorRoleDefinitionIDsFromConfig(s.clusterScopedIdentitiesConfig, operatorName)
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

		target, ok, err := coreapihelpers.ResolveDataPlaneRoleAssignmentTargetIdentity(&serviceProviderCluster.Status, identityResourceID)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			unresolvedResourceIDs[strings.ToLower(identityResourceID.String())] = struct{}{}
			continue
		}
		roleDefinitionIDs, err := DataPlaneOperatorRoleDefinitionIDsFromConfig(s.clusterScopedIdentitiesConfig, operatorName)
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
	target, ok, err := coreapihelpers.ResolveMSIBasedRoleAssignmentTargetIdentity(&serviceProviderCluster.Status, identityResourceID, s.managedIdentitiesDataPlaneServiceAvailable)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		unresolvedResourceIDs[strings.ToLower(identityResourceID.String())] = struct{}{}
	} else {
		roleDefinitionIDs, err := ServiceManagedIdentityRoleDefinitionIDsFromConfig(s.clusterScopedIdentitiesConfig)
		if err != nil {
			return nil, nil, err
		}
		if err := addDesiredRoleAssignmentKeys(desired, identityResourceID, roleDefinitionIDs, target); err != nil {
			return nil, nil, err
		}
	}

	return desired, unresolvedResourceIDs, nil
}

// mergeRoleAssignments builds the next RoleAssignment map from existing
// statuses, currently desired keys, and identities that are not ready yet.
//
// Desired keys are kept or added. A missing key becomes a new status with
// TargetIdentity set. An existing key has DeconfigureTimestamp cleared and
// TargetIdentity replaced. Metadata changes on a still-desired key do not
// stamp deconfigure.
//
// Keys that left the desired set are handled in the second loop. A key whose
// TargetIdentity.ResourceID is still unresolved is copied unchanged so a
// missing source does not deconfigure. A key that already has
// DeconfigureTimestamp keeps that stamp until roleAssignmentDeconfigureDelay
// has elapsed, at which point it is dropped. Any other leftover key is
// stamped with DeconfigureTimestamp from the controller clock, which keeps it
// in the map (and therefore in the StackDesire template ClusterRoleAssignments
// publishes) for roleAssignmentDeconfigureDelay before it is dropped in a
// later sync.
func (s *clusterRoleAssignmentsIntentSyncer) mergeRoleAssignments(
	existing map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus,
	desiredIdentities map[coreapi.RoleAssignmentKey]desiredRoleAssignmentIdentityTarget,
	unresolvedResourceIDs map[string]struct{},
) (map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus, error) {
	// Sized for the first loop. The second loop may add leftover keys that
	// are still draining or unresolved.
	desiredRoleAssignments := make(map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus, len(desiredIdentities))
	deconfigureRequestedAt := metav1.NewTime(s.clock.Now())

	// First loop: keys that are currently desired. Missing keys become a new
	// status with TargetIdentity. Existing keys have DeconfigureTimestamp
	// cleared and TargetIdentity replaced. A resource path, ClientID, or
	// TenantID change on the same key does not deconfigure.
	for key, desiredIdentity := range desiredIdentities {
		existingStatus, hasExisting := existing[key]
		if hasExisting && existingStatus == nil {
			return nil, utils.TrackError(fmt.Errorf("RoleAssignmentOverManagedResourceGroup has a nil status for principal ID %s role definition resource ID %s", key.PrincipalID, key.RoleDefinitionResourceID))
		}
		// The map key is the lowercased identity; the publishing controller generates the
		// Azure role assignment name from the ORIGINAL casing, so record it from the desired
		// source (TargetIdentity.PrincipalID and the preserved role-definition casing).
		if !hasExisting {
			desiredRoleAssignments[key] = &coreapi.RoleAssignmentStatus{
				TargetIdentity:                        desiredIdentity.target.DeepCopy(),
				CasePreservedPrincipalID:              desiredIdentity.casePreservedPrincipalID,
				CasePreservedRoleDefinitionResourceID: desiredIdentity.casePreservedRoleDefinitionResourceID,
			}
			continue
		}

		status := existingStatus.DeepCopy()
		status.DeconfigureTimestamp = nil
		status.TargetIdentity = desiredIdentity.target.DeepCopy()
		status.CasePreservedPrincipalID = desiredIdentity.casePreservedPrincipalID
		status.CasePreservedRoleDefinitionResourceID = desiredIdentity.casePreservedRoleDefinitionResourceID
		desiredRoleAssignments[key] = status
	}

	// Second loop: keys the first loop did not keep as desired. Deconfigure
	// only when the identity has left the desired set. If the ResourceID is
	// still unresolved, the identity has not left; copy the existing status
	// so a missing source does not deconfigure.
	for key, existingStatus := range existing {
		if existingStatus == nil {
			return nil, utils.TrackError(fmt.Errorf("RoleAssignmentOverManagedResourceGroup has a nil status for principal ID %s role definition resource ID %s", key.PrincipalID, key.RoleDefinitionResourceID))
		}
		if _, stillDesired := desiredIdentities[key]; stillDesired {
			continue
		}
		if _, unresolved := unresolvedResourceIDs[strings.ToLower(existingStatus.TargetIdentity.ResourceID.String())]; unresolved {
			desiredRoleAssignments[key] = existingStatus.DeepCopy()
			continue
		}

		// Already stamped: keep the timestamp so the 24h wait is not reset,
		// unless it has elapsed, in which case the key is dropped.
		if existingStatus.DeconfigureTimestamp != nil {
			if !s.clock.Now().Before(existingStatus.DeconfigureTimestamp.Add(roleAssignmentDeconfigureDelay)) {
				continue
			}
			desiredRoleAssignments[key] = existingStatus.DeepCopy()
			continue
		}

		// Not yet stamped: request deconfigure.
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

// desiredRoleAssignmentIdentityTarget is an internal helper type for the intent controller
// only (clusterRoleAssignmentsIntentSyncer). It must not be used elsewhere. For a single
// desired key it bundles the resolved TargetIdentity plus the ORIGINAL casing of both key
// role assignment key parts.
//
// The map key is the lowercased, case-insensitive identity (built with
// coreapihelpers.NewRoleAssignmentKey), so the original casing - needed to generate the
// deterministic Azure role assignment name - is captured here at the exact point the key is
// built. Both parts are stored (rather than reading the principal back from
// TargetIdentity.PrincipalID) so the preserved casing is guaranteed to be exactly what the
// key was derived from, independent of how TargetIdentity is later populated.
//
// TODO: remove this type together with RoleAssignmentStatus.CasePreservedPrincipalID /
// CasePreservedRoleDefinitionResourceID (see their TODO). Once the role assignment name no
// longer has to match a Cluster-Service-generated name, the name can be generated from the
// lowercased key directly and there is nothing to preserve: drop the casePreserved* fields
// here, revert this map's value type back to *coreapi.RoleAssignmentTargetIdentity, and
// delete this type.
type desiredRoleAssignmentIdentityTarget struct {
	target                                *coreapi.RoleAssignmentTargetIdentity
	casePreservedPrincipalID              string
	casePreservedRoleDefinitionResourceID string
}

// addDesiredRoleAssignmentKeys inserts one RoleAssignment key per role
// definition for identityResourceID. The key is the lowercased PrincipalID and role
// definition; the original casing of both parts is preserved on the value. target
// already carries the lowercased identity resource ID. A nil role definition ID is an error.
func addDesiredRoleAssignmentKeys(
	desired map[coreapi.RoleAssignmentKey]desiredRoleAssignmentIdentityTarget,
	identityResourceID *azcorearm.ResourceID,
	roleDefinitionIDs []*azcorearm.ResourceID,
	target *coreapi.RoleAssignmentTargetIdentity,
) error {
	for _, roleDefinitionID := range roleDefinitionIDs {
		if roleDefinitionID == nil {
			return utils.TrackError(fmt.Errorf("unexpected nil role definition Resource ID for identity %s", identityResourceID))
		}
		key := coreapihelpers.NewRoleAssignmentKey(
			coreapi.RoleAssignmentKeyPartPrincipalID(target.PrincipalID),
			coreapi.RoleAssignmentKeyPartRoleDefinitionResourceID(roleDefinitionID.String()),
		)
		desired[key] = desiredRoleAssignmentIdentityTarget{
			target:                                target,
			casePreservedPrincipalID:              target.PrincipalID,
			casePreservedRoleDefinitionResourceID: roleDefinitionID.String(),
		}
	}
	return nil
}

// ControlPlaneOperatorRoleDefinitionIDsFromConfig returns the role definition
// resource IDs ClusterScopedIdentitiesConfig associates with the given
// control-plane operator. Exported so callers other than this controller
// (e.g. the create-operation completion check) can independently derive what
// role assignments are currently required, instead of inferring it from
// whatever this controller has already written.
func ControlPlaneOperatorRoleDefinitionIDsFromConfig(config *azure.ClusterScopedIdentitiesConfig, operatorName string) ([]*azcorearm.ResourceID, error) {
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

// DataPlaneOperatorRoleDefinitionIDsFromConfig returns the role definition
// resource IDs ClusterScopedIdentitiesConfig associates with the given
// data-plane operator. See ControlPlaneOperatorRoleDefinitionIDsFromConfig
// for why this is exported.
func DataPlaneOperatorRoleDefinitionIDsFromConfig(config *azure.ClusterScopedIdentitiesConfig, operatorName string) ([]*azcorearm.ResourceID, error) {
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

// ServiceManagedIdentityRoleDefinitionIDsFromConfig returns the role
// definition resource IDs ClusterScopedIdentitiesConfig associates with the
// service managed identity. See
// ControlPlaneOperatorRoleDefinitionIDsFromConfig for why this is exported.
func ServiceManagedIdentityRoleDefinitionIDsFromConfig(config *azure.ClusterScopedIdentitiesConfig) ([]*azcorearm.ResourceID, error) {
	if config.ServiceManagedIdentity == nil {
		return nil, utils.TrackError(fmt.Errorf("no service managed identity configuration"))
	}
	roleDefinitionIDs := config.ServiceManagedIdentity.RoleDefinitionsResourceIDs()
	if len(roleDefinitionIDs) == 0 {
		return nil, utils.TrackError(fmt.Errorf("no role definitions configured for the service managed identity"))
	}
	return roleDefinitionIDs, nil
}
