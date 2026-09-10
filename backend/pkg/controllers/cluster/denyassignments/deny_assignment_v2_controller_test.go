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

package denyassignments

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/azuremockclient"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

func TestClusterDenyAssignmentV2NeedsWork(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	future := metav1.NewTime(now.Add(time.Hour))
	past := metav1.NewTime(now.Add(-time.Hour))
	syncer := &clusterDenyAssignmentV2Syncer{clock: clocktesting.NewFakePassiveClock(now)}

	testCases := []struct {
		name              string
		cluster           *coreapi.HCPOpenShiftCluster
		denyAssignmentsV2 map[string]*coreapi.DenyAssignmentStatus
		expectedNeedsWork bool
	}{
		{
			name:              "empty map does not need work",
			expectedNeedsWork: false,
		},
		{
			name: "PendingConfigure needs work",
			denyAssignmentsV2: map[string]*coreapi.DenyAssignmentStatus{
				denyAssignmentSuffixResources: {Phase: coreapi.DenyAssignmentPhasePendingConfigure},
			},
			expectedNeedsWork: true,
		},
		{
			name: "PendingDeconfigure needs work",
			denyAssignmentsV2: map[string]*coreapi.DenyAssignmentStatus{
				denyAssignmentSuffixResources: {Phase: coreapi.DenyAssignmentPhasePendingDeconfigure},
			},
			expectedNeedsWork: true,
		},
		{
			name: "Configured with nil recheck needs work",
			denyAssignmentsV2: map[string]*coreapi.DenyAssignmentStatus{
				denyAssignmentSuffixResources: {Phase: coreapi.DenyAssignmentPhaseConfigured},
			},
			expectedNeedsWork: true,
		},
		{
			name: "Configured with future recheck and PendingConfigure identity needs work",
			denyAssignmentsV2: map[string]*coreapi.DenyAssignmentStatus{
				denyAssignmentSuffixResources: {
					Phase:               coreapi.DenyAssignmentPhaseConfigured,
					EarliestRecheckTime: &future,
					ExcludedIdentities: map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedIdentityStatus{
						{ResourceID: "id-a", PrincipalID: "principal-a"}: {
							Phase: coreapi.DenyAssignmentExcludedIdentityPhasePendingConfigure,
						},
					},
				},
			},
			expectedNeedsWork: true,
		},
		{
			name: "Configured with future recheck and PendingDeconfigure inside 24h does not need work",
			denyAssignmentsV2: map[string]*coreapi.DenyAssignmentStatus{
				denyAssignmentSuffixResources: {
					Phase:               coreapi.DenyAssignmentPhaseConfigured,
					EarliestRecheckTime: &future,
					ExcludedIdentities: map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedIdentityStatus{
						{ResourceID: "id-a", PrincipalID: "principal-a"}: {
							Phase:                coreapi.DenyAssignmentExcludedIdentityPhasePendingDeconfigure,
							DeconfigureTimestamp: &past,
						},
					},
				},
			},
			expectedNeedsWork: false,
		},
		{
			name: "Configured with past recheck needs work",
			denyAssignmentsV2: map[string]*coreapi.DenyAssignmentStatus{
				denyAssignmentSuffixResources: {
					Phase:               coreapi.DenyAssignmentPhaseConfigured,
					EarliestRecheckTime: &past,
				},
			},
			expectedNeedsWork: true,
		},
		{
			name: "Deconfigured does not need work",
			denyAssignmentsV2: map[string]*coreapi.DenyAssignmentStatus{
				denyAssignmentSuffixResources: {Phase: coreapi.DenyAssignmentPhaseDeconfigured},
			},
			expectedNeedsWork: false,
		},
		{
			name: "PendingConfigure does not need work when the cluster is being deleted",
			cluster: newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &future
			}),
			denyAssignmentsV2: map[string]*coreapi.DenyAssignmentStatus{
				denyAssignmentSuffixResources: {Phase: coreapi.DenyAssignmentPhasePendingConfigure},
			},
			expectedNeedsWork: false,
		},
		{
			name: "nil status needs work",
			denyAssignmentsV2: map[string]*coreapi.DenyAssignmentStatus{
				denyAssignmentSuffixResources: nil,
			},
			expectedNeedsWork: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cluster := tc.cluster
			if cluster == nil {
				cluster = newTestCluster()
			}
			spc := newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Status.DenyAssignmentsV2 = tc.denyAssignmentsV2
			})
			assert.Equal(t, tc.expectedNeedsWork, syncer.needsWork(cluster, spc))
		})
	}
}

