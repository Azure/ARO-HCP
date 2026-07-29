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

package externalauthdeletion

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/lru"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID      = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName   = "test-rg"
	testClusterName         = "test-cluster"
	testExternalAuthName    = "test-externalauth"
	testClusterServiceIDStr = "/api/aro_hcp/v1alpha1/clusters/abc123"
	testExternalAuthCSIDStr = testClusterServiceIDStr + "/external_auth_config/external_auths/" + testExternalAuthName
)

func TestExternalAuthClusterServiceDeleteDispatchSyncer_SyncOnce(t *testing.T) {
	fixedClockTime := time.Now().UTC().Truncate(time.Second)

	testKey := controllerutils.HCPExternalAuthKey{
		SubscriptionID:      testSubscriptionID,
		ResourceGroupName:   testResourceGroupName,
		HCPClusterName:      testClusterName,
		HCPExternalAuthName: testExternalAuthName,
	}

	verifyClusterServiceDeletionTimestampIsNil := func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
		t.Helper()
		stored, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).
			ExternalAuth(testClusterName).Get(ctx, testExternalAuthName)
		require.NoError(t, err)
		assert.Nil(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp)
	}

	verifyClusterServiceDeletionTimestampStamped := func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
		t.Helper()
		stored, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).
			ExternalAuth(testClusterName).Get(ctx, testExternalAuthName)
		require.NoError(t, err)
		require.NotNil(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp, "expected ClusterServiceDeletionTimestamp to be stamped")
		assert.True(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp.Time.Equal(fixedClockTime),
			"expected ClusterServiceDeletionTimestamp to equal fixedClockTime, got %v", stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp.Time)
	}

	testCases := []struct {
		name                 string
		existingExternalAuth *api.HCPOpenShiftClusterExternalAuth
		firstSeenDeletionAt  time.Time
		setupMockCSClient    func(mock *ocm.MockClusterServiceClientSpec)
		wantErr              bool
		wantErrContain       string
		verifyDB             func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name:                 "when no DeletionTimestamp no-op is performed",
			existingExternalAuth: newTestExternalAuthWithNewDeletionApproach(t, nil),
			verifyDB:             verifyClusterServiceDeletionTimestampIsNil,
		},
		{
			name: "when ClusterServiceDeletionTimestamp is set no-op is performed",
			existingExternalAuth: newTestExternalAuthWithNewDeletionApproach(t, func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
				ea.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-30 * time.Minute)}
			}),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				stored, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).
					ExternalAuth(testClusterName).Get(ctx, testExternalAuthName)
				require.NoError(t, err)
				require.NotNil(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp)
				assert.True(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp.Time.Equal(fixedClockTime.Add(-30*time.Minute)),
					"expected ClusterServiceDeletionTimestamp unchanged")
			},
		},
		{
			name: "when ClusterServiceID is not set and deletion is first observed then first seen is recorded and no-op is performed",
			existingExternalAuth: newTestExternalAuthWithNewDeletionApproach(t, func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
				ea.ServiceProviderProperties.ClusterServiceID = nil
			}),
			verifyDB: verifyClusterServiceDeletionTimestampIsNil,
		},
		{
			name: "when ClusterServiceID is not set and first seen within missing cluster service id is within timeout no-op is performed",
			existingExternalAuth: newTestExternalAuthWithNewDeletionApproach(t, func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
				ea.ServiceProviderProperties.ClusterServiceID = nil
			}),
			firstSeenDeletionAt: fixedClockTime.Add(-missingClusterServiceIDTimeout / 2),
			verifyDB:            verifyClusterServiceDeletionTimestampIsNil,
		},
		{
			name: "when ClusterServiceID is not set and first seen older than missing cluster service id timeout then we give up and set ClusterServiceDeletionTimestamp",
			existingExternalAuth: newTestExternalAuthWithNewDeletionApproach(t, func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
				ea.ServiceProviderProperties.ClusterServiceID = nil
			}),
			firstSeenDeletionAt: fixedClockTime.Add(-missingClusterServiceIDTimeout - time.Second),
			verifyDB:            verifyClusterServiceDeletionTimestampStamped,
		},
		{
			name: "when ClusterServiceID is set we trigger CS external auth deletion and set ClusterServiceDeletionTimestamp",
			existingExternalAuth: newTestExternalAuthWithNewDeletionApproach(t, func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Minute)}
			}),
			firstSeenDeletionAt: fixedClockTime.Add(-missingClusterServiceIDTimeout / 2),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					DeleteExternalAuth(gomock.Any(), api.Must(api.NewInternalID(testExternalAuthCSIDStr))).
					Return(nil)
			},
			verifyDB: verifyClusterServiceDeletionTimestampStamped,
		},
		{
			name: "when CS external auth deletion returns 404 and first seen is within the missing cluster service id timeout no-op is performed",
			existingExternalAuth: newTestExternalAuthWithNewDeletionApproach(t, func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
			}),
			firstSeenDeletionAt: fixedClockTime.Add(-missingClusterServiceIDTimeout / 2),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					DeleteExternalAuth(gomock.Any(), api.Must(api.NewInternalID(testExternalAuthCSIDStr))).
					Return(fakeOCMNotFoundError())
			},
			verifyDB: verifyClusterServiceDeletionTimestampIsNil,
		},
		{
			name: "when CS external auth deletion returns 404 and first seen is older than the missing cluster service id timeout then we give up and set ClusterServiceDeletionTimestamp",
			existingExternalAuth: newTestExternalAuthWithNewDeletionApproach(t, func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
			}),
			firstSeenDeletionAt: fixedClockTime.Add(-missingClusterServiceIDTimeout - time.Second),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					DeleteExternalAuth(gomock.Any(), api.Must(api.NewInternalID(testExternalAuthCSIDStr))).
					Return(fakeOCMNotFoundError())
			},
			verifyDB: verifyClusterServiceDeletionTimestampStamped,
		},
		{
			name: "when CS external auth deletion returns one of the not handled errors we propagate it without setting ClusterServiceDeletionTimestamp",
			existingExternalAuth: newTestExternalAuthWithNewDeletionApproach(t, func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Minute)}
			}),
			firstSeenDeletionAt: fixedClockTime.Add(-missingClusterServiceIDTimeout / 2),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					DeleteExternalAuth(gomock.Any(), api.Must(api.NewInternalID(testExternalAuthCSIDStr))).
					Return(errors.New("boom"))
			},
			wantErr:        true,
			wantErrContain: "failed to delete cluster-service ExternalAuth",
		},
		{
			name: "when CS external auth deletion returns parent cluster is uninstalling we set ClusterServiceDeletionTimestamp immediately",
			existingExternalAuth: newTestExternalAuthWithNewDeletionApproach(t, func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-30 * time.Second)}
			}),
			firstSeenDeletionAt: fixedClockTime.Add(-missingClusterServiceIDTimeout / 2),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				parentClusterUninstallingErr, _ := ocmerrors.NewError().
					Status(http.StatusBadRequest).
					Reason("Cannot delete ExternalAuth: its parent cluster must be in deletable state. Parent cluster state: 'uninstalling'").
					Build()
				mock.EXPECT().
					DeleteExternalAuth(gomock.Any(), api.Must(api.NewInternalID(testExternalAuthCSIDStr))).
					Return(parentClusterUninstallingErr)
			},
			verifyDB: verifyClusterServiceDeletionTimestampStamped,
		},
		{
			name: "UsesNewExternalAuthDeletionApproach false -- no-op even when DeletionTimestamp is set",
			existingExternalAuth: newTestExternalAuthWithOldDeletionApproach(t, func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Minute)}
			}),
			verifyDB: verifyClusterServiceDeletionTimestampIsNil,
		},
		{
			name: "external auth not found no-op is performed",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)

			resources := []any{}
			if tc.existingExternalAuth != nil {
				resources = append(resources, tc.existingExternalAuth)
			}
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tc.setupMockCSClient != nil {
				tc.setupMockCSClient(mockCSClient)
			}

			externalAuthsForLister := []*api.HCPOpenShiftClusterExternalAuth{}
			if tc.existingExternalAuth != nil {
				externalAuthsForLister = append(externalAuthsForLister, tc.existingExternalAuth)
			}

			firstSeenDeletionTimestampCache := lru.New(10)
			if !tc.firstSeenDeletionAt.IsZero() {
				firstSeenDeletionTimestampCache.Add(
					strings.ToLower(tc.existingExternalAuth.ID.String()),
					tc.firstSeenDeletionAt,
				)
			}

			syncer := &externalAuthClusterServiceDeleteDispatchSyncer{
				clock:                           clocktesting.NewFakePassiveClock(fixedClockTime),
				externalAuthLister:              &listertesting.SliceExternalAuthLister{ExternalAuths: externalAuthsForLister},
				resourcesDBClient:               mockResourcesDBClient,
				clusterServiceClient:            mockCSClient,
				firstSeenDeletionTimestampCache: firstSeenDeletionTimestampCache,
			}

			err = syncer.SyncOnce(ctx, testKey)
			if tc.wantErr {
				require.Error(t, err)
				if len(tc.wantErrContain) > 0 {
					require.ErrorContains(t, err, tc.wantErrContain)
				}
				return
			}
			require.NoError(t, err)

			if tc.verifyDB != nil {
				tc.verifyDB(t, ctx, mockResourcesDBClient)
			}
		})
	}
}

