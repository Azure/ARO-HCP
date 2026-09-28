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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/azuremockclient"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
	testTenantID          = "11111111-1111-1111-1111-111111111111"
	testManagedRG         = "testManagedResourceGroup"
)

func TestClusterDenyAssignmentV2NeedsWork(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	future := metav1.NewTime(now.Add(time.Hour))
	past := metav1.NewTime(now.Add(-time.Hour))
	elapsedWait := metav1.NewTime(now.Add(-25 * time.Hour))
	syncer := &clusterDenyAssignmentSyncer{clock: clocktesting.NewFakePassiveClock(now)}
	cluster := newTestCluster()
	azureResource := testDenyAssignmentResourceID("resources-uuid")

	testCases := []struct {
		name              string
		cluster           *coreapi.Cluster
		spc               *coreapi.ServiceProviderCluster
		expectedNeedsWork bool
	}{
		{
			name:              "empty map does not need work",
			spc:               newTestSPC(),
			expectedNeedsWork: false,
		},
		{
			name: "desired type without AzureResource needs work",
			spc: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Status.DenyAssignmentsOverManagedResourceGroup = map[string]*coreapi.DenyAssignmentStatus{
					denyAssignmentSuffixResources: {
						ExcludedIdentities: seedTestExcludedIdentities(cluster, spc, denyAssignmentSuffixResources),
					},
				}
			}),
			expectedNeedsWork: true,
		},
		{
			name: "draining type needs work",
			spc: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Status.DenyAssignmentsOverManagedResourceGroup = map[string]*coreapi.DenyAssignmentStatus{
					denyAssignmentSuffixResources: {
						DeconfigureTimestamp: &past,
						AzureResource:        azureResource,
					},
				}
			}),
			expectedNeedsWork: true,
		},
		{
			name: "ensured type with nil controller recheck needs work",
			spc: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Status.DenyAssignmentsOverManagedResourceGroup = map[string]*coreapi.DenyAssignmentStatus{
					denyAssignmentSuffixResources: seedEnsuredTypeStatus(cluster, spc, denyAssignmentSuffixResources, azureResource),
				}
			}),
			expectedNeedsWork: true,
		},
		{
			name: "ensured type with future recheck and elapsed cooldown needs work",
			spc: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				status := seedEnsuredTypeStatus(cluster, spc, denyAssignmentSuffixResources, azureResource)
				status.ExcludedIdentities["principal-a"] = &coreapi.DenyAssignmentExcludedIdentityStatus{
					DeconfigureTimestamp: &elapsedWait,
					EnsuredIdentity:      &coreapi.DenyAssignmentExcludedEnsuredIdentity{PrincipalID: "principal-a"},
				}
				spc.Status.DenyAssignmentsOverManagedResourceGroup = map[string]*coreapi.DenyAssignmentStatus{
					denyAssignmentSuffixResources: status,
				}
				spc.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{
					ClusterDenyAssignmentControllerName: &future,
				}
			}),
			expectedNeedsWork: true,
		},
		{
			name: "ensured type with future recheck and cooldown inside 24h does not need work",
			spc: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				status := seedEnsuredTypeStatus(cluster, spc, denyAssignmentSuffixResources, azureResource)
				status.ExcludedIdentities["principal-a"] = &coreapi.DenyAssignmentExcludedIdentityStatus{
					DeconfigureTimestamp: &past,
					EnsuredIdentity:      &coreapi.DenyAssignmentExcludedEnsuredIdentity{PrincipalID: "principal-a"},
				}
				spc.Status.DenyAssignmentsOverManagedResourceGroup = map[string]*coreapi.DenyAssignmentStatus{
					denyAssignmentSuffixResources: status,
				}
				spc.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{
					ClusterDenyAssignmentControllerName: &future,
				}
			}),
			expectedNeedsWork: false,
		},
		{
			name: "ensured type with past controller recheck needs work",
			spc: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Status.DenyAssignmentsOverManagedResourceGroup = map[string]*coreapi.DenyAssignmentStatus{
					denyAssignmentSuffixResources: seedEnsuredTypeStatus(cluster, spc, denyAssignmentSuffixResources, azureResource),
				}
				spc.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{
					ClusterDenyAssignmentControllerName: &past,
				}
			}),
			expectedNeedsWork: true,
		},
		{
			name: "unstamped type that left the definition set does not need work",
			spc: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Status.DenyAssignmentsOverManagedResourceGroup = map[string]*coreapi.DenyAssignmentStatus{
					"stale-type-not-in-definitions": {
						AzureResource: azureResource,
					},
				}
			}),
			expectedNeedsWork: false,
		},
		{
			name: "desired type does not need work when the observed managed resource group is missing",
			spc: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Status.AzureResources.ManagedResourceGroup.AzureResource = nil
				spc.Status.DenyAssignmentsOverManagedResourceGroup = map[string]*coreapi.DenyAssignmentStatus{
					denyAssignmentSuffixResources: {},
				}
			}),
			expectedNeedsWork: false,
		},
		{
			name: "desired type does not need work when the cluster is being deleted",
			cluster: newTestCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &future
			}),
			spc: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Status.DenyAssignmentsOverManagedResourceGroup = map[string]*coreapi.DenyAssignmentStatus{
					denyAssignmentSuffixResources: {},
				}
			}),
			expectedNeedsWork: false,
		},
		{
			name: "permissions drift needs work even with future recheck",
			spc: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				status := seedEnsuredTypeStatus(cluster, spc, denyAssignmentSuffixResources, azureResource)
				status.EnsuredPermissions.Actions = append(status.EnsuredPermissions.Actions, "Microsoft.Example/drifted")
				spc.Status.DenyAssignmentsOverManagedResourceGroup = map[string]*coreapi.DenyAssignmentStatus{
					denyAssignmentSuffixResources: status,
				}
				spc.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{
					ClusterDenyAssignmentControllerName: &future,
				}
			}),
			expectedNeedsWork: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := tc.cluster
			if c == nil {
				c = cluster
			}
			assert.Equal(t, tc.expectedNeedsWork, syncer.needsWork(c, tc.spc))
		})
	}
}

