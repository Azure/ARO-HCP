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
	"slices"
	"strings"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/azure/roleassignment"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/azure"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// RoleAssignmentsControllerName is the single source of truth for this
// controller's name. It is used for the workqueue name (a Prometheus label),
// context/logger controller name, and log fields.
const RoleAssignmentsControllerName = "IdentityRoleAssignments"

// roleAssignmentsSyncer MANAGES the Azure role assignments on a cluster's managed
// resource group (MRG) for each control-plane and data-plane operator managed identity:
// it creates the ones that do not exist yet and reflects their existence onto
// ServiceProviderCluster.Status.AzureResources.RoleAssignments.
//
// Cluster Service creates the same deterministically-named role assignments in parallel,
// so the two actors are idempotent with respect to each other: a create that races with
// Cluster Service surfaces as "already exists" and is treated as success. Reflecting the
// confirmed state onto the ServiceProviderCluster lets DB-consuming code (for example the
// cluster-create completion gate) reason about the role assignments without reaching into
// Azure directly.
//
// Deletion is a deliberate no-op: when the cluster is deleted Cluster Service deletes the
// managed resource group, and that cascade removes the MRG-scoped role assignments with it.
// There is therefore nothing to create or delete (and no Azure call to make) on the delete
// path.
type roleAssignmentsSyncer struct {
	resourcesDBClient             corecosmosstorage.ResourcesDBClient
	clusterLister                 corelisters.ClusterLister
	serviceProviderClusterLister  corelisters.ServiceProviderClusterLister
	subscriptionLister            corelisters.SubscriptionLister
	azureFPAClientBuilder         azureclient.FirstPartyApplicationClientBuilder
	clusterScopedIdentitiesConfig *azure.ClusterScopedIdentitiesConfig
}

var _ controllerutils.ClusterSyncer = (*roleAssignmentsSyncer)(nil)

// NewRoleAssignmentsController creates a cluster-watching controller that manages the
// managed-resource-group-scoped role assignments for the cluster's control-plane and
// data-plane operator managed identities: it creates the ones Azure reports missing and
// keeps ServiceProviderCluster.Status.AzureResources.RoleAssignments in sync with their
// confirmed existence. Cluster Service creates the same assignments in parallel.
func NewRoleAssignmentsController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister,
	subscriptionLister corelisters.SubscriptionLister,
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder,
	clusterScopedIdentitiesConfig *azure.ClusterScopedIdentitiesConfig,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()

	syncer := &roleAssignmentsSyncer{
		resourcesDBClient:             resourcesDBClient,
		clusterLister:                 clusterLister,
		serviceProviderClusterLister:  serviceProviderClusterLister,
		subscriptionLister:            subscriptionLister,
		azureFPAClientBuilder:         azureFPAClientBuilder,
		clusterScopedIdentitiesConfig: clusterScopedIdentitiesConfig,
	}

	return controllerutils.NewClusterWatchingController(
		RoleAssignmentsControllerName,
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		5*time.Minute,
		syncer,
	)
}