func TestExternalAuthClusterServiceDeleteDispatchSyncer_SyncOnce_cacheShortCircuit(t *testing.T) {
	fixedClockTime := time.Now().UTC().Truncate(time.Second)
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)

	externalAuthInDB := newTestExternalAuthWithNewDeletionApproach(t, func(ea *api.HCPOpenShiftClusterExternalAuth) {
		ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
	})
	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{externalAuthInDB})
	require.NoError(t, err)

	// Here the cached external auth does not have a DeletionTimestamp set, so the syncer will short-circuit.
	cachedExternalAuth := newTestExternalAuthWithNewDeletionApproach(t, nil)

	syncer := &externalAuthClusterServiceDeleteDispatchSyncer{
		clock:                           clocktesting.NewFakePassiveClock(fixedClockTime),
		externalAuthLister:              &listertesting.SliceExternalAuthLister{ExternalAuths: []*api.HCPOpenShiftClusterExternalAuth{cachedExternalAuth}},
		resourcesDBClient:               mockResourcesDBClient,
		clusterServiceClient:            ocm.NewMockClusterServiceClientSpec(ctrl),
		firstSeenDeletionTimestampCache: lru.New(10),
	}

	key := controllerutils.HCPExternalAuthKey{
		SubscriptionID:      testSubscriptionID,
		ResourceGroupName:   testResourceGroupName,
		HCPClusterName:      testClusterName,
		HCPExternalAuthName: testExternalAuthName,
	}

	require.NoError(t, syncer.SyncOnce(ctx, key))

	stored, err := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).
		ExternalAuth(testClusterName).Get(ctx, testExternalAuthName)
	require.NoError(t, err)
	assert.Nil(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp)
}

