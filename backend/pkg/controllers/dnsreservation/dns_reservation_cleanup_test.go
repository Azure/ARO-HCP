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

package dnsreservation

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestDNSReservationCleanupController_SyncOnce(t *testing.T) {
	subscriptionID := "00000000-0000-0000-0000-000000000001"
	resourceGroupName := "test-rg"
	clusterName := "test-cluster"
	dnsReservationName := "my-dns-name"

	clusterResourceID := metadataapi.Must(coreapi.ToClusterResourceID(subscriptionID, resourceGroupName, clusterName))
	dnsReservationResourceID := metadataapi.Must(coreapi.ToDNSReservationResourceID(subscriptionID, dnsReservationName))
	serviceProviderClusterResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		coreapi.ToServiceProviderClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)))

	now := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
	oneWeekFromNow := now.Add(oneWeek)
	oneHourAgo := now.Add(-1 * time.Hour)
	oneHourFromNow := now.Add(1 * time.Hour)

	// newDNSReservation builds a reservation document with PartitionKey set (the
	// storage layer requires it before Create).
	newDNSReservation := func() *coreapi.DNSReservation {
		return &coreapi.DNSReservation{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   dnsReservationResourceID,
				PartitionKey: strings.ToLower(subscriptionID),
			},
		}
	}

	verifyMarkedForCleanup := func(expectedCleanupTime time.Time) func(t *testing.T, mockDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
		return func(t *testing.T, mockDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
			dnsReservation, err := mockDBClient.DNSReservations(subscriptionID).Get(context.Background(), dnsReservationName)
			require.NoError(t, err)
			assert.Equal(t, coreapi.BindingStatePendingDeletion, dnsReservation.BindingState)
			require.NotNil(t, dnsReservation.CleanupTime)
			assert.True(t, expectedCleanupTime.Equal(dnsReservation.CleanupTime.Time), "expected cleanup time %v, got %v", expectedCleanupTime, dnsReservation.CleanupTime.Time)
			assert.Nil(t, dnsReservation.MustBindByTime)
		}
	}

	verifyDeleted := func(t *testing.T, mockDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
		_, err := mockDBClient.DNSReservations(subscriptionID).Get(context.Background(), dnsReservationName)
		assert.True(t, cosmosstorageutils.IsNotFoundError(err), "expected DNS reservation to be deleted, got err=%v", err)
	}

	// spcWithReservation builds a ServiceProviderCluster whose status points at the
	// given DNS reservation resource ID (nil pointer => no reservation).
	spcWithReservation := func(reservationID string) *coreapi.ServiceProviderCluster {
		spc := &coreapi.ServiceProviderCluster{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   serviceProviderClusterResourceID,
				PartitionKey: strings.ToLower(subscriptionID),
			},
		}
		if reservationID != "" {
			spc.Status.KubeAPIServerDNSReservation = metadataapi.Must(azcorearm.ParseResourceID(reservationID))
		}
		return spc
	}

	tests := []struct {
		name                   string
		dnsReservation         *coreapi.DNSReservation
		serviceProviderCluster *coreapi.ServiceProviderCluster
		verify                 func(t *testing.T, mockDBClient *corecosmosstoragetesting.MockResourcesDBClient)
	}{
		{
			name: "Case 1: cleanupTime in the past - delete the DNS reservation",
			dnsReservation: func() *coreapi.DNSReservation {
				r := newDNSReservation()
				r.BindingState = coreapi.BindingStatePendingDeletion
				r.CleanupTime = &metav1.Time{Time: oneHourAgo}
				return r
			}(),
			verify: verifyDeleted,
		},
		{
			name: "Case 2: cleanupTime in the future - no action",
			dnsReservation: func() *coreapi.DNSReservation {
				r := newDNSReservation()
				r.BindingState = coreapi.BindingStatePendingDeletion
				r.CleanupTime = &metav1.Time{Time: oneHourFromNow}
				return r
			}(),
			verify: func(t *testing.T, mockDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				dnsReservation, err := mockDBClient.DNSReservations(subscriptionID).Get(context.Background(), dnsReservationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.BindingStatePendingDeletion, dnsReservation.BindingState)
				require.NotNil(t, dnsReservation.CleanupTime)
				assert.True(t, oneHourFromNow.Equal(dnsReservation.CleanupTime.Time))
			},
		},
		{
			name: "Case 3: owningCluster gone and Bound - mark for cleanup in one week",
			dnsReservation: func() *coreapi.DNSReservation {
				r := newDNSReservation()
				r.BindingState = coreapi.BindingStateBound
				r.OwningCluster = clusterResourceID
				return r
			}(),
			serviceProviderCluster: nil,
			verify:                 verifyMarkedForCleanup(oneWeekFromNow),
		},
		{
			name: "Case 4: owningCluster gone and Pending - delete immediately",
			dnsReservation: func() *coreapi.DNSReservation {
				r := newDNSReservation()
				r.BindingState = coreapi.BindingStatePending
				r.OwningCluster = clusterResourceID
				r.MustBindByTime = &metav1.Time{Time: oneHourFromNow}
				return r
			}(),
			serviceProviderCluster: nil,
			verify:                 verifyDeleted,
		},
		{
			name: "Case 5: cluster points to this reservation and Bound - steady state",
			dnsReservation: func() *coreapi.DNSReservation {
				r := newDNSReservation()
				r.BindingState = coreapi.BindingStateBound
				r.OwningCluster = clusterResourceID
				return r
			}(),
			serviceProviderCluster: spcWithReservation(dnsReservationResourceID.String()),
			verify: func(t *testing.T, mockDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				dnsReservation, err := mockDBClient.DNSReservations(subscriptionID).Get(context.Background(), dnsReservationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.BindingStateBound, dnsReservation.BindingState)
				assert.Nil(t, dnsReservation.CleanupTime)
			},
		},
		{
			name: "Case 6: cluster points to this reservation but Pending - fix to Bound",
			dnsReservation: func() *coreapi.DNSReservation {
				r := newDNSReservation()
				r.BindingState = coreapi.BindingStatePending
				r.OwningCluster = clusterResourceID
				r.MustBindByTime = &metav1.Time{Time: oneHourFromNow}
				return r
			}(),
			serviceProviderCluster: spcWithReservation(dnsReservationResourceID.String()),
			verify: func(t *testing.T, mockDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				dnsReservation, err := mockDBClient.DNSReservations(subscriptionID).Get(context.Background(), dnsReservationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.BindingStateBound, dnsReservation.BindingState)
				assert.Nil(t, dnsReservation.CleanupTime)
				assert.Nil(t, dnsReservation.MustBindByTime)
			},
		},
		{
			name: "Case 7: cluster has no reservation, Pending, mustBindByTime not expired - wait",
			dnsReservation: func() *coreapi.DNSReservation {
				r := newDNSReservation()
				r.BindingState = coreapi.BindingStatePending
				r.OwningCluster = clusterResourceID
				r.MustBindByTime = &metav1.Time{Time: oneHourFromNow}
				return r
			}(),
			serviceProviderCluster: spcWithReservation(""),
			verify: func(t *testing.T, mockDBClient *corecosmosstoragetesting.MockResourcesDBClient) {
				dnsReservation, err := mockDBClient.DNSReservations(subscriptionID).Get(context.Background(), dnsReservationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.BindingStatePending, dnsReservation.BindingState)
				require.NotNil(t, dnsReservation.MustBindByTime)
			},
		},
		{
			name: "Case 8: cluster has no reservation, Pending, mustBindByTime expired - delete",
			dnsReservation: func() *coreapi.DNSReservation {
				r := newDNSReservation()
				r.BindingState = coreapi.BindingStatePending
				r.OwningCluster = clusterResourceID
				r.MustBindByTime = &metav1.Time{Time: oneHourAgo}
				return r
			}(),
			serviceProviderCluster: spcWithReservation(""),
			verify:                 verifyDeleted,
		},
		{
			name: "Case 9: cluster points to a different reservation, Pending - delete extra",
			dnsReservation: func() *coreapi.DNSReservation {
				r := newDNSReservation()
				r.BindingState = coreapi.BindingStatePending
				r.OwningCluster = clusterResourceID
				r.MustBindByTime = &metav1.Time{Time: oneHourFromNow}
				return r
			}(),
			serviceProviderCluster: spcWithReservation(coreapi.ToDNSReservationResourceIDString(subscriptionID, "other-dns-name")),
			verify:                 verifyDeleted,
		},
		{
			name: "Case 10: cluster points to a different reservation, Bound - mark for cleanup in one week",
			dnsReservation: func() *coreapi.DNSReservation {
				r := newDNSReservation()
				r.BindingState = coreapi.BindingStateBound
				r.OwningCluster = clusterResourceID
				return r
			}(),
			serviceProviderCluster: spcWithReservation(coreapi.ToDNSReservationResourceIDString(subscriptionID, "other-dns-name")),
			verify:                 verifyMarkedForCleanup(oneWeekFromNow),
		},
		{
			name: "Case 10 variant: cluster has no reservation, Bound - mark for cleanup in one week",
			dnsReservation: func() *coreapi.DNSReservation {
				r := newDNSReservation()
				r.BindingState = coreapi.BindingStateBound
				r.OwningCluster = clusterResourceID
				return r
			}(),
			serviceProviderCluster: spcWithReservation(""),
			verify:                 verifyMarkedForCleanup(oneWeekFromNow),
		},
		{
			name:           "DNS reservation not found - return nil",
			dnsReservation: nil,
			verify:         verifyDeleted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()

			if tt.dnsReservation != nil {
				_, err := mockDBClient.DNSReservations(subscriptionID).Create(context.Background(), tt.dnsReservation, nil)
				require.NoError(t, err)
			}
			if tt.serviceProviderCluster != nil {
				_, err := mockDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName).Create(
					context.Background(), tt.serviceProviderCluster, nil)
				require.NoError(t, err)
			}

			fakeClock := clocktesting.NewFakeClock(now)
			controller := &dnsReservationCleanupController{
				name:              "TestDNSReservationCleanupController",
				clock:             fakeClock,
				resourcesDBClient: mockDBClient,
			}

			key := DNSReservationKey{
				SubscriptionID:     subscriptionID,
				DNSReservationName: dnsReservationName,
			}

			ctx := utils.ContextWithLogger(context.Background(), utils.DefaultLogger())
			err := controller.SyncOnce(ctx, key)
			assert.NoError(t, err)

			tt.verify(t, mockDBClient)
		})
	}
}
