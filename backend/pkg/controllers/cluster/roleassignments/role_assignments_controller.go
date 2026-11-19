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
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks/v2"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/roleassignment"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/azureresourcesapihelpers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/azureresourcescosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/azureresourcesinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/azureresourceslisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ClusterRoleAssignmentsControllerName is the single source of truth for this
// controller's name. It is used for the workqueue name (a Prometheus label),
// context/logger controller name, and log fields.
const ClusterRoleAssignmentsControllerName = "ClusterRoleAssignments"

const (
	// RoleAssignmentsDeploymentStackDesireName identifies the cluster's complete role
	// assignment intent as a StackDesire.
	RoleAssignmentsDeploymentStackDesireName = "role-assignments-over-mrg"
	// roleAssignmentsAzureDeploymentStackName is the deployment stack name, stable within
	// each cluster's managed resource group.
	roleAssignmentsAzureDeploymentStackName = "role-assignments-over-mrg"
	// roleAssignmentAPIVersion is the ARM API version embedded in each
	// Microsoft.Authorization/roleAssignments template resource. It matches
	// the version armauthorization/v2 uses for direct role assignment calls
	// elsewhere in this repo.
	roleAssignmentAPIVersion = "2022-04-01"
)

// clusterRoleAssignmentsSyncer publishes the role assignments the cluster
// currently needs over its managed resource group as a single StackDesire, so
// the stack desires controller can apply or delete the whole set in Azure
// through one deployment stack.
//
// It does not call Azure itself. The only Azure-shape decision it makes is
// reserving a role assignment resource ID for a key that does not have one
// yet (RoleAssignmentStatus.ReservedAzureResourceID), which it persists before the ID
// is ever used so a later naming-strategy change cannot orphan or duplicate
// an already-reserved assignment. Everything else — applying the stack,
// deleting resources that drop out of the template, retrying failures — is
// the stack desires controller's job.
//
// Cluster deletion and cluster-not-found are no-ops beyond removing the
// bookkeeping StackDesire document: role assignments are scoped to the
// managed resource group, so Azure deletes them in cascade when that
// resource group is removed. A live cluster whose desired set becomes empty
// is different — nothing else will tear down an already-applied deployment
// stack, so this controller asks for it to be deleted instead of abandoning
// the StackDesire document.
type clusterRoleAssignmentsSyncer struct {
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	subscriptionLister           corelisters.SubscriptionLister
	deploymentStackDesireLister  azureresourceslisters.DeploymentStackDesireLister
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	azureResourcesDBClient       azureresourcescosmosstorage.AzureResourcesDBClient
}

var _ controllerutils.ClusterSyncer = (*clusterRoleAssignmentsSyncer)(nil)

// NewClusterRoleAssignmentsController creates a cluster-watching controller
// that publishes ServiceProviderCluster.Status.RoleAssignmentsOverManagedResourceGroup
// as a StackDesire for the stack desires controller to reconcile against Azure.
func NewClusterRoleAssignmentsController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	azureResourcesDBClient azureresourcescosmosstorage.AzureResourcesDBClient,
	backendInformers coreinformers.BackendInformers,
	azureResourcesInformers azureresourcesinformers.AzureResourcesInformers,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()
	_, subscriptionLister := backendInformers.Subscriptions()
	deploymentStackDesireInformer, deploymentStackDesireLister := azureResourcesInformers.DeploymentStackDesires()

	syncer := &clusterRoleAssignmentsSyncer{
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		subscriptionLister:           subscriptionLister,
		deploymentStackDesireLister:  deploymentStackDesireLister,
		resourcesDBClient:            resourcesDBClient,
	}

	controller := controllerutils.NewClusterWatchingController(
		ClusterRoleAssignmentsControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		1*time.Minute,
		syncer,
	)
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)
	controller.AddInterestingResource(azcorearm.SubscriptionResourceType, "*")

	// AddInterestingResource only filters events, it does not subscribe to them.
	// Wire the DeploymentStackDesire informer as an actual event source so that
	// the stack desires controller writing an observed Azure status back onto the
	// desire re-triggers this controller, which consumes that status to advance its
	// stamp/drop state machine. Resync is 0: the periodic resync is done once on the
	// cluster (key) resource inside NewClusterWatchingController. This watch only
	// needs to fire on actual DeploymentStackDesire changes.
	if err := controller.QueueForChildInformers(0, deploymentStackDesireInformer); err != nil {
		panic(err)
	}
	controller.AddInterestingResource(azureresourcesapi.ClusterScopedDeploymentStackDesireResourceType, azureresourcesapi.RoleAssignmentsDeploymentStackDesireName)

	return controller
}

func (s *clusterRoleAssignmentsSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
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
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	// Role assignments are scoped to the managed resource group, so Azure
	// deletes them in cascade when that resource group is removed during
	// cluster teardown. Only the bookkeeping DeploymentStackDesire document needs
	// cleaning up here.
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		err := s.deleteDeploymentStackDesireDatabaseEntry(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to delete role assignments DeploymentStackDesire from the database: %w", err)
		}
		return nil
	}

	// We only publish role assignments when the managed resource group is confirmed as owned by this cluster.
	scope := confirmedManagedResourceGroupID(existingServiceProviderCluster.Status.AzureResources.ManagedResourceGroup)
	if scope == nil {
		// Role assignment names and the stack target are both scoped to the managed
		// resource group. Nothing can be published yet.
		return nil
	}

	// Publishing an empty template rather than deleting the whole stack keeps the
	// stack (which lives in and cascades away with the managed resource group anyway) and
	// avoids create/delete churn if a key is later re-added.

	// Reserve the role assignment resource IDs onto the ServiceProviderCluster as a first step. This ensures
	// that the role assignments references are not lost if the DeploymentStackDesire is not published yet. In the future
	// the references will be generated randomly and non deterministically.
	desiredServiceProviderCluster, err := s.serviceProviderClusterWithReservedAzureResourceIDs(existingServiceProviderCluster, scope)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to reserve role assignment resource IDs: %w", err))
	}
	if controllerutil.NeedsUpdate(existingServiceProviderCluster, desiredServiceProviderCluster) {
		logger.Info("reserving role assignment resource IDs onto ServiceProviderCluster")
		persisted, err := s.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, desiredServiceProviderCluster, nil)
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			return nil
		}
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
		}
		desiredServiceProviderCluster = persisted
	}

	// The role assignments stack is applied as the cluster's First Party Application, which
	// needs the tenant the FPA authenticates in. Resolve it from the subscription document.
	subscription, err := s.subscriptionLister.Get(ctx, key.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get subscription %q: %w", key.SubscriptionID, err))
	}
	if subscription.Properties == nil || subscription.Properties.TenantId == nil {
		return utils.TrackError(fmt.Errorf("subscription %q has no tenant ID", key.SubscriptionID))
	}

	desiredSpec, err := s.desiredDeploymentStackDesireSpec(desiredServiceProviderCluster, scope, *subscription.Properties.TenantId)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build desired role assignments DeploymentStackDesire spec: %w", err))
	}

	err = s.publishDeploymentStackDesire(ctx, key, desiredSpec)
	if err != nil {
		return fmt.Errorf("failed to publish role assignments DeploymentStackDesire: %w", err)
	}

	return nil
}

// confirmedManagedResourceGroupID returns the resource ID of the managed resource group
// once it has been confirmed as owned by this cluster, or nil otherwise. It is the single
// definition of "which reference the mock FPA may act on", shared by the publishing
// controller (which needs the ID) and the intent controller (which needs its presence).
//
// It uses only the confirmed AzureResource, never the PendingAzureResource marker. The
// managed resource group controller persists the pending marker BEFORE it queries Azure
// and validates ownership, so a pending-only reference may point at a resource group that
// is owned by another cluster. Publishing an Owner grant against such a group would be
// orphaned: this cluster's teardown never deletes a foreign group, so the MRG-scoped
// assignment never cascades away, and teardown only deletes the bookkeeping desire.
// AzureResource is set only after the managed resource group controller has validated
// ManagedBy == this cluster - including the promotion it performs on the deletion path for
// a managed resource group whose creation was confirmed in Azure but never persisted - so
// gating on it means we only ever publish against a group we own, while still covering the
// pending-only-at-delete-time case.
func confirmedManagedResourceGroupID(ref coreapi.AzureReference) *azcorearm.ResourceID {
	return ref.AzureResource
}

