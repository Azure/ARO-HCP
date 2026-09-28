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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

func TestDesiredDenyAssignmentsOverManagedResourceGroup(t *testing.T) {
	t.Parallel()

	cluster := newTestCluster()
	readySPC := newTestSPC()
	unreadySPC := newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
		spc.Status.ManagedIdentityDetails = nil
	})

	syncer := &clusterDenyAssignmentIntentSyncer{
		clock: clocktesting.NewFakePassiveClock(time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)),
	}
	requiredTypes := RequiredDenyAssignmentTypes(cluster)
	require.NotEmpty(t, requiredTypes)

	t.Run("identities ready adds every required type with desired identity rows", func(t *testing.T) {
		t.Parallel()
		got, err := syncer.desiredDenyAssignmentsOverManagedResourceGroup(cluster, readySPC, nil)
		require.NoError(t, err)
		require.Len(t, got, len(requiredTypes))
		for denyAssignmentType := range requiredTypes {
			require.Contains(t, got, denyAssignmentType)
			require.NotNil(t, got[denyAssignmentType])
			assert.Nil(t, got[denyAssignmentType].DeconfigureTimestamp)
			assertDesiredIdentityRows(t, cluster, readySPC, denyAssignmentType, got[denyAssignmentType].ExcludedIdentities, false)
		}
	})

	t.Run("identities not ready and empty existing only adds types that need no identities", func(t *testing.T) {
		t.Parallel()
		got, err := syncer.desiredDenyAssignmentsOverManagedResourceGroup(cluster, unreadySPC, nil)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Contains(t, got, denyAssignmentSuffixDenyAllOtherRPs)
		assert.Nil(t, got[denyAssignmentSuffixDenyAllOtherRPs].DeconfigureTimestamp)
		assert.Empty(t, got[denyAssignmentSuffixDenyAllOtherRPs].ExcludedIdentities)
	})

	t.Run("identities not ready leaves a still-required type as-is", func(t *testing.T) {
		t.Parallel()
		azureResource := testDenyAssignmentResourceID("resources-uuid")
		current := map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: {AzureResource: azureResource},
		}
		got, err := syncer.desiredDenyAssignmentsOverManagedResourceGroup(cluster, unreadySPC, current)
		require.NoError(t, err)
		require.Contains(t, got, denyAssignmentSuffixResources)
		assert.True(t, controllerutil.ResourceIDsEqual(azureResource, got[denyAssignmentSuffixResources].AzureResource))
		assert.Nil(t, got[denyAssignmentSuffixResources].DeconfigureTimestamp)
	})

	t.Run("required type with missing identity rows gets desired rows inserted", func(t *testing.T) {
		t.Parallel()
		current := map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: {AzureResource: testDenyAssignmentResourceID("resources-uuid")},
		}
		got, err := syncer.desiredDenyAssignmentsOverManagedResourceGroup(cluster, readySPC, current)
		require.NoError(t, err)
		assertDesiredIdentityRows(t, cluster, readySPC, denyAssignmentSuffixResources, got[denyAssignmentSuffixResources].ExcludedIdentities, false)
		assertDesiredIdentityRows(t, cluster, readySPC, denyAssignmentSuffixCompute, got[denyAssignmentSuffixCompute].ExcludedIdentities, false)
	})

	t.Run("draining type that is required again clears type DeconfigureTimestamp", func(t *testing.T) {
		t.Parallel()
		stamped := metav1.NewTime(time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC))
		current := map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: {
				DeconfigureTimestamp: &stamped,
				AzureResource:        testDenyAssignmentResourceID("resources-uuid"),
			},
		}
		got, err := syncer.desiredDenyAssignmentsOverManagedResourceGroup(cluster, readySPC, current)
		require.NoError(t, err)
		assert.Nil(t, got[denyAssignmentSuffixResources].DeconfigureTimestamp)
		assertDesiredIdentityRows(t, cluster, readySPC, denyAssignmentSuffixResources, got[denyAssignmentSuffixResources].ExcludedIdentities, false)
	})

	t.Run("type that left the definition set is stamped for deconfigure when Azure IDs are tracked", func(t *testing.T) {
		t.Parallel()
		current := map[string]*coreapi.DenyAssignmentStatus{
			"stale-type-not-in-definitions": {AzureResource: testDenyAssignmentResourceID("stale-uuid")},
		}
		got, err := syncer.desiredDenyAssignmentsOverManagedResourceGroup(cluster, readySPC, current)
		require.NoError(t, err)
		require.Contains(t, got, "stale-type-not-in-definitions")
		require.NotNil(t, got["stale-type-not-in-definitions"].DeconfigureTimestamp)
	})

	t.Run("already draining stale type keeps its DeconfigureTimestamp", func(t *testing.T) {
		t.Parallel()
		stamped := metav1.NewTime(time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC))
		current := map[string]*coreapi.DenyAssignmentStatus{
			"stale-type-not-in-definitions": {
				DeconfigureTimestamp: &stamped,
				AzureResource:        testDenyAssignmentResourceID("stale-uuid"),
			},
		}
		got, err := syncer.desiredDenyAssignmentsOverManagedResourceGroup(cluster, unreadySPC, current)
		require.NoError(t, err)
		require.NotNil(t, got["stale-type-not-in-definitions"].DeconfigureTimestamp)
		assert.Equal(t, stamped, *got["stale-type-not-in-definitions"].DeconfigureTimestamp)
	})

	t.Run("stale type with nothing tracked to delete is dropped", func(t *testing.T) {
		t.Parallel()
		current := map[string]*coreapi.DenyAssignmentStatus{
			"stale-type-not-in-definitions": {},
		}
		got, err := syncer.desiredDenyAssignmentsOverManagedResourceGroup(cluster, readySPC, current)
		require.NoError(t, err)
		assert.NotContains(t, got, "stale-type-not-in-definitions")
	})

	t.Run("nil existing status is an error", func(t *testing.T) {
		t.Parallel()
		_, err := syncer.desiredDenyAssignmentsOverManagedResourceGroup(cluster, readySPC, map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: nil,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil status")
	})

	t.Run("principal ID change stamps cooldown on the old key and inserts the new key", func(t *testing.T) {
		t.Parallel()
		capi := testIdentityResourceID("capi-azure")
		const oldPrincipal = "old-principal"
		newPrincipal := testPrincipalID(capi)
		observed := seedTestExcludedIdentities(cluster, readySPC, denyAssignmentSuffixResources)
		observed[oldPrincipal] = &coreapi.DenyAssignmentExcludedIdentityStatus{
			TargetIdentity: &coreapi.DenyAssignmentTargetIdentity{
				ResourceID:  capi,
				PrincipalID: oldPrincipal,
			},
			EnsuredIdentity: &coreapi.DenyAssignmentExcludedEnsuredIdentity{PrincipalID: oldPrincipal},
		}
		delete(observed, newPrincipal)
		current := map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: {
				AzureResource:      testDenyAssignmentResourceID("resources-uuid"),
				ExcludedIdentities: observed,
			},
		}
		got, err := syncer.desiredDenyAssignmentsOverManagedResourceGroup(cluster, readySPC, current)
		require.NoError(t, err)
		require.Contains(t, got, denyAssignmentSuffixResources)
		old := got[denyAssignmentSuffixResources].ExcludedIdentities[oldPrincipal]
		require.NotNil(t, old)
		require.NotNil(t, old.DeconfigureTimestamp)
		require.NotNil(t, old.EnsuredIdentity)
		assert.Equal(t, oldPrincipal, old.EnsuredIdentity.PrincipalID)
		require.Contains(t, got[denyAssignmentSuffixResources].ExcludedIdentities, newPrincipal)
		assert.Nil(t, got[denyAssignmentSuffixResources].ExcludedIdentities[newPrincipal].EnsuredIdentity)
		assert.Nil(t, got[denyAssignmentSuffixResources].ExcludedIdentities[newPrincipal].DeconfigureTimestamp)
		require.NotNil(t, got[denyAssignmentSuffixResources].ExcludedIdentities[newPrincipal].TargetIdentity)
		assert.Equal(t, newPrincipal, got[denyAssignmentSuffixResources].ExcludedIdentities[newPrincipal].TargetIdentity.PrincipalID)
	})

	t.Run("never-ensured identity that left is dropped without a cooldown", func(t *testing.T) {
		t.Parallel()
		capi := testIdentityResourceID("capi-azure")
		const oldPrincipal = "old-principal"
		observed := seedTestExcludedIdentities(cluster, readySPC, denyAssignmentSuffixResources)
		observed[oldPrincipal] = &coreapi.DenyAssignmentExcludedIdentityStatus{}
		delete(observed, testPrincipalID(capi))
		current := map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: {ExcludedIdentities: observed},
		}
		got, err := syncer.desiredDenyAssignmentsOverManagedResourceGroup(cluster, readySPC, current)
		require.NoError(t, err)
		assert.NotContains(t, got[denyAssignmentSuffixResources].ExcludedIdentities, oldPrincipal)
	})

	t.Run("ensured identities are left in place", func(t *testing.T) {
		t.Parallel()
		current := map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: {
				AzureResource:      testDenyAssignmentResourceID("resources-uuid"),
				ExcludedIdentities: seedTestExcludedIdentities(cluster, readySPC, denyAssignmentSuffixResources),
				EnsuredPermissions: seedTestEnsuredPermissions(cluster, denyAssignmentSuffixResources),
			},
		}
		got, err := syncer.desiredDenyAssignmentsOverManagedResourceGroup(cluster, readySPC, current)
		require.NoError(t, err)
		assert.Equal(t, current[denyAssignmentSuffixResources].ExcludedIdentities, got[denyAssignmentSuffixResources].ExcludedIdentities)
	})

	t.Run("coming back during cooldown clears DeconfigureTimestamp", func(t *testing.T) {
		t.Parallel()
		observed := seedTestExcludedIdentities(cluster, readySPC, denyAssignmentSuffixResources)
		capi := testIdentityResourceID("capi-azure")
		key := testPrincipalID(capi)
		stamped := metav1.NewTime(time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC))
		observed[key].DeconfigureTimestamp = &stamped
		current := map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: {
				AzureResource:      testDenyAssignmentResourceID("resources-uuid"),
				ExcludedIdentities: observed,
				EnsuredPermissions: seedTestEnsuredPermissions(cluster, denyAssignmentSuffixResources),
			},
		}
		got, err := syncer.desiredDenyAssignmentsOverManagedResourceGroup(cluster, readySPC, current)
		require.NoError(t, err)
		gotStatus := got[denyAssignmentSuffixResources].ExcludedIdentities[key]
		require.NotNil(t, gotStatus)
		assert.Nil(t, gotStatus.DeconfigureTimestamp)
		require.NotNil(t, gotStatus.EnsuredIdentity)
		assert.Equal(t, testPrincipalID(capi), gotStatus.EnsuredIdentity.PrincipalID)
	})
}

