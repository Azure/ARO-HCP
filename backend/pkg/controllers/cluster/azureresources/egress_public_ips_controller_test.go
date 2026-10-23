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

package azureresources

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

type egressStatusNotifier struct {
	handler cache.ResourceEventHandler
}

func (notifier *egressStatusNotifier) AddEventHandlerWithOptions(handler cache.ResourceEventHandler, _ cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error) {
	notifier.handler = handler
	return nil, nil
}

type egressEventSyncer struct {
	*egressPublicIPsSyncer
	permits chan struct{}
	results chan error
}

func (syncer *egressEventSyncer) MakeKey(resourceID *azcorearm.ResourceID) controllerutils.HCPClusterKey {
	return controllerutils.HCPClusterKey{SubscriptionID: resourceID.SubscriptionID, ResourceGroupName: resourceID.ResourceGroupName, HCPClusterName: resourceID.Name}
}

func (syncer *egressEventSyncer) CooldownChecker() controllerutil.CooldownChecker { return nil }

func (syncer *egressEventSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	select {
	case <-ctx.Done():
		return nil
	case <-syncer.permits:
	}
	err := syncer.egressPublicIPsSyncer.SyncOnce(ctx, key)
	syncer.results <- err
	return err
}

func TestEgressPublicIPsMixedResultsStatusEvents(t *testing.T) {
	for _, status := range []int{429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cluster := newTestCluster(false)
			clusterID := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123"))
			cluster.ServiceProviderProperties.ClusterServiceID = &clusterID
			expected, err := coreapihelpers.EgressPublicIPResourceIDs(cluster)
			require.NoError(t, err)
			serviceProvider := newTestServiceProviderCluster(coreapi.AzureReference{})
			database, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProvider})
			require.NoError(t, err)
			crud := database.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
			stored := func() *coreapi.ServiceProviderCluster {
				value, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				return value
			}
			mockController := gomock.NewController(t)
			builder := azureclient.NewMockFirstPartyApplicationClientBuilder(mockController)
			client := azureclient.NewMockPublicIPAddressesClient(mockController)
			builder.EXPECT().PublicIPAddressesClient(testTenantID, testSubscriptionID).Return(client, nil).AnyTimes()
			getCalls := 0
			recovered := false
			for index, resourceID := range expected {
				client.EXPECT().Get(gomock.Any(), testManagedRGName, resourceID.Name, nil).DoAndReturn(func(context.Context, string, string, *armnetwork.PublicIPAddressesClientGetOptions) (armnetwork.PublicIPAddressesClientGetResponse, error) {
					getCalls++
					var getErr error
					if !recovered && index == 0 {
						getErr = &azcore.ResponseError{StatusCode: 404, ErrorCode: "ResourceNotFound"}
					} else if !recovered && index == 1 {
						getErr = &azcore.ResponseError{StatusCode: status}
					}
					return armnetwork.PublicIPAddressesClientGetResponse{}, getErr
				}).AnyTimes()
			}
			clock := clocktesting.NewFakePassiveClock(time.Now())
			enqueuer := &fakeAfterEnqueuer{}
			syncer := &egressEventSyncer{
				egressPublicIPsSyncer: &egressPublicIPsSyncer{
					clock:                        clock,
					resourcesDBClient:            database,
					clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: database},
					serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: database},
					subscriptionLister:           &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{newTestSubscription(ptr.To(testTenantID))}},
					azureFPAClientBuilder:        builder,
					enqueueAfter:                 enqueuer,
				},
				permits: make(chan struct{}),
				results: make(chan error, 1),
			}
			controller := controllerutil.NewGenericWatchingController(EgressPublicIPsControllerName, coreapi.ClusterResourceType, syncer, nil)
			controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)
			notifier := &egressStatusNotifier{}
			require.NoError(t, controller.QueueForInformers(0, notifier))
			done := make(chan struct{})
			go func() {
				defer close(done)
				controller.Run(ctx, 1)
			}()
			defer func() {
				cancel()
				<-done
			}()
			runQueued := func() error {
				select {
				case syncer.permits <- struct{}{}:
				case <-ctx.Done():
					t.Fatal("status event did not reach the worker")
				}
				select {
				case err := <-syncer.results:
					return err
				case <-ctx.Done():
					t.Fatal("worker did not complete")
					return ctx.Err()
				}
			}
			initial := stored()
			notifier.handler.OnAdd(initial, false)
			require.Error(t, runQueued())
			observed := stored()
			require.NotEqual(t, initial.CosmosETag, observed.CosmosETag)
			require.Equal(t, coreapi.AzureResourceNotFound, observed.Status.AzureResources.EgressPublicIPs[expected[0].String()])
			require.Equal(t, coreapi.AzureResourceUnknown, observed.Status.AzureResources.EgressPublicIPs[expected[1].String()])
			require.Len(t, enqueuer.enqueuedDurations, 1)
			delay := enqueuer.enqueuedDurations[0]
			require.GreaterOrEqual(t, delay, time.Minute)
			require.Less(t, delay, 90*time.Second)
			recheck := observed.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName]
			require.NotNil(t, recheck)
			require.WithinDuration(t, clock.Now().Add(delay), recheck.Time, time.Second)
			clock.SetTime(recheck.Add(-time.Second))
			for range 3 {
				notifier.handler.OnUpdate(initial, observed)
				require.NoError(t, runQueued())
				require.Equal(t, 9, getCalls, "status events must not repeat ARM reads during cooldown")
				require.Equal(t, observed.CosmosETag, stored().CosmosETag, "status events must not replace the document during cooldown")
			}
			require.Len(t, enqueuer.enqueuedDurations, 4)
			require.Equal(t, time.Second, enqueuer.enqueuedDurations[3], "early events must preserve the explicit retry after error backoff is forgotten")
			clock.SetTime(recheck.Time)
			controller.Enqueue(enqueuer.enqueuedKeys[3])
			require.Error(t, runQueued())
			require.Equal(t, 18, getCalls, "cooldown expiry must allow exactly one nine-GET pass")
			retried := stored()
			require.NotEqual(t, observed.CosmosETag, retried.CosmosETag)
			recovered = true
			clock.SetTime(retried.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName].Time)
			controller.Enqueue(enqueuer.enqueuedKeys[4])
			require.NoError(t, runQueued())
			require.Equal(t, 27, getCalls)
			completed := stored()
			for _, observation := range completed.Status.AzureResources.EgressPublicIPs {
				require.Equal(t, coreapi.AzureResourceFound, observation)
			}
			require.Nil(t, completed.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName])
			notifier.handler.OnUpdate(retried, completed)
			require.NoError(t, runQueued())
			require.Equal(t, 27, getCalls)
			require.Len(t, enqueuer.enqueuedKeys, 5, "completed observations must not schedule retries")
			require.Equal(t, completed.CosmosETag, stored().CosmosETag)
		})
	}
}

