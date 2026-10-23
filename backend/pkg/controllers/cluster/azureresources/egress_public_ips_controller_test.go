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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
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

type egressListPage struct {
	values []*armnetwork.PublicIPAddress
	status int
	code   string
	err    error
	body   string
}

type egressListTransport struct {
	test     *testing.T
	pages    []egressListPage
	requests int
}

func (transport *egressListTransport) Do(request *http.Request) (*http.Response, error) {
	transport.test.Helper()
	require.Equal(transport.test, http.MethodGet, request.Method)
	require.Equal(transport.test, "/subscriptions/"+testSubscriptionID+"/resourceGroups/"+testManagedRGName+"/providers/Microsoft.Network/publicIPAddresses", request.URL.Path, "only resource-group enumeration is allowed, never per-IP GETs")
	require.Empty(transport.test, request.URL.Query().Get("$filter"))
	pageIndex := 0
	if page := request.URL.Query().Get("page"); page != "" {
		var err error
		pageIndex, err = strconv.Atoi(page)
		require.NoError(transport.test, err)
	}
	require.Less(transport.test, pageIndex, len(transport.pages))
	transport.requests++
	page := transport.pages[pageIndex]
	if page.err != nil {
		return nil, page.err
	}
	status := page.status
	if status == 0 {
		status = http.StatusOK
	}
	body := page.body
	if body == "" {
		result := map[string]any{"value": page.values}
		if pageIndex+1 < len(transport.pages) {
			nextURL := *request.URL
			query := nextURL.Query()
			query.Set("page", strconv.Itoa(pageIndex+1))
			nextURL.RawQuery = query.Encode()
			result["nextLink"] = nextURL.String()
		}
		if status != http.StatusOK {
			code := page.code
			if code == "" {
				code = "ResourceNotFound"
			}
			result = map[string]any{"error": map[string]string{"code": code, "message": "list request failed"}}
		}
		encoded, err := json.Marshal(result)
		require.NoError(transport.test, err)
		body = string(encoded)
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func newEgressListClient(test *testing.T, transport *egressListTransport) *armnetwork.PublicIPAddressesClient {
	test.Helper()
	client, err := armnetwork.NewPublicIPAddressesClient(testSubscriptionID, &fake.TokenCredential{}, &azcorearm.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1}},
	})
	require.NoError(test, err)
	return client
}

func TestEgressPublicIPsListNextLinkPreservesRequest(t *testing.T) {
	for _, page := range []string{"", "&page=1"} {
		t.Run("query"+page, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodGet, "https://arm.example.invalid:8443/subscriptions/"+testSubscriptionID+"/resourceGroups/"+testManagedRGName+"/providers/Microsoft.Network/publicIPAddresses?api-version=test-version&tag=one&tag=two&token=a%2Bb%26c"+page, nil)
			require.NoError(t, err)
			originalURL := *request.URL
			transport := &egressListTransport{test: t, pages: make([]egressListPage, 3)}
			response, err := transport.Do(request)
			require.NoError(t, err)
			defer response.Body.Close()
			var result struct {
				NextLink string `json:"nextLink"`
			}
			require.NoError(t, json.NewDecoder(response.Body).Decode(&result))
			require.NotEmpty(t, result.NextLink)
			nextURL, err := request.URL.Parse(result.NextLink)
			require.NoError(t, err)
			expectedURL := originalURL
			expectedQuery := originalURL.Query()
			nextPage := "1"
			if page != "" {
				nextPage = "2"
			}
			expectedQuery.Set("page", nextPage)
			expectedURL.RawQuery = expectedQuery.Encode()
			require.Equal(t, expectedURL, *nextURL, "nextLink must preserve the endpoint and every query value except page")
			require.Equal(t, originalURL, *request.URL, "creating nextLink must not mutate the original request URL")
		})
	}
}

func egressListValues(expected []*azcorearm.ResourceID) []*armnetwork.PublicIPAddress {
	values := make([]*armnetwork.PublicIPAddress, 0, len(expected))
	for _, resourceID := range expected {
		values = append(values, &armnetwork.PublicIPAddress{ID: ptr.To(resourceID.String())})
	}
	return values
}

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

