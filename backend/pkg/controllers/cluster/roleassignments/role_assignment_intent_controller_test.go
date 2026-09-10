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
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestDesiredRoleAssignmentsV2(t *testing.T) {
	t.Parallel()

	cluster := newTestCluster(false)
	readySPC := newTestServiceProviderClusterWithIdentityDetails(t)
	unreadySPC := newTestServiceProviderCluster(t, true, false, false, coreapi.AzureMultiReference{})

	syncer := newTestRoleAssignmentIntentSyncer(nil)
	desiredKeys := testDesiredRoleAssignmentKeys(t)
	require.Len(t, desiredKeys, 2)

	t.Run("identities ready adds every desired key as PendingConfigure", func(t *testing.T) {
		t.Parallel()
		got, err := syncer.desiredRoleAssignmentsV2(cluster, readySPC, nil)
		require.NoError(t, err)
		require.Len(t, got, len(desiredKeys))
		for _, key := range desiredKeys {
			require.Contains(t, got, key)
			require.NotNil(t, got[key])
			assert.Equal(t, coreapi.RoleAssignmentPhasePendingConfigure, got[key].Phase)
			require.NotNil(t, got[key].ObservedIdentity)
			assert.Equal(t, key.PrincipalID, got[key].ObservedIdentity.PrincipalID)
			assert.NotEmpty(t, got[key].ObservedIdentity.ClientID)
			assert.NotEmpty(t, got[key].ObservedIdentity.TenantID)
		}
	})

	t.Run("identities not ready and empty existing adds nothing", func(t *testing.T) {
		t.Parallel()
		got, err := syncer.desiredRoleAssignmentsV2(cluster, unreadySPC, nil)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("identities not ready leaves a still-required Configured key as-is", func(t *testing.T) {
		t.Parallel()
		current := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			desiredKeys[0]: {Phase: coreapi.RoleAssignmentPhaseConfigured},
		}
		got, err := syncer.desiredRoleAssignmentsV2(cluster, unreadySPC, current)
		require.NoError(t, err)
		require.Contains(t, got, desiredKeys[0])
		assert.Equal(t, coreapi.RoleAssignmentPhaseConfigured, got[desiredKeys[0]].Phase)
	})

	t.Run("configured key that is still required is left Configured", func(t *testing.T) {
		t.Parallel()
		current := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			desiredKeys[0]: {
				Phase:            coreapi.RoleAssignmentPhaseConfigured,
				ObservedIdentity: resolveRoleAssignmentObservedIdentity(readySPC, mustParseResourceID(testControlPlaneIdentityID), testControlPlanePrincipalID),
			},
		}
		got, err := syncer.desiredRoleAssignmentsV2(cluster, readySPC, current)
		require.NoError(t, err)
		assert.Equal(t, coreapi.RoleAssignmentPhaseConfigured, got[desiredKeys[0]].Phase)
		assert.Equal(t, coreapi.RoleAssignmentPhasePendingConfigure, got[desiredKeys[1]].Phase)
	})

	t.Run("deconfigured key that is required again becomes PendingConfigure", func(t *testing.T) {
		t.Parallel()
		current := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			desiredKeys[0]: {Phase: coreapi.RoleAssignmentPhaseDeconfigured},
		}
		got, err := syncer.desiredRoleAssignmentsV2(cluster, readySPC, current)
		require.NoError(t, err)
		assert.Equal(t, coreapi.RoleAssignmentPhasePendingConfigure, got[desiredKeys[0]].Phase)
		assert.Nil(t, got[desiredKeys[0]].DeconfigureTimestamp)
	})

	t.Run("key that left the desired set is marked PendingDeconfigure", func(t *testing.T) {
		t.Parallel()
		stale := coreapi.RoleAssignmentKey{
			ResourceID:       "stale-identity",
			PrincipalID:      "stale-principal",
			RoleDefinitionResourceID: "/providers/Microsoft.Authorization/roleDefinitions/00000000-0000-0000-0000-000000000000",
		}
		current := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			stale: {Phase: coreapi.RoleAssignmentPhaseConfigured},
		}
		got, err := syncer.desiredRoleAssignmentsV2(cluster, readySPC, current)
		require.NoError(t, err)
		assert.Equal(t, coreapi.RoleAssignmentPhasePendingDeconfigure, got[stale].Phase)
		require.NotNil(t, got[stale].DeconfigureTimestamp)
	})

	t.Run("already PendingDeconfigure leftover stays PendingDeconfigure", func(t *testing.T) {
		t.Parallel()
		stale := coreapi.RoleAssignmentKey{
			ResourceID:       "stale-identity",
			PrincipalID:      "stale-principal",
			RoleDefinitionResourceID: "/providers/Microsoft.Authorization/roleDefinitions/00000000-0000-0000-0000-000000000000",
		}
		stamp := metav1.NewTime(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
		current := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			stale: {
				Phase:                coreapi.RoleAssignmentPhasePendingDeconfigure,
				DeconfigureTimestamp: &stamp,
			},
		}
		got, err := syncer.desiredRoleAssignmentsV2(cluster, readySPC, current)
		require.NoError(t, err)
		assert.Equal(t, coreapi.RoleAssignmentPhasePendingDeconfigure, got[stale].Phase)
		require.NotNil(t, got[stale].DeconfigureTimestamp)
		assert.True(t, got[stale].DeconfigureTimestamp.Equal(&stamp))
	})

	t.Run("nil existing status is an error", func(t *testing.T) {
		t.Parallel()
		_, err := syncer.desiredRoleAssignmentsV2(cluster, readySPC, map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			desiredKeys[0]: nil,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil status")
	})

	t.Run("principal ID change marks the old key PendingDeconfigure and adds the new key", func(t *testing.T) {
		t.Parallel()
		oldKey := desiredKeys[0]
		oldKey.PrincipalID = "old-principal"
		current := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			oldKey: {
				Phase: coreapi.RoleAssignmentPhaseConfigured,
				ObservedIdentity: &coreapi.RoleAssignmentObservedIdentity{
					ClientID:    "old-client",
					TenantID:    "old-tenant",
					PrincipalID: "old-principal",
				},
			},
		}
		got, err := syncer.desiredRoleAssignmentsV2(cluster, readySPC, current)
		require.NoError(t, err)
		old := got[oldKey]
		require.NotNil(t, old)
		assert.Equal(t, coreapi.RoleAssignmentPhasePendingDeconfigure, old.Phase)
		require.NotNil(t, old.DeconfigureTimestamp)
		require.NotNil(t, old.ObservedIdentity)
		assert.Equal(t, "old-client", old.ObservedIdentity.ClientID)
		assert.Equal(t, "old-principal", old.ObservedIdentity.PrincipalID)
		newStatus := got[desiredKeys[0]]
		require.NotNil(t, newStatus)
		assert.Equal(t, coreapi.RoleAssignmentPhasePendingConfigure, newStatus.Phase)
		require.NotNil(t, newStatus.ObservedIdentity)
		assert.Equal(t, desiredKeys[0].PrincipalID, newStatus.ObservedIdentity.PrincipalID)
	})

	t.Run("client ID change on the same key sets PendingConfigure without a new key", func(t *testing.T) {
		t.Parallel()
		key := desiredKeys[0]
		current := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			key: {
				Phase: coreapi.RoleAssignmentPhaseConfigured,
				ObservedIdentity: &coreapi.RoleAssignmentObservedIdentity{
					ClientID:    "old-client",
					TenantID:    "arm-tenant-cp",
					PrincipalID: key.PrincipalID,
				},
			},
		}
		got, err := syncer.desiredRoleAssignmentsV2(cluster, readySPC, current)
		require.NoError(t, err)
		require.Contains(t, got, key)
		assert.Equal(t, coreapi.RoleAssignmentPhasePendingConfigure, got[key].Phase)
		assert.Nil(t, got[key].DeconfigureTimestamp)
		require.NotNil(t, got[key].ObservedIdentity)
		assert.NotEqual(t, "old-client", got[key].ObservedIdentity.ClientID)
		assert.Equal(t, key.PrincipalID, got[key].ObservedIdentity.PrincipalID)
	})
}

