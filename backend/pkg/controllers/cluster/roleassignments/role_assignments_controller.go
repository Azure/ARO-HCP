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
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks/v2"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/roleassignment"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/azureresourcesapihelpers"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/azure"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/azureresourcescosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/azureresourcesinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/azureresourceslisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// RoleAssignmentsControllerName is shared by the workqueue, logs and status writer.
	RoleAssignmentsControllerName = "IdentityRoleAssignments"

	// RoleAssignmentsAzureDeploymentStackDesireName identifies the cluster's complete role assignment intent.
	RoleAssignmentsAzureDeploymentStackDesireName = "role-assignments-over-managed-resource-group"
	// RoleAssignmentDeploymentStackDesireReconcileInterval is set on every role assignments
	// DeploymentStackDesire's Spec.ReconcileInterval: the cooldown azuredeploymentstackdesires'
	// controller waits before re-checking unchanged, already-settled (successful or invalid)
	// intent. It is set explicitly here even though it matches that controller's own
	// defaultInterval fallback, so this producer's cadence does not silently change if that
	// default is ever retuned.
	RoleAssignmentDeploymentStackDesireReconcileInterval = 6 * time.Hour

	// RoleAssignmentsAzureDeploymentStackName is the name of the Azure Deployment Stack that manages the role assignments over the managed
	// resource group. It is stable within each cluster's managed resource group.
	RoleAssignmentsAzureDeploymentStackName = "role-assignments-over-managed-resource-group"
)

// roleAssignmentsSyncer MANAGES the Azure role assignments on a cluster's managed
// resource group (MRG) for each control-plane operator, data-plane operator, and the
// service managed identity: it does so by managing an Azure Deployment Stack that contains
// them. The Azure Deployment Stack is created in the Cluster's Managed Resource Group.
// Existing deterministic assignment names allow the stack to adopt current assignments.
// Historical assignments outside the desired set are not discovered or adopted.
type roleAssignmentsSyncer struct {
	resourcesDBClient             corecosmosstorage.ResourcesDBClient
	azureResourcesDBClient        azureresourcescosmosstorage.AzureResourcesDBClient
	clusterLister                 corelisters.ClusterLister
	serviceProviderClusterLister  corelisters.ServiceProviderClusterLister
	subscriptionLister            corelisters.SubscriptionLister
	stackDesireLister             azureresourceslisters.DeploymentStackDesireLister
	clusterScopedIdentitiesConfig *azure.ClusterScopedIdentitiesConfig
}

var _ controllerutils.ClusterSyncer = (*roleAssignmentsSyncer)(nil)

func NewRoleAssignmentsController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	azureResourcesDBClient azureresourcescosmosstorage.AzureResourcesDBClient,
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister,
	subscriptionLister corelisters.SubscriptionLister,
	clusterScopedIdentitiesConfig *azure.ClusterScopedIdentitiesConfig,
	informers coreinformers.BackendInformers,
	azureResourcesInformers azureresourcesinformers.AzureResourcesInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	stackInformer, stackLister := azureResourcesInformers.DeploymentStackDesires()

	syncer := &roleAssignmentsSyncer{
		resourcesDBClient:             resourcesDBClient,
		azureResourcesDBClient:        azureResourcesDBClient,
		clusterLister:                 clusterLister,
		serviceProviderClusterLister:  serviceProviderClusterLister,
		subscriptionLister:            subscriptionLister,
		stackDesireLister:             stackLister,
		clusterScopedIdentitiesConfig: clusterScopedIdentitiesConfig,
	}
	controller := controllerutils.NewClusterWatchingController(
		RoleAssignmentsControllerName, resourcesDBClient, informers, kubeApplierInformers, 5*time.Minute, syncer,
	)
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)
	controller.AddInterestingResource(azcorearm.SubscriptionResourceType, "*")

	if err := controller.QueueForChildInformers(5*time.Minute, stackInformer); err != nil {
		panic(err)
	}
	controller.AddInterestingResource(azureresourcesapi.ClusterScopedDeploymentStackDesireResourceType, RoleAssignmentsAzureDeploymentStackDesireName)

	return controller
}