func TestClusterDenyAssignmentV2SyncOncePersistsPendingBeforeAzure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cluster := newTestCluster()
	desiredID, err := denyAssignmentResourceID(cluster, denyAssignmentSuffixResources)
	require.NoError(t, err)

	serviceProviderCluster := newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
		spc.Status.DenyAssignmentsV2 = map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: {Phase: coreapi.DenyAssignmentPhasePendingConfigure},
		}
	})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster, testSubscription()})
	require.NoError(t, err)

	mockGeneric := &azuremockclient.GenericResourcesClientFunc{
		CreateErr: &azcore.ResponseError{StatusCode: 500, ErrorCode: "InternalServerError"},
	}
	syncer := newTestDenyAssignmentV2Syncer(t, mockResourcesDB, &azuremockclient.DenyAssignmentsClientFunc{
		GetFunc: func(ctx context.Context, scope string, id string, opts *armauthorization.DenyAssignmentsClientGetOptions) (armauthorization.DenyAssignmentsClientGetResponse, error) {
			return armauthorization.DenyAssignmentsClientGetResponse{}, denyAssignmentNotFoundError()
		},
	}, mockGeneric)

	err = syncer.SyncOnce(ctx, testKey())
	require.Error(t, err)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	status := updated.Status.DenyAssignmentsV2[denyAssignmentSuffixResources]
	require.NotNil(t, status)
	assert.Equal(t, coreapi.DenyAssignmentPhasePendingConfigure, status.Phase)
	require.NotNil(t, status.PendingAzureResource)
	assert.True(t, resourceIDsEqual(desiredID, status.PendingAzureResource))
	assert.Nil(t, status.AzureResource)
	require.NotEmpty(t, mockGeneric.CreateCalls)
}

func TestClusterDenyAssignmentV2SyncOnceConfiguresWhenAzureAlreadyMatches(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cluster := newTestCluster()
	desiredID, err := denyAssignmentResourceID(cluster, denyAssignmentSuffixResources)
	require.NoError(t, err)

	serviceProviderCluster := newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
		spc.Status.DenyAssignmentsV2 = map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: {
				Phase:              coreapi.DenyAssignmentPhasePendingConfigure,
				ExcludedIdentities: seedTestExcludedIdentities(cluster, spc, denyAssignmentSuffixResources, coreapi.DenyAssignmentExcludedIdentityPhasePendingConfigure),
			},
		}
	})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster, testSubscription()})
	require.NoError(t, err)

	mockGeneric := &azuremockclient.GenericResourcesClientFunc{
		CreateErr: &azcore.ResponseError{StatusCode: 500, ErrorCode: "should not create"},
	}
	matchingGet := matchingGetResponseForAllTypes(cluster, serviceProviderCluster)
	syncer := newTestDenyAssignmentV2Syncer(t, mockResourcesDB, &azuremockclient.DenyAssignmentsClientFunc{
		GetFunc: matchingGet,
	}, mockGeneric)

	err = syncer.SyncOnce(ctx, testKey())
	require.NoError(t, err)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	status := updated.Status.DenyAssignmentsV2[denyAssignmentSuffixResources]
	require.NotNil(t, status)
	assert.Equal(t, coreapi.DenyAssignmentPhaseConfigured, status.Phase)
	require.NotNil(t, status.AzureResource)
	assert.True(t, resourceIDsEqual(desiredID, status.AzureResource))
	assert.Nil(t, status.PendingAzureResource)
	assert.NotNil(t, status.EarliestRecheckTime)
	assert.Empty(t, mockGeneric.CreateCalls)
	for _, identityStatus := range status.ExcludedIdentities {
		require.NotNil(t, identityStatus)
		assert.Equal(t, coreapi.DenyAssignmentExcludedIdentityPhaseConfigured, identityStatus.Phase)
	}
}