// NeedsWork reports whether SyncOnce has anything to do for a cluster that is not
// being deleted.
//
// There is work only until every expected role assignment has been confirmed
// (moved to AzureResources) and nothing is left pending. Once the managed resource
// group is confirmed, every expected role assignment is confirmed, and the pending
// list is empty, steady-state resyncs skip all Azure calls.
//
// The deletion path is handled entirely in SyncOnce (a genuine no-op), so NeedsWork
// only reasons about the non-deletion case.
func (c *roleAssignmentsSyncer) NeedsWork(cluster *coreapi.HCPOpenShiftCluster, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	// Gate: only manage role assignments once the managed resource group they are
	// scoped to has been confirmed to exist. Until then there is no scope to build
	// their resource IDs against, so there is nothing to do.
	if serviceProviderCluster.Status.AzureResources.ManagedResourceGroup.AzureResource == nil {
		return false
	}

	// Gate: only do work once the principal IDs for every expected control-plane AND
	// data-plane operator identity are resolved. Until then we could only compute a
	// partial expected set, so skip entirely rather than persist partial pending state;
	// the identity-resolution controllers re-enqueue us once the principal IDs are
	// populated.
	if !c.principalIDsResolvable(cluster, serviceProviderCluster) {
		return false
	}

	expected, err := c.expectedRoleAssignments(cluster, serviceProviderCluster)
	if err != nil {
		// Principal IDs are resolvable (checked above), so any error here is a genuine
		// configuration problem (for example an operator with no role definitions).
		// Treat that as "work to do" so SyncOnce runs and surfaces the error for a retry.
		return true
	}

	roleAssignments := serviceProviderCluster.Status.AzureResources.RoleAssignments
	if len(roleAssignments.PendingAzureResources) != 0 {
		return true
	}
	if len(expected) != len(roleAssignments.AzureResources) {
		return true
	}
	for _, assignment := range expected {
		if !slices.ContainsFunc(roleAssignments.AzureResources, func(id *azcorearm.ResourceID) bool {
			return controllerutil.ResourceIDsEqual(id, assignment.resourceID)
		}) {
			return true
		}
	}
	return false
}

// principalIDsResolvable reports whether every control-plane and data-plane operator
// identity configured on the cluster has a resolved (present, non-empty) principal ID
// on the ServiceProviderCluster status. It is the NeedsWork gate that ensures the
// controller only ever computes and persists the full expected role assignment set,
// never a partial one.
func (c *roleAssignmentsSyncer) principalIDsResolvable(cluster *coreapi.HCPOpenShiftCluster, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	userAssignedIdentities := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities
	for _, identityResourceID := range userAssignedIdentities.ControlPlaneOperators {
		if _, ok := controlPlaneOperatorPrincipalID(serviceProviderCluster, identityResourceID); !ok {
			return false
		}
	}
	for _, identityResourceID := range userAssignedIdentities.DataPlaneOperators {
		if _, ok := dataPlaneOperatorPrincipalID(serviceProviderCluster, identityResourceID); !ok {
			return false
		}
	}
	return true
}

// SyncOnce reads the cluster and ServiceProviderCluster from the informer caches,
// short-circuits deletion (a no-op) and the NeedsWork gate, and then reconciles the
// role assignment state, creating any that Azure reports missing. This controller never
// deletes a role assignment (the managed resource group deletion cascade handles that).
func (c *roleAssignmentsSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	// Deletion is a genuine no-op: the managed resource group deletion cascades and
	// removes the role assignments scoped to it. We do not gate deletion and we make
	// no Azure calls on the delete path.
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}

	existingServiceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		// CreateServiceProviderCluster will populate it; we'll be re-enqueued via the
		// ServiceProviderCluster informer.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	// Short-circuit on the cheap lister reads before doing anything fallible.
	if !c.NeedsWork(cluster, existingServiceProviderCluster) {
		return nil
	}

	return c.syncRoleAssignments(ctx, cluster, existingServiceProviderCluster)
}

