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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/roleassignment"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/azure"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// RoleAssignmentsControllerName is shared by the workqueue, logs and status writer.
	RoleAssignmentsControllerName = "IdentityRoleAssignments"
	// RoleAssignmentsDesireName identifies the cluster's complete role assignment intent.
	RoleAssignmentsDesireName = "role-assignments"
	// RoleAssignmentsStackName is stable within each cluster's managed resource group.
	RoleAssignmentsStackName      = "aro-hcp-role-assignments"
	roleAssignmentRecheckInterval = 6 * time.Hour
)

// roleAssignmentsSyncer publishes one authoritative deployment stack in the MRG.
// Existing deterministic assignment names allow the stack to adopt current assignments.
// Historical assignments outside the desired set are not discovered or adopted.
type roleAssignmentsSyncer struct {
	resourcesDBClient             corecosmosstorage.ResourcesDBClient
	clusterLister                 corelisters.ClusterLister
	serviceProviderClusterLister  corelisters.ServiceProviderClusterLister
	subscriptionLister            corelisters.SubscriptionLister
	stackDesireLister             corelisters.StackDesireLister
	clusterScopedIdentitiesConfig *azure.ClusterScopedIdentitiesConfig
}

var _ controllerutils.ClusterSyncer = (*roleAssignmentsSyncer)(nil)

func NewRoleAssignmentsController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister,
	subscriptionLister corelisters.SubscriptionLister,
	clusterScopedIdentitiesConfig *azure.ClusterScopedIdentitiesConfig,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	stackInformer, stackLister := informers.StackDesires()
	syncer := &roleAssignmentsSyncer{
		resourcesDBClient:             resourcesDBClient,
		clusterLister:                 clusterLister,
		serviceProviderClusterLister:  serviceProviderClusterLister,
		subscriptionLister:            subscriptionLister,
		stackDesireLister:             stackLister,
		clusterScopedIdentitiesConfig: clusterScopedIdentitiesConfig,
	}
	controller := controllerutils.NewClusterWatchingController(
		RoleAssignmentsControllerName, resourcesDBClient, informers, kubeApplierInformers, 5*time.Minute, syncer,
	)
	if err := controller.QueueForInformers(5*time.Minute, stackInformer); err != nil {
		panic(err)
	}
	return controller
}

func (c *roleAssignmentsSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	desires := c.resourcesDBClient.StackDesires(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) || cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		// MRG deletion removes the stack and assignments. Stop publishing immediately;
		// do not ask the generic applier to issue an Azure DELETE during cluster teardown.
		if err := desires.Delete(ctx, RoleAssignmentsDesireName); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
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
		return c.setStackReady(ctx, cluster, spc, false)
	}
	spec, err := c.desiredSpec(ctx, cluster, spc)
	if err != nil {
		return errors.Join(utils.TrackError(err), c.setStackReady(ctx, cluster, spc, false))
	}
	hash, err := coreapi.StackDesireSpecHash(spec)
	if err != nil {
		return errors.Join(utils.TrackError(err), c.setStackReady(ctx, cluster, spc, false))
	}
	existing, err := c.stackDesireLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, RoleAssignmentsDesireName)
	if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to get StackDesire: %w", err))
	}
	if cosmosstorageutils.IsNotFoundError(err) {
		existing = nil
	}
	ready := false
	if existing != nil {
		existingHash, err := coreapi.StackDesireSpecHash(existing.Spec)
		if err != nil {
			return errors.Join(utils.TrackError(err), c.setStackReady(ctx, cluster, spc, false))
		}
		stack := existing.Status.Stack
		ready = existingHash == hash && existing.Status.ObservedSpecHash == hash &&
			meta.IsStatusConditionTrue(existing.Status.Conditions, coreapi.StackDesireConditionSuccessfullyApplied) &&
			stack != nil && stack.Properties != nil && stack.Properties.ProvisioningState != nil &&
			strings.EqualFold(string(*stack.Properties.ProvisioningState), string(armdeploymentstacks.DeploymentStackProvisioningStateSucceeded))
	}
	// Revoke stale readiness before publishing a changed template.
	if err := c.setStackReady(ctx, cluster, spc, ready); err != nil {
		return err
	}
	var replacement *coreapi.StackDesire
	if existing != nil {
		replacement = existing.DeepCopy()
		replacement.Spec = spec
		if !controllerutil.NeedsUpdate(existing, replacement) {
			return nil
		}
	}
	// Recheck the parent before writing intent: the informer can lag deletion.
	// This narrows the race, but is not a transaction with the following write.
	liveCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to recheck Cluster before publishing StackDesire: %w", err))
	}
	if cosmosstorageutils.IsNotFoundError(err) || liveCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		if err := desires.Delete(ctx, RoleAssignmentsDesireName); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("failed to purge role assignment StackDesire: %w", err))
		}
		return nil
	}
	if existing == nil {
		resourceID, err := azcorearm.ParseResourceID(cluster.ID.String() + "/" + coreapi.StackDesireResourceTypeName + "/" + RoleAssignmentsDesireName)
		if err != nil {
			return utils.TrackError(err)
		}
		_, err = desires.Create(ctx, &coreapi.StackDesire{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(key.SubscriptionID)},
			Spec:           spec,
		}, nil)
		return err
	}
	_, err = desires.Replace(ctx, replacement, nil)
	return err
}

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