func TestClusterDenyAssignmentV2SyncOnceDeconfiguresTrackedResource(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cluster := newTestCluster()
	resourceID := testDenyAssignmentResourceID("stale-uuid")

	serviceProviderCluster := newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
		spc.Status.DenyAssignmentsV2 = map[string]*coreapi.DenyAssignmentStatus{
			"stale-type-not-in-definitions": {
				Phase:         coreapi.DenyAssignmentPhasePendingDeconfigure,
				AzureResource: resourceID,
			},
		}
	})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster, testSubscription()})
	require.NoError(t, err)

	mockGeneric := &azuremockclient.GenericResourcesClientFunc{
		DeleteErr: resourceNotFoundError(),
	}
	syncer := newTestDenyAssignmentV2Syncer(t, mockResourcesDB, &azuremockclient.DenyAssignmentsClientFunc{}, mockGeneric)

	err = syncer.SyncOnce(ctx, testKey())
	require.NoError(t, err)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	status := updated.Status.DenyAssignmentsV2["stale-type-not-in-definitions"]
	require.NotNil(t, status)
	assert.Equal(t, coreapi.DenyAssignmentPhaseDeconfigured, status.Phase)
	assert.Nil(t, status.AzureResource)
	assert.Nil(t, status.PendingAzureResource)
	require.Equal(t, []string{resourceID.String()}, mockGeneric.DeleteCalls)
}

func TestClusterDenyAssignmentV2SyncOnceSkipsClusterDeletion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := metav1.NewTime(time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC))
	cluster := newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
		c.ServiceProviderProperties.DeletionTimestamp = &now
	})
	serviceProviderCluster := newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
		spc.Status.DenyAssignmentsV2 = map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: {Phase: coreapi.DenyAssignmentPhasePendingConfigure},
		}
	})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster, testSubscription()})
	require.NoError(t, err)

	mockGeneric := &azuremockclient.GenericResourcesClientFunc{
		CreateErr: &azcore.ResponseError{StatusCode: 500, ErrorCode: "should not create"},
	}
	syncer := newTestDenyAssignmentV2Syncer(t, mockResourcesDB, &azuremockclient.DenyAssignmentsClientFunc{
		GetFunc: func(ctx context.Context, scope string, id string, opts *armauthorization.DenyAssignmentsClientGetOptions) (armauthorization.DenyAssignmentsClientGetResponse, error) {
			t.Fatal("Azure Get should not run during cluster deletion")
			return armauthorization.DenyAssignmentsClientGetResponse{}, nil
		},
	}, mockGeneric)

	err = syncer.SyncOnce(ctx, testKey())
	require.NoError(t, err)
	assert.Empty(t, mockGeneric.CreateCalls)
}

func testDenyAssignmentResourceID(name string) *azcorearm.ResourceID {
	id, err := coreapi.ToDenyAssignmentResourceID(testSubscriptionID, testManagedRG, name)
	if err != nil {
		panic(err)
	}
	return id
}

func newTestDenyAssignmentV2Syncer(
	t *testing.T,
	resourcesDB *corecosmosstoragetesting.MockResourcesDBClient,
	denyAssignmentsClient *azuremockclient.DenyAssignmentsClientFunc,
	genericResourcesClient *azuremockclient.GenericResourcesClientFunc,
) *clusterDenyAssignmentV2Syncer {
	t.Helper()
	return &clusterDenyAssignmentV2Syncer{
		clock:                        clocktesting.NewFakePassiveClock(time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)),
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: resourcesDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: resourcesDB},
		subscriptionLister:           &corelistertesting.DBSubscriptionLister{ResourcesDBClient: resourcesDB},
		resourcesDBClient:            resourcesDB,
		azureFPAClientBuilder: &azuremockclient.FirstPartyApplicationClientBuilderFunc{
			DenyAssignmentsClientVal:  denyAssignmentsClient,
			GenericResourcesClientVal: genericResourcesClient,
		},
	}
}
