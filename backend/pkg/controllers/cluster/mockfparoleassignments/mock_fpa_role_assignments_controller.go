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

package mockfparoleassignments

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks/v2"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/roleassignment"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/azureresourcesapihelpers"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/azureresourcescosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/azureresourcesinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/azureresourceslisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// MockFPARoleAssignmentsControllerName is the single source of truth for this
// controller's name. It is used for the workqueue name (a Prometheus label),
// context/logger controller name, and log fields.
const MockFPARoleAssignmentsControllerName = "MockFPARoleAssignments"

// OwnerRoleDefinitionResourceID is the tenant-level resource ID of the Azure
// built-in Owner role definition. It is the role the mock First Party Application
// principal is granted over each cluster's managed resource group.
// The ID has been obtained from https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles/privileged#owner.
const OwnerRoleDefinitionResourceID = "/providers/Microsoft.Authorization/roleDefinitions/8e3af657-a8ff-443c-a75c-2fe8c4bcb635"

const (
	// mockFPARoleAssignmentsAzureDeploymentStackName is the deployment stack name, stable within
	// each cluster's managed resource group.
	mockFPARoleAssignmentsAzureDeploymentStackName = "mock-fpa-role-assignments-over-mrg"
	// roleAssignmentAPIVersion is the ARM API version embedded in each
	// Microsoft.Authorization/roleAssignments template resource.
	roleAssignmentAPIVersion = "2022-04-01"
)

// mockFPARoleAssignmentsSyncer publishes the mock FPA Owner role assignment over
// the managed resource group as a single StackDesire authenticated as the ARM
// permissions manager identity, so the stack desires controller can apply or
// delete it in Azure. It only runs when the backend has no real FPA.
//
// There is no intermediate state on the ServiceProviderCluster. The published
// DeploymentStackDesire.Spec template is itself the ledger: its
// Microsoft.Authorization/roleAssignments resources record, for every assignment
// the mock FPA stack manages, that assignment's principal ID, role definition, and
// reserved role assignment name. The syncer reads that template back to learn what
// is currently managed, unions it with the single desired key (the configured mock
// FPA principal paired with the Owner role), and republishes.
//
// Identity replacement (for example when the mock FPA principal ID changes, or a
// transitive A to B to C change) is handled without losing the Owner grant and
// without orphaning any assignment:
//   - A superseded key is kept rendered in the template (so its Azure assignment
//     stays live) until the desired key is confirmed live in Azure by a current,
//     successful stack reconcile.
//   - Once the desired key is confirmed, the superseded key is excluded from the
//     next published template. The deployment stack deletes it via ActionOnUnmanage,
//     and dropping it from the template is the only bookkeeping needed - the stack
//     tracks its own managed resources, so there is nothing further to reconcile here.
//
// It does not call Azure itself. Because the reserved role assignment name is baked
// into the published template before Azure ever applies it, the Spec write is the
// durable reservation: a crash after apply cannot orphan an assignment whose name
// was never recorded. Role assignment names are deterministic today; when they
// become fully random the read path above is unchanged (it reads the name from the
// template) and only the reservation call below would switch to a random generator.
type mockFPARoleAssignmentsSyncer struct {
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	deploymentStackDesireLister  azureresourceslisters.DeploymentStackDesireLister
	azureResourcesDBClient       azureresourcescosmosstorage.AzureResourcesDBClient
	// mockFPAPrincipalID is the principal ID of the mock FPA service principal that
	// is granted Owner over the managed resource group.
	mockFPAPrincipalID string
	// desiredRoleDefinitionResourceID is the role definition resource ID granted to
	// the mock FPA principal over the managed resource group (the Owner role).
	desiredRoleDefinitionResourceID string
}

var _ controllerutils.ClusterSyncer = (*mockFPARoleAssignmentsSyncer)(nil)