// serviceProviderClusterWithReservedAzureResourceIDs returns a copy of existing with a
// ReservedAzureResourceID assigned to every non-draining role assignment key that does not
// have one yet. Draining keys keep whatever reserved ID they already had; naming is a
// one-time reservation, never recomputed.
//
// It is pure (no I/O): the caller persists the result, so the reserved ID is recorded
// durably before it is rendered into the deployment stack. An error is returned only for
// an internal failure building the deterministic resource ID.
func (s *clusterRoleAssignmentsSyncer) serviceProviderClusterWithReservedAzureResourceIDs(existing *coreapi.ServiceProviderCluster, scope *azcorearm.ResourceID) (*coreapi.ServiceProviderCluster, error) {
	replacement := existing.DeepCopy()

	keys := make([]coreapi.RoleAssignmentKey, 0, len(replacement.Status.RoleAssignmentsOverManagedResourceGroup))
	for key := range replacement.Status.RoleAssignmentsOverManagedResourceGroup {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, compareRoleAssignmentKeyByPrincipalAndResourceID)

	for _, key := range keys {
		status := replacement.Status.RoleAssignmentsOverManagedResourceGroup[key]
		if status.DeconfigureTimestamp != nil {
			continue
		}

		// If the resource id has been reserved we don't recalculate it. The reason for this is that Azure
		// does not allow setting two role assignments with the same principal, scope and role so if one already exists that
		// is originally set using that information we must keep it as otherwise if the name generation logic changes we would end up
		// with two role assignments with the same principal, scope and role.
		if status.ReservedAzureResourceID != nil {
			continue
		}

		// The map key is the lowercased identity; the Azure role assignment name must be
		// generated from the ORIGINAL casing (preserved on the status) so the name stays stable.
		if status.CasePreservedPrincipalID == "" || status.CasePreservedRoleDefinitionResourceID == "" {
			return nil, utils.TrackError(fmt.Errorf("role assignment %+v has no case-preserved principal/role definition casing", key))
		}
		reservedID, err := azcorearm.ParseResourceID(roleassignment.ManagedResourceGroupScopedRoleAssignmentResourceID(scope.String(), status.CasePreservedPrincipalID, status.CasePreservedRoleDefinitionResourceID))
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to parse reserved role assignment resource ID: %w", err))
		}
		status.ReservedAzureResourceID = reservedID
	}

	return replacement, nil
}

// desiredDeploymentStackDesireSpec builds the StackDesire spec that applies every
// non-draining key as one ARM template, authenticated as the ARM permissions
// manager identity.
func (s *clusterRoleAssignmentsSyncer) desiredDeploymentStackDesireSpec(
	serviceProviderCluster *coreapi.ServiceProviderCluster,
	scope *azcorearm.ResourceID,
	tenantID string,
) (azureresourcesapi.DeploymentStackDesireSpec, error) {
	target, err := azcorearm.ParseResourceID(scope.String() + "/providers/Microsoft.Resources/deploymentStacks/" + roleAssignmentsAzureDeploymentStackName)
	if err != nil {
		return azureresourcesapi.DeploymentStackDesireSpec{}, utils.TrackError(fmt.Errorf("failed to parse role assignments stack resource ID: %w", err))
	}

	resources, err := roleAssignmentTemplateResources(serviceProviderCluster.Status.RoleAssignmentsOverManagedResourceGroup)
	if err != nil {
		return azureresourcesapi.DeploymentStackDesireSpec{}, utils.TrackError(fmt.Errorf("failed to build role assignment template resources: %w", err))
	}

	return azureresourcesapi.DeploymentStackDesireSpec{
		TargetResourceID: target,
		Authentication: azureresourcesapi.DeploymentStackDesireAuthentication{
			Type: azureresourcesapi.DeploymentStackDesireAuthenticationTypeFPA,
			FirstPartyApplication: &azureresourcesapi.DeploymentStackDesireAuthenticationFirstPartyApplication{
				TenantID: tenantID,
			},
		},
		Action: azureresourcesapi.DeploymentStackDesireActionTypeApply,
		Stack: &armdeploymentstacks.DeploymentStack{
			Properties: &armdeploymentstacks.DeploymentStackProperties{
				Template: map[string]any{
					"$schema":        "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
					"contentVersion": "1.0.0.0",
					"resources":      resources,
				},
				ActionOnUnmanage: &armdeploymentstacks.ActionOnUnmanage{
					Resources:        ptr.To(armdeploymentstacks.UnmanageActionResourceModeDelete),
					ResourceGroups:   ptr.To(armdeploymentstacks.UnmanageActionResourceGroupModeDetach),
					ManagementGroups: ptr.To(armdeploymentstacks.UnmanageActionManagementGroupModeDetach),
				},
				DenySettings: &armdeploymentstacks.DenySettings{
					Mode: ptr.To(armdeploymentstacks.DenySettingsModeNone),
				},
			},
		},
	}, nil
}

