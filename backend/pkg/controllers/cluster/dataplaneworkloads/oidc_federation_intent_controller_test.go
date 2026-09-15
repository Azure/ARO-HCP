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
	clocktesting "k8s.io/utils/clock/testing"
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

	resolvedA := resolvedARMManagedIdentityMetadata(identityA, "client-a", "principal-a", "tenant-a")
	keyA := strings.ToLower(identityA.String())
	targetA := coreapi.DataplaneOIDCFederationIdentityInstance{
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	keyB := strings.ToLower(identityB.String())
	ficA := metadataapi.Must(azcorearm.ParseResourceID(identityA.String() + "/federatedIdentityCredentials/fic-a"))
	ficB := metadataapi.Must(azcorearm.ParseResourceID(identityB.String() + "/federatedIdentityCredentials/fic-b"))

	unresolvedA := &coreapi.ManagedIdentityMetadata{
		ResourceID: identityA,
	}

	hardcodedOnlyA := &coreapi.ManagedIdentityMetadata{
		ResourceID: identityA,
		MetadataFromHardcodedIdentity: &coreapi.IdentityMetadataValue{
			ClientID:    ptr.To("hardcoded-client"),
			PrincipalID: ptr.To("hardcoded-principal"),
			TenantID:    ptr.To("hardcoded-tenant"),
		},
	}

	testCases := []struct {
		name                              string
		dataPlaneOperators                map[string]*azcorearm.ResourceID
		details                           map[string]*coreapi.ManagedIdentityMetadata
		current                           map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus
		expectedStates                    map[string]string
		expectedTarget                    map[string]coreapi.DataplaneOIDCFederationIdentityInstance
		expectStampedDeconfigureTimestamp []string
		expectIdentityChangeClear         []string
		expectNilWhenEmpty                bool
	}{
		{
			name:               "empty operators and empty federation yields nil",
			expectNilWhenEmpty: true,
		},
		{
			name: "resolved data-plane identity is added as PendingConfigure",
			dataPlaneOperators: map[string]*azcorearm.ResourceID{
				"cloud-controller-manager": identityA,
			},
			details: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): resolvedA,
			},
			expectedStates: map[string]string{
				keyA: "pending",
			},
			expectedTarget: map[string]coreapi.DataplaneOIDCFederationIdentityInstance{
				keyA: targetA,
			},
		},
		{
			name: "shared data-plane identity is federated once",
			dataPlaneOperators: map[string]*azcorearm.ResourceID{
				"cloud-controller-manager": identityA,
				"ingress":                  identityA,
			},
			details: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): resolvedA,
			},
			expectedStates: map[string]string{
				keyA: "pending",
			},
			expectedTarget: map[string]coreapi.DataplaneOIDCFederationIdentityInstance{
				keyA: targetA,
			},
		},
		{
			name: "unresolved ARM metadata is not added",
			dataPlaneOperators: map[string]*azcorearm.ResourceID{
				"cloud-controller-manager": identityA,
			},
			details: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): unresolvedA,
			},
			expectNilWhenEmpty: true,
		},
		{
			name: "identity missing from ManagedIdentityDetails is not added",
			dataPlaneOperators: map[string]*azcorearm.ResourceID{
				"cloud-controller-manager": identityA,
			},
			expectNilWhenEmpty: true,
		},
		{
			name: "hardcoded-identity metadata is not used for data-plane federation",
			dataPlaneOperators: map[string]*azcorearm.ResourceID{
				"cloud-controller-manager": identityA,
			},
			details: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): hardcodedOnlyA,
			},
			expectNilWhenEmpty: true,
		},
		{
			name: "control-plane identity with ARM metadata is not added",
			details: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): resolvedA,
			},
			expectNilWhenEmpty: true,
		},
		{
			name: "configured identity that is still resolved is left Configured",
			dataPlaneOperators: map[string]*azcorearm.ResourceID{
				"cloud-controller-manager": identityA,
			},
			details: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): resolvedA,
			},
			current: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {
					TargetIdentity:  targetA,
					EnsuredIdentity: ptr.To(targetA),
				},
			},
			expectedStates: map[string]string{
				keyA: "ensured",
			},
			expectedTarget: map[string]coreapi.DataplaneOIDCFederationIdentityInstance{
				keyA: targetA,
			},
		},
		{
			name: "deconfigured identity that is resolved again becomes PendingConfigure",
			dataPlaneOperators: map[string]*azcorearm.ResourceID{
				"cloud-controller-manager": identityA,
			},
			details: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): resolvedA,
			},
			current: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {
					DeconfigureTimestamp: &metav1.Time{Time: time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)},
				},
			},
			expectedStates: map[string]string{
				keyA: "pending",
			},
			expectedTarget: map[string]coreapi.DataplaneOIDCFederationIdentityInstance{
				keyA: targetA,
			},
		},
		{
			name: "pending deconfigure identity that is resolved again becomes PendingConfigure",
			dataPlaneOperators: map[string]*azcorearm.ResourceID{
				"cloud-controller-manager": identityA,
			},
			details: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): resolvedA,
			},
			current: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {
					DeconfigureTimestamp: &metav1.Time{Time: time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)},
				},
			},
			expectedStates: map[string]string{
				keyA: "pending",
			},
			expectedTarget: map[string]coreapi.DataplaneOIDCFederationIdentityInstance{
				keyA: targetA,
			},
		},
		{
			name: "identity that left data-plane operators is marked PendingDeconfigure",
			current: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {
					TargetIdentity:  targetA,
					EnsuredIdentity: ptr.To(targetA),
					AzureResources:  []*azcorearm.ResourceID{ficA},
				},
			},
			expectedStates: map[string]string{
				keyA: "deconfigure",
			},
			expectStampedDeconfigureTimestamp: []string{keyA},
		},
		{
			name: "already PendingDeconfigure identity that left operators stays PendingDeconfigure",
			current: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {
					DeconfigureTimestamp: &metav1.Time{Time: time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)},
				},
			},
			expectedStates: map[string]string{
				keyA: "deconfigure",
			},
		},
		{
			name: "already deconfigured identity that left operators is dropped",
			current: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {},
			},
			expectNilWhenEmpty: true,
		},
		{
			name: "unresolved ARM metadata leaves previously configured identity as-is",
			dataPlaneOperators: map[string]*azcorearm.ResourceID{
				"cloud-controller-manager": identityA,
			},
			details: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): unresolvedA,
			},
			current: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {TargetIdentity: targetA, EnsuredIdentity: ptr.To(targetA)},
			},
			expectedStates: map[string]string{
				keyA: "ensured",
			},
		},
		{
			name: "client and principal id change updates TargetIdentity and clears ensured FIC state on the same key",
			dataPlaneOperators: map[string]*azcorearm.ResourceID{
				"cloud-controller-manager": identityA,
			},
			details: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): resolvedARMManagedIdentityMetadata(identityA, "client-a-rotated", "principal-a-rotated", "tenant-a"),
			},
			current: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {
					TargetIdentity:        targetA,
					EnsuredIdentity:       ptr.To(targetA),
					AzureResources:        []*azcorearm.ResourceID{ficA},
					PendingAzureResources: []*azcorearm.ResourceID{ficA},
				},
			},
			expectedStates: map[string]string{
				keyA: "pending",
			},
			expectedTarget: map[string]coreapi.DataplaneOIDCFederationIdentityInstance{
				keyA: {
					ClientID:    "client-a-rotated",
					PrincipalID: "principal-a-rotated",
					TenantID:    "tenant-a",
				},
			},
			expectIdentityChangeClear: []string{keyA},
		},
		{
			name: "one identity added and another removed",
			dataPlaneOperators: map[string]*azcorearm.ResourceID{
				"cloud-controller-manager": identityA,
			},
			details: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): resolvedA,
			},
			current: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyB: {
					TargetIdentity:  targetA,
					EnsuredIdentity: ptr.To(targetA),
					AzureResources:  []*azcorearm.ResourceID{ficB},
				},
			},
			expectedStates: map[string]string{
				keyA: "pending",
				keyB: "deconfigure",
			},
			expectedTarget: map[string]coreapi.DataplaneOIDCFederationIdentityInstance{
				keyA: targetA,
			},
			expectStampedDeconfigureTimestamp: []string{keyB},
		},
		{
			name: "ARM metadata for a UAMI that is no longer a data-plane operator does not keep federation",
			details: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): resolvedA,
			},
			current: map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {
					TargetIdentity:  targetA,
					EnsuredIdentity: ptr.To(targetA),
					AzureResources:  []*azcorearm.ResourceID{ficA},
				},
			},
			expectedStates: map[string]string{
				keyA: "deconfigure",
			},
			expectStampedDeconfigureTimestamp: []string{keyA},
		},
		{
			name: "data-plane identity is federated from ARM metadata even when the same UAMI also has hardcoded identity metadata",
			dataPlaneOperators: map[string]*azcorearm.ResourceID{
				"cloud-controller-manager": identityA,
			},
			details: map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): {
					ResourceID: identityA,
					MetadataFromARMUserAssignedIdentitiesAPI: &coreapi.IdentityMetadataValue{
						ClientID:    ptr.To("client-a"),
						PrincipalID: ptr.To("principal-a"),
						TenantID:    ptr.To("tenant-a"),
					},
					MetadataFromHardcodedIdentity: &coreapi.IdentityMetadataValue{
						ClientID:    ptr.To("hardcoded-client"),
						PrincipalID: ptr.To("hardcoded-principal"),
						TenantID:    ptr.To("hardcoded-tenant"),
					},
				},
			},
			expectedStates: map[string]string{
				keyA: "pending",
			},
			expectedTarget: map[string]coreapi.DataplaneOIDCFederationIdentityInstance{
				keyA: targetA,
			},
		},
	}

	since := metav1.NewTime(time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC))
	syncer := &dataPlaneOIDCFederationIntentSyncer{
		clock: clocktesting.NewFakePassiveClock(since.Time),
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := syncer.desiredDataPlaneOIDCFederationStatus(context.Background(), tc.dataPlaneOperators, tc.details, tc.current)
			require.NoError(t, err)
			if tc.expectNilWhenEmpty {
				assert.Nil(t, got)
				return
			}

			require.Len(t, got, len(tc.expectedStates))
			for key, expectedState := range tc.expectedStates {
				require.Contains(t, got, key)
				require.NotNil(t, got[key])
				switch expectedState {
				case "pending":
					assert.Nil(t, got[key].DeconfigureTimestamp, "key %s should not be deconfiguring", key)
					assert.False(t, got[key].TargetIdentityEnsured(), "key %s should not be ensured", key)
				case "ensured":
					assert.True(t, got[key].TargetIdentityEnsured(), "key %s should be ensured", key)
				case "deconfigure":
					assert.NotNil(t, got[key].DeconfigureTimestamp, "key %s should be deconfiguring", key)
				default:
					t.Fatalf("unknown expected state %q for key %s", expectedState, key)
				}
			}
			for key, expectedTarget := range tc.expectedTarget {
				require.Contains(t, got, key)
				assert.Equal(t, expectedTarget, got[key].TargetIdentity)
			}
			stamped := map[string]struct{}{}
			for _, key := range tc.expectStampedDeconfigureTimestamp {
				stamped[key] = struct{}{}
				require.NotNil(t, got[key].DeconfigureTimestamp)
				assert.Equal(t, since, *got[key].DeconfigureTimestamp)
			}
			for key := range tc.expectedStates {
				if _, ok := stamped[key]; ok {
					continue
				}
				if tc.expectedStates[key] == "pending" {
					assert.Nil(t, got[key].DeconfigureTimestamp)
					continue
				}
				if tc.current[key] == nil {
					assert.Nil(t, got[key].DeconfigureTimestamp)
					continue
				}
				assert.Equal(t, tc.current[key].DeconfigureTimestamp, got[key].DeconfigureTimestamp)
			}
			for _, key := range tc.expectIdentityChangeClear {
				require.Contains(t, got, key)
				assert.Nil(t, got[key].EnsuredIdentity)
				assert.Empty(t, got[key].AzureResources)
				assert.Empty(t, got[key].PendingAzureResources)
			}
		})
	}
}

