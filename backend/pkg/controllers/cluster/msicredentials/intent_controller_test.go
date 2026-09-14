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

package msicredentials

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

func TestDesiredMSIBasedOperatorCredentials(t *testing.T) {
	t.Parallel()

	identityID := testIdentityResourceID()
	syncer := &msiBasedOperatorCredentialsIntentSyncer{}
	spc := testServiceProviderCluster(nil)

	t.Run("resolved operator is added as PendingConfigure", func(t *testing.T) {
		t.Parallel()
		got, err := syncer.desiredMSIBasedOperatorCredentials(
			map[string]*azcorearm.ResourceID{testOperatorName: identityID},
			spc,
			nil,
		)
		require.NoError(t, err)
		require.Contains(t, got, testOperatorName)
		assert.Equal(t, coreapi.MSIBasedOperatorCredentialsPhasePendingConfigure, got[testOperatorName].Phase)
		assert.Equal(t, testClientID, got[testOperatorName].ObservedIdentity.ClientID)
		assert.Equal(t, testPrincipalID, got[testOperatorName].ObservedIdentity.PrincipalID)
		assert.Equal(t, testTenantID, got[testOperatorName].ObservedIdentity.TenantID)
	})

	t.Run("unresolved operator is not added", func(t *testing.T) {
		t.Parallel()
		unready := testServiceProviderCluster(nil)
		unready.Status.ManagedIdentityDetails = nil
		got, err := syncer.desiredMSIBasedOperatorCredentials(
			map[string]*azcorearm.ResourceID{testOperatorName: identityID},
			unready,
			nil,
		)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("operator that left the set is PendingDeconfigure", func(t *testing.T) {
		t.Parallel()
		current := map[string]*coreapi.MSIBasedOperatorCredentialsStatus{
			testOperatorName: {
				Phase: coreapi.MSIBasedOperatorCredentialsPhaseConfigured,
				ObservedIdentity: coreapi.MSIBasedOperatorCredentialsObservedIdentity{
					ResourceID:  identityID,
					ClientID:    testClientID,
					PrincipalID: testPrincipalID,
				},
			},
		}
		got, err := syncer.desiredMSIBasedOperatorCredentials(nil, spc, current)
		require.NoError(t, err)
		require.Contains(t, got, testOperatorName)
		assert.Equal(t, coreapi.MSIBasedOperatorCredentialsPhasePendingDeconfigure, got[testOperatorName].Phase)
	})

	t.Run("identity change on the same operator is PendingConfigure not deconfigure", func(t *testing.T) {
		t.Parallel()
		current := map[string]*coreapi.MSIBasedOperatorCredentialsStatus{
			testOperatorName: {
				Phase: coreapi.MSIBasedOperatorCredentialsPhaseConfigured,
				ObservedIdentity: coreapi.MSIBasedOperatorCredentialsObservedIdentity{
					ResourceID:  identityID,
					ClientID:    "old-client",
					PrincipalID: testPrincipalID,
				},
			},
		}
		got, err := syncer.desiredMSIBasedOperatorCredentials(
			map[string]*azcorearm.ResourceID{testOperatorName: identityID},
			spc,
			current,
		)
		require.NoError(t, err)
		require.Contains(t, got, testOperatorName)
		assert.Equal(t, coreapi.MSIBasedOperatorCredentialsPhasePendingConfigure, got[testOperatorName].Phase)
		assert.Equal(t, testClientID, got[testOperatorName].ObservedIdentity.ClientID)
	})

	t.Run("hardcoded identity metadata is used when dataplane metadata is absent", func(t *testing.T) {
		t.Parallel()
		got, err := syncer.desiredMSIBasedOperatorCredentials(
			map[string]*azcorearm.ResourceID{testOperatorName: identityID},
			testServiceProviderClusterWithHardcodedIdentity(nil),
			nil,
		)
		require.NoError(t, err)
		require.Contains(t, got, testOperatorName)
		assert.Equal(t, coreapi.MSIBasedOperatorCredentialsPhasePendingConfigure, got[testOperatorName].Phase)
		assert.Equal(t, testClientID, got[testOperatorName].ObservedIdentity.ClientID)
		assert.Equal(t, testPrincipalID, got[testOperatorName].ObservedIdentity.PrincipalID)
		assert.Equal(t, testTenantID, got[testOperatorName].ObservedIdentity.TenantID)
	})
}

func TestMSIBasedOperatorCredentialsIntentSyncOnce(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cluster := testCluster()
	spc := testServiceProviderCluster(nil)

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc})
	require.NoError(t, err)

	syncer := &msiBasedOperatorCredentialsIntentSyncer{
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:            mockResourcesDB,
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.Contains(t, updated.Status.MSIBasedOperatorCredentials, testOperatorName)
	assert.Equal(t, coreapi.MSIBasedOperatorCredentialsPhasePendingConfigure, updated.Status.MSIBasedOperatorCredentials[testOperatorName].Phase)
}

func TestClusterServiceGone(t *testing.T) {
	t.Parallel()

	syncer := &msiBasedOperatorCredentialsIntentSyncer{}
	cluster := testCluster()
	assert.False(t, syncer.clusterServiceGone(cluster))

	now := metav1.Now()
	cluster.ServiceProviderProperties.DeletionTimestamp = &now
	cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &now
	cluster.ServiceProviderProperties.ClusterServiceID = nil
	assert.True(t, syncer.clusterServiceGone(cluster))
}
