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

package dataplaneworkloads

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

func TestDesiredDataPlaneOIDCFederationStatus(t *testing.T) {
	t.Parallel()

	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	identityB := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-b"))

	resolvedA := &coreapi.ManagedIdentityDetails{
		ResourceID:  identityA,
		ClientID:    ptr.To("client-a"),
		PrincipalID: ptr.To("principal-a"),
		TenantID:    ptr.To("tenant-a"),
	}
	keyA, ok := resolvedA.AsDataplaneOIDCFederationKey()
	require.True(t, ok)

	resolvedB := &coreapi.ManagedIdentityDetails{
		ResourceID:  identityB,
		ClientID:    ptr.To("client-b"),
		PrincipalID: ptr.To("principal-b"),
		TenantID:    ptr.To("tenant-b"),
	}
	keyB, ok := resolvedB.AsDataplaneOIDCFederationKey()
	require.True(t, ok)

	unresolvedA := &coreapi.ManagedIdentityDetails{
		ResourceID: identityA,
		ClientID:   nil,
	}

	testCases := []struct {
		name               string
		details            map[coreapi.ManagedIdentityDetailsKey]*coreapi.ManagedIdentityDetails
		current            map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus
		expectedPhases     map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]coreapi.ManagedIdentityDataplaneOIDCFederationPhase
		expectNilWhenEmpty bool
	}{
		{
			name:               "empty details and empty federation yields nil",
			expectNilWhenEmpty: true,
		},
		{
			name: "resolved identity is added as PendingConfigure",
			details: map[coreapi.ManagedIdentityDetailsKey]*coreapi.ManagedIdentityDetails{
				{ResourceID: strings.ToLower(identityA.String()), MSIBasedDetails: false}: resolvedA,
			},
			expectedPhases: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]coreapi.ManagedIdentityDataplaneOIDCFederationPhase{
				keyA: coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure,
			},
		},
		{
			name: "unresolved identity is not added",
			details: map[coreapi.ManagedIdentityDetailsKey]*coreapi.ManagedIdentityDetails{
				{ResourceID: strings.ToLower(identityA.String()), MSIBasedDetails: false}: unresolvedA,
			},
			expectNilWhenEmpty: true,
		},
		{
			name: "configured identity that is still resolved is left Configured",
			details: map[coreapi.ManagedIdentityDetailsKey]*coreapi.ManagedIdentityDetails{
				{ResourceID: strings.ToLower(identityA.String()), MSIBasedDetails: false}: resolvedA,
			},
			current: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {Phase: coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured},
			},
			expectedPhases: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]coreapi.ManagedIdentityDataplaneOIDCFederationPhase{
				keyA: coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured,
			},
		},
		{
			name: "deconfigured identity that is resolved again becomes PendingConfigure",
			details: map[coreapi.ManagedIdentityDetailsKey]*coreapi.ManagedIdentityDetails{
				{ResourceID: strings.ToLower(identityA.String()), MSIBasedDetails: false}: resolvedA,
			},
			current: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {Phase: coreapi.ManagedIdentityDataplaneOIDCFederationPhaseDeconfigured},
			},
			expectedPhases: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]coreapi.ManagedIdentityDataplaneOIDCFederationPhase{
				keyA: coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure,
			},
		},
		{
			name: "identity that left details is marked PendingDeconfigure",
			current: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {Phase: coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured},
			},
			expectedPhases: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]coreapi.ManagedIdentityDataplaneOIDCFederationPhase{
				keyA: coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure,
			},
		},
		{
			name: "already deconfigured identity that left details stays Deconfigured",
			current: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {Phase: coreapi.ManagedIdentityDataplaneOIDCFederationPhaseDeconfigured},
			},
			expectedPhases: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]coreapi.ManagedIdentityDataplaneOIDCFederationPhase{
				keyA: coreapi.ManagedIdentityDataplaneOIDCFederationPhaseDeconfigured,
			},
		},
		{
			name: "unresolved details entry leaves previously configured identity as-is",
			details: map[coreapi.ManagedIdentityDetailsKey]*coreapi.ManagedIdentityDetails{
				{ResourceID: strings.ToLower(identityA.String()), MSIBasedDetails: false}: unresolvedA,
			},
			current: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {Phase: coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured},
			},
			expectedPhases: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]coreapi.ManagedIdentityDataplaneOIDCFederationPhase{
				keyA: coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured,
			},
		},
		{
			name: "client and principal id change deconfigures the old key and configures the new key",
			details: map[coreapi.ManagedIdentityDetailsKey]*coreapi.ManagedIdentityDetails{
				{ResourceID: strings.ToLower(identityA.String()), MSIBasedDetails: false}: {
					ResourceID:  identityA,
					ClientID:    ptr.To("client-a-rotated"),
					PrincipalID: ptr.To("principal-a-rotated"),
					TenantID:    ptr.To("tenant-a"),
				},
			},
			current: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {Phase: coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured},
			},
			expectedPhases: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]coreapi.ManagedIdentityDataplaneOIDCFederationPhase{
				{
					ResourceID:  strings.ToLower(identityA.String()),
					ClientID:    "client-a-rotated",
					PrincipalID: "principal-a-rotated",
					TenantID:    "tenant-a",
				}: coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure,
				keyA: coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure,
			},
		},
		{
			name: "one identity added and another removed",
			details: map[coreapi.ManagedIdentityDetailsKey]*coreapi.ManagedIdentityDetails{
				{ResourceID: strings.ToLower(identityA.String()), MSIBasedDetails: false}: resolvedA,
			},
			current: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyB: {Phase: coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured},
			},
			expectedPhases: map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]coreapi.ManagedIdentityDataplaneOIDCFederationPhase{
				keyA: coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure,
				keyB: coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := desiredDataPlaneOIDCFederationStatus(tc.details, tc.current)
			require.NoError(t, err)
			if tc.expectNilWhenEmpty {
				assert.Nil(t, got)
				return
			}

			require.Len(t, got, len(tc.expectedPhases))
			for key, expectedPhase := range tc.expectedPhases {
				require.Contains(t, got, key)
				require.NotNil(t, got[key])
				assert.Equal(t, expectedPhase, got[key].Phase)
			}
		})
	}
}

