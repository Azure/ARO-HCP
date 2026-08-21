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

package identity

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

func TestDesiredDataPlaneOperatorResourceIDsMatchServiceProviderCluster(t *testing.T) {
	t.Parallel()

	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	identityB := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-b"))
	mixedCaseIdentityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/Test-RG/providers/Microsoft.ManagedIdentity/userAssignedIdentities/Identity-A"))

	testCases := []struct {
		name                             string
		desiredResourceIDs               map[string]struct{}
		serviceProviderClusterIdentities map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity
		expectedMatch                    bool
	}{
		{
			name:                             "both empty match",
			desiredResourceIDs:               map[string]struct{}{},
			serviceProviderClusterIdentities: map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{},
			expectedMatch:                    true,
		},
		{
			name: "matching resource ID",
			desiredResourceIDs: map[string]struct{}{
				strings.ToLower(identityA.String()): {},
			},
			serviceProviderClusterIdentities: map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
				strings.ToLower(identityA.String()): {
					ResourceID: identityA,
				},
			},
			expectedMatch: true,
		},
		{
			name: "matching ignores resource ID casing when already lowercased as key",
			desiredResourceIDs: map[string]struct{}{
				strings.ToLower(mixedCaseIdentityA.String()): {},
			},
			serviceProviderClusterIdentities: map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
				strings.ToLower(identityA.String()): {
					ResourceID: identityA,
				},
			},
			expectedMatch: true,
		},
		{
			name: "unique identity count mismatch",
			desiredResourceIDs: map[string]struct{}{
				strings.ToLower(identityA.String()): {},
			},
			serviceProviderClusterIdentities: map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
				strings.ToLower(identityA.String()): {
					ResourceID: identityA,
				},
				strings.ToLower(identityB.String()): {
					ResourceID: identityB,
				},
			},
			expectedMatch: false,
		},
		{
			name: "resource ID mismatch",
			desiredResourceIDs: map[string]struct{}{
				strings.ToLower(identityA.String()): {},
			},
			serviceProviderClusterIdentities: map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
				strings.ToLower(identityB.String()): {
					ResourceID: identityB,
				},
			},
			expectedMatch: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			syncer := &fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer{}
			serviceProviderCluster := &coreapi.ServiceProviderCluster{}
			serviceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.Identities = tc.serviceProviderClusterIdentities

			assert.Equal(t, tc.expectedMatch, syncer.desiredDataPlaneOperatorResourceIDsMatchServiceProviderCluster(tc.desiredResourceIDs, serviceProviderCluster))
		})
	}
}

func TestUniqueDataPlaneOperatorResourceIDs(t *testing.T) {
	t.Parallel()

	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	mixedCaseIdentityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/Test-RG/providers/Microsoft.ManagedIdentity/userAssignedIdentities/Identity-A"))

	syncer := &fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer{}

	t.Run("dedupes shared identity across operators", func(t *testing.T) {
		t.Parallel()
		unique := syncer.uniqueDataPlaneOperatorResourceIDs(map[string]*azcorearm.ResourceID{
			"operator-a": identityA,
			"operator-b": identityA,
		})
		require.NotNil(t, unique)
		assert.Equal(t, map[string]struct{}{
			strings.ToLower(identityA.String()): {},
		}, unique)
	})

	t.Run("lowercases resource ID keys", func(t *testing.T) {
		t.Parallel()
		unique := syncer.uniqueDataPlaneOperatorResourceIDs(map[string]*azcorearm.ResourceID{
			"operator-a": mixedCaseIdentityA,
		})
		require.NotNil(t, unique)
		assert.Equal(t, map[string]struct{}{
			strings.ToLower(identityA.String()): {},
		}, unique)
	})

	t.Run("nil resource ID returns nil", func(t *testing.T) {
		t.Parallel()
		unique := syncer.uniqueDataPlaneOperatorResourceIDs(map[string]*azcorearm.ResourceID{
			"operator-a": nil,
		})
		assert.Nil(t, unique)
	})
}

