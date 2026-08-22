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

package azureresources

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
	testTenantID          = "test-tenant-id"
	testManagedRGName     = "test-managed-rg"
)

// testManagedResourceGroupID returns the resource ID the controller derives from
// the cluster's CustomerProperties.Platform.ManagedResourceGroup.
func testManagedResourceGroupID(t *testing.T) *azcorearm.ResourceID {
	t.Helper()
	return metadataapi.Must(coreapi.ToResourceGroupResourceID(testSubscriptionID, testManagedRGName))
}

// newTestCluster builds an HCPOpenShiftCluster addressable by the mock
// ResourcesDBClient with the given managed resource group name and deletion state.
func newTestCluster(deleting bool) *coreapi.HCPOpenShiftCluster {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))

	cluster := &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: testClusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
	}
	cluster.CustomerProperties.Platform.ManagedResourceGroup = testManagedRGName
	if deleting {
		cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	}
	return cluster
}

// newTestServiceProviderCluster builds a ServiceProviderCluster addressable by the
// mock ResourcesDBClient with the given managed resource group reference.
func newTestServiceProviderCluster(reference coreapi.AzureReference) *coreapi.ServiceProviderCluster {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/" + coreapi.ServiceProviderClusterResourceTypeName +
			"/" + coreapi.ServiceProviderClusterResourceName,
	))

	serviceProviderCluster := &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
	}
	serviceProviderCluster.Status.AzureResources.ManagedResourceGroup = reference
	return serviceProviderCluster
}

// newTestSubscription builds a Subscription for the SliceSubscriptionLister with
// the given tenant ID.
func newTestSubscription(tenantID *string) *coreapi.Subscription {
	subscriptionResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID))
	return &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   subscriptionResourceID,
			PartitionKey: strings.ToLower(subscriptionResourceID.SubscriptionID),
		},
		ResourceID: subscriptionResourceID,
		Properties: &coreapi.SubscriptionProperties{
			TenantId: tenantID,
		},
	}
}