func TestExternalAuthClusterServiceDeleteDispatchSyncer_SyncOnce_firstSeenDeletionCachePopulation(t *testing.T) {
	fixedClockTime := time.Now().UTC().Truncate(time.Second)
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)

	externalAuth := newTestExternalAuthWithNewDeletionApproach(t, func(ea *api.HCPOpenShiftClusterExternalAuth) {
		ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
		ea.ServiceProviderProperties.ClusterServiceID = nil
	})
	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{externalAuth})
	require.NoError(t, err)

	firstSeenDeletionTimestampCache := lru.New(10)
	syncer := &externalAuthClusterServiceDeleteDispatchSyncer{
		clock:                           clocktesting.NewFakePassiveClock(fixedClockTime),
		externalAuthLister:              &listertesting.SliceExternalAuthLister{ExternalAuths: []*api.HCPOpenShiftClusterExternalAuth{externalAuth}},
		resourcesDBClient:               mockResourcesDBClient,
		clusterServiceClient:            ocm.NewMockClusterServiceClientSpec(ctrl),
		firstSeenDeletionTimestampCache: firstSeenDeletionTimestampCache,
	}

	key := controllerutils.HCPExternalAuthKey{
		SubscriptionID:      testSubscriptionID,
		ResourceGroupName:   testResourceGroupName,
		HCPClusterName:      testClusterName,
		HCPExternalAuthName: testExternalAuthName,
	}

	require.NoError(t, syncer.SyncOnce(ctx, key))

	cacheKey := strings.ToLower(externalAuth.ID.String())
	firstSeenEntry, ok := firstSeenDeletionTimestampCache.Get(cacheKey)
	require.True(t, ok, "expected first seen deletion timestamp to be cached")
	assert.True(t, firstSeenEntry.(time.Time).Equal(fixedClockTime))
}

