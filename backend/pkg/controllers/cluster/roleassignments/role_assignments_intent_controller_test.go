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

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// testConfig returns the real cluster-scoped identities config used to enumerate
// operator role definitions.
func testConfig() *azure.ClusterScopedIdentitiesConfig {
	return azure.NewClusterScopedIdentitiesConfig(azure.RoleDefinitionConfigSetNameDev)
}

var testHCPClusterKey = controllerutils.HCPClusterKey{
	SubscriptionID:    testSubscriptionID,
	ResourceGroupName: testResourceGroupName,
	HCPClusterName:    testClusterName,
}

func TestDesiredRoleAssignments(t *testing.T) {
	t.Parallel()

	cluster := newTestCluster(false)
	readySPC := newTestServiceProviderClusterWithIdentityDetails(t)
	unreadySPC := newTestServiceProviderCluster(t, true, false, false, nil)

	syncer := newTestRoleAssignmentIntentSyncer(nil, true)
	desiredKeys := testDesiredRoleAssignmentKeys(t)
	require.Len(t, desiredKeys, 3)

	t.Run("identities ready adds every desired key", func(t *testing.T) {
		t.Parallel()
		got, err := syncer.desiredRoleAssignmentsV2(cluster, readySPC, nil)
		require.NoError(t, err)
		require.Len(t, got, len(desiredKeys))
		for _, key := range desiredKeys {
			require.Contains(t, got, key)
			require.NotNil(t, got[key])
			assert.Nil(t, got[key].DeconfigureTimestamp)
			assert.Nil(t, got[key].AzureResource)
			require.NotNil(t, got[key].TargetIdentity)
			assert.Equal(t, key.PrincipalID, got[key].TargetIdentity.PrincipalID)
			assert.NotEmpty(t, got[key].TargetIdentity.ClientID)
			assert.NotEmpty(t, got[key].TargetIdentity.TenantID)
		}
		assert.Equal(t, "dp-client-cp", got[desiredKeys[0]].TargetIdentity.ClientID)
		assert.Equal(t, "arm-client-dp", got[desiredKeys[1]].TargetIdentity.ClientID)
		assert.Equal(t, "dp-client-smi", got[desiredKeys[2]].TargetIdentity.ClientID)
	})

	t.Run("identities not ready and empty existing adds nothing", func(t *testing.T) {
		t.Parallel()
		got, err := syncer.desiredRoleAssignmentsV2(cluster, unreadySPC, nil)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("old MSI maps resolved without ManagedIdentityDetails adds nothing", func(t *testing.T) {
		t.Parallel()
		spc := newTestServiceProviderCluster(t, true, true, true, nil)
		got, err := syncer.desiredRoleAssignmentsV2(cluster, spc, nil)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("control plane ignores ARM and uses dataplane", func(t *testing.T) {
		t.Parallel()
		spc := newTestServiceProviderClusterWithIdentityDetails(t)
		got, err := syncer.desiredRoleAssignmentsV2(cluster, spc, nil)
		require.NoError(t, err)
		require.Contains(t, got, desiredKeys[0])
		require.NotNil(t, got[desiredKeys[0]].TargetIdentity)
		assert.Equal(t, "dp-client-cp", got[desiredKeys[0]].TargetIdentity.ClientID)
		assert.Equal(t, testControlPlanePrincipalID, got[desiredKeys[0]].TargetIdentity.PrincipalID)
		assert.NotContains(t, got, coreapi.RoleAssignmentKey{
			ResourceID:               desiredKeys[0].ResourceID,
			PrincipalID:              testControlPlaneARMPrincipalID,
			RoleDefinitionResourceID: desiredKeys[0].RoleDefinitionResourceID,
		})
	})

	t.Run("service managed identity uses dataplane even when ARM is present", func(t *testing.T) {
		t.Parallel()
		spc := newTestServiceProviderClusterWithIdentityDetails(t)
		key := strings.ToLower(testServiceManagedIdentityID)
		spc.Status.ManagedIdentityDetails[key].MetadataFromARMUserAssignedIdentitiesAPI = &coreapi.IdentityMetadataValue{
			ClientID:    ptr.To("arm-client-smi"),
			PrincipalID: ptr.To("arm-principal-smi"),
			TenantID:    ptr.To("arm-tenant-smi"),
		}
		got, err := syncer.desiredRoleAssignmentsV2(cluster, spc, nil)
		require.NoError(t, err)
		require.Contains(t, got, desiredKeys[2])
		require.NotNil(t, got[desiredKeys[2]].TargetIdentity)
		assert.Equal(t, "dp-client-smi", got[desiredKeys[2]].TargetIdentity.ClientID)
		assert.Equal(t, testServiceManagedIdentityPrincipalID, got[desiredKeys[2]].TargetIdentity.PrincipalID)
		assert.NotContains(t, got, coreapi.RoleAssignmentKey{
			ResourceID:               desiredKeys[2].ResourceID,
			PrincipalID:              "arm-principal-smi",
			RoleDefinitionResourceID: desiredKeys[2].RoleDefinitionResourceID,
		})
	})

	t.Run("nil service managed identity is an error", func(t *testing.T) {
		t.Parallel()
		clusterWithoutSMI := newTestCluster(false)
		clusterWithoutSMI.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = nil
		_, err := syncer.desiredRoleAssignmentsV2(clusterWithoutSMI, readySPC, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil identity Resource ID for service managed identity")
	})

	t.Run("ARM-only control plane identity does not add control plane keys", func(t *testing.T) {
		t.Parallel()
		spc := newTestServiceProviderClusterWithIdentityDetails(t)
		key := strings.ToLower(testControlPlaneIdentityID)
		spc.Status.ManagedIdentityDetails[key].MetadataFromManagedIdentitiesDataplaneService = nil
		spc.Status.ManagedIdentityDetails[key].MetadataFromHardcodedIdentity = nil
		got, err := syncer.desiredRoleAssignmentsV2(cluster, spc, nil)
		require.NoError(t, err)
		assert.NotContains(t, got, desiredKeys[0])
		require.Contains(t, got, desiredKeys[1])
	})

	t.Run("control plane uses hardcoded identity when the controller is wired for it", func(t *testing.T) {
		t.Parallel()
		hardcodedSyncer := newTestRoleAssignmentIntentSyncer(nil, false)
		spc := newTestServiceProviderClusterWithIdentityDetails(t)
		key := strings.ToLower(testControlPlaneIdentityID)
		spc.Status.ManagedIdentityDetails[key].MetadataFromHardcodedIdentity = &coreapi.IdentityMetadataValue{
			ClientID:    ptr.To("hardcoded-client-cp"),
			PrincipalID: ptr.To(testControlPlanePrincipalID),
			TenantID:    ptr.To("hardcoded-tenant-cp"),
		}
		got, err := hardcodedSyncer.desiredRoleAssignmentsV2(cluster, spc, nil)
		require.NoError(t, err)
		require.Contains(t, got, desiredKeys[0])
		require.NotNil(t, got[desiredKeys[0]].TargetIdentity)
		assert.Equal(t, "hardcoded-client-cp", got[desiredKeys[0]].TargetIdentity.ClientID)
		assert.Equal(t, testControlPlanePrincipalID, got[desiredKeys[0]].TargetIdentity.PrincipalID)
	})

	t.Run("nil dataplane source waits instead of using hardcoded", func(t *testing.T) {
		t.Parallel()
		spc := newTestServiceProviderClusterWithIdentityDetails(t)
		key := strings.ToLower(testControlPlaneIdentityID)
		spc.Status.ManagedIdentityDetails[key].MetadataFromManagedIdentitiesDataplaneService = nil
		spc.Status.ManagedIdentityDetails[key].MetadataFromHardcodedIdentity = &coreapi.IdentityMetadataValue{
			ClientID:    ptr.To("hardcoded-client-cp"),
			PrincipalID: ptr.To(testControlPlanePrincipalID),
			TenantID:    ptr.To("hardcoded-tenant-cp"),
		}
		got, err := syncer.desiredRoleAssignmentsV2(cluster, spc, nil)
		require.NoError(t, err)
		assert.NotContains(t, got, desiredKeys[0])
		require.Contains(t, got, desiredKeys[1])
	})

	t.Run("unresolved dataplane source does not fall back to hardcoded", func(t *testing.T) {
		t.Parallel()
		spc := newTestServiceProviderClusterWithIdentityDetails(t)
		key := strings.ToLower(testControlPlaneIdentityID)
		spc.Status.ManagedIdentityDetails[key].MetadataFromManagedIdentitiesDataplaneService = &coreapi.IdentityMetadataValue{}
		spc.Status.ManagedIdentityDetails[key].MetadataFromHardcodedIdentity = &coreapi.IdentityMetadataValue{
			ClientID:    ptr.To("hardcoded-client-cp"),
			PrincipalID: ptr.To(testControlPlanePrincipalID),
			TenantID:    ptr.To("hardcoded-tenant-cp"),
		}
		got, err := syncer.desiredRoleAssignmentsV2(cluster, spc, nil)
		require.NoError(t, err)
		assert.NotContains(t, got, desiredKeys[0])
		require.Contains(t, got, desiredKeys[1])
	})

	t.Run("same UAMI as control plane and data plane adds both principals", func(t *testing.T) {
		t.Parallel()
		sharedCluster := newTestCluster(false)
		sharedCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators[testDataPlaneOperatorName] = mustParseResourceID(testControlPlaneIdentityID)
		got, err := syncer.desiredRoleAssignmentsV2(sharedCluster, readySPC, nil)
		require.NoError(t, err)
		cpKey := desiredKeys[0]
		dpKey := coreapi.RoleAssignmentKey{
			ResourceID:               strings.ToLower(testControlPlaneIdentityID),
			PrincipalID:              testControlPlaneARMPrincipalID,
			RoleDefinitionResourceID: desiredKeys[1].RoleDefinitionResourceID,
		}
		require.Contains(t, got, cpKey)
		require.Contains(t, got, dpKey)
		assert.Equal(t, "dp-client-cp", got[cpKey].TargetIdentity.ClientID)
		assert.Equal(t, testControlPlanePrincipalID, got[cpKey].TargetIdentity.PrincipalID)
		assert.Equal(t, "arm-client-cp", got[dpKey].TargetIdentity.ClientID)
		assert.Equal(t, testControlPlaneARMPrincipalID, got[dpKey].TargetIdentity.PrincipalID)
	})

	t.Run("identities not ready leaves a still-required key as-is", func(t *testing.T) {
		t.Parallel()
		current := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			desiredKeys[0]: {AzureResource: testRoleAssignmentAzureResource(t)},
		}
		got, err := syncer.desiredRoleAssignmentsV2(cluster, unreadySPC, current)
		require.NoError(t, err)
		require.Contains(t, got, desiredKeys[0])
		assert.True(t, got[desiredKeys[0]].Configured())
	})

	t.Run("ensured key that is still required stays configured", func(t *testing.T) {
		t.Parallel()
		current := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			desiredKeys[0]: {
				AzureResource:  testRoleAssignmentAzureResource(t),
				TargetIdentity: mustResolveControlPlaneRoleAssignmentTargetIdentity(t, syncer, readySPC, mustParseResourceID(testControlPlaneIdentityID)),
			},
		}
		got, err := syncer.desiredRoleAssignmentsV2(cluster, readySPC, current)
		require.NoError(t, err)
		assert.True(t, got[desiredKeys[0]].Configured())
		assert.False(t, got[desiredKeys[1]].Configured())
		assert.Nil(t, got[desiredKeys[1]].DeconfigureTimestamp)
	})

	t.Run("draining key that is required again clears DeconfigureTimestamp", func(t *testing.T) {
		t.Parallel()
		stamped := metav1.NewTime(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
		current := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			desiredKeys[0]: {
				DeconfigureTimestamp: &stamped,
				AzureResource:        testRoleAssignmentAzureResource(t),
			},
		}
		got, err := syncer.desiredRoleAssignmentsV2(cluster, readySPC, current)
		require.NoError(t, err)
		assert.Nil(t, got[desiredKeys[0]].DeconfigureTimestamp)
		assert.True(t, got[desiredKeys[0]].Configured())
	})

	t.Run("key that left the desired set is stamped for deconfigure", func(t *testing.T) {
		t.Parallel()
		stale := coreapi.RoleAssignmentKey{
			ResourceID:               "stale-identity",
			PrincipalID:              "stale-principal",
			RoleDefinitionResourceID: "/providers/Microsoft.Authorization/roleDefinitions/00000000-0000-0000-0000-000000000000",
		}
		current := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			stale: {AzureResource: testRoleAssignmentAzureResource(t)},
		}
		got, err := syncer.desiredRoleAssignmentsV2(cluster, readySPC, current)
		require.NoError(t, err)
		require.NotNil(t, got[stale].DeconfigureTimestamp)
		assert.False(t, got[stale].Configured())
	})

	t.Run("already draining leftover keeps its DeconfigureTimestamp", func(t *testing.T) {
		t.Parallel()
		stale := coreapi.RoleAssignmentKey{
			ResourceID:               "stale-identity",
			PrincipalID:              "stale-principal",
			RoleDefinitionResourceID: "/providers/Microsoft.Authorization/roleDefinitions/00000000-0000-0000-0000-000000000000",
		}
		stamp := metav1.NewTime(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
		current := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			stale: {
				DeconfigureTimestamp: &stamp,
				AzureResource:        testRoleAssignmentAzureResource(t),
			},
		}
		got, err := syncer.desiredRoleAssignmentsV2(cluster, readySPC, current)
		require.NoError(t, err)
		require.NotNil(t, got[stale].DeconfigureTimestamp)
		assert.True(t, got[stale].DeconfigureTimestamp.Equal(&stamp))
	})

	t.Run("never-ensured leftover is dropped", func(t *testing.T) {
		t.Parallel()
		stale := coreapi.RoleAssignmentKey{
			ResourceID:               "stale-identity",
			PrincipalID:              "stale-principal",
			RoleDefinitionResourceID: "/providers/Microsoft.Authorization/roleDefinitions/00000000-0000-0000-0000-000000000000",
		}
		current := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			stale: {},
		}
		got, err := syncer.desiredRoleAssignmentsV2(cluster, readySPC, current)
		require.NoError(t, err)
		assert.NotContains(t, got, stale)
	})

	t.Run("nil existing status is an error", func(t *testing.T) {
		t.Parallel()
		_, err := syncer.desiredRoleAssignmentsV2(cluster, readySPC, map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			desiredKeys[0]: nil,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil status")
	})

	t.Run("principal ID change stamps the old key and adds the new key", func(t *testing.T) {
		t.Parallel()
		oldKey := desiredKeys[0]
		oldKey.PrincipalID = "old-principal"
		current := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			oldKey: {
				AzureResource: testRoleAssignmentAzureResource(t),
				TargetIdentity: &coreapi.RoleAssignmentTargetIdentity{
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
		require.NotNil(t, old.DeconfigureTimestamp)
		require.NotNil(t, old.TargetIdentity)
		assert.Equal(t, "old-client", old.TargetIdentity.ClientID)
		assert.Equal(t, "old-principal", old.TargetIdentity.PrincipalID)
		newStatus := got[desiredKeys[0]]
		require.NotNil(t, newStatus)
		assert.Nil(t, newStatus.DeconfigureTimestamp)
		assert.False(t, newStatus.Configured())
		require.NotNil(t, newStatus.TargetIdentity)
		assert.Equal(t, desiredKeys[0].PrincipalID, newStatus.TargetIdentity.PrincipalID)
	})

	t.Run("client ID change on the same key updates TargetIdentity without deconfigure", func(t *testing.T) {
		t.Parallel()
		key := desiredKeys[0]
		current := map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus{
			key: {
				AzureResource: testRoleAssignmentAzureResource(t),
				TargetIdentity: &coreapi.RoleAssignmentTargetIdentity{
					ClientID:    "old-client",
					TenantID:    "arm-tenant-cp",
					PrincipalID: key.PrincipalID,
				},
			},
		}
		got, err := syncer.desiredRoleAssignmentsV2(cluster, readySPC, current)
		require.NoError(t, err)
		require.Contains(t, got, key)
		assert.Nil(t, got[key].DeconfigureTimestamp)
		assert.True(t, got[key].Configured())
		require.NotNil(t, got[key].TargetIdentity)
		assert.Equal(t, "dp-client-cp", got[key].TargetIdentity.ClientID)
		assert.Equal(t, key.PrincipalID, got[key].TargetIdentity.PrincipalID)
	})
}

func TestRoleAssignmentIntentSyncOncePersistsDesiredKeys(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	cluster := newTestCluster(false)
	serviceProviderCluster := newTestServiceProviderClusterWithIdentityDetails(t)

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	syncer := newTestRoleAssignmentIntentSyncer(mockResourcesDB, true)
	err = syncer.SyncOnce(ctx, testHCPClusterKey)
	require.NoError(t, err)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.Len(t, updated.Status.RoleAssignments, 3)
	for _, key := range testDesiredRoleAssignmentKeys(t) {
		require.Contains(t, updated.Status.RoleAssignments, key)
		assert.Nil(t, updated.Status.RoleAssignments[key].DeconfigureTimestamp)
		assert.False(t, updated.Status.RoleAssignments[key].Configured())
	}
}

func TestRoleAssignmentIntentSyncOnceSkipsClusterDeletion(t *testing.T) {
	t.Parallel()

	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	cluster := newTestCluster(true)
	serviceProviderCluster := newTestServiceProviderClusterWithIdentityDetails(t)

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	syncer := newTestRoleAssignmentIntentSyncer(mockResourcesDB, true)
	err = syncer.SyncOnce(ctx, testHCPClusterKey)
	require.NoError(t, err)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	assert.Empty(t, updated.Status.RoleAssignments)
}

func TestResolveControlPlaneRoleAssignmentTargetIdentity(t *testing.T) {
	t.Parallel()

	identityID := mustParseResourceID(testControlPlaneIdentityID)
	dataplaneSyncer := newTestRoleAssignmentIntentSyncer(nil, true)
	hardcodedSyncer := newTestRoleAssignmentIntentSyncer(nil, false)

	t.Run("dataplane metadata is used even when ARM is resolved", func(t *testing.T) {
		t.Parallel()
		spc := newTestServiceProviderClusterWithIdentityDetails(t)
		target, ok, err := dataplaneSyncer.resolveMSIBasedRoleAssignmentTargetIdentity(spc, identityID)
		require.NoError(t, err)
		require.True(t, ok)
		require.NotNil(t, target)
		assert.Equal(t, "dp-client-cp", target.ClientID)
		assert.Equal(t, "dp-tenant-cp", target.TenantID)
		assert.Equal(t, testControlPlanePrincipalID, target.PrincipalID)
	})

	t.Run("hardcoded identity is used when the controller is wired for it", func(t *testing.T) {
		t.Parallel()
		spc := newTestServiceProviderClusterWithIdentityDetails(t)
		key := strings.ToLower(testControlPlaneIdentityID)
		spc.Status.ManagedIdentityDetails[key].MetadataFromHardcodedIdentity = &coreapi.IdentityMetadataValue{
			ClientID:    ptr.To("hardcoded-client-cp"),
			PrincipalID: ptr.To(testControlPlanePrincipalID),
			TenantID:    ptr.To("hardcoded-tenant-cp"),
		}
		target, ok, err := hardcodedSyncer.resolveMSIBasedRoleAssignmentTargetIdentity(spc, identityID)
		require.NoError(t, err)
		require.True(t, ok)
		require.NotNil(t, target)
		assert.Equal(t, "hardcoded-client-cp", target.ClientID)
		assert.Equal(t, "hardcoded-tenant-cp", target.TenantID)
		assert.Equal(t, testControlPlanePrincipalID, target.PrincipalID)
	})

	t.Run("nil dataplane pointer waits instead of using hardcoded", func(t *testing.T) {
		t.Parallel()
		spc := newTestServiceProviderClusterWithIdentityDetails(t)
		key := strings.ToLower(testControlPlaneIdentityID)
		spc.Status.ManagedIdentityDetails[key].MetadataFromManagedIdentitiesDataplaneService = nil
		spc.Status.ManagedIdentityDetails[key].MetadataFromHardcodedIdentity = &coreapi.IdentityMetadataValue{
			ClientID:    ptr.To("hardcoded-client-cp"),
			PrincipalID: ptr.To(testControlPlanePrincipalID),
			TenantID:    ptr.To("hardcoded-tenant-cp"),
		}
		target, ok, err := dataplaneSyncer.resolveMSIBasedRoleAssignmentTargetIdentity(spc, identityID)
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Nil(t, target)
	})

	t.Run("unresolved dataplane source does not fall back to hardcoded", func(t *testing.T) {
		t.Parallel()
		spc := newTestServiceProviderClusterWithIdentityDetails(t)
		key := strings.ToLower(testControlPlaneIdentityID)
		spc.Status.ManagedIdentityDetails[key].MetadataFromManagedIdentitiesDataplaneService = &coreapi.IdentityMetadataValue{
			RetrievalError: ptr.To("simulated dataplane Get failure"),
		}
		spc.Status.ManagedIdentityDetails[key].MetadataFromHardcodedIdentity = &coreapi.IdentityMetadataValue{
			ClientID:    ptr.To("hardcoded-client-cp"),
			PrincipalID: ptr.To(testControlPlanePrincipalID),
			TenantID:    ptr.To("hardcoded-tenant-cp"),
		}
		target, ok, err := dataplaneSyncer.resolveMSIBasedRoleAssignmentTargetIdentity(spc, identityID)
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Nil(t, target)
	})

	t.Run("nil hardcoded pointer waits instead of using dataplane", func(t *testing.T) {
		t.Parallel()
		spc := newTestServiceProviderClusterWithIdentityDetails(t)
		spc.Status.ManagedIdentityDetails[strings.ToLower(testControlPlaneIdentityID)].MetadataFromHardcodedIdentity = nil
		target, ok, err := hardcodedSyncer.resolveMSIBasedRoleAssignmentTargetIdentity(spc, identityID)
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Nil(t, target)
	})

	t.Run("ARM-only metadata is unresolved", func(t *testing.T) {
		t.Parallel()
		spc := newTestServiceProviderClusterWithIdentityDetails(t)
		key := strings.ToLower(testControlPlaneIdentityID)
		spc.Status.ManagedIdentityDetails[key].MetadataFromManagedIdentitiesDataplaneService = nil
		spc.Status.ManagedIdentityDetails[key].MetadataFromHardcodedIdentity = nil
		target, ok, err := dataplaneSyncer.resolveMSIBasedRoleAssignmentTargetIdentity(spc, identityID)
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Nil(t, target)
	})

	t.Run("missing ManagedIdentityDetails is unresolved", func(t *testing.T) {
		t.Parallel()
		spc := newTestServiceProviderClusterWithIdentityDetails(t)
		spc.Status.ManagedIdentityDetails = nil
		target, ok, err := dataplaneSyncer.resolveMSIBasedRoleAssignmentTargetIdentity(spc, identityID)
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Nil(t, target)
	})

	t.Run("nil ManagedIdentityDetails entry is an error", func(t *testing.T) {
		t.Parallel()
		spc := newTestServiceProviderClusterWithIdentityDetails(t)
		spc.Status.ManagedIdentityDetails[strings.ToLower(testControlPlaneIdentityID)] = nil
		_, _, err := dataplaneSyncer.resolveMSIBasedRoleAssignmentTargetIdentity(spc, identityID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil metadata entry")
	})
}

func TestResolveDataPlaneRoleAssignmentTargetIdentity(t *testing.T) {
	t.Parallel()

	identityID := mustParseResourceID(testDataPlaneIdentityID)

	t.Run("ARM metadata fills ClientID TenantID PrincipalID", func(t *testing.T) {
		t.Parallel()
		spc := newTestServiceProviderClusterWithIdentityDetails(t)
		target, ok, err := resolveDataPlaneRoleAssignmentTargetIdentity(spc, identityID)
		require.NoError(t, err)
		require.True(t, ok)
		require.NotNil(t, target)
		assert.Equal(t, "arm-client-dp", target.ClientID)
		assert.Equal(t, "arm-tenant-dp", target.TenantID)
		assert.Equal(t, testDataPlanePrincipalID, target.PrincipalID)
	})

	t.Run("dataplane and hardcoded metadata are ignored", func(t *testing.T) {
		t.Parallel()
		spc := newTestServiceProviderClusterWithIdentityDetails(t)
		key := strings.ToLower(testDataPlaneIdentityID)
		spc.Status.ManagedIdentityDetails[key].MetadataFromARMUserAssignedIdentitiesAPI = nil
		spc.Status.ManagedIdentityDetails[key].MetadataFromManagedIdentitiesDataplaneService = &coreapi.IdentityMetadataValue{
			ClientID:    ptr.To("dp-client-dp"),
			PrincipalID: ptr.To("dp-dataplane-principal"),
			TenantID:    ptr.To("dp-tenant-dp"),
		}
		spc.Status.ManagedIdentityDetails[key].MetadataFromHardcodedIdentity = &coreapi.IdentityMetadataValue{
			ClientID:    ptr.To("hardcoded-client-dp"),
			PrincipalID: ptr.To("hardcoded-principal-dp"),
			TenantID:    ptr.To("hardcoded-tenant-dp"),
		}
		target, ok, err := resolveDataPlaneRoleAssignmentTargetIdentity(spc, identityID)
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Nil(t, target)
	})

	t.Run("missing ManagedIdentityDetails is unresolved", func(t *testing.T) {
		t.Parallel()
		spc := newTestServiceProviderClusterWithIdentityDetails(t)
		spc.Status.ManagedIdentityDetails = nil
		target, ok, err := resolveDataPlaneRoleAssignmentTargetIdentity(spc, identityID)
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Nil(t, target)
	})
}

func mustResolveControlPlaneRoleAssignmentTargetIdentity(t *testing.T, syncer *clusterRoleAssignmentIntentSyncer, spc *coreapi.ServiceProviderCluster, identityID *azcorearm.ResourceID) *coreapi.RoleAssignmentTargetIdentity {
	t.Helper()
	target, ok, err := syncer.resolveMSIBasedRoleAssignmentTargetIdentity(spc, identityID)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, target)
	return target
}

func newTestRoleAssignmentIntentSyncer(resourcesDB *corecosmosstoragetesting.MockResourcesDBClient, managedIdentitiesDataPlaneServiceAvailable bool) *clusterRoleAssignmentIntentSyncer {
	syncer := &clusterRoleAssignmentIntentSyncer{
		clock:                         clocktesting.NewFakePassiveClock(time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)),
		clusterScopedIdentitiesConfig: testConfig(),
		managedIdentitiesDataPlaneServiceAvailable: managedIdentitiesDataPlaneServiceAvailable,
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
	spc := newTestServiceProviderCluster(t, true, false, false, nil)
	cpKey := strings.ToLower(testControlPlaneIdentityID)
	dpKey := strings.ToLower(testDataPlaneIdentityID)
	smiKey := strings.ToLower(testServiceManagedIdentityID)
	spc.Status.ManagedIdentityDetails = map[string]*coreapi.ManagedIdentityMetadata{
		cpKey: {
			MetadataFromARMUserAssignedIdentitiesAPI: &coreapi.IdentityMetadataValue{
				ClientID:    ptr.To("arm-client-cp"),
				PrincipalID: ptr.To(testControlPlaneARMPrincipalID),
				TenantID:    ptr.To("arm-tenant-cp"),
			},
			MetadataFromManagedIdentitiesDataplaneService: &coreapi.IdentityMetadataValue{
				ClientID:    ptr.To("dp-client-cp"),
				PrincipalID: ptr.To(testControlPlanePrincipalID),
				TenantID:    ptr.To("dp-tenant-cp"),
			},
		},
		dpKey: {
			MetadataFromARMUserAssignedIdentitiesAPI: &coreapi.IdentityMetadataValue{
				ClientID:    ptr.To("arm-client-dp"),
				PrincipalID: ptr.To(testDataPlanePrincipalID),
				TenantID:    ptr.To("arm-tenant-dp"),
			},
		},
		smiKey: {
			MetadataFromManagedIdentitiesDataplaneService: &coreapi.IdentityMetadataValue{
				ClientID:    ptr.To("dp-client-smi"),
				PrincipalID: ptr.To(testServiceManagedIdentityPrincipalID),
				TenantID:    ptr.To("dp-tenant-smi"),
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
	smiRoleDefs := config.ServiceManagedIdentity.RoleDefinitionsResourceIDs()
	require.NotEmpty(t, smiRoleDefs)
	return []coreapi.RoleAssignmentKey{
		{
			ResourceID:               strings.ToLower(testControlPlaneIdentityID),
			PrincipalID:              testControlPlanePrincipalID,
			RoleDefinitionResourceID: cpRoleDefs[0].String(),
		},
		{
			ResourceID:               strings.ToLower(testDataPlaneIdentityID),
			PrincipalID:              testDataPlanePrincipalID,
			RoleDefinitionResourceID: dpRoleDefs[0].String(),
		},
		{
			ResourceID:               strings.ToLower(testServiceManagedIdentityID),
			PrincipalID:              testServiceManagedIdentityPrincipalID,
			RoleDefinitionResourceID: smiRoleDefs[0].String(),
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

func testRoleAssignmentAzureResource(t *testing.T) *azcorearm.ResourceID {
	t.Helper()
	return mustParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testManagedRGName + "/providers/Microsoft.Authorization/roleAssignments/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
}
