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

// resourceGroupPresentResponseUnclaimed returns a Get response describing an
// existing managed resource group with no ManagedBy set (unclaimed by any cluster).
func resourceGroupPresentResponseUnclaimed() armresources.ResourceGroupsClientGetResponse {
	return armresources.ResourceGroupsClientGetResponse{
		ResourceGroup: armresources.ResourceGroup{
			Name: ptr.To(testManagedRGName),
			Properties: &armresources.ResourceGroupProperties{
				ProvisioningState: ptr.To("Succeeded"),
			},
		},
	}
}

// TestManagedResourceGroupSyncerSyncOnce exercises the switch-based reconcile and
// deletion paths end to end through the mock Cosmos DB, listers, and Azure client.
func TestManagedResourceGroupSyncerSyncOnce(t *testing.T) {
	t.Parallel()

	mrgID := testManagedResourceGroupID(t)
	// ownerClusterID is the ManagedBy value that marks the resource group as owned
	// by this cluster.
	ownerClusterID := newTestCluster(false).ID.String()
	// differentOwnerID is a valid but foreign ManagedBy value (owned by a different
	// cluster) used to exercise the "exists but owned by another cluster" error path.
	differentOwnerID := "/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/other-cluster"
	// sameOwnerDifferentCasingID is this cluster's own ID as Azure returns it:
	// identical to ownerClusterID except for the provider-namespace casing
	// ("Microsoft.RedHatOpenshift" vs "Microsoft.RedHatOpenShift"). ARM IDs are
	// case-insensitive, so this must be treated as owned by THIS cluster (regression
	// guard for the observe-controller hot loop).
	sameOwnerDifferentCasingID := strings.Replace(ownerClusterID, "Microsoft.RedHatOpenShift", "Microsoft.RedHatOpenshift", 1)

	testCases := []struct {
		name              string
		deleting          bool
		initialReference  coreapi.AzureReference
		getResponse       armresources.ResourceGroupsClientGetResponse
		getErr            error
		expectErr         bool
		expectErrContains string
		expectAzure       *azcorearm.ResourceID
		expectPending     *azcorearm.ResourceID
	}{
		{
			// Set-pending-before-Get: the pending marker persisted before the Get stays
			// in place when the resource group does not exist yet, and no actual is set.
			name:             "not deleting and resource group missing keeps pending and no actual",
			deleting:         false,
			initialReference: coreapi.AzureReference{},
			getResponse:      armresources.ResourceGroupsClientGetResponse{},
			getErr:           resourceGroupNotFoundError(),
			expectAzure:      nil,
			expectPending:    mrgID,
		},
		{
			name:             "not deleting and resource group present and owned sets actual and clears pending",
			deleting:         false,
			initialReference: coreapi.AzureReference{},
			getResponse:      resourceGroupPresentResponse(ownerClusterID),
			getErr:           nil,
			expectAzure:      mrgID,
			expectPending:    nil,
		},
		{
			// An existing resource group with no ManagedBy is treated as ours (Cluster
			// Service does not always stamp ManagedBy): actual is set, pending cleared.
			name:             "not deleting and resource group present and unclaimed sets actual and clears pending",
			deleting:         false,
			initialReference: coreapi.AzureReference{},
			getResponse:      resourceGroupPresentResponseUnclaimed(),
			getErr:           nil,
			expectAzure:      mrgID,
			expectPending:    nil,
		},
		{
			// Owned-by-another: returns an error and does NOT set actual; the pending
			// marker recorded before the Get remains.
			name:              "not deleting and resource group owned by another cluster errors and does not set actual",
			deleting:          false,
			initialReference:  coreapi.AzureReference{},
			getResponse:       resourceGroupPresentResponse(differentOwnerID),
			getErr:            nil,
			expectErr:         true,
			expectErrContains: "owned by another cluster",
			expectAzure:       nil,
			expectPending:     mrgID,
		},
		{
			// Owned-by-this-cluster despite provider-namespace casing differences:
			// Azure returns ManagedBy with "Microsoft.RedHatOpenshift" while this
			// cluster's ID uses "Microsoft.RedHatOpenShift". ARM IDs are
			// case-insensitive, so this is NOT owned by another cluster: actual is
			// set and pending cleared, with no error. Regression guard against the
			// observe-controller hot loop.
			name:             "not deleting and resource group owned by this cluster with different provider casing sets actual and clears pending",
			deleting:         false,
			initialReference: coreapi.AzureReference{},
			getResponse:      resourceGroupPresentResponse(sameOwnerDifferentCasingID),
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
			// The resource group exists and is owned by THIS cluster: Cluster Service
			// owns its deletion, so the reference is left in place and the deletion gate
			// stays closed until the resource group is actually gone.
			name:             "deleting and resource group present and owned by this cluster leaves reference in place",
			deleting:         true,
			initialReference: coreapi.AzureReference{AzureResource: mrgID},
			getResponse:      resourceGroupPresentResponse(ownerClusterID),
			getErr:           nil,
			expectAzure:      mrgID,
			expectPending:    nil,
		},
		{
			// The resource group exists but is owned by ANOTHER cluster (a foreign /
			// pre-existing resource group Cluster Service will not delete on our behalf).
			// It is not ours to wait on, so both references are cleared to open the
			// deletion gate rather than blocking cluster deletion forever.
			name:             "deleting and resource group owned by another cluster clears both to unblock deletion",
			deleting:         true,
			initialReference: coreapi.AzureReference{AzureResource: mrgID},
			getResponse:      resourceGroupPresentResponse(differentOwnerID),
			getErr:           nil,
			expectAzure:      nil,
			expectPending:    nil,
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
				clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
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
			if tc.expectErr {
				require.Error(t, err)
				if tc.expectErrContains != "" {
					assert.Contains(t, err.Error(), tc.expectErrContains)
				}
			} else {
				require.NoError(t, err)
			}

			updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)

			gotReference := updated.Status.AzureResources.ManagedResourceGroup
			assertResourceIDEqual(t, tc.expectAzure, gotReference.AzureResource, "AzureResource")
			assertResourceIDEqual(t, tc.expectPending, gotReference.PendingAzureResource, "PendingAzureResource")
		})
	}
}

