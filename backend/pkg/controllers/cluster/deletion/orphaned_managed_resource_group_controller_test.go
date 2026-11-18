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
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Note: The watching controller's discoverAndEnqueueManagedResourceGroups uses Azure SDK Pager which is complex to mock.
// This function is tested indirectly through integration tests.
// Here we focus on testing the business logic in deleteOrphanedManagedResourceGroup, needsWork, and SyncOnce.

// stubSubscriptionLister is a minimal SubscriptionLister for testing.
type stubSubscriptionLister struct {
	subscriptions map[string]*coreapi.Subscription
}

func (s *stubSubscriptionLister) List(_ context.Context) ([]*coreapi.Subscription, error) {
	var result []*coreapi.Subscription
	for _, sub := range s.subscriptions {
		result = append(result, sub)
	}
	return result, nil
}

func (s *stubSubscriptionLister) Get(_ context.Context, subscriptionID string) (*coreapi.Subscription, error) {
	sub, ok := s.subscriptions[subscriptionID]
	if !ok {
		return nil, &azcore.ResponseError{StatusCode: http.StatusNotFound}
	}
	return sub, nil
}

// stubClusterLister is a minimal ClusterLister for testing.
type stubClusterLister struct {
	clusters map[string]*coreapi.Cluster // key: "sub/rg/name"
}

func (s *stubClusterLister) List(_ context.Context) ([]*coreapi.Cluster, error) {
	return nil, nil
}

func (s *stubClusterLister) Get(_ context.Context, subscriptionID, resourceGroupName, clusterName string) (*coreapi.Cluster, error) {
	key := subscriptionID + "/" + resourceGroupName + "/" + clusterName
	cluster, ok := s.clusters[key]
	if !ok {
		return nil, &azcore.ResponseError{StatusCode: http.StatusNotFound}
	}
	return cluster, nil
}

func (s *stubClusterLister) ListForResourceGroup(_ context.Context, _, _ string) ([]*coreapi.Cluster, error) {
	return nil, nil
}

func (s *stubClusterLister) ListForSubscription(_ context.Context, _ string) ([]*coreapi.Cluster, error) {
	return nil, nil
}

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

	key := controllerutils.ManagedResourceGroupKey{
		SubscriptionID:    subscriptionID,
		ResourceGroupName: resourceGroupName,
		ManagedBy:         managedBy,
		Location:          location,
	}

	requeueAfter, err := controller.deleteOrphanedManagedResourceGroup(ctx, mockRGClient, key, "tenant-1", true)
	require.NoError(t, err)
	require.Equal(t, 15*time.Minute, requeueAfter)
	require.Equal(t, float64(1), testutil.ToFloat64(orphanedMRGsPresent.WithLabelValues(location, subscriptionID, resourceGroupName)))
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

	key := controllerutils.ManagedResourceGroupKey{
		SubscriptionID:    subscriptionID,
		ResourceGroupName: resourceGroupName,
		ManagedBy:         managedBy,
		Location:          location,
	}

	orphanedMRGsPresent.WithLabelValues(location, subscriptionID, resourceGroupName).Set(1)
	requeueAfter, err := controller.deleteOrphanedManagedResourceGroup(ctx, mockRGClient, key, "tenant-1", false)
	require.NoError(t, err)
	require.Zero(t, requeueAfter)
	require.Zero(t, testutil.ToFloat64(orphanedMRGsPresent.WithLabelValues(location, subscriptionID, resourceGroupName)))
}

