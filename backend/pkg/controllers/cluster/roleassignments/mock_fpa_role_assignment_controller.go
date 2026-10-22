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

type mockFPARoleAssignmentSyncer struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	clientBuilder                azureclient.ARMPermissionsManagerClientBuilder
	principalID                  string
	clock                        utilsclock.PassiveClock
}

var _ controllerutils.ClusterSyncer = (*mockFPARoleAssignmentSyncer)(nil)

func NewMockFPARoleAssignmentController(clock utilsclock.PassiveClock, resourcesDBClient corecosmosstorage.ResourcesDBClient, clientBuilder azureclient.ARMPermissionsManagerClientBuilder, principalID string, informers coreinformers.BackendInformers) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	syncer := &mockFPARoleAssignmentSyncer{
		resourcesDBClient:            resourcesDBClient,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		clientBuilder:                clientBuilder,
		principalID:                  principalID,
		clock:                        clock,
	}
	controller := controllerutils.NewClusterWatchingController(MockFPARoleAssignmentControllerName, resourcesDBClient, informers, nil, time.Minute, syncer)
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)
	return controller
}

func mockFPARoleAssignmentID(serviceProviderCluster *coreapi.ServiceProviderCluster, principalID string) (*azcorearm.ResourceID, error) {
	scope := serviceProviderCluster.Status.AzureResources.ManagedResourceGroup.AzureResource
	if scope == nil || len(principalID) == 0 {
		return nil, fmt.Errorf("confirmed managed resource group and mock FPA principal ID are required")
	}
	return azcorearm.ParseResourceID(roleassignment.ManagedResourceGroupScopedRoleAssignmentResourceID(scope.String(), principalID, mockFPAOwnerRoleDefinitionID))
}

func MockFPARoleAssignmentReady(serviceProviderCluster *coreapi.ServiceProviderCluster, principalID string) bool {
	expectedID, err := mockFPARoleAssignmentID(serviceProviderCluster, principalID)
	if err != nil {
		return false
	}
	reference := serviceProviderCluster.Status.AzureResources.MockFPARoleAssignment
	return reference.PendingAzureResource == nil &&
		controllerutil.ResourceIDsEqual(reference.AzureResource, expectedID) &&
		serviceProviderCluster.Spec.EarliestRecheckTimesByController[MockFPARoleAssignmentControllerName] != nil
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
	if existing.Status.AzureResources.ManagedResourceGroup.AzureResource == nil {
		return nil
	}
	if MockFPARoleAssignmentReady(existing, c.principalID) {
		earliest := existing.Spec.EarliestRecheckTimesByController[MockFPARoleAssignmentControllerName]
		if c.clock.Now().Before(earliest.Time) {
			return nil
		}
	}
	expectedID, err := mockFPARoleAssignmentID(existing, c.principalID)
	if err != nil {
		return utils.TrackError(err)
	}
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
	client, err := c.clientBuilder.RoleAssignmentsClient(cluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build ARM helper role assignments client: %w", err))
	}
	scope := existing.Status.AzureResources.ManagedResourceGroup.AzureResource.String()
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
