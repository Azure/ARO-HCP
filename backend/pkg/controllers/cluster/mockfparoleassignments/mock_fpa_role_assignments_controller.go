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

// MockFPARoleAssignmentsControllerName is the single source of truth for this
// controller's name. It is used for the workqueue name (a Prometheus label),
// context/logger controller name, and log fields.
const MockFPARoleAssignmentsControllerName = "MockFPARoleAssignments"

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
// It does not call Azure itself. It reserves a role assignment resource ID for a
// key that does not have one yet and persists it before use, then renders every
// non-draining key into one ARM template. A key that has started deconfigure
// (DeconfigureTimestamp set) is excluded from the template immediately, which
// lets the stack desires controller delete it from Azure right away rather than
// after a time-based wait.
type mockFPARoleAssignmentsSyncer struct {
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	subscriptionLister           corelisters.SubscriptionLister
	deploymentStackDesireLister  azureresourceslisters.DeploymentStackDesireLister
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	azureResourcesDBClient       azureresourcescosmosstorage.AzureResourcesDBClient
}

var _ controllerutils.ClusterSyncer = (*mockFPARoleAssignmentsSyncer)(nil)

// NewMockFPARoleAssignmentsController creates a cluster-watching controller that
// publishes ServiceProviderCluster.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup
// as a StackDesire for the stack desires controller to reconcile against Azure.
func NewMockFPARoleAssignmentsController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	azureResourcesDBClient azureresourcescosmosstorage.AzureResourcesDBClient,
	backendInformers coreinformers.BackendInformers,
	azureResourcesInformers azureresourcesinformers.AzureResourcesInformers,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()
	_, subscriptionLister := backendInformers.Subscriptions()
	deploymentStackDesireInformer, deploymentStackDesireLister := azureResourcesInformers.DeploymentStackDesires()

	syncer := &mockFPARoleAssignmentsSyncer{
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		subscriptionLister:           subscriptionLister,
		deploymentStackDesireLister:  deploymentStackDesireLister,
		resourcesDBClient:            resourcesDBClient,
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
	// desire re-triggers this controller, which consumes that status to advance its
	// stamp/drop state machine. Resync is 0: the periodic resync is done once on the
	// cluster (key) resource inside NewClusterWatchingController. This watch only
	// needs to fire on actual DeploymentStackDesire changes.
	if err := controller.QueueForChildInformers(0, deploymentStackDesireInformer); err != nil {
		panic(err)
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
		err := s.deleteDeploymentStackDesireDatabaseEntry(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to delete mock FPA role assignments DeploymentStackDesire from the database: %w", err)
		}
		return nil
	}

	if scope == nil {
		// Role assignment names and the stack target are both scoped to the managed
		// resource group. Nothing can be published yet.
		return nil
	}

	// Publishing an empty template rather than deleting the whole stack keeps the
	// stack (which lives in and cascades away with the managed resource group anyway) and
	// avoids create/delete churn if a key is later re-added.

	// Reserve the mock FPA role assignment resource IDs onto the ServiceProviderCluster as a first step. This ensures
	// that the role assignments references are not lost if the DeploymentStackDesire is not published yet. In the future
	// the references will be generated randomly and non deterministically.
	desiredServiceProviderCluster, err := s.serviceProviderClusterWithReservedAzureResourceIDs(existingServiceProviderCluster, scope)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to reserve mock FPA role assignment resource IDs: %w", err))
	}
	if controllerutil.NeedsUpdate(existingServiceProviderCluster, desiredServiceProviderCluster) {
		logger.Info("reserving mock FPA role assignment resource IDs onto ServiceProviderCluster")
		persisted, err := s.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, desiredServiceProviderCluster, nil)
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			return nil
		}
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
		}
		desiredServiceProviderCluster = persisted
	}

	desiredSpec, err := s.desiredDeploymentStackDesireSpec(desiredServiceProviderCluster, scope)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build desired mock FPA role assignments DeploymentStackDesire spec: %w", err))
	}

	err = s.publishDeploymentStackDesire(ctx, key, desiredSpec, logger)
	if err != nil {
		return fmt.Errorf("failed to publish mock FPA role assignments DeploymentStackDesire: %w", err)
	}

	return nil
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