func TestFetchDataPlaneOperatorsManagedIdentitiesInfoNeedsWork(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	identityB := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-b"))

	matchingDesired := map[string]struct{}{
		strings.ToLower(identityA.String()): {},
	}
	matchingServiceProviderClusterIdentities := map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
		strings.ToLower(identityA.String()): {
			ResourceID: identityA,
		},
	}

	testCases := []struct {
		name                             string
		desiredResourceIDs               map[string]struct{}
		serviceProviderClusterIdentities map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity
		earliestRecheckTime              *metav1.Time
		expectedNeedsWork                bool
	}{
		{
			name:                             "matching identities with future recheck skips work",
			desiredResourceIDs:               matchingDesired,
			serviceProviderClusterIdentities: matchingServiceProviderClusterIdentities,
			earliestRecheckTime:              &metav1.Time{Time: now.Add(time.Hour)},
			expectedNeedsWork:                false,
		},
		{
			name:                             "matching identities with past recheck needs work",
			desiredResourceIDs:               matchingDesired,
			serviceProviderClusterIdentities: matchingServiceProviderClusterIdentities,
			earliestRecheckTime:              &metav1.Time{Time: now.Add(-time.Hour)},
			expectedNeedsWork:                true,
		},
		{
			name:                             "matching identities with nil recheck needs work",
			desiredResourceIDs:               matchingDesired,
			serviceProviderClusterIdentities: matchingServiceProviderClusterIdentities,
			earliestRecheckTime:              nil,
			expectedNeedsWork:                true,
		},
		{
			name: "mismatched identities ignore future recheck",
			desiredResourceIDs: map[string]struct{}{
				strings.ToLower(identityB.String()): {},
			},
			serviceProviderClusterIdentities: matchingServiceProviderClusterIdentities,
			earliestRecheckTime:              &metav1.Time{Time: now.Add(time.Hour)},
			expectedNeedsWork:                true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			syncer := &fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer{
				clock: clocktesting.NewFakePassiveClock(now),
			}
			serviceProviderCluster := &coreapi.ServiceProviderCluster{}
			serviceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.Identities = tc.serviceProviderClusterIdentities
			serviceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.EarliestRecheckTime = tc.earliestRecheckTime

			require.Equal(t, tc.expectedNeedsWork, syncer.needsWork(serviceProviderCluster, tc.desiredResourceIDs))
		})
	}
}

// fakeUserAssignedIdentitiesClient is a minimal hand-written fake implementing
// azureclient.UserAssignedIdentitiesClient. Only Get is exercised by the controller,
// so getResp/getErr make it configurable. CreateOrUpdate and Delete are never called by
// the controller and panic if invoked so an accidental use is caught immediately.
type fakeUserAssignedIdentitiesClient struct {
	getResp armmsi.UserAssignedIdentitiesClientGetResponse
	getErr  error
}

var _ azureclient.UserAssignedIdentitiesClient = (*fakeUserAssignedIdentitiesClient)(nil)

func (f *fakeUserAssignedIdentitiesClient) Get(_ context.Context, _ string, _ string, _ *armmsi.UserAssignedIdentitiesClientGetOptions) (armmsi.UserAssignedIdentitiesClientGetResponse, error) {
	return f.getResp, f.getErr
}

func (f *fakeUserAssignedIdentitiesClient) CreateOrUpdate(_ context.Context, _ string, _ string, _ armmsi.Identity, _ *armmsi.UserAssignedIdentitiesClientCreateOrUpdateOptions) (armmsi.UserAssignedIdentitiesClientCreateOrUpdateResponse, error) {
	panic("CreateOrUpdate not implemented in fakeUserAssignedIdentitiesClient")
}

func (f *fakeUserAssignedIdentitiesClient) Delete(_ context.Context, _ string, _ string, _ *armmsi.UserAssignedIdentitiesClientDeleteOptions) (armmsi.UserAssignedIdentitiesClientDeleteResponse, error) {
	panic("Delete not implemented in fakeUserAssignedIdentitiesClient")
}

// newTestClusterWithIdentities builds an HCPOpenShiftCluster addressable by the mock
// ResourcesDBClient with the supplied ServiceManagedIdentity and data plane operator
// identities on its CustomerProperties.
func newTestClusterWithIdentities(t *testing.T, clusterName string, serviceManagedIdentity *azcorearm.ResourceID, dataPlaneOperators map[string]*azcorearm.ResourceID) *coreapi.HCPOpenShiftCluster {
	t.Helper()

	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName,
	))

	cluster := &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: clusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
	}
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = serviceManagedIdentity
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators = dataPlaneOperators

	return cluster
}

// newTestServiceProviderClusterWithIdentities builds a ServiceProviderCluster addressable
// by the mock ResourcesDBClient with the supplied resolved identities and recheck time.
func newTestServiceProviderClusterWithIdentities(clusterName string, identities map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity, earliestRecheckTime *metav1.Time) *coreapi.ServiceProviderCluster {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/" + coreapi.ServiceProviderClusterResourceTypeName +
			"/" + coreapi.ServiceProviderClusterResourceName,
	))

	serviceProviderCluster := &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
	}
	serviceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.Identities = identities
	serviceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.EarliestRecheckTime = earliestRecheckTime

	return serviceProviderCluster
}