func (c *roleAssignmentsSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	clusterMissing := cosmosstorageutils.IsNotFoundError(err)
	desiresCRUD, err := c.azureResourcesDBClient.DeploymentStackDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to resolve DeploymentStackDesire CRUD: %w", err))
	}
	if clusterMissing || cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		// MRG deletion removes the stack and assignments. Stop publishing immediately;
		// do not ask the generic applier to issue an Azure DELETE during cluster teardown.
		// Skip the Delete call once the lister already shows the desire is gone, to avoid
		// a redundant Cosmos write on every reconcile for the rest of the teardown window.
		// A stale cache here only delays purging this document by one reconcile (the
		// lister catching up re-triggers SyncOnce, since the desire is a watched
		// interesting resource); the actual Azure role assignments are removed by MRG
		// deletion regardless of this document's lifecycle, so that delay is harmless.
		_, lookupErr := c.stackDesireLister.GetByResourceID(ctx, azureresourcesapihelpers.ToClusterScopedDeploymentStackDesireResourceIDString(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, RoleAssignmentsAzureDeploymentStackDesireName))
		if cosmosstorageutils.IsNotFoundError(lookupErr) {
			return nil
		}
		if err := desiresCRUD.Delete(ctx, RoleAssignmentsAzureDeploymentStackDesireName); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("failed to purge role assignment StackDesire: %w", err))
		}
		return nil
	}
	spc, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}
	if spc.Status.AzureResources.ManagedResourceGroup.AzureResource == nil || !c.principalIDsResolvable(cluster, spc) {
		// Never publish a partial template: resources omitted from it would be deleted.
		err := c.setStackReady(ctx, cluster, spc, false)
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			return nil
		}
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to set stack ready: %w", err))
		}
		return nil
	}
	desiredDeploymentStackDesireSpec, err := c.desiredDeploymentStackSpec(ctx, cluster, spc)
	if err != nil {
		innerErr := c.setStackReady(ctx, cluster, spc, false)
		if cosmosstorageutils.IsPreconditionFailedError(innerErr) {
			return utils.TrackError(err)
		}
		if innerErr != nil {
			return errors.Join(utils.TrackError(fmt.Errorf("failed to set stack ready: %w", innerErr)), err)
		}
		return utils.TrackError(err)
	}
	desiredDeploymentStackDesireSpecHash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(desiredDeploymentStackDesireSpec)
	if err != nil {
		innerErr := c.setStackReady(ctx, cluster, spc, false)
		if cosmosstorageutils.IsPreconditionFailedError(innerErr) {
			return utils.TrackError(err)
		}
		if innerErr != nil {
			return errors.Join(utils.TrackError(fmt.Errorf("failed to set stack ready: %w", innerErr)), err)
		}
		return utils.TrackError(err)
	}
	existingDeploymentStackDesire, err := c.stackDesireLister.GetByResourceID(ctx, azureresourcesapihelpers.ToClusterScopedDeploymentStackDesireResourceIDString(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, RoleAssignmentsAzureDeploymentStackDesireName))
	if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to get DeploymentStackDesire: %w", err))
	}
	if cosmosstorageutils.IsNotFoundError(err) {
		existingDeploymentStackDesire = nil
	}
	ready := false
	if existingDeploymentStackDesire != nil {
		existingDeploymentStackDesireSpecHash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(existingDeploymentStackDesire.Spec)
		if err != nil {
			innerErr := c.setStackReady(ctx, cluster, spc, false)
			if cosmosstorageutils.IsPreconditionFailedError(innerErr) {
				return utils.TrackError(err)
			}
			if innerErr != nil {
				return utils.TrackError(fmt.Errorf("failed to set stack ready: %w", err))
			}
			return utils.TrackError(err)
		}
		stack := existingDeploymentStackDesire.Status.Stack
		ready = existingDeploymentStackDesireSpecHash == desiredDeploymentStackDesireSpecHash && existingDeploymentStackDesire.Status.ObservedSpecHash == desiredDeploymentStackDesireSpecHash &&
			meta.IsStatusConditionTrue(existingDeploymentStackDesire.Status.Conditions, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied) &&
			stack != nil && stack.Properties != nil && stack.Properties.ProvisioningState != nil &&
			strings.EqualFold(string(*stack.Properties.ProvisioningState), string(armdeploymentstacks.DeploymentStackProvisioningStateSucceeded))
	}

	// We set the stack ready status to the desired value. Doing this at this point
	// ensures that we submit the stack readiness status in Cosmos before publishing the changed template.
	err = c.setStackReady(ctx, cluster, spc, ready)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return utils.TrackError(err)
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to set stack ready: %w", err))
	}

	if existingDeploymentStackDesire == nil {
		resourceID, err := azureresourcesapihelpers.ToClusterScopedDeploymentStackDesireResourceID(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, RoleAssignmentsAzureDeploymentStackDesireName)
		if err != nil {
			return utils.TrackError(err)
		}
		_, err = desiresCRUD.Create(ctx, &azureresourcesapi.DeploymentStackDesire{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(key.SubscriptionID)},
			Spec:           desiredDeploymentStackDesireSpec,
		}, nil)
		return err
	}

	// At this point we know that the DeploymentStackDesire exists and we need to update it if has changed.

	replacement := existingDeploymentStackDesire.DeepCopy()
	replacement.Spec = desiredDeploymentStackDesireSpec
	if !controllerutil.NeedsUpdate(existingDeploymentStackDesire, replacement) {
		return nil
	}
	_, err = desiresCRUD.Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace DeploymentStackDesire: %w", err))
	}
	return nil
}

