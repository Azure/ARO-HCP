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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/azuremockclient"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azure"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
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
	testServiceManagedIdentityID = "/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi-identity"

	testControlPlanePrincipalID           = "cp-principal-11111111-1111-1111-1111-111111111111"
	testControlPlaneARMPrincipalID        = "arm-principal-cp-33333333-3333-3333-3333-333333333333"
	testDataPlanePrincipalID              = "dp-principal-22222222-2222-2222-2222-222222222222"
	testServiceManagedIdentityPrincipalID = "smi-principal-44444444-4444-4444-4444-444444444444"
)

func TestClusterRoleAssignmentsNeedsWork(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	future := metav1.NewTime(now.Add(time.Hour))
	past := metav1.NewTime(now.Add(-time.Hour))
	waitElapsed := metav1.NewTime(now.Add(-25 * time.Hour))
	syncer := &clusterRoleAssignmentsSyncer{clock: clocktesting.NewFakePassiveClock(now)}
	key := testDesiredRoleAssignmentKeys(t)[0]
	azureResource := testRoleAssignmentAzureResource(t)

	testCases := []struct {
		name              string
		cluster           *coreapi.HCPOpenShiftCluster
		mrgConfirmed      bool
		roleAssignments   map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus
		recheckTime       *metav1.Time
		expectedNeedsWork bool
	}{
		{
			name:              "empty map does not need work",
			mrgConfirmed:      true,
			expectedNeedsWork: false,
		},
		{
			name:         "desired key without AzureResource needs work when MRG is confirmed",
			mrgConfirmed: true,
			roleAssignments: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
				key: {},
			},
			expectedNeedsWork: true,
		},
		{
			name:         "desired key does not need work when MRG is not confirmed",
			mrgConfirmed: false,
			roleAssignments: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
				key: {},
			},
			expectedNeedsWork: false,
		},
		{
			name:         "draining key needs work when the 24h wait has elapsed",
			mrgConfirmed: true,
			roleAssignments: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
				key: {
					DeconfigureTimestamp: &waitElapsed,
					AzureResource:        azureResource,
				},
			},
			recheckTime:       &future,
			expectedNeedsWork: true,
		},
		{
			name:         "draining key does not need work inside the 24h wait with future recheck",
			mrgConfirmed: true,
			roleAssignments: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
				key: {
					DeconfigureTimestamp: &past,
					AzureResource:        azureResource,
				},
			},
			recheckTime:       &future,
			expectedNeedsWork: false,
		},
		{
			name:         "ensured key with nil controller recheck needs work",
			mrgConfirmed: true,
			roleAssignments: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
				key: {AzureResource: azureResource},
			},
			expectedNeedsWork: true,
		},
		{
			name:         "ensured key with future controller recheck does not need work",
			mrgConfirmed: true,
			roleAssignments: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
				key: {AzureResource: azureResource},
			},
			recheckTime:       &future,
			expectedNeedsWork: false,
		},
		{
			name:         "ensured key with past controller recheck needs work",
			mrgConfirmed: true,
			roleAssignments: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
				key: {AzureResource: azureResource},
			},
			recheckTime:       &past,
			expectedNeedsWork: true,
		},
		{
			name:         "desired key does not need work when the cluster is being deleted",
			cluster:      newTestCluster(true),
			mrgConfirmed: true,
			roleAssignments: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
				key: {},
			},
			expectedNeedsWork: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cluster := tc.cluster
			if cluster == nil {
				cluster = newTestCluster(false)
			}
			spc := newTestServiceProviderCluster(t, tc.mrgConfirmed, true, true, tc.roleAssignments)
			if tc.recheckTime != nil {
				spc.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{
					ClusterRoleAssignmentsControllerName: tc.recheckTime,
				}
			}
			assert.Equal(t, tc.expectedNeedsWork, syncer.needsWork(cluster, spc))
		})
	}
}