func TestClusterDenyAssignmentV2SyncOncePersistsPendingBeforeAzure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cluster := newTestCluster()
	desiredID, err := generateDenyAssignmentResourceID(cluster, denyAssignmentSuffixResources)
	require.NoError(t, err)

	serviceProviderCluster := newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
		spc.Status.DenyAssignmentsOverManagedResourceGroup = map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: {
				ExcludedIdentities: desiredIdentityRows(cluster, spc, denyAssignmentSuffixResources),
			},
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
	status := updated.Status.DenyAssignmentsOverManagedResourceGroup[denyAssignmentSuffixResources]
	require.NotNil(t, status)
	require.NotNil(t, status.PendingAzureResource)
	assert.True(t, controllerutil.ResourceIDsEqual(desiredID, status.PendingAzureResource))
	assert.Nil(t, status.AzureResource)
	require.NotEmpty(t, mockGeneric.CreateCalls)
}

func TestClusterDenyAssignmentV2SyncOnceConfiguresWhenAzureAlreadyMatches(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cluster := newTestCluster()
	desiredID, err := generateDenyAssignmentResourceID(cluster, denyAssignmentSuffixResources)
	require.NoError(t, err)

	serviceProviderCluster := newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
		spc.Status.DenyAssignmentsOverManagedResourceGroup = map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: {
				ExcludedIdentities: desiredIdentityRows(cluster, spc, denyAssignmentSuffixResources),
			},
		}
	})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster, testSubscription()})
	require.NoError(t, err)

	mockGeneric := &azuremockclient.GenericResourcesClientFunc{
		CreateErr: &azcore.ResponseError{StatusCode: 500, ErrorCode: "should not create"},
	}
	denyAssignmentsClient := &azuremockclient.DenyAssignmentsClientFunc{}
	syncer := newTestDenyAssignmentV2Syncer(t, mockResourcesDB, denyAssignmentsClient, mockGeneric)
	denyAssignmentsClient.GetFunc = matchingGetResponseForAllTypes(cluster, serviceProviderCluster, syncer)

	err = syncer.SyncOnce(ctx, testKey())
	require.NoError(t, err)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	status := updated.Status.DenyAssignmentsOverManagedResourceGroup[denyAssignmentSuffixResources]
	require.NotNil(t, status)
	require.NotNil(t, status.AzureResource)
	assert.True(t, controllerutil.ResourceIDsEqual(desiredID, status.AzureResource))
	assert.Nil(t, status.PendingAzureResource)
	require.NotNil(t, updated.Spec.EarliestRecheckTimesByController[ClusterDenyAssignmentControllerName])
	assert.Empty(t, mockGeneric.CreateCalls)
	assert.Equal(t, seedTestEnsuredPermissions(cluster, denyAssignmentSuffixResources), status.EnsuredPermissions)
	require.NotEmpty(t, status.ExcludedIdentities)
	for _, identityStatus := range status.ExcludedIdentities {
		require.NotNil(t, identityStatus)
		require.NotNil(t, identityStatus.EnsuredIdentity)
		assert.NotEmpty(t, identityStatus.EnsuredIdentity.PrincipalID)
		assert.Nil(t, identityStatus.DeconfigureTimestamp)
	}
}