func TestDesiredDataPlaneOIDCFederationStatusNilEntryErrors(t *testing.T) {
	t.Parallel()

	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())

	t.Run("nil federation status", func(t *testing.T) {
		t.Parallel()
		_, err := (&dataPlaneOIDCFederationIntentSyncer{
			clock: clocktesting.NewFakePassiveClock(time.Time{}),
		}).desiredDataPlaneOIDCFederationStatus(context.Background(), nil, nil, map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
			keyA: nil,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil status")
	})

	t.Run("nil ManagedIdentityDetails metadata", func(t *testing.T) {
		t.Parallel()
		_, err := (&dataPlaneOIDCFederationIntentSyncer{
			clock: clocktesting.NewFakePassiveClock(time.Time{}),
		}).desiredDataPlaneOIDCFederationStatus(context.Background(), map[string]*azcorearm.ResourceID{
			"cloud-controller-manager": identityA,
		}, map[string]*coreapi.ManagedIdentityMetadata{
			strings.ToLower(identityA.String()): nil,
		}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil metadata")
	})
}

func TestDataPlaneOIDCFederationIntentSyncOnce(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	resolvedA := resolvedARMManagedIdentityMetadata(identityA, "client-a", "principal-a", "tenant-a")
	keyA := strings.ToLower(identityA.String())

	cluster := newTestClusterWithIdentities(t, testClusterName, nil, map[string]*azcorearm.ResourceID{
		"cloud-controller-manager": identityA,
	})
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentityDetails = map[string]*coreapi.ManagedIdentityMetadata{
		strings.ToLower(identityA.String()): resolvedA,
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	syncer := &dataPlaneOIDCFederationIntentSyncer{
		clock:                        clocktesting.NewFakePassiveClock(now),
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
	assert.False(t, updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA].TargetIdentityEnsured())
	assert.Equal(t, coreapi.DataplaneOIDCFederationIdentityInstance{
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}, updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA].TargetIdentity)
}

func TestDataPlaneOIDCFederationIntentSyncOnceStampsDeconfigureTimestampOnLiveCluster(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	keyA := strings.ToLower(identityA.String())
	ficA := metadataapi.Must(azcorearm.ParseResourceID(identityA.String() + "/federatedIdentityCredentials/fic-a"))
	targetA := coreapi.DataplaneOIDCFederationIdentityInstance{
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	cluster := newTestClusterWithIdentities(t, testClusterName, nil, nil)
	serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
	serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
		keyA: {
			TargetIdentity:  targetA,
			EnsuredIdentity: ptr.To(targetA),
			AzureResources:  []*azcorearm.ResourceID{ficA},
		},
	}

	mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
	require.NoError(t, err)

	syncer := &dataPlaneOIDCFederationIntentSyncer{
		clock:                        clocktesting.NewFakePassiveClock(now),
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
	got := updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA]
	require.NotNil(t, got)
	assert.NotNil(t, got.DeconfigureTimestamp)
	require.NotNil(t, got.DeconfigureTimestamp)
	assert.True(t, got.DeconfigureTimestamp.Time.Equal(now))
}

func TestDataPlaneOIDCFederationIntentSyncOnceMarksPendingDeconfigureOnClusterDeletion(t *testing.T) {
	t.Parallel()

	identityA := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/identity-a"))
	resolvedA := resolvedARMManagedIdentityMetadata(identityA, "client-a", "principal-a", "tenant-a")
	keyA := strings.ToLower(identityA.String())
	ficA := metadataapi.Must(azcorearm.ParseResourceID(identityA.String() + "/federatedIdentityCredentials/fic-a"))
	targetA := coreapi.DataplaneOIDCFederationIdentityInstance{
		ClientID:    "client-a",
		PrincipalID: "principal-a",
		TenantID:    "tenant-a",
	}

	deletionTimestamp := &metav1.Time{Time: time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)}
	clusterServiceDeletionTimestamp := &metav1.Time{Time: time.Date(2026, 9, 5, 12, 2, 0, 0, time.UTC)}
	clusterServiceID := testClusterServiceID()
	pendingClusterServiceID := testClusterServiceID()
	now := deletionTimestamp.Time

	testCases := []struct {
		name              string
		mutateCluster     func(cluster *coreapi.HCPOpenShiftCluster)
		expectDeconfigure bool
	}{
		{
			name: "new deletion approach deconfigures after CS is confirmed gone even if PendingClusterServiceID is still set",
			mutateCluster: func(cluster *coreapi.HCPOpenShiftCluster) {
				cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach = true
				cluster.ServiceProviderProperties.DeletionTimestamp = deletionTimestamp
				cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = clusterServiceDeletionTimestamp
				cluster.ServiceProviderProperties.PendingClusterServiceID = pendingClusterServiceID
			},
			expectDeconfigure: true,
		},
		{
			name: "new deletion approach does not deconfigure before ClusterServiceDeletionTimestamp is set",
			mutateCluster: func(cluster *coreapi.HCPOpenShiftCluster) {
				cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach = true
				cluster.ServiceProviderProperties.DeletionTimestamp = deletionTimestamp
			},
			expectDeconfigure: false,
		},
		{
			name: "new deletion approach does not deconfigure while ClusterServiceID is still set",
			mutateCluster: func(cluster *coreapi.HCPOpenShiftCluster) {
				cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach = true
				cluster.ServiceProviderProperties.DeletionTimestamp = deletionTimestamp
				cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = clusterServiceDeletionTimestamp
				cluster.ServiceProviderProperties.ClusterServiceID = clusterServiceID
			},
			expectDeconfigure: false,
		},
		{
			name: "legacy deletion approach does not deconfigure while ClusterServiceID is already cleared",
			mutateCluster: func(cluster *coreapi.HCPOpenShiftCluster) {
				cluster.ServiceProviderProperties.DeletionTimestamp = deletionTimestamp
			},
			expectDeconfigure: false,
		},
		{
			name: "legacy deletion approach does not deconfigure while ClusterServiceID is still set",
			mutateCluster: func(cluster *coreapi.HCPOpenShiftCluster) {
				cluster.ServiceProviderProperties.DeletionTimestamp = deletionTimestamp
				cluster.ServiceProviderProperties.ClusterServiceID = clusterServiceID
			},
			expectDeconfigure: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			cluster := newTestClusterWithIdentities(t, testClusterName, nil, map[string]*azcorearm.ResourceID{
				"cloud-controller-manager": identityA,
			})
			tc.mutateCluster(cluster)

			serviceProviderCluster := newTestServiceProviderClusterWithIdentities(testClusterName, nil, nil)
			serviceProviderCluster.Status.ManagedIdentityDetails = map[string]*coreapi.ManagedIdentityMetadata{
				strings.ToLower(identityA.String()): resolvedA,
			}
			serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[string]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{
				keyA: {
					TargetIdentity:  targetA,
					EnsuredIdentity: ptr.To(targetA),
					AzureResources:  []*azcorearm.ResourceID{ficA},
				},
			}

			mockResourcesDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProviderCluster})
			require.NoError(t, err)

			syncer := &dataPlaneOIDCFederationIntentSyncer{
				clock:                        clocktesting.NewFakePassiveClock(now),
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
			got := updated.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[keyA]
			assert.Equal(t, tc.expectDeconfigure, got.DeconfigureTimestamp != nil)
			if tc.expectDeconfigure {
				require.NotNil(t, got.DeconfigureTimestamp)
				assert.True(t, got.DeconfigureTimestamp.Time.Equal(now))
			}
		})
	}
}