func TestClusterRoleAssignmentV2SyncOncePersistsPendingBeforeAzure(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	cluster := newTestCluster(false)
	keys := testDesiredRoleAssignmentKeys(t)
	desiredID := testDesiredRoleAssignmentResourceID(t, keys[0])

	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
		keys[0]: {},
	})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster, newTestSubscription(ptr.To(testTenantID))})
	require.NoError(t, err)

	mockRA := &azuremockclient.RoleAssignmentsClientFunc{
		GetByIDFunc: func(ctx context.Context, roleAssignmentID string, options *armauthorization.RoleAssignmentsClientGetByIDOptions) (armauthorization.RoleAssignmentsClientGetByIDResponse, error) {
			return armauthorization.RoleAssignmentsClientGetByIDResponse{}, roleAssignmentNotFoundError()
		},
		CreateFunc: func(ctx context.Context, scope, roleAssignmentName string, parameters armauthorization.RoleAssignmentCreateParameters, options *armauthorization.RoleAssignmentsClientCreateOptions) (armauthorization.RoleAssignmentsClientCreateResponse, error) {
			return armauthorization.RoleAssignmentsClientCreateResponse{}, &azcore.ResponseError{StatusCode: http.StatusInternalServerError, ErrorCode: "InternalServerError"}
		},
	}
	syncer := newTestRoleAssignmentV2Syncer(t, mockResourcesDB, mockRA)

	err = syncer.SyncOnce(ctx, testHCPClusterKey)
	require.Error(t, err)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	status := updated.Status.RoleAssignments[keys[0]]
	require.NotNil(t, status)
	assert.False(t, status.Configured())
	require.NotNil(t, status.PendingAzureResource)
	assert.True(t, controllerutil.ResourceIDsEqual(desiredID, status.PendingAzureResource))
	assert.Nil(t, status.AzureResource)
	require.NotEmpty(t, mockRA.CreateCalls)
}

func TestClusterRoleAssignmentSyncOnceConfiguresWhenAzureAlreadyMatches(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	cluster := newTestCluster(false)
	keys := testDesiredRoleAssignmentKeys(t)
	desiredID := testDesiredRoleAssignmentResourceID(t, keys[0])

	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
		keys[0]: {},
	})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster, newTestSubscription(ptr.To(testTenantID))})
	require.NoError(t, err)

	mockRA := &azuremockclient.RoleAssignmentsClientFunc{
		GetByIDFunc: matchingRoleAssignmentGet(keys[0], desiredID),
		CreateFunc: func(ctx context.Context, scope, roleAssignmentName string, parameters armauthorization.RoleAssignmentCreateParameters, options *armauthorization.RoleAssignmentsClientCreateOptions) (armauthorization.RoleAssignmentsClientCreateResponse, error) {
			t.Fatal("Create should not run when Azure already matches")
			return armauthorization.RoleAssignmentsClientCreateResponse{}, nil
		},
	}
	syncer := newTestRoleAssignmentV2Syncer(t, mockResourcesDB, mockRA)

	err = syncer.SyncOnce(ctx, testHCPClusterKey)
	require.NoError(t, err)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	status := updated.Status.RoleAssignments[keys[0]]
	require.NotNil(t, status)
	assert.True(t, status.Configured())
	require.NotNil(t, status.AzureResource)
	assert.True(t, controllerutil.ResourceIDsEqual(desiredID, status.AzureResource))
	assert.Nil(t, status.PendingAzureResource)
	assert.NotNil(t, updated.Spec.EarliestRecheckTimesByController[ClusterRoleAssignmentsControllerName])
	assert.Empty(t, mockRA.CreateCalls)
}

func TestClusterRoleAssignmentV2SyncOnceCreatesWhenMissing(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	cluster := newTestCluster(false)
	keys := testDesiredRoleAssignmentKeys(t)
	desiredID := testDesiredRoleAssignmentResourceID(t, keys[0])

	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
		keys[0]: {},
	})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster, newTestSubscription(ptr.To(testTenantID))})
	require.NoError(t, err)

	mockRA := &azuremockclient.RoleAssignmentsClientFunc{
		GetByIDFunc: func(ctx context.Context, roleAssignmentID string, options *armauthorization.RoleAssignmentsClientGetByIDOptions) (armauthorization.RoleAssignmentsClientGetByIDResponse, error) {
			return armauthorization.RoleAssignmentsClientGetByIDResponse{}, roleAssignmentNotFoundError()
		},
		CreateFunc: func(ctx context.Context, scope, roleAssignmentName string, parameters armauthorization.RoleAssignmentCreateParameters, options *armauthorization.RoleAssignmentsClientCreateOptions) (armauthorization.RoleAssignmentsClientCreateResponse, error) {
			return armauthorization.RoleAssignmentsClientCreateResponse{}, nil
		},
	}
	syncer := newTestRoleAssignmentV2Syncer(t, mockResourcesDB, mockRA)

	err = syncer.SyncOnce(ctx, testHCPClusterKey)
	require.NoError(t, err)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	status := updated.Status.RoleAssignments[keys[0]]
	require.NotNil(t, status)
	assert.True(t, status.Configured())
	require.NotNil(t, status.AzureResource)
	assert.True(t, controllerutil.ResourceIDsEqual(desiredID, status.AzureResource))
	assert.Nil(t, status.PendingAzureResource)
	require.Len(t, mockRA.CreateCalls, 1)
	assert.Equal(t, desiredID.Name, mockRA.CreateCalls[0].RoleAssignmentName)
	require.NotNil(t, mockRA.CreateCalls[0].Parameters.Properties)
	assert.Equal(t, keys[0].PrincipalID, ptr.Deref(mockRA.CreateCalls[0].Parameters.Properties.PrincipalID, ""))
	assert.Equal(t, keys[0].RoleDefinitionResourceID, ptr.Deref(mockRA.CreateCalls[0].Parameters.Properties.RoleDefinitionID, ""))
}

