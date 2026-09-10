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
	"net/http"
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
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestClusterRoleAssignmentV2NeedsWork(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	future := metav1.NewTime(now.Add(time.Hour))
	past := metav1.NewTime(now.Add(-time.Hour))
	waitElapsed := metav1.NewTime(now.Add(-25 * time.Hour))
	syncer := &clusterRoleAssignmentV2Syncer{clock: clocktesting.NewFakePassiveClock(now)}
	key := testDesiredRoleAssignmentKeys(t)[0]

	testCases := []struct {
		name              string
		cluster           *coreapi.HCPOpenShiftCluster
		mrgConfirmed      bool
		roleAssignmentsV2 map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus
		expectedNeedsWork bool
	}{
		{
			name:              "empty map does not need work",
			mrgConfirmed:      true,
			expectedNeedsWork: false,
		},
		{
			name:         "PendingConfigure needs work when MRG is confirmed",
			mrgConfirmed: true,
			roleAssignmentsV2: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
				key: {Phase: coreapi.RoleAssignmentPhasePendingConfigure},
			},
			expectedNeedsWork: true,
		},
		{
			name:         "PendingConfigure does not need work when MRG is not confirmed",
			mrgConfirmed: false,
			roleAssignmentsV2: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
				key: {Phase: coreapi.RoleAssignmentPhasePendingConfigure},
			},
			expectedNeedsWork: false,
		},
		{
			name:         "PendingDeconfigure needs work when the 24h wait has elapsed",
			mrgConfirmed: true,
			roleAssignmentsV2: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
				key: {
					Phase:                coreapi.RoleAssignmentPhasePendingDeconfigure,
					DeconfigureTimestamp: &waitElapsed,
				},
			},
			expectedNeedsWork: true,
		},
		{
			name:         "PendingDeconfigure does not need work inside the 24h wait",
			mrgConfirmed: true,
			roleAssignmentsV2: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
				key: {
					Phase:                coreapi.RoleAssignmentPhasePendingDeconfigure,
					DeconfigureTimestamp: &past,
				},
			},
			expectedNeedsWork: false,
		},
		{
			name:         "Configured with nil recheck needs work",
			mrgConfirmed: true,
			roleAssignmentsV2: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
				key: {Phase: coreapi.RoleAssignmentPhaseConfigured},
			},
			expectedNeedsWork: true,
		},
		{
			name:         "Configured with future recheck does not need work",
			mrgConfirmed: true,
			roleAssignmentsV2: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
				key: {
					Phase:               coreapi.RoleAssignmentPhaseConfigured,
					EarliestRecheckTime: &future,
				},
			},
			expectedNeedsWork: false,
		},
		{
			name:         "Configured with past recheck needs work",
			mrgConfirmed: true,
			roleAssignmentsV2: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
				key: {
					Phase:               coreapi.RoleAssignmentPhaseConfigured,
					EarliestRecheckTime: &past,
				},
			},
			expectedNeedsWork: true,
		},
		{
			name:         "Deconfigured does not need work",
			mrgConfirmed: true,
			roleAssignmentsV2: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
				key: {Phase: coreapi.RoleAssignmentPhaseDeconfigured},
			},
			expectedNeedsWork: false,
		},
		{
			name:         "PendingConfigure does not need work when the cluster is being deleted",
			cluster:      newTestCluster(true),
			mrgConfirmed: true,
			roleAssignmentsV2: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
				key: {Phase: coreapi.RoleAssignmentPhasePendingConfigure},
			},
			expectedNeedsWork: false,
		},
		{
			name:         "nil status needs work",
			mrgConfirmed: true,
			roleAssignmentsV2: map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
				key: nil,
			},
			expectedNeedsWork: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cluster := tc.cluster
			if cluster == nil {
				cluster = newTestCluster(false)
			}
			spc := newTestServiceProviderCluster(t, tc.mrgConfirmed, true, true, coreapi.AzureMultiReference{})
			spc.Status.RoleAssignmentsV2 = tc.roleAssignmentsV2
			assert.Equal(t, tc.expectedNeedsWork, syncer.needsWork(cluster, spc))
		})
	}
}