func TestExternalAuthClusterServiceDeleteDispatchSyncer_SyncOnce_firstSeenDeletionCacheCleared(t *testing.T) {
	fixedClockTime := time.Now().UTC().Truncate(time.Second)
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)

	externalAuth := newTestExternalAuthWithNewDeletionApproach(t, func(ea *api.HCPOpenShiftClusterExternalAuth) {
		ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Minute)}
	})
	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{externalAuth})
	require.NoError(t, err)

	cacheKey := strings.ToLower(externalAuth.ID.String())
	firstSeenDeletionTimestampCache := lru.New(10)
	firstSeenDeletionTimestampCache.Add(cacheKey, fixedClockTime.Add(-missingClusterServiceIDTimeout/2))

	mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockCSClient.EXPECT().
		DeleteExternalAuth(gomock.Any(), api.Must(api.NewInternalID(testExternalAuthCSIDStr))).
		Return(nil)

	syncer := &externalAuthClusterServiceDeleteDispatchSyncer{
		clock:                           clocktesting.NewFakePassiveClock(fixedClockTime),
		externalAuthLister:              &listertesting.SliceExternalAuthLister{ExternalAuths: []*api.HCPOpenShiftClusterExternalAuth{externalAuth}},
		resourcesDBClient:               mockResourcesDBClient,
		clusterServiceClient:            mockCSClient,
		firstSeenDeletionTimestampCache: firstSeenDeletionTimestampCache,
	}

	key := controllerutils.HCPExternalAuthKey{
		SubscriptionID:      testSubscriptionID,
		ResourceGroupName:   testResourceGroupName,
		HCPClusterName:      testClusterName,
		HCPExternalAuthName: testExternalAuthName,
	}

	require.NoError(t, syncer.SyncOnce(ctx, key))

	_, ok := firstSeenDeletionTimestampCache.Get(cacheKey)
	assert.False(t, ok, "expected first seen deletion cache entry to be removed after terminal sync")

	stored, err := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).
		ExternalAuth(testClusterName).Get(ctx, testExternalAuthName)
	require.NoError(t, err)
	require.NotNil(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp)
	assert.True(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp.Time.Equal(fixedClockTime))
}

// newTestExternalAuthWithNewDeletionApproach creates a test external auth with the new
// deletion approach enabled. The ClusterServiceID uses the external_auth_config/external_auths
// path format required by Cluster Service.
//
// TODO rename this and remove the newTestExternalAuthWithOldDeletionApproach function once
// the new deletion approach is fully rolled out in all ARO-HCP permanent environments, for all regions.
func newTestExternalAuthWithNewDeletionApproach(t *testing.T, opts func(*api.HCPOpenShiftClusterExternalAuth)) *api.HCPOpenShiftClusterExternalAuth {
	t.Helper()
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/externalAuths/" + testExternalAuthName))
	externalAuthInternalID := api.Ptr(api.Must(api.NewInternalID(testExternalAuthCSIDStr)))
	ea := &api.HCPOpenShiftClusterExternalAuth{
		ProxyResource: arm.ProxyResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: testExternalAuthName,
				Type: api.ExternalAuthResourceType.String(),
			},
		},
		CosmosMetadata: arm.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(resourceID.SubscriptionID)},
		Properties: api.HCPOpenShiftClusterExternalAuthProperties{
			Issuer: api.TokenIssuerProfile{
				URL:       "https://example.com",
				Audiences: []string{"audience1"},
			},
			Claim: api.ExternalAuthClaimProfile{
				Mappings: api.TokenClaimMappingsProfile{
					Username: api.UsernameClaimProfile{
						Claim:        "sub",
						PrefixPolicy: api.UsernameClaimPrefixPolicyNone,
					},
				},
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterExternalAuthServiceProviderProperties{
			ClusterServiceID:                    externalAuthInternalID,
			UsesNewExternalAuthDeletionApproach: true,
		},
	}
	if opts != nil {
		opts(ea)
	}
	return ea
}

// TODO remove this once the new deletion approach is fully rolled out in all ARO-HCP permanent environments, for all regions.
func newTestExternalAuthWithOldDeletionApproach(t *testing.T, opts func(*api.HCPOpenShiftClusterExternalAuth)) *api.HCPOpenShiftClusterExternalAuth {
	ea := newTestExternalAuthWithNewDeletionApproach(t, opts)
	ea.ServiceProviderProperties.UsesNewExternalAuthDeletionApproach = false
	return ea
}

func fakeOCMNotFoundError() error {
	e, _ := ocmerrors.NewError().Status(http.StatusNotFound).Reason("not found").Build()
	return e
}
