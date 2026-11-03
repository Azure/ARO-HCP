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
	"k8s.io/apimachinery/pkg/util/wait"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/azure/roleassignment"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	MockFPAOwnerRoleAssignmentControllerName = "MockFPAOwnerRoleAssignment"
	// ownerRoleDefinitionID identifies Azure's built-in Owner role, used here to give
	// the mock FPA Owner permissions on a cluster's managed resource group.
	// https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles/privileged#owner
	ownerRoleDefinitionID = "/providers/Microsoft.Authorization/roleDefinitions/8e3af657-a8ff-443c-a75c-2fe8c4bcb635"
)

type resourceGroupsClientBuilder interface {
	ResourceGroupsClient(tenantID, subscriptionID string) (azureclient.ResourceGroupsClient, error)
}

type mockFPAOwnerRoleAssignmentSyncer struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	subscriptionLister           corelisters.SubscriptionLister
	fpaClientBuilder             resourceGroupsClientBuilder
	clientBuilder                azureclient.ARMPermissionsManagerClientBuilder
	principalID                  string
	clock                        utilsclock.PassiveClock
}

var _ controllerutils.ClusterSyncer = (*mockFPAOwnerRoleAssignmentSyncer)(nil)

func NewMockFPAOwnerRoleAssignmentController(clock utilsclock.PassiveClock, resourcesDBClient corecosmosstorage.ResourcesDBClient, clientBuilder azureclient.ARMPermissionsManagerClientBuilder, fpaClientBuilder resourceGroupsClientBuilder, principalID string, informers coreinformers.BackendInformers) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	_, subscriptionLister := informers.Subscriptions()
	syncer := &mockFPAOwnerRoleAssignmentSyncer{
		resourcesDBClient:            resourcesDBClient,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		subscriptionLister:           subscriptionLister,
		fpaClientBuilder:             fpaClientBuilder,
		clientBuilder:                clientBuilder,
		principalID:                  principalID,
		clock:                        clock,
	}
	controller := controllerutils.NewClusterWatchingController(MockFPAOwnerRoleAssignmentControllerName, resourcesDBClient, informers, nil, time.Minute, syncer)
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)
	return controller
}

func mockFPAOwnerRoleAssignmentID(cluster *coreapi.Cluster, principalID string) (*azcorearm.ResourceID, error) {
	managedResourceGroupName := cluster.CustomerProperties.Platform.ManagedResourceGroup
	if len(managedResourceGroupName) == 0 || len(principalID) == 0 {
		return nil, fmt.Errorf("managed resource group name and mock FPA principal ID are required")
	}
	scopeString := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", strings.ToLower(cluster.ID.SubscriptionID), managedResourceGroupName)
	scope, err := azcorearm.ParseResourceID(scopeString)
	if err != nil {
		return nil, err
	}
	if scope.ResourceType.String() != azcorearm.ResourceGroupResourceType.String() || scope.ResourceGroupName != managedResourceGroupName || scope.String() != scopeString {
		return nil, fmt.Errorf("invalid managed resource group scope %q", scopeString)
	}
	return azcorearm.ParseResourceID(roleassignment.ManagedResourceGroupScopedRoleAssignmentResourceID(scopeString, principalID, ownerRoleDefinitionID))
}

func (c *mockFPAOwnerRoleAssignmentSyncer) needsWork(serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	reference := serviceProviderCluster.Status.AzureResources.MockFPAOwnerRoleAssignment
	earliest := serviceProviderCluster.Spec.EarliestRecheckTimesByController[MockFPAOwnerRoleAssignmentControllerName]
	return reference.AzureResource == nil || reference.PendingAzureResource != nil ||
		earliest == nil || !c.clock.Now().Before(earliest.Time)
}

func (c *mockFPAOwnerRoleAssignmentSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	existing, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if !c.needsWork(existing) {
		return nil
	}
	expectedID, assignmentIDErr := mockFPAOwnerRoleAssignmentID(cluster, c.principalID)
	replacement := existing.DeepCopy()
	replacement.Status.AzureResources.MockFPAOwnerRoleAssignment = coreapi.AzureReference{PendingAzureResource: expectedID}
	delete(replacement.Spec.EarliestRecheckTimesByController, MockFPAOwnerRoleAssignmentControllerName)
	storage := c.resourcesDBClient.ServiceProviderClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name)
	if controllerutil.NeedsUpdate(existing, replacement) {
		replacement, err = storage.Replace(ctx, replacement, nil)
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			return nil
		}
		if err != nil {
			return utils.TrackError(err)
		}
	}
	if assignmentIDErr != nil {
		return utils.TrackError(assignmentIDErr)
	}
	subscription, err := c.subscriptionLister.Get(ctx, cluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get subscription %q: %w", cluster.ID.SubscriptionID, err))
	}
	if subscription.Properties == nil || subscription.Properties.TenantId == nil {
		return utils.TrackError(fmt.Errorf("subscription %q has no tenant ID", cluster.ID.SubscriptionID))
	}
	rgClient, err := c.fpaClientBuilder.ResourceGroupsClient(*subscription.Properties.TenantId, cluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build resource groups client: %w", err))
	}
	resourceGroup, err := rgClient.Get(ctx, cluster.CustomerProperties.Platform.ManagedResourceGroup, nil)
	if azureclient.IsResourceGroupNotFoundErr(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get managed resource group: %w", err))
	}
	if resourceGroup.Properties == nil || resourceGroup.Properties.ProvisioningState == nil {
		return utils.TrackError(fmt.Errorf("managed resource group has no provisioning state"))
	}
	if strings.EqualFold(*resourceGroup.Properties.ProvisioningState, string(armresources.ProvisioningStateDeleting)) {
		return nil
	}
	client, err := c.clientBuilder.RoleAssignmentsClient(cluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build ARM helper role assignments client: %w", err))
	}
	scope := expectedID.Parent.String()
	_, err = client.GetByID(ctx, expectedID.String(), nil)
	if azureclient.IsRoleAssignmentNotFoundErr(err) {
		_, err = client.Create(ctx, scope, expectedID.Name, armauthorization.RoleAssignmentCreateParameters{
			Properties: &armauthorization.RoleAssignmentProperties{
				PrincipalID:      ptr.To(c.principalID),
				RoleDefinitionID: ptr.To(ownerRoleDefinitionID),
				PrincipalType:    ptr.To(armauthorization.PrincipalTypeServicePrincipal),
			},
		}, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to create mock FPA Owner assignment: %w", err))
		}
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get mock FPA Owner assignment: %w", err))
	}
	replacement.Status.AzureResources.MockFPAOwnerRoleAssignment = coreapi.AzureReference{AzureResource: expectedID}
	if replacement.Spec.EarliestRecheckTimesByController == nil {
		replacement.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{}
	}
	recheckAt := metav1.NewTime(c.clock.Now().Add(wait.Jitter(roleAssignmentRecheckInterval, roleAssignmentRecheckJitterFactor)))
	replacement.Spec.EarliestRecheckTimesByController[MockFPAOwnerRoleAssignmentControllerName] = &recheckAt
	_, err = storage.Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	return utils.TrackError(err)
}