func TestRoleAssignmentIntentSyncOncePersistsPendingConfigure(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	cluster := newTestCluster(false)
	serviceProviderCluster := newTestServiceProviderClusterWithIdentityDetails(t)

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	syncer := newTestRoleAssignmentIntentSyncer(mockResourcesDB)
	err = syncer.SyncOnce(ctx, testHCPClusterKey)
	require.NoError(t, err)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.Len(t, updated.Status.RoleAssignmentsV2, 2)
	for _, key := range testDesiredRoleAssignmentKeys(t) {
		require.Contains(t, updated.Status.RoleAssignmentsV2, key)
		assert.Equal(t, coreapi.RoleAssignmentPhasePendingConfigure, updated.Status.RoleAssignmentsV2[key].Phase)
	}
}

func TestRoleAssignmentIntentSyncOnceSkipsClusterDeletion(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	cluster := newTestCluster(true)
	serviceProviderCluster := newTestServiceProviderClusterWithIdentityDetails(t)

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	syncer := newTestRoleAssignmentIntentSyncer(mockResourcesDB)
	err = syncer.SyncOnce(ctx, testHCPClusterKey)
	require.NoError(t, err)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	assert.Empty(t, updated.Status.RoleAssignmentsV2)
}

func TestResolveRoleAssignmentObservedIdentity(t *testing.T) {
	t.Parallel()

	identityID := mustParseResourceID(testControlPlaneIdentityID)
	principalID := testControlPlanePrincipalID

	t.Run("ARM metadata with matching principal fills ClientID TenantID PrincipalID", func(t *testing.T) {
		t.Parallel()
		spc := newTestServiceProviderClusterWithIdentityDetails(t)
		observed := resolveRoleAssignmentObservedIdentity(spc, identityID, principalID)
		require.NotNil(t, observed)
		assert.Equal(t, "arm-client-cp", observed.ClientID)
		assert.Equal(t, "arm-tenant-cp", observed.TenantID)
		assert.Equal(t, principalID, observed.PrincipalID)
	})

	t.Run("ARM principal mismatch keeps MSI ClientID and leaves TenantID empty", func(t *testing.T) {
		t.Parallel()
		spc := newTestServiceProviderClusterWithIdentityDetails(t)
		key := strings.ToLower(testControlPlaneIdentityID)
		spc.Status.ManagedIdentityDetails[key].MetadataFromARMUserAssignedIdentitiesAPI.PrincipalID = ptr.To("other-principal")
		observed := resolveRoleAssignmentObservedIdentity(spc, identityID, principalID)
		require.NotNil(t, observed)
		assert.Equal(t, "cp-client", observed.ClientID)
		assert.Empty(t, observed.TenantID)
		assert.Equal(t, principalID, observed.PrincipalID)
	})

	t.Run("missing ARM metadata uses MSI ClientID", func(t *testing.T) {
		t.Parallel()
		spc := newTestServiceProviderClusterWithIdentityDetails(t)
		spc.Status.ManagedIdentityDetails = nil
		observed := resolveRoleAssignmentObservedIdentity(spc, identityID, principalID)
		require.NotNil(t, observed)
		assert.Equal(t, "cp-client", observed.ClientID)
		assert.Empty(t, observed.TenantID)
		assert.Equal(t, principalID, observed.PrincipalID)
	})
}