// NewMockFPARoleAssignmentsController creates a cluster-watching controller that
// publishes the mock FPA Owner role assignment over the managed resource group as a
// DeploymentStackDesire, using that desire's own Spec template as the ledger of what
// is managed. It must only be instantiated when the backend has no real FPA.
func NewMockFPARoleAssignmentsController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	azureResourcesDBClient azureresourcescosmosstorage.AzureResourcesDBClient,
	backendInformers coreinformers.BackendInformers,
	azureResourcesInformers azureresourcesinformers.AzureResourcesInformers,
	mockFPAPrincipalID string,
	desiredRoleDefinitionResourceID string,
) controllerutils.Controller {
	if len(mockFPAPrincipalID) == 0 {
		panic("mock FPA principal ID is not configured")
	}
	if len(desiredRoleDefinitionResourceID) == 0 {
		panic("desired role definition resource ID is not configured")
	}

	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()
	deploymentStackDesireInformer, deploymentStackDesireLister := azureResourcesInformers.DeploymentStackDesires()

	syncer := &mockFPARoleAssignmentsSyncer{
		clusterLister:                   clusterLister,
		serviceProviderClusterLister:    serviceProviderClusterLister,
		deploymentStackDesireLister:     deploymentStackDesireLister,
		azureResourcesDBClient:          azureResourcesDBClient,
		mockFPAPrincipalID:              mockFPAPrincipalID,
		desiredRoleDefinitionResourceID: desiredRoleDefinitionResourceID,
	}

	controller := controllerutils.NewClusterWatchingController(
		MockFPARoleAssignmentsControllerName,
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
	// desire re-triggers this controller, which consumes that status to decide when a
	// superseded key is safe to drop from the template. Resync is 0: the periodic
	// resync is done once on the cluster (key) resource inside
	// NewClusterWatchingController. This watch only needs to fire on actual
	// DeploymentStackDesire changes.
	if err := controller.QueueForChildInformers(0, deploymentStackDesireInformer); err != nil {
		panic(err) // coding error
	}
	controller.AddInterestingResource(azureresourcesapi.ClusterScopedDeploymentStackDesireResourceType, azureresourcesapi.MockFPARoleAssignmentsDeploymentStackDesireName)

	return controller
}

func (s *mockFPARoleAssignmentsSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
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

	scope := managedResourceGroupScope(existingServiceProviderCluster.Status.AzureResources.ManagedResourceGroup)
	// Deletion must NOT short-circuit while the managed resource group still exists.
	// The mock FPA deletes the MRG using exactly this Owner assignment (its base
	// subscription role grants resourceGroups read/write but NOT delete), so tearing
	// the assignment down at the start of deletion - or never creating it because the
	// controller only now caught up - would leave the MRG undeletable and orphaned.
	// We therefore keep the assignment published for as long as the MRG is present,
	// even during cluster teardown, and only clean up the bookkeeping StackDesire once
	// the MRG is gone (both references cleared by the managed resource group controller).
	// The assignment itself is MRG-scoped, so Azure removes it in cascade with the MRG.
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil && scope == nil {
		if err := s.deleteDeploymentStackDesireDatabaseEntry(ctx, key); err != nil {
			return fmt.Errorf("failed to delete mock FPA role assignments DeploymentStackDesire from the database: %w", err)
		}
		return nil
	}

	if scope == nil {
		// Role assignment names and the stack target are both scoped to the managed
		// resource group. Nothing can be published yet.
		return nil
	}

	existingDeploymentStackDesire, err := s.deploymentStackDesireLister.GetForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, azureresourcesapi.MockFPARoleAssignmentsDeploymentStackDesireName)
	if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to get mock FPA role assignments DeploymentStackDesire: %w", err))
	}
	if cosmosstorageutils.IsNotFoundError(err) {
		existingDeploymentStackDesire = nil
	}

	desiredKey := coreapi.RoleAssignmentKey{
		PrincipalID:              s.mockFPAPrincipalID,
		RoleDefinitionResourceID: s.desiredRoleDefinitionResourceID,
	}

	desiredRoleAssignmentKeys, err := s.calculateDesiredRoleAssignmentKeys(desiredKey, scope, existingDeploymentStackDesire)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to compute desired mock FPA role assignments: %w", err))
	}

	desiredSpec, err := desiredDeploymentStackDesireSpec(scope, desiredRoleAssignmentKeys)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build desired mock FPA role assignments DeploymentStackDesire spec: %w", err))
	}

	if err := s.publishDeploymentStackDesire(ctx, key, desiredSpec, logger); err != nil {
		return fmt.Errorf("failed to publish mock FPA role assignments DeploymentStackDesire: %w", err)
	}

	return nil
}