// TestFetchDataPlaneOperatorsManagedIdentitiesInfoSyncOnceNilServiceManagedIdentity verifies
// that a nil cluster ServiceManagedIdentity is turned into a tracked error before the SMI
// client is built, rather than panicking inside the client builder (which dereferences
// smiResourceID.String()).
func TestFetchDataPlaneOperatorsManagedIdentitiesInfoSyncOnceNilServiceManagedIdentity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))

	// Cluster has a data plane operator identity but a nil ServiceManagedIdentity.
	cluster := newTestClusterWithIdentities(t, testClusterName, nil, map[string]*azcorearm.ResourceID{
		"operator-a": identityA,
	})
	// ServiceProviderCluster with no resolved identities so needsWork returns true and SyncOnce reaches the guard.
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	// The builder must NOT be called: the nil-SMI guard returns before building the client.
	smiClientBuilder.EXPECT().
		UserAssignedIdentitiesClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Times(0)

	syncer := &fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer{
		clock:                        clocktesting.NewFakePassiveClock(now),
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:            mockResourcesDB,
		smiClientBuilder:             smiClientBuilder,
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	// SyncOnce must return a tracked error (not panic) when ServiceManagedIdentity is nil.
	err = syncer.SyncOnce(ctx, key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ServiceManagedIdentity is nil")
}

// TestFetchDataPlaneOperatorsManagedIdentitiesInfoSyncOnceClearsEarliestRecheckTimeOnGetError
// verifies that when an Azure Get fails and the desired identity set changed, the persisted
// ServiceProviderCluster has EarliestRecheckTime cleared to nil (so needsWork returns true on the retry) even
// though the previously stored value was in the future.
func TestFetchDataPlaneOperatorsManagedIdentitiesInfoSyncOnceClearsEarliestRecheckTimeOnGetError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	identityB := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-b"))

	// Desired set is {B}; the ServiceProviderCluster currently stores {A} with a FUTURE recheck time. The desired
	// set differs from ServiceProviderCluster, so needsWork returns true and SyncOnce re-queries Azure.
	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		"operator-b": identityB,
	})
	futureRecheck := &metav1.Time{Time: now.Add(time.Hour)}
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
		strings.ToLower(identityA.String()): {ResourceID: identityA},
	}, futureRecheck)

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	// The fake Azure client returns a non-ResourceNotFound error so the Get failure is accumulated.
	getErr := errors.New("simulated azure Get failure")
	fakeClient := &fakeUserAssignedIdentitiesClient{getErr: getErr}

	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		UserAssignedIdentitiesClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(fakeClient, nil).
		Times(1)

	syncer := &fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer{
		clock:                        clocktesting.NewFakePassiveClock(now),
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:            mockResourcesDB,
		smiClientBuilder:             smiClientBuilder,
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	// SyncOnce should surface the accumulated Get error...
	err = syncer.SyncOnce(ctx, key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulated azure Get failure")

	// ...and must clear EarliestRecheckTime so needsWork returns true on the workqueue retry.
	updatedServiceProviderCluster, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	assert.Nil(t, updatedServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.EarliestRecheckTime)

	// The desired identity (B) should be persisted; the stale one (A) pruned.
	assert.Contains(t, updatedServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.Identities, strings.ToLower(identityB.String()))
	assert.NotContains(t, updatedServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.Identities, strings.ToLower(identityA.String()))

	// On the Get error, identity B must have ClientID/PrincipalID cleared and the error recorded in RetrievalError.
	identityBEntry := updatedServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.Identities[strings.ToLower(identityB.String())]
	require.NotNil(t, identityBEntry)
	assert.Nil(t, identityBEntry.ClientID)
	assert.Nil(t, identityBEntry.PrincipalID)
	require.NotNil(t, identityBEntry.RetrievalError)
	assert.Contains(t, *identityBEntry.RetrievalError, "simulated azure Get failure")
}

// TestFetchDataPlaneOperatorsManagedIdentitiesInfoSyncOnceClearsResolvedValuesOnGetError
// verifies that a non-ResourceNotFound Azure Get failure clears any previously resolved
// ClientID/PrincipalID (they are no longer trustworthy) and records the error in
// RetrievalError, while still surfacing the accumulated error.
func TestFetchDataPlaneOperatorsManagedIdentitiesInfoSyncOnceClearsResolvedValuesOnGetError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))

	// Desired set is {A}; the ServiceProviderCluster already stores A with resolved
	// ClientID/PrincipalID and a past recheck time so needsWork returns true.
	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		"operator-a": identityA,
	})
	priorClientID := "prior-client-id"
	priorPrincipalID := "prior-principal-id"
	pastRecheck := &metav1.Time{Time: now.Add(-time.Hour)}
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
		strings.ToLower(identityA.String()): {
			ResourceID:  identityA,
			ClientID:    &priorClientID,
			PrincipalID: &priorPrincipalID,
		},
	}, pastRecheck)

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	getErr := errors.New("simulated azure Get failure")
	fakeClient := &fakeUserAssignedIdentitiesClient{getErr: getErr}

	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		UserAssignedIdentitiesClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(fakeClient, nil).
		Times(1)

	syncer := &fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer{
		clock:                        clocktesting.NewFakePassiveClock(now),
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:            mockResourcesDB,
		smiClientBuilder:             smiClientBuilder,
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	err = syncer.SyncOnce(ctx, key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulated azure Get failure")

	updatedServiceProviderCluster, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)

	entry := updatedServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.Identities[strings.ToLower(identityA.String())]
	require.NotNil(t, entry)
	assert.Nil(t, entry.ClientID, "previously resolved ClientID must be cleared on Get error")
	assert.Nil(t, entry.PrincipalID, "previously resolved PrincipalID must be cleared on Get error")
	require.NotNil(t, entry.RetrievalError)
	assert.Contains(t, *entry.RetrievalError, "simulated azure Get failure")

	// A Get failure also clears EarliestRecheckTime so needsWork retries immediately.
	assert.Nil(t, updatedServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.EarliestRecheckTime)
}

