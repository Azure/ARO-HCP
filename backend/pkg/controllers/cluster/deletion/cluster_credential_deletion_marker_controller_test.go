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

package deletion

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func newTestCredentialRequest(t *testing.T, credName string, opts ...func(*coreapi.SystemAdminCredentialRequest)) *coreapi.SystemAdminCredentialRequest {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		coreapi.ToSystemAdminCredentialRequestResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName, credName),
	))
	cred := &coreapi.SystemAdminCredentialRequest{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(testSubscriptionID),
		},
		Spec: coreapi.SystemAdminCredentialRequestSpec{
			Username:    "test-user",
			OperationID: "test-op",
		},
	}
	for _, opt := range opts {
		opt(cred)
	}
	return cred
}

func newTestCredentialRevocation(t *testing.T, revocationName string, opts ...func(*coreapi.SystemAdminCredentialRevocation)) *coreapi.SystemAdminCredentialRevocation {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		coreapi.ToSystemAdminCredentialRevocationResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName, revocationName),
	))
	revocation := &coreapi.SystemAdminCredentialRevocation{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(testSubscriptionID),
		},
		Spec: coreapi.SystemAdminCredentialRevocationSpec{
			OperationID:    "test-op",
			RevokeOpSuffix: "abcd1234",
		},
	}
	for _, opt := range opts {
		opt(revocation)
	}
	return revocation
}

func TestClusterCredentialDeletionMarkerController_SyncOnce(t *testing.T) {
	fixedClockTime := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	testKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	readyForDeletionCluster := func(t *testing.T) *coreapi.HCPOpenShiftCluster {
		return newTestClusterWithNewDeletionApproach(t, func(c *coreapi.HCPOpenShiftCluster) {
			c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
		})
	}

	testCases := []struct {
		name            string
		existingCluster *coreapi.HCPOpenShiftCluster
		extraResources  []any
		wantErr         bool
		verifyDB        func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient)
	}{
		{
			name:            "no DeletionTimestamp -- no-op",
			existingCluster: newTestClusterWithNewDeletionApproach(t, nil),
			extraResources:  []any{newTestCredentialRequest(t, "cred-1")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cred, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRequests(testClusterName).Get(ctx, "cred-1")
				require.NoError(t, err)
				assert.Nil(t, cred.Status.DeletionTimestamp)
			},
		},
		{
			name: "feature flag false -- no-op",
			existingCluster: newTestClusterWithOldDeletionApproach(t, func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
				c.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-30 * time.Minute)}
				c.ServiceProviderProperties.ClusterServiceID = nil
			}),
			extraResources: []any{newTestCredentialRequest(t, "cred-1")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cred, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRequests(testClusterName).Get(ctx, "cred-1")
				require.NoError(t, err)
				assert.Nil(t, cred.Status.DeletionTimestamp)
			},
		},
		{
			name: "cluster not found -- no-op",
		},
		{
			name:            "marks credential request for deletion",
			existingCluster: readyForDeletionCluster(t),
			extraResources:  []any{newTestCredentialRequest(t, "cred-1")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cred, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRequests(testClusterName).Get(ctx, "cred-1")
				require.NoError(t, err)
				require.NotNil(t, cred.Status.DeletionTimestamp)
				assert.True(t, fixedClockTime.Equal(cred.Status.DeletionTimestamp.Time))
			},
		},
		{
			name:            "marks multiple credential requests for deletion",
			existingCluster: readyForDeletionCluster(t),
			extraResources: []any{
				newTestCredentialRequest(t, "cred-1"),
				newTestCredentialRequest(t, "cred-2"),
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				credCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRequests(testClusterName)
				cred1, err := credCRUD.Get(ctx, "cred-1")
				require.NoError(t, err)
				require.NotNil(t, cred1.Status.DeletionTimestamp)

				cred2, err := credCRUD.Get(ctx, "cred-2")
				require.NoError(t, err)
				require.NotNil(t, cred2.Status.DeletionTimestamp)
			},
		},
		{
			name:            "skips credential request already marked for deletion",
			existingCluster: readyForDeletionCluster(t),
			extraResources: []any{
				newTestCredentialRequest(t, "cred-1", func(cred *coreapi.SystemAdminCredentialRequest) {
					earlier := metav1.NewTime(fixedClockTime.Add(-2 * time.Hour))
					cred.Status.DeletionTimestamp = &earlier
				}),
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cred, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRequests(testClusterName).Get(ctx, "cred-1")
				require.NoError(t, err)
				require.NotNil(t, cred.Status.DeletionTimestamp)
				assert.True(t, fixedClockTime.Add(-2*time.Hour).Equal(cred.Status.DeletionTimestamp.Time))
			},
		},
		{
			name:            "marks credential revocation for deletion",
			existingCluster: readyForDeletionCluster(t),
			extraResources:  []any{newTestCredentialRevocation(t, "revoke-1")},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				revocation, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRevocations(testClusterName).Get(ctx, "revoke-1")
				require.NoError(t, err)
				require.NotNil(t, revocation.Status.DeletionTimestamp)
				assert.True(t, fixedClockTime.Equal(revocation.Status.DeletionTimestamp.Time))
			},
		},
		{
			name:            "skips credential revocation already marked for deletion",
			existingCluster: readyForDeletionCluster(t),
			extraResources: []any{
				newTestCredentialRevocation(t, "revoke-1", func(rev *coreapi.SystemAdminCredentialRevocation) {
					earlier := metav1.NewTime(fixedClockTime.Add(-2 * time.Hour))
					rev.Status.DeletionTimestamp = &earlier
				}),
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				revocation, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRevocations(testClusterName).Get(ctx, "revoke-1")
				require.NoError(t, err)
				require.NotNil(t, revocation.Status.DeletionTimestamp)
				assert.True(t, fixedClockTime.Add(-2*time.Hour).Equal(revocation.Status.DeletionTimestamp.Time))
			},
		},
		{
			name:            "marks both credential requests and revocations",
			existingCluster: readyForDeletionCluster(t),
			extraResources: []any{
				newTestCredentialRequest(t, "cred-1"),
				newTestCredentialRevocation(t, "revoke-1"),
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cred, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRequests(testClusterName).Get(ctx, "cred-1")
				require.NoError(t, err)
				require.NotNil(t, cred.Status.DeletionTimestamp)

				revocation, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRevocations(testClusterName).Get(ctx, "revoke-1")
				require.NoError(t, err)
				require.NotNil(t, revocation.Status.DeletionTimestamp)
			},
		},
		{
			name:            "no credential requests or revocations -- no-op",
			existingCluster: readyForDeletionCluster(t),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			resources := []any{}
			if tc.existingCluster != nil {
				resources = append(resources, tc.existingCluster)
			}
			resources = append(resources, tc.extraResources...)

			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			clustersForLister := []*coreapi.HCPOpenShiftCluster{}
			if tc.existingCluster != nil {
				clustersForLister = append(clustersForLister, tc.existingCluster)
			}

			credsForLister := []*coreapi.SystemAdminCredentialRequest{}
			revocationsForLister := []*coreapi.SystemAdminCredentialRevocation{}
			for _, r := range tc.extraResources {
				switch v := r.(type) {
				case *coreapi.SystemAdminCredentialRequest:
					credsForLister = append(credsForLister, v)
				case *coreapi.SystemAdminCredentialRevocation:
					revocationsForLister = append(revocationsForLister, v)
				}
			}

			syncer := &clusterCredentialDeletionMarkerController{
				clock:                      clocktesting.NewFakePassiveClock(fixedClockTime),
				clusterLister:              &corelistertesting.SliceClusterLister{Clusters: clustersForLister},
				credentialRequestLister:    &corelistertesting.SliceSystemAdminCredentialRequestLister{CredentialRequests: credsForLister},
				credentialRevocationLister: &corelistertesting.SliceSystemAdminCredentialRevocationLister{CredentialRevocations: revocationsForLister},
				resourcesDBClient:          mockResourcesDBClient,
			}

			err = syncer.SyncOnce(ctx, testKey)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tc.verifyDB != nil {
				tc.verifyDB(t, ctx, mockResourcesDBClient)
			}
		})
	}
}