func TestClusterRoleAssignmentV2SyncOnceWaitsBeforeDeconfigure(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	cluster := newTestCluster(false)
	keys := testDesiredRoleAssignmentKeys(t)
	desiredID := testDesiredRoleAssignmentResourceID(t, keys[0])
	requestedAt := metav1.NewTime(now.Add(-time.Hour))

	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
		keys[0]: {
			DeconfigureTimestamp: &requestedAt,
			AzureResource:        desiredID,
		},
	})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster, newTestSubscription(ptr.To(testTenantID))})
	require.NoError(t, err)

	mockRA := &azuremockclient.RoleAssignmentsClientFunc{
		DeleteByIDFunc: func(ctx context.Context, roleAssignmentID string, options *armauthorization.RoleAssignmentsClientDeleteByIDOptions) (armauthorization.RoleAssignmentsClientDeleteByIDResponse, error) {
			t.Fatal("Delete should not run before the 24h wait elapses")
			return armauthorization.RoleAssignmentsClientDeleteByIDResponse{}, nil
		},
	}
	syncer := newTestRoleAssignmentV2Syncer(t, mockResourcesDB, mockRA)

	err = syncer.SyncOnce(ctx, testHCPClusterKey)
	require.NoError(t, err)
	assert.Empty(t, mockRA.DeleteByIDCalls)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	status := updated.Status.RoleAssignments[keys[0]]
	require.NotNil(t, status)
	assert.NotNil(t, status.DeconfigureTimestamp)
	require.NotNil(t, status.AzureResource)
}

func TestClusterRoleAssignmentSyncOnceDeconfiguresAfterWait(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	cluster := newTestCluster(false)
	keys := testDesiredRoleAssignmentKeys(t)
	desiredID := testDesiredRoleAssignmentResourceID(t, keys[0])
	requestedAt := metav1.NewTime(now.Add(-25 * time.Hour))

	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
		keys[0]: {
			DeconfigureTimestamp: &requestedAt,
			AzureResource:        desiredID,
		},
	})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster, newTestSubscription(ptr.To(testTenantID))})
	require.NoError(t, err)

	mockRA := &azuremockclient.RoleAssignmentsClientFunc{
		DeleteByIDFunc: func(ctx context.Context, roleAssignmentID string, options *armauthorization.RoleAssignmentsClientDeleteByIDOptions) (armauthorization.RoleAssignmentsClientDeleteByIDResponse, error) {
			return armauthorization.RoleAssignmentsClientDeleteByIDResponse{}, roleAssignmentNotFoundError()
		},
	}
	syncer := newTestRoleAssignmentV2Syncer(t, mockResourcesDB, mockRA)

	err = syncer.SyncOnce(ctx, testHCPClusterKey)
	require.NoError(t, err)
	require.Equal(t, []string{desiredID.String()}, mockRA.DeleteByIDCalls)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	assert.Empty(t, updated.Status.RoleAssignments)
	assert.Nil(t, updated.Spec.EarliestRecheckTimesByController[ClusterRoleAssignmentsControllerName])
}

func TestClusterRoleAssignmentV2SyncOnceSkipsClusterDeletion(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	cluster := newTestCluster(true)
	keys := testDesiredRoleAssignmentKeys(t)
	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
		keys[0]: {},
	})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster, newTestSubscription(ptr.To(testTenantID))})
	require.NoError(t, err)

	mockRA := &azuremockclient.RoleAssignmentsClientFunc{
		GetByIDFunc: func(ctx context.Context, roleAssignmentID string, options *armauthorization.RoleAssignmentsClientGetByIDOptions) (armauthorization.RoleAssignmentsClientGetByIDResponse, error) {
			t.Fatal("Azure Get should not run during cluster deletion")
			return armauthorization.RoleAssignmentsClientGetByIDResponse{}, nil
		},
	}
	syncer := newTestRoleAssignmentV2Syncer(t, mockResourcesDB, mockRA)

	err = syncer.SyncOnce(ctx, testHCPClusterKey)
	require.NoError(t, err)
	assert.Empty(t, mockRA.GetByIDCalls)
	assert.Empty(t, mockRA.CreateCalls)
}

