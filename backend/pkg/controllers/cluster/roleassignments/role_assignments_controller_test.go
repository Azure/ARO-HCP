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

	// testControlPlaneOperatorName / testDataPlaneOperatorName are real operator
	// identifiers so their role definitions can be resolved from the cluster-scoped
	// identities config. "control-plane" is control-plane only; "disk-csi-driver" has a
	// data-plane config, so the test cluster exercises both the CP and DP enumeration.
	testControlPlaneOperatorName = string(azure.ClusterOperatorIdentifierControlPlane)
	testDataPlaneOperatorName    = string(azure.ClusterOperatorIdentifierDiskCSIDriver)

	testControlPlaneIdentityID = "/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/cp-identity"
	testDataPlaneIdentityID = "/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/dp-identity"

	testControlPlanePrincipalID = "cp-principal-11111111-1111-1111-1111-111111111111"
	testDataPlanePrincipalID    = "dp-principal-22222222-2222-2222-2222-222222222222"
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

// testExpectedRoleAssignmentIDs returns the role assignment IDs the controller expects
// for the test cluster: one for the control-plane operator and one for the data-plane
// operator.
func testExpectedRoleAssignmentIDs(t *testing.T) []*azcorearm.ResourceID {
	t.Helper()
	config := testConfig()
	scope := testManagedResourceGroupScope(t)

	cpRoleDefs := config.ControlPlaneOperatorsIdentities[azure.ClusterOperatorIdentifier(testControlPlaneOperatorName)].RoleDefinitionsResourceIDs()
	require.NotEmpty(t, cpRoleDefs, "control-plane operator must have at least one role definition")
	dpRoleDefs := config.DataPlaneOperatorsIdentities[azure.ClusterOperatorIdentifier(testDataPlaneOperatorName)].RoleDefinitionsResourceIDs()
	require.NotEmpty(t, dpRoleDefs, "data-plane operator must have at least one role definition")

	cpID := metadataapi.Must(azcorearm.ParseResourceID(
		roleassignment.ManagedResourceGroupScopedRoleAssignmentResourceID(scope, testControlPlanePrincipalID, cpRoleDefs[0].String())))
	dpID := metadataapi.Must(azcorearm.ParseResourceID(
		roleassignment.ManagedResourceGroupScopedRoleAssignmentResourceID(scope, testDataPlanePrincipalID, dpRoleDefs[0].String())))
	return []*azcorearm.ResourceID{cpID, dpID}
}

// newTestCluster builds an HCPOpenShiftCluster addressable by the mock
// ResourcesDBClient with one control-plane and one data-plane operator identity and
// the given deletion state.
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
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators = map[string]*azcorearm.ResourceID{
		testDataPlaneOperatorName: metadataapi.Must(azcorearm.ParseResourceID(testDataPlaneIdentityID)),
	}
	if deleting {
		cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	}
	return cluster
}