func TestClusterCredentialDeletionMarkerController_NeedsWork(t *testing.T) {
	fixedClockTime := time.Now().UTC().Truncate(time.Second)

	testCases := []struct {
		name    string
		cluster *coreapi.HCPOpenShiftCluster
		want    bool
	}{
		{
			name:    "feature flag false",
			cluster: newTestClusterWithOldDeletionApproach(t, nil),
			want:    false,
		},
		{
			name:    "no DeletionTimestamp",
			cluster: newTestClusterWithNewDeletionApproach(t, nil),
			want:    false,
		},
		{
			name: "DeletionTimestamp set",
			cluster: newTestClusterWithNewDeletionApproach(t, func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime}
			}),
			want: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			controller := &clusterCredentialDeletionMarkerController{}
			assert.Equal(t, tc.want, controller.NeedsWork(tc.cluster))
		})
	}
}

func TestDeletePreconditionAllCredentialRequestsDeleted(t *testing.T) {
	testKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	testCases := []struct {
		name      string
		resources []any
		wantMet   bool
	}{
		{
			name:    "no credential requests -- precondition met",
			wantMet: true,
		},
		{
			name:      "credential request exists -- precondition not met",
			resources: []any{newTestCredentialRequest(t, "cred-1")},
			wantMet:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			cluster := newTestClusterWithNewDeletionApproach(t, nil)
			resources := append([]any{cluster}, tc.resources...)
			mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			met, err := deletePreconditionAllCredentialRequestsDeleted(ctx, mockDB, testKey)
			require.NoError(t, err)
			assert.Equal(t, tc.wantMet, met)
		})
	}
}

func TestDeletePreconditionAllCredentialRevocationsDeleted(t *testing.T) {
	testKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	testCases := []struct {
		name      string
		resources []any
		wantMet   bool
	}{
		{
			name:    "no credential revocations -- precondition met",
			wantMet: true,
		},
		{
			name:      "credential revocation exists -- precondition not met",
			resources: []any{newTestCredentialRevocation(t, "revoke-1")},
			wantMet:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			cluster := newTestClusterWithNewDeletionApproach(t, nil)
			resources := append([]any{cluster}, tc.resources...)
			mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			met, err := deletePreconditionAllCredentialRevocationsDeleted(ctx, mockDB, testKey)
			require.NoError(t, err)
			assert.Equal(t, tc.wantMet, met)
		})
	}
}
