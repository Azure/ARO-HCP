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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/azure/roleassignment"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
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

	// testControlPlaneOperatorName is a real control-plane operator identifier so
	// its role definitions can be resolved from the cluster-scoped identities config.
	testControlPlaneOperatorName = string(azure.ClusterOperatorIdentifierControlPlane)
	testControlPlaneIdentityID   = "/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/cp-identity"
	testControlPlanePrincipalID = "cp-principal-11111111-1111-1111-1111-111111111111"
)

// testConfig returns the real cluster-scoped identities config used to enumerate
// operator role definitions.
func testConfig() *azure.ClusterScopedIdentitiesConfig {
	return azure.NewClusterScopedIdentitiesConfig(azure.RoleDefinitionConfigSetNameDev)
}

// testManagedResourceGroupScope returns the MRG scope the controller derives from
// the cluster's CustomerProperties.Platform.ManagedResourceGroup.
func testManagedResourceGroupScope(t *testing.T) string {
	t.Helper()
	return metadataapi.Must(coreapi.ToResourceGroupResourceID(testSubscriptionID, testManagedRGName)).String()
}

// testManagedResourceGroupID returns the confirmed managed resource group reference
// resource ID used to open the observation gate.
func testManagedResourceGroupID(t *testing.T) *azcorearm.ResourceID {
	t.Helper()
	return metadataapi.Must(coreapi.ToResourceGroupResourceID(testSubscriptionID, testManagedRGName))
}

// testExpectedRoleAssignmentID returns the single role assignment ID the controller
// expects for the test cluster's one control-plane operator.
func testExpectedRoleAssignmentID(t *testing.T) *azcorearm.ResourceID {
	t.Helper()
	roleDefinitionIDs := testConfig().ControlPlaneOperatorsIdentities[azure.ClusterOperatorIdentifier(testControlPlaneOperatorName)].RoleDefinitionsResourceIDs()
	require.NotEmpty(t, roleDefinitionIDs, "control-plane operator must have at least one role definition")
	fullID := roleassignment.ManagedResourceGroupScopedRoleAssignmentResourceID(
		testManagedResourceGroupScope(t),
		testControlPlanePrincipalID,
		roleDefinitionIDs[0].String(),
	)
	return metadataapi.Must(azcorearm.ParseResourceID(fullID))
}

// newTestCluster builds an HCPOpenShiftCluster addressable by the mock
// ResourcesDBClient with a single control-plane operator identity and the given
// deletion state.
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
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = map[string]*azcorearm.ResourceID{
		testControlPlaneOperatorName: metadataapi.Must(azcorearm.ParseResourceID(testControlPlaneIdentityID)),
	}
	if deleting {
		cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	}
	return cluster
}

// newTestServiceProviderCluster builds a ServiceProviderCluster addressable by the
// mock ResourcesDBClient. When mrgConfirmed is true the managed resource group is
// reflected as confirmed (opening the observation gate); when principalResolved is
// true the control-plane operator's principal ID is present so the expected role
// assignment set can be computed. roleAssignments is the initial observed state.
func newTestServiceProviderCluster(t *testing.T, mrgConfirmed, principalResolved bool, roleAssignments coreapi.AzureMultiReference) *coreapi.ServiceProviderCluster {
	t.Helper()
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
	if mrgConfirmed {
		serviceProviderCluster.Status.AzureResources.ManagedResourceGroup.AzureResource = testManagedResourceGroupID(t)
	}
	if principalResolved {
		serviceProviderCluster.Status.MSIManagedIdentities.ControlPlaneOperatorsIdentities = map[string]*coreapi.ServiceProviderClusterControlPlaneOperatorIdentity{
			strings.ToLower(testControlPlaneIdentityID): {
				ResourceID:  metadataapi.Must(azcorearm.ParseResourceID(testControlPlaneIdentityID)),
				PrincipalID: ptr.To(testControlPlanePrincipalID),
			},
		}
	}
	serviceProviderCluster.Status.AzureResources.RoleAssignments = roleAssignments
	return serviceProviderCluster
}

// newTestSubscription builds a Subscription for the SliceSubscriptionLister.
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