func TestClusterRoleAssignmentV2SyncOncePersistsPendingBeforeAzure(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	cluster := newTestCluster(false)
	keys := testDesiredRoleAssignmentKeys(t)
	desiredID := testDesiredRoleAssignmentResourceID(t, cluster, keys[0])

	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, coreapi.AzureMultiReference{})
	serviceProviderCluster.Status.RoleAssignmentsV2 = map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
		keys[0]: {Phase: coreapi.RoleAssignmentPhasePendingConfigure},
	}

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
	status := updated.Status.RoleAssignmentsV2[keys[0]]
	require.NotNil(t, status)
	assert.Equal(t, coreapi.RoleAssignmentPhasePendingConfigure, status.Phase)
	require.NotNil(t, status.PendingAzureResource)
	assert.True(t, roleAssignmentResourceIDsEqual(desiredID, status.PendingAzureResource))
	assert.Nil(t, status.AzureResource)
	require.NotEmpty(t, mockRA.CreateCalls)
}

func TestClusterRoleAssignmentV2SyncOnceConfiguresWhenAzureAlreadyMatches(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	cluster := newTestCluster(false)
	keys := testDesiredRoleAssignmentKeys(t)
	desiredID := testDesiredRoleAssignmentResourceID(t, cluster, keys[0])

	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, coreapi.AzureMultiReference{})
	serviceProviderCluster.Status.RoleAssignmentsV2 = map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
		keys[0]: {Phase: coreapi.RoleAssignmentPhasePendingConfigure},
	}

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
	status := updated.Status.RoleAssignmentsV2[keys[0]]
	require.NotNil(t, status)
	assert.Equal(t, coreapi.RoleAssignmentPhaseConfigured, status.Phase)
	require.NotNil(t, status.AzureResource)
	assert.True(t, roleAssignmentResourceIDsEqual(desiredID, status.AzureResource))
	assert.Nil(t, status.PendingAzureResource)
	assert.NotNil(t, status.EarliestRecheckTime)
	assert.Empty(t, mockRA.CreateCalls)
}

func TestClusterRoleAssignmentV2SyncOnceCreatesWhenMissing(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	cluster := newTestCluster(false)
	keys := testDesiredRoleAssignmentKeys(t)
	desiredID := testDesiredRoleAssignmentResourceID(t, cluster, keys[0])

	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, coreapi.AzureMultiReference{})
	serviceProviderCluster.Status.RoleAssignmentsV2 = map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
		keys[0]: {Phase: coreapi.RoleAssignmentPhasePendingConfigure},
	}

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
	status := updated.Status.RoleAssignmentsV2[keys[0]]
	require.NotNil(t, status)
	assert.Equal(t, coreapi.RoleAssignmentPhaseConfigured, status.Phase)
	require.NotNil(t, status.AzureResource)
	assert.True(t, roleAssignmentResourceIDsEqual(desiredID, status.AzureResource))
	assert.Nil(t, status.PendingAzureResource)
	require.Len(t, mockRA.CreateCalls, 1)
	assert.Equal(t, desiredID.Name, mockRA.CreateCalls[0].RoleAssignmentName)
	require.NotNil(t, mockRA.CreateCalls[0].Parameters.Properties)
	assert.Equal(t, keys[0].PrincipalID, ptr.Deref(mockRA.CreateCalls[0].Parameters.Properties.PrincipalID, ""))
	assert.Equal(t, keys[0].RoleDefinitionResourceID, ptr.Deref(mockRA.CreateCalls[0].Parameters.Properties.RoleDefinitionID, ""))
}

func TestClusterRoleAssignmentV2SyncOnceRecreatesOnDrift(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	cluster := newTestCluster(false)
	keys := testDesiredRoleAssignmentKeys(t)
	desiredID := testDesiredRoleAssignmentResourceID(t, cluster, keys[0])

	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, coreapi.AzureMultiReference{})
	serviceProviderCluster.Status.RoleAssignmentsV2 = map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
		keys[0]: {Phase: coreapi.RoleAssignmentPhaseConfigured},
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster, newTestSubscription(ptr.To(testTenantID))})
	require.NoError(t, err)

	mockRA := &azuremockclient.RoleAssignmentsClientFunc{
		GetByIDFunc: func(ctx context.Context, roleAssignmentID string, options *armauthorization.RoleAssignmentsClientGetByIDOptions) (armauthorization.RoleAssignmentsClientGetByIDResponse, error) {
			return armauthorization.RoleAssignmentsClientGetByIDResponse{
				RoleAssignment: armauthorization.RoleAssignment{
					Properties: &armauthorization.RoleAssignmentProperties{
						PrincipalID:      ptr.To("drifted-principal"),
						RoleDefinitionID: ptr.To(keys[0].RoleDefinitionResourceID),
					},
				},
			}, nil
		},
		DeleteByIDFunc: func(ctx context.Context, roleAssignmentID string, options *armauthorization.RoleAssignmentsClientDeleteByIDOptions) (armauthorization.RoleAssignmentsClientDeleteByIDResponse, error) {
			return armauthorization.RoleAssignmentsClientDeleteByIDResponse{}, nil
		},
		CreateFunc: func(ctx context.Context, scope, roleAssignmentName string, parameters armauthorization.RoleAssignmentCreateParameters, options *armauthorization.RoleAssignmentsClientCreateOptions) (armauthorization.RoleAssignmentsClientCreateResponse, error) {
			return armauthorization.RoleAssignmentsClientCreateResponse{}, nil
		},
	}
	syncer := newTestRoleAssignmentV2Syncer(t, mockResourcesDB, mockRA)

	err = syncer.SyncOnce(ctx, testHCPClusterKey)
	require.NoError(t, err)
	require.Equal(t, []string{desiredID.String()}, mockRA.DeleteByIDCalls)
	require.Len(t, mockRA.CreateCalls, 1)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	status := updated.Status.RoleAssignmentsV2[keys[0]]
	require.NotNil(t, status)
	assert.Equal(t, coreapi.RoleAssignmentPhaseConfigured, status.Phase)
}