// serviceProviderClusterWithReservedAzureResourceIDs returns a copy of existing with a
// ReservedAzureResourceID assigned to every non-draining role assignment key that does not
// have one yet. Draining keys keep whatever reserved ID they already had; naming is a
// one-time reservation, never recomputed.
//
// It is pure (no I/O): the caller persists the result, so the reserved ID is recorded
// durably before it is rendered into the deployment stack. An error is returned only for
// an internal failure building the deterministic resource ID.
func (s *mockFPARoleAssignmentsSyncer) serviceProviderClusterWithReservedAzureResourceIDs(existing *coreapi.ServiceProviderCluster, scope *azcorearm.ResourceID) (*coreapi.ServiceProviderCluster, error) {
	replacement := existing.DeepCopy()

	keys := make([]coreapi.RoleAssignmentKey, 0, len(replacement.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup))
	for key := range replacement.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup {
		keys = append(keys, key)
	}
	sortRoleAssignmentKeys(keys)

	for _, key := range keys {
		status := replacement.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup[key]
		if status.DeconfigureTimestamp != nil || status.ReservedAzureResourceID != nil {
			continue
		}
		reservedID, err := azcorearm.ParseResourceID(roleassignment.ManagedResourceGroupScopedRoleAssignmentResourceID(scope.String(), key.PrincipalID, key.RoleDefinitionResourceID))
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
func (s *mockFPARoleAssignmentsSyncer) desiredDeploymentStackDesireSpec(
	serviceProviderCluster *coreapi.ServiceProviderCluster,
	scope *azcorearm.ResourceID,
) (azureresourcesapi.DeploymentStackDesireSpec, error) {
	target, err := azcorearm.ParseResourceID(scope.String() + "/providers/Microsoft.Resources/deploymentStacks/" + mockFPARoleAssignmentsAzureDeploymentStackName)
	if err != nil {
		return azureresourcesapi.DeploymentStackDesireSpec{}, utils.TrackError(fmt.Errorf("failed to parse mock FPA role assignments stack resource ID: %w", err))
	}

	resources, err := roleAssignmentTemplateResources(serviceProviderCluster.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup)
	if err != nil {
		return azureresourcesapi.DeploymentStackDesireSpec{}, utils.TrackError(fmt.Errorf("failed to build role assignment template resources: %w", err))
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
					"resources":      resources,
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
// template resource per non-draining key, named with that key's
// ReservedAzureResourceID. Draining keys (DeconfigureTimestamp set) are excluded so
// the stack desires controller deletes them from Azure. Keys are sorted so the
// generated template (and therefore its spec hash) is stable across syncs.
func roleAssignmentTemplateResources(assignments map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus) ([]map[string]any, error) {
	keys := make([]coreapi.RoleAssignmentKey, 0, len(assignments))
	for key := range assignments {
		if assignments[key] == nil || assignments[key].DeconfigureTimestamp != nil {
			continue
		}
		keys = append(keys, key)
	}
	sortRoleAssignmentKeys(keys)

	resources := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		status := assignments[key]
		if status.ReservedAzureResourceID == nil {
			return nil, utils.TrackError(fmt.Errorf("mock FPA role assignment for principal ID %s role definition resource ID %s has no reserved AzureResourceID", key.PrincipalID, key.RoleDefinitionResourceID))
		}
		resources = append(resources, map[string]any{
			"type":       "Microsoft.Authorization/roleAssignments",
			"apiVersion": roleAssignmentAPIVersion,
			"name":       status.ReservedAzureResourceID.Name,
			"properties": map[string]any{
				"principalId":      key.PrincipalID,
				"roleDefinitionId": key.RoleDefinitionResourceID,
				"principalType":    "ServicePrincipal",
			},
		})
	}
	return resources, nil
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
			logger.Info("mock FPA role assignments DeploymentStackDesire already exists", "deploymentStackDesireResourceID", existing.ResourceID)
			return nil
		}
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to create mock FPA role assignments DeploymentStackDesire: %w", err))
		}
		logger.Info("created mock FPA role assignments DeploymentStackDesire", "deploymentStackDesireResourceID", existing.ResourceID)
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