func TestClusterRoleAssignmentV2SyncOnceSkipsUntilManagedResourceGroupConfirmed(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	cluster := newTestCluster(false)
	keys := testDesiredRoleAssignmentKeys(t)
	serviceProviderCluster := newTestServiceProviderCluster(t, false, true, true, map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
		keys[0]: {},
	})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster, newTestSubscription(ptr.To(testTenantID))})
	require.NoError(t, err)

	mockRA := &azuremockclient.RoleAssignmentsClientFunc{
		GetByIDFunc: func(ctx context.Context, roleAssignmentID string, options *armauthorization.RoleAssignmentsClientGetByIDOptions) (armauthorization.RoleAssignmentsClientGetByIDResponse, error) {
			t.Fatal("Azure Get should not run before the managed resource group is confirmed")
			return armauthorization.RoleAssignmentsClientGetByIDResponse{}, nil
		},
	}
	syncer := newTestRoleAssignmentV2Syncer(t, mockResourcesDB, mockRA)

	err = syncer.SyncOnce(ctx, testHCPClusterKey)
	require.NoError(t, err)
	assert.Empty(t, mockRA.GetByIDCalls)
	assert.Empty(t, mockRA.CreateCalls)
}

func newTestRoleAssignmentV2Syncer(
	t *testing.T,
	resourcesDB *corecosmosstoragetesting.MockResourcesDBClient,
	roleAssignmentsClient *azuremockclient.RoleAssignmentsClientFunc,
) *clusterRoleAssignmentsSyncer {
	t.Helper()
	return &clusterRoleAssignmentsSyncer{
		clock:                        clocktesting.NewFakePassiveClock(time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)),
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: resourcesDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: resourcesDB},
		subscriptionLister:           &corelistertesting.DBSubscriptionLister{ResourcesDBClient: resourcesDB},
		resourcesDBClient:            resourcesDB,
		azureFPAClientBuilder: &azuremockclient.FirstPartyApplicationClientBuilderFunc{
			RoleAssignmentsClientVal: roleAssignmentsClient,
		},
	}
}

func testDesiredRoleAssignmentResourceID(t *testing.T, key coreapi.RoleAssignmentKey) *azcorearm.ResourceID {
	t.Helper()
	syncer := &clusterRoleAssignmentsSyncer{}
	id, err := syncer.desiredRoleAssignmentResourceID(newTestServiceProviderCluster(t, true, true, true, nil), key)
	require.NoError(t, err)
	require.NotNil(t, id)
	return id
}

func matchingRoleAssignmentGet(key coreapi.RoleAssignmentKey, desiredID *azcorearm.ResourceID) func(ctx context.Context, roleAssignmentID string, options *armauthorization.RoleAssignmentsClientGetByIDOptions) (armauthorization.RoleAssignmentsClientGetByIDResponse, error) {
	return func(ctx context.Context, roleAssignmentID string, options *armauthorization.RoleAssignmentsClientGetByIDOptions) (armauthorization.RoleAssignmentsClientGetByIDResponse, error) {
		return armauthorization.RoleAssignmentsClientGetByIDResponse{
			RoleAssignment: armauthorization.RoleAssignment{
				ID: ptr.To(desiredID.String()),
				Properties: &armauthorization.RoleAssignmentProperties{
					PrincipalID:      ptr.To(key.PrincipalID),
					RoleDefinitionID: ptr.To(key.RoleDefinitionResourceID),
					PrincipalType:    ptr.To(armauthorization.PrincipalTypeServicePrincipal),
				},
			},
		}, nil
	}
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
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = metadataapi.Must(azcorearm.ParseResourceID(testServiceManagedIdentityID))
	if deleting {
		cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	}
	return cluster
}

// newTestServiceProviderCluster builds a ServiceProviderCluster addressable by the
// mock ResourcesDBClient. When mrgConfirmed is true the managed resource group is
// reflected as confirmed (opening the observation gate); cpResolved / dpResolved
// control whether the control-plane / data-plane operator principal IDs are resolved
// on the status. roleAssignments is the initial Status.RoleAssignments map.
func newTestServiceProviderCluster(t *testing.T, mrgConfirmed, cpResolved, dpResolved bool, roleAssignments map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus) *coreapi.ServiceProviderCluster {
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
	serviceProviderCluster.Status.RoleAssignments = roleAssignments
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

// testManagedResourceGroupID returns the confirmed managed resource group reference
// resource ID used to open the observation gate.
func testManagedResourceGroupID(t *testing.T) *azcorearm.ResourceID {
	t.Helper()
	return metadataapi.Must(coreapi.ToResourceGroupResourceID(testSubscriptionID, testManagedRGName))
}