// syncRoleAssignments manages the role assignments for a cluster that is not being
// deleted and reflects their state onto the ServiceProviderCluster.
//
// It first records every not-yet-tracked expected role assignment as
// PendingAzureResources and persists that intent BEFORE any Azure write, so that a
// failure - or a role assignment that does not exist yet - still leaves a durable pending
// marker rather than an empty reference. It then queries Azure for each pending role
// assignment and:
//
//   - exists: moves it from pending to confirmed (AzureResources).
//   - not found: creates it (mirroring Cluster Service's parameters) and, on success or
//     an "already exists" race with Cluster Service, moves it to confirmed.
//   - other error (on Get or Create): returns the error so the sync retries, leaving the
//     assignment pending.
func (c *roleAssignmentsSyncer) syncRoleAssignments(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster, existingServiceProviderCluster *coreapi.ServiceProviderCluster) error {
	expected, err := c.expectedRoleAssignments(cluster, existingServiceProviderCluster)
	if err != nil {
		return utils.TrackError(err)
	}
	if len(expected) == 0 {
		// A real cluster always has control-plane operators, so this should not
		// happen; there is simply nothing to manage.
		return nil
	}

	// Phase 1: record every not-yet-tracked expected role assignment as pending and
	// persist that intent BEFORE querying Azure ("set pending before Get").
	existingRoleAssignments := existingServiceProviderCluster.Status.AzureResources.RoleAssignments
	var toAdd []*azcorearm.ResourceID
	for _, assignment := range expected {
		if slices.ContainsFunc(existingRoleAssignments.AzureResources, func(id *azcorearm.ResourceID) bool {
			return controllerutil.ResourceIDsEqual(id, assignment.resourceID)
		}) {
			continue
		}
		if slices.ContainsFunc(existingRoleAssignments.PendingAzureResources, func(id *azcorearm.ResourceID) bool {
			return controllerutil.ResourceIDsEqual(id, assignment.resourceID)
		}) {
			continue
		}
		toAdd = append(toAdd, assignment.resourceID)
	}
	if len(toAdd) != 0 {
		replacement := existingServiceProviderCluster.DeepCopy()
		replacement.Status.AzureResources.RoleAssignments.PendingAzureResources = append(
			replacement.Status.AzureResources.RoleAssignments.PendingAzureResources, toAdd...)
		existingServiceProviderCluster, err = c.persistIfChanged(ctx, cluster, existingServiceProviderCluster, replacement)
		if err != nil {
			return utils.TrackError(err)
		}
	}

	// Phase 2: for each pending role assignment, ensure it exists in Azure - creating it
	// when Azure reports it missing - and promote it to confirmed. The create parameters
	// are looked up by resource ID: the hashed role-assignment name cannot be reversed, so
	// the expected set (which carries scope/name/principalID/roleDefinitionID) is keyed by
	// its resource ID.
	pending := existingServiceProviderCluster.Status.AzureResources.RoleAssignments.PendingAzureResources
	if len(pending) == 0 {
		return nil
	}

	expectedByID := make(map[string]expectedRoleAssignment, len(expected))
	for _, assignment := range expected {
		expectedByID[strings.ToLower(assignment.resourceID.String())] = assignment
	}

	roleAssignmentsClient, err := c.roleAssignmentsClient(ctx, cluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(err)
	}

	var stillPending []*azcorearm.ResourceID
	var newlyConfirmed []*azcorearm.ResourceID
	for _, pendingID := range pending {
		_, getErr := roleAssignmentsClient.GetByID(ctx, pendingID.String(), nil)
		switch {
		case getErr == nil:
			// The role assignment already exists (created on a previous pass or by
			// Cluster Service): promote it to confirmed.
			newlyConfirmed = append(newlyConfirmed, pendingID)
			continue
		case !azureclient.IsRoleAssignmentNotFoundErr(getErr):
			return utils.TrackError(fmt.Errorf("failed to get role assignment %q: %w", pendingID.String(), getErr))
		}

		// The role assignment does not exist yet: create it.
		assignment, ok := expectedByID[strings.ToLower(pendingID.String())]
		if !ok {
			// A pending entry that is no longer expected (for example an operator removed
			// after it was recorded pending). Its create parameters cannot be
			// reconstructed, so leave it pending; it settles once expectations stabilize.
			stillPending = append(stillPending, pendingID)
			continue
		}
		if createErr := c.createRoleAssignment(ctx, roleAssignmentsClient, cluster, assignment); createErr != nil {
			return utils.TrackError(createErr)
		}
		newlyConfirmed = append(newlyConfirmed, pendingID)
	}

	if len(newlyConfirmed) == 0 {
		// Nothing was confirmed this pass; leave the document unchanged.
		return nil
	}

	replacement := existingServiceProviderCluster.DeepCopy()
	replacement.Status.AzureResources.RoleAssignments.PendingAzureResources = stillPending
	replacement.Status.AzureResources.RoleAssignments.AzureResources = append(
		replacement.Status.AzureResources.RoleAssignments.AzureResources, newlyConfirmed...)
	_, err = c.persistIfChanged(ctx, cluster, existingServiceProviderCluster, replacement)
	return utils.TrackError(err)
}