// setStackReady updates the stack ready status in Cosmos. If the updatei in Cosmos fails it returns an error. The error
// returned can be a precondition failed error. Consumers should check for this and return nil if it is the case.
func (c *roleAssignmentsSyncer) setStackReady(ctx context.Context, cluster *coreapi.Cluster, spc *coreapi.ServiceProviderCluster, ready bool) error {
	if spc.Status.AzureResources.RoleAssignments.StackReady == ready {
		return nil
	}
	replacement := spc.DeepCopy()
	replacement.Status.AzureResources.RoleAssignments.StackReady = ready
	_, err := c.resourcesDBClient.ServiceProviderClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name).Replace(ctx, replacement, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to update role assignment readiness: %w", err))
	}
	return nil
}

func (c *roleAssignmentsSyncer) desiredDeploymentStackSpec(ctx context.Context, cluster *coreapi.Cluster, spc *coreapi.ServiceProviderCluster) (azureresourcesapi.DeploymentStackDesireSpec, error) {
	expected, err := c.expectedRoleAssignments(cluster, spc)
	if err != nil {
		return azureresourcesapi.DeploymentStackDesireSpec{}, err
	}
	subscription, err := c.subscriptionLister.Get(ctx, cluster.ID.SubscriptionID)
	if err != nil {
		return azureresourcesapi.DeploymentStackDesireSpec{}, fmt.Errorf("failed to get subscription: %w", err)
	}
	if subscription.Properties == nil || subscription.Properties.TenantId == nil || len(*subscription.Properties.TenantId) == 0 {
		return azureresourcesapi.DeploymentStackDesireSpec{}, fmt.Errorf("subscription %q has no tenant ID", cluster.ID.SubscriptionID)
	}
	scope, err := coreapihelpers.ToResourceGroupResourceID(cluster.ID.SubscriptionID, cluster.CustomerProperties.Platform.ManagedResourceGroup)
	if err != nil {
		return azureresourcesapi.DeploymentStackDesireSpec{}, err
	}
	target, err := azcorearm.ParseResourceID(scope.String() + "/providers/Microsoft.Resources/deploymentStacks/" + RoleAssignmentsAzureDeploymentStackName)
	if err != nil {
		return azureresourcesapi.DeploymentStackDesireSpec{}, err
	}
	resources := make([]any, 0, len(expected))
	for _, assignment := range expected {
		resources = append(resources, map[string]any{
			"type":       "Microsoft.Authorization/roleAssignments",
			"apiVersion": "2022-04-01",
			"name":       assignment.name,
			"properties": map[string]any{
				"principalId":      assignment.principalID,
				"roleDefinitionId": assignment.roleDefinitionID,
				"principalType":    "ServicePrincipal",
			},
		})
	}

	// Resource-group-scoped stacks use the resource group's location.
	return azureresourcesapi.DeploymentStackDesireSpec{
		TargetResourceID: target,
		Authentication: azureresourcesapi.DeploymentStackDesireAuthentication{
			Type:                  azureresourcesapi.DeploymentStackDesireAuthenticationTypeFPA,
			FirstPartyApplication: &azureresourcesapi.DeploymentStackDesireAuthenticationFirstPartyApplication{TenantID: *subscription.Properties.TenantId},
		},
		Action: azureresourcesapi.DeploymentStackDesireActionTypeApply,
		Stack: &armdeploymentstacks.DeploymentStack{
			Properties: &armdeploymentstacks.DeploymentStackProperties{
				ActionOnUnmanage: &armdeploymentstacks.ActionOnUnmanage{
					Resources:        ptr.To(armdeploymentstacks.UnmanageActionResourceModeDelete),
					ResourceGroups:   ptr.To(armdeploymentstacks.UnmanageActionResourceGroupModeDetach),
					ManagementGroups: ptr.To(armdeploymentstacks.UnmanageActionManagementGroupModeDetach),
				},
				DenySettings: &armdeploymentstacks.DenySettings{Mode: ptr.To(armdeploymentstacks.DenySettingsModeNone)},
				Template: map[string]any{
					// $schema is the ARM Template schema version. At the moment of writing this (2026-11-03)
					// https://learn.microsoft.com/en-us/azure/azure-resource-manager/templates/syntax#template-format recommends using 2019-04-01 for
					// resource group scoped deployments.
					"$schema":        "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
					"contentVersion": "1.0.0.0",
					"resources":      resources,
				},
			},
		},
		ReconcileInterval: metav1.Duration{Duration: RoleAssignmentDeploymentStackDesireReconcileInterval},
	}, nil
}