func TestTargetIdentityFromARMUserAssignedIdentities(t *testing.T) {
	t.Parallel()

	syncer := &dataPlaneOIDCFederationIntentSyncer{}
	identity := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/Test-RG/providers/Microsoft.ManagedIdentity/userAssignedIdentities/Identity-A"))

	t.Run("resolved ARM user assigned identities metadata", func(t *testing.T) {
		t.Parallel()
		metadata := &coreapi.ManagedIdentityMetadata{
			ResourceID: identity,
			MetadataFromARMUserAssignedIdentitiesAPI: &coreapi.IdentityMetadataValue{
				ClientID:    ptr.To("arm-client"),
				PrincipalID: ptr.To("arm-principal"),
				TenantID:    ptr.To("arm-tenant"),
			},
			MetadataFromHardcodedIdentity: &coreapi.IdentityMetadataValue{
				ClientID:    ptr.To("hardcoded-client"),
				PrincipalID: ptr.To("hardcoded-principal"),
				TenantID:    ptr.To("hardcoded-tenant"),
			},
		}
		target, ok := syncer.targetIdentityFromARMUserAssignedIdentities(metadata)
		require.True(t, ok)
		assert.Equal(t, coreapi.DataplaneOIDCFederationIdentityInstance{
			ClientID:    "arm-client",
			PrincipalID: "arm-principal",
			TenantID:    "arm-tenant",
		}, target)
	})

	t.Run("missing ARM user assigned identities metadata", func(t *testing.T) {
		t.Parallel()
		metadata := &coreapi.ManagedIdentityMetadata{
			ResourceID: identity,
			MetadataFromHardcodedIdentity: &coreapi.IdentityMetadataValue{
				ClientID:    ptr.To("hardcoded-client"),
				PrincipalID: ptr.To("hardcoded-principal"),
				TenantID:    ptr.To("hardcoded-tenant"),
			},
		}
		_, ok := syncer.targetIdentityFromARMUserAssignedIdentities(metadata)
		assert.False(t, ok)
	})

	t.Run("ARM retrieval error is unresolved", func(t *testing.T) {
		t.Parallel()
		metadata := &coreapi.ManagedIdentityMetadata{
			ResourceID: identity,
			MetadataFromARMUserAssignedIdentitiesAPI: &coreapi.IdentityMetadataValue{
				RetrievalError: ptr.To("ResourceNotFound"),
			},
		}
		_, ok := syncer.targetIdentityFromARMUserAssignedIdentities(metadata)
		assert.False(t, ok)
	})
}

func resolvedARMManagedIdentityMetadata(resourceID *azcorearm.ResourceID, clientID, principalID, tenantID string) *coreapi.ManagedIdentityMetadata {
	return &coreapi.ManagedIdentityMetadata{
		ResourceID: resourceID,
		MetadataFromARMUserAssignedIdentitiesAPI: &coreapi.IdentityMetadataValue{
			ClientID:    ptr.To(clientID),
			PrincipalID: ptr.To(principalID),
			TenantID:    ptr.To(tenantID),
		},
	}
}