// expectedRoleAssignment is a single managed-resource-group-scoped role assignment the
// backend expects to exist for the cluster. It carries both what is needed to recognize
// the assignment (resourceID) and what is needed to create it with the same deterministic
// identity Cluster Service uses (scope, name, principalID, roleDefinitionID) - the hashed
// name cannot be reversed from the resource ID, so these inputs are retained.
type expectedRoleAssignment struct {
	resourceID       *azcorearm.ResourceID
	scope            string
	name             string
	principalID      string
	roleDefinitionID string
}

// expectedRoleAssignments computes the managed-resource-group-scoped role assignments the
// backend expects to exist for the cluster's control-plane and data-plane operator managed
// identities. Cluster Service creates the same assignments in parallel.
//
// For each control-plane and data-plane operator configured on the cluster it pairs
// the operator identity's resolved principal ID (read from the ServiceProviderCluster
// status) with each of that operator's role definitions (read from the cluster-scoped
// identities config) and derives the deterministic role assignment name and resource ID
// using the same algorithm Cluster Service uses (see the roleassignment package).
//
// Enumerating from the cluster's actual operators (rather than the whole config)
// naturally excludes operators that are not provisioned on this cluster (for example
// on-enablement operators whose feature is disabled). The service managed identity
// is intentionally excluded: its role assignments are not scoped to the managed
// resource group.
//
// A missing or unresolved required input (empty managed resource group name,
// unresolved principal ID, or an operator with no configured role definitions)
// returns an error so the caller retries rather than computing an incomplete set.
func (c *roleAssignmentsSyncer) expectedRoleAssignments(cluster *coreapi.HCPOpenShiftCluster, serviceProviderCluster *coreapi.ServiceProviderCluster) ([]expectedRoleAssignment, error) {
	managedResourceGroupName := cluster.CustomerProperties.Platform.ManagedResourceGroup
	if len(managedResourceGroupName) == 0 {
		return nil, fmt.Errorf("managed resource group name is empty for cluster %q", cluster.ID.String())
	}
	scopeID, err := coreapi.ToResourceGroupResourceID(cluster.ID.SubscriptionID, managedResourceGroupName)
	if err != nil {
		return nil, fmt.Errorf("failed to build managed resource group scope for cluster %q: %w", cluster.ID.String(), err)
	}

	userAssignedIdentities := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities

	var expected []expectedRoleAssignment

	// Control-plane operators.
	for operatorName, identityResourceID := range userAssignedIdentities.ControlPlaneOperators {
		principalID, ok := controlPlaneOperatorPrincipalID(serviceProviderCluster, identityResourceID)
		if !ok {
			return nil, fmt.Errorf("principal ID not yet resolved for control plane operator %q (identity %q)", operatorName, identityResourceID.String())
		}
		roleDefinitionIDs, err := c.controlPlaneOperatorRoleDefinitionIDs(operatorName)
		if err != nil {
			return nil, err
		}
		expected, err = appendRoleAssignments(expected, scopeID.String(), principalID, roleDefinitionIDs)
		if err != nil {
			return nil, err
		}
	}

	// Data-plane operators.
	for operatorName, identityResourceID := range userAssignedIdentities.DataPlaneOperators {
		principalID, ok := dataPlaneOperatorPrincipalID(serviceProviderCluster, identityResourceID)
		if !ok {
			return nil, fmt.Errorf("principal ID not yet resolved for data plane operator %q (identity %q)", operatorName, identityResourceID.String())
		}
		roleDefinitionIDs, err := c.dataPlaneOperatorRoleDefinitionIDs(operatorName)
		if err != nil {
			return nil, err
		}
		expected, err = appendRoleAssignments(expected, scopeID.String(), principalID, roleDefinitionIDs)
		if err != nil {
			return nil, err
		}
	}

	return expected, nil
}