func TestClusterDenyAssignmentIntentSyncOnce(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cluster := newTestCluster()
	serviceProviderCluster := newTestSPC()

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	syncer := &clusterDenyAssignmentIntentSyncer{
		clock:                        clocktesting.NewFakePassiveClock(time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)),
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:            mockResourcesDB,
	}

	err = syncer.SyncOnce(ctx, testKey())
	require.NoError(t, err)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.NotEmpty(t, updated.Status.DenyAssignmentsOverManagedResourceGroup)
	assertDesiredIdentityRows(t, cluster, serviceProviderCluster, denyAssignmentSuffixResources, updated.Status.DenyAssignmentsOverManagedResourceGroup[denyAssignmentSuffixResources].ExcludedIdentities, false)
}

func TestClusterDenyAssignmentIntentSyncOnceSkipsClusterDeletion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := metav1.NewTime(time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC))
	cluster := newTestCluster(func(c *coreapi.Cluster) {
		c.ServiceProviderProperties.DeletionTimestamp = &now
	})
	serviceProviderCluster := newTestSPC()

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	syncer := &clusterDenyAssignmentIntentSyncer{
		clock:                        clocktesting.NewFakePassiveClock(time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)),
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:            mockResourcesDB,
	}

	err = syncer.SyncOnce(ctx, testKey())
	require.NoError(t, err)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	assert.Empty(t, updated.Status.DenyAssignmentsOverManagedResourceGroup)
}

