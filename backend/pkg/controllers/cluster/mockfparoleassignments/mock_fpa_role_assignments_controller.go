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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/roleassignment"
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

// MockFPARoleAssignmentsControllerName is the single source of truth for this
// controller's name. It is used for the workqueue name (a Prometheus label),
// context/logger controller name, and log fields.
const MockFPARoleAssignmentsControllerName = "MockFPARoleAssignments"

const (
	// MockFPARoleAssignmentsDesireName identifies the mock FPA Owner role
	// assignment intent as a StackDesire.
	MockFPARoleAssignmentsDesireName = "mock-fpa-role-assignments"
	// MockFPARoleAssignmentsStackName is the deployment stack name, stable within
	// each cluster's managed resource group.
	MockFPARoleAssignmentsStackName = "aro-hcp-mock-fpa-role-assignments"
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
	stackDesireLister            corelisters.StackDesireLister
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
}

var _ controllerutils.ClusterSyncer = (*mockFPARoleAssignmentsSyncer)(nil)

// NewMockFPARoleAssignmentsController creates a cluster-watching controller that
// publishes ServiceProviderCluster.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup
// as a StackDesire for the stack desires controller to reconcile against Azure.
func NewMockFPARoleAssignmentsController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	backendInformers coreinformers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()
	_, subscriptionLister := backendInformers.Subscriptions()
	_, stackDesireLister := backendInformers.StackDesires()

	syncer := &mockFPARoleAssignmentsSyncer{
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		subscriptionLister:           subscriptionLister,
		stackDesireLister:            stackDesireLister,
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

	return controller
}

func (s *mockFPARoleAssignmentsSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	existingCluster, err := s.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return s.deleteStackDesire(ctx, key)
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster from cache: %w", err))
	}

	// The assignment is scoped to the managed resource group, so Azure deletes it
	// in cascade when that resource group is removed during cluster teardown. Only
	// the bookkeeping StackDesire document needs cleaning up here.
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return s.deleteStackDesire(ctx, key)
	}

	existingServiceProviderCluster, err := s.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return s.deleteStackDesire(ctx, key)
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	scope := existingServiceProviderCluster.Status.AzureResources.ManagedResourceGroup.AzureResource
	if scope == nil {
		// Role assignment names and the stack target are both scoped to the managed
		// resource group. Nothing can be published yet.
		return nil
	}

	if !hasRenderableKeys(existingServiceProviderCluster.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup) {
		// No non-draining keys: nothing should be managed, so ask for the stack to
		// be deleted (which also removes any draining assignment left in Azure).
		return s.requestStackDeletion(ctx, key)
	}

	reservedServiceProviderCluster, err := s.reserveAzureResourceIDs(ctx, existingCluster, existingServiceProviderCluster, scope)
	if err != nil {
		return err
	}

	subscription, err := s.subscriptionLister.Get(ctx, key.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Subscription from cache: %w", err))
	}
	if subscription.Properties == nil || subscription.Properties.TenantId == nil {
		return utils.TrackError(fmt.Errorf("subscription %s has no tenantId", key.SubscriptionID))
	}

	desiredSpec, err := s.desiredStackDesireSpec(reservedServiceProviderCluster, scope, *subscription.Properties.TenantId)
	if err != nil {
		return err
	}

	return s.publishStackDesire(ctx, key, desiredSpec, logger)
}

// hasRenderableKeys reports whether any key is not draining (has no
// DeconfigureTimestamp), i.e. whether anything should be rendered into the stack.
func hasRenderableKeys(assignments map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus) bool {
	for _, status := range assignments {
		if status != nil && status.DeconfigureTimestamp == nil {
			return true
		}
	}
	return false
}

// reserveAzureResourceIDs assigns ReservedAzureResourceID to every non-draining
// key that does not have one yet, and persists the result before returning, so
// the reserved ID is never lost to a crash between being chosen and being used.
// Draining keys keep whatever reserved ID they already had; naming is a one-time
// reservation, never recomputed.
func (s *mockFPARoleAssignmentsSyncer) reserveAzureResourceIDs(
	ctx context.Context,
	existingCluster *coreapi.Cluster,
	existingServiceProviderCluster *coreapi.ServiceProviderCluster,
	scope *azcorearm.ResourceID,
) (*coreapi.ServiceProviderCluster, error) {
	replacement := existingServiceProviderCluster.DeepCopy()

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

	if !controllerutil.NeedsUpdate(existingServiceProviderCluster, replacement) {
		return existingServiceProviderCluster, nil
	}

	utils.LoggerFromContext(ctx).Info("reserving mock FPA role assignment resource IDs onto ServiceProviderCluster")
	persisted, err := s.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, replacement, nil)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}
	return persisted, nil
}