func TestClusterDenyAssignmentV2SyncOnceDeconfiguresTrackedResource(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cluster := newTestCluster()
	resourceID := testDenyAssignmentResourceID("stale-uuid")
	stamped := metav1.NewTime(time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC))

	serviceProviderCluster := newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
		spc.Status.DenyAssignmentsOverManagedResourceGroup = map[string]*coreapi.DenyAssignmentStatus{
			"stale-type-not-in-definitions": {
				DeconfigureTimestamp: &stamped,
				AzureResource:        resourceID,
				ExcludedIdentities: map[string]*coreapi.DenyAssignmentExcludedIdentityStatus{
					"principal-a": {
						EnsuredIdentity: &coreapi.DenyAssignmentExcludedEnsuredIdentity{PrincipalID: "principal-a"},
					},
				},
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
	assert.NotContains(t, updated.Status.DenyAssignmentsOverManagedResourceGroup, "stale-type-not-in-definitions")
	require.Equal(t, []string{resourceID.String()}, mockGeneric.DeleteCalls)
}

func TestClusterDenyAssignmentV2SyncOnceSkipsUnstampedTypeThatLeftDefinitions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cluster := newTestCluster()
	staleResourceID := testDenyAssignmentResourceID("stale-uuid")

	serviceProviderCluster := newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
		spc.Status.DenyAssignmentsOverManagedResourceGroup = map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: {
				ExcludedIdentities: desiredIdentityRows(cluster, spc, denyAssignmentSuffixResources),
			},
			"stale-type-not-in-definitions": {
				AzureResource: staleResourceID,
			},
		}
	})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster, testSubscription()})
	require.NoError(t, err)

	mockGeneric := &azuremockclient.GenericResourcesClientFunc{
		CreateErr: &azcore.ResponseError{StatusCode: 500, ErrorCode: "should not create"},
		DeleteErr: &azcore.ResponseError{StatusCode: 500, ErrorCode: "should not delete"},
	}
	denyAssignmentsClient := &azuremockclient.DenyAssignmentsClientFunc{}
	syncer := newTestDenyAssignmentV2Syncer(t, mockResourcesDB, denyAssignmentsClient, mockGeneric)
	denyAssignmentsClient.GetFunc = matchingGetResponseForAllTypes(cluster, serviceProviderCluster, syncer)

	err = syncer.SyncOnce(ctx, testKey())
	require.NoError(t, err)
	assert.Empty(t, mockGeneric.DeleteCalls)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.Contains(t, updated.Status.DenyAssignmentsOverManagedResourceGroup, "stale-type-not-in-definitions")
	assert.True(t, controllerutil.ResourceIDsEqual(staleResourceID, updated.Status.DenyAssignmentsOverManagedResourceGroup["stale-type-not-in-definitions"].AzureResource))
	assert.Nil(t, updated.Status.DenyAssignmentsOverManagedResourceGroup["stale-type-not-in-definitions"].DeconfigureTimestamp)
	require.Contains(t, updated.Status.DenyAssignmentsOverManagedResourceGroup, denyAssignmentSuffixResources)
	require.NotNil(t, updated.Status.DenyAssignmentsOverManagedResourceGroup[denyAssignmentSuffixResources].AzureResource)
}