func TestSyncOnce_ClearsPresenceAfterDeletionWithoutRestart(t *testing.T) {
	const targetAFEC = "Microsoft.RedHatOpenShift/STAGING-APPROVED"
	key := controllerutils.ManagedResourceGroupKey{
		SubscriptionID:    "presence-test-subscription",
		ResourceGroupName: "presence-test-managed-rg",
		ManagedBy:         "/subscriptions/presence-test-subscription/resourceGroups/customer-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		Location:          "eastus",
	}
	ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
	ctrl := gomock.NewController(t)
	subscription := &coreapi.Subscription{
		Properties: &coreapi.SubscriptionProperties{
			TenantId:           ptr.To("tenant-1"),
			RegisteredFeatures: &[]coreapi.Feature{{Name: ptr.To(targetAFEC), State: ptr.To("Registered")}},
		},
	}
	db := &stubOrphanedMRGResourcesDBClient{
		getClusters: func(_, _ string) corecosmosstorage.HCPClusterCRUD {
			return &stubOrphanedMRGClusterCRUD{
				getCluster: func(context.Context, string) (*coreapi.Cluster, error) {
					return nil, &azcore.ResponseError{StatusCode: http.StatusNotFound}
				},
			}
		},
	}
	rgClient := azureclient.NewMockResourceGroupsClient(ctrl)
	builder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
	builder.EXPECT().ResourceGroupsClient("tenant-1", key.SubscriptionID).Return(rgClient, nil).Times(2)
	gomock.InOrder(
		rgClient.EXPECT().Get(gomock.Any(), key.ResourceGroupName, nil).Return(
			armresources.ResourceGroupsClientGetResponse{ResourceGroup: armresources.ResourceGroup{
				ManagedBy: ptr.To(key.ManagedBy), Location: ptr.To(key.Location),
			}}, nil),
		rgClient.EXPECT().BeginDelete(gomock.Any(), key.ResourceGroupName, nil).Return(nil, nil),
		rgClient.EXPECT().Get(gomock.Any(), key.ResourceGroupName, nil).Return(
			armresources.ResourceGroupsClientGetResponse{}, &azcore.ResponseError{StatusCode: http.StatusNotFound}),
	)
	controller := &orphanedManagedResourceGroupController{
		location:              key.Location,
		resourcesDBClient:     db,
		subscriptionLister:    &stubSubscriptionLister{subscriptions: map[string]*coreapi.Subscription{key.SubscriptionID: subscription}},
		clusterLister:         &stubClusterLister{},
		azureFPAClientBuilder: builder,
		afecOwnership:         parseAFECOwnership(targetAFEC, nil),
	}
	presence := orphanedMRGsPresent.WithLabelValues(key.Location, key.SubscriptionID, key.ResourceGroupName)
	t.Cleanup(func() { orphanedMRGsPresent.DeleteLabelValues(key.Location, key.SubscriptionID, key.ResourceGroupName) })

	requeueAfter, err := controller.SyncOnce(ctx, key)
	require.NoError(t, err)
	require.Equal(t, orphanedMRGDeletionRecheckInterval, requeueAfter)
	require.Equal(t, float64(1), testutil.ToFloat64(presence))

	// The scheduled recheck observes the 404 and resolves the alert on the same controller instance.
	requeueAfter, err = controller.SyncOnce(ctx, key)
	require.NoError(t, err)
	require.Zero(t, requeueAfter)
	require.Zero(t, testutil.ToFloat64(presence))
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

	key := controllerutils.ManagedResourceGroupKey{
		SubscriptionID:    subscriptionID,
		ResourceGroupName: resourceGroupName,
		ManagedBy:         managedBy,
		Location:          location,
	}

	requeueAfter, err := controller.deleteOrphanedManagedResourceGroup(ctx, mockRGClient, key, "tenant-1", false)
	require.NoError(t, err)
	require.Equal(t, 15*time.Minute, requeueAfter)
	require.Equal(t, float64(1), testutil.ToFloat64(orphanedMRGsPresent.WithLabelValues(location, subscriptionID, resourceGroupName)))
}