// roleAssignmentTemplateResources renders one Microsoft.Authorization/roleAssignments
// template resource per present key, named with that key's ReservedAzureResourceID. Keys
// are sorted so the generated template (and therefore its spec hash) is stable across syncs.
//
// Draining keys (DeconfigureTimestamp set) ARE rendered, unlike the mock FPA controller.
// When an assignment is no longer desired, ClusterRoleAssignmentIntent stamps
// DeconfigureTimestamp (starting a roleAssignmentDeconfigureDelay / 24h countdown) but keeps
// the key's row; we keep rendering it so the assignment stays in Azure during that window.
// Once the window elapses the intent controller drops the row, the key disappears from here,
// and the stack unmanages (deletes) it - i.e. removal is triggered 24h after the assignment
// stops being desired, not immediately.
func roleAssignmentTemplateResources(assignments map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus) ([]map[string]any, error) {
	keys := make([]coreapi.RoleAssignmentKey, 0, len(assignments))
	for key := range assignments {
		status := assignments[key]
		if status == nil {
			continue
		}
		// A draining key (DeconfigureTimestamp set) with no reserved ID was deconfigured
		// before it was ever reserved - it was added to the desired set and
		// removed again before serviceProviderClusterWithReservedAzureResourceIDs reserved it.
		// There is nothing in Azure to keep during its window, so skip it rather than error
		// on the nil ReservedAzureResourceID below. (Non-draining keys are always reserved
		// first, so a non-draining key with no reserved ID remains a real error.)
		if status.DeconfigureTimestamp != nil && status.ReservedAzureResourceID == nil {
			continue
		}
		keys = append(keys, key)
	}
	slices.SortFunc(keys, compareRoleAssignmentKeyByPrincipalAndResourceID)

	resources := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		status := assignments[key]
		if status.ReservedAzureResourceID == nil {
			return nil, utils.TrackError(fmt.Errorf("role assignment for principal ID %s role definition resource ID %s has no reserved AzureResourceID", key.PrincipalID, key.RoleDefinitionResourceID))
		}
		resources = append(resources, map[string]any{
			"type":       "Microsoft.Authorization/roleAssignments",
			"apiVersion": roleAssignmentAPIVersion,
			"name":       status.ReservedAzureResourceID.Name,
			"properties": map[string]any{
				// Properties come from the (lowercased) map key. Azure treats principalId and
				// roleDefinitionId case-insensitively, so lowercased values are equivalent; only
				// the name needs the original casing (already reserved in ReservedAzureResourceID).
				// The key is expected lowercase already (built via coreapihelpers.NewRoleAssignmentKey);
				// ToLower is a defensive guarantee in case a non-canonical key ever reaches here.
				"principalId":      strings.ToLower(string(key.PrincipalID)),
				"roleDefinitionId": strings.ToLower(string(key.RoleDefinitionResourceID)),
				"principalType":    "ServicePrincipal",
			},
		})
	}
	return resources, nil
}