func TestClusterDenyAssignmentV2SyncOnceSkipsClusterDeletion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := metav1.NewTime(time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC))
	cluster := newTestCluster(func(c *coreapi.Cluster) {
		c.ServiceProviderProperties.DeletionTimestamp = &now
	})
	serviceProviderCluster := newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
		spc.Status.DenyAssignmentsOverManagedResourceGroup = map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: {},
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
	id, err := coreapihelpers.ToDenyAssignmentResourceID(testSubscriptionID, testManagedRG, name)
	if err != nil {
		panic(err)
	}
	return id
}

func seedEnsuredTypeStatus(
	cluster *coreapi.Cluster,
	spc *coreapi.ServiceProviderCluster,
	denyAssignmentType string,
	azureResource *azcorearm.ResourceID,
) *coreapi.DenyAssignmentStatus {
	return &coreapi.DenyAssignmentStatus{
		AzureResource:      azureResource,
		EnsuredPermissions: seedTestEnsuredPermissions(cluster, denyAssignmentType),
		ExcludedIdentities: seedTestExcludedIdentities(cluster, spc, denyAssignmentType),
	}
}

func desiredIdentityRows(
	cluster *coreapi.Cluster,
	spc *coreapi.ServiceProviderCluster,
	denyAssignmentType string,
) map[string]*coreapi.DenyAssignmentExcludedIdentityStatus {
	identities := seedTestExcludedIdentities(cluster, spc, denyAssignmentType)
	for _, status := range identities {
		status.EnsuredIdentity = nil
	}
	return identities
}

