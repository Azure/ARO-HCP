// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package subscription

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Note: listManagedResourceGroupsForSubscription uses Azure SDK Pager which is complex to mock.
// This function is tested indirectly through integration tests.
// Here we focus on testing the business logic in deleteOrphanedManagedResourceGroup and pollResourceGroupDeletion.

// Note: listClusterResourceIDsForSubscription requires mocking ResourcesDBClient's complex paging interface.
// This function is tested indirectly through integration tests.

func TestDeleteOrphanedManagedResourceGroup_ReadOnlyMode(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	location := "eastus"
	subscriptionID := "sub1"
	resourceGroupName := "managed-rg-1"
	managedBy := "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1"

	controller := &cleanOrphanedClusterManagedResourceGroup{
		location: location,
	}

	mockRGClient := azureclient.NewMockResourceGroupsClient(ctrl)

	// In read-only mode, the function should call Get to check state
	rg := armresources.ResourceGroup{
		Name:      ptr.To(resourceGroupName),
		Location:  ptr.To(location),
		ManagedBy: ptr.To(managedBy),
		Properties: &armresources.ResourceGroupProperties{
			ProvisioningState: ptr.To("Succeeded"),
		},
	}

	mockRGClient.EXPECT().
		Get(gomock.Any(), resourceGroupName, nil).
		Return(armresources.ResourceGroupsClientGetResponse{ResourceGroup: rg}, nil)

	// BeginDelete should NOT be called in read-only mode

	err := controller.deleteOrphanedManagedResourceGroup(ctx, mockRGClient, subscriptionID, resourceGroupName, managedBy, true)
	require.NoError(t, err)
}

func TestDeleteOrphanedManagedResourceGroup_AlreadyDeleted(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	location := "eastus"
	subscriptionID := "sub1"
	resourceGroupName := "managed-rg-1"
	managedBy := "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1"

	controller := &cleanOrphanedClusterManagedResourceGroup{
		location: location,
	}

	mockRGClient := azureclient.NewMockResourceGroupsClient(ctrl)

	// Resource group already deleted (404)
	respErr := &azcore.ResponseError{
		StatusCode: http.StatusNotFound,
		RawResponse: &http.Response{
			StatusCode: http.StatusNotFound,
		},
	}

	mockRGClient.EXPECT().
		Get(gomock.Any(), resourceGroupName, nil).
		Return(armresources.ResourceGroupsClientGetResponse{}, respErr)

	err := controller.deleteOrphanedManagedResourceGroup(ctx, mockRGClient, subscriptionID, resourceGroupName, managedBy, false)
	require.NoError(t, err)
}

func TestDeleteOrphanedManagedResourceGroup_AlreadyDeleting(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	location := "eastus"
	subscriptionID := "sub1"
	resourceGroupName := "managed-rg-1"
	managedBy := "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1"

	controller := &cleanOrphanedClusterManagedResourceGroup{
		location: location,
	}

	mockRGClient := azureclient.NewMockResourceGroupsClient(ctrl)

	// Resource group is already being deleted
	rg := armresources.ResourceGroup{
		Name:      ptr.To(resourceGroupName),
		Location:  ptr.To(location),
		ManagedBy: ptr.To(managedBy),
		Properties: &armresources.ResourceGroupProperties{
			ProvisioningState: ptr.To("Deleting"),
		},
	}

	mockRGClient.EXPECT().
		Get(gomock.Any(), resourceGroupName, nil).
		Return(armresources.ResourceGroupsClientGetResponse{ResourceGroup: rg}, nil)

	// BeginDelete should NOT be called if already deleting

	err := controller.deleteOrphanedManagedResourceGroup(ctx, mockRGClient, subscriptionID, resourceGroupName, managedBy, false)
	require.NoError(t, err)
}

func TestDeleteOrphanedManagedResourceGroup_GetError(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	location := "eastus"
	subscriptionID := "sub1"
	resourceGroupName := "managed-rg-1"
	managedBy := "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1"

	controller := &cleanOrphanedClusterManagedResourceGroup{
		location: location,
	}

	mockRGClient := azureclient.NewMockResourceGroupsClient(ctrl)

	// Get fails with non-404 error
	getErr := errors.New("internal server error")

	mockRGClient.EXPECT().
		Get(gomock.Any(), resourceGroupName, nil).
		Return(armresources.ResourceGroupsClientGetResponse{}, getErr)

	err := controller.deleteOrphanedManagedResourceGroup(ctx, mockRGClient, subscriptionID, resourceGroupName, managedBy, false)
	require.Error(t, err)
	assert.Equal(t, getErr, err)
}

func TestDeleteOrphanedManagedResourceGroup_BeginDeleteError(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	location := "eastus"
	subscriptionID := "sub1"
	resourceGroupName := "managed-rg-1"
	managedBy := "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1"

	controller := &cleanOrphanedClusterManagedResourceGroup{
		location: location,
	}

	mockRGClient := azureclient.NewMockResourceGroupsClient(ctrl)

	rg := armresources.ResourceGroup{
		Name:      ptr.To(resourceGroupName),
		Location:  ptr.To(location),
		ManagedBy: ptr.To(managedBy),
		Properties: &armresources.ResourceGroupProperties{
			ProvisioningState: ptr.To("Succeeded"),
		},
	}

	mockRGClient.EXPECT().
		Get(gomock.Any(), resourceGroupName, nil).
		Return(armresources.ResourceGroupsClientGetResponse{ResourceGroup: rg}, nil)

	deleteErr := errors.New("failed to initiate deletion")

	mockRGClient.EXPECT().
		BeginDelete(gomock.Any(), resourceGroupName, nil).
		Return(nil, deleteErr)

	err := controller.deleteOrphanedManagedResourceGroup(ctx, mockRGClient, subscriptionID, resourceGroupName, managedBy, false)
	require.Error(t, err)
	assert.Equal(t, deleteErr, err)
}

func TestDeleteOrphanedManagedResourceGroup_BeginDelete404(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	location := "eastus"
	subscriptionID := "sub1"
	resourceGroupName := "managed-rg-1"
	managedBy := "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1"

	controller := &cleanOrphanedClusterManagedResourceGroup{
		location: location,
	}

	mockRGClient := azureclient.NewMockResourceGroupsClient(ctrl)

	rg := armresources.ResourceGroup{
		Name:      ptr.To(resourceGroupName),
		Location:  ptr.To(location),
		ManagedBy: ptr.To(managedBy),
		Properties: &armresources.ResourceGroupProperties{
			ProvisioningState: ptr.To("Succeeded"),
		},
	}

	mockRGClient.EXPECT().
		Get(gomock.Any(), resourceGroupName, nil).
		Return(armresources.ResourceGroupsClientGetResponse{ResourceGroup: rg}, nil)

	// Resource group was deleted between Get and BeginDelete
	respErr := &azcore.ResponseError{
		StatusCode: http.StatusNotFound,
		RawResponse: &http.Response{
			StatusCode: http.StatusNotFound,
		},
	}

	mockRGClient.EXPECT().
		BeginDelete(gomock.Any(), resourceGroupName, nil).
		Return(nil, respErr)

	err := controller.deleteOrphanedManagedResourceGroup(ctx, mockRGClient, subscriptionID, resourceGroupName, managedBy, false)
	require.NoError(t, err)
}

// Note: Testing successful deletion with polling is complex because it requires mocking
// the Poller[T] type which is a concrete struct from the Azure SDK, not an interface.
// The polling logic is best tested through integration tests.
// We test the error handling paths above which cover the critical business logic.