func TestDeleteOrphanedManagedResourceGroup_SkipsChangedResourceGroup(t *testing.T) {
	key := controllerutils.ManagedResourceGroupKey{
		SubscriptionID:    "sub1",
		ResourceGroupName: "managed-rg",
		ManagedBy:         "/subscriptions/sub1/resourceGroups/customer-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		Location:          "eastus",
	}
	tests := []struct {
		name string
		rg   armresources.ResourceGroup
	}{
		{
			name: "claimed by another cluster",
			rg: armresources.ResourceGroup{
				ManagedBy: ptr.To("/subscriptions/sub1/resourceGroups/customer-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-2"),
				Location:  ptr.To(key.Location),
			},
		},
		{
			name: "owner cleared",
			rg:   armresources.ResourceGroup{Location: ptr.To(key.Location)},
		},
		{
			name: "location changed",
			rg: armresources.ResourceGroup{
				ManagedBy: ptr.To(key.ManagedBy),
				Location:  ptr.To("westus"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rgClient := azureclient.NewMockResourceGroupsClient(gomock.NewController(t))
			rgClient.EXPECT().Get(gomock.Any(), key.ResourceGroupName, nil).Return(
				armresources.ResourceGroupsClientGetResponse{ResourceGroup: tt.rg}, nil)
			controller := &orphanedManagedResourceGroupController{location: key.Location}

			requeueAfter, err := controller.deleteOrphanedManagedResourceGroup(
				utils.ContextWithLogger(t.Context(), testr.New(t)), rgClient, key, "tenant-1", false)
			require.NoError(t, err)
			require.Equal(t, orphanedMRGDeletionRecheckInterval, requeueAfter)
		})
	}
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

	key := controllerutils.ManagedResourceGroupKey{
		SubscriptionID:    subscriptionID,
		ResourceGroupName: resourceGroupName,
		ManagedBy:         managedBy,
		Location:          location,
	}

	requeueAfter, err := controller.deleteOrphanedManagedResourceGroup(ctx, mockRGClient, key, "tenant-1", false)
	require.Error(t, err)
	assert.Equal(t, getErr, err)
	require.Zero(t, requeueAfter)
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

	key := controllerutils.ManagedResourceGroupKey{
		SubscriptionID:    subscriptionID,
		ResourceGroupName: resourceGroupName,
		ManagedBy:         managedBy,
		Location:          location,
	}

	requeueAfter, err := controller.deleteOrphanedManagedResourceGroup(ctx, mockRGClient, key, "tenant-1", false)
	require.Error(t, err)
	assert.Equal(t, deleteErr, err)
	require.Zero(t, requeueAfter)
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

	key := controllerutils.ManagedResourceGroupKey{
		SubscriptionID:    subscriptionID,
		ResourceGroupName: resourceGroupName,
		ManagedBy:         managedBy,
		Location:          location,
	}

	requeueAfter, err := controller.deleteOrphanedManagedResourceGroup(ctx, mockRGClient, key, "tenant-1", false)
	require.NoError(t, err)
	require.Zero(t, requeueAfter)
	require.Zero(t, testutil.ToFloat64(orphanedMRGsPresent.WithLabelValues(location, subscriptionID, resourceGroupName)))
}

func TestDeleteOrphanedManagedResourceGroup_BeginDeleteSchedulesRecheck(t *testing.T) {
	for _, state := range []string{"Succeeded", "Failed"} {
		t.Run(state, func(t *testing.T) {
			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
			ctrl := gomock.NewController(t)
			resourceGroupName := "managed-rg-1"
			key := controllerutils.ManagedResourceGroupKey{
				SubscriptionID:    "sub1",
				ResourceGroupName: resourceGroupName,
				ManagedBy:         "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1",
				Location:          "eastus",
			}
			rgClient := azureclient.NewMockResourceGroupsClient(ctrl)
			rgClient.EXPECT().Get(gomock.Any(), resourceGroupName, nil).Return(armresources.ResourceGroupsClientGetResponse{
				ResourceGroup: armresources.ResourceGroup{
					ManagedBy: ptr.To(key.ManagedBy), Location: ptr.To(key.Location),
					Properties: &armresources.ResourceGroupProperties{ProvisioningState: ptr.To(state)},
				},
			}, nil)
			// A successful initial DELETE is sufficient; the poller is deliberately unused.
			rgClient.EXPECT().BeginDelete(gomock.Any(), resourceGroupName, nil).Return(nil, nil)

			controller := &orphanedManagedResourceGroupController{location: key.Location}
			if state == "Failed" {
				builder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
				builder.EXPECT().ActivityLogsClient("tenant-1", key.SubscriptionID).Return(
					&activityLogsClientStub{pages: []armmonitor.ActivityLogsClientListResponse{{}}}, nil)
				controller.azureFPAClientBuilder = builder
			}
			requeueAfter, err := controller.deleteOrphanedManagedResourceGroup(ctx, rgClient, key, "tenant-1", false)
			require.NoError(t, err)
			require.Equal(t, 15*time.Minute, requeueAfter)
		})
	}
}

func TestFailedMRGDeletionContinuesWhenActivityLogIsUnavailable(t *testing.T) {
	ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
	ctrl := gomock.NewController(t)
	key := controllerutils.ManagedResourceGroupKey{
		SubscriptionID:    "sub1",
		ResourceGroupName: "managed-rg-1",
		ManagedBy:         "/subscriptions/sub1/resourceGroups/customer-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		Location:          "eastus",
	}
	rgClient := azureclient.NewMockResourceGroupsClient(ctrl)
	rgClient.EXPECT().Get(gomock.Any(), key.ResourceGroupName, nil).Return(armresources.ResourceGroupsClientGetResponse{
		ResourceGroup: armresources.ResourceGroup{
			ManagedBy: ptr.To(key.ManagedBy), Location: ptr.To(key.Location),
			Properties: &armresources.ResourceGroupProperties{ProvisioningState: ptr.To("Failed")},
		},
	}, nil)
	rgClient.EXPECT().BeginDelete(gomock.Any(), key.ResourceGroupName, nil).Return(nil, nil)
	builder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
	builder.EXPECT().ActivityLogsClient("tenant-1", key.SubscriptionID).Return(nil, errors.New("access denied"))
	controller := &orphanedManagedResourceGroupController{location: key.Location, azureFPAClientBuilder: builder}

	requeueAfter, err := controller.deleteOrphanedManagedResourceGroup(ctx, rgClient, key, "tenant-1", false)
	require.NoError(t, err)
	require.Equal(t, 15*time.Minute, requeueAfter)
}

func TestFailedMRGActivityLogIsCheckedInDryRun(t *testing.T) {
	ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
	ctrl := gomock.NewController(t)
	key := controllerutils.ManagedResourceGroupKey{
		SubscriptionID: "sub1", ResourceGroupName: "managed-rg-1", Location: "eastus",
		ManagedBy: "/subscriptions/sub1/resourceGroups/customer-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
	}
	rgClient := azureclient.NewMockResourceGroupsClient(ctrl)
	rgClient.EXPECT().Get(gomock.Any(), key.ResourceGroupName, nil).Return(armresources.ResourceGroupsClientGetResponse{
		ResourceGroup: armresources.ResourceGroup{
			ManagedBy: ptr.To(key.ManagedBy), Location: ptr.To(key.Location),
			Properties: &armresources.ResourceGroupProperties{ProvisioningState: ptr.To("Failed")},
		},
	}, nil)
	builder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
	builder.EXPECT().ActivityLogsClient("tenant-1", key.SubscriptionID).Return(
		&activityLogsClientStub{pages: []armmonitor.ActivityLogsClientListResponse{{}}}, nil)
	controller := &orphanedManagedResourceGroupController{location: key.Location, azureFPAClientBuilder: builder}

	requeueAfter, err := controller.deleteOrphanedManagedResourceGroup(ctx, rgClient, key, "tenant-1", true)
	require.NoError(t, err)
	require.Equal(t, 15*time.Minute, requeueAfter)
}

func TestOrphanedMRGPresenceClearedWhenControllerStops(t *testing.T) {
	orphanedMRGsPresent.WithLabelValues("eastus", "sub1", "managed-rg-1").Set(1)
	controller := &orphanedManagedResourceGroupController{}
	controller.OnStopped()
	require.Zero(t, testutil.CollectAndCount(orphanedMRGsPresent))
}

func TestIsSubscriptionOwnedByThisEnvironment(t *testing.T) {
	tests := []struct {
		name               string
		afecConfig         AFECOwnershipConfig
		subscription       *coreapi.Subscription // if set, used directly instead of building from subscriptionAFECs
		subscriptionAFECs  []string
		expectedOwned      bool
		expectedLogMessage string
	}{
		{
			name: "nil Properties - fail closed",
			afecConfig: AFECOwnershipConfig{
				Mode: AFECOwnershipExcludedAFECs,
				ExcludedAFECs: map[string]struct{}{
					"Microsoft.RedHatOpenShift/STAGING-APPROVED": {},
				},
			},
			subscription:  &coreapi.Subscription{Properties: nil},
			expectedOwned: false,
		},
		{
			name: "nil RegisteredFeatures - fail closed",
			afecConfig: AFECOwnershipConfig{
				Mode: AFECOwnershipExcludedAFECs,
				ExcludedAFECs: map[string]struct{}{
					"Microsoft.RedHatOpenShift/STAGING-APPROVED": {},
				},
			},
			subscription: &coreapi.Subscription{
				Properties: &coreapi.SubscriptionProperties{
					RegisteredFeatures: nil,
				},
			},
			expectedOwned: false,
		},
		{
			name:              "No ownership AFECs - no subscriptions owned",
			afecConfig:        AFECOwnershipConfig{},
			subscriptionAFECs: []string{"Microsoft.RedHatOpenShift/STAGING-APPROVED"},
			expectedOwned:     false,
		},
		{
			name:          "Excluded mode with no AFECs - no subscriptions owned",
			afecConfig:    AFECOwnershipConfig{Mode: AFECOwnershipExcludedAFECs},
			expectedOwned: false,
		},
		{
			name: "TargetAFECs mode - subscription has matching AFEC",
			afecConfig: AFECOwnershipConfig{
				Mode:        AFECOwnershipTargetAFECs,
				TargetAFECs: "Microsoft.RedHatOpenShift/STAGING-APPROVED",
			},
			subscriptionAFECs: []string{"Microsoft.RedHatOpenShift/STAGING-APPROVED"},
			expectedOwned:     true,
		},
		{
			name: "TargetAFECs mode - subscription missing required AFEC",
			afecConfig: AFECOwnershipConfig{
				Mode:        AFECOwnershipTargetAFECs,
				TargetAFECs: "Microsoft.RedHatOpenShift/STAGING-APPROVED",
			},
			subscriptionAFECs: []string{"Microsoft.RedHatOpenShift/INT-APPROVED"},
			expectedOwned:     false,
		},
		{
			name: "ExcludedAFECs mode - subscription has no other AFECs (PROD subscription)",
			afecConfig: AFECOwnershipConfig{
				Mode: AFECOwnershipExcludedAFECs,
				ExcludedAFECs: map[string]struct{}{
					"Microsoft.RedHatOpenShift/STAGING-APPROVED": {},
					"Microsoft.RedHatOpenShift/INT-APPROVED":     {},
				},
			},
			subscriptionAFECs: []string{},
			expectedOwned:     true,
		},
		{
			name: "ExcludedAFECs mode - subscription has staging AFEC (not owned)",
			afecConfig: AFECOwnershipConfig{
				Mode: AFECOwnershipExcludedAFECs,
				ExcludedAFECs: map[string]struct{}{
					"Microsoft.RedHatOpenShift/STAGING-APPROVED": {},
					"Microsoft.RedHatOpenShift/INT-APPROVED":     {},
				},
			},
			subscriptionAFECs: []string{"Microsoft.RedHatOpenShift/STAGING-APPROVED"},
			expectedOwned:     false,
		},
		{
			name: "ExcludedAFECs mode - subscription has INT AFEC (not owned)",
			afecConfig: AFECOwnershipConfig{
				Mode: AFECOwnershipExcludedAFECs,
				ExcludedAFECs: map[string]struct{}{
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

			subscription := tt.subscription
			if subscription == nil {
				// Create a subscription with the specified AFECs
				features := make([]coreapi.Feature, 0, len(tt.subscriptionAFECs))
				for _, afec := range tt.subscriptionAFECs {
					features = append(features, coreapi.Feature{
						Name:  ptr.To(afec),
						State: ptr.To("Registered"),
					})
				}
				subscription = &coreapi.Subscription{
					Properties: &coreapi.SubscriptionProperties{
						RegisteredFeatures: &features,
					},
				}
			}

			owned := controller.isSubscriptionOwnedByThisEnvironment(subscription)
			assert.Equal(t, tt.expectedOwned, owned)
		})
	}
}

func TestParseAFECOwnership(t *testing.T) {
	tests := []struct {
		name                string
		targetAFECs         string
		excludedAFECs       []string
		expectedMode        AFECOwnershipMode
		expectedTargetAFECs string
		expectedExcluded    []string
	}{
		{
			name:          "Both empty - no ownership configuration",
			targetAFECs:   "",
			excludedAFECs: nil,
			expectedMode:  "",
		},
		{
			name:          "Blank excluded AFECs - no ownership configuration",
			excludedAFECs: []string{" ", ""},
			expectedMode:  "",
		},
		{
			name:                "Target AFECs set - TargetAFECs mode",
			targetAFECs:         "Microsoft.RedHatOpenShift/STAGING-APPROVED",
			excludedAFECs:       nil,
			expectedMode:        AFECOwnershipTargetAFECs,
			expectedTargetAFECs: "Microsoft.RedHatOpenShift/STAGING-APPROVED",
		},
		{
			name:          "Excluded AFECs set - ExcludedAFECs mode",
			targetAFECs:   "",
			excludedAFECs: []string{"Microsoft.RedHatOpenShift/STAGING-APPROVED", "Microsoft.RedHatOpenShift/INT-APPROVED"},
			expectedMode:  AFECOwnershipExcludedAFECs,
			expectedExcluded: []string{
				"Microsoft.RedHatOpenShift/STAGING-APPROVED",
				"Microsoft.RedHatOpenShift/INT-APPROVED",
			},
		},
		{
			name:          "Excluded AFECs with whitespace",
			targetAFECs:   "",
			excludedAFECs: []string{" Microsoft.RedHatOpenShift/STAGING-APPROVED ", " Microsoft.RedHatOpenShift/INT-APPROVED "},
			expectedMode:  AFECOwnershipExcludedAFECs,
			expectedExcluded: []string{
				"Microsoft.RedHatOpenShift/STAGING-APPROVED",
				"Microsoft.RedHatOpenShift/INT-APPROVED",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := parseAFECOwnership(tt.targetAFECs, tt.excludedAFECs)

			assert.Equal(t, tt.expectedMode, config.Mode)
			if tt.expectedTargetAFECs != "" {
				assert.Equal(t, tt.expectedTargetAFECs, config.TargetAFECs)
			}
			if len(tt.expectedExcluded) > 0 {
				for _, expected := range tt.expectedExcluded {
					_, exists := config.ExcludedAFECs[expected]
					assert.True(t, exists, "Expected AFEC %s not found in ExcludedAFECs", expected)
				}
				assert.Equal(t, len(tt.expectedExcluded), len(config.ExcludedAFECs))
			}
		})
	}
}

type stubOrphanedMRGResourcesDBClient struct {
	corecosmosstorage.ResourcesDBClient
	getClusters func(string, string) corecosmosstorage.HCPClusterCRUD
}

func (s *stubOrphanedMRGResourcesDBClient) HCPClusters(subscriptionID, resourceGroupName string) corecosmosstorage.HCPClusterCRUD {
	return s.getClusters(subscriptionID, resourceGroupName)
}

type stubOrphanedMRGClusterCRUD struct {
	corecosmosstorage.HCPClusterCRUD
	getCluster func(context.Context, string) (*coreapi.Cluster, error)
}

func (s *stubOrphanedMRGClusterCRUD) Get(ctx context.Context, name string) (*coreapi.Cluster, error) {
	return s.getCluster(ctx, name)
}

func TestOrphanedManagedResourceGroup_ClusterExistence(t *testing.T) {
	cosmosErr := errors.New("Cosmos unavailable")
	tests := []struct {
		name           string
		cachedCluster  bool
		cosmosCluster  *coreapi.Cluster
		cosmosError    error
		expectedOrphan bool
		expectedError  error
	}{
		{
			name:          "cluster found in lister skips Cosmos and Azure",
			cachedCluster: true,
		},
		{
			name:          "cluster missing from lister but found in Cosmos skips Azure",
			cosmosCluster: &coreapi.Cluster{},
		},
		{
			name:           "cluster absent from Cosmos permits Azure deletion",
			cosmosError:    &azcore.ResponseError{StatusCode: http.StatusNotFound},
			expectedOrphan: true,
		},
		{
			name:          "Cosmos error prevents Azure deletion and is returned",
			cosmosError:   cosmosErr,
			expectedError: cosmosErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)
			const targetAFEC = "Microsoft.RedHatOpenShift/STAGING-APPROVED"
			subscription := &coreapi.Subscription{
				Properties: &coreapi.SubscriptionProperties{
					TenantId:           ptr.To("tenant-1"),
					RegisteredFeatures: &[]coreapi.Feature{{Name: ptr.To(targetAFEC), State: ptr.To("Registered")}},
				},
			}
			key := controllerutils.ManagedResourceGroupKey{
				SubscriptionID:    "mrg-subscription",
				ResourceGroupName: "managed-rg",
				ManagedBy:         "/subscriptions/mrg-subscription/resourceGroups/cluster-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
				Location:          "eastus",
			}
			clusters := map[string]*coreapi.Cluster{}
			if tt.cachedCluster {
				clusters["mrg-subscription/cluster-rg/cluster-1"] = &coreapi.Cluster{}
			}
			cosmosReads := 0
			db := &stubOrphanedMRGResourcesDBClient{
				getClusters: func(subscriptionID, resourceGroupName string) corecosmosstorage.HCPClusterCRUD {
					require.False(t, tt.cachedCluster, "a cached cluster must avoid Cosmos reads")
					require.Equal(t, "mrg-subscription", subscriptionID)
					require.Equal(t, "cluster-rg", resourceGroupName)
					return &stubOrphanedMRGClusterCRUD{
						getCluster: func(_ context.Context, name string) (*coreapi.Cluster, error) {
							require.Equal(t, "cluster-1", name)
							cosmosReads++
							return tt.cosmosCluster, tt.cosmosError
						},
					}
				},
			}
			// No Azure expectations for existing clusters or Cosmos errors: any
			// Azure call in those cases fails the test, including client creation.
			builder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
			if tt.expectedOrphan {
				rgClient := azureclient.NewMockResourceGroupsClient(ctrl)
				gomock.InOrder(
					builder.EXPECT().ResourceGroupsClient("tenant-1", key.SubscriptionID).Return(rgClient, nil),
					rgClient.EXPECT().Get(gomock.Any(), key.ResourceGroupName, nil).
						Return(armresources.ResourceGroupsClientGetResponse{ResourceGroup: armresources.ResourceGroup{
							ManagedBy: ptr.To(key.ManagedBy), Location: ptr.To(key.Location),
						}}, nil),
					// A 404 at deletion time completes processing without a poller.
					rgClient.EXPECT().BeginDelete(gomock.Any(), key.ResourceGroupName, nil).
						Return(nil, &azcore.ResponseError{StatusCode: http.StatusNotFound}),
				)
			}
			controller := &orphanedManagedResourceGroupController{
				location:              key.Location,
				resourcesDBClient:     db,
				subscriptionLister:    &stubSubscriptionLister{subscriptions: map[string]*coreapi.Subscription{key.SubscriptionID: subscription}},
				clusterLister:         &stubClusterLister{clusters: clusters},
				azureFPAClientBuilder: builder,
				afecOwnership:         parseAFECOwnership(targetAFEC, nil),
			}

			shouldCleanup, actualSubscription, err := controller.needsWork(ctx, key)
			require.Equal(t, tt.expectedOrphan, shouldCleanup)
			if tt.expectedError != nil {
				require.ErrorIs(t, err, tt.expectedError)
				require.Nil(t, actualSubscription)
			} else {
				require.NoError(t, err)
				require.Same(t, subscription, actualSubscription)
			}
			expectedReads := 1
			if tt.cachedCluster {
				expectedReads = 0
			}
			require.Equal(t, expectedReads, cosmosReads)

			found := orphanedMRGsFound.WithLabelValues(key.Location, key.SubscriptionID, key.ResourceGroupName)
			before := testutil.ToFloat64(found)
			requeueAfter, err := controller.SyncOnce(ctx, key)
			require.Zero(t, requeueAfter)
			if tt.expectedError != nil {
				require.ErrorIs(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
			}
			if tt.expectedOrphan {
				require.Equal(t, before+1, testutil.ToFloat64(found))
			} else {
				require.Equal(t, before, testutil.ToFloat64(found))
			}
			require.Equal(t, 2*expectedReads, cosmosReads)
		})
	}
}

func TestOrphanedManagedResourceGroup_SkipsCrossSubscriptionManagedBy(t *testing.T) {
	const targetAFEC = "Microsoft.RedHatOpenShift/STAGING-APPROVED"
	key := controllerutils.ManagedResourceGroupKey{
		SubscriptionID:    "mrg-subscription",
		ResourceGroupName: "managed-rg",
		ManagedBy:         "/subscriptions/other-subscription/resourceGroups/cluster-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1",
		Location:          "eastus",
	}
	subscription := &coreapi.Subscription{
		Properties: &coreapi.SubscriptionProperties{
			RegisteredFeatures: &[]coreapi.Feature{{Name: ptr.To(targetAFEC), State: ptr.To("Registered")}},
		},
	}
	controller := &orphanedManagedResourceGroupController{
		subscriptionLister: &stubSubscriptionLister{subscriptions: map[string]*coreapi.Subscription{key.SubscriptionID: subscription}},
		afecOwnership:      parseAFECOwnership(targetAFEC, nil),
	}

	// A cross-subscription key must stop before any cluster lookup or Azure delete.
	requeueAfter, err := controller.SyncOnce(utils.ContextWithLogger(t.Context(), testr.New(t)), key)
	require.NoError(t, err)
	require.Zero(t, requeueAfter)
}

func TestSyncOnce_SkipsWhenOwnershipCheckFails(t *testing.T) {
	registeredState := "Registered"
	validManagedBy := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-1"

	key := controllerutils.ManagedResourceGroupKey{
		SubscriptionID:    "sub-1",
		ResourceGroupName: "managed-rg-1",
		ManagedBy:         validManagedBy,
		Location:          "eastus",
	}

	stagingAFEC := "Microsoft.RedHatOpenShift/STAGING-APPROVED"

	tests := []struct {
		name          string
		afecOwnership AFECOwnershipConfig
		subscriptions map[string]*coreapi.Subscription
	}{
		{
			name:          "No ownership AFECs - skips without Azure calls",
			afecOwnership: AFECOwnershipConfig{},
			subscriptions: map[string]*coreapi.Subscription{
				"sub-1": {
					Properties: &coreapi.SubscriptionProperties{
						RegisteredFeatures: &[]coreapi.Feature{},
					},
				},
			},
		},
		{
			name: "TargetAFECs mode - subscription missing required AFEC, skips without Azure calls",
			afecOwnership: AFECOwnershipConfig{
				Mode:        AFECOwnershipTargetAFECs,
				TargetAFECs: stagingAFEC,
			},
			subscriptions: map[string]*coreapi.Subscription{
				"sub-1": {
					Properties: &coreapi.SubscriptionProperties{
						RegisteredFeatures: &[]coreapi.Feature{
							{Name: ptr.To("Microsoft.RedHatOpenShift/INT-APPROVED"), State: &registeredState},
						},
					},
				},
			},
		},
		{
			name: "ExcludedAFECs mode - subscription has excluded AFEC, skips without Azure calls",
			afecOwnership: AFECOwnershipConfig{
				Mode:          AFECOwnershipExcludedAFECs,
				ExcludedAFECs: map[string]struct{}{stagingAFEC: {}},
			},
			subscriptions: map[string]*coreapi.Subscription{
				"sub-1": {
					Properties: &coreapi.SubscriptionProperties{
						RegisteredFeatures: &[]coreapi.Feature{
							{Name: &stagingAFEC, State: &registeredState},
						},
					},
				},
			},
		},
		{
			name: "Subscription not in lister - skips without Azure calls",
			afecOwnership: AFECOwnershipConfig{
				Mode:        AFECOwnershipTargetAFECs,
				TargetAFECs: stagingAFEC,
			},
			subscriptions: map[string]*coreapi.Subscription{}, // empty - sub-1 not found
		},
		{
			name: "nil RegisteredFeatures - fail closed, skips without Azure calls",
			afecOwnership: AFECOwnershipConfig{
				Mode:          AFECOwnershipExcludedAFECs,
				ExcludedAFECs: map[string]struct{}{stagingAFEC: {}},
			},
			subscriptions: map[string]*coreapi.Subscription{
				"sub-1": {
					Properties: &coreapi.SubscriptionProperties{
						RegisteredFeatures: nil,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			// Create a mock FPA client builder. If SyncOnce reaches this,
			// it means ownership check didn't short-circuit - fail the test.
			mockFPABuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
			// No expectations set - any call will fail the test.

			controller := &orphanedManagedResourceGroupController{
				location:              "eastus",
				readOnly:              false,
				subscriptionLister:    &stubSubscriptionLister{subscriptions: tt.subscriptions},
				clusterLister:         &stubClusterLister{clusters: map[string]*coreapi.Cluster{}},
				azureFPAClientBuilder: mockFPABuilder,
				afecOwnership:         tt.afecOwnership,
			}

			requeueAfter, err := controller.SyncOnce(ctx, key)
			require.NoError(t, err)
			require.Zero(t, requeueAfter)
			// If we get here without error and without the mock being called,
			// SyncOnce correctly short-circuited before Azure calls.
		})
	}
}