func TestEgressPublicIPsSyncOnce(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		found          int
		status         int
		errorCode      string
		builderFailure bool
		conflict       bool
	}{
		{name: "zero", status: 404},
		{name: "resource group absent", status: 404, errorCode: "ResourceGroupNotFound"},
		{name: "unrecognized 404", status: 404, errorCode: "NoRegisteredProviderFound"},
		{name: "partial", found: 4, status: 404},
		{name: "all", found: 9},
		{name: "authorization", found: 4, status: 403},
		{name: "throttling", status: 429},
		{name: "service failure", status: 503},
		{name: "transport failure", status: -1},
		{name: "client failure clears stale missing", builderFailure: true},
		{name: "persistence conflict", found: 9, conflict: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			readFailure := testCase.status != 0 && (testCase.status != 404 || testCase.errorCode == "NoRegisteredProviderFound")
			ctx := context.Background()
			cluster := newTestCluster(false)
			clusterID := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123"))
			cluster.ServiceProviderProperties.ClusterServiceID = &clusterID
			expected, err := coreapihelpers.EgressPublicIPResourceIDs(cluster)
			require.NoError(t, err)
			require.Len(t, expected, 9)
			serviceProvider := newTestServiceProviderCluster(coreapi.AzureReference{})
			serviceProvider.Status.AzureResources.EgressPublicIPs = map[string]coreapi.AzureResourceObservation{"unrelated": coreapi.AzureResourceFound}
			for index, resourceID := range expected {
				require.Equal(t, fmt.Sprintf("abc123-%d", index), resourceID.Name)
				require.Equal(t, testManagedRGName, resourceID.ResourceGroupName)
				serviceProvider.Status.AzureResources.EgressPublicIPs[resourceID.String()] = coreapi.AzureResourceNotFound
			}
			database, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProvider})
			require.NoError(t, err)
			mockController := gomock.NewController(t)
			builder := azureclient.NewMockFirstPartyApplicationClientBuilder(mockController)
			client := azureclient.NewMockPublicIPAddressesClient(mockController)
			if testCase.builderFailure {
				builder.EXPECT().PublicIPAddressesClient(testTenantID, testSubscriptionID).Return(nil, errors.New("credential failure"))
			} else {
				calls := 1
				if readFailure {
					calls = 2
				}
				builder.EXPECT().PublicIPAddressesClient(testTenantID, testSubscriptionID).Return(client, nil).Times(calls)
				for index, resourceID := range expected {
					var getErr error
					if index >= testCase.found {
						errorCode := testCase.errorCode
						if errorCode == "" {
							errorCode = "ResourceNotFound"
						}
						getErr = &azcore.ResponseError{StatusCode: testCase.status, ErrorCode: errorCode}
						if testCase.status == -1 {
							getErr = errors.New("transport failure")
						}
					}
					client.EXPECT().Get(gomock.Any(), testManagedRGName, resourceID.Name, nil).Return(armnetwork.PublicIPAddressesClientGetResponse{}, getErr).Times(calls)
				}
			}
			syncer := &egressPublicIPsSyncer{
				clock:                        clocktesting.NewFakePassiveClock(time.Now()),
				resourcesDBClient:            database,
				clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: database},
				serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: database},
				subscriptionLister:           &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{newTestSubscription(ptr.To(testTenantID))}},
				azureFPAClientBuilder:        builder,
			}
			if testCase.conflict {
				serviceProvider.CosmosETag = "stale"
				syncer.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{serviceProvider}}
			}
			key := controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: testClusterName}
			err = syncer.SyncOnce(ctx, key)
			if testCase.conflict {
				require.True(t, cosmosstorageutils.IsPreconditionFailedError(err))
				syncer.serviceProviderClusterLister = &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: database}
				require.NoError(t, syncer.SyncOnce(ctx, key))
			} else if testCase.builderFailure || readFailure {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			updated, err := database.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			require.Len(t, updated.Status.AzureResources.EgressPublicIPs, 9)
			for index, resourceID := range expected {
				want := coreapi.AzureResourceUnknown
				if index < testCase.found {
					want = coreapi.AzureResourceFound
				} else if testCase.status == 404 && !readFailure {
					want = coreapi.AzureResourceNotFound
				}
				require.Equal(t, want, updated.Status.AzureResources.EgressPublicIPs[resourceID.String()])
			}
			if !testCase.builderFailure {
				retryErr := syncer.SyncOnce(ctx, key)
				if readFailure {
					require.Error(t, retryErr)
				} else {
					require.NoError(t, retryErr)
				}
				again, err := database.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				require.Equal(t, updated.CosmosETag, again.CosmosETag, "unchanged observations must not write")
			}
		})
	}
}