func TestEgressPublicIPsListStatusEvents(t *testing.T) {
	for _, status := range []int{0, 429, 503, 403, 404} {
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
			transport := &egressListTransport{test: t, pages: []egressListPage{{values: egressListValues(expected[1:])}}}
			if status != 0 {
				transport.pages = append(transport.pages, egressListPage{status: status})
			}
			client := newEgressListClient(t, transport)
			builder.EXPECT().PublicIPAddressesClient(testTenantID, testSubscriptionID).Return(client, nil).AnyTimes()
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
			require.Equal(t, status != 0, runQueued() != nil)
			observed := stored()
			require.NotEqual(t, initial.CosmosETag, observed.CosmosETag)
			unconfirmed := coreapi.AzureResourceNotFound
			if status != 0 {
				unconfirmed = coreapi.AzureResourceUnknown
			}
			require.Equal(t, unconfirmed, observed.Status.AzureResources.EgressPublicIPs[expected[0].String()])
			require.Equal(t, coreapi.AzureResourceFound, observed.Status.AzureResources.EgressPublicIPs[expected[1].String()])
			requestsPerPass := len(transport.pages)
			require.Equal(t, requestsPerPass, transport.requests)
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
				require.Equal(t, requestsPerPass, transport.requests, "status events must not repeat enumeration during cooldown")
				require.Equal(t, observed.CosmosETag, stored().CosmosETag, "status events must not replace the document during cooldown")
			}
			require.Len(t, enqueuer.enqueuedDurations, 4)
			require.Equal(t, time.Second, enqueuer.enqueuedDurations[3], "early events must preserve the explicit retry after error backoff is forgotten")
			clock.SetTime(recheck.Time)
			controller.Enqueue(enqueuer.enqueuedKeys[3])
			require.Equal(t, status != 0, runQueued() != nil)
			require.Equal(t, 2*requestsPerPass, transport.requests, "cooldown expiry must allow exactly one enumeration")
			retried := stored()
			require.NotEqual(t, observed.CosmosETag, retried.CosmosETag)
			transport.pages = []egressListPage{{values: egressListValues(expected)}}
			clock.SetTime(retried.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName].Time)
			controller.Enqueue(enqueuer.enqueuedKeys[4])
			require.NoError(t, runQueued())
			require.Equal(t, 2*requestsPerPass+1, transport.requests)
			completed := stored()
			for _, observation := range completed.Status.AzureResources.EgressPublicIPs {
				require.Equal(t, coreapi.AzureResourceFound, observation)
			}
			require.Nil(t, completed.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName])
			notifier.handler.OnUpdate(retried, completed)
			require.NoError(t, runQueued())
			require.Equal(t, 2*requestsPerPass+1, transport.requests)
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
		{name: "zero"},
		{name: "resource group absent", status: 404, errorCode: "ResourceGroupNotFound"},
		{name: "unrecognized 404", status: 404, errorCode: "NoRegisteredProviderFound"},
		{name: "partial", found: 4},
		{name: "all", found: 9},
		{name: "authorization", found: 4, status: 403},
		{name: "throttling", status: 429},
		{name: "service failure", status: 503},
		{name: "transport failure", status: -1},
		{name: "client failure clears stale missing", builderFailure: true},
		{name: "persistence conflict", found: 9, conflict: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			readFailure := testCase.status != 0
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
				pages := []egressListPage{{values: egressListValues(expected[:testCase.found])}}
				if readFailure {
					failure := egressListPage{status: testCase.status, code: testCase.errorCode}
					if testCase.status == -1 {
						failure.err = errors.New("transport failure")
					}
					if testCase.found == 0 {
						pages = []egressListPage{failure}
					} else {
						pages = append(pages, failure)
					}
				}
				transport := &egressListTransport{test: t, pages: pages}
				sdkClient := newEgressListClient(t, transport)
				builder.EXPECT().PublicIPAddressesClient(testTenantID, testSubscriptionID).Return(client, nil)
				client.EXPECT().NewListPager(testManagedRGName, nil).DoAndReturn(func(group string, options *armnetwork.PublicIPAddressesClientListOptions) *runtime.Pager[armnetwork.PublicIPAddressesClientListResponse] {
					return sdkClient.NewListPager(group, options)
				})
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
				} else if !readFailure && !testCase.builderFailure {
					want = coreapi.AzureResourceNotFound
				}
				require.Equal(t, want, updated.Status.AzureResources.EgressPublicIPs[resourceID.String()])
			}
			if !testCase.builderFailure {
				retryErr := syncer.SyncOnce(ctx, key)
				require.NoError(t, retryErr)
				again, err := database.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				require.Equal(t, updated.CosmosETag, again.CosmosETag, "unchanged observations must not write")
			}
		})
	}
}