// desiredStackDesireSpec builds the StackDesire spec that applies every
// non-draining key as one ARM template, authenticated as the ARM permissions
// manager identity.
func (s *mockFPARoleAssignmentsSyncer) desiredStackDesireSpec(
	serviceProviderCluster *coreapi.ServiceProviderCluster,
	scope *azcorearm.ResourceID,
	tenantID string,
) (coreapi.StackDesireSpec, error) {
	target, err := azcorearm.ParseResourceID(scope.String() + "/providers/Microsoft.Resources/deploymentStacks/" + MockFPARoleAssignmentsStackName)
	if err != nil {
		return coreapi.StackDesireSpec{}, utils.TrackError(fmt.Errorf("failed to parse mock FPA role assignments stack resource ID: %w", err))
	}

	resources, err := roleAssignmentTemplateResources(serviceProviderCluster.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup)
	if err != nil {
		return coreapi.StackDesireSpec{}, err
	}

	return coreapi.StackDesireSpec{
		TargetResourceID: target,
		Authentication: coreapi.StackDesireAuthentication{
			Type:     coreapi.StackDesireAuthenticationTypeInsecureARMPermissionsManager,
			TenantID: tenantID,
		},
		Action: coreapi.StackDesireActionApply,
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
		Resources:        ptr.To(armdeploymentstacks.DeploymentStacksDeleteDetachEnumDelete),
		ResourceGroups:   ptr.To(armdeploymentstacks.DeploymentStacksDeleteDetachEnumDetach),
		ManagementGroups: ptr.To(armdeploymentstacks.DeploymentStacksDeleteDetachEnumDetach),
	}
}

// roleAssignmentTemplateResources renders one Microsoft.Authorization/roleAssignments
// template resource per non-draining key, named with that key's
// ReservedAzureResourceID. Draining keys (DeconfigureTimestamp set) are excluded so
// the stack desires controller deletes them from Azure. Keys are sorted so the
// generated template (and therefore its spec hash) is stable across syncs.
func roleAssignmentTemplateResources(assignments map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus) ([]map[string]any, error) {
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

// publishStackDesire creates the mock FPA role assignments StackDesire if it does
// not exist yet, or replaces it only when desiredSpec actually differs from what
// is already published.
func (s *mockFPARoleAssignmentsSyncer) publishStackDesire(ctx context.Context, key controllerutils.HCPClusterKey, desiredSpec coreapi.StackDesireSpec, logger logr.Logger) error {
	existing, err := s.stackDesireLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, MockFPARoleAssignmentsDesireName)
	if cosmosstorageutils.IsNotFoundError(err) {
		resourceID, err := coreapihelpers.ToStackDesireResourceID(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, MockFPARoleAssignmentsDesireName)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to build mock FPA role assignments StackDesire resource ID: %w", err))
		}
		newDesire := &coreapi.StackDesire{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: key.SubscriptionID,
			},
			Spec: desiredSpec,
		}
		logger.Info("creating mock FPA role assignments StackDesire")
		_, err = s.resourcesDBClient.StackDesires(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Create(ctx, newDesire, nil)
		if cosmosstorageutils.IsConflictError(err) {
			return nil
		}
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to create mock FPA role assignments StackDesire: %w", err))
		}
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get mock FPA role assignments StackDesire: %w", err))
	}

	existingHash, err := coreapi.StackDesireSpecHash(existing.Spec)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to hash existing mock FPA role assignments StackDesire spec: %w", err))
	}
	desiredHash, err := coreapi.StackDesireSpecHash(desiredSpec)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to hash desired mock FPA role assignments StackDesire spec: %w", err))
	}
	if existingHash == desiredHash {
		return nil
	}

	replacement := existing.DeepCopy()
	replacement.Spec = desiredSpec
	logger.Info("updating mock FPA role assignments StackDesire")
	_, err = s.resourcesDBClient.StackDesires(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace mock FPA role assignments StackDesire: %w", err))
	}
	return nil
}

// requestStackDeletion asks the stack desires controller to delete the Azure
// deployment stack when no non-draining keys remain on a live cluster. It never
// removes the StackDesire document itself here: nothing else will delete the
// already-applied stack, so abandoning the document would orphan it.
func (s *mockFPARoleAssignmentsSyncer) requestStackDeletion(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existing, err := s.stackDesireLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, MockFPARoleAssignmentsDesireName)
	if cosmosstorageutils.IsNotFoundError(err) {
		// Never published, so there is nothing to delete.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get mock FPA role assignments StackDesire: %w", err))
	}
	if existing.Spec.Action == coreapi.StackDesireActionDelete {
		return nil
	}

	replacement := existing.DeepCopy()
	replacement.Spec.Action = coreapi.StackDesireActionDelete
	utils.LoggerFromContext(ctx).Info("requesting deletion of mock FPA role assignments StackDesire")
	_, err = s.resourcesDBClient.StackDesires(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace mock FPA role assignments StackDesire: %w", err))
	}
	return nil
}

func (s *mockFPARoleAssignmentsSyncer) deleteStackDesire(ctx context.Context, key controllerutils.HCPClusterKey) error {
	err := s.resourcesDBClient.StackDesires(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Delete(ctx, MockFPARoleAssignmentsDesireName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to delete mock FPA role assignments StackDesire: %w", err))
	}
	return nil
}