func TestClusterDenyAssignmentIntentSyncOnceDoesNotDeconfigureWhenIdentitiesUnready(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cluster := newTestCluster()
	azureResource := testDenyAssignmentResourceID("resources-uuid")
	serviceProviderCluster := newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
		spc.Status.ManagedIdentityDetails = nil
		spc.Status.DenyAssignmentsOverManagedResourceGroup = map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: {AzureResource: azureResource},
		}
	})

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	syncer := &clusterDenyAssignmentIntentSyncer{
		clock:                        clocktesting.NewFakePassiveClock(time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)),
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDB},
		resourcesDBClient:            mockResourcesDB,
	}

	err = syncer.SyncOnce(ctx, testKey())
	require.NoError(t, err)

	updated, err := mockResourcesDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.Contains(t, updated.Status.DenyAssignmentsOverManagedResourceGroup, denyAssignmentSuffixResources)
	assert.True(t, controllerutil.ResourceIDsEqual(azureResource, updated.Status.DenyAssignmentsOverManagedResourceGroup[denyAssignmentSuffixResources].AzureResource))
	require.Contains(t, updated.Status.DenyAssignmentsOverManagedResourceGroup, denyAssignmentSuffixDenyAllOtherRPs)
	assert.Nil(t, updated.Status.DenyAssignmentsOverManagedResourceGroup[denyAssignmentSuffixDenyAllOtherRPs].DeconfigureTimestamp)
}

