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

	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/msi-dataplane/pkg/dataplane"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
)

func TestMSIBasedOperatorCredentialsNeedsWork(t *testing.T) {
	t.Parallel()

	syncer := &msiBasedOperatorCredentialsSyncer{clock: clocktesting.NewFakePassiveClock(testNow())}
	cluster := testCluster()

	assert.False(t, syncer.needsWork(cluster, testServiceProviderCluster(nil)))

	spc := testServiceProviderCluster(nil)
	spc.Status.MSIBasedOperatorCredentials = map[string]*coreapi.MSIBasedOperatorCredentialsStatus{
		testOperatorName: {Phase: coreapi.MSIBasedOperatorCredentialsPhasePendingConfigure},
	}
	assert.True(t, syncer.needsWork(cluster, spc))

	spc.Status.MSIBasedOperatorCredentials[testOperatorName] = &coreapi.MSIBasedOperatorCredentialsStatus{
		Phase:               coreapi.MSIBasedOperatorCredentialsPhaseConfigured,
		EarliestRecheckTime: futureTime(),
	}
	assert.False(t, syncer.needsWork(cluster, spc))

	spc.Status.MSIBasedOperatorCredentials[testOperatorName].EarliestRecheckTime = pastTime()
	assert.True(t, syncer.needsWork(cluster, spc))

	deleting := testCluster()
	deleting.ServiceProviderProperties.DeletionTimestamp = pastTime()
	assert.False(t, syncer.needsWork(deleting, spc), "Configured is skipped while the cluster is being deleted")

	spc.Status.MSIBasedOperatorCredentials[testOperatorName] = &coreapi.MSIBasedOperatorCredentialsStatus{
		Phase: coreapi.MSIBasedOperatorCredentialsPhasePendingDeconfigure,
	}
	assert.True(t, syncer.needsWork(deleting, spc))
}

func TestMSIBasedOperatorCredentialsSyncOnceConfigure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cluster := testCluster()
	roleAssignmentIDs := expectedOperatorRoleAssignmentIDs(t)
	spc := testServiceProviderCluster(roleAssignmentIDs)
	spc.Status.MSIBasedOperatorCredentials = map[string]*coreapi.MSIBasedOperatorCredentialsStatus{
		testOperatorName: {
			Phase: coreapi.MSIBasedOperatorCredentialsPhasePendingConfigure,
			ObservedIdentity: coreapi.MSIBasedOperatorCredentialsObservedIdentity{
				ResourceID:  testIdentityResourceID(),
				ClientID:    testClientID,
				PrincipalID: testPrincipalID,
				TenantID:    testTenantID,
			},
		},
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc})
	require.NoError(t, err)

	kvClient := &fakeKeyVaultSecretsClient{}
	identityID := testIdentityResourceID()
	syncer := &msiBasedOperatorCredentialsSyncer{
		clock:                        clocktesting.NewFakePassiveClock(testNow()),
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		managementClusterLister: &fleetlistertesting.SliceManagementClusterLister{
			ManagementClusters: []*fleetapi.ManagementCluster{testManagementCluster()},
		},
		resourcesDBClient: mockResourcesDB,
		fpaMIdataplaneClientBuilder: &fakeFPAMIDataplaneClientBuilder{
			client: &fakeManagedIdentitiesDataplaneClient{
				creds: &dataplane.ManagedIdentityCredentials{
					ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{testUACredentials(identityID.String())},
				},
			},
		},
		keyVaultSecretsClientBuilder:  &fakeKeyVaultSecretsClientBuilder{client: kvClient},
		clusterScopedIdentitiesConfig: testConfig(),
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)
	require.Len(t, kvClient.setCalls, 1)
	assert.Equal(t, expectedSecretName(), kvClient.setCalls[0].name)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	status := updated.Status.MSIBasedOperatorCredentials[testOperatorName]
	require.NotNil(t, status)
	assert.Equal(t, coreapi.MSIBasedOperatorCredentialsPhaseConfigured, status.Phase)
	assert.Equal(t, expectedSecretName(), status.SecretName)
	assert.Empty(t, status.PendingSecretName)
	assert.Equal(t, testKeyVaultURL, status.KeyVaultURL)
}

func TestMSIBasedOperatorCredentialsSyncOnceWaitsForRoleAssignments(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cluster := testCluster()
	spc := testServiceProviderCluster(nil)
	spc.Status.MSIBasedOperatorCredentials = map[string]*coreapi.MSIBasedOperatorCredentialsStatus{
		testOperatorName: {
			Phase: coreapi.MSIBasedOperatorCredentialsPhasePendingConfigure,
			ObservedIdentity: coreapi.MSIBasedOperatorCredentialsObservedIdentity{
				ResourceID:  testIdentityResourceID(),
				ClientID:    testClientID,
				PrincipalID: testPrincipalID,
				TenantID:    testTenantID,
			},
		},
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc})
	require.NoError(t, err)

	kvClient := &fakeKeyVaultSecretsClient{}
	syncer := &msiBasedOperatorCredentialsSyncer{
		clock:                        clocktesting.NewFakePassiveClock(testNow()),
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		managementClusterLister: &fleetlistertesting.SliceManagementClusterLister{
			ManagementClusters: []*fleetapi.ManagementCluster{testManagementCluster()},
		},
		resourcesDBClient: mockResourcesDB,
		fpaMIdataplaneClientBuilder: &fakeFPAMIDataplaneClientBuilder{
			client: &fakeManagedIdentitiesDataplaneClient{},
		},
		keyVaultSecretsClientBuilder:  &fakeKeyVaultSecretsClientBuilder{client: kvClient},
		clusterScopedIdentitiesConfig: testConfig(),
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)
	assert.Empty(t, kvClient.setCalls)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	status := updated.Status.MSIBasedOperatorCredentials[testOperatorName]
	require.NotNil(t, status)
	assert.Equal(t, coreapi.MSIBasedOperatorCredentialsPhasePendingConfigure, status.Phase)
	assert.Equal(t, expectedSecretName(), status.PendingSecretName)
	assert.Empty(t, status.SecretName)
}