func (c *roleAssignmentsSyncer) desiredSpec(ctx context.Context, cluster *coreapi.Cluster, spc *coreapi.ServiceProviderCluster) (coreapi.StackDesireSpec, error) {
	expected, err := c.expectedRoleAssignments(cluster, spc)
	if err != nil {
		return coreapi.StackDesireSpec{}, err
	}
	subscription, err := c.subscriptionLister.Get(ctx, cluster.ID.SubscriptionID)
	if err != nil {
		return coreapi.StackDesireSpec{}, fmt.Errorf("failed to get subscription: %w", err)
	}
	if subscription.Properties == nil || subscription.Properties.TenantId == nil || len(*subscription.Properties.TenantId) == 0 {
		return coreapi.StackDesireSpec{}, fmt.Errorf("subscription %q has no tenant ID", cluster.ID.SubscriptionID)
	}
	scope, err := coreapihelpers.ToResourceGroupResourceID(cluster.ID.SubscriptionID, cluster.CustomerProperties.Platform.ManagedResourceGroup)
	if err != nil {
		return coreapi.StackDesireSpec{}, err
	}
	target, err := azcorearm.ParseResourceID(scope.String() + "/providers/Microsoft.Resources/deploymentStacks/" + RoleAssignmentsStackName)
	if err != nil {
		return coreapi.StackDesireSpec{}, err
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
	return coreapi.StackDesireSpec{
		TargetResourceID: target,
		Authentication:   coreapi.StackDesireAuthentication{Type: coreapi.StackDesireAuthenticationTypeFPA, TenantID: *subscription.Properties.TenantId},
		Action:           coreapi.StackDesireActionApply,
		Stack: &armdeploymentstacks.DeploymentStack{
			Properties: &armdeploymentstacks.DeploymentStackProperties{
				ActionOnUnmanage: &armdeploymentstacks.ActionOnUnmanage{
					Resources:        ptr.To(armdeploymentstacks.DeploymentStacksDeleteDetachEnumDelete),
					ResourceGroups:   ptr.To(armdeploymentstacks.DeploymentStacksDeleteDetachEnumDetach),
					ManagementGroups: ptr.To(armdeploymentstacks.DeploymentStacksDeleteDetachEnumDetach),
				},
				DenySettings: &armdeploymentstacks.DenySettings{Mode: ptr.To(armdeploymentstacks.DenySettingsModeNone)},
				Template: map[string]any{
					"$schema":        "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
					"contentVersion": "1.0.0.0",
					"resources":      resources,
				},
			},
		},
		ReconcileInterval: metav1.Duration{Duration: roleAssignmentRecheckInterval},
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
	if id == nil {
		return "", false
	}
	identity := spc.Status.MSIManagedIdentities.ControlPlaneOperatorsIdentities[strings.ToLower(id.String())]
	if identity == nil || identity.PrincipalID == nil || len(*identity.PrincipalID) == 0 {
		return "", false
	}
	return *identity.PrincipalID, true
}

func dataPlaneOperatorPrincipalID(spc *coreapi.ServiceProviderCluster, id *azcorearm.ResourceID) (string, bool) {
	if id == nil {
		return "", false
	}
	identity := spc.Status.DataPlaneOperatorsManagedIdentities.Identities[strings.ToLower(id.String())]
	if identity == nil || identity.PrincipalID == nil || len(*identity.PrincipalID) == 0 {
		return "", false
	}
	return *identity.PrincipalID, true
}

func serviceManagedIdentityPrincipalID(spc *coreapi.ServiceProviderCluster, id *azcorearm.ResourceID) (string, bool) {
	identity := spc.Status.MSIManagedIdentities.ServiceManagedIdentity
	if id == nil || identity == nil || identity.ResourceID == nil || !strings.EqualFold(identity.ResourceID.String(), id.String()) || identity.PrincipalID == nil || len(*identity.PrincipalID) == 0 {
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