// publishDeploymentStackDesire creates the role assignments DeploymentStackDesire if it does
// not exist yet, or replaces it only when desiredSpec actually differs from what
// is already published.
func (s *clusterRoleAssignmentsSyncer) publishDeploymentStackDesire(ctx context.Context, key controllerutils.HCPClusterKey, desiredSpec azureresourcesapi.DeploymentStackDesireSpec) error {
	logger := utils.LoggerFromContext(ctx)

	resourceID, err := azureresourcesapihelpers.ToClusterScopedDeploymentStackDesireResourceID(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, azureresourcesapi.RoleAssignmentsDeploymentStackDesireName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build role assignments DeploymentStackDesire resource ID: %w", err))
	}
	existing, err := s.deploymentStackDesireLister.GetForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, azureresourcesapi.RoleAssignmentsDeploymentStackDesireName)
	if cosmosstorageutils.IsNotFoundError(err) {
		newDesire := &azureresourcesapi.DeploymentStackDesire{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: key.SubscriptionID,
			},
			Spec: desiredSpec,
		}
		logger.Info("creating role assignments DeploymentStackDesire")
		clusterScopedStackDesiresCRUD, err := s.azureResourcesDBClient.DeploymentStackDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to get cluster scoped stack desires CRUD: %w", err))
		}
		_, err = clusterScopedStackDesiresCRUD.Create(ctx, newDesire, nil)
		if cosmosstorageutils.IsConflictError(err) {
			logger.Info("role assignments DeploymentStackDesire already exists", "deploymentStackDesireResourceID", resourceID)
			return nil
		}
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to create role assignments DeploymentStackDesire: %w", err))
		}
		logger.Info("created role assignments DeploymentStackDesire", "deploymentStackDesireResourceID", resourceID)
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get role assignments DeploymentStackDesire: %w", err))
	}

	existingHash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(existing.Spec)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to hash existing role assignments DeploymentStackDesire spec: %w", err))
	}
	desiredHash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(desiredSpec)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to hash desired role assignments DeploymentStackDesire spec: %w", err))
	}
	if existingHash == desiredHash {
		return nil
	}

	logger.Info("updating role assignments DeploymentStackDesire", "deploymentStackDesireResourceID", existing.ResourceID, "existingHash", existingHash, "desiredHash", desiredHash)
	replacement := existing.DeepCopy()
	replacement.Spec = desiredSpec
	clusterScopedStackDesiresCRUD, err := s.azureResourcesDBClient.DeploymentStackDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster scoped stack desires CRUD: %w", err))
	}
	_, err = clusterScopedStackDesiresCRUD.Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace role assignments DeploymentStackDesire: %w", err))
	}
	return nil
}

// compareRoleAssignmentKeyByPrincipalAndResourceID compares two role assignment keys by principal ID and then role definition resource ID.
// It returns a negative value if a < b, 0 if a == b, and a positive value if a > b.
// It can be used with slices.SortFunc to sort a slice of role assignment keys by principal ID and then role definition resource ID.
func compareRoleAssignmentKeyByPrincipalAndResourceID(a, b coreapi.RoleAssignmentKey) int {
	c := cmp.Compare(a.PrincipalID, b.PrincipalID)
	if c != 0 {
		return c
	}

	return cmp.Compare(a.RoleDefinitionResourceID, b.RoleDefinitionResourceID)
}

func (s *clusterRoleAssignmentsSyncer) deleteDeploymentStackDesireDatabaseEntry(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	_, err := s.deploymentStackDesireLister.GetForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, azureresourcesapi.RoleAssignmentsDeploymentStackDesireName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get role assignments DeploymentStackDesire: %w", err))
	}

	resourceID, err := azureresourcesapihelpers.ToClusterScopedDeploymentStackDesireResourceID(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, azureresourcesapi.RoleAssignmentsDeploymentStackDesireName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build role assignments DeploymentStackDesire resource ID: %w", err))
	}
	clusterScopedStackDesiresCRUD, err := s.azureResourcesDBClient.DeploymentStackDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster scoped stack desires CRUD: %w", err))
	}

	logger.Info("deleting role assignments DeploymentStackDesire", "deploymentStackDesireResourceID", resourceID.String())
	err = clusterScopedStackDesiresCRUD.Delete(ctx, azureresourcesapi.RoleAssignmentsDeploymentStackDesireName)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.Info("role assignments DeploymentStackDesire already deleted", "deploymentStackDesireResourceID", resourceID.String())
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to delete role assignments DeploymentStackDesire: %w", err))
	}
	logger.Info("deleted role assignments DeploymentStackDesire", "deploymentStackDesireResourceID", resourceID.String())
	return nil
}