// resourceGroupNotFoundError returns an *azcore.ResponseError that
// azureclient.IsResourceGroupNotFoundErr recognizes as a missing resource group.
func resourceGroupNotFoundError() *azcore.ResponseError {
	return &azcore.ResponseError{
		ErrorCode:  "ResourceGroupNotFound",
		StatusCode: http.StatusNotFound,
		RawResponse: &http.Response{
			Status:     "404 Not Found",
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"ResourceGroupNotFound","message":"Resource group not found."}}`)),
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    &url.URL{Scheme: "https", Host: "management.azure.com", Path: "/rg"},
			},
		},
	}
}

// resourceGroupPresentResponse returns a Get response describing an existing
// managed resource group whose ManagedBy is set to the given owner resource ID.
func resourceGroupPresentResponse(managedBy string) armresources.ResourceGroupsClientGetResponse {
	return armresources.ResourceGroupsClientGetResponse{
		ResourceGroup: armresources.ResourceGroup{
			Name:      ptr.To(testManagedRGName),
			ManagedBy: ptr.To(managedBy),
			Properties: &armresources.ResourceGroupProperties{
				ProvisioningState: ptr.To("Succeeded"),
			},
		},
	}
}

func TestManagedResourceGroupSyncerSyncOnce(t *testing.T) {
	t.Parallel()

	mrgID := testManagedResourceGroupID(t)
	// ownerClusterID is the resource ID a managed resource group's ManagedBy must
	// equal for the controller to treat it as owned by this cluster.
	ownerClusterID := newTestCluster(false).ID.String()
	// differentOwnerID is a ManagedBy value belonging to a different cluster, used
	// to exercise the "exists but not owned by us" path.
	differentOwnerID := "/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/other-cluster"
	// unparseableManagedBy is not a valid Azure resource ID, so ParseResourceID
	// fails and the resource group must be treated as not owned by this cluster.
	unparseableManagedBy := "not-a-valid-resource-id"

	testCases := []struct {
		name             string
		deleting         bool
		initialReference coreapi.AzureReference
		getResponse      armresources.ResourceGroupsClientGetResponse
		getErr           error
		expectAzure      *azcorearm.ResourceID
		expectPending    *azcorearm.ResourceID
		expectErr        bool
	}{
		{
			name:             "not deleting and resource group missing sets pending",
			deleting:         false,
			initialReference: coreapi.AzureReference{},
			getResponse:      armresources.ResourceGroupsClientGetResponse{},
			getErr:           resourceGroupNotFoundError(),
			expectAzure:      nil,
			expectPending:    mrgID,
		},
		{
			name:             "not deleting and resource group present sets actual and clears pending",
			deleting:         false,
			initialReference: coreapi.AzureReference{PendingAzureResource: mrgID},
			getResponse:      resourceGroupPresentResponse(ownerClusterID),
			getErr:           nil,
			expectAzure:      mrgID,
			expectPending:    nil,
		},
		{
			name:             "deleting and resource group gone clears both",
			deleting:         true,
			initialReference: coreapi.AzureReference{AzureResource: mrgID},
			getResponse:      armresources.ResourceGroupsClientGetResponse{},
			getErr:           resourceGroupNotFoundError(),
			expectAzure:      nil,
			expectPending:    nil,
		},
		{
			name:             "deleting and resource group present makes no change",
			deleting:         true,
			initialReference: coreapi.AzureReference{AzureResource: mrgID},
			getResponse:      resourceGroupPresentResponse(ownerClusterID),
			getErr:           nil,
			expectAzure:      mrgID,
			expectPending:    nil,
		},
		{
			// Regression test for the deletion-gate race: a cluster deleted shortly
			// after creation may start deletion with an empty reference while the
			// managed resource group still exists in Azure. The controller must
			// still record AzureResource so the deletion gate blocks removal of the
			// ServiceProviderCluster document.
			name:             "deleting and resource group present with empty reference sets actual",
			deleting:         true,
			initialReference: coreapi.AzureReference{},
			getResponse:      resourceGroupPresentResponse(ownerClusterID),
			getErr:           nil,
			expectAzure:      mrgID,
			expectPending:    nil,
		},
		{
			// The resource group exists but is managed by a different cluster, so it
			// must be treated as not-ours: reflect as pending, never claim it.
			name:             "not deleting and resource group present but not owned sets pending",
			deleting:         false,
			initialReference: coreapi.AzureReference{},
			getResponse:      resourceGroupPresentResponse(differentOwnerID),
			getErr:           nil,
			expectAzure:      nil,
			expectPending:    mrgID,
		},
		{
			// During deletion a resource group owned by a different cluster must not
			// block this cluster's deletion: clear both references (open the gate).
			name:             "deleting and resource group present but not owned clears both",
			deleting:         true,
			initialReference: coreapi.AzureReference{AzureResource: mrgID},
			getResponse:      resourceGroupPresentResponse(differentOwnerID),
			getErr:           nil,
			expectAzure:      nil,
			expectPending:    nil,
		},
		{
			// A resource group whose ManagedBy is not a parseable resource ID must be
			// treated as not owned: while not deleting, reflect it as pending.
			name:             "not deleting and resource group ManagedBy unparseable sets pending",
			deleting:         false,
			initialReference: coreapi.AzureReference{},
			getResponse:      resourceGroupPresentResponse(unparseableManagedBy),
			getErr:           nil,
			expectAzure:      nil,
			expectPending:    mrgID,
		},
		{
			// A resource group whose ManagedBy is not a parseable resource ID must be
			// treated as not owned: while deleting, clear both references.
			name:             "deleting and resource group ManagedBy unparseable clears both",
			deleting:         true,
			initialReference: coreapi.AzureReference{AzureResource: mrgID},
			getResponse:      resourceGroupPresentResponse(unparseableManagedBy),
			getErr:           nil,
			expectAzure:      nil,
			expectPending:    nil,
		},
		{
			// Fail-closed: during deletion a transient (non-404) Azure error with no
			// reference yet must record the MRG as pending so the deletion gate stays
			// closed, and still surface the error so the syncer retries.
			name:             "deleting and transient Get error with empty reference sets pending and errors",
			deleting:         true,
			initialReference: coreapi.AzureReference{},
			getResponse:      armresources.ResourceGroupsClientGetResponse{},
			getErr:           errors.New("transient azure error"),
			expectAzure:      nil,
			expectPending:    mrgID,
			expectErr:        true,
		},
		{
			// Fail-closed: during deletion a transient (non-404) Azure error when the
			// reference already holds the gate closed makes no extra write and still
			// surfaces the error.
			name:             "deleting and transient Get error with existing reference errors without change",
			deleting:         true,
			initialReference: coreapi.AzureReference{AzureResource: mrgID},
			getResponse:      armresources.ResourceGroupsClientGetResponse{},
			getErr:           errors.New("transient azure error"),
			expectAzure:      mrgID,
			expectPending:    nil,
			expectErr:        true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			cluster := newTestCluster(tc.deleting)
			serviceProviderCluster := newTestServiceProviderCluster(tc.initialReference)

			mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
			require.NoError(t, err)

			ctrl := gomock.NewController(t)
			mockRGClient := azureclient.NewMockResourceGroupsClient(ctrl)
			mockRGClient.EXPECT().
				Get(gomock.Any(), mrgID.Name, nil).
				Return(tc.getResponse, tc.getErr).
				Times(1)

			fpaClientBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
			fpaClientBuilder.EXPECT().
				ResourceGroupsClient(testTenantID, testSubscriptionID).
				Return(mockRGClient, nil).
				Times(1)

			syncer := &managedResourceGroupSyncer{
				resourcesDBClient:            mockResourcesDB,
				serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
				subscriptionLister:           &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{newTestSubscription(ptr.To(testTenantID))}},
				azureFPAClientBuilder:        fpaClientBuilder,
			}

			key := controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
			}

			syncErr := syncer.SyncOnce(ctx, key)
			if tc.expectErr {
				require.Error(t, syncErr)
			} else {
				require.NoError(t, syncErr)
			}

			updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)

			gotReference := updated.Status.AzureResources.ManagedResourceGroup
			assertResourceIDEqual(t, tc.expectAzure, gotReference.AzureResource, "AzureResource")
			assertResourceIDEqual(t, tc.expectPending, gotReference.PendingAzureResource, "PendingAzureResource")
		})
	}
}

// TestManagedResourceGroupSyncerSyncOnceEmptyManagedResourceGroupName verifies that
// a cluster without a managed resource group name is a hard error (a cluster should
// always have one) and that Azure is never queried.
func TestManagedResourceGroupSyncerSyncOnceEmptyManagedResourceGroupName(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	cluster := newTestCluster(false)
	cluster.CustomerProperties.Platform.ManagedResourceGroup = ""
	serviceProviderCluster := newTestServiceProviderCluster(coreapi.AzureReference{})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	ctrl := gomock.NewController(t)
	fpaClientBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
	// The FPA client builder must not be called when there is no MRG name to observe.
	fpaClientBuilder.EXPECT().
		ResourceGroupsClient(gomock.Any(), gomock.Any()).
		Times(0)

	syncer := &managedResourceGroupSyncer{
		resourcesDBClient:            mockResourcesDB,
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:           &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{newTestSubscription(ptr.To(testTenantID))}},
		azureFPAClientBuilder:        fpaClientBuilder,
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	err = syncer.SyncOnce(ctx, key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "managed resource group name is empty")
}

// TestManagedResourceGroupSyncerSyncOnceSkipsAzureWhenAlreadyReflected verifies
// that when the cluster is not being deleted and the ServiceProviderCluster
// already reflects the managed resource group as AzureResource, the controller
// short-circuits without building the FPA client or calling Azure Get.
func TestManagedResourceGroupSyncerSyncOnceSkipsAzureWhenAlreadyReflected(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	mrgID := testManagedResourceGroupID(t)

	cluster := newTestCluster(false)
	serviceProviderCluster := newTestServiceProviderCluster(coreapi.AzureReference{AzureResource: mrgID})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	ctrl := gomock.NewController(t)
	// Neither the FPA client build nor the Azure Get must happen: the reflected
	// state is already up to date and the cluster is not being deleted.
	mockRGClient := azureclient.NewMockResourceGroupsClient(ctrl)
	mockRGClient.EXPECT().
		Get(gomock.Any(), gomock.Any(), gomock.Any()).
		Times(0)
	fpaClientBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
	fpaClientBuilder.EXPECT().
		ResourceGroupsClient(gomock.Any(), gomock.Any()).
		Times(0)

	syncer := &managedResourceGroupSyncer{
		resourcesDBClient:            mockResourcesDB,
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:           &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{newTestSubscription(ptr.To(testTenantID))}},
		azureFPAClientBuilder:        fpaClientBuilder,
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	require.NoError(t, syncer.SyncOnce(ctx, key))

	// The reflected reference must be unchanged.
	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	gotReference := updated.Status.AzureResources.ManagedResourceGroup
	assertResourceIDEqual(t, mrgID, gotReference.AzureResource, "AzureResource")
	assertResourceIDEqual(t, nil, gotReference.PendingAzureResource, "PendingAzureResource")
}

// TestManagedResourceGroupSyncerSyncOnceFailClosedOnFPAClientBuildError verifies
// that when the FPA ResourceGroups client cannot be built during deletion, the
// controller fails closed: it records a pending marker when no reference has been
// recorded yet (keeping the deletion gate closed), makes no extra write when a
// reference already holds the gate closed, and returns the error in both cases.
func TestManagedResourceGroupSyncerSyncOnceFailClosedOnFPAClientBuildError(t *testing.T) {
	t.Parallel()

	mrgID := testManagedResourceGroupID(t)

	testCases := []struct {
		name             string
		initialReference coreapi.AzureReference
		expectAzure      *azcorearm.ResourceID
		expectPending    *azcorearm.ResourceID
	}{
		{
			name:             "empty reference records pending",
			initialReference: coreapi.AzureReference{},
			expectAzure:      nil,
			expectPending:    mrgID,
		},
		{
			name:             "existing reference is left unchanged",
			initialReference: coreapi.AzureReference{AzureResource: mrgID},
			expectAzure:      mrgID,
			expectPending:    nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			cluster := newTestCluster(true) // deleting
			serviceProviderCluster := newTestServiceProviderCluster(tc.initialReference)

			mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
			require.NoError(t, err)

			ctrl := gomock.NewController(t)
			fpaClientBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
			// The FPA client build fails; Azure Get is therefore never reached.
			fpaClientBuilder.EXPECT().
				ResourceGroupsClient(testTenantID, testSubscriptionID).
				Return(nil, errors.New("fpa build error")).
				Times(1)

			syncer := &managedResourceGroupSyncer{
				resourcesDBClient:            mockResourcesDB,
				serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
				subscriptionLister:           &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{newTestSubscription(ptr.To(testTenantID))}},
				azureFPAClientBuilder:        fpaClientBuilder,
			}

			key := controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
			}

			require.Error(t, syncer.SyncOnce(ctx, key))

			updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			gotReference := updated.Status.AzureResources.ManagedResourceGroup
			assertResourceIDEqual(t, tc.expectAzure, gotReference.AzureResource, "AzureResource")
			assertResourceIDEqual(t, tc.expectPending, gotReference.PendingAzureResource, "PendingAzureResource")
		})
	}
}

// TestManagedResourceGroupSyncerSyncOnceFailClosedOnSubscriptionResolutionError
// verifies that when the subscription (and thus tenant) cannot be resolved during
// deletion with an empty reference, the controller records a pending marker to keep
// the deletion gate closed and returns the error, without ever building the FPA
// client or querying Azure.
func TestManagedResourceGroupSyncerSyncOnceFailClosedOnSubscriptionResolutionError(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	mrgID := testManagedResourceGroupID(t)

	cluster := newTestCluster(true) // deleting
	serviceProviderCluster := newTestServiceProviderCluster(coreapi.AzureReference{})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	ctrl := gomock.NewController(t)
	fpaClientBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
	// Subscription resolution fails first, so the FPA client is never built.
	fpaClientBuilder.EXPECT().
		ResourceGroupsClient(gomock.Any(), gomock.Any()).
		Times(0)

	syncer := &managedResourceGroupSyncer{
		resourcesDBClient:            mockResourcesDB,
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		// Empty subscription lister -> Get returns not-found, so tenant resolution fails.
		subscriptionLister:    &corelistertesting.SliceSubscriptionLister{Subscriptions: nil},
		azureFPAClientBuilder: fpaClientBuilder,
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	require.Error(t, syncer.SyncOnce(ctx, key))

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	gotReference := updated.Status.AzureResources.ManagedResourceGroup
	assertResourceIDEqual(t, nil, gotReference.AzureResource, "AzureResource")
	assertResourceIDEqual(t, mrgID, gotReference.PendingAzureResource, "PendingAzureResource")
}

// assertResourceIDEqual compares two optional resource IDs by their canonical string form.
func assertResourceIDEqual(t *testing.T, expected, actual *azcorearm.ResourceID, field string) {
	t.Helper()
	if expected == nil {
		assert.Nil(t, actual, "%s should be nil", field)
		return
	}
	require.NotNil(t, actual, "%s should not be nil", field)
	assert.Equal(t, expected.String(), actual.String(), "%s resource ID mismatch", field)
}