// controlPlaneOperatorPrincipalID returns the resolved Azure principal ID for a
// control-plane operator identity, looked up on the ServiceProviderCluster status by
// the lowercased identity resource ID. The bool is false when the principal ID has not
// been resolved yet. The identity resource ID is always set on a cluster's operators,
// so it is not nil-guarded.
func controlPlaneOperatorPrincipalID(serviceProviderCluster *coreapi.ServiceProviderCluster, identityResourceID *azcorearm.ResourceID) (string, bool) {
	key := strings.ToLower(identityResourceID.String())
	identity, ok := serviceProviderCluster.Status.MSIManagedIdentities.ControlPlaneOperatorsIdentities[key]
	if !ok || identity == nil || identity.PrincipalID == nil || len(*identity.PrincipalID) == 0 {
		return "", false
	}
	return *identity.PrincipalID, true
}

// dataPlaneOperatorPrincipalID returns the resolved Azure principal ID for a
// data-plane operator identity, looked up on the ServiceProviderCluster status by
// the lowercased identity resource ID. The bool is false when the principal ID has not
// been resolved yet. The identity resource ID is always set on a cluster's operators,
// so it is not nil-guarded.
func dataPlaneOperatorPrincipalID(serviceProviderCluster *coreapi.ServiceProviderCluster, identityResourceID *azcorearm.ResourceID) (string, bool) {
	key := strings.ToLower(identityResourceID.String())
	identity, ok := serviceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.Identities[key]
	if !ok || identity == nil || identity.PrincipalID == nil || len(*identity.PrincipalID) == 0 {
		return "", false
	}
	return *identity.PrincipalID, true
}

// controlPlaneOperatorRoleDefinitionIDs returns the role definition resource IDs
// configured for a control-plane operator. It returns an error for an unknown
// operator or one with no configured role definitions.
func (c *roleAssignmentsSyncer) controlPlaneOperatorRoleDefinitionIDs(operatorName string) ([]*azcorearm.ResourceID, error) {
	operatorIdentity, ok := c.clusterScopedIdentitiesConfig.ControlPlaneOperatorsIdentities[azure.ClusterOperatorIdentifier(operatorName)]
	if !ok || operatorIdentity == nil {
		return nil, fmt.Errorf("no control plane operator identity configuration for operator %q", operatorName)
	}
	roleDefinitionIDs := operatorIdentity.RoleDefinitionsResourceIDs()
	if len(roleDefinitionIDs) == 0 {
		return nil, fmt.Errorf("no role definitions configured for control plane operator %q", operatorName)
	}
	return roleDefinitionIDs, nil
}

// dataPlaneOperatorRoleDefinitionIDs returns the role definition resource IDs
// configured for a data-plane operator. It returns an error for an unknown operator
// or one with no configured role definitions.
func (c *roleAssignmentsSyncer) dataPlaneOperatorRoleDefinitionIDs(operatorName string) ([]*azcorearm.ResourceID, error) {
	operatorIdentity, ok := c.clusterScopedIdentitiesConfig.DataPlaneOperatorsIdentities[azure.ClusterOperatorIdentifier(operatorName)]
	if !ok || operatorIdentity == nil {
		return nil, fmt.Errorf("no data plane operator identity configuration for operator %q", operatorName)
	}
	roleDefinitionIDs := operatorIdentity.RoleDefinitionsResourceIDs()
	if len(roleDefinitionIDs) == 0 {
		return nil, fmt.Errorf("no role definitions configured for data plane operator %q", operatorName)
	}
	return roleDefinitionIDs, nil
}

// appendRoleAssignments derives the deterministic role assignment name and resource ID for
// principalID paired with each role definition at scope and appends the resulting expected
// assignments to expected, skipping duplicates (a shared identity + role definition would
// otherwise produce the same deterministic ID twice).
func appendRoleAssignments(expected []expectedRoleAssignment, scope, principalID string, roleDefinitionIDs []*azcorearm.ResourceID) ([]expectedRoleAssignment, error) {
	for _, roleDefinitionID := range roleDefinitionIDs {
		if roleDefinitionID == nil {
			continue
		}
		roleDefID := roleDefinitionID.String()
		name := roleassignment.GenerateManagedResourceGroupScopedRoleAssignmentName(scope, principalID, roleDefID)
		fullID := roleassignment.ManagedResourceGroupScopedRoleAssignmentResourceID(scope, principalID, roleDefID)
		parsed, err := azcorearm.ParseResourceID(fullID)
		if err != nil {
			return nil, fmt.Errorf("failed to parse role assignment resource ID %q: %w", fullID, err)
		}
		if slices.ContainsFunc(expected, func(e expectedRoleAssignment) bool {
			return controllerutil.ResourceIDsEqual(e.resourceID, parsed)
		}) {
			continue
		}
		expected = append(expected, expectedRoleAssignment{
			resourceID:       parsed,
			scope:            scope,
			name:             name,
			principalID:      principalID,
			roleDefinitionID: roleDefID,
		})
	}
	return expected, nil
}

