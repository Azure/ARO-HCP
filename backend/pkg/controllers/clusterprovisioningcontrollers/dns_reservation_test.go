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
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tzvatot/go-clean-lang/pkg/cleanlang"

	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// findDNSReservationByOwner finds a DNS reservation by its owning cluster.
// Returns the first matching DNS reservation or nil if none found.
func findDNSReservationByOwner(ctx context.Context, dbClient *databasetesting.MockDBClient, subscriptionID string, owningClusterResourceID *azcorearm.ResourceID) *api.DNSReservation {
	iter, err := dbClient.DNSReservations(subscriptionID).List(ctx, nil)
	if err != nil {
		return nil
	}
	for _, dnsReservation := range iter.Items(ctx) {
		if dnsReservation.OwningCluster != nil && dnsReservation.OwningCluster.String() == owningClusterResourceID.String() {
			return dnsReservation
		}
	}
	return nil
}

// listAllDNSReservations returns all DNS reservations in the subscription.
func listAllDNSReservations(ctx context.Context, dbClient *databasetesting.MockDBClient, subscriptionID string) []*api.DNSReservation {
	iter, err := dbClient.DNSReservations(subscriptionID).List(ctx, nil)
	if err != nil {
		return nil
	}
	var reservations []*api.DNSReservation
	for _, dnsReservation := range iter.Items(ctx) {
		reservations = append(reservations, dnsReservation)
	}
	return reservations
}

