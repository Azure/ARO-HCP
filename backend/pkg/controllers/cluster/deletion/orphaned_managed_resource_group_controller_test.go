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
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Note: The watching controller's discoverAndEnqueueManagedResourceGroups uses Azure SDK Pager which is complex to mock.
// This function is tested indirectly through integration tests.
// Here we focus on testing the business logic in deleteOrphanedManagedResourceGroup and needsWork.

// Note: needsWork requires mocking ResourcesDBClient's complex paging interface.
// This function is tested indirectly through integration tests.

func TestDeleteOrphanedManagedResourceGroup_ReadOnlyMode(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	location := "eastus"
	subscriptionID := "sub1"
	resourceGroupName := "managed-rg-1"
	managedBy := "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1"

	controller := &orphanedManagedResourceGroupController{
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

	key := ManagedResourceGroupKey{
		SubscriptionID:    subscriptionID,
		ResourceGroupName: resourceGroupName,
		ManagedBy:         managedBy,
		Location:          location,
	}

	err := controller.deleteOrphanedManagedResourceGroup(ctx, mockRGClient, key, true)
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

	controller := &orphanedManagedResourceGroupController{
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

	key := ManagedResourceGroupKey{
		SubscriptionID:    subscriptionID,
		ResourceGroupName: resourceGroupName,
		ManagedBy:         managedBy,
		Location:          location,
	}

	err := controller.deleteOrphanedManagedResourceGroup(ctx, mockRGClient, key, false)
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

	controller := &orphanedManagedResourceGroupController{
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

	key := ManagedResourceGroupKey{
		SubscriptionID:    subscriptionID,
		ResourceGroupName: resourceGroupName,
		ManagedBy:         managedBy,
		Location:          location,
	}

	err := controller.deleteOrphanedManagedResourceGroup(ctx, mockRGClient, key, false)
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

	controller := &orphanedManagedResourceGroupController{
		location: location,
	}

	mockRGClient := azureclient.NewMockResourceGroupsClient(ctrl)

	// Get fails with non-404 error
	getErr := errors.New("internal server error")

	mockRGClient.EXPECT().
		Get(gomock.Any(), resourceGroupName, nil).
		Return(armresources.ResourceGroupsClientGetResponse{}, getErr)

	key := ManagedResourceGroupKey{
		SubscriptionID:    subscriptionID,
		ResourceGroupName: resourceGroupName,
		ManagedBy:         managedBy,
		Location:          location,
	}

	err := controller.deleteOrphanedManagedResourceGroup(ctx, mockRGClient, key, false)
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

	controller := &orphanedManagedResourceGroupController{
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

	key := ManagedResourceGroupKey{
		SubscriptionID:    subscriptionID,
		ResourceGroupName: resourceGroupName,
		ManagedBy:         managedBy,
		Location:          location,
	}

	err := controller.deleteOrphanedManagedResourceGroup(ctx, mockRGClient, key, false)
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

	controller := &orphanedManagedResourceGroupController{
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

	key := ManagedResourceGroupKey{
		SubscriptionID:    subscriptionID,
		ResourceGroupName: resourceGroupName,
		ManagedBy:         managedBy,
		Location:          location,
	}

	err := controller.deleteOrphanedManagedResourceGroup(ctx, mockRGClient, key, false)
	require.NoError(t, err)
}

// Note: Testing successful deletion with polling is complex because it requires mocking
// the Poller[T] type which is a concrete struct from the Azure SDK, not an interface.
// The polling logic is best tested through integration tests.
// We test the error handling paths above which cover the critical business logic.

func TestIsSubscriptionOwnedByThisEnvironment(t *testing.T) {
	tests := []struct {
		name               string
		afecConfig         AFECOwnershipConfig
		subscriptionAFECs  []string
		expectedOwned      bool
		expectedLogMessage string
	}{
		{
			name: "None mode - no subscriptions owned",
			afecConfig: AFECOwnershipConfig{
				Mode: AFECOwnershipNone,
			},
			subscriptionAFECs: []string{"Microsoft.RedHatOpenShift/STAGING-APPROVED"},
			expectedOwned:     false,
		},
		{
			name: "MyAFEC mode - subscription has matching AFEC",
			afecConfig: AFECOwnershipConfig{
				Mode:   AFECOwnershipMyAFEC,
				MyAFEC: "Microsoft.RedHatOpenShift/STAGING-APPROVED",
			},
			subscriptionAFECs: []string{"Microsoft.RedHatOpenShift/STAGING-APPROVED"},
			expectedOwned:     true,
		},
		{
			name: "MyAFEC mode - subscription missing required AFEC",
			afecConfig: AFECOwnershipConfig{
				Mode:   AFECOwnershipMyAFEC,
				MyAFEC: "Microsoft.RedHatOpenShift/STAGING-APPROVED",
			},
			subscriptionAFECs: []string{"Microsoft.RedHatOpenShift/INT-APPROVED"},
			expectedOwned:     false,
		},
		{
			name: "NotOtherAFECs mode - subscription has no other AFECs (PROD subscription)",
			afecConfig: AFECOwnershipConfig{
				Mode: AFECOwnershipNotOtherAFECs,
				NotOtherAFECs: map[string]struct{}{
					"Microsoft.RedHatOpenShift/STAGING-APPROVED": {},
					"Microsoft.RedHatOpenShift/INT-APPROVED":     {},
				},
			},
			subscriptionAFECs: []string{},
			expectedOwned:     true,
		},
		{
			name: "NotOtherAFECs mode - subscription has staging AFEC (not owned)",
			afecConfig: AFECOwnershipConfig{
				Mode: AFECOwnershipNotOtherAFECs,
				NotOtherAFECs: map[string]struct{}{
					"Microsoft.RedHatOpenShift/STAGING-APPROVED": {},
					"Microsoft.RedHatOpenShift/INT-APPROVED":     {},
				},
			},
			subscriptionAFECs: []string{"Microsoft.RedHatOpenShift/STAGING-APPROVED"},
			expectedOwned:     false,
		},
		{
			name: "NotOtherAFECs mode - subscription has INT AFEC (not owned)",
			afecConfig: AFECOwnershipConfig{
				Mode: AFECOwnershipNotOtherAFECs,
				NotOtherAFECs: map[string]struct{}{
					"Microsoft.RedHatOpenShift/STAGING-APPROVED": {},
					"Microsoft.RedHatOpenShift/INT-APPROVED":     {},
				},
			},
			subscriptionAFECs: []string{"Microsoft.RedHatOpenShift/INT-APPROVED"},
			expectedOwned:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := &orphanedManagedResourceGroupController{
				afecOwnership: tt.afecConfig,
			}

			// Create a subscription with the specified AFECs
			features := make([]coreapi.Feature, 0, len(tt.subscriptionAFECs))
			for _, afec := range tt.subscriptionAFECs {
				features = append(features, coreapi.Feature{
					Name:  ptr.To(afec),
					State: ptr.To("Registered"),
				})
			}
			subscription := &coreapi.Subscription{
				Properties: &coreapi.SubscriptionProperties{
					RegisteredFeatures: &features,
				},
			}

			owned := controller.isSubscriptionOwnedByThisEnvironment(subscription)
			assert.Equal(t, tt.expectedOwned, owned)
		})
	}
}

func TestParseAFECOwnershipFromEnv(t *testing.T) {
	tests := []struct {
		name           string
		myAFEC         string
		otherAFECs     string
		expectedMode   AFECOwnershipMode
		expectedMyAFEC string
		expectedOthers []string
	}{
		{
			name:         "Both empty - None mode",
			myAFEC:       "",
			otherAFECs:   "",
			expectedMode: AFECOwnershipNone,
		},
		{
			name:           "MY_AFEC set - MyAFEC mode",
			myAFEC:         "Microsoft.RedHatOpenShift/STAGING-APPROVED",
			otherAFECs:     "",
			expectedMode:   AFECOwnershipMyAFEC,
			expectedMyAFEC: "Microsoft.RedHatOpenShift/STAGING-APPROVED",
		},
		{
			name:         "OTHER_AFECS set - NotOtherAFECs mode",
			myAFEC:       "",
			otherAFECs:   "Microsoft.RedHatOpenShift/STAGING-APPROVED,Microsoft.RedHatOpenShift/INT-APPROVED",
			expectedMode: AFECOwnershipNotOtherAFECs,
			expectedOthers: []string{
				"Microsoft.RedHatOpenShift/STAGING-APPROVED",
				"Microsoft.RedHatOpenShift/INT-APPROVED",
			},
		},
		{
			name:         "OTHER_AFECS with whitespace",
			myAFEC:       "",
			otherAFECs:   " Microsoft.RedHatOpenShift/STAGING-APPROVED , Microsoft.RedHatOpenShift/INT-APPROVED ",
			expectedMode: AFECOwnershipNotOtherAFECs,
			expectedOthers: []string{
				"Microsoft.RedHatOpenShift/STAGING-APPROVED",
				"Microsoft.RedHatOpenShift/INT-APPROVED",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set env vars
			if tt.myAFEC != "" {
				t.Setenv("MY_AFEC", tt.myAFEC)
			}
			if tt.otherAFECs != "" {
				t.Setenv("OTHER_AFECS", tt.otherAFECs)
			}

			config := parseAFECOwnershipFromEnv()

			assert.Equal(t, tt.expectedMode, config.Mode)
			if tt.expectedMyAFEC != "" {
				assert.Equal(t, tt.expectedMyAFEC, config.MyAFEC)
			}
			if len(tt.expectedOthers) > 0 {
				for _, expected := range tt.expectedOthers {
					_, exists := config.NotOtherAFECs[expected]
					assert.True(t, exists, "Expected AFEC %s not found in NotOtherAFECs", expected)
				}
				assert.Equal(t, len(tt.expectedOthers), len(config.NotOtherAFECs))
			}
		})
	}
}