func TestDenyAssignmentIntentDesiredExcludedIdentitiesSource(t *testing.T) {
	t.Parallel()

	cluster := newTestCluster()
	definition := denyAssignmentDefinitionsByType(cluster)[denyAssignmentSuffixResources]
	require.NotNil(t, definition)
	capi := testIdentityResourceID("capi-azure")
	dpImage := testIdentityResourceID("dp-image-registry")
	capiKey := strings.ToLower(capi.String())
	dpKey := strings.ToLower(dpImage.String())

	spc := newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
		spc.Status.ManagedIdentityDetails[capiKey] = &coreapi.ManagedIdentityMetadata{
			ResourceID:                                    capi,
			MetadataFromARMUserAssignedIdentitiesAPI:      testIdentityMetadataValue("arm-principal"),
			MetadataFromManagedIdentitiesDataplaneService: testIdentityMetadataValue("dataplane-principal"),
			MetadataFromHardcodedIdentity:                 testIdentityMetadataValue("hardcoded-principal"),
		}
		spc.Status.ManagedIdentityDetails[dpKey] = &coreapi.ManagedIdentityMetadata{
			ResourceID:                                    dpImage,
			MetadataFromARMUserAssignedIdentitiesAPI:      testIdentityMetadataValue("dp-arm-principal"),
			MetadataFromManagedIdentitiesDataplaneService: testIdentityMetadataValue("dp-dataplane-principal"),
			MetadataFromHardcodedIdentity:                 testIdentityMetadataValue("dp-hardcoded-principal"),
		}
	})

	t.Run("dataplane available uses dataplane principal for control plane and ARM for data plane", func(t *testing.T) {
		t.Parallel()
		syncer := &clusterDenyAssignmentIntentSyncer{managedIdentitiesDataPlaneServiceAvailable: true}
		desired, unresolved, err := syncer.desiredExcludedIdentities(cluster, spc, definition)
		require.NoError(t, err)
		require.Empty(t, unresolved)
		assert.Contains(t, desired, "dataplane-principal")
		assert.Equal(t, capi, desired["dataplane-principal"].ResourceID)
		assert.NotContains(t, desired, "hardcoded-principal")
		assert.NotContains(t, desired, "arm-principal")
		assert.Contains(t, desired, "dp-arm-principal")
		assert.Equal(t, dpImage, desired["dp-arm-principal"].ResourceID)
		assert.NotContains(t, desired, "dp-dataplane-principal")
	})

	t.Run("dataplane unavailable uses hardcoded principal for control plane and ARM for data plane", func(t *testing.T) {
		t.Parallel()
		syncer := &clusterDenyAssignmentIntentSyncer{managedIdentitiesDataPlaneServiceAvailable: false}
		desired, unresolved, err := syncer.desiredExcludedIdentities(cluster, spc, definition)
		require.NoError(t, err)
		require.Empty(t, unresolved)
		assert.Contains(t, desired, "hardcoded-principal")
		assert.Equal(t, capi, desired["hardcoded-principal"].ResourceID)
		assert.NotContains(t, desired, "dataplane-principal")
		assert.NotContains(t, desired, "arm-principal")
		assert.Contains(t, desired, "dp-arm-principal")
	})

	t.Run("unresolved chosen source does not fall back", func(t *testing.T) {
		t.Parallel()
		unresolvedSPC := newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
			entry := spc.Status.ManagedIdentityDetails[capiKey]
			entry.MetadataFromManagedIdentitiesDataplaneService = &coreapi.IdentityMetadataValue{}
		})
		syncer := &clusterDenyAssignmentIntentSyncer{managedIdentitiesDataPlaneServiceAvailable: true}
		desired, unresolved, err := syncer.desiredExcludedIdentities(cluster, unresolvedSPC, definition)
		require.NoError(t, err)
		assert.Contains(t, unresolved, capiKey)
		assert.NotContains(t, desired, "hardcoded-principal")
		assert.NotContains(t, desired, "arm-principal")
	})
}