func TestEgressPublicIPsPrerequisites(t *testing.T) {
	for _, name := range []string{"UDR", "deleting", "missing cluster ID", "missing managed group", "missing tenant", "missing subscription", "missing service provider"} {
		t.Run(name, func(t *testing.T) {
			cluster := newTestCluster(name == "deleting")
			clusterID := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123"))
			cluster.ServiceProviderProperties.ClusterServiceID = &clusterID
			serviceProvider := newTestServiceProviderCluster(coreapi.AzureReference{})
			if name == "UDR" {
				cluster.CustomerProperties.Platform.OutboundType = metadataapi.OutboundType("UserDefinedRouting")
			}
			if name == "missing cluster ID" {
				cluster.ServiceProviderProperties.ClusterServiceID = nil
			}
			if name == "missing managed group" {
				cluster.CustomerProperties.Platform.ManagedResourceGroup = ""
			}
			subscription := newTestSubscription(ptr.To(testTenantID))
			if name == "missing tenant" {
				subscription.Properties.TenantId = nil
			}
			subscriptions := []*coreapi.Subscription{subscription}
			if name == "missing subscription" {
				subscriptions = nil
			}
			ctx := context.Background()
			if name == "UDR" || name == "deleting" {
				recheck := metav1.NewTime(time.Now().Add(time.Minute))
				serviceProvider.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{EgressPublicIPsControllerName: &recheck}
			}
			database, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProvider})
			require.NoError(t, err)
			enqueuer := &fakeAfterEnqueuer{}
			syncer := &egressPublicIPsSyncer{
				clock:                        clocktesting.NewFakePassiveClock(time.Now()),
				resourcesDBClient:            database,
				clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: database},
				serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: database},
				subscriptionLister:           &corelistertesting.SliceSubscriptionLister{Subscriptions: subscriptions},
				azureFPAClientBuilder:        azureclient.NewMockFirstPartyApplicationClientBuilder(gomock.NewController(t)),
				enqueueAfter:                 enqueuer,
			}
			if name == "missing service provider" {
				syncer.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{}
			}
			require.NoError(t, syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: testClusterName}))
			require.Empty(t, enqueuer.enqueuedKeys, "skipped observations must not schedule a retry")
		})
	}
}

