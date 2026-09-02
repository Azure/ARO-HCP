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
	clocktesting "k8s.io/utils/clock/testing"
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

// roleAssignmentAlreadyExistsError returns an *azcore.ResponseError that
// azureclient.IsRoleAssignmentAlreadyExistsErr recognizes as a role assignment that
// Cluster Service (or a previous create) already created.
func roleAssignmentAlreadyExistsError() *azcore.ResponseError {
	return &azcore.ResponseError{
		ErrorCode:  "RoleAssignmentExists",
		StatusCode: http.StatusConflict,
		RawResponse: &http.Response{
			Status:     "409 Conflict",
			StatusCode: http.StatusConflict,
			Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"RoleAssignmentExists","message":"The role assignment already exists."}}`)),
			Request: &http.Request{
				Method: http.MethodPut,
				URL:    &url.URL{Scheme: "https", Host: "management.azure.com", Path: "/ra"},
			},
		},
	}
}

// roleAssignmentGenericError returns an *azcore.ResponseError that is neither a
// not-found nor an already-exists error, so a create failing with it must surface.
func roleAssignmentGenericError() *azcore.ResponseError {
	return &azcore.ResponseError{
		ErrorCode:  "AuthorizationFailed",
		StatusCode: http.StatusForbidden,
		RawResponse: &http.Response{
			Status:     "403 Forbidden",
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"AuthorizationFailed","message":"The client does not have authorization."}}`)),
			Request: &http.Request{
				Method: http.MethodPut,
				URL:    &url.URL{Scheme: "https", Host: "management.azure.com", Path: "/ra"},
			},
		},
	}
}

// testFixedNow is the fixed "now" the test fake clock reports, so tests can compute
// earliest-recheck times relative to it deterministically.
func testFixedNow() time.Time {
	return time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
}

func newTestSyncer(mockResourcesDB corecosmosstorage.ResourcesDBClient, fpaClientBuilder azureclient.FirstPartyApplicationClientBuilder) *roleAssignmentsSyncer {
	return &roleAssignmentsSyncer{
		resourcesDBClient:             mockResourcesDB,
		clusterLister:                 &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		serviceProviderClusterLister:  &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		subscriptionLister:            &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{newTestSubscription(ptr.To(testTenantID))}},
		azureFPAClientBuilder:         fpaClientBuilder,
		clusterScopedIdentitiesConfig: testConfig(),
		clock:                         clocktesting.NewFakeClock(testFixedNow()),
	}
}

var testHCPClusterKey = controllerutils.HCPClusterKey{
	SubscriptionID:    testSubscriptionID,
	ResourceGroupName: testResourceGroupName,
	HCPClusterName:    testClusterName,
}

// TestRoleAssignmentsSyncerSyncOnceReconcile exercises the reconcile path end to end
// through the mock Cosmos DB, listers, and Azure client. Each case queries Azure once
// per expected role assignment (one control-plane + one data-plane) and, when Azure
// reports an assignment missing, creates it (mirroring Cluster Service's parameters)
// before promoting it to confirmed.
func TestRoleAssignmentsSyncerSyncOnceReconcile(t *testing.T) {
	t.Parallel()

	expectedIDs := testExpectedRoleAssignmentIDs(t)

	testCases := []struct {
		name            string
		initial         coreapi.AzureMultiReference
		getByIDErr      error
		createErr       error
		expectCreate    bool
		expectPending   []*azcorearm.ResourceID
		expectConfirmed []*azcorearm.ResourceID
	}{
		{
			// Empty start: the expected role assignments are recorded as pending before the
			// Azure write and, since Azure reports them missing, created and confirmed.
			name:            "empty state records pending then creates and confirms",
			initial:         coreapi.AzureMultiReference{},
			getByIDErr:      roleAssignmentNotFoundError(),
			createErr:       nil,
			expectCreate:    true,
			expectPending:   nil,
			expectConfirmed: expectedIDs,
		},
		{
			// Already pending and Azure reports them missing: they are created and confirmed.
			name:            "pending not found is created and confirmed",
			initial:         coreapi.AzureMultiReference{PendingAzureResources: expectedIDs},
			getByIDErr:      roleAssignmentNotFoundError(),
			createErr:       nil,
			expectCreate:    true,
			expectPending:   nil,
			expectConfirmed: expectedIDs,
		},
		{
			// Create races with Cluster Service (already exists): treated as success.
			name:            "create already exists is treated as confirmed",
			initial:         coreapi.AzureMultiReference{PendingAzureResources: expectedIDs},
			getByIDErr:      roleAssignmentNotFoundError(),
			createErr:       roleAssignmentAlreadyExistsError(),
			expectCreate:    true,
			expectPending:   nil,
			expectConfirmed: expectedIDs,
		},
		{
			// Already pending and Azure reports them present: promoted to confirmed, no create.
			name:            "pending is promoted to confirmed when role assignments exist",
			initial:         coreapi.AzureMultiReference{PendingAzureResources: expectedIDs},
			getByIDErr:      nil,
			createErr:       nil,
			expectCreate:    false,
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
			if tc.expectCreate {
				mockRAClient.EXPECT().
					Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), nil).
					DoAndReturn(assertCreateParams(t, expectedIDs, tc.createErr)).
					Times(len(expectedIDs))
			} else {
				mockRAClient.EXPECT().
					Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Times(0)
			}
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

// TestRoleAssignmentsSyncerSyncOnceCreateErrorStaysPending verifies that when creating a
// role assignment fails with a non-"already exists" error, SyncOnce returns the error and
// leaves the assignment pending (never confirmed).
func TestRoleAssignmentsSyncerSyncOnceCreateErrorStaysPending(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	expectedIDs := testExpectedRoleAssignmentIDs(t)

	cluster := newTestCluster(false)
	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, coreapi.AzureMultiReference{
		PendingAzureResources: expectedIDs,
	})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	ctrl := gomock.NewController(t)
	mockRAClient := azureclient.NewMockRoleAssignmentsClient(ctrl)
	// Azure reports every assignment missing; the create fails. SyncOnce returns on the
	// first create error, so the exact number of Get/Create calls is not asserted.
	mockRAClient.EXPECT().
		GetByID(gomock.Any(), gomock.Any(), nil).
		Return(armauthorization.RoleAssignmentsClientGetByIDResponse{}, roleAssignmentNotFoundError()).
		MinTimes(1)
	mockRAClient.EXPECT().
		Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), nil).
		Return(armauthorization.RoleAssignmentsClientCreateResponse{}, roleAssignmentGenericError()).
		MinTimes(1)
	fpaClientBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
	fpaClientBuilder.EXPECT().
		RoleAssignmentsClient(testTenantID, testSubscriptionID).
		Return(mockRAClient, nil).
		Times(1)

	syncer := newTestSyncer(mockResourcesDB, fpaClientBuilder)

	require.Error(t, syncer.SyncOnce(ctx, testHCPClusterKey))

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	got := updated.Status.AzureResources.RoleAssignments
	assertResourceIDSetEqual(t, expectedIDs, got.PendingAzureResources, "PendingAzureResources")
	assertResourceIDSetEqual(t, nil, got.AzureResources, "AzureResources")
}