func testIdentityMetadataValue(principalID string) *coreapi.IdentityMetadataValue {
	return &coreapi.IdentityMetadataValue{
		ClientID:    ptr.To("client-" + principalID),
		PrincipalID: ptr.To(principalID),
		TenantID:    ptr.To("tenant-" + principalID),
	}
}

func assertDesiredIdentityRows(
	t *testing.T,
	cluster *coreapi.Cluster,
	spc *coreapi.ServiceProviderCluster,
	denyAssignmentType string,
	got map[string]*coreapi.DenyAssignmentExcludedIdentityStatus,
	expectEnsured bool,
) {
	t.Helper()
	definition := denyAssignmentDefinitionsByType(cluster)[denyAssignmentType]
	desired, unresolved, err := (&clusterDenyAssignmentIntentSyncer{}).desiredExcludedIdentities(cluster, spc, definition)
	require.NoError(t, err)
	require.Empty(t, unresolved)
	require.Len(t, got, len(desired))
	for principalID, target := range desired {
		require.Contains(t, got, principalID)
		require.NotNil(t, got[principalID])
		assert.Nil(t, got[principalID].DeconfigureTimestamp)
		assert.Equal(t, target, got[principalID].TargetIdentity)
		if expectEnsured {
			require.NotNil(t, got[principalID].EnsuredIdentity)
			assert.Equal(t, principalID, got[principalID].EnsuredIdentity.PrincipalID)
			continue
		}
		assert.Nil(t, got[principalID].EnsuredIdentity)
	}
}
