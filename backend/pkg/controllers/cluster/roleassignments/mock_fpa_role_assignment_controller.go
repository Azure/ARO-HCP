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

const MockFPARoleAssignmentControllerName = "MockFPARoleAssignment"
const mockFPAOwnerRoleDefinitionID = "/providers/Microsoft.Authorization/roleDefinitions/8e3af657-a8ff-443c-a75c-2fe8c4bcb635"

type resourceGroupsClientBuilder interface {
	ResourceGroupsClient(tenantID, subscriptionID string) (azureclient.ResourceGroupsClient, error)
}

type mockFPARoleAssignmentSyncer struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	subscriptionLister           corelisters.SubscriptionLister
	fpaClientBuilder             resourceGroupsClientBuilder
	clientBuilder                azureclient.ARMPermissionsManagerClientBuilder
	principalID                  string
	clock                        utilsclock.PassiveClock
}

var _ controllerutils.ClusterSyncer = (*mockFPARoleAssignmentSyncer)(nil)

func NewMockFPARoleAssignmentController(clock utilsclock.PassiveClock, resourcesDBClient corecosmosstorage.ResourcesDBClient, clientBuilder azureclient.ARMPermissionsManagerClientBuilder, fpaClientBuilder resourceGroupsClientBuilder, principalID string, informers coreinformers.BackendInformers) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	_, subscriptionLister := informers.Subscriptions()
	syncer := &mockFPARoleAssignmentSyncer{
		resourcesDBClient:            resourcesDBClient,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		subscriptionLister:           subscriptionLister,
		fpaClientBuilder:             fpaClientBuilder,
		clientBuilder:                clientBuilder,
		principalID:                  principalID,
		clock:                        clock,
	}
	controller := controllerutils.NewClusterWatchingController(MockFPARoleAssignmentControllerName, resourcesDBClient, informers, nil, time.Minute, syncer)
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)
	return controller
}

func mockFPARoleAssignmentID(cluster *coreapi.Cluster, principalID string) (*azcorearm.ResourceID, error) {
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
	return azcorearm.ParseResourceID(roleassignment.ManagedResourceGroupScopedRoleAssignmentResourceID(scopeString, principalID, mockFPAOwnerRoleDefinitionID))
}

func (c *mockFPARoleAssignmentSyncer) needsWork(serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	reference := serviceProviderCluster.Status.AzureResources.MockFPARoleAssignment
	earliest := serviceProviderCluster.Spec.EarliestRecheckTimesByController[MockFPARoleAssignmentControllerName]
	return reference.AzureResource == nil || reference.PendingAzureResource != nil ||
		earliest == nil || !c.clock.Now().Before(earliest.Time)
}

func (c *mockFPARoleAssignmentSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
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
	expectedID, assignmentIDErr := mockFPARoleAssignmentID(cluster, c.principalID)
	replacement := existing.DeepCopy()
	replacement.Status.AzureResources.MockFPARoleAssignment = coreapi.AzureReference{PendingAzureResource: expectedID}
	delete(replacement.Spec.EarliestRecheckTimesByController, MockFPARoleAssignmentControllerName)
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
	response, err := client.GetByID(ctx, expectedID.String(), nil)
	if azureclient.IsRoleAssignmentNotFoundErr(err) {
		_, err = client.Create(ctx, scope, expectedID.Name, armauthorization.RoleAssignmentCreateParameters{
			Properties: &armauthorization.RoleAssignmentProperties{
				PrincipalID:      ptr.To(c.principalID),
				RoleDefinitionID: ptr.To(mockFPAOwnerRoleDefinitionID),
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
	properties := response.Properties
	roleDefinitionID := ""
	if properties != nil {
		roleDefinitionID = ptr.Deref(properties.RoleDefinitionID, "")
	}
	if properties == nil || !strings.EqualFold(ptr.Deref(properties.PrincipalID, ""), c.principalID) ||
		(!strings.EqualFold(roleDefinitionID, mockFPAOwnerRoleDefinitionID) && !strings.EqualFold(roleDefinitionID, "/subscriptions/"+cluster.ID.SubscriptionID+mockFPAOwnerRoleDefinitionID)) ||
		!strings.EqualFold(ptr.Deref(properties.Scope, ""), scope) ||
		ptr.Deref(properties.PrincipalType, "") != armauthorization.PrincipalTypeServicePrincipal ||
		ptr.Deref(properties.Condition, "") != "" {
		return utils.TrackError(fmt.Errorf("role assignment %s does not grant unconditional Owner to the intended mock FPA", expectedID))
	}
	replacement.Status.AzureResources.MockFPARoleAssignment = coreapi.AzureReference{AzureResource: expectedID}
	if replacement.Spec.EarliestRecheckTimesByController == nil {
		replacement.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{}
	}
	recheckAt := metav1.NewTime(c.clock.Now().Add(wait.Jitter(roleAssignmentRecheckInterval, roleAssignmentRecheckJitterFactor)))
	replacement.Spec.EarliestRecheckTimesByController[MockFPARoleAssignmentControllerName] = &recheckAt
	_, err = storage.Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	return utils.TrackError(err)
}