// TestRoleAssignmentsSyncerSyncOnceRecheckAllPresentReschedules verifies that when the
// earliest-recheck interval has elapsed (nil == due now) and every confirmed role
// assignment is still present in Azure, SyncOnce re-verifies them (no create) and pushes
// the recheck time out.
func TestRoleAssignmentsSyncerSyncOnceRecheckAllPresentReschedules(t *testing.T) {
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
	mockRAClient := azureclient.NewMockRoleAssignmentsClient(ctrl)
	// Every confirmed assignment is re-verified and still present; nothing is created.
	mockRAClient.EXPECT().
		GetByID(gomock.Any(), gomock.Any(), nil).
		Return(armauthorization.RoleAssignmentsClientGetByIDResponse{}, nil).
		Times(len(expectedIDs))
	mockRAClient.EXPECT().
		Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Times(0)
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
	assertResourceIDSetEqual(t, expectedIDs, got.AzureResources, "AzureResources")
	assertResourceIDSetEqual(t, nil, got.PendingAzureResources, "PendingAzureResources")
	assertRecheckScheduled(t, got.EarliestRecheckTime)
}

// TestRoleAssignmentsSyncerSyncOnceRecheckDisappearedRecreates verifies that when a
// confirmed role assignment has disappeared from Azure, the recheck demotes it to pending
// and the create phase re-creates it, leaving it confirmed with a fresh recheck time.
func TestRoleAssignmentsSyncerSyncOnceRecheckDisappearedRecreates(t *testing.T) {
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
	mockRAClient := azureclient.NewMockRoleAssignmentsClient(ctrl)
	// Recheck reports every assignment gone; the create phase then re-Gets (still gone) and
	// creates each, so GetByID is called during both phases.
	mockRAClient.EXPECT().
		GetByID(gomock.Any(), gomock.Any(), nil).
		Return(armauthorization.RoleAssignmentsClientGetByIDResponse{}, roleAssignmentNotFoundError()).
		Times(2 * len(expectedIDs))
	mockRAClient.EXPECT().
		Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), nil).
		DoAndReturn(assertCreateParams(t, expectedIDs, nil)).
		Times(len(expectedIDs))
	fpaClientBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
	// The client is built once for the recheck phase and once for the create phase.
	fpaClientBuilder.EXPECT().
		RoleAssignmentsClient(testTenantID, testSubscriptionID).
		Return(mockRAClient, nil).
		Times(2)

	syncer := newTestSyncer(mockResourcesDB, fpaClientBuilder)

	require.NoError(t, syncer.SyncOnce(ctx, testHCPClusterKey))

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	got := updated.Status.AzureResources.RoleAssignments
	assertResourceIDSetEqual(t, expectedIDs, got.AzureResources, "AzureResources")
	assertResourceIDSetEqual(t, nil, got.PendingAzureResources, "PendingAzureResources")
	assertRecheckScheduled(t, got.EarliestRecheckTime)
}