// TestManagedResourceGroupSyncerNeedsWork verifies the NeedsWork short-circuit:
// a not-deleting cluster has work only until the managed resource group is reflected
// as AzureResource; a deleting cluster has work only while a reference is still set.
func TestManagedResourceGroupSyncerNeedsWork(t *testing.T) {
	t.Parallel()

	mrgID := testManagedResourceGroupID(t)

	testCases := []struct {
		name      string
		deleting  bool
		reference coreapi.AzureReference
		expect    bool
	}{
		{
			name:      "not deleting and already reflected has no work",
			deleting:  false,
			reference: coreapi.AzureReference{AzureResource: mrgID},
			expect:    false,
		},
		{
			name:      "not deleting and only pending needs work",
			deleting:  false,
			reference: coreapi.AzureReference{PendingAzureResource: mrgID},
			expect:    true,
		},
		{
			name:      "not deleting and empty reference needs work",
			deleting:  false,
			reference: coreapi.AzureReference{},
			expect:    true,
		},
		{
			name:      "deleting and confirmed reference needs work",
			deleting:  true,
			reference: coreapi.AzureReference{AzureResource: mrgID},
			expect:    true,
		},
		{
			name:      "deleting and only pending needs work",
			deleting:  true,
			reference: coreapi.AzureReference{PendingAzureResource: mrgID},
			expect:    true,
		},
		{
			name:      "deleting and empty reference has no work",
			deleting:  true,
			reference: coreapi.AzureReference{},
			expect:    false,
		},
	}

	syncer := &managedResourceGroupSyncer{}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cluster := newTestCluster(tc.deleting)
			serviceProviderCluster := newTestServiceProviderCluster(tc.reference)
			assert.Equal(t, tc.expect, syncer.NeedsWork(cluster, serviceProviderCluster))
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
	fpaClientBuilder.EXPECT().
		ResourceGroupsClient(gomock.Any(), gomock.Any()).
		Times(0)

	syncer := &managedResourceGroupSyncer{
		resourcesDBClient:            mockResourcesDB,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
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
// the NeedsWork short-circuit end to end: when the cluster is not being deleted and
// the managed resource group is already reflected as AzureResource, the controller
// neither builds the FPA client nor queries Azure, and makes no write.
func TestManagedResourceGroupSyncerSyncOnceSkipsAzureWhenAlreadyReflected(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	mrgID := testManagedResourceGroupID(t)

	cluster := newTestCluster(false)
	serviceProviderCluster := newTestServiceProviderCluster(coreapi.AzureReference{AzureResource: mrgID})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	ctrl := gomock.NewController(t)
	fpaClientBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
	fpaClientBuilder.EXPECT().
		ResourceGroupsClient(gomock.Any(), gomock.Any()).
		Times(0)

	syncer := &managedResourceGroupSyncer{
		resourcesDBClient:            mockResourcesDB,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
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

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	gotReference := updated.Status.AzureResources.ManagedResourceGroup
	assertResourceIDEqual(t, mrgID, gotReference.AzureResource, "AzureResource")
	assertResourceIDEqual(t, nil, gotReference.PendingAzureResource, "PendingAzureResource")
}

// TestManagedResourceGroupSyncerSyncOnceDeletingKeepsGateClosedOnGetError verifies
// that during deletion, when a reference already holds the gate closed and the
// Azure Get fails with a non-404 error, the controller returns the error and leaves
// the reference unchanged (the gate stays closed).
func TestManagedResourceGroupSyncerSyncOnceDeletingKeepsGateClosedOnGetError(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	mrgID := testManagedResourceGroupID(t)

	cluster := newTestCluster(true) // deleting
	serviceProviderCluster := newTestServiceProviderCluster(coreapi.AzureReference{AzureResource: mrgID})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	ctrl := gomock.NewController(t)
	mockRGClient := azureclient.NewMockResourceGroupsClient(ctrl)
	mockRGClient.EXPECT().
		Get(gomock.Any(), mrgID.Name, nil).
		Return(armresources.ResourceGroupsClientGetResponse{}, errors.New("transient azure error")).
		Times(1)
	fpaClientBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
	fpaClientBuilder.EXPECT().
		ResourceGroupsClient(testTenantID, testSubscriptionID).
		Return(mockRGClient, nil).
		Times(1)

	syncer := &managedResourceGroupSyncer{
		resourcesDBClient:            mockResourcesDB,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
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

	// The reference must be unchanged so the deletion gate stays closed.
	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	gotReference := updated.Status.AzureResources.ManagedResourceGroup
	assertResourceIDEqual(t, mrgID, gotReference.AzureResource, "AzureResource")
	assertResourceIDEqual(t, nil, gotReference.PendingAzureResource, "PendingAzureResource")
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