// TestFetchDataPlaneOperatorsManagedIdentitiesInfoSyncOnceResourceNotFoundSetsRetrievalError
// verifies that when Azure reports the identity as not found, the entry is kept with nil
// ClientID/PrincipalID and a RetrievalError explaining why, the sync is NOT failed (no error
// returned), and EarliestRecheckTime is set to a future value.
func TestFetchDataPlaneOperatorsManagedIdentitiesInfoSyncOnceResourceNotFoundSetsRetrievalError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

	serviceManagedIdentity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi"))
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))

	cluster := newTestClusterWithIdentities(t, testClusterName, serviceManagedIdentity, map[string]*azcorearm.ResourceID{
		"operator-a": identityA,
	})
	// ServiceProviderCluster with no resolved identities and nil recheck so needsWork returns true.
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	// The fake Azure client returns a ResourceNotFound error.
	fakeClient := &fakeUserAssignedIdentitiesClient{getErr: resourceNotFoundResponseError()}

	ctrl := gomock.NewController(t)
	smiClientBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
	smiClientBuilder.EXPECT().
		UserAssignedIdentitiesClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(fakeClient, nil).
		Times(1)

	syncer := &fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer{
		clock:                        clocktesting.NewFakePassiveClock(now),
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:            mockResourcesDB,
		smiClientBuilder:             smiClientBuilder,
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	// ResourceNotFound is not a sync failure.
	err = syncer.SyncOnce(ctx, key)
	require.NoError(t, err)

	updatedServiceProviderCluster, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)

	entry := updatedServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.Identities[strings.ToLower(identityA.String())]
	require.NotNil(t, entry)
	assert.Nil(t, entry.ClientID)
	assert.Nil(t, entry.PrincipalID)
	require.NotNil(t, entry.RetrievalError)
	assert.Contains(t, *entry.RetrievalError, "ResourceNotFound")

	// A non-failing sync sets a future EarliestRecheckTime.
	require.NotNil(t, updatedServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.EarliestRecheckTime)
	assert.True(t, updatedServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.EarliestRecheckTime.After(now))
}

// resourceNotFoundResponseError returns an *azcore.ResponseError that
// azureclient.IsResourceNotFoundErr recognizes as a ResourceNotFound. The RawResponse is
// fully populated so err.Error() renders without panicking.
func resourceNotFoundResponseError() *azcore.ResponseError {
	return &azcore.ResponseError{
		ErrorCode:  "ResourceNotFound",
		StatusCode: http.StatusNotFound,
		RawResponse: &http.Response{
			Status:     "404 Not Found",
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"ResourceNotFound","message":"The identity was not found."}}`)),
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    &url.URL{Scheme: "https", Host: "management.azure.com", Path: "/identity"},
			},
		},
	}
}