// persistIfChanged replaces the ServiceProviderCluster when replacement differs from
// existing and returns the object to use for any subsequent write (the freshly
// persisted document on success, or existing when nothing changed). A Cosmos
// precondition conflict is treated as success (another writer updated the document
// first; we'll be re-enqueued and retry).
func (c *roleAssignmentsSyncer) persistIfChanged(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster, existing, replacement *coreapi.ServiceProviderCluster) (*coreapi.ServiceProviderCluster, error) {
	if !controllerutil.NeedsUpdate(existing, replacement) {
		return existing, nil
	}

	logger := utils.LoggerFromContext(ctx)
	roleAssignments := replacement.Status.AzureResources.RoleAssignments
	logger.Info("reflecting role assignment state onto ServiceProviderCluster",
		"clusterID", cluster.ID.String(),
		"confirmedRoleAssignments", resourceIDStrings(roleAssignments.AzureResources),
		"pendingRoleAssignments", resourceIDStrings(roleAssignments.PendingAzureResources))

	updated, err := c.resourcesDBClient.ServiceProviderClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name).Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return existing, nil
	}
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}
	return updated, nil
}

// roleAssignmentsClient builds an FPA-credentialed Azure RoleAssignments client for
// the given subscription, resolving the tenant ID from the subscription document.
func (c *roleAssignmentsSyncer) roleAssignmentsClient(ctx context.Context, subscriptionID string) (azureclient.RoleAssignmentsClient, error) {
	subscription, err := c.subscriptionLister.Get(ctx, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get subscription %q: %w", subscriptionID, err)
	}
	if subscription.Properties == nil || subscription.Properties.TenantId == nil {
		return nil, fmt.Errorf("subscription %q has no tenant ID", subscriptionID)
	}

	client, err := c.azureFPAClientBuilder.RoleAssignmentsClient(*subscription.Properties.TenantId, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("failed to build role assignments client: %w", err)
	}
	return client, nil
}

// createRoleAssignment creates the managed-resource-group-scoped role assignment described
// by assignment, mirroring the parameters Cluster Service uses (PrincipalID,
// RoleDefinitionID, PrincipalType=ServicePrincipal) at the deterministic name. Because
// Cluster Service creates the same deterministically-named assignment in parallel, a create
// that races with it - or a retried create - surfaces as RoleAssignmentExists, which is
// treated as success.
func (c *roleAssignmentsSyncer) createRoleAssignment(ctx context.Context, client azureclient.RoleAssignmentsClient, cluster *coreapi.HCPOpenShiftCluster, assignment expectedRoleAssignment) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("creating role assignment",
		"clusterID", cluster.ID.String(),
		"roleAssignmentID", assignment.resourceID.String())

	_, err := client.Create(ctx, assignment.scope, assignment.name, armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			PrincipalID:      to.Ptr(assignment.principalID),
			RoleDefinitionID: to.Ptr(assignment.roleDefinitionID),
			PrincipalType:    to.Ptr(armauthorization.PrincipalTypeServicePrincipal),
		},
	}, nil)
	if err != nil && !azureclient.IsRoleAssignmentAlreadyExistsErr(err) {
		return fmt.Errorf("failed to create role assignment %q: %w", assignment.resourceID.String(), err)
	}
	return nil
}

// resourceIDStrings renders resource IDs for structured logging.
func resourceIDStrings(ids []*azcorearm.ResourceID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}
