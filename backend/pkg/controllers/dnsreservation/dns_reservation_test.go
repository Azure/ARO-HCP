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
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tzvatot/go-clean-lang/pkg/cleanlang"

	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func listAllDNSReservations(ctx context.Context, t *testing.T, dbClient *corecosmosstoragetesting.MockResourcesDBClient, subscriptionID string) []*coreapi.DNSReservation {
	iter, err := dbClient.DNSReservations(subscriptionID).List(ctx, nil)
	require.NoError(t, err)
	var reservations []*coreapi.DNSReservation
	for _, r := range iter.Items(ctx) {
		reservations = append(reservations, r)
	}
	require.NoError(t, iter.GetError())
	return reservations
}

func newCreationController(clock *clocktesting.FakeClock, dbClient corecosmosstorage.ResourcesDBClient) *dnsReservationController {
	return &dnsReservationController{
		clock:                  clock,
		resourcesDBClient:      dbClient,
		rand:                   rand.New(rand.NewSource(42)), // fixed seed for determinism
		cleanLanguageValidator: cleanlang.NewValidator(),
	}
}

func TestDNSReservationController_SyncOnce(t *testing.T) {
	subscriptionID := "00000000-0000-0000-0000-000000000001"
	resourceGroupName := "test-rg"
	clusterName := "test-cluster"
	baseDomainPrefix := "myprefix"

	clusterResourceID := metadataapi.Must(coreapi.ToClusterResourceID(subscriptionID, resourceGroupName, clusterName))
	now := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)

	newCluster := func() *coreapi.HCPOpenShiftCluster {
		cluster := coreapi.NewDefaultHCPOpenShiftCluster(clusterResourceID, "eastus")
		cluster.ResourceID = clusterResourceID
		cluster.PartitionKey = strings.ToLower(subscriptionID)
		cluster.CustomerProperties.DNS.BaseDomainPrefix = baseDomainPrefix
		return cluster
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    subscriptionID,
		ResourceGroupName: resourceGroupName,
		HCPClusterName:    clusterName,
	}

	t.Run("reserves a DNS name and binds it to the cluster", func(t *testing.T) {
		mockDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
		fakeClock := clocktesting.NewFakeClock(now)

		_, err := mockDBClient.HCPClusters(subscriptionID, resourceGroupName).Create(context.Background(), newCluster(), nil)
		require.NoError(t, err)

		syncer := newCreationController(fakeClock, mockDBClient)
		ctx := utils.ContextWithLogger(context.Background(), utils.DefaultLogger())

		require.NoError(t, syncer.SyncOnce(ctx, key))

		reservations := listAllDNSReservations(ctx, t, mockDBClient, subscriptionID)
		require.Len(t, reservations, 1, "expected exactly one DNS reservation")
		reservation := reservations[0]
		assert.Equal(t, coreapi.BindingStateBound, reservation.BindingState)
		assert.Nil(t, reservation.MustBindByTime)
		require.NotNil(t, reservation.OwningCluster)
		assert.Equal(t, clusterResourceID.String(), reservation.OwningCluster.String())
		assert.True(t, strings.HasPrefix(reservation.GetResourceID().Name, baseDomainPrefix+"."),
			"expected DNS name %q to start with %q", reservation.GetResourceID().Name, baseDomainPrefix+".")

		spc, err := mockDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		require.NotNil(t, spc.Status.KubeAPIServerDNSReservation)
		assert.Equal(t, reservation.GetResourceID().String(), spc.Status.KubeAPIServerDNSReservation.String())
	})

	t.Run("already has a bound reservation - no new reservation", func(t *testing.T) {
		mockDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
		fakeClock := clocktesting.NewFakeClock(now)

		_, err := mockDBClient.HCPClusters(subscriptionID, resourceGroupName).Create(context.Background(), newCluster(), nil)
		require.NoError(t, err)

		// Pre-create the SPC already pointing at a reservation.
		spc, err := corecosmosstorage.GetOrCreateServiceProviderCluster(context.Background(), mockDBClient, clusterResourceID)
		require.NoError(t, err)
		spc.Status.KubeAPIServerDNSReservation = metadataapi.Must(coreapi.ToDNSReservationResourceID(subscriptionID, baseDomainPrefix+".zzzz"))
		_, err = mockDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName).Replace(context.Background(), spc, nil)
		require.NoError(t, err)

		syncer := newCreationController(fakeClock, mockDBClient)
		ctx := utils.ContextWithLogger(context.Background(), utils.DefaultLogger())

		require.NoError(t, syncer.SyncOnce(ctx, key))

		reservations := listAllDNSReservations(ctx, t, mockDBClient, subscriptionID)
		assert.Empty(t, reservations, "expected no new DNS reservation to be created")
	})

	t.Run("cluster without base domain prefix - no reservation", func(t *testing.T) {
		mockDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
		fakeClock := clocktesting.NewFakeClock(now)

		cluster := newCluster()
		cluster.CustomerProperties.DNS.BaseDomainPrefix = ""
		_, err := mockDBClient.HCPClusters(subscriptionID, resourceGroupName).Create(context.Background(), cluster, nil)
		require.NoError(t, err)

		syncer := newCreationController(fakeClock, mockDBClient)
		ctx := utils.ContextWithLogger(context.Background(), utils.DefaultLogger())

		require.NoError(t, syncer.SyncOnce(ctx, key))
		assert.Empty(t, listAllDNSReservations(ctx, t, mockDBClient, subscriptionID))
	})

	t.Run("cluster not found - returns nil", func(t *testing.T) {
		mockDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
		fakeClock := clocktesting.NewFakeClock(now)

		syncer := newCreationController(fakeClock, mockDBClient)
		ctx := utils.ContextWithLogger(context.Background(), utils.DefaultLogger())

		require.NoError(t, syncer.SyncOnce(ctx, key))
		assert.Empty(t, listAllDNSReservations(ctx, t, mockDBClient, subscriptionID))
	})
}
