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

// ClusterRoleAssignmentsControllerName is the single source of truth for this
// controller's name. It is used for the workqueue name (a Prometheus label),
// context/logger controller name, and log fields.
const ClusterRoleAssignmentsControllerName = "ClusterRoleAssignments"

const (
	// RoleAssignmentsDesireName identifies the cluster's complete role
	// assignment intent as a StackDesire.
	RoleAssignmentsDesireName = "role-assignments"
	// RoleAssignmentsStackName is the deployment stack name, stable within
	// each cluster's managed resource group.
	RoleAssignmentsStackName = "aro-hcp-role-assignments"
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
	stackDesireLister            corelisters.StackDesireLister
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
}

var _ controllerutils.ClusterSyncer = (*clusterRoleAssignmentsSyncer)(nil)

// NewClusterRoleAssignmentsController creates a cluster-watching controller
// that publishes ServiceProviderCluster.Status.RoleAssignmentsOverManagedResourceGroup
// as a StackDesire for the stack desires controller to reconcile against Azure.
func NewClusterRoleAssignmentsController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	backendInformers coreinformers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()
	_, subscriptionLister := backendInformers.Subscriptions()
	_, stackDesireLister := backendInformers.StackDesires()

	syncer := &clusterRoleAssignmentsSyncer{
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		subscriptionLister:           subscriptionLister,
		stackDesireLister:            stackDesireLister,
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

	return controller
}

func (s *clusterRoleAssignmentsSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	existingCluster, err := s.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return s.deleteStackDesire(ctx, key)
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster from cache: %w", err))
	}

	// Role assignments are scoped to the managed resource group, so Azure
	// deletes them in cascade when that resource group is removed during
	// cluster teardown. Only the bookkeeping StackDesire document needs
	// cleaning up here.
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
		// Role assignment names and the stack target are both scoped to the
		// managed resource group. Nothing can be published yet.
		return nil
	}

	if len(existingServiceProviderCluster.Status.RoleAssignmentsOverManagedResourceGroup) == 0 {
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

// reserveAzureResourceIDs assigns ReservedAzureResourceID to every key in
// RoleAssignmentsOverManagedResourceGroup that does not have one yet, and
// persists the result before returning, so the reserved ID is never lost to
// a crash between being chosen and being used. Keys that already have a
// ReservedAzureResourceID are left untouched: naming is a one-time reservation, never
// recomputed.
func (s *clusterRoleAssignmentsSyncer) reserveAzureResourceIDs(
	ctx context.Context,
	existingCluster *coreapi.Cluster,
	existingServiceProviderCluster *coreapi.ServiceProviderCluster,
	scope *azcorearm.ResourceID,
) (*coreapi.ServiceProviderCluster, error) {
	replacement := existingServiceProviderCluster.DeepCopy()

	for key, status := range replacement.Status.RoleAssignmentsOverManagedResourceGroup {
		if status.ReservedAzureResourceID != nil {
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

	utils.LoggerFromContext(ctx).Info("reserving role assignment resource IDs onto ServiceProviderCluster")
	persisted, err := s.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, replacement, nil)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}
	return persisted, nil
}

// desiredStackDesireSpec builds the StackDesire spec that applies every key
// currently in RoleAssignmentsOverManagedResourceGroup (including draining
// keys still inside their deconfigure wait) as one ARM template.
func (s *clusterRoleAssignmentsSyncer) desiredStackDesireSpec(
	serviceProviderCluster *coreapi.ServiceProviderCluster,
	scope *azcorearm.ResourceID,
	tenantID string,
) (coreapi.StackDesireSpec, error) {
	target, err := azcorearm.ParseResourceID(scope.String() + "/providers/Microsoft.Resources/deploymentStacks/" + RoleAssignmentsStackName)
	if err != nil {
		return coreapi.StackDesireSpec{}, utils.TrackError(fmt.Errorf("failed to parse role assignments stack resource ID: %w", err))
	}

	resources, err := roleAssignmentTemplateResources(serviceProviderCluster.Status.RoleAssignmentsOverManagedResourceGroup)
	if err != nil {
		return coreapi.StackDesireSpec{}, err
	}

	return coreapi.StackDesireSpec{
		TargetResourceID: target,
		Authentication: coreapi.StackDesireAuthentication{
			Type:     coreapi.StackDesireAuthenticationTypeFPA,
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
					// Deny assignments are managed by the separate deny
					// assignment controller, not by this stack.
					Mode: ptr.To(armdeploymentstacks.DenySettingsModeNone),
				},
			},
		},
	}, nil
}