// roleAssignmentNotFoundError returns an *azcore.ResponseError that
// azureclient.IsRoleAssignmentNotFoundErr recognizes as a missing role assignment.
func roleAssignmentNotFoundError() *azcore.ResponseError {
	return &azcore.ResponseError{
		ErrorCode:  "RoleAssignmentNotFound",
		StatusCode: http.StatusNotFound,
		RawResponse: &http.Response{
			Status:     "404 Not Found",
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"RoleAssignmentNotFound","message":"Role assignment not found."}}`)),
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    &url.URL{Scheme: "https", Host: "management.azure.com", Path: "/ra"},
			},
		},
	}
}

func newTestSyncer(mockResourcesDB corecosmosstorage.ResourcesDBClient, fpaClientBuilder azureclient.FirstPartyApplicationClientBuilder) *roleAssignmentsSyncer {
	return &roleAssignmentsSyncer{
		resourcesDBClient:             mockResourcesDB,
		clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:            &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{newTestSubscription(ptr.To(testTenantID))}},
		azureFPAClientBuilder:         fpaClientBuilder,
		clusterScopedIdentitiesConfig: testConfig(),
	}
}

var testHCPClusterKey = controllerutils.HCPClusterKey{
	SubscriptionID:    testSubscriptionID,
	ResourceGroupName: testResourceGroupName,
	HCPClusterName:    testClusterName,
}

// TestRoleAssignmentsSyncerSyncOnceReconcile exercises the reconcile path end to
// end through the mock Cosmos DB, listers, and Azure client for the cases where the
// controller queries Azure exactly once.
func TestRoleAssignmentsSyncerSyncOnceReconcile(t *testing.T) {
	t.Parallel()

	expectedID := testExpectedRoleAssignmentID(t)

	testCases := []struct {
		name            string
		initial         coreapi.AzureMultiReference
		getByIDErr      error
		expectPending   []*azcorearm.ResourceID
		expectConfirmed []*azcorearm.ResourceID
	}{
		{
			// Empty start: the expected role assignment is recorded as pending before the
			// Get, and (Azure reports it missing) stays pending afterwards.
			name:            "empty state records pending then stays pending when not yet created",
			initial:         coreapi.AzureMultiReference{},
			getByIDErr:      roleAssignmentNotFoundError(),
			expectPending:   []*azcorearm.ResourceID{expectedID},
			expectConfirmed: nil,
		},
		{
			// Already pending and Azure reports it missing: it remains pending (NOOP).
			name:            "pending stays pending when role assignment not found",
			initial:         coreapi.AzureMultiReference{PendingAzureResources: []*azcorearm.ResourceID{expectedID}},
			getByIDErr:      roleAssignmentNotFoundError(),
			expectPending:   []*azcorearm.ResourceID{expectedID},
			expectConfirmed: nil,
		},
		{
			// Already pending and Azure reports it exists: it is promoted to confirmed.
			name:            "pending is promoted to confirmed when role assignment exists",
			initial:         coreapi.AzureMultiReference{PendingAzureResources: []*azcorearm.ResourceID{expectedID}},
			getByIDErr:      nil,
			expectPending:   nil,
			expectConfirmed: []*azcorearm.ResourceID{expectedID},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			cluster := newTestCluster(false)
			serviceProviderCluster := newTestServiceProviderCluster(t, true, true, tc.initial)

			mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
			require.NoError(t, err)

			ctrl := gomock.NewController(t)
			mockRAClient := azureclient.NewMockRoleAssignmentsClient(ctrl)
			mockRAClient.EXPECT().
				GetByID(gomock.Any(), expectedID.String(), nil).
				Return(armauthorization.RoleAssignmentsClientGetByIDResponse{}, tc.getByIDErr).
				Times(1)
			fpaClientBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
			fpaClientBuilder.EXPECT().
				RoleAssignmentsClient(testTenantID, testSubscriptionID).
				Return(mockRAClient, nil).
				Times(1)

			syncer := newTestSyncer(mockResourcesDB, fpaClientBuilder)

			require.NoError(t, syncer.SyncOnce(ctx, testHCPClusterKey))

			updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)

			got := updated.Status.AzureResources.RoleAssignments
			assertResourceIDsEqual(t, tc.expectPending, got.PendingAzureResources, "PendingAzureResources")
			assertResourceIDsEqual(t, tc.expectConfirmed, got.AzureResources, "AzureResources")
		})
	}
}

// TestRoleAssignmentsSyncerSyncOnceSteadyStateSkipsAzure verifies the NeedsWork
// short-circuit end to end: when every expected role assignment is already confirmed
// and nothing is pending, the controller neither builds the FPA client nor queries
// Azure, and makes no write.
func TestRoleAssignmentsSyncerSyncOnceSteadyStateSkipsAzure(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	expectedID := testExpectedRoleAssignmentID(t)

	cluster := newTestCluster(false)
	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, coreapi.AzureMultiReference{
		AzureResources: []*azcorearm.ResourceID{expectedID},
	})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	ctrl := gomock.NewController(t)
	fpaClientBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
	fpaClientBuilder.EXPECT().RoleAssignmentsClient(gomock.Any(), gomock.Any()).Times(0)

	syncer := newTestSyncer(mockResourcesDB, fpaClientBuilder)

	require.NoError(t, syncer.SyncOnce(ctx, testHCPClusterKey))

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	got := updated.Status.AzureResources.RoleAssignments
	assertResourceIDsEqual(t, []*azcorearm.ResourceID{expectedID}, got.AzureResources, "AzureResources")
	assertResourceIDsEqual(t, nil, got.PendingAzureResources, "PendingAzureResources")
}

// TestRoleAssignmentsSyncerSyncOnceDeletingIsNoOp verifies that a deleting cluster
// is a genuine no-op: the controller never builds the FPA client, never queries
// Azure, and leaves the observed state untouched (even with a pending reference).
func TestRoleAssignmentsSyncerSyncOnceDeletingIsNoOp(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	expectedID := testExpectedRoleAssignmentID(t)

	cluster := newTestCluster(true) // deleting
	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, coreapi.AzureMultiReference{
		PendingAzureResources: []*azcorearm.ResourceID{expectedID},
	})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	ctrl := gomock.NewController(t)
	fpaClientBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
	fpaClientBuilder.EXPECT().RoleAssignmentsClient(gomock.Any(), gomock.Any()).Times(0)

	syncer := newTestSyncer(mockResourcesDB, fpaClientBuilder)

	require.NoError(t, syncer.SyncOnce(ctx, testHCPClusterKey))

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	got := updated.Status.AzureResources.RoleAssignments
	// The reference must be unchanged (still pending, nothing confirmed).
	assertResourceIDsEqual(t, []*azcorearm.ResourceID{expectedID}, got.PendingAzureResources, "PendingAzureResources")
	assertResourceIDsEqual(t, nil, got.AzureResources, "AzureResources")
}

// TestRoleAssignmentsSyncerSyncOnceManagedResourceGroupNotConfirmedGate verifies the
// gate: while the managed resource group is not confirmed, the controller does
// nothing (no FPA client, no Azure calls, no write).
func TestRoleAssignmentsSyncerSyncOnceManagedResourceGroupNotConfirmedGate(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	cluster := newTestCluster(false)
	serviceProviderCluster := newTestServiceProviderCluster(t, false, true, coreapi.AzureMultiReference{})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	ctrl := gomock.NewController(t)
	fpaClientBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
	fpaClientBuilder.EXPECT().RoleAssignmentsClient(gomock.Any(), gomock.Any()).Times(0)

	syncer := newTestSyncer(mockResourcesDB, fpaClientBuilder)

	require.NoError(t, syncer.SyncOnce(ctx, testHCPClusterKey))

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	got := updated.Status.AzureResources.RoleAssignments
	assertResourceIDsEqual(t, nil, got.PendingAzureResources, "PendingAzureResources")
	assertResourceIDsEqual(t, nil, got.AzureResources, "AzureResources")
}

// TestRoleAssignmentsSyncerNeedsWork verifies the NeedsWork short-circuit.
func TestRoleAssignmentsSyncerNeedsWork(t *testing.T) {
	t.Parallel()

	expectedID := testExpectedRoleAssignmentID(t)

	testCases := []struct {
		name              string
		mrgConfirmed      bool
		principalResolved bool
		roleAssignments   coreapi.AzureMultiReference
		expect            bool
	}{
		{
			name:         "managed resource group not confirmed has no work",
			mrgConfirmed: false, principalResolved: true,
			roleAssignments: coreapi.AzureMultiReference{},
			expect:          false,
		},
		{
			name:         "confirmed mrg and empty role assignments needs work",
			mrgConfirmed: true, principalResolved: true,
			roleAssignments: coreapi.AzureMultiReference{},
			expect:          true,
		},
		{
			name:         "pending role assignment needs work",
			mrgConfirmed: true, principalResolved: true,
			roleAssignments: coreapi.AzureMultiReference{PendingAzureResources: []*azcorearm.ResourceID{expectedID}},
			expect:          true,
		},
		{
			name:         "all expected confirmed has no work",
			mrgConfirmed: true, principalResolved: true,
			roleAssignments: coreapi.AzureMultiReference{AzureResources: []*azcorearm.ResourceID{expectedID}},
			expect:          false,
		},
		{
			// Unresolved principal ID means the desired set cannot be computed yet, so
			// there is work to do (SyncOnce will surface the retryable error).
			name:         "unresolved principal needs work",
			mrgConfirmed: true, principalResolved: false,
			roleAssignments: coreapi.AzureMultiReference{},
			expect:          true,
		},
	}

	syncer := &roleAssignmentsSyncer{clusterScopedIdentitiesConfig: testConfig()}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cluster := newTestCluster(false)
			serviceProviderCluster := newTestServiceProviderCluster(t, tc.mrgConfirmed, tc.principalResolved, tc.roleAssignments)
			assert.Equal(t, tc.expect, syncer.NeedsWork(cluster, serviceProviderCluster))
		})
	}
}

// assertResourceIDsEqual compares two slices of optional resource IDs by their
// canonical string form.
func assertResourceIDsEqual(t *testing.T, expected, actual []*azcorearm.ResourceID, field string) {
	t.Helper()
	require.Equal(t, len(expected), len(actual), "%s length mismatch (expected %d, got %d)", field, len(expected), len(actual))
	for i := range expected {
		require.NotNil(t, actual[i], "%s[%d] should not be nil", field, i)
		assert.Equal(t, expected[i].String(), actual[i].String(), "%s[%d] resource ID mismatch", field, i)
	}
}