func newTestRoleAssignmentIntentSyncer(resourcesDB *corecosmosstoragetesting.MockResourcesDBClient) *clusterRoleAssignmentIntentSyncer {
	syncer := &clusterRoleAssignmentIntentSyncer{
		clock:                         clocktesting.NewFakePassiveClock(time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)),
		clusterScopedIdentitiesConfig: testConfig(),
	}
	if resourcesDB != nil {
		syncer.clusterLister = &corelistertesting.DBClusterLister{ResourcesDBClient: resourcesDB}
		syncer.serviceProviderClusterLister = &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: resourcesDB}
		syncer.resourcesDBClient = resourcesDB
	}
	return syncer
}

func newTestServiceProviderClusterWithIdentityDetails(t *testing.T) *coreapi.ServiceProviderCluster {
	t.Helper()
	spc := newTestServiceProviderCluster(t, true, true, true, coreapi.AzureMultiReference{})
	cpKey := strings.ToLower(testControlPlaneIdentityID)
	dpKey := strings.ToLower(testDataPlaneIdentityID)
	spc.Status.MSIManagedIdentities.ControlPlaneOperatorsIdentities[cpKey].ClientID = ptr.To("cp-client")
	spc.Status.DataPlaneOperatorsManagedIdentities.Identities[dpKey].ClientID = ptr.To("dp-client")
	spc.Status.ManagedIdentityDetails = map[string]*coreapi.ManagedIdentityMetadata{
		cpKey: {
			MetadataFromARMUserAssignedIdentitiesAPI: &coreapi.IdentityMetadataValue{
				ClientID:    ptr.To("arm-client-cp"),
				PrincipalID: ptr.To(testControlPlanePrincipalID),
				TenantID:    ptr.To("arm-tenant-cp"),
			},
		},
		dpKey: {
			MetadataFromARMUserAssignedIdentitiesAPI: &coreapi.IdentityMetadataValue{
				ClientID:    ptr.To("arm-client-dp"),
				PrincipalID: ptr.To(testDataPlanePrincipalID),
				TenantID:    ptr.To("arm-tenant-dp"),
			},
		},
	}
	return spc
}

func testDesiredRoleAssignmentKeys(t *testing.T) []coreapi.RoleAssignmentKey {
	t.Helper()
	config := testConfig()
	cpRoleDefs := config.ControlPlaneOperatorsIdentities[azure.ClusterOperatorIdentifier(testControlPlaneOperatorName)].RoleDefinitionsResourceIDs()
	require.NotEmpty(t, cpRoleDefs)
	dpRoleDefs := config.DataPlaneOperatorsIdentities[azure.ClusterOperatorIdentifier(testDataPlaneOperatorName)].RoleDefinitionsResourceIDs()
	require.NotEmpty(t, dpRoleDefs)
	return []coreapi.RoleAssignmentKey{
		{
			ResourceID:       strings.ToLower(testControlPlaneIdentityID),
			PrincipalID:      testControlPlanePrincipalID,
			RoleDefinitionResourceID: cpRoleDefs[0].String(),
		},
		{
			ResourceID:       strings.ToLower(testDataPlaneIdentityID),
			PrincipalID:      testDataPlanePrincipalID,
			RoleDefinitionResourceID: dpRoleDefs[0].String(),
		},
	}
}

func mustParseResourceID(id string) *azcorearm.ResourceID {
	parsed, err := azcorearm.ParseResourceID(id)
	if err != nil {
		panic(err)
	}
	return parsed
}