// TestRoleAssignmentsSyncerSyncOnceSteadyStateSkipsAzure verifies the NeedsWork
// short-circuit end to end: when every expected role assignment is already confirmed,
// nothing is pending, and the earliest-recheck time is still in the future, the controller
// neither builds the FPA client nor queries Azure, and makes no write.
func TestRoleAssignmentsSyncerSyncOnceSteadyStateSkipsAzure(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	expectedIDs := testExpectedRoleAssignmentIDs(t)

	cluster := newTestCluster(false)
	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, coreapi.AzureMultiReference{
		AzureResources:      expectedIDs,
		EarliestRecheckTime: &metav1.Time{Time: testFixedNow().Add(12 * time.Hour)},
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
			name:         "all expected confirmed with future recheck has no work",
			mrgConfirmed: true, cpResolved: true, dpResolved: true,
			roleAssign: coreapi.AzureMultiReference{
				AzureResources:      expectedIDs,
				EarliestRecheckTime: &metav1.Time{Time: testFixedNow().Add(12 * time.Hour)},
			},
			expect: false,
		},
		{
			name:         "all expected confirmed with nil recheck needs work",
			mrgConfirmed: true, cpResolved: true, dpResolved: true,
			roleAssign: coreapi.AzureMultiReference{AzureResources: expectedIDs},
			expect:     true,
		},
		{
			name:         "all expected confirmed with elapsed recheck needs work",
			mrgConfirmed: true, cpResolved: true, dpResolved: true,
			roleAssign: coreapi.AzureMultiReference{
				AzureResources:      expectedIDs,
				EarliestRecheckTime: &metav1.Time{Time: testFixedNow().Add(-time.Minute)},
			},
			expect: true,
		},
	}

	syncer := &roleAssignmentsSyncer{
		clusterScopedIdentitiesConfig: testConfig(),
		clock:                         clocktesting.NewFakeClock(testFixedNow()),
	}
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

// assertCreateParams returns a gomock DoAndReturn for RoleAssignmentsClient.Create that
// verifies the create is issued at the managed resource group scope with the Cluster
// Service-mirrored parameters (PrincipalID, RoleDefinitionID, PrincipalType=ServicePrincipal)
// and the deterministic name / resource ID of one of the expected role assignments, then
// returns createErr.
func assertCreateParams(t *testing.T, expectedIDs []*azcorearm.ResourceID, createErr error) func(context.Context, string, string, armauthorization.RoleAssignmentCreateParameters, *armauthorization.RoleAssignmentsClientCreateOptions) (armauthorization.RoleAssignmentsClientCreateResponse, error) {
	t.Helper()
	return func(_ context.Context, scope, name string, params armauthorization.RoleAssignmentCreateParameters, _ *armauthorization.RoleAssignmentsClientCreateOptions) (armauthorization.RoleAssignmentsClientCreateResponse, error) {
		assert.Equal(t, testManagedResourceGroupScope(t), scope, "create scope must be the managed resource group scope")
		require.NotNil(t, params.Properties, "create parameters must have Properties")
		require.NotNil(t, params.Properties.PrincipalID, "create must set PrincipalID")
		require.NotNil(t, params.Properties.RoleDefinitionID, "create must set RoleDefinitionID")
		require.NotNil(t, params.Properties.PrincipalType, "create must set PrincipalType")
		assert.Equal(t, armauthorization.PrincipalTypeServicePrincipal, *params.Properties.PrincipalType, "create must use the ServicePrincipal principal type")

		// The name and full ID must match the deterministic scheme for these params, and the
		// resulting ID must be one of the expected role assignments.
		wantID := roleassignment.ManagedResourceGroupScopedRoleAssignmentResourceID(scope, *params.Properties.PrincipalID, *params.Properties.RoleDefinitionID)
		parsed := metadataapi.Must(azcorearm.ParseResourceID(wantID))
		assert.Equal(t, parsed.Name, name, "create name must be the deterministic role assignment name")
		found := false
		for _, id := range expectedIDs {
			if strings.EqualFold(id.String(), parsed.String()) {
				found = true
				break
			}
		}
		assert.True(t, found, "created role assignment %q must be one of the expected assignments", parsed.String())

		return armauthorization.RoleAssignmentsClientCreateResponse{}, createErr
	}
}

// assertRecheckScheduled asserts the earliest-recheck time was set to roughly one recheck
// interval from the fake clock's now, within the jittered window [now+interval,
// now+interval*(1+jitterFactor)].
func assertRecheckScheduled(t *testing.T, earliest *metav1.Time) {
	t.Helper()
	require.NotNil(t, earliest, "EarliestRecheckTime must be set once the confirmed set is complete")
	got := earliest.Time
	minTime := testFixedNow().Add(roleAssignmentRecheckInterval)
	maxTime := testFixedNow().Add(roleAssignmentRecheckInterval + time.Duration(roleAssignmentRecheckJitterFactor*float64(roleAssignmentRecheckInterval)))
	assert.False(t, got.Before(minTime), "recheck time %s must be at least the base interval out (%s)", got, minTime)
	assert.False(t, got.After(maxTime), "recheck time %s must be within the jittered window (<= %s)", got, maxTime)
}