func TestDesiredDataPlaneOIDCFederationStatusNilEntryErrors(t *testing.T) {
	t.Parallel()

	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := coreapi.ManagedIdentityDataplaneOIDCFederationKey{
		ResourceID:  strings.ToLower(identityA.String()),
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	_, err := desiredDataPlaneOIDCFederationStatus(nil, map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: nil,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil status")
}

func TestDataPlaneOIDCFederationIntentSyncOnce(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	resolvedA := &coreapi.ManagedIdentityDetails{
		ResourceID:  identityA,
		ClientID:    ptr.To("client-a"),
		PrincipalID: ptr.To("principal-a"),
		TenantID:    ptr.To("tenant-a"),
	}
	keyA, ok := resolvedA.AsDataplaneOIDCFederationKey()
	require.True(t, ok)

	cluster := newTestClusterWithIdentities(t, testClusterName, nil, nil)
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentityDetails = map[coreapi.ManagedIdentityDetailsKey]*coreapi.ManagedIdentityDetails{
		{ResourceID: strings.ToLower(identityA.String()), MSIBasedDetails: false}: resolvedA,
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	syncer := &dataPlaneOIDCFederationIntentSyncer{
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
	require.Contains(t, updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation, keyA)
	assert.Equal(t, coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure, updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA].Phase)
}

func TestDataPlaneOIDCFederationIntentSyncOnceMarksPendingDeconfigureOnClusterDeletion(t *testing.T) {
	t.Parallel()

	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	resolvedA := &coreapi.ManagedIdentityDetails{
		ResourceID:  identityA,
		ClientID:    ptr.To("client-a"),
		PrincipalID: ptr.To("principal-a"),
		TenantID:    ptr.To("tenant-a"),
	}
	keyA, ok := resolvedA.AsDataplaneOIDCFederationKey()
	require.True(t, ok)

	deletionTimestamp := &metav1.Time{Time: time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)}
	clusterServiceDeletionTimestamp := &metav1.Time{Time: time.Date(2026, 9, 5, 12, 2, 0, 0, time.UTC)}
	clusterServiceID := testClusterServiceID()
	pendingClusterServiceID := testClusterServiceID()

	testCases := []struct {
		name          string
		mutateCluster func(cluster *coreapi.HCPOpenShiftCluster)
		expectedPhase coreapi.ManagedIdentityDataplaneOIDCFederationPhase
	}{
		{
			name: "new deletion approach deconfigures after CS is confirmed gone even if PendingClusterServiceID is still set",
			mutateCluster: func(cluster *coreapi.HCPOpenShiftCluster) {
				cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach = true
				cluster.ServiceProviderProperties.DeletionTimestamp = deletionTimestamp
				cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = clusterServiceDeletionTimestamp
				cluster.ServiceProviderProperties.PendingClusterServiceID = pendingClusterServiceID
			},
			expectedPhase: coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure,
		},
		{
			name: "new deletion approach does not deconfigure before ClusterServiceDeletionTimestamp is set",
			mutateCluster: func(cluster *coreapi.HCPOpenShiftCluster) {
				cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach = true
				cluster.ServiceProviderProperties.DeletionTimestamp = deletionTimestamp
			},
			expectedPhase: coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured,
		},
		{
			name: "new deletion approach does not deconfigure while ClusterServiceID is still set",
			mutateCluster: func(cluster *coreapi.HCPOpenShiftCluster) {
				cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach = true
				cluster.ServiceProviderProperties.DeletionTimestamp = deletionTimestamp
				cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = clusterServiceDeletionTimestamp
				cluster.ServiceProviderProperties.ClusterServiceID = clusterServiceID
			},
			expectedPhase: coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured,
		},
		{
			name: "legacy deletion approach deconfigures when ClusterServiceID is already cleared",
			mutateCluster: func(cluster *coreapi.HCPOpenShiftCluster) {
				cluster.ServiceProviderProperties.DeletionTimestamp = deletionTimestamp
			},
			expectedPhase: coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure,
		},
		{
			name: "legacy deletion approach does not deconfigure while ClusterServiceID is still set",
			mutateCluster: func(cluster *coreapi.HCPOpenShiftCluster) {
				cluster.ServiceProviderProperties.DeletionTimestamp = deletionTimestamp
				cluster.ServiceProviderProperties.ClusterServiceID = clusterServiceID
			},
			expectedPhase: coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			cluster := newTestClusterWithIdentities(t, testClusterName, nil, nil)
			tc.mutateCluster(cluster)

			serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
			serviceProviderCluster.Status.ManagedIdentityDetails = map[coreapi.ManagedIdentityDetailsKey]*coreapi.ManagedIdentityDetails{
				{ResourceID: strings.ToLower(identityA.String()), MSIBasedDetails: false}: resolvedA,
			}
			serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {Phase: coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured},
			}

			mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
			require.NoError(t, err)

			syncer := &dataPlaneOIDCFederationIntentSyncer{
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
			require.Contains(t, updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation, keyA)
			assert.Equal(t, tc.expectedPhase, updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA].Phase)
		})
	}
}