func TestEgressPublicIPsEnumeration(t *testing.T) {
	cluster := newTestCluster(false)
	clusterID := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123"))
	cluster.ServiceProviderProperties.ClusterServiceID = &clusterID
	expected, err := coreapihelpers.EgressPublicIPResourceIDs(cluster)
	require.NoError(t, err)
	values := egressListValues(expected)
	identity := expected[0].String()
	testCases := []struct {
		name    string
		pages   []egressListPage
		found   int
		unknown bool
	}{
		{name: "all", pages: []egressListPage{{values: values}}, found: 9},
		{name: "some", pages: []egressListPage{{values: values[:4]}}, found: 4},
		{name: "empty", pages: []egressListPage{{values: []*armnetwork.PublicIPAddress{}}}},
		{name: "duplicates", pages: []egressListPage{{values: []*armnetwork.PublicIPAddress{values[0], values[0]}}}, found: 1},
		{name: "later pages", pages: []egressListPage{{values: values[:2]}, {}, {values: values[2:]}}, found: 9},
		{name: "all found before later failure", pages: []egressListPage{{values: values}, {status: 503}}, found: 9, unknown: true},
		{name: "case insensitive ID", pages: []egressListPage{{values: []*armnetwork.PublicIPAddress{{ID: ptr.To(strings.ToUpper(identity))}}}}, found: 1},
		{name: "unrelated name", pages: []egressListPage{{values: []*armnetwork.PublicIPAddress{{ID: ptr.To(identity + "0")}}}}},
		{name: "wrong resource group", pages: []egressListPage{{values: []*armnetwork.PublicIPAddress{{ID: ptr.To(strings.Replace(identity, testManagedRGName, "other-rg", 1))}}}}},
		{name: "wrong subscription", pages: []egressListPage{{values: []*armnetwork.PublicIPAddress{{ID: ptr.To(strings.Replace(identity, testSubscriptionID, "other-subscription", 1))}}}}},
		{name: "wrong resource type", pages: []egressListPage{{values: []*armnetwork.PublicIPAddress{{ID: ptr.To(strings.Replace(identity, "publicIPAddresses", "publicIPPrefixes", 1))}}}}},
		{name: "nil record", pages: []egressListPage{{values: []*armnetwork.PublicIPAddress{nil}}, {values: values[:1]}}, found: 1, unknown: true},
		{name: "nil ID", pages: []egressListPage{{values: []*armnetwork.PublicIPAddress{{Name: ptr.To("abc123-0")}}}}, unknown: true},
		{name: "invalid ID", pages: []egressListPage{{values: []*armnetwork.PublicIPAddress{{ID: ptr.To("invalid")}}}}, unknown: true},
		{name: "empty ID", pages: []egressListPage{{values: []*armnetwork.PublicIPAddress{{ID: ptr.To("")}}}}, unknown: true},
		{name: "malformed later page", pages: []egressListPage{{values: values[:1]}, {body: "{"}}, found: 1, unknown: true},
	}
	for _, status := range []int{429, 503, 403, 404} {
		for _, later := range []bool{false, true} {
			pages := []egressListPage{}
			found := 0
			if later {
				pages = append(pages, egressListPage{values: values[:2]})
				found = 2
			}
			pages = append(pages, egressListPage{status: status})
			testCases = append(testCases, struct {
				name    string
				pages   []egressListPage
				found   int
				unknown bool
			}{name: fmt.Sprintf("status %d later %t", status, later), pages: pages, found: found, unknown: true})
		}
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			transport := &egressListTransport{test: t, pages: testCase.pages}
			builder := azureclient.NewMockFirstPartyApplicationClientBuilder(gomock.NewController(t))
			builder.EXPECT().PublicIPAddressesClient(testTenantID, testSubscriptionID).Return(newEgressListClient(t, transport), nil)
			syncer := &egressPublicIPsSyncer{
				subscriptionLister:    &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{newTestSubscription(ptr.To(testTenantID))}},
				azureFPAClientBuilder: builder,
			}
			observations, err := syncer.observe(context.Background(), cluster)
			require.Equal(t, testCase.unknown, err != nil)
			require.Equal(t, len(testCase.pages), transport.requests, "all pages must be followed before deriving absence")
			require.Len(t, observations, 9)
			for index, resourceID := range expected {
				want := coreapi.AzureResourceNotFound
				if index < testCase.found {
					want = coreapi.AzureResourceFound
				} else if testCase.unknown {
					want = coreapi.AzureResourceUnknown
				}
				require.Equal(t, want, observations[resourceID.String()], resourceID.String())
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
	transport := &egressListTransport{test: t, pages: []egressListPage{{status: 503}}}
	client := newEgressListClient(t, transport)
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
	require.Error(t, syncer.SyncOnce(ctx, key))
	stored, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	for _, observation := range stored.Status.AzureResources.EgressPublicIPs {
		require.Equal(t, coreapi.AzureResourceUnknown, observation, "failed final persist must not resurrect a missing diagnostic")
	}
	require.NotNil(t, stored.Status.AzureResources.ManagedResourceGroup.AzureResource, "concurrent sibling updates must be preserved")
	require.Nil(t, stored.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName], "failed reads must remain retryable")
}