func newTestDenyAssignmentV2Syncer(
	t *testing.T,
	resourcesDB *corecosmosstoragetesting.MockResourcesDBClient,
	denyAssignmentsClient *azuremockclient.DenyAssignmentsClientFunc,
	genericResourcesClient *azuremockclient.GenericResourcesClientFunc,
) *clusterDenyAssignmentSyncer {
	t.Helper()
	return &clusterDenyAssignmentSyncer{
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

func TestSelectExcludedPrincipalsForPUT(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	insideWait := metav1.NewTime(now.Add(-time.Hour))
	elapsedWait := metav1.NewTime(now.Add(-25 * time.Hour))
	olderWait := metav1.NewTime(now.Add(-2 * time.Hour))

	const (
		liveA = "principal-a"
		liveB = "principal-b"
		waitC = "principal-c"
		waitD = "principal-d"
	)

	syncer := &clusterDenyAssignmentSyncer{}
	ensured := func(principalID string) *coreapi.DenyAssignmentExcludedEnsuredIdentity {
		return &coreapi.DenyAssignmentExcludedEnsuredIdentity{PrincipalID: principalID}
	}

	t.Run("live desired principals are always included", func(t *testing.T) {
		t.Parallel()
		desired := map[string]struct{}{
			liveA: {},
			liveB: {},
		}
		included, dropped, err := syncer.selectExcludedPrincipalsForDenyAssignmentPUT(&coreapi.DenyAssignmentStatus{}, desired, now, 25)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{liveA, liveB}, included)
		assert.Empty(t, dropped)
	})

	t.Run("nil timestamp row not in desired is not included", func(t *testing.T) {
		t.Parallel()
		status := &coreapi.DenyAssignmentStatus{
			ExcludedIdentities: map[string]*coreapi.DenyAssignmentExcludedIdentityStatus{
				liveA: {EnsuredIdentity: ensured(liveA)},
			},
		}
		included, dropped, err := syncer.selectExcludedPrincipalsForDenyAssignmentPUT(status, nil, now, 25)
		require.NoError(t, err)
		assert.Empty(t, included)
		assert.Empty(t, dropped)
	})

	t.Run("elapsed cooldown is dropped when nothing is desired", func(t *testing.T) {
		t.Parallel()
		status := &coreapi.DenyAssignmentStatus{
			ExcludedIdentities: map[string]*coreapi.DenyAssignmentExcludedIdentityStatus{
				waitC: {DeconfigureTimestamp: &elapsedWait, EnsuredIdentity: ensured(waitC)},
			},
		}
		included, dropped, err := syncer.selectExcludedPrincipalsForDenyAssignmentPUT(status, nil, now, 25)
		require.NoError(t, err)
		assert.Empty(t, included)
		assert.Equal(t, []string{waitC}, dropped)
	})

	t.Run("cooldown inside 24h is included", func(t *testing.T) {
		t.Parallel()
		desired := map[string]struct{}{
			liveA: {},
		}
		status := &coreapi.DenyAssignmentStatus{
			ExcludedIdentities: map[string]*coreapi.DenyAssignmentExcludedIdentityStatus{
				liveA: {EnsuredIdentity: ensured(liveA)},
				waitC: {DeconfigureTimestamp: &insideWait, EnsuredIdentity: ensured(waitC)},
			},
		}
		included, dropped, err := syncer.selectExcludedPrincipalsForDenyAssignmentPUT(status, desired, now, 25)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{liveA, waitC}, included)
		assert.Empty(t, dropped)
	})

	t.Run("cooldown after 24h is dropped", func(t *testing.T) {
		t.Parallel()
		desired := map[string]struct{}{
			liveA: {},
		}
		status := &coreapi.DenyAssignmentStatus{
			ExcludedIdentities: map[string]*coreapi.DenyAssignmentExcludedIdentityStatus{
				liveA: {EnsuredIdentity: ensured(liveA)},
				waitC: {DeconfigureTimestamp: &elapsedWait, EnsuredIdentity: ensured(waitC)},
			},
		}
		included, dropped, err := syncer.selectExcludedPrincipalsForDenyAssignmentPUT(status, desired, now, 25)
		require.NoError(t, err)
		assert.Equal(t, []string{liveA}, included)
		assert.Equal(t, []string{waitC}, dropped)
	})

	t.Run("LRU drops older waiters when over the principal limit", func(t *testing.T) {
		t.Parallel()
		desired := map[string]struct{}{
			liveA: {},
			liveB: {},
		}
		status := &coreapi.DenyAssignmentStatus{
			ExcludedIdentities: map[string]*coreapi.DenyAssignmentExcludedIdentityStatus{
				liveA: {EnsuredIdentity: ensured(liveA)},
				liveB: {EnsuredIdentity: ensured(liveB)},
				waitC: {DeconfigureTimestamp: &insideWait, EnsuredIdentity: ensured(waitC)},
				waitD: {DeconfigureTimestamp: &olderWait, EnsuredIdentity: ensured(waitD)},
			},
		}
		included, dropped, err := syncer.selectExcludedPrincipalsForDenyAssignmentPUT(status, desired, now, 3)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{liveA, liveB, waitC}, included)
		assert.Equal(t, []string{waitD}, dropped)
	})

	t.Run("must-include over the limit is an error", func(t *testing.T) {
		t.Parallel()
		desired := map[string]struct{}{
			liveA: {},
			liveB: {},
		}
		_, _, err := syncer.selectExcludedPrincipalsForDenyAssignmentPUT(&coreapi.DenyAssignmentStatus{}, desired, now, 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceed Azure ExcludePrincipals limit")
	})
}

func TestSyncEnsuredExcludedIdentities(t *testing.T) {
	t.Parallel()

	const (
		liveA = "principal-a"
		waitC = "principal-c"
	)
	insideWait := metav1.NewTime(time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC))
	targetA := &coreapi.DenyAssignmentTargetIdentity{
		ResourceID:  metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/a")),
		ClientID:    "client-a",
		TenantID:    "tenant-a",
		PrincipalID: liveA,
	}
	syncer := &clusterDenyAssignmentSyncer{}

	t.Run("existing desired row is ensured and dropped waiters are deleted", func(t *testing.T) {
		t.Parallel()
		status := &coreapi.DenyAssignmentStatus{
			ExcludedIdentities: map[string]*coreapi.DenyAssignmentExcludedIdentityStatus{
				liveA: {TargetIdentity: targetA},
				waitC: {
					DeconfigureTimestamp: &insideWait,
					EnsuredIdentity:      &coreapi.DenyAssignmentExcludedEnsuredIdentity{PrincipalID: waitC},
				},
			},
		}
		desired := map[string]struct{}{
			liveA: {},
		}
		require.NoError(t, syncer.syncEnsuredExcludedIdentities(status, desired, map[string]struct{}{}))

		require.Contains(t, status.ExcludedIdentities, liveA)
		require.NotNil(t, status.ExcludedIdentities[liveA].EnsuredIdentity)
		assert.Equal(t, liveA, status.ExcludedIdentities[liveA].EnsuredIdentity.PrincipalID)
		assert.Nil(t, status.ExcludedIdentities[liveA].DeconfigureTimestamp)
		assert.Equal(t, targetA, status.ExcludedIdentities[liveA].TargetIdentity)
		require.Contains(t, status.ExcludedIdentities, waitC)
		require.NotNil(t, status.ExcludedIdentities[waitC].DeconfigureTimestamp)

		require.NoError(t, syncer.syncEnsuredExcludedIdentities(status, desired, map[string]struct{}{waitC: {}}))
		require.Contains(t, status.ExcludedIdentities, liveA)
		assert.Equal(t, targetA, status.ExcludedIdentities[liveA].TargetIdentity)
		assert.NotContains(t, status.ExcludedIdentities, waitC)
	})
}