func TestClusterRoleAssignmentV2SyncOnceWaitsBeforeDeconfigure(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	cluster := newTestCluster(false)
	keys := testDesiredRoleAssignmentKeys(t)
	desiredID := testDesiredRoleAssignmentResourceID(t, cluster, keys[0])
	requestedAt := metav1.NewTime(now.Add(-time.Hour))

	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, coreapi.AzureMultiReference{})
	serviceProviderCluster.Status.RoleAssignmentsV2 = map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
		keys[0]: {
			Phase:                coreapi.RoleAssignmentPhasePendingDeconfigure,
			DeconfigureTimestamp: &requestedAt,
			AzureResource:        desiredID,
		},
	}

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
	status := updated.Status.RoleAssignmentsV2[keys[0]]
	require.NotNil(t, status)
	assert.Equal(t, coreapi.RoleAssignmentPhasePendingDeconfigure, status.Phase)
	require.NotNil(t, status.AzureResource)
}

func TestClusterRoleAssignmentV2SyncOnceDeconfiguresAfterWait(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	cluster := newTestCluster(false)
	keys := testDesiredRoleAssignmentKeys(t)
	desiredID := testDesiredRoleAssignmentResourceID(t, cluster, keys[0])
	requestedAt := metav1.NewTime(now.Add(-25 * time.Hour))

	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, coreapi.AzureMultiReference{})
	serviceProviderCluster.Status.RoleAssignmentsV2 = map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
		keys[0]: {
			Phase:                coreapi.RoleAssignmentPhasePendingDeconfigure,
			DeconfigureTimestamp: &requestedAt,
			AzureResource:        desiredID,
		},
	}

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
	status := updated.Status.RoleAssignmentsV2[keys[0]]
	require.NotNil(t, status)
	assert.Equal(t, coreapi.RoleAssignmentPhaseDeconfigured, status.Phase)
	assert.Nil(t, status.AzureResource)
	assert.Nil(t, status.PendingAzureResource)
}

func TestClusterRoleAssignmentV2SyncOnceSkipsClusterDeletion(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	cluster := newTestCluster(true)
	keys := testDesiredRoleAssignmentKeys(t)
	serviceProviderCluster := newTestServiceProviderCluster(t, true, true, true, coreapi.AzureMultiReference{})
	serviceProviderCluster.Status.RoleAssignmentsV2 = map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
		keys[0]: {Phase: coreapi.RoleAssignmentPhasePendingConfigure},
	}

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
	serviceProviderCluster := newTestServiceProviderCluster(t, false, true, true, coreapi.AzureMultiReference{})
	serviceProviderCluster.Status.RoleAssignmentsV2 = map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
		keys[0]: {Phase: coreapi.RoleAssignmentPhasePendingConfigure},
	}

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
) *clusterRoleAssignmentV2Syncer {
	t.Helper()
	return &clusterRoleAssignmentV2Syncer{
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

func testDesiredRoleAssignmentResourceID(t *testing.T, cluster *coreapi.HCPOpenShiftCluster, key coreapi.RoleAssignmentKey) *azcorearm.ResourceID {
	t.Helper()
	syncer := &clusterRoleAssignmentV2Syncer{}
	id, err := syncer.desiredResourceID(cluster, key)
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