// calculateDesiredRoleAssignmentKeys computes the set of role assignments to render in the
// next published template, keyed by RoleAssignmentKey and valued by the reserved role
// assignment name.
//
// The currently published template is read back from existingDeploymentStackDesire (the
// ledger) here, so the current set can never diverge from the desire it is derived from.
//
// The desired key is always rendered, reusing its already-reserved name when it is
// already in the published template, or reserving a new name otherwise. Every other
// key currently in the template (a superseded identity) is kept rendered until the
// desired key is confirmed live in Azure, then excluded so the deployment stack
// deletes it. This keeps the Owner grant over the managed resource group continuous
// across an identity replacement, including transitive A to B to C changes where
// several superseded keys may be live at once.
func (s *mockFPARoleAssignmentsSyncer) calculateDesiredRoleAssignmentKeys(
	desiredKey coreapi.RoleAssignmentKey,
	scope *azcorearm.ResourceID,
	existingDeploymentStackDesire *azureresourcesapi.DeploymentStackDesire,
) (map[coreapi.RoleAssignmentKey]string, error) {
	currentRoleAssignmentKeysInSpecTemplate, err := coreapihelpers.MockFPARoleAssignmentsInSpecTemplate(existingDeploymentStackDesire)
	if err != nil {
		return nil, fmt.Errorf("failed to read rendered mock FPA role assignments from DeploymentStackDesire: %w", err)
	}

	desiredRoleAssignmentKeys := make(map[coreapi.RoleAssignmentKey]string)

	// Desired key: reuse the reserved name already in the published template, or reserve
	// a new one. Reserving here and persisting via the Spec write below records the name
	// durably before Azure ever applies it.
	desiredName, ok := currentRoleAssignmentKeysInSpecTemplate[desiredKey]
	if !ok {
		desiredName = reserveRoleAssignmentName(scope, desiredKey)
	}
	desiredRoleAssignmentKeys[desiredKey] = desiredName

	// Whether the desired assignment is confirmed live in Azure per the current,
	// successful stack reconcile. A superseded assignment is only removed once this is
	// true, so we never lose the Owner grant over the managed resource group during an
	// identity replacement. The check uses the name as it appears in the published spec
	// (currentRendered[desiredKey]); a freshly reserved, not-yet-published name is never
	// configured.
	desiredConfigured, err := coreapihelpers.MockFPARoleAssignmentConfigured(existingDeploymentStackDesire, currentRoleAssignmentKeysInSpecTemplate[desiredKey])
	if err != nil {
		return nil, err
	}

	for existingKey, name := range currentRoleAssignmentKeysInSpecTemplate {
		if existingKey == desiredKey {
			continue
		}
		if !desiredConfigured {
			// Keep the superseded assignment live until the replacement is confirmed.
			desiredRoleAssignmentKeys[existingKey] = name
		}
		// Once the desired assignment is confirmed, exclude the superseded key so the
		// deployment stack deletes it via ActionOnUnmanage. Dropping it from the template
		// is the removal. The stack tracks its own managed resources.
	}

	return desiredRoleAssignmentKeys, nil
}

// reserveRoleAssignmentName returns the role assignment name to reserve for key at
// scope. It is deterministic today (derived from scope, principal, and role
// definition); if the naming scheme ever becomes fully random this is the only call
// that changes, because every reader resolves the name from the published template.
func reserveRoleAssignmentName(scope *azcorearm.ResourceID, key coreapi.RoleAssignmentKey) string {
	return roleassignment.GenerateManagedResourceGroupScopedRoleAssignmentName(scope.String(), key.PrincipalID, key.RoleDefinitionResourceID)
}