func (c *roleAssignmentsSyncer) principalIDsResolvable(cluster *coreapi.Cluster, spc *coreapi.ServiceProviderCluster) bool {
	identities := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities
	for _, id := range identities.ControlPlaneOperators {
		if _, ok := controlPlaneOperatorPrincipalID(spc, id); !ok {
			return false
		}
	}
	for _, id := range identities.DataPlaneOperators {
		if _, ok := dataPlaneOperatorPrincipalID(spc, id); !ok {
			return false
		}
	}
	_, ok := serviceManagedIdentityPrincipalID(spc, identities.ServiceManagedIdentity)
	return ok
}

type roleAssignmentDefinition struct {
	resourceID       *azcorearm.ResourceID
	name             string
	principalID      string
	roleDefinitionID string
}

// expectedRoleAssignments resolves the complete configured set, deduplicated and
// sorted by deterministic resource ID to keep the stack content stable.
func (c *roleAssignmentsSyncer) expectedRoleAssignments(cluster *coreapi.Cluster, spc *coreapi.ServiceProviderCluster) ([]roleAssignmentDefinition, error) {
	if len(cluster.CustomerProperties.Platform.ManagedResourceGroup) == 0 {
		return nil, fmt.Errorf("managed resource group name is empty for cluster %q", cluster.ID.String())
	}
	scope, err := coreapihelpers.ToResourceGroupResourceID(cluster.ID.SubscriptionID, cluster.CustomerProperties.Platform.ManagedResourceGroup)
	if err != nil {
		return nil, err
	}
	identities := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities
	var expected []roleAssignmentDefinition
	for operator, id := range identities.ControlPlaneOperators {
		principal, ok := controlPlaneOperatorPrincipalID(spc, id)
		if !ok {
			return nil, fmt.Errorf("principal ID not yet resolved for control plane operator %q", operator)
		}
		config := c.clusterScopedIdentitiesConfig.ControlPlaneOperatorsIdentities[azure.ClusterOperatorIdentifier(operator)]
		if config == nil || len(config.RoleDefinitionsResourceIDs()) == 0 {
			return nil, fmt.Errorf("no role definitions configured for control plane operator %q", operator)
		}
		expected, err = appendRoleAssignments(expected, scope.String(), principal, config.RoleDefinitionsResourceIDs())
		if err != nil {
			return nil, err
		}
	}
	for operator, id := range identities.DataPlaneOperators {
		principal, ok := dataPlaneOperatorPrincipalID(spc, id)
		if !ok {
			return nil, fmt.Errorf("principal ID not yet resolved for data plane operator %q", operator)
		}
		config := c.clusterScopedIdentitiesConfig.DataPlaneOperatorsIdentities[azure.ClusterOperatorIdentifier(operator)]
		if config == nil || len(config.RoleDefinitionsResourceIDs()) == 0 {
			return nil, fmt.Errorf("no role definitions configured for data plane operator %q", operator)
		}
		expected, err = appendRoleAssignments(expected, scope.String(), principal, config.RoleDefinitionsResourceIDs())
		if err != nil {
			return nil, err
		}
	}
	principal, ok := serviceManagedIdentityPrincipalID(spc, identities.ServiceManagedIdentity)
	if !ok {
		return nil, fmt.Errorf("principal ID not yet resolved for service managed identity")
	}
	config := c.clusterScopedIdentitiesConfig.ServiceManagedIdentity
	if config == nil || len(config.RoleDefinitionsResourceIDs()) == 0 {
		return nil, fmt.Errorf("no role definitions configured for service managed identity")
	}
	expected, err = appendRoleAssignments(expected, scope.String(), principal, config.RoleDefinitionsResourceIDs())
	if err != nil {
		return nil, err
	}
	slices.SortFunc(expected, func(a, b roleAssignmentDefinition) int {
		return strings.Compare(strings.ToLower(a.resourceID.String()), strings.ToLower(b.resourceID.String()))
	})
	return expected, nil
}

