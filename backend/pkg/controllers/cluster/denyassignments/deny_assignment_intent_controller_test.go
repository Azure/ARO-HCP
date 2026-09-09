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
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

func TestDesiredDenyAssignmentsV2(t *testing.T) {
	t.Parallel()

	cluster := newTestCluster()
	requiredTypes := requiredDenyAssignmentTypes(cluster)
	require.NotEmpty(t, requiredTypes)

	readySPC := newTestSPC()
	unreadySPC := newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
		spc.Status.MSIManagedIdentities.ControlPlaneOperatorsIdentities = nil
		spc.Status.DataPlaneOperatorsManagedIdentities.Identities = nil
	})

	syncer := &clusterDenyAssignmentIntentSyncer{
		clock: clocktesting.NewFakePassiveClock(time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)),
	}

	t.Run("identities ready adds every required type as PendingConfigure", func(t *testing.T) {
		t.Parallel()
		got, err := syncer.desiredDenyAssignmentsV2(cluster, readySPC, nil)
		require.NoError(t, err)
		require.Len(t, got, len(requiredTypes))
		for denyAssignmentType := range requiredTypes {
			require.Contains(t, got, denyAssignmentType)
			require.NotNil(t, got[denyAssignmentType])
			assert.Equal(t, coreapi.DenyAssignmentPhasePendingConfigure, got[denyAssignmentType].Phase)
		}
		require.NotEmpty(t, got[denyAssignmentSuffixResources].ExcludedIdentities)
		for _, identityStatus := range got[denyAssignmentSuffixResources].ExcludedIdentities {
			require.NotNil(t, identityStatus.ObservedIdentity)
			assert.NotEmpty(t, identityStatus.ObservedIdentity.PrincipalID)
			assert.NotEmpty(t, identityStatus.ObservedIdentity.ClientID)
			assert.NotEmpty(t, identityStatus.ObservedIdentity.TenantID)
		}
	})

	t.Run("identities not ready and empty existing only adds types that need no identities", func(t *testing.T) {
		t.Parallel()
		got, err := syncer.desiredDenyAssignmentsV2(cluster, unreadySPC, nil)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Contains(t, got, denyAssignmentSuffixDenyAllOtherRPs)
		assert.Equal(t, coreapi.DenyAssignmentPhasePendingConfigure, got[denyAssignmentSuffixDenyAllOtherRPs].Phase)
		assert.Empty(t, got[denyAssignmentSuffixDenyAllOtherRPs].ExcludedIdentities)
	})

	t.Run("identities not ready leaves a still-required Configured type as-is", func(t *testing.T) {
		t.Parallel()
		current := map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: {Phase: coreapi.DenyAssignmentPhaseConfigured},
		}
		got, err := syncer.desiredDenyAssignmentsV2(cluster, unreadySPC, current)
		require.NoError(t, err)
		require.Contains(t, got, denyAssignmentSuffixResources)
		assert.Equal(t, coreapi.DenyAssignmentPhaseConfigured, got[denyAssignmentSuffixResources].Phase)
	})

	t.Run("configured type that is still required is left Configured", func(t *testing.T) {
		t.Parallel()
		current := map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: {Phase: coreapi.DenyAssignmentPhaseConfigured},
		}
		got, err := syncer.desiredDenyAssignmentsV2(cluster, readySPC, current)
		require.NoError(t, err)
		assert.Equal(t, coreapi.DenyAssignmentPhaseConfigured, got[denyAssignmentSuffixResources].Phase)
		assert.Equal(t, coreapi.DenyAssignmentPhasePendingConfigure, got[denyAssignmentSuffixCompute].Phase)
	})

	t.Run("deconfigured type that is required again becomes PendingConfigure", func(t *testing.T) {
		t.Parallel()
		current := map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: {Phase: coreapi.DenyAssignmentPhaseDeconfigured},
		}
		got, err := syncer.desiredDenyAssignmentsV2(cluster, readySPC, current)
		require.NoError(t, err)
		assert.Equal(t, coreapi.DenyAssignmentPhasePendingConfigure, got[denyAssignmentSuffixResources].Phase)
	})

	t.Run("pending deconfigure type that is required again becomes PendingConfigure", func(t *testing.T) {
		t.Parallel()
		current := map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: {Phase: coreapi.DenyAssignmentPhasePendingDeconfigure},
		}
		got, err := syncer.desiredDenyAssignmentsV2(cluster, readySPC, current)
		require.NoError(t, err)
		assert.Equal(t, coreapi.DenyAssignmentPhasePendingConfigure, got[denyAssignmentSuffixResources].Phase)
	})

	t.Run("type that left the definition set is marked PendingDeconfigure", func(t *testing.T) {
		t.Parallel()
		current := map[string]*coreapi.DenyAssignmentStatus{
			"stale-type-not-in-definitions": {Phase: coreapi.DenyAssignmentPhaseConfigured},
		}
		got, err := syncer.desiredDenyAssignmentsV2(cluster, readySPC, current)
		require.NoError(t, err)
		assert.Equal(t, coreapi.DenyAssignmentPhasePendingDeconfigure, got["stale-type-not-in-definitions"].Phase)
	})

	t.Run("already PendingDeconfigure stale type stays PendingDeconfigure", func(t *testing.T) {
		t.Parallel()
		current := map[string]*coreapi.DenyAssignmentStatus{
			"stale-type-not-in-definitions": {Phase: coreapi.DenyAssignmentPhasePendingDeconfigure},
		}
		got, err := syncer.desiredDenyAssignmentsV2(cluster, unreadySPC, current)
		require.NoError(t, err)
		assert.Equal(t, coreapi.DenyAssignmentPhasePendingDeconfigure, got["stale-type-not-in-definitions"].Phase)
	})

	t.Run("already Deconfigured stale type stays Deconfigured", func(t *testing.T) {
		t.Parallel()
		current := map[string]*coreapi.DenyAssignmentStatus{
			"stale-type-not-in-definitions": {Phase: coreapi.DenyAssignmentPhaseDeconfigured},
		}
		got, err := syncer.desiredDenyAssignmentsV2(cluster, readySPC, current)
		require.NoError(t, err)
		assert.Equal(t, coreapi.DenyAssignmentPhaseDeconfigured, got["stale-type-not-in-definitions"].Phase)
	})

	t.Run("nil existing status is an error", func(t *testing.T) {
		t.Parallel()
		_, err := syncer.desiredDenyAssignmentsV2(cluster, readySPC, map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: nil,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil status")
	})

	t.Run("principal ID change marks the old key PendingDeconfigure and adds the new key", func(t *testing.T) {
		t.Parallel()
		capi := testIdentityResourceID("capi-azure")
		oldKey := coreapi.DenyAssignmentExcludedIdentityKey{
			ResourceID:  strings.ToLower(capi.String()),
			PrincipalID: "old-principal",
		}
		current := map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: {
				Phase: coreapi.DenyAssignmentPhaseConfigured,
				ExcludedIdentities: map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedIdentityStatus{
					oldKey: {
						Phase: coreapi.DenyAssignmentExcludedIdentityPhaseConfigured,
						ObservedIdentity: &coreapi.DenyAssignmentExcludedObservedIdentity{
							ClientID:    "old-client",
							TenantID:    "old-tenant",
							PrincipalID: "old-principal",
						},
					},
				},
			},
		}
		got, err := syncer.desiredDenyAssignmentsV2(cluster, readySPC, current)
		require.NoError(t, err)
		require.Contains(t, got, denyAssignmentSuffixResources)
		assert.Equal(t, coreapi.DenyAssignmentPhaseConfigured, got[denyAssignmentSuffixResources].Phase)
		old := got[denyAssignmentSuffixResources].ExcludedIdentities[oldKey]
		require.NotNil(t, old)
		assert.Equal(t, coreapi.DenyAssignmentExcludedIdentityPhasePendingDeconfigure, old.Phase)
		require.NotNil(t, old.DeconfigureTimestamp)
		require.NotNil(t, old.ObservedIdentity)
		assert.Equal(t, "old-client", old.ObservedIdentity.ClientID)
		assert.Equal(t, "old-tenant", old.ObservedIdentity.TenantID)
		assert.Equal(t, "old-principal", old.ObservedIdentity.PrincipalID)
		newKey := coreapi.DenyAssignmentExcludedIdentityKey{
			ResourceID:  strings.ToLower(capi.String()),
			PrincipalID: testPrincipalID(capi),
		}
		newStatus := got[denyAssignmentSuffixResources].ExcludedIdentities[newKey]
		require.NotNil(t, newStatus)
		assert.Equal(t, coreapi.DenyAssignmentExcludedIdentityPhasePendingConfigure, newStatus.Phase)
		require.NotNil(t, newStatus.ObservedIdentity)
		assert.Equal(t, testPrincipalID(capi), newStatus.ObservedIdentity.PrincipalID)
		assert.Equal(t, testClientID(capi), newStatus.ObservedIdentity.ClientID)
		assert.Equal(t, testIdentityTenantID(capi), newStatus.ObservedIdentity.TenantID)
	})

	t.Run("client ID change on the same principal sets PendingConfigure without a new key", func(t *testing.T) {
		t.Parallel()
		capi := testIdentityResourceID("capi-azure")
		key := coreapi.DenyAssignmentExcludedIdentityKey{
			ResourceID:  strings.ToLower(capi.String()),
			PrincipalID: testPrincipalID(capi),
		}
		current := map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: {
				Phase: coreapi.DenyAssignmentPhaseConfigured,
				ExcludedIdentities: map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedIdentityStatus{
					key: {
						Phase: coreapi.DenyAssignmentExcludedIdentityPhaseConfigured,
						ObservedIdentity: &coreapi.DenyAssignmentExcludedObservedIdentity{
							ClientID:    "old-client",
							TenantID:    testIdentityTenantID(capi),
							PrincipalID: testPrincipalID(capi),
						},
					},
				},
			},
		}
		got, err := syncer.desiredDenyAssignmentsV2(cluster, readySPC, current)
		require.NoError(t, err)
		gotStatus := got[denyAssignmentSuffixResources].ExcludedIdentities[key]
		require.NotNil(t, gotStatus)
		assert.Equal(t, coreapi.DenyAssignmentExcludedIdentityPhasePendingConfigure, gotStatus.Phase)
		assert.Nil(t, gotStatus.DeconfigureTimestamp)
		require.NotNil(t, gotStatus.ObservedIdentity)
		assert.Equal(t, testClientID(capi), gotStatus.ObservedIdentity.ClientID)
		assert.Equal(t, testIdentityTenantID(capi), gotStatus.ObservedIdentity.TenantID)
		assert.Equal(t, testPrincipalID(capi), gotStatus.ObservedIdentity.PrincipalID)
	})

	t.Run("unchanged observed identity stays Configured", func(t *testing.T) {
		t.Parallel()
		capi := testIdentityResourceID("capi-azure")
		key := coreapi.DenyAssignmentExcludedIdentityKey{
			ResourceID:  strings.ToLower(capi.String()),
			PrincipalID: testPrincipalID(capi),
		}
		current := map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: {
				Phase: coreapi.DenyAssignmentPhaseConfigured,
				ExcludedIdentities: map[coreapi.DenyAssignmentExcludedIdentityKey]*coreapi.DenyAssignmentExcludedIdentityStatus{
					key: {
						Phase: coreapi.DenyAssignmentExcludedIdentityPhaseConfigured,
						ObservedIdentity: &coreapi.DenyAssignmentExcludedObservedIdentity{
							ClientID:    testClientID(capi),
							TenantID:    testIdentityTenantID(capi),
							PrincipalID: testPrincipalID(capi),
						},
					},
				},
			},
		}
		got, err := syncer.desiredDenyAssignmentsV2(cluster, readySPC, current)
		require.NoError(t, err)
		gotStatus := got[denyAssignmentSuffixResources].ExcludedIdentities[key]
		require.NotNil(t, gotStatus)
		assert.Equal(t, coreapi.DenyAssignmentExcludedIdentityPhaseConfigured, gotStatus.Phase)
		assert.Nil(t, gotStatus.DeconfigureTimestamp)
		require.NotNil(t, gotStatus.ObservedIdentity)
		assert.Equal(t, testClientID(capi), gotStatus.ObservedIdentity.ClientID)
		assert.Equal(t, testIdentityTenantID(capi), gotStatus.ObservedIdentity.TenantID)
		assert.Equal(t, testPrincipalID(capi), gotStatus.ObservedIdentity.PrincipalID)
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
	require.NotEmpty(t, updated.Status.DenyAssignmentsV2)
	assert.Equal(t, coreapi.DenyAssignmentPhasePendingConfigure, updated.Status.DenyAssignmentsV2[denyAssignmentSuffixResources].Phase)
}