func testManagedResourceGroupID() *azcorearm.ResourceID {
	return metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testManagedRG,
	))
}

func testKey() controllerutils.HCPClusterKey {
	return controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
}

func testSubscription() *coreapi.Subscription {
	rid := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID))
	return &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   rid,
			PartitionKey: strings.ToLower(rid.SubscriptionID),
		},
		Properties: &coreapi.SubscriptionProperties{TenantId: ptr.To(testTenantID)},
	}
}

func testIdentityResourceID(name string) *azcorearm.ResourceID {
	return metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/" + name,
	))
}

func newTestCluster(opts ...func(*coreapi.Cluster)) *coreapi.Cluster {
	rid := testClusterResourceID()
	cluster := coreapitesting.MinimumValidClusterTestCase()
	cluster.CosmosMetadata = coreapi.CosmosMetadata{
		ResourceID:   rid,
		PartitionKey: strings.ToLower(rid.SubscriptionID),
	}
	cluster.ID = rid
	cluster.Name = testClusterName
	cluster.Type = rid.ResourceType.String()
	cluster.ServiceProviderProperties.ClusterServiceID = nil

	csID := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/abc123"))
	cluster.ServiceProviderProperties.PendingClusterServiceID = &csID
	cluster.CustomerProperties.Platform.ManagedResourceGroup = testManagedRG

	cpOps, dpOps, serviceManagedID := testClusterIdentities()
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = cpOps
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators = dpOps
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = serviceManagedID

	for _, opt := range opts {
		opt(cluster)
	}
	return cluster
}

// testClusterIdentities returns the control plane operator, data plane operator, and service
// managed identity resource IDs used across the deny-assignment tests. newTestCluster wires these
// into the cluster's operator configuration; newTestSPC mirrors them (with resolved principal IDs)
// onto the ServiceProviderCluster the way the identity-resolution controllers do in production.
func testClusterIdentities() (cpOps, dpOps map[string]*azcorearm.ResourceID, serviceManagedID *azcorearm.ResourceID) {
	cpOps = map[string]*azcorearm.ResourceID{
		"cluster-api-azure":        testIdentityResourceID("capi-azure"),
		"cloud-controller-manager": testIdentityResourceID("ccm"),
		"disk-csi-driver":          testIdentityResourceID("disk-csi"),
		"control-plane":            testIdentityResourceID("control-plane"),
		"image-registry":           testIdentityResourceID("image-registry"),
		"file-csi-driver":          testIdentityResourceID("file-csi"),
		"kms":                      testIdentityResourceID("kms"),
		"ingress":                  testIdentityResourceID("ingress"),
		"cloud-network-config":     testIdentityResourceID("cloud-network-config"),
	}
	dpOps = map[string]*azcorearm.ResourceID{
		"image-registry":  testIdentityResourceID("dp-image-registry"),
		"disk-csi-driver": testIdentityResourceID("dp-disk-csi"),
		"file-csi-driver": testIdentityResourceID("dp-file-csi"),
	}
	serviceManagedID = testIdentityResourceID("service-managed")
	return cpOps, dpOps, serviceManagedID
}

// testPrincipalID returns the deterministic principal ID the tests expect for a given managed
// identity resource ID. It is the value newTestSPC records on the ServiceProviderCluster and the
// value resolvePrincipalID must return for that identity.
func testPrincipalID(id *azcorearm.ResourceID) string {
	return "principal-" + id.Name
}

