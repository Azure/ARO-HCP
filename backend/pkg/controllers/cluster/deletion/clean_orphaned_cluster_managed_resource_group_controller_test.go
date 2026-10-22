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

package deletion

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

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

func TestCleanOrphanedManagedResourceGroupsSyncOnce(t *testing.T) {
	const subscriptionID = "abcdefab-0000-0000-0000-000000000000"
	const tenantID = "11111111-1111-1111-1111-111111111111"
	const ownerID = "/subscriptions/" + subscriptionID + "/resourceGroups/customer/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"
	notFound := &azcore.ResponseError{StatusCode: http.StatusNotFound}

	tests := []struct {
		name        string
		managedBy   string
		localOwner  bool
		checkParent bool
		parentErr   error
		readOnly    bool
		wantOrphans float64
		wantSyncErr bool
	}{
		{name: "foreign backend owner with existing parent", managedBy: ownerID, checkParent: true},
		{name: "local owner matched case insensitively", managedBy: strings.ToUpper(ownerID), localOwner: true},
		{name: "missing parent permits cleanup", managedBy: ownerID, checkParent: true, parentErr: notFound, wantOrphans: 1},
		{name: "missing parent in read only mode", managedBy: ownerID, checkParent: true, parentErr: notFound, readOnly: true, wantOrphans: 1},
		{name: "forbidden parent fails closed", managedBy: ownerID, checkParent: true, parentErr: &azcore.ResponseError{StatusCode: http.StatusForbidden}, wantSyncErr: true},
		{name: "throttled parent fails closed", managedBy: ownerID, checkParent: true, parentErr: &azcore.ResponseError{StatusCode: http.StatusTooManyRequests}, wantSyncErr: true},
		{name: "unavailable parent fails closed", managedBy: ownerID, checkParent: true, parentErr: &azcore.ResponseError{StatusCode: http.StatusInternalServerError}, wantSyncErr: true},
		{name: "cross subscription owner remains protected", managedBy: strings.Replace(ownerID, subscriptionID, "22222222-2222-2222-2222-222222222222", 1)},
		{name: "invalid owner remains protected", managedBy: "not-a-resource-id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CLEAN_ORPHANED_MANAGED_RESOURCE_GROUPS_MODE", "readwrite")
			if tt.readOnly {
				t.Setenv("CLEAN_ORPHANED_MANAGED_RESOURCE_GROUPS_MODE", "")
			}
			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
			ctrl := gomock.NewController(t)
			subscriptionResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + subscriptionID))
			resources := []any{&coreapi.Subscription{
				CosmosMetadata: coreapi.CosmosMetadata{ResourceID: subscriptionResourceID, PartitionKey: subscriptionID},
				Properties:     &coreapi.SubscriptionProperties{TenantId: ptr.To(tenantID)},
			}}
			if tt.localOwner {
				id := metadataapi.Must(azcorearm.ParseResourceID(ownerID))
				resources = append(resources, &coreapi.Cluster{
					CosmosMetadata:  coreapi.CosmosMetadata{ResourceID: id, PartitionKey: subscriptionID},
					TrackedResource: coreapi.TrackedResource{Resource: coreapi.Resource{ID: id}},
				})
			}
			db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)
			// Isolate counter assertions from other controller tests without resetting global metrics.
			location := t.Name()
			rg := armresources.ResourceGroup{Name: ptr.To("managed"), Location: ptr.To(location), ManagedBy: ptr.To(tt.managedBy)}
			resourceGroups := []*armresources.ResourceGroup{&rg}
			if tt.wantSyncErr {
				resourceGroups = append(resourceGroups, &armresources.ResourceGroup{
					Name: ptr.To("other-managed"), Location: ptr.To(location),
					ManagedBy: ptr.To(strings.Replace(ownerID, "/customer/", "/other-customer/", 1)),
				})
			}
			rgClient := azureclient.NewMockResourceGroupsClient(ctrl)
			pager := azruntime.NewPager(azruntime.PagingHandler[armresources.ResourceGroupsClientListResponse]{
				More: func(armresources.ResourceGroupsClientListResponse) bool { return false },
				Fetcher: func(context.Context, *armresources.ResourceGroupsClientListResponse) (armresources.ResourceGroupsClientListResponse, error) {
					return armresources.ResourceGroupsClientListResponse{
						ResourceGroupListResult: armresources.ResourceGroupListResult{Value: resourceGroups},
					}, nil
				},
			})
			rgClient.EXPECT().NewListPager(gomock.Any()).Return(pager)
			if tt.wantSyncErr {
				// Regardless of map iteration order, first discover an orphan, then
				// fail another owner's lookup. Neither MRG may be counted or deleted.
				gomock.InOrder(
					rgClient.EXPECT().Get(gomock.Any(), gomock.Any(), nil).Return(armresources.ResourceGroupsClientGetResponse{}, notFound),
					rgClient.EXPECT().Get(gomock.Any(), gomock.Any(), nil).Return(armresources.ResourceGroupsClientGetResponse{}, tt.parentErr),
				)
			} else if tt.checkParent {
				rgClient.EXPECT().Get(gomock.Any(), "customer", nil).Return(armresources.ResourceGroupsClientGetResponse{}, tt.parentErr)
			}
			if tt.wantOrphans > 0 {
				rgClient.EXPECT().Get(gomock.Any(), "managed", nil).Return(armresources.ResourceGroupsClientGetResponse{ResourceGroup: rg}, nil)
				if !tt.readOnly {
					// The MRG disappearing at delete time completes cleanup without an SDK poller.
					rgClient.EXPECT().BeginDelete(gomock.Any(), "managed", nil).Return(nil, notFound)
				}
			}
			builder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
			builder.EXPECT().ResourceGroupsClient(tenantID, subscriptionID).Return(rgClient, nil)
			controller := &cleanOrphanedClusterManagedResourceGroup{location: location, resourcesDBClient: db, azureFPAClientBuilder: builder}
			counter := orphanedMRGsFound.WithLabelValues(location)
			before := testutil.ToFloat64(counter)

			err = controller.SyncOnce(ctx, controllerutils.SubscriptionKey{SubscriptionID: subscriptionID})
			if tt.wantSyncErr {
				require.ErrorIs(t, err, tt.parentErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantOrphans, testutil.ToFloat64(counter)-before, "only unprotected MRGs should be counted as orphans")
		})
	}
}