func TestEgressPublicIPsInvalidationAndConflict(t *testing.T) {
	ctx := context.Background()
	cluster := newTestCluster(false)
	clusterID := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123"))
	cluster.ServiceProviderProperties.ClusterServiceID = &clusterID
	expected, err := coreapihelpers.EgressPublicIPResourceIDs(cluster)
	require.NoError(t, err)
	serviceProvider := newTestServiceProviderCluster(coreapi.AzureReference{})
	serviceProvider.Status.AzureResources.EgressPublicIPs = map[string]coreapi.AzureResourceObservation{}
	for _, resourceID := range expected {
		serviceProvider.Status.AzureResources.EgressPublicIPs[resourceID.String()] = coreapi.AzureResourceNotFound
	}
	clock := clocktesting.NewFakePassiveClock(time.Now())
	recheck := metav1.NewTime(clock.Now().Add(time.Minute))
	serviceProvider.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{EgressPublicIPsControllerName: &recheck}
	database, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProvider})
	require.NoError(t, err)
	crud := database.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
	mockController := gomock.NewController(t)
	builder := azureclient.NewMockFirstPartyApplicationClientBuilder(mockController)
	client := azureclient.NewMockPublicIPAddressesClient(mockController)
	syncer := &egressPublicIPsSyncer{
		clock:                        clock,
		resourcesDBClient:            database,
		clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: database},
		serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: database},
		subscriptionLister:           &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{newTestSubscription(ptr.To(testTenantID))}},
		azureFPAClientBuilder:        builder,
	}
	key := controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: testClusterName}
	initial, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.NoError(t, syncer.SyncOnce(ctx, key))
	unchanged, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.Equal(t, initial.CosmosETag, unchanged.CosmosETag, "self-generated events must not recheck before the cooldown")
	clock.SetTime(recheck.Add(time.Second))
	stale := initial.DeepCopy()
	stale.CosmosETag = "stale"
	syncer.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{stale}}
	require.True(t, cosmosstorageutils.IsPreconditionFailedError(syncer.SyncOnce(ctx, key)), "failed invalidation must prevent all Azure calls")
	syncer.serviceProviderClusterLister = &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: database}
	builder.EXPECT().PublicIPAddressesClient(testTenantID, testSubscriptionID).DoAndReturn(func(_, _ string) (azureclient.PublicIPAddressesClient, error) {
		invalidated, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		for _, observation := range invalidated.Status.AzureResources.EgressPublicIPs {
			require.Equal(t, coreapi.AzureResourceUnknown, observation, "invalidation must precede even client construction")
		}
		invalidated.Status.AzureResources.ManagedResourceGroup.AzureResource = testManagedResourceGroupID(t)
		_, err = crud.Replace(ctx, invalidated, nil)
		require.NoError(t, err)
		return client, nil
	})
	for index, resourceID := range expected {
		var getErr error
		if index == 0 {
			getErr = &azcore.ResponseError{StatusCode: 503}
		}
		client.EXPECT().Get(gomock.Any(), testManagedRGName, resourceID.Name, nil).Return(armnetwork.PublicIPAddressesClientGetResponse{}, getErr)
	}
	require.Error(t, syncer.SyncOnce(ctx, key))
	stored, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	for _, observation := range stored.Status.AzureResources.EgressPublicIPs {
		require.Equal(t, coreapi.AzureResourceUnknown, observation, "failed final persist must not resurrect a missing diagnostic")
	}
	require.NotNil(t, stored.Status.AzureResources.ManagedResourceGroup.AzureResource, "concurrent sibling updates must be preserved")
	require.Nil(t, stored.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName], "failed reads must remain retryable")
}