func controlPlaneOperatorPrincipalID(spc *coreapi.ServiceProviderCluster, id *azcorearm.ResourceID) (string, bool) {
	identity := spc.Status.MSIManagedIdentities.ControlPlaneOperatorsIdentities[strings.ToLower(id.String())]
	if identity == nil || identity.PrincipalID == nil || len(*identity.PrincipalID) == 0 {
		return "", false
	}
	return *identity.PrincipalID, true
}

func dataPlaneOperatorPrincipalID(spc *coreapi.ServiceProviderCluster, id *azcorearm.ResourceID) (string, bool) {
	identity := spc.Status.DataPlaneOperatorsManagedIdentities.Identities[strings.ToLower(id.String())]
	if identity == nil || identity.PrincipalID == nil || len(*identity.PrincipalID) == 0 {
		return "", false
	}
	return *identity.PrincipalID, true
}

func serviceManagedIdentityPrincipalID(spc *coreapi.ServiceProviderCluster, id *azcorearm.ResourceID) (string, bool) {
	identity := spc.Status.MSIManagedIdentities.ServiceManagedIdentity
	if identity == nil || identity.ResourceID == nil || !strings.EqualFold(identity.ResourceID.String(), id.String()) || identity.PrincipalID == nil || len(*identity.PrincipalID) == 0 {
		return "", false
	}
	return *identity.PrincipalID, true
}

func appendRoleAssignments(expected []roleAssignmentDefinition, scope, principalID string, roleDefinitionIDs []*azcorearm.ResourceID) ([]roleAssignmentDefinition, error) {
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
		if slices.ContainsFunc(expected, func(e roleAssignmentDefinition) bool {
			return controllerutil.ResourceIDsEqual(e.resourceID, parsed)
		}) {
			continue
		}
		expected = append(expected, roleAssignmentDefinition{resourceID: parsed, name: name, principalID: principalID, roleDefinitionID: roleDefID})
	}
	return expected, nil
}