func testClientID(id *azcorearm.ResourceID) string {
	return "client-" + id.Name
}

func testIdentityTenantID(id *azcorearm.ResourceID) string {
	return "tenant-" + id.Name
}

func newTestSPC(opts ...func(*coreapi.ServiceProviderCluster)) *coreapi.ServiceProviderCluster {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s",
		testClusterResourceID().String(),
		coreapi.ServiceProviderClusterResourceTypeName,
		coreapi.ServiceProviderClusterResourceName,
	)))
	spc := &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID},
		Spec:           coreapi.ServiceProviderClusterSpec{},
	}
	spc.SetPartitionKey(testSubscriptionID)
	spc.Status.AzureResources.ManagedResourceGroup.AzureResource = testManagedResourceGroupID()
	seedResolvedIdentities(spc)
	for _, opt := range opts {
		opt(spc)
	}
	return spc

}

func seedTestEnsuredPermissions(cluster *coreapi.Cluster, denyAssignmentType string) *coreapi.DenyAssignmentEnsuredPermissions {
	definition := denyAssignmentDefinitionsByType(cluster)[denyAssignmentType]
	if definition == nil {
		panic("no definition for deny assignment type " + denyAssignmentType)
	}
	return (&clusterDenyAssignmentSyncer{}).ensuredPermissionsFromDefinition(definition)
}

func testClusterResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))
}

// seedResolvedIdentities mirrors the cluster's managed identities onto the ServiceProviderCluster
// status the same way the MSI and data-plane identity-resolution controllers do in production, so
// resolvePrincipalID can find a resolved principal ID for every excluded identity.
func seedResolvedIdentities(spc *coreapi.ServiceProviderCluster) {
	cpOps, dpOps, serviceManagedID := testClusterIdentities()

	cpIdentities := make(map[string]*coreapi.ServiceProviderClusterControlPlaneOperatorIdentity, len(cpOps))
	for _, id := range cpOps {
		cpIdentities[strings.ToLower(id.String())] = &coreapi.ServiceProviderClusterControlPlaneOperatorIdentity{
			ResourceID:  id,
			ClientID:    ptr.To(testClientID(id)),
			PrincipalID: ptr.To(testPrincipalID(id)),
		}
	}
	spc.Status.MSIManagedIdentities.ControlPlaneOperatorsIdentities = cpIdentities
	spc.Status.MSIManagedIdentities.ServiceManagedIdentity = &coreapi.ServiceProviderClusterServiceManagedIdentity{
		ResourceID:  serviceManagedID,
		ClientID:    ptr.To(testClientID(serviceManagedID)),
		PrincipalID: ptr.To(testPrincipalID(serviceManagedID)),
	}

	dpIdentities := make(map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity, len(dpOps))
	for _, id := range dpOps {
		dpIdentities[strings.ToLower(id.String())] = &coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
			ResourceID:  id,
			ClientID:    ptr.To(testClientID(id)),
			PrincipalID: ptr.To(testPrincipalID(id)),
		}
	}
	spc.Status.DataPlaneOperatorsManagedIdentities.Identities = dpIdentities

	details := make(map[string]*coreapi.ManagedIdentityMetadata, len(cpOps)+len(dpOps)+1)
	metadataValue := func(id *azcorearm.ResourceID) *coreapi.IdentityMetadataValue {
		return &coreapi.IdentityMetadataValue{
			ClientID:    ptr.To(testClientID(id)),
			PrincipalID: ptr.To(testPrincipalID(id)),
			TenantID:    ptr.To(testIdentityTenantID(id)),
		}
	}
	seedIdentity := func(id *azcorearm.ResourceID) {
		details[strings.ToLower(id.String())] = &coreapi.ManagedIdentityMetadata{
			ResourceID:                                    id,
			MetadataFromARMUserAssignedIdentitiesAPI:      metadataValue(id),
			MetadataFromManagedIdentitiesDataplaneService: metadataValue(id),
			MetadataFromHardcodedIdentity:                 metadataValue(id),
		}
	}
	for _, id := range cpOps {
		seedIdentity(id)
	}
	for _, id := range dpOps {
		seedIdentity(id)
	}
	seedIdentity(serviceManagedID)
	spc.Status.ManagedIdentityDetails = details
}