// roleAssignmentsActionOnUnmanage deletes a role assignment from Azure once it
// drops out of the published template, and never touches resource groups or
// management groups — this stack only ever contains role assignments.
func roleAssignmentsActionOnUnmanage() *armdeploymentstacks.ActionOnUnmanage {
	return &armdeploymentstacks.ActionOnUnmanage{
		Resources:        ptr.To(armdeploymentstacks.DeploymentStacksDeleteDetachEnumDelete),
		ResourceGroups:   ptr.To(armdeploymentstacks.DeploymentStacksDeleteDetachEnumDetach),
		ManagementGroups: ptr.To(armdeploymentstacks.DeploymentStacksDeleteDetachEnumDetach),
	}
}

// roleAssignmentTemplateResources renders one Microsoft.Authorization/roleAssignments
// template resource per key, named with that key's ReservedAzureResourceID. Keys
// are sorted so the generated template (and therefore its spec hash) does not
// change from one sync to the next unless the desired set actually changed:
// StackDesireSpecHash retains array order, but Go map iteration order does not.
func roleAssignmentTemplateResources(assignments map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus) ([]map[string]any, error) {
	keys := make([]coreapi.RoleAssignmentKey, 0, len(assignments))
	for key := range assignments {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].PrincipalID != keys[j].PrincipalID {
			return keys[i].PrincipalID < keys[j].PrincipalID
		}
		return keys[i].RoleDefinitionResourceID < keys[j].RoleDefinitionResourceID
	})

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
				"principalId":      key.PrincipalID,
				"roleDefinitionId": key.RoleDefinitionResourceID,
				"principalType":    "ServicePrincipal",
			},
		})
	}
	return resources, nil
}

// publishStackDesire creates the role-assignments StackDesire if it does not
// exist yet, or replaces it only when desiredSpec actually differs from what
// is already published.
func (s *clusterRoleAssignmentsSyncer) publishStackDesire(ctx context.Context, key controllerutils.HCPClusterKey, desiredSpec coreapi.StackDesireSpec, logger logr.Logger) error {
	existing, err := s.stackDesireLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, RoleAssignmentsDesireName)
	if cosmosstorageutils.IsNotFoundError(err) {
		resourceID, err := coreapihelpers.ToStackDesireResourceID(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, RoleAssignmentsDesireName)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to build role assignments StackDesire resource ID: %w", err))
		}
		newDesire := &coreapi.StackDesire{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: key.SubscriptionID,
			},
			Spec: desiredSpec,
		}
		logger.Info("creating role assignments StackDesire")
		_, err = s.resourcesDBClient.StackDesires(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Create(ctx, newDesire, nil)
		if cosmosstorageutils.IsConflictError(err) {
			return nil
		}
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to create role assignments StackDesire: %w", err))
		}
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get role assignments StackDesire: %w", err))
	}

	existingHash, err := coreapi.StackDesireSpecHash(existing.Spec)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to hash existing role assignments StackDesire spec: %w", err))
	}
	desiredHash, err := coreapi.StackDesireSpecHash(desiredSpec)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to hash desired role assignments StackDesire spec: %w", err))
	}
	if existingHash == desiredHash {
		// A document round-tripped through Cosmos JSON storage decodes
		// Template's nested values differently (e.g. []any instead of the
		// []map[string]any a producer builds in memory) than a freshly
		// computed spec, even when semantically identical, so a generic
		// reflect-based comparison would see a spurious difference on every
		// sync. StackDesireSpecHash canonicalizes through JSON for exactly
		// this reason.
		return nil
	}

	replacement := existing.DeepCopy()
	replacement.Spec = desiredSpec
	logger.Info("updating role assignments StackDesire")
	_, err = s.resourcesDBClient.StackDesires(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace role assignments StackDesire: %w", err))
	}
	return nil
}

// requestStackDeletion asks the stack desires controller to delete the Azure
// deployment stack when the desired role assignment set has gone from
// non-empty to empty on a live cluster. It never removes the StackDesire
// document itself here: unlike cluster teardown, nothing else will delete the
// already-applied stack, so abandoning the document would orphan it.
func (s *clusterRoleAssignmentsSyncer) requestStackDeletion(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existing, err := s.stackDesireLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, RoleAssignmentsDesireName)
	if cosmosstorageutils.IsNotFoundError(err) {
		// Never published, so there is nothing to delete.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get role assignments StackDesire: %w", err))
	}
	if existing.Spec.Action == coreapi.StackDesireActionDelete {
		return nil
	}

	replacement := existing.DeepCopy()
	replacement.Spec.Action = coreapi.StackDesireActionDelete
	utils.LoggerFromContext(ctx).Info("requesting deletion of role assignments StackDesire")
	_, err = s.resourcesDBClient.StackDesires(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace role assignments StackDesire: %w", err))
	}
	return nil
}

func (s *clusterRoleAssignmentsSyncer) deleteStackDesire(ctx context.Context, key controllerutils.HCPClusterKey) error {
	err := s.resourcesDBClient.StackDesires(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Delete(ctx, RoleAssignmentsDesireName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to delete role assignments StackDesire: %w", err))
	}
	return nil
}
