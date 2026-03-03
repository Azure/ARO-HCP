// Copyright 2025 Microsoft Corporation
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

package clusterprovisioningcontrollers

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestDNSReservationCleanupController_SyncOnce(t *testing.T) {
	subscriptionID := "test-subscription"
	resourceGroupName := "test-rg"
	clusterName := "test-cluster"

	// Parse resource IDs
	clusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID + "/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/" + clusterName))
	dnsReservationResourceID := api.Must(api.ToDNSReservationResourceID(subscriptionID, "my-dns-name"))

	serviceProviderClusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID + "/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/" + clusterName +
			"/serviceProviderClusters/default"))

	now := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
	oneWeekFromNow := now.Add(7 * 24 * time.Hour)
	oneHourAgo := now.Add(-1 * time.Hour)
	oneHourFromNow := now.Add(1 * time.Hour)

	// verifyMarkedForCleanup returns a verify function that checks the DNS reservation
	// has been marked for cleanup with PendingDeletion state and the expected cleanup time
	verifyMarkedForCleanup := func(expectedCleanupTime time.Time) func(t *testing.T, mockDBClient *databasetesting.MockDBClient) {
		return func(t *testing.T, mockDBClient *databasetesting.MockDBClient) {
			dnsReservation, err := mockDBClient.DNSReservations(subscriptionID).Get(context.Background(), "my-dns-name")
			require.NoError(t, err)
			assert.Equal(t, api.BindingStatePendingDeletion, dnsReservation.BindingState)
			require.NotNil(t, dnsReservation.CleanupTime)
			assert.True(t, expectedCleanupTime.Equal(dnsReservation.CleanupTime.Time), "expected cleanup time %v, got %v", expectedCleanupTime, dnsReservation.CleanupTime.Time)
			assert.Nil(t, dnsReservation.MustBindByTime)
		}
	}

	// verifyDeleted returns a verify function that checks the DNS reservation has been deleted
	verifyDeleted := func(t *testing.T, mockDBClient *databasetesting.MockDBClient) {
		_, err := mockDBClient.DNSReservations(subscriptionID).Get(context.Background(), "my-dns-name")
		assert.True(t, database.IsResponseError(err, 404), "expected DNS reservation to be deleted")
	}

	tests := []struct {
		name                   string
		dnsReservation         *api.DNSReservation
		serviceProviderCluster *api.ServiceProviderCluster
		verify                 func(t *testing.T, mockDBClient *databasetesting.MockDBClient)
	}{
		{
			name: "Case 1: cleanupTime in the past - delete the DNS reservation",
			dnsReservation: &api.DNSReservation{
				CosmosMetadata: api.CosmosMetadata{
					ResourceID: dnsReservationResourceID,
				},
				ResourceID:   dnsReservationResourceID,
				BindingState: api.BindingStatePendingDeletion,
				CleanupTime:  &metav1.Time{Time: oneHourAgo},
			},
			serviceProviderCluster: nil,
			verify:                 verifyDeleted,
		},
		{
			name: "Case 2: cleanupTime in the future - return early, no action",
			dnsReservation: &api.DNSReservation{
				CosmosMetadata: api.CosmosMetadata{
					ResourceID: dnsReservationResourceID,
				},
				ResourceID:   dnsReservationResourceID,
				BindingState: api.BindingStatePendingDeletion,
				CleanupTime:  &metav1.Time{Time: oneHourFromNow},
			},
			serviceProviderCluster: nil,
			verify: func(t *testing.T, mockDBClient *databasetesting.MockDBClient) {
				dnsReservation, err := mockDBClient.DNSReservations(subscriptionID).Get(context.Background(), "my-dns-name")
				require.NoError(t, err)
				assert.Equal(t, api.BindingStatePendingDeletion, dnsReservation.BindingState)
				require.NotNil(t, dnsReservation.CleanupTime)
				assert.True(t, oneHourFromNow.Equal(dnsReservation.CleanupTime.Time), "expected cleanup time %v, got %v", oneHourFromNow, dnsReservation.CleanupTime.Time)
			},
		},
		{
			name: "Case 3: owningCluster does not exist and bindingState is Bound - mark for cleanup in one week",
			dnsReservation: &api.DNSReservation{
				CosmosMetadata: api.CosmosMetadata{
					ResourceID: dnsReservationResourceID,
				},
				ResourceID:    dnsReservationResourceID,
				BindingState:  api.BindingStateBound,
				OwningCluster: clusterResourceID,
			},
			serviceProviderCluster: nil,
			verify:                 verifyMarkedForCleanup(oneWeekFromNow),
		},
		{
			name: "Case 4: owningCluster does not exist and bindingState is Pending - delete immediately",
			dnsReservation: &api.DNSReservation{
				CosmosMetadata: api.CosmosMetadata{
					ResourceID: dnsReservationResourceID,
				},
				ResourceID:     dnsReservationResourceID,
				BindingState:   api.BindingStatePending,
				OwningCluster:  clusterResourceID,
				MustBindByTime: &metav1.Time{Time: oneHourFromNow},
			},
			serviceProviderCluster: nil,
			verify:                 verifyDeleted,
		},
		{
			name: "Case 5: owningCluster exists, points to this DNS reservation, state is Bound - steady state, no action",
			dnsReservation: &api.DNSReservation{
				CosmosMetadata: api.CosmosMetadata{
					ResourceID: dnsReservationResourceID,
				},
				ResourceID:    dnsReservationResourceID,
				BindingState:  api.BindingStateBound,
				OwningCluster: clusterResourceID,
			},
			serviceProviderCluster: &api.ServiceProviderCluster{
				CosmosMetadata: api.CosmosMetadata{
					ResourceID: serviceProviderClusterResourceID,
				},
				ResourceID: *serviceProviderClusterResourceID,
				Status: api.ServiceProviderClusterStatus{
					KubeAPIServerDNSReservation: dnsReservationResourceID,
				},
			},
			verify: func(t *testing.T, mockDBClient *databasetesting.MockDBClient) {
				dnsReservation, err := mockDBClient.DNSReservations(subscriptionID).Get(context.Background(), "my-dns-name")
				require.NoError(t, err)
				assert.Equal(t, api.BindingStateBound, dnsReservation.BindingState)
				assert.Nil(t, dnsReservation.CleanupTime)
			},
		},
		{
			name: "Case 6: owningCluster exists, points to this DNS reservation, state is Pending - fix state to Bound",
			dnsReservation: &api.DNSReservation{
				CosmosMetadata: api.CosmosMetadata{
					ResourceID: dnsReservationResourceID,
				},
				ResourceID:     dnsReservationResourceID,
				BindingState:   api.BindingStatePending,
				OwningCluster:  clusterResourceID,
				MustBindByTime: &metav1.Time{Time: oneHourFromNow},
			},
			serviceProviderCluster: &api.ServiceProviderCluster{
				CosmosMetadata: api.CosmosMetadata{
					ResourceID: serviceProviderClusterResourceID,
				},
				ResourceID: *serviceProviderClusterResourceID,
				Status: api.ServiceProviderClusterStatus{
					KubeAPIServerDNSReservation: dnsReservationResourceID,
				},
			},
			verify: func(t *testing.T, mockDBClient *databasetesting.MockDBClient) {
				dnsReservation, err := mockDBClient.DNSReservations(subscriptionID).Get(context.Background(), "my-dns-name")
				require.NoError(t, err)
				assert.Equal(t, api.BindingStateBound, dnsReservation.BindingState)
				assert.Nil(t, dnsReservation.CleanupTime)
				assert.Nil(t, dnsReservation.MustBindByTime)
			},
		},
		{
			name: "Case 7: owningCluster exists, KubeAPIServerDNSReservation is empty, state is Pending, mustBindByTime not expired - wait",
			dnsReservation: &api.DNSReservation{
				CosmosMetadata: api.CosmosMetadata{
					ResourceID: dnsReservationResourceID,
				},
				ResourceID:     dnsReservationResourceID,
				BindingState:   api.BindingStatePending,
				OwningCluster:  clusterResourceID,
				MustBindByTime: &metav1.Time{Time: oneHourFromNow},
			},
			serviceProviderCluster: &api.ServiceProviderCluster{
				CosmosMetadata: api.CosmosMetadata{
					ResourceID: serviceProviderClusterResourceID,
				},
				ResourceID: *serviceProviderClusterResourceID,
				Status: api.ServiceProviderClusterStatus{
					KubeAPIServerDNSReservation: nil, // empty
				},
			},
			verify: func(t *testing.T, mockDBClient *databasetesting.MockDBClient) {
				dnsReservation, err := mockDBClient.DNSReservations(subscriptionID).Get(context.Background(), "my-dns-name")
				require.NoError(t, err)
				assert.Equal(t, api.BindingStatePending, dnsReservation.BindingState)
				require.NotNil(t, dnsReservation.MustBindByTime)
				assert.True(t, oneHourFromNow.Equal(dnsReservation.MustBindByTime.Time), "expected mustBindByTime %v, got %v", oneHourFromNow, dnsReservation.MustBindByTime.Time)
			},
		},
		{
			name: "Case 8: owningCluster exists, KubeAPIServerDNSReservation is empty, state is Pending, mustBindByTime expired - delete",
			dnsReservation: &api.DNSReservation{
				CosmosMetadata: api.CosmosMetadata{
					ResourceID: dnsReservationResourceID,
				},
				ResourceID:     dnsReservationResourceID,
				BindingState:   api.BindingStatePending,
				OwningCluster:  clusterResourceID,
				MustBindByTime: &metav1.Time{Time: oneHourAgo},
			},
			serviceProviderCluster: &api.ServiceProviderCluster{
				CosmosMetadata: api.CosmosMetadata{
					ResourceID: serviceProviderClusterResourceID,
				},
				ResourceID: *serviceProviderClusterResourceID,
				Status: api.ServiceProviderClusterStatus{
					KubeAPIServerDNSReservation: nil, // empty
				},
			},
			verify: verifyDeleted,
		},
		{
			name: "Case 9: owningCluster exists, points to different DNS reservation, state is Pending - delete extra reservation",
			dnsReservation: &api.DNSReservation{
				CosmosMetadata: api.CosmosMetadata{
					ResourceID: dnsReservationResourceID,
				},
				ResourceID:     dnsReservationResourceID,
				BindingState:   api.BindingStatePending,
				OwningCluster:  clusterResourceID,
				MustBindByTime: &metav1.Time{Time: oneHourFromNow},
			},
			serviceProviderCluster: &api.ServiceProviderCluster{
				CosmosMetadata: api.CosmosMetadata{
					ResourceID: serviceProviderClusterResourceID,
				},
				ResourceID: *serviceProviderClusterResourceID,
				Status: api.ServiceProviderClusterStatus{
					KubeAPIServerDNSReservation: api.Must(api.ToDNSReservationResourceID(subscriptionID, "other-dns-name")),
				},
			},
			verify: verifyDeleted,
		},
		{
			name: "Case 10: owningCluster exists, points to different DNS reservation, state is Bound - mark for cleanup in one week",
			dnsReservation: &api.DNSReservation{
				CosmosMetadata: api.CosmosMetadata{
					ResourceID: dnsReservationResourceID,
				},
				ResourceID:    dnsReservationResourceID,
				BindingState:  api.BindingStateBound,
				OwningCluster: clusterResourceID,
			},
			serviceProviderCluster: &api.ServiceProviderCluster{
				CosmosMetadata: api.CosmosMetadata{
					ResourceID: serviceProviderClusterResourceID,
				},
				ResourceID: *serviceProviderClusterResourceID,
				Status: api.ServiceProviderClusterStatus{
					KubeAPIServerDNSReservation: api.Must(api.ToDNSReservationResourceID(subscriptionID, "other-dns-name")),
				},
			},
			verify: verifyMarkedForCleanup(oneWeekFromNow),
		},
		{
			name: "Case 10 variant: owningCluster exists, KubeAPIServerDNSReservation is empty, state is Bound - mark for cleanup in one week",
			dnsReservation: &api.DNSReservation{
				CosmosMetadata: api.CosmosMetadata{
					ResourceID: dnsReservationResourceID,
				},
				ResourceID:    dnsReservationResourceID,
				BindingState:  api.BindingStateBound,
				OwningCluster: clusterResourceID,
			},
			serviceProviderCluster: &api.ServiceProviderCluster{
				CosmosMetadata: api.CosmosMetadata{
					ResourceID: serviceProviderClusterResourceID,
				},
				ResourceID: *serviceProviderClusterResourceID,
				Status: api.ServiceProviderClusterStatus{
					KubeAPIServerDNSReservation: nil, // empty
				},
			},
			verify: verifyMarkedForCleanup(oneWeekFromNow),
		},
		{
			name:                   "DNS reservation not found - return nil (no error)",
			dnsReservation:         nil, // will not be stored
			serviceProviderCluster: nil,
			verify:                 verifyDeleted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mock DB client
			mockDBClient := databasetesting.NewMockDBClient()

			// Store DNS reservation if provided
			if tt.dnsReservation != nil {
				_, err := mockDBClient.DNSReservations(subscriptionID).Create(context.Background(), tt.dnsReservation, nil)
				require.NoError(t, err)
			}

			// Store ServiceProviderCluster if provided
			if tt.serviceProviderCluster != nil {
				_, err := mockDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName).Create(
					context.Background(), tt.serviceProviderCluster, nil)
				require.NoError(t, err)
			}

			// Create the controller with fake clock
			fakeClock := clocktesting.NewFakeClock(now)
			controller := &dnsReservationCleanupController{
				name:         "TestDNSReservationCleanupController",
				clock:        fakeClock,
				cosmosClient: mockDBClient,
			}

			// Build key
			key := DNSReservationKey{
				SubscriptionID:     subscriptionID,
				DNSReservationName: "my-dns-name",
			}

			// Create context with logger
			ctx := utils.ContextWithLogger(context.Background(), utils.DefaultLogger())

			// Run SyncOnce
			err := controller.SyncOnce(ctx, key)

			// Verify error expectation
			assert.NoError(t, err)

			// Run the test-specific verification
			tt.verify(t, mockDBClient)
		})
	}
}