// managedResourceGroupScope returns the resource ID to scope the mock FPA role
// assignment against, or nil when the managed resource group is not present. It
// prefers the confirmed AzureResource and falls back to PendingAzureResource: during
// deletion the managed resource group controller never promotes pending to confirmed
// (it only deletes) and it deletes a pending-only MRG too, so a pending-only reference
// is still a real MRG the mock FPA must be able to delete. The scope resource ID is
// identical either way. A nil return means AzureResource and PendingAzureResource in
// ref are both nil.
func managedResourceGroupScope(ref coreapi.AzureReference) *azcorearm.ResourceID {
	if ref.AzureResource != nil {
		return ref.AzureResource
	}
	return ref.PendingAzureResource
}

// desiredDeploymentStackDesireSpec builds the StackDesire spec that applies every
// rendered key as one ARM template, authenticated as the ARM permissions manager
// identity.
func desiredDeploymentStackDesireSpec(
	scope *azcorearm.ResourceID,
	desiredRoleAssignmentKeys map[coreapi.RoleAssignmentKey]string,
) (azureresourcesapi.DeploymentStackDesireSpec, error) {
	target, err := azcorearm.ParseResourceID(scope.String() + "/providers/Microsoft.Resources/deploymentStacks/" + mockFPARoleAssignmentsAzureDeploymentStackName)
	if err != nil {
		return azureresourcesapi.DeploymentStackDesireSpec{}, utils.TrackError(fmt.Errorf("failed to parse mock FPA role assignments stack resource ID: %w", err))
	}

	return azureresourcesapi.DeploymentStackDesireSpec{
		TargetResourceID: target,
		Authentication: azureresourcesapi.DeploymentStackDesireAuthentication{
			Type: azureresourcesapi.DeploymentStackDesireAuthenticationTypeInsecureARMPermissionsManager,
		},
		Action: azureresourcesapi.DeploymentStackDesireActionTypeApply,
		Stack: &armdeploymentstacks.DeploymentStack{
			Properties: &armdeploymentstacks.DeploymentStackProperties{
				Template: map[string]any{
					"$schema":        "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
					"contentVersion": "1.0.0.0",
					"resources":      roleAssignmentTemplateResources(desiredRoleAssignmentKeys),
				},
				ActionOnUnmanage: roleAssignmentsActionOnUnmanage(),
				DenySettings: &armdeploymentstacks.DenySettings{
					Mode: ptr.To(armdeploymentstacks.DenySettingsModeNone),
				},
			},
		},
	}, nil
}

// roleAssignmentsActionOnUnmanage deletes a role assignment from Azure once it
// drops out of the published template, and never touches resource groups or
// management groups.
func roleAssignmentsActionOnUnmanage() *armdeploymentstacks.ActionOnUnmanage {
	return &armdeploymentstacks.ActionOnUnmanage{
		Resources:        ptr.To(armdeploymentstacks.UnmanageActionResourceModeDelete),
		ResourceGroups:   ptr.To(armdeploymentstacks.UnmanageActionResourceGroupModeDetach),
		ManagementGroups: ptr.To(armdeploymentstacks.UnmanageActionManagementGroupModeDetach),
	}
}

// roleAssignmentTemplateResources renders one Microsoft.Authorization/roleAssignments
// template resource per key, named with that key's reserved role assignment name. Keys
// are sorted so the generated template (and therefore its spec hash) is stable across
// syncs.
func roleAssignmentTemplateResources(desiredRoleAssignmentKeys map[coreapi.RoleAssignmentKey]string) []map[string]any {
	keys := make([]coreapi.RoleAssignmentKey, 0, len(desiredRoleAssignmentKeys))
	for key := range desiredRoleAssignmentKeys {
		keys = append(keys, key)
	}
	sortRoleAssignmentKeys(keys)

	resources := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		resources = append(resources, map[string]any{
			"type":       coreapihelpers.RoleAssignmentResourceType,
			"apiVersion": roleAssignmentAPIVersion,
			"name":       desiredRoleAssignmentKeys[key],
			"properties": map[string]any{
				"principalId":      key.PrincipalID,
				"roleDefinitionId": key.RoleDefinitionResourceID,
				"principalType":    "ServicePrincipal",
			},
		})
	}
	return resources
}