func TestClusterDenyAssignmentIntentSyncOnceSkipsClusterDeletion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := metav1.NewTime(time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC))
	cluster := newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
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
	assert.Empty(t, updated.Status.DenyAssignmentsV2)
}

func TestClusterDenyAssignmentIntentSyncOnceDoesNotDeconfigureWhenIdentitiesUnready(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cluster := newTestCluster()
	serviceProviderCluster := newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
		spc.Status.MSIManagedIdentities.ControlPlaneOperatorsIdentities = nil
		spc.Status.DataPlaneOperatorsManagedIdentities.Identities = nil
		spc.Status.DenyAssignmentsV2 = map[string]*coreapi.DenyAssignmentStatus{
			denyAssignmentSuffixResources: {Phase: coreapi.DenyAssignmentPhaseConfigured},
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
	require.Contains(t, updated.Status.DenyAssignmentsV2, denyAssignmentSuffixResources)
	assert.Equal(t, coreapi.DenyAssignmentPhaseConfigured, updated.Status.DenyAssignmentsV2[denyAssignmentSuffixResources].Phase)
	require.Contains(t, updated.Status.DenyAssignmentsV2, denyAssignmentSuffixDenyAllOtherRPs)
	assert.Equal(t, coreapi.DenyAssignmentPhasePendingConfigure, updated.Status.DenyAssignmentsV2[denyAssignmentSuffixDenyAllOtherRPs].Phase)
}

func TestResolveObservedIdentity(t *testing.T) {
	t.Parallel()

	cpOps, _, _ := testClusterIdentities()
	controlPlaneID := cpOps["cluster-api-azure"]
	principalID := testPrincipalID(controlPlaneID)

	t.Run("ARM metadata with matching principal fills ClientID TenantID PrincipalID", func(t *testing.T) {
		t.Parallel()
		spc := newTestSPC()
		observed := resolveObservedIdentity(spc, controlPlaneID, principalID)
		require.NotNil(t, observed)
		assert.Equal(t, testClientID(controlPlaneID), observed.ClientID)
		assert.Equal(t, testIdentityTenantID(controlPlaneID), observed.TenantID)
		assert.Equal(t, principalID, observed.PrincipalID)
	})

	t.Run("ARM principal mismatch keeps MSI ClientID and leaves TenantID empty", func(t *testing.T) {
		t.Parallel()
		spc := newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
			key := strings.ToLower(controlPlaneID.String())
			spc.Status.ManagedIdentityDetails[key].MetadataFromARMUserAssignedIdentitiesAPI.PrincipalID = ptr.To("other-principal")
		})
		observed := resolveObservedIdentity(spc, controlPlaneID, principalID)
		require.NotNil(t, observed)
		assert.Equal(t, testClientID(controlPlaneID), observed.ClientID)
		assert.Empty(t, observed.TenantID)
		assert.Equal(t, principalID, observed.PrincipalID)
	})

	t.Run("missing ARM metadata uses MSI ClientID", func(t *testing.T) {
		t.Parallel()
		spc := newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
			spc.Status.ManagedIdentityDetails = nil
		})
		observed := resolveObservedIdentity(spc, controlPlaneID, principalID)
		require.NotNil(t, observed)
		assert.Equal(t, testClientID(controlPlaneID), observed.ClientID)
		assert.Empty(t, observed.TenantID)
		assert.Equal(t, principalID, observed.PrincipalID)
	})
}