func seedTestExcludedIdentities(
	cluster *coreapi.Cluster,
	spc *coreapi.ServiceProviderCluster,
	denyAssignmentType string,
) map[string]*coreapi.DenyAssignmentExcludedIdentityStatus {
	definition := denyAssignmentDefinitionsByType(cluster)[denyAssignmentType]
	desired, unresolved, err := (&clusterDenyAssignmentIntentSyncer{}).desiredExcludedIdentities(cluster, spc, definition)
	if err != nil {
		panic(err)
	}
	if len(unresolved) > 0 {
		panic("test identities are unresolved")
	}
	identities := make(map[string]*coreapi.DenyAssignmentExcludedIdentityStatus, len(desired))
	for principalID, target := range desired {
		identities[principalID] = &coreapi.DenyAssignmentExcludedIdentityStatus{
			TargetIdentity: target,
			EnsuredIdentity: &coreapi.DenyAssignmentExcludedEnsuredIdentity{
				PrincipalID: principalID,
			},
		}
	}
	if len(identities) == 0 {
		return nil
	}
	return identities
}

func denyAssignmentNotFoundError() error {
	return &azcore.ResponseError{ErrorCode: "DenyAssignmentNotFound"}
}

func resourceNotFoundError() error {
	return &azcore.ResponseError{StatusCode: 404}
}

func matchingGetResponseForAllTypes(cluster *coreapi.Cluster, spc *coreapi.ServiceProviderCluster, syncer *clusterDenyAssignmentSyncer) func(ctx context.Context, scope string, denyAssignmentID string, opts *armauthorization.DenyAssignmentsClientGetOptions) (armauthorization.DenyAssignmentsClientGetResponse, error) {
	defs := denyAssignmentDefinitions(cluster)
	nameToType := make(map[string]string, len(defs))
	for _, def := range defs {
		resourceID, err := generateDenyAssignmentResourceID(cluster, def.denyAssignmentType)
		if err != nil {
			panic(err)
		}
		nameToType[resourceID.Name] = def.denyAssignmentType
	}

	defsByType := denyAssignmentDefinitionsByType(cluster)

	return func(ctx context.Context, scope string, denyAssignmentID string, opts *armauthorization.DenyAssignmentsClientGetOptions) (armauthorization.DenyAssignmentsClientGetResponse, error) {
		daType, ok := nameToType[denyAssignmentID]
		if !ok {
			return armauthorization.DenyAssignmentsClientGetResponse{}, denyAssignmentNotFoundError()
		}
		def := defsByType[daType]
		notActions := def.notActions
		if notActions == nil {
			notActions = []string{}
		}
		dataActions := def.dataActions
		if dataActions == nil {
			dataActions = []string{}
		}
		var principalIDs []string
		if status := spc.Status.DenyAssignmentsOverManagedResourceGroup[daType]; status != nil {
			desired, err := desiredExcludedIdentitiesFromDenyAssignmentStatus(status)
			if err != nil {
				panic(err)
			}
			included, _, err := syncer.selectExcludedPrincipalsForDenyAssignmentPUT(status, desired, syncer.clock.Now(), denyAssignmentExcludePrincipalsLimit)
			if err != nil {
				panic(err)
			}
			principalIDs = included
		}
		excludedPrincipals := make([]*armauthorization.Principal, 0, len(principalIDs))
		for _, pid := range principalIDs {
			excludedPrincipals = append(excludedPrincipals, &armauthorization.Principal{ID: ptr.To(pid)})
		}

		return armauthorization.DenyAssignmentsClientGetResponse{
			DenyAssignment: armauthorization.DenyAssignment{
				Properties: &armauthorization.DenyAssignmentProperties{
					Permissions: []*armauthorization.DenyAssignmentPermission{
						{
							Actions:     to.SliceOfPtrs(def.actions...),
							NotActions:  to.SliceOfPtrs(notActions...),
							DataActions: to.SliceOfPtrs(dataActions...),
						},
					},
					ExcludePrincipals: excludedPrincipals,
				},
			},
		}, nil
	}
}