func TestMSIBasedOperatorCredentialsSyncOnceDeconfigure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cluster := testCluster()
	spc := testServiceProviderCluster(nil)
	spc.Status.MSIBasedOperatorCredentials = map[string]*coreapi.MSIBasedOperatorCredentialsStatus{
		testOperatorName: {
			Phase:       coreapi.MSIBasedOperatorCredentialsPhasePendingDeconfigure,
			SecretName:  expectedSecretName(),
			KeyVaultURL: testKeyVaultURL,
			ObservedIdentity: coreapi.MSIBasedOperatorCredentialsObservedIdentity{
				ResourceID:  testIdentityResourceID(),
				ClientID:    testClientID,
				PrincipalID: testPrincipalID,
				TenantID:    testTenantID,
			},
		},
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc})
	require.NoError(t, err)

	kvClient := &fakeKeyVaultSecretsClient{}
	syncer := &msiBasedOperatorCredentialsSyncer{
		clock:                        clocktesting.NewFakePassiveClock(testNow()),
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		managementClusterLister: &fleetlistertesting.SliceManagementClusterLister{
			ManagementClusters: []*fleetapi.ManagementCluster{testManagementCluster()},
		},
		resourcesDBClient:             mockResourcesDB,
		keyVaultSecretsClientBuilder:  &fakeKeyVaultSecretsClientBuilder{client: kvClient},
		clusterScopedIdentitiesConfig: testConfig(),
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)
	require.Equal(t, []string{expectedSecretName()}, kvClient.deleteCalls)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	status := updated.Status.MSIBasedOperatorCredentials[testOperatorName]
	require.NotNil(t, status)
	assert.Equal(t, coreapi.MSIBasedOperatorCredentialsPhaseDeconfigured, status.Phase)
	assert.Empty(t, status.SecretName)
	assert.Empty(t, status.PendingSecretName)
}

func TestMSIBasedOperatorCredentialsSyncOnceConfigureHardcodedIdentity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cluster := testCluster()
	roleAssignmentIDs := expectedOperatorRoleAssignmentIDs(t)
	spc := testServiceProviderClusterWithHardcodedIdentity(roleAssignmentIDs)
	spc.Status.MSIBasedOperatorCredentials = map[string]*coreapi.MSIBasedOperatorCredentialsStatus{
		testOperatorName: {
			Phase: coreapi.MSIBasedOperatorCredentialsPhasePendingConfigure,
			ObservedIdentity: coreapi.MSIBasedOperatorCredentialsObservedIdentity{
				ResourceID:  testIdentityResourceID(),
				ClientID:    testClientID,
				PrincipalID: testPrincipalID,
				TenantID:    testTenantID,
			},
		},
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc})
	require.NoError(t, err)

	kvClient := &fakeKeyVaultSecretsClient{}
	syncer := &msiBasedOperatorCredentialsSyncer{
		clock:                        clocktesting.NewFakePassiveClock(testNow()),
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		managementClusterLister: &fleetlistertesting.SliceManagementClusterLister{
			ManagementClusters: []*fleetapi.ManagementCluster{testManagementCluster()},
		},
		resourcesDBClient:             mockResourcesDB,
		hardcodedIdentity:             testHardcodedIdentity(),
		cloudConfiguration:            &cloud.AzurePublic,
		keyVaultSecretsClientBuilder:  &fakeKeyVaultSecretsClientBuilder{client: kvClient},
		clusterScopedIdentitiesConfig: testConfig(),
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	})
	require.NoError(t, err)
	require.Len(t, kvClient.setCalls, 1)
	assert.Equal(t, expectedSecretName(), kvClient.setCalls[0].name)
	require.NotNil(t, kvClient.setCalls[0].parameters.Value)
	assert.Contains(t, *kvClient.setCalls[0].parameters.Value, testClientID)
	assert.Contains(t, *kvClient.setCalls[0].parameters.Value, testHardcodedSecret)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	status := updated.Status.MSIBasedOperatorCredentials[testOperatorName]
	require.NotNil(t, status)
	assert.Equal(t, coreapi.MSIBasedOperatorCredentialsPhaseConfigured, status.Phase)
	assert.Equal(t, expectedSecretName(), status.SecretName)
	assert.Empty(t, status.PendingSecretName)
}