func sortRoleAssignmentKeys(keys []coreapi.RoleAssignmentKey) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].PrincipalID != keys[j].PrincipalID {
			return keys[i].PrincipalID < keys[j].PrincipalID
		}
		return keys[i].RoleDefinitionResourceID < keys[j].RoleDefinitionResourceID
	})
}

// publishDeploymentStackDesire creates the mock FPA role assignments DeploymentStackDesire if it does
// not exist yet, or replaces it only when desiredSpec actually differs from what
// is already published.
func (s *mockFPARoleAssignmentsSyncer) publishDeploymentStackDesire(ctx context.Context, key controllerutils.HCPClusterKey, desiredSpec azureresourcesapi.DeploymentStackDesireSpec, logger logr.Logger) error {
	resourceID, err := azureresourcesapihelpers.ToClusterScopedDeploymentStackDesireResourceID(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, azureresourcesapi.MockFPARoleAssignmentsDeploymentStackDesireName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build mock FPA role assignments DeploymentStackDesire resource ID: %w", err))
	}
	existing, err := s.deploymentStackDesireLister.GetForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, azureresourcesapi.MockFPARoleAssignmentsDeploymentStackDesireName)
	if cosmosstorageutils.IsNotFoundError(err) {
		newDesire := &azureresourcesapi.DeploymentStackDesire{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: key.SubscriptionID,
			},
			Spec: desiredSpec,
		}
		logger.Info("creating mock FPA role assignments DeploymentStackDesire")
		clusterScopedStackDesiresCRUD, err := s.azureResourcesDBClient.DeploymentStackDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to get cluster scoped stack desires CRUD: %w", err))
		}
		_, err = clusterScopedStackDesiresCRUD.Create(ctx, newDesire, nil)
		if cosmosstorageutils.IsConflictError(err) {
			logger.Info("mock FPA role assignments DeploymentStackDesire already exists", "deploymentStackDesireResourceID", resourceID)
			return nil
		}
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to create mock FPA role assignments DeploymentStackDesire: %w", err))
		}
		logger.Info("created mock FPA role assignments DeploymentStackDesire", "deploymentStackDesireResourceID", resourceID)
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get mock FPA role assignments DeploymentStackDesire: %w", err))
	}

	existingHash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(existing.Spec)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to hash existing mock FPA role assignments DeploymentStackDesire spec: %w", err))
	}
	desiredHash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(desiredSpec)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to hash desired mock FPA role assignments DeploymentStackDesire spec: %w", err))
	}
	if existingHash == desiredHash {
		return nil
	}

	logger.Info("updating mock FPA role assignments DeploymentStackDesire", "deploymentStackDesireResourceID", existing.ResourceID, "existingHash", existingHash, "desiredHash", desiredHash)
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
		return utils.TrackError(fmt.Errorf("failed to replace mock FPA role assignments DeploymentStackDesire: %w", err))
	}
	return nil
}

func (s *mockFPARoleAssignmentsSyncer) deleteDeploymentStackDesireDatabaseEntry(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)
	resourceID, err := azureresourcesapihelpers.ToClusterScopedDeploymentStackDesireResourceID(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, azureresourcesapi.MockFPARoleAssignmentsDeploymentStackDesireName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build mock FPA role assignments DeploymentStackDesire resource ID: %w", err))
	}
	clusterScopedStackDesiresCRUD, err := s.azureResourcesDBClient.DeploymentStackDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster scoped stack desires CRUD: %w", err))
	}

	logger.Info("deleting mock FPA role assignments DeploymentStackDesire", "deploymentStackDesireResourceID", resourceID.String())
	err = clusterScopedStackDesiresCRUD.Delete(ctx, resourceID.String())
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.Info("mock FPA role assignments DeploymentStackDesire already deleted", "deploymentStackDesireResourceID", resourceID.String())
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to delete mock FPA role assignments DeploymentStackDesire: %w", err))
	}
	logger.Info("deleted mock FPA role assignments DeploymentStackDesire", "deploymentStackDesireResourceID", resourceID.String())
	return nil
}