func TestDNSReservationController_SyncOnce(t *testing.T) {
	subscriptionID := "test-subscription"
	resourceGroupName := "test-rg"
	clusterName := "test-cluster"
	baseDomainPrefix := "my-dns-name"

	// Parse resource IDs
	clusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID + "/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/" + clusterName))

	serviceProviderClusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID + "/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/" + clusterName +
			"/serviceProviderClusters/default"))

	now := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)

	// Create internal ID for ClusterServiceID
	internalID := api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/test-cluster-id"))

	// Create a basic HCPCluster for testing
	createHCPCluster := func() *api.HCPOpenShiftCluster {
		return &api.HCPOpenShiftCluster{
			TrackedResource: arm.TrackedResource{
				Resource: arm.Resource{
					ID:   clusterResourceID,
					Name: clusterName,
					Type: api.ClusterResourceType.String(),
				},
				Location: "eastus",
			},
			CustomerProperties: api.HCPOpenShiftClusterCustomerProperties{
				DNS: api.CustomerDNSProfile{
					BaseDomainPrefix: baseDomainPrefix,
				},
			},
			ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
				ProvisioningState: arm.ProvisioningStateSucceeded,
				ClusterServiceID:  internalID,
			},
		}
	}

	// Helper to create controller with proper fields
	createController := func(clock *clocktesting.FakeClock, dbClient database.DBClient) *dnsReservationController {
		return &dnsReservationController{
			clock:                  clock,
			cooldownChecker:        &noOpCooldownChecker{},
			cosmosClient:           dbClient,
			rand:                   rand.New(rand.NewSource(42)), // fixed seed for determinism
			cleanLanguageValidator: cleanlang.NewValidator(),
		}
	}

	// Create a ServiceProviderCluster without DNS reservation
	createServiceProviderCluster := func() *api.ServiceProviderCluster {
		return &api.ServiceProviderCluster{
			CosmosMetadata: api.CosmosMetadata{
				ResourceID: serviceProviderClusterResourceID,
			},
			ResourceID: *serviceProviderClusterResourceID,
		}
	}

	t.Run("Success case - creates DNS reservation and updates cluster", func(t *testing.T) {
		mockDBClient := databasetesting.NewMockDBClient()
		fakeClock := clocktesting.NewFakeClock(now)

		// Store HCPCluster
		hcpCluster := createHCPCluster()
		_, err := mockDBClient.HCPClusters(subscriptionID, resourceGroupName).Create(context.Background(), hcpCluster, nil)
		require.NoError(t, err)

		// Store ServiceProviderCluster without DNS reservation
		spc := createServiceProviderCluster()
		_, err = mockDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName).Create(context.Background(), spc, nil)
		require.NoError(t, err)

		// Create controller
		syncer := createController(fakeClock, mockDBClient)

		key := controllerutils.HCPClusterKey{
			SubscriptionID:    subscriptionID,
			ResourceGroupName: resourceGroupName,
			HCPClusterName:    clusterName,
		}

		ctx := utils.ContextWithLogger(context.Background(), utils.DefaultLogger())

		// Run SyncOnce
		err = syncer.SyncOnce(ctx, key)
		require.NoError(t, err)

		// Verify DNS reservation was created and is bound
		dnsReservation := findDNSReservationByOwner(ctx, mockDBClient, subscriptionID, clusterResourceID)
		require.NotNil(t, dnsReservation, "expected DNS reservation to be created")
		assert.Equal(t, api.BindingStateBound, dnsReservation.BindingState)
		assert.Nil(t, dnsReservation.MustBindByTime)
		assert.Equal(t, clusterResourceID.String(), dnsReservation.OwningCluster.String())
		// Verify DNS name starts with baseDomainPrefix
		assert.True(t, strings.HasPrefix(dnsReservation.ResourceID.Name, baseDomainPrefix+"."),
			"expected DNS name %q to start with %q", dnsReservation.ResourceID.Name, baseDomainPrefix+".")

		// Verify ServiceProviderCluster was updated with DNS reservation
		updatedSPC, err := mockDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName).Get(context.Background(), api.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		require.NotNil(t, updatedSPC.Status.KubeAPIServerDNSReservation)
		// The SPC should reference the same DNS reservation
		assert.Equal(t, dnsReservation.ResourceID.String(), updatedSPC.Status.KubeAPIServerDNSReservation.String())
	})

	t.Run("Already has DNS reservation - no action", func(t *testing.T) {
		mockDBClient := databasetesting.NewMockDBClient()
		fakeClock := clocktesting.NewFakeClock(now)

		// Create a pre-existing DNS reservation
		existingDNSName := baseDomainPrefix + ".existing"
		existingDNSReservationResourceID := api.Must(api.ToDNSReservationResourceID(subscriptionID, existingDNSName))

		// Store HCPCluster
		hcpCluster := createHCPCluster()
		_, err := mockDBClient.HCPClusters(subscriptionID, resourceGroupName).Create(context.Background(), hcpCluster, nil)
		require.NoError(t, err)

		// Store ServiceProviderCluster WITH DNS reservation already set
		spc := createServiceProviderCluster()
		spc.Status.KubeAPIServerDNSReservation = existingDNSReservationResourceID
		_, err = mockDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName).Create(context.Background(), spc, nil)
		require.NoError(t, err)

		// Create controller
		syncer := createController(fakeClock, mockDBClient)

		key := controllerutils.HCPClusterKey{
			SubscriptionID:    subscriptionID,
			ResourceGroupName: resourceGroupName,
			HCPClusterName:    clusterName,
		}

		ctx := utils.ContextWithLogger(context.Background(), utils.DefaultLogger())

		// Run SyncOnce
		err = syncer.SyncOnce(ctx, key)
		require.NoError(t, err)

		// Verify no new DNS reservation was created
		allReservations := listAllDNSReservations(ctx, mockDBClient, subscriptionID)
		assert.Empty(t, allReservations, "expected no new DNS reservations to be created")
	})

	t.Run("HCPCluster not found - returns nil", func(t *testing.T) {
		mockDBClient := databasetesting.NewMockDBClient()
		fakeClock := clocktesting.NewFakeClock(now)

		// Don't store any HCPCluster

		// Create controller
		syncer := createController(fakeClock, mockDBClient)

		key := controllerutils.HCPClusterKey{
			SubscriptionID:    subscriptionID,
			ResourceGroupName: resourceGroupName,
			HCPClusterName:    clusterName,
		}

		ctx := utils.ContextWithLogger(context.Background(), utils.DefaultLogger())

		// Run SyncOnce
		err := syncer.SyncOnce(ctx, key)
		require.NoError(t, err)

		// Verify no DNS reservation was created
		allReservations := listAllDNSReservations(ctx, mockDBClient, subscriptionID)
		assert.Empty(t, allReservations)
	})

	t.Run("ServiceProviderCluster Replace fails - returns error, DNS reservation left in Pending state", func(t *testing.T) {
		mockDBClient := databasetesting.NewMockDBClient()
		fakeClock := clocktesting.NewFakeClock(now)

		// Store HCPCluster
		hcpCluster := createHCPCluster()
		_, err := mockDBClient.HCPClusters(subscriptionID, resourceGroupName).Create(context.Background(), hcpCluster, nil)
		require.NoError(t, err)

		// Store ServiceProviderCluster without DNS reservation
		spc := createServiceProviderCluster()
		_, err = mockDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName).Create(context.Background(), spc, nil)
		require.NoError(t, err)

		// Set up interceptor to fail ServiceProviderCluster Replace
		mockDBClient.SetReplaceInterceptor(func(resourceType, resourceName string) error {
			if strings.Contains(resourceType, "serviceProviderCluster") {
				return fmt.Errorf("simulated Replace failure")
			}
			return nil
		})

		// Create controller
		syncer := createController(fakeClock, mockDBClient)

		key := controllerutils.HCPClusterKey{
			SubscriptionID:    subscriptionID,
			ResourceGroupName: resourceGroupName,
			HCPClusterName:    clusterName,
		}

		ctx := utils.ContextWithLogger(context.Background(), utils.DefaultLogger())

		// Run SyncOnce - should fail
		err = syncer.SyncOnce(ctx, key)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update service provider cluster")

		// Verify DNS reservation was created but is still in Pending state
		dnsReservation := findDNSReservationByOwner(ctx, mockDBClient, subscriptionID, clusterResourceID)
		require.NotNil(t, dnsReservation, "expected DNS reservation to be created")
		assert.Equal(t, api.BindingStatePending, dnsReservation.BindingState)
		require.NotNil(t, dnsReservation.MustBindByTime)
		assert.True(t, strings.HasPrefix(dnsReservation.ResourceID.Name, baseDomainPrefix+"."),
			"expected DNS name to start with baseDomainPrefix")

		// Verify ServiceProviderCluster was NOT updated with DNS reservation
		updatedSPC, err := mockDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName).Get(context.Background(), api.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		assert.Nil(t, updatedSPC.Status.KubeAPIServerDNSReservation)
	})

	t.Run("ServiceProviderCluster Replace fails then succeeds on retry", func(t *testing.T) {
		mockDBClient := databasetesting.NewMockDBClient()
		fakeClock := clocktesting.NewFakeClock(now)

		// Store HCPCluster
		hcpCluster := createHCPCluster()
		_, err := mockDBClient.HCPClusters(subscriptionID, resourceGroupName).Create(context.Background(), hcpCluster, nil)
		require.NoError(t, err)

		// Store ServiceProviderCluster without DNS reservation
		spc := createServiceProviderCluster()
		_, err = mockDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName).Create(context.Background(), spc, nil)
		require.NoError(t, err)

		// Track call count
		callCount := 0

		// Set up interceptor to fail ServiceProviderCluster Replace on first call only
		mockDBClient.SetReplaceInterceptor(func(resourceType, resourceName string) error {
			if strings.Contains(resourceType, "serviceProviderCluster") {
				callCount++
				if callCount == 1 {
					return fmt.Errorf("simulated Replace failure")
				}
			}
			return nil
		})

		// Create controller
		syncer := createController(fakeClock, mockDBClient)

		key := controllerutils.HCPClusterKey{
			SubscriptionID:    subscriptionID,
			ResourceGroupName: resourceGroupName,
			HCPClusterName:    clusterName,
		}

		ctx := utils.ContextWithLogger(context.Background(), utils.DefaultLogger())

		// First call - should fail
		err = syncer.SyncOnce(ctx, key)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update service provider cluster")

		// Verify DNS reservation was created in Pending state
		firstDNSReservation := findDNSReservationByOwner(ctx, mockDBClient, subscriptionID, clusterResourceID)
		require.NotNil(t, firstDNSReservation, "expected DNS reservation to be created")
		assert.Equal(t, api.BindingStatePending, firstDNSReservation.BindingState)
		firstDNSName := firstDNSReservation.ResourceID.Name

		// Second call - creates a NEW DNS reservation with different random name and succeeds
		// because the SPC Replace interceptor only fails on the first call
		err = syncer.SyncOnce(ctx, key)
		require.NoError(t, err)

		// Verify a second DNS reservation was created and is now Bound
		// The SPC now points to the second reservation
		updatedSPC, err := mockDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName).Get(context.Background(), api.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		require.NotNil(t, updatedSPC.Status.KubeAPIServerDNSReservation)

		// Verify the SPC points to a different DNS reservation than the first one
		assert.NotEqual(t, firstDNSName, updatedSPC.Status.KubeAPIServerDNSReservation.Name,
			"expected SPC to point to a different DNS reservation")

		// The first DNS reservation is orphaned (still in Pending state)
		// It will be cleaned up by the cleanup controller
		allReservations := listAllDNSReservations(ctx, mockDBClient, subscriptionID)
		assert.Len(t, allReservations, 2, "expected two DNS reservations (one orphaned, one active)")
	})

	t.Run("DNS reservation bindingState update fails - cleanup controller fixes it", func(t *testing.T) {
		mockDBClient := databasetesting.NewMockDBClient()
		fakeClock := clocktesting.NewFakeClock(now)

		// Store HCPCluster
		hcpCluster := createHCPCluster()
		_, err := mockDBClient.HCPClusters(subscriptionID, resourceGroupName).Create(context.Background(), hcpCluster, nil)
		require.NoError(t, err)

		// Store ServiceProviderCluster without DNS reservation
		spc := createServiceProviderCluster()
		_, err = mockDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName).Create(context.Background(), spc, nil)
		require.NoError(t, err)

		// Set up interceptor to fail DNS reservation Replace (bindingState update)
		mockDBClient.SetReplaceInterceptor(func(resourceType, resourceName string) error {
			if strings.Contains(strings.ToLower(resourceType), "dnsreservations") {
				return fmt.Errorf("simulated DNS reservation Replace failure")
			}
			return nil
		})

		// Create controller
		syncer := createController(fakeClock, mockDBClient)

		key := controllerutils.HCPClusterKey{
			SubscriptionID:    subscriptionID,
			ResourceGroupName: resourceGroupName,
			HCPClusterName:    clusterName,
		}

		ctx := utils.ContextWithLogger(context.Background(), utils.DefaultLogger())

		// Run SyncOnce - should succeed (DNS reservation Replace failure is best-effort)
		err = syncer.SyncOnce(ctx, key)
		require.NoError(t, err)

		// Verify DNS reservation is still in Pending state (best-effort update failed)
		dnsReservation := findDNSReservationByOwner(ctx, mockDBClient, subscriptionID, clusterResourceID)
		require.NotNil(t, dnsReservation, "expected DNS reservation to be created")
		assert.Equal(t, api.BindingStatePending, dnsReservation.BindingState)

		// Verify ServiceProviderCluster was updated with DNS reservation
		updatedSPC, err := mockDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName).Get(context.Background(), api.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		require.NotNil(t, updatedSPC.Status.KubeAPIServerDNSReservation)

		// Clear the interceptor so cleanup controller can update the DNS reservation
		mockDBClient.SetReplaceInterceptor(nil)

		// Run cleanup controller to fix the bindingState
		cleanupController := &dnsReservationCleanupController{
			name:         "TestDNSReservationCleanupController",
			clock:        fakeClock,
			cosmosClient: mockDBClient,
		}

		// Use the actual DNS reservation name for the cleanup key
		cleanupKey := DNSReservationKey{
			SubscriptionID:     subscriptionID,
			DNSReservationName: dnsReservation.ResourceID.Name,
		}

		err = cleanupController.SyncOnce(ctx, cleanupKey)
		require.NoError(t, err)

		// Verify DNS reservation is now Bound (cleanup controller fixed it - Case 6)
		dnsReservation = findDNSReservationByOwner(ctx, mockDBClient, subscriptionID, clusterResourceID)
		require.NotNil(t, dnsReservation)
		assert.Equal(t, api.BindingStateBound, dnsReservation.BindingState)
		assert.Nil(t, dnsReservation.MustBindByTime)
	})

	t.Run("Complex failure - ServiceProviderCluster Replace fails, then succeeds on retry with DNS state update failure", func(t *testing.T) {
		mockDBClient := databasetesting.NewMockDBClient()
		fakeClock := clocktesting.NewFakeClock(now)

		// Store HCPCluster
		hcpCluster := createHCPCluster()
		_, err := mockDBClient.HCPClusters(subscriptionID, resourceGroupName).Create(context.Background(), hcpCluster, nil)
		require.NoError(t, err)

		// Store ServiceProviderCluster without DNS reservation
		spc := createServiceProviderCluster()
		_, err = mockDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName).Create(context.Background(), spc, nil)
		require.NoError(t, err)

		spcReplaceCallCount := 0

		// Set up interceptor:
		// - First ServiceProviderCluster Replace: fail
		// - Second ServiceProviderCluster Replace: succeed
		// - DNS reservation Replace: fail (best effort, doesn't cause error)
		mockDBClient.SetReplaceInterceptor(func(resourceType, resourceName string) error {
			if strings.Contains(resourceType, "serviceProviderCluster") {
				spcReplaceCallCount++
				if spcReplaceCallCount == 1 {
					return fmt.Errorf("simulated ServiceProviderCluster Replace failure")
				}
			}
			if strings.Contains(strings.ToLower(resourceType), "dnsreservations") {
				return fmt.Errorf("simulated DNS reservation Replace failure")
			}
			return nil
		})

		// Create controller
		syncer := createController(fakeClock, mockDBClient)

		key := controllerutils.HCPClusterKey{
			SubscriptionID:    subscriptionID,
			ResourceGroupName: resourceGroupName,
			HCPClusterName:    clusterName,
		}

		ctx := utils.ContextWithLogger(context.Background(), utils.DefaultLogger())

		// First call - ServiceProviderCluster Replace fails
		// DNS reservation is created in Pending state
		err = syncer.SyncOnce(ctx, key)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update service provider cluster")

		// Verify first DNS reservation exists in Pending state
		firstDNSReservation := findDNSReservationByOwner(ctx, mockDBClient, subscriptionID, clusterResourceID)
		require.NotNil(t, firstDNSReservation, "expected DNS reservation to be created")
		assert.Equal(t, api.BindingStatePending, firstDNSReservation.BindingState)
		require.NotNil(t, firstDNSReservation.MustBindByTime)
		firstDNSReservationName := firstDNSReservation.ResourceID.Name

		// ServiceProviderCluster still has no DNS reservation
		updatedSPC, err := mockDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName).Get(context.Background(), api.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		assert.Nil(t, updatedSPC.Status.KubeAPIServerDNSReservation)

		// Second call - creates new DNS reservation, SPC update succeeds, DNS state update fails (best effort)
		err = syncer.SyncOnce(ctx, key)
		require.NoError(t, err)

		// Verify SPC was updated with the new DNS reservation
		updatedSPC, err = mockDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName).Get(context.Background(), api.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		require.NotNil(t, updatedSPC.Status.KubeAPIServerDNSReservation)

		// The SPC points to a different (second) DNS reservation
		assert.NotEqual(t, firstDNSReservationName, updatedSPC.Status.KubeAPIServerDNSReservation.Name)

		// Clear interceptor for cleanup controller
		mockDBClient.SetReplaceInterceptor(nil)

		// Advance time past mustBindByTime to trigger cleanup of orphaned first reservation
		oneHourLater := now.Add(2 * time.Hour)
		fakeClockAdvanced := clocktesting.NewFakeClock(oneHourLater)

		// Run cleanup controller on the first (orphaned) DNS reservation
		cleanupController := &dnsReservationCleanupController{
			name:         "TestDNSReservationCleanupController",
			clock:        fakeClockAdvanced,
			cosmosClient: mockDBClient,
		}

		cleanupKey := DNSReservationKey{
			SubscriptionID:     subscriptionID,
			DNSReservationName: firstDNSReservationName,
		}

		// The first DNS reservation has mustBindByTime in the past
		// and ServiceProviderCluster points to a different reservation
		// Case 9: owningCluster exists, points to different DNS reservation, state is Pending - delete extra reservation
		err = cleanupController.SyncOnce(ctx, cleanupKey)
		require.NoError(t, err)

		// Verify only one DNS reservation remains (the one SPC points to)
		allReservations := listAllDNSReservations(ctx, mockDBClient, subscriptionID)
		assert.Len(t, allReservations, 1, "expected only one DNS reservation to remain")
		assert.Equal(t, updatedSPC.Status.KubeAPIServerDNSReservation.Name, allReservations[0].ResourceID.Name)
	})
}

// noOpCooldownChecker is a CooldownChecker that always allows syncing
type noOpCooldownChecker struct{}

func (c *noOpCooldownChecker) CanSync(ctx context.Context, key any) bool {
	return true
}

var _ controllerutils.CooldownChecker = &noOpCooldownChecker{}