// newTestServiceProviderCluster builds a ServiceProviderCluster addressable by the
// mock ResourcesDBClient. When mrgConfirmed is true the managed resource group is
// reflected as confirmed (opening the observation gate); cpResolved / dpResolved
// control whether the control-plane / data-plane operator principal IDs are resolved
// on the status. roleAssignments is the initial observed state.
func newTestServiceProviderCluster(t *testing.T, mrgConfirmed, cpResolved, dpResolved bool, roleAssignments coreapi.AzureMultiReference) *coreapi.ServiceProviderCluster {
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
	if cpResolved {
		serviceProviderCluster.Status.MSIManagedIdentities.ControlPlaneOperatorsIdentities = map[string]*coreapi.ServiceProviderClusterControlPlaneOperatorIdentity{
			strings.ToLower(testControlPlaneIdentityID): {
				ResourceID:  metadataapi.Must(azcorearm.ParseResourceID(testControlPlaneIdentityID)),
				PrincipalID: ptr.To(testControlPlanePrincipalID),
			},
		}
	}
	if dpResolved {
		serviceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.Identities = map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
			strings.ToLower(testDataPlaneIdentityID): {
				ResourceID:  metadataapi.Must(azcorearm.ParseResourceID(testDataPlaneIdentityID)),
				PrincipalID: ptr.To(testDataPlanePrincipalID),
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

// TestRoleAssignmentsSyncerSyncOnceReconcile exercises the reconcile path end to end
// through the mock Cosmos DB, listers, and Azure client. Each case queries Azure once
// per expected role assignment (one control-plane + one data-plane).
func TestRoleAssignmentsSyncerSyncOnceReconcile(t *testing.T) {
	t.Parallel()

	expectedIDs := testExpectedRoleAssignmentIDs(t)

	testCases := []struct {
		name            string
		initial         coreapi.AzureMultiReference
		getByIDErr      error
		expectPending   []*azcorearm.ResourceID
		expectConfirmed []*azcorearm.ResourceID
	}{
		{
			// Empty start: the expected role assignments are recorded as pending before the
			// Get, and (Azure reports them missing) stay pending afterwards.
			name:            "empty state records pending then stays pending when not yet created",
			initial:         coreapi.AzureMultiReference{},
			getByIDErr:      roleAssignmentNotFoundError(),
			expectPending:   expectedIDs,
			expectConfirmed: nil,
		},
		{
			// Already pending and Azure reports them missing: they remain pending (NOOP).
			name:            "pending stays pending when role assignments not found",
			initial:         coreapi.AzureMultiReference{PendingAzureResources: expectedIDs},
			getByIDErr:      roleAssignmentNotFoundError(),
			expectPending:   expectedIDs,
			expectConfirmed: nil,
		},
		{
			// Already pending and Azure reports them present: they are promoted to confirmed.
			name:            "pending is promoted to confirmed when role assignments exist",
			initial:         coreapi.AzureMultiReference{PendingAzureResources: expectedIDs},
			getByIDErr:      nil,
			expectPending:   nil,
			expectConfirmed: expectedIDs,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			cluster := newTestCluster(false)
			serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, tc.initial)

			mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
			require.NoError(t, err)

			ctrl := gomock.NewController(t)
			mockRAClient := azureclient.NewMockRoleAssignmentsClient(ctrl)
			mockRAClient.EXPECT().
				GetByID(gomock.Any(), gomock.Any(), nil).
				Return(armauthorization.RoleAssignmentsClientGetByIDResponse{}, tc.getByIDErr).
				Times(len(expectedIDs))
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
			assertResourceIDSetEqual(t, tc.expectPending, got.PendingAzureResources, "PendingAzureResources")
			assertResourceIDSetEqual(t, tc.expectConfirmed, got.AzureResources, "AzureResources")
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
	expectedIDs := testExpectedRoleAssignmentIDs(t)

	cluster := newTestCluster(false)
	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, coreapi.AzureMultiReference{
		AzureResources: expectedIDs,
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
	assertResourceIDSetEqual(t, expectedIDs, got.AzureResources, "AzureResources")
	assertResourceIDSetEqual(t, nil, got.PendingAzureResources, "PendingAzureResources")
}

// TestRoleAssignmentsSyncerSyncOnceUnresolvedPrincipalSkipsAzure verifies that while a
// principal ID is not yet resolved the controller skips entirely (no FPA client, no
// Azure calls, no write) rather than persisting a partial pending set.
func TestRoleAssignmentsSyncerSyncOnceUnresolvedPrincipalSkipsAzure(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	cluster := newTestCluster(false)
	// Control-plane principal resolved, data-plane principal not resolved yet.
	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, false, coreapi.AzureMultiReference{})

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
	assertResourceIDSetEqual(t, nil, got.PendingAzureResources, "PendingAzureResources")
	assertResourceIDSetEqual(t, nil, got.AzureResources, "AzureResources")
}

// TestRoleAssignmentsSyncerSyncOnceDeletingIsNoOp verifies that a deleting cluster is a
// genuine no-op: the controller never builds the FPA client, never queries Azure, and
// leaves the observed state untouched (even with a pending reference).
func TestRoleAssignmentsSyncerSyncOnceDeletingIsNoOp(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	expectedIDs := testExpectedRoleAssignmentIDs(t)

	cluster := newTestCluster(true) // deleting
	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, coreapi.AzureMultiReference{
		PendingAzureResources: expectedIDs,
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
	assertResourceIDSetEqual(t, expectedIDs, got.PendingAzureResources, "PendingAzureResources")
	assertResourceIDSetEqual(t, nil, got.AzureResources, "AzureResources")
}

// TestRoleAssignmentsSyncerSyncOnceManagedResourceGroupNotConfirmedGate verifies the
// gate: while the managed resource group is not confirmed, the controller does nothing
// (no FPA client, no Azure calls, no write).
func TestRoleAssignmentsSyncerSyncOnceManagedResourceGroupNotConfirmedGate(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	cluster := newTestCluster(false)
	serviceProviderCluster := newTestServiceProviderCluster(t, false, true, true, coreapi.AzureMultiReference{})

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
	assertResourceIDSetEqual(t, nil, got.PendingAzureResources, "PendingAzureResources")
	assertResourceIDSetEqual(t, nil, got.AzureResources, "AzureResources")
}

// TestRoleAssignmentsSyncerNeedsWork verifies the NeedsWork short-circuit, including
// the resolvable-principal gate for both control-plane and data-plane identities.
func TestRoleAssignmentsSyncerNeedsWork(t *testing.T) {
	t.Parallel()

	expectedIDs := testExpectedRoleAssignmentIDs(t)

	testCases := []struct {
		name         string
		mrgConfirmed bool
		cpResolved   bool
		dpResolved   bool
		roleAssign   coreapi.AzureMultiReference
		expect       bool
	}{
		{
			name:         "managed resource group not confirmed has no work",
			mrgConfirmed: false, cpResolved: true, dpResolved: true,
			roleAssign: coreapi.AzureMultiReference{},
			expect:     false,
		},
		{
			name:         "control-plane principal unresolved has no work",
			mrgConfirmed: true, cpResolved: false, dpResolved: true,
			roleAssign: coreapi.AzureMultiReference{},
			expect:     false,
		},
		{
			name:         "data-plane principal unresolved has no work",
			mrgConfirmed: true, cpResolved: true, dpResolved: false,
			roleAssign: coreapi.AzureMultiReference{},
			expect:     false,
		},
		{
			name:         "all principals resolved and empty role assignments needs work",
			mrgConfirmed: true, cpResolved: true, dpResolved: true,
			roleAssign: coreapi.AzureMultiReference{},
			expect:     true,
		},
		{
			name:         "pending role assignment needs work",
			mrgConfirmed: true, cpResolved: true, dpResolved: true,
			roleAssign: coreapi.AzureMultiReference{PendingAzureResources: expectedIDs},
			expect:     true,
		},
		{
			name:         "all expected confirmed has no work",
			mrgConfirmed: true, cpResolved: true, dpResolved: true,
			roleAssign: coreapi.AzureMultiReference{AzureResources: expectedIDs},
			expect:     false,
		},
	}

	syncer := &roleAssignmentsSyncer{clusterScopedIdentitiesConfig: testConfig()}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cluster := newTestCluster(false)
			serviceProviderCluster := newTestServiceProviderCluster(t, tc.mrgConfirmed, tc.cpResolved, tc.dpResolved, tc.roleAssign)
			assert.Equal(t, tc.expect, syncer.NeedsWork(cluster, serviceProviderCluster))
		})
	}
}

// assertResourceIDSetEqual compares two slices of resource IDs as case-insensitive
// sets (the controller's slice order depends on map iteration order).
func assertResourceIDSetEqual(t *testing.T, expected, actual []*azcorearm.ResourceID, field string) {
	t.Helper()
	toSet := func(ids []*azcorearm.ResourceID) map[string]struct{} {
		set := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			require.NotNil(t, id, "%s contains a nil resource ID", field)
			set[strings.ToLower(id.String())] = struct{}{}
		}
		return set
	}
	assert.Equal(t, toSet(expected), toSet(actual), "%s set mismatch", field)
}
