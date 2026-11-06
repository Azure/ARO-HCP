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

package egresspublicips

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

type egressTestInformers struct {
	coreinformers.BackendInformers
	database        corecosmosstorage.ResourcesDBClient
	clusterInformer cache.SharedIndexInformer
	clusterLister   corelisters.ClusterLister
}

type egressRecordingInformer struct {
	cache.SharedIndexInformer
	options []cache.HandlerOptions
}

func (informer *egressRecordingInformer) AddEventHandlerWithOptions(handler cache.ResourceEventHandler, options cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error) {
	informer.options = append(informer.options, options)
	return informer.SharedIndexInformer.AddEventHandlerWithOptions(handler, options)
}

type egressWriteFaultDB struct {
	corecosmosstorage.ResourcesDBClient
	beforeReplace func()
	afterReplace  func()
	getErr        error
	replaceErr    error
	reads         int
	replacements  int
}

func (database *egressWriteFaultDB) ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName string) cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster] {
	return &egressWriteFaultCRUD{ResourceCRUD: database.ResourcesDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName), database: database}
}

type egressWriteFaultCRUD struct {
	cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster]
	database *egressWriteFaultDB
}

func (crud *egressWriteFaultCRUD) Get(ctx context.Context, name string) (*coreapi.ServiceProviderCluster, error) {
	crud.database.reads++
	if crud.database.getErr != nil {
		return nil, crud.database.getErr
	}
	return crud.ResourceCRUD.Get(ctx, name)
}

func (crud *egressWriteFaultCRUD) Replace(ctx context.Context, value *coreapi.ServiceProviderCluster, options *azcosmos.ItemOptions) (*coreapi.ServiceProviderCluster, error) {
	crud.database.replacements++
	if crud.database.beforeReplace != nil {
		crud.database.beforeReplace()
	}
	if crud.database.replaceErr != nil {
		return nil, crud.database.replaceErr
	}
	stored, err := crud.ResourceCRUD.Replace(ctx, value, options)
	if err == nil && crud.database.afterReplace != nil {
		crud.database.afterReplace()
	}
	return stored, err
}

func (informers *egressTestInformers) Clusters() (cache.SharedIndexInformer, corelisters.ClusterLister) {
	if informers.clusterLister != nil {
		return informers.clusterInformer, informers.clusterLister
	}
	if informers.clusterInformer != nil {
		return informers.clusterInformer, &corelistertesting.DBClusterLister{ResourcesDBClient: informers.database}
	}
	return cache.NewSharedIndexInformer(nil, &coreapi.Cluster{}, 0, nil), &corelistertesting.DBClusterLister{ResourcesDBClient: informers.database}
}

func (informers *egressTestInformers) ServiceProviderClusters() (cache.SharedIndexInformer, corelisters.ServiceProviderClusterLister) {
	return cache.NewSharedIndexInformer(nil, &coreapi.ServiceProviderCluster{}, 0, nil), &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: informers.database}
}

func (informers *egressTestInformers) ManagementClusterContents() (cache.SharedIndexInformer, corelisters.ManagementClusterContentLister) {
	return cache.NewSharedIndexInformer(nil, &coreapi.ManagementClusterContent{}, 0, nil), nil
}

func (informers *egressTestInformers) Controllers() (cache.SharedIndexInformer, corelisters.ControllerLister) {
	return cache.NewSharedIndexInformer(nil, &coreapi.Controller{}, 0, nil), &corelistertesting.DBControllerLister{ResourcesDBClient: informers.database}
}

type egressGetTransport struct {
	test          *testing.T
	found         []*azcorearm.ResourceID
	status        int
	code          string
	err           error
	body          string
	failureAfter  int
	requests      int
	resourceGroup string
	paths         []string
}

func (transport *egressGetTransport) set(found []*azcorearm.ResourceID, status int) {
	transport.found, transport.status = found, status
	transport.failureAfter = len(found)
	transport.body, transport.code, transport.err = "", "", nil
}

func (transport *egressGetTransport) Do(request *http.Request) (*http.Response, error) {
	transport.test.Helper()
	require.Equal(transport.test, http.MethodGet, request.Method)
	group := transport.resourceGroup
	if len(group) == 0 {
		group = testManagedRGName
	}
	prefix := "/subscriptions/" + testSubscriptionID + "/resourceGroups/" + group + "/providers/Microsoft.Network/publicIPAddresses/"
	require.True(transport.test, strings.HasPrefix(request.URL.Path, prefix), request.URL.Path)
	name := strings.TrimPrefix(request.URL.Path, prefix)
	require.NotEmpty(transport.test, name)
	require.NotContains(transport.test, name, "/")
	require.NotEmpty(transport.test, request.URL.Query().Get("api-version"))
	transport.requests++
	transport.paths = append(transport.paths, request.URL.Path)
	index, err := strconv.Atoi(name[strings.LastIndex(name, "-")+1:])
	require.NoError(transport.test, err)
	require.Less(transport.test, index, 9)
	status := http.StatusNotFound
	for _, resourceID := range transport.found {
		if strings.EqualFold(resourceID.String(), request.URL.Path) {
			status = http.StatusOK
		}
	}
	body := ""
	if index >= transport.failureAfter {
		if transport.err != nil {
			return nil, transport.err
		}
		if transport.status != 0 {
			status = transport.status
		}
		body = transport.body
	}
	if len(body) == 0 {
		result := map[string]any{"id": request.URL.Path}
		if status != http.StatusOK {
			code := transport.code
			if len(code) == 0 {
				code = "ResourceNotFound"
			}
			result = map[string]any{"error": map[string]string{"code": code, "message": "get request failed"}}
		}
		encoded, err := json.Marshal(result)
		require.NoError(transport.test, err)
		body = string(encoded)
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func newEgressGetClient(test *testing.T, transport *egressGetTransport) *armnetwork.PublicIPAddressesClient {
	test.Helper()
	client, err := armnetwork.NewPublicIPAddressesClient(testSubscriptionID, &fake.TokenCredential{}, &azcorearm.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1}},
	})
	require.NoError(test, err)
	return client
}

type egressStatusNotifier struct {
	handler cache.ResourceEventHandler
}

func (notifier *egressStatusNotifier) AddEventHandlerWithOptions(handler cache.ResourceEventHandler, _ cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error) {
	notifier.handler = handler
	return nil, nil
}

type egressEventSyncer struct {
	controller controllerutils.Controller
	permits    chan struct{}
	results    chan error
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
	err := syncer.controller.SyncOnce(ctx, key)
	syncer.results <- err
	return err
}

func TestEgressPublicIPsGet(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		found, status int
		code          string
		transportErr  bool
	}{
		{name: "all", found: 9}, {name: "some404", found: 4}, {name: "none"},
		{name: "group missing", status: 404, code: "ResourceGroupNotFound"},
		{name: "unknown404", status: 404, code: "SubscriptionNotFound"},
		{name: "wrong resource404", status: 404, code: "RoleAssignmentNotFound"},
		{name: "403", status: 403}, {name: "429", status: 429}, {name: "503", status: 503},
		{name: "later503", found: 4, status: 503}, {name: "transport", transportErr: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cluster := newTestCluster(false)
			clusterID := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123"))
			cluster.ServiceProviderProperties.ClusterServiceID = &clusterID
			expected, err := coreapihelpers.EgressPublicIPResourceIDs(cluster)
			require.NoError(t, err)
			transport := &egressGetTransport{test: t}
			transport.set(expected[:testCase.found], testCase.status)
			transport.code = testCase.code
			if testCase.transportErr {
				transport.err = errors.New("transport unavailable")
			}
			builder := azureclient.NewMockFirstPartyApplicationClientBuilder(gomock.NewController(t))
			builder.EXPECT().PublicIPAddressesClient(testTenantID, testSubscriptionID).Return(newEgressGetClient(t, transport), nil)
			syncer := &egressPublicIPsSyncer{azureFPAClientBuilder: builder}
			observations, err := syncer.observe(context.Background(), cluster, testTenantID, coreapi.AzureMultiReference{})
			failed := testCase.transportErr || (testCase.status != 0 && (testCase.status != 404 || testCase.code != "ResourceGroupNotFound"))
			require.Equal(t, failed, err != nil)
			if testCase.transportErr {
				require.ErrorIs(t, err, transport.err)
			}
			if failed {
				require.Nil(t, observations, "unexpected error returns no partial classifications")
				require.Equal(t, testCase.found+1, transport.requests)
			} else {
				require.Len(t, observations.AzureResources, testCase.found)
				require.Equal(t, 9, transport.requests)
				for index, resourceID := range expected {
					require.Equal(t, resourceID.String(), transport.paths[index])
				}
			}
			if !failed {
				require.Len(t, observations.PendingAzureResources, 9-len(observations.AzureResources))
			}
		})
	}
}

func TestEgressPublicIPsIneligibleSkipsStoreRead(t *testing.T) {
	for _, scenario := range []string{"deleting", "UDR"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			cluster := newTestCluster(scenario == "deleting")
			if scenario == "UDR" {
				cluster.CustomerProperties.Platform.OutboundType = metadataapi.OutboundType("UserDefinedRouting")
			}
			serviceProvider := newTestServiceProviderCluster(coreapi.AzureReference{})
			recheck := metav1.NewTime(time.Now().Add(-time.Minute))
			serviceProvider.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{EgressPublicIPsControllerName: &recheck}
			memory, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProvider})
			require.NoError(t, err)
			crud := memory.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
			initial, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			database := &egressWriteFaultDB{ResourcesDBClient: memory, getErr: errors.New("live SP read must not run for an ineligible cluster")}
			controller := NewEgressPublicIPsController(clocktesting.NewFakePassiveClock(time.Now()), database,
				&corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: memory},
				&corelistertesting.SliceSubscriptionLister{},
				azureclient.NewMockFirstPartyApplicationClientBuilder(gomock.NewController(t)),
				&egressTestInformers{database: memory})
			key := controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: testClusterName}
			for range 2 {
				require.NoError(t, controller.SyncOnce(ctx, key))
			}
			require.Zero(t, database.replacements)
			stored, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			require.Equal(t, initial, stored)
			health, err := memory.HCPClusters(testSubscriptionID, testResourceGroupName).Controllers(testClusterName).Get(ctx, EgressPublicIPsControllerName)
			require.NoError(t, err)
			require.Equal(t, metav1.ConditionFalse, meta.FindStatusCondition(health.Status.Conditions, "Degraded").Status)
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

			syncer := &egressPublicIPsSyncer{
				clock:                        clocktesting.NewFakePassiveClock(time.Now()),
				resourcesDBClient:            database,
				clusterLister:                &corelistertesting.DBClusterLister{ResourcesDBClient: database},
				serviceProviderClusterLister: &corelistertesting.DBServiceProviderClusterLister{ResourcesDBClient: database},
				subscriptionLister:           &corelistertesting.SliceSubscriptionLister{Subscriptions: subscriptions},
				azureFPAClientBuilder:        azureclient.NewMockFirstPartyApplicationClientBuilder(gomock.NewController(t)),
			}
			if name == "missing service provider" {
				syncer.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{}
			}
			err = syncer.SyncOnce(ctx, controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: testClusterName})
			require.NoError(t, err)
		})
	}
}

type egressFaultLister struct {
	corelisters.ServiceProviderClusterLister
	err error
}

func (lister *egressFaultLister) Get(ctx context.Context, subscription, group, cluster string) (*coreapi.ServiceProviderCluster, error) {
	if lister.err != nil {
		return nil, lister.err
	}
	return lister.ServiceProviderClusterLister.Get(ctx, subscription, group, cluster)
}

type egressFixture struct {
	cluster       *coreapi.Cluster
	expected      []*azcorearm.ResourceID
	memory        corecosmosstorage.ResourcesDBClient
	database      *egressWriteFaultDB
	indexer       cache.Indexer
	clock         *clocktesting.FakePassiveClock
	subscriptions *egressSubscriptionLister
	transport     *egressGetTransport
	controller    controllerutils.Controller
	builderErr    error
	builderCalls  int
	lister        *egressFaultLister
	key           controllerutils.HCPClusterKey
}

type egressSubscriptionLister struct {
	corelistertesting.SliceSubscriptionLister
	err error
}

func (lister *egressSubscriptionLister) Get(ctx context.Context, subscriptionID string) (*coreapi.Subscription, error) {
	if lister.err != nil {
		return nil, lister.err
	}
	return lister.SliceSubscriptionLister.Get(ctx, subscriptionID)
}

func newEgressFixture(t *testing.T, configure func(*coreapi.Cluster, *coreapi.ServiceProviderCluster, []*azcorearm.ResourceID)) *egressFixture {
	t.Helper()
	cluster := newTestCluster(false)
	clusterID := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123"))
	cluster.ServiceProviderProperties.ClusterServiceID = &clusterID
	expected, err := coreapihelpers.EgressPublicIPResourceIDs(cluster)
	require.NoError(t, err)
	serviceProvider := newTestServiceProviderCluster(coreapi.AzureReference{})
	serviceProvider.Status.AzureResources.EgressPublicIPs.PendingAzureResources = expected
	if configure != nil {
		configure(cluster, serviceProvider, expected)
	}
	memory, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(context.Background(), []any{cluster, serviceProvider})
	require.NoError(t, err)
	database := &egressWriteFaultDB{ResourcesDBClient: memory, getErr: errors.New("unexpected live SP read")}
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	subscriptions := &egressSubscriptionLister{SliceSubscriptionLister: corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{newTestSubscription(ptr.To(testTenantID))}}}
	transport := &egressGetTransport{test: t}
	builder := azureclient.NewMockFirstPartyApplicationClientBuilder(gomock.NewController(t))
	clock := clocktesting.NewFakePassiveClock(time.Now())
	clusterInformer := &egressRecordingInformer{SharedIndexInformer: cache.NewSharedIndexInformer(nil, &coreapi.Cluster{}, time.Hour, nil)}
	fixture := &egressFixture{
		cluster: cluster, expected: expected, memory: memory, database: database, indexer: indexer, clock: clock,
		subscriptions: subscriptions, transport: transport,
		key: controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: testClusterName},
	}
	builder.EXPECT().PublicIPAddressesClient(testTenantID, testSubscriptionID).DoAndReturn(func(_, _ string) (azureclient.PublicIPAddressesClient, error) {
		fixture.builderCalls++
		return newEgressGetClient(t, transport), fixture.builderErr
	}).AnyTimes()
	fixture.lister = &egressFaultLister{ServiceProviderClusterLister: corelisters.NewServiceProviderClusterLister(indexer)}
	fixture.controller = NewEgressPublicIPsController(clock, database, fixture.lister, subscriptions, builder,
		&egressTestInformers{database: memory, clusterInformer: clusterInformer, clusterLister: &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}}})
	require.Len(t, clusterInformer.options, 1)
	require.Equal(t, 5*time.Minute, *clusterInformer.options[0].ResyncPeriod)
	fixture.refresh(t)
	return fixture
}

func (fixture *egressFixture) stored(t *testing.T) *coreapi.ServiceProviderCluster {
	t.Helper()
	value, err := fixture.memory.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(context.Background(), coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	return value
}

func (fixture *egressFixture) refresh(t *testing.T) {
	t.Helper()
	require.NoError(t, fixture.indexer.Update(fixture.stored(t).DeepCopy()))
}

func (fixture *egressFixture) sync() error {
	return fixture.controller.SyncOnce(context.Background(), fixture.key)
}

func (fixture *egressFixture) assertHealth(t *testing.T, degraded bool) {
	t.Helper()
	health, err := fixture.memory.HCPClusters(testSubscriptionID, testResourceGroupName).Controllers(testClusterName).Get(context.Background(), EgressPublicIPsControllerName)
	require.NoError(t, err)
	require.Equal(t, degraded, meta.FindStatusCondition(health.Status.Conditions, "Degraded").Status == metav1.ConditionTrue)
}

func TestEgressPublicIPsQueueRecovery(t *testing.T) {
	fixture := newEgressFixture(t, nil)
	fixture.transport.set(nil, 503)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	syncer := &egressEventSyncer{controller: fixture.controller, permits: make(chan struct{}), results: make(chan error)}
	controller := controllerutil.NewGenericWatchingController(EgressPublicIPsControllerName, coreapi.ClusterResourceType, syncer, nil)
	controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	clusterNotifier, statusNotifier := &egressStatusNotifier{}, &egressStatusNotifier{}
	require.NoError(t, controller.QueueForInformers(5*time.Minute, clusterNotifier))
	require.NoError(t, controller.QueueForInformers(0, statusNotifier))
	done := make(chan struct{})
	go func() {
		defer close(done)
		controller.Run(ctx, 1)
	}()
	defer func() { cancel(); <-done }()
	runQueued := func() error {
		select {
		case syncer.permits <- struct{}{}:
		case <-ctx.Done():
			t.Fatal("no queued reconcile")
		}
		select {
		case err := <-syncer.results:
			return err
		case <-ctx.Done():
			t.Fatal("reconcile did not complete")
			return ctx.Err()
		}
	}
	initial := fixture.stored(t)
	statusNotifier.handler.OnAdd(initial, false)
	require.Error(t, runQueued())
	fixture.assertHealth(t, true)
	require.Error(t, runQueued(), "the error queue retries without an input event")
	require.Equal(t, 2, fixture.transport.requests)
	require.Equal(t, initial, fixture.stored(t), "errors do not write retry metadata or partial classifications")
	fixture.transport.set(fixture.expected[1:], 0)
	require.NoError(t, runQueued(), "the error queue recovers on a completed pass")
	fixture.refresh(t)
	partial := fixture.stored(t)
	require.Len(t, partial.Status.AzureResources.EgressPublicIPs.PendingAzureResources, 1)
	fixture.assertHealth(t, false)
	for range 3 {
		statusNotifier.handler.OnUpdate(initial, partial)
		require.NoError(t, runQueued())
		require.Equal(t, partial, fixture.stored(t), "unchanged pending observations do not create self-write events")
	}
	fixture.transport.set(fixture.expected, 0)
	clusterNotifier.handler.OnUpdate(fixture.cluster, fixture.cluster)
	require.NoError(t, runQueued(), "cluster resync permits pending recovery without a delayed enqueue")
	fixture.refresh(t)
	confirmed := fixture.stored(t)
	requests := fixture.transport.requests
	clusterNotifier.handler.OnUpdate(fixture.cluster, fixture.cluster)
	require.NoError(t, runQueued())
	require.Equal(t, requests, fixture.transport.requests, "confirmed future recheck skips GETs")
	require.Equal(t, confirmed, fixture.stored(t))
	fixture.clock.SetTime(confirmed.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName].Time)
	fixture.transport.set(fixture.expected[1:], 0)
	clusterNotifier.handler.OnUpdate(fixture.cluster, fixture.cluster)
	require.NoError(t, runQueued())
	require.Equal(t, requests+9, fixture.transport.requests, "due resync reobserves all expected resources")
	require.Len(t, fixture.stored(t).Status.AzureResources.EgressPublicIPs.PendingAzureResources, 1)
	require.Zero(t, fixture.database.reads)
}

func TestEgressPublicIPsPrerequisiteRecovery(t *testing.T) {
	for _, name := range []string{"nil CS ID", "empty CS ID", "missing group", "nil tenant", "empty tenant", "nil subscription properties", "missing subscription", "subscription lister error", "missing cached SP", "deleting", "UDR"} {
		t.Run(name, func(t *testing.T) {
			fixture := newEgressFixture(t, func(_ *coreapi.Cluster, sp *coreapi.ServiceProviderCluster, _ []*azcorearm.ResourceID) {
				sp.Status.AzureResources.ManagedResourceGroup.AzureResource = testManagedResourceGroupID(t)
				sp.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{
					EgressPublicIPsControllerName: ptr.To(metav1.NewTime(time.Now().Add(-time.Hour))),
					"peer":                        ptr.To(metav1.NewTime(time.Now().Add(time.Hour))),
				}
			})
			cluster := fixture.cluster.DeepCopy()
			subscriptions := slices.Clone(fixture.subscriptions.Subscriptions)
			switch name {
			case "nil CS ID":
				fixture.cluster.ServiceProviderProperties.ClusterServiceID = nil
			case "empty CS ID":
				fixture.cluster.ServiceProviderProperties.ClusterServiceID = &metadataapi.InternalID{}
			case "missing group":
				fixture.cluster.CustomerProperties.Platform.ManagedResourceGroup = ""
			case "nil tenant":
				fixture.subscriptions.Subscriptions = []*coreapi.Subscription{newTestSubscription(nil)}
			case "empty tenant":
				fixture.subscriptions.Subscriptions = []*coreapi.Subscription{newTestSubscription(ptr.To(""))}
			case "nil subscription properties":
				fixture.subscriptions.Subscriptions = []*coreapi.Subscription{{}}
				fixture.subscriptions.Subscriptions[0].ResourceID = subscriptions[0].ResourceID
			case "missing subscription":
				fixture.subscriptions.Subscriptions = nil
			case "subscription lister error":
				fixture.subscriptions.err = errors.New("subscription cache unavailable")
			case "missing cached SP":
				require.NoError(t, fixture.indexer.Delete(fixture.stored(t)))
			case "deleting":
				fixture.cluster.ServiceProviderProperties.DeletionTimestamp = ptr.To(metav1.Now())
			case "UDR":
				fixture.cluster.CustomerProperties.Platform.OutboundType = metadataapi.OutboundType("UserDefinedRouting")
			}
			before := fixture.stored(t)
			wantMessage := ""
			switch name {
			case "nil tenant", "empty tenant", "nil subscription properties":
				wantMessage = fmt.Sprintf("subscription %q has no tenant ID", testSubscriptionID)
			case "missing subscription", "subscription lister error":
				wantMessage = fmt.Sprintf("failed to get subscription %q", testSubscriptionID)
			}
			for range 3 {
				err := fixture.sync()
				if wantMessage == "" {
					require.NoError(t, err)
					fixture.assertHealth(t, false)
				} else {
					assert.ErrorContains(t, err, wantMessage)
					if fixture.subscriptions.err != nil {
						assert.ErrorIs(t, err, fixture.subscriptions.err)
					}
					if name == "missing subscription" {
						assert.True(t, cosmosstorageutils.IsNotFoundError(err))
					}
					health, healthErr := fixture.memory.HCPClusters(testSubscriptionID, testResourceGroupName).Controllers(testClusterName).Get(context.Background(), EgressPublicIPsControllerName)
					require.NoError(t, healthErr)
					condition := meta.FindStatusCondition(health.Status.Conditions, "Degraded")
					require.NotNil(t, condition)
					assert.Equal(t, metav1.ConditionTrue, condition.Status)
					assert.Equal(t, "Failed", condition.Reason)
				}
			}
			require.Zero(t, fixture.database.reads)
			require.Zero(t, fixture.database.replacements)
			require.Zero(t, fixture.transport.requests)
			require.Equal(t, before, fixture.stored(t))
			require.Zero(t, fixture.builderCalls)
			*fixture.cluster = *cluster
			fixture.subscriptions.Subscriptions = subscriptions
			fixture.subscriptions.err = nil
			fixture.refresh(t)
			fixture.transport.set(fixture.expected, 0)
			require.NoError(t, fixture.sync())
			require.Len(t, fixture.stored(t).Status.AzureResources.EgressPublicIPs.AzureResources, 9)
			fixture.assertHealth(t, false)
			require.Equal(t, before.Status.AzureResources.ManagedResourceGroup, fixture.stored(t).Status.AzureResources.ManagedResourceGroup)
			require.Equal(t, before.Spec.EarliestRecheckTimesByController["peer"], fixture.stored(t).Spec.EarliestRecheckTimesByController["peer"])
		})
	}
}

func TestEgressPublicIPsListerErrorAndNilSkipHealth(t *testing.T) {
	fixture := newEgressFixture(t, nil)
	fixture.lister.err = errors.New("cache read failed")
	before := fixture.stored(t)
	require.ErrorIs(t, fixture.sync(), fixture.lister.err)
	fixture.assertHealth(t, true)
	fixture.lister.err = nil
	fixture.cluster.CustomerProperties.Platform.ManagedResourceGroup = ""
	require.NoError(t, fixture.sync())
	fixture.assertHealth(t, false)
	require.Equal(t, before, fixture.stored(t))
	require.Zero(t, fixture.database.reads)
	require.Zero(t, fixture.database.replacements)
	require.Zero(t, fixture.transport.requests)
}

func TestEgressPublicIPsBuilderError(t *testing.T) {
	fixture := newEgressFixture(t, nil)
	fixture.builderErr = errors.New("client build failed")
	before := fixture.stored(t)
	require.ErrorIs(t, fixture.sync(), fixture.builderErr)
	fixture.assertHealth(t, true)
	require.Equal(t, before, fixture.stored(t))
	require.Zero(t, fixture.database.replacements)
	require.Zero(t, fixture.transport.requests)
	fixture.builderErr = nil
	fixture.transport.set(fixture.expected, 0)
	require.NoError(t, fixture.sync())
	fixture.assertHealth(t, false)
}

func TestEgressPublicIPsIntentBeforeMissingSubscription(t *testing.T) {
	fixture := newEgressFixture(t, func(_ *coreapi.Cluster, sp *coreapi.ServiceProviderCluster, _ []*azcorearm.ResourceID) {
		sp.Status.AzureResources.EgressPublicIPs = coreapi.AzureMultiReference{}
	})
	fixture.subscriptions.Subscriptions = nil
	require.NoError(t, fixture.sync())
	fixture.assertHealth(t, false)
	require.Equal(t, 1, fixture.database.replacements)
	require.Zero(t, fixture.builderCalls)
	require.Zero(t, fixture.transport.requests)
	intent := fixture.stored(t)
	require.Equal(t, fixture.expected, intent.Status.AzureResources.EgressPublicIPs.PendingAzureResources)
	fixture.refresh(t)
	require.ErrorContains(t, fixture.sync(), "failed to get subscription")
	fixture.assertHealth(t, true)
	require.Equal(t, intent, fixture.stored(t))
	require.Equal(t, 1, fixture.database.replacements)
	require.Zero(t, fixture.builderCalls)
	require.Zero(t, fixture.transport.requests)
}

func TestEgressPublicIPsIntentBeforeObservation(t *testing.T) {
	for _, scenario := range []string{"empty", "mismatched", "partial"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newEgressFixture(t, func(_ *coreapi.Cluster, sp *coreapi.ServiceProviderCluster, expected []*azcorearm.ResourceID) {
				sp.Status.AzureResources.EgressPublicIPs = coreapi.AzureMultiReference{}
				if scenario == "mismatched" {
					sp.Status.AzureResources.EgressPublicIPs.AzureResources = []*azcorearm.ResourceID{testManagedResourceGroupID(t)}
				}
				if scenario == "partial" {
					sp.Status.AzureResources.EgressPublicIPs.AzureResources = expected[:2]
				}
			})
			initial := fixture.stored(t)
			fixture.database.beforeReplace = func() { require.Zero(t, fixture.transport.requests) }
			require.NoError(t, fixture.sync())
			require.Zero(t, fixture.transport.requests, "intent must be durable before any Azure GET")
			require.Equal(t, 1, fixture.database.replacements)
			intent := fixture.stored(t)
			want := coreapihelpers.EgressPublicIPReferences(fixture.expected, initial.Status.AzureResources.EgressPublicIPs.AzureResources)
			require.Equal(t, want, intent.Status.AzureResources.EgressPublicIPs)
			cached, err := fixture.lister.Get(context.Background(), testSubscriptionID, testResourceGroupName, testClusterName)
			require.NoError(t, err)
			require.Equal(t, initial, cached, "intent preparation must not mutate the informer object")
			require.True(t, cosmosstorageutils.IsPreconditionFailedError(fixture.sync()), "stale cache retries intent, not observation")
			require.Zero(t, fixture.transport.requests)
			fixture.database.beforeReplace = nil
			fixture.refresh(t)
			fixture.transport.set(fixture.expected, 0)
			require.NoError(t, fixture.sync())
			require.Equal(t, len(want.PendingAzureResources), fixture.transport.requests)
			require.Equal(t, fixture.expected, fixture.stored(t).Status.AzureResources.EgressPublicIPs.AzureResources)
			require.Zero(t, fixture.database.reads)
		})
	}
}

func TestEgressPublicIPsIntentWriteFailure(t *testing.T) {
	for _, scenario := range []string{"error", "conflict"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newEgressFixture(t, func(_ *coreapi.Cluster, sp *coreapi.ServiceProviderCluster, _ []*azcorearm.ResourceID) {
				sp.Status.AzureResources.EgressPublicIPs = coreapi.AzureMultiReference{}
			})
			initial := fixture.stored(t)
			fixture.database.beforeReplace = func() {
				require.Zero(t, fixture.transport.requests)
				if scenario == "conflict" {
					peer := fixture.stored(t)
					peer.Status.AzureResources.ManagedResourceGroup.AzureResource = testManagedResourceGroupID(t)
					_, err := fixture.memory.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Replace(context.Background(), peer, nil)
					require.NoError(t, err)
				}
			}
			if scenario == "error" {
				fixture.database.replaceErr = errors.New("intent write unavailable")
			}
			err := fixture.sync()
			if scenario == "error" {
				require.ErrorIs(t, err, fixture.database.replaceErr)
			} else {
				require.True(t, cosmosstorageutils.IsPreconditionFailedError(err))
			}
			require.Equal(t, initial.Status.AzureResources.EgressPublicIPs, fixture.stored(t).Status.AzureResources.EgressPublicIPs)
			require.Zero(t, fixture.transport.requests)
			fixture.database.beforeReplace, fixture.database.replaceErr = nil, nil
			fixture.refresh(t)
			require.NoError(t, fixture.sync())
			require.Equal(t, fixture.expected, fixture.stored(t).Status.AzureResources.EgressPublicIPs.PendingAzureResources)
			require.Zero(t, fixture.transport.requests)
			fixture.refresh(t)
			fixture.transport.set(fixture.expected, 0)
			require.NoError(t, fixture.sync())
			require.Len(t, fixture.stored(t).Status.AzureResources.EgressPublicIPs.AzureResources, 9)
			if scenario == "conflict" {
				require.Equal(t, testManagedResourceGroupID(t), fixture.stored(t).Status.AzureResources.ManagedResourceGroup.AzureResource)
			}
			require.Zero(t, fixture.database.reads)
		})
	}
}

func TestEgressPublicIPsDurableIntentSurvivesReadFailure(t *testing.T) {
	fixture := newEgressFixture(t, func(_ *coreapi.Cluster, sp *coreapi.ServiceProviderCluster, _ []*azcorearm.ResourceID) {
		sp.Status.AzureResources.EgressPublicIPs = coreapi.AzureMultiReference{}
	})
	require.NoError(t, fixture.sync())
	require.Zero(t, fixture.transport.requests)
	fixture.refresh(t)
	intent := fixture.stored(t)
	fixture.transport.set(fixture.expected[:1], 503)
	fixture.transport.failureAfter = 4
	require.Error(t, fixture.sync(), "one success, three not-found results, then a read error")
	require.Equal(t, 5, fixture.transport.requests)
	require.Equal(t, intent, fixture.stored(t), "failed pass preserves durable pending intent without partial confirmations")
	require.Equal(t, 1, fixture.database.replacements)
	fixture.transport.set(nil, 0)
	require.NoError(t, fixture.sync())
	require.Equal(t, intent, fixture.stored(t), "unchanged pending observations need no write")
	require.Equal(t, 1, fixture.database.replacements)
	fixture.transport.set(fixture.expected, 0)
	require.NoError(t, fixture.sync())
	require.Equal(t, fixture.expected, fixture.stored(t).Status.AzureResources.EgressPublicIPs.AzureResources)
}

func TestEgressPublicIPsChangedIdentity(t *testing.T) {
	for _, name := range []string{"cluster", "group"} {
		t.Run(name, func(t *testing.T) {
			fixture := newEgressFixture(t, nil)
			fixture.transport.set(fixture.expected, 0)
			require.NoError(t, fixture.sync())
			fixture.refresh(t)
			if name == "cluster" {
				clusterID := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/changed"))
				fixture.cluster.ServiceProviderProperties.ClusterServiceID = &clusterID
			} else {
				fixture.cluster.CustomerProperties.Platform.ManagedResourceGroup = "changed-group"
				fixture.transport.resourceGroup = "changed-group"
			}
			expected, err := coreapihelpers.EgressPublicIPResourceIDs(fixture.cluster)
			require.NoError(t, err)
			fixture.transport.set(nil, 0)
			require.NoError(t, fixture.sync(), "new expected identities must not wait for an old future recheck")
			observed := fixture.stored(t)
			require.Empty(t, observed.Status.AzureResources.EgressPublicIPs.AzureResources)
			require.Equal(t, expected, observed.Status.AzureResources.EgressPublicIPs.PendingAzureResources)
			require.Nil(t, observed.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName])
			require.Equal(t, 9, fixture.transport.requests, "changed identities are persisted before any new GET")
			fixture.refresh(t)
			fixture.transport.set(expected, 0)
			require.NoError(t, fixture.sync())
			require.Equal(t, 18, fixture.transport.requests)
			require.Equal(t, expected, fixture.stored(t).Status.AzureResources.EgressPublicIPs.AzureResources)
		})
	}
}

func TestEgressPublicIPsCompletedPass(t *testing.T) {
	for _, found := range []int{0, 4, 9} {
		t.Run(fmt.Sprint(found), func(t *testing.T) {
			fixture := newEgressFixture(t, func(_ *coreapi.Cluster, sp *coreapi.ServiceProviderCluster, _ []*azcorearm.ResourceID) {
				sp.Status.AzureResources.EgressPublicIPs = coreapi.AzureMultiReference{}
			})
			require.NoError(t, fixture.sync())
			require.Zero(t, fixture.transport.requests)
			fixture.refresh(t)
			initial := fixture.stored(t)
			fixture.transport.set(fixture.expected[:found], 0)
			fixture.database.beforeReplace = func() {
				require.Equal(t, 9, fixture.transport.requests, "only the completed GET pass may write")
			}
			require.NoError(t, fixture.sync())
			require.Zero(t, fixture.database.reads)
			wantWrites := 1
			if found > 0 {
				wantWrites++
			}
			require.Equal(t, wantWrites, fixture.database.replacements)
			observed := fixture.stored(t)
			require.Len(t, observed.Status.AzureResources.EgressPublicIPs.AzureResources, found)
			require.Len(t, observed.Status.AzureResources.EgressPublicIPs.PendingAzureResources, 9-found)
			cached, err := corelisters.NewServiceProviderClusterLister(fixture.indexer).Get(context.Background(), testSubscriptionID, testResourceGroupName, testClusterName)
			require.NoError(t, err)
			require.Equal(t, initial, cached, "cache object must not be mutated")
			recheck := observed.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName]
			if found == 9 {
				require.NotNil(t, recheck)
				require.GreaterOrEqual(t, recheck.Sub(fixture.clock.Now()), 6*time.Hour)
				require.Less(t, recheck.Sub(fixture.clock.Now()), 9*time.Hour)
			} else {
				require.Nil(t, recheck)
			}
			fixture.database.beforeReplace = nil
			fixture.refresh(t)
			for range 3 {
				require.NoError(t, fixture.sync())
			}
			require.Equal(t, observed, fixture.stored(t), "unchanged observations must not churn SP writes")
			require.Equal(t, wantWrites, fixture.database.replacements)
			wantRequests := 9
			if found != 9 {
				wantRequests += 3 * (9 - found)
			}
			require.Equal(t, wantRequests, fixture.transport.requests)
			fixture.transport.set(fixture.expected, 0)
			require.NoError(t, fixture.sync())
			require.Len(t, fixture.stored(t).Status.AzureResources.EgressPublicIPs.AzureResources, 9)
			fixture.assertHealth(t, false)
		})
	}
}

func TestEgressPublicIPsFailedPassDoesNotWrite(t *testing.T) {
	for _, failure := range []string{"403", "429", "503", "transport", "malformed"} {
		for _, priorConfirmed := range []int{0, 2, 9} {
			t.Run(fmt.Sprintf("%s/prior%d", failure, priorConfirmed), func(t *testing.T) {
				fixture := newEgressFixture(t, func(_ *coreapi.Cluster, sp *coreapi.ServiceProviderCluster, expected []*azcorearm.ResourceID) {
					sp.Status.AzureResources.EgressPublicIPs = coreapi.AzureMultiReference{AzureResources: expected[:priorConfirmed], PendingAzureResources: expected[priorConfirmed:]}
					sp.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{EgressPublicIPsControllerName: ptr.To(metav1.NewTime(time.Now().Add(-time.Hour)))}
				})
				initial := fixture.stored(t)
				fixture.transport.set(fixture.expected[:4], 503)
				switch failure {
				case "403":
					fixture.transport.status = 403
				case "429":
					fixture.transport.status = 429
				case "transport":
					fixture.transport.err = errors.New("GET transport failed")
				case "malformed":
					fixture.transport.status, fixture.transport.body = 200, "{"
				}
				for range 2 {
					err := fixture.sync()
					require.Error(t, err)
					if failure == "transport" {
						require.ErrorIs(t, err, fixture.transport.err)
					}
					require.Equal(t, initial, fixture.stored(t), "failed pass cannot persist partial confirmations or refresh timestamp")
					require.Zero(t, fixture.database.replacements)
					fixture.assertHealth(t, true)
				}
				require.Zero(t, fixture.database.reads)
				fixture.transport.set(fixture.expected, 0)
				require.NoError(t, fixture.sync())
				require.Equal(t, 1, fixture.database.replacements)
				require.Len(t, fixture.stored(t).Status.AzureResources.EgressPublicIPs.AzureResources, 9)
				fixture.assertHealth(t, false)
			})
		}
	}
}

func TestEgressPublicIPsPeriodicReobservation(t *testing.T) {
	fixture := newEgressFixture(t, nil)
	fixture.transport.set(fixture.expected, 0)
	require.NoError(t, fixture.sync())
	fixture.refresh(t)
	confirmed := fixture.stored(t)
	require.Equal(t, 1, fixture.database.replacements)
	deadline := confirmed.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName]
	fixture.clock.SetTime(deadline.Add(-time.Second))
	require.NoError(t, fixture.sync())
	require.Equal(t, 9, fixture.transport.requests)
	fixture.clock.SetTime(deadline.Time)
	fixture.transport.set(fixture.expected[1:], 0)
	fixture.database.beforeReplace = func() {
		require.Equal(t, 18, fixture.transport.requests, "already durable expected identities do not need a new intent write")
	}
	require.NoError(t, fixture.sync())
	fixture.database.beforeReplace = nil
	require.Equal(t, 2, fixture.database.replacements)
	require.Equal(t, 18, fixture.transport.requests, "due all-confirmed pass must GET all expected IDs again")
	missing := fixture.stored(t)
	require.Equal(t, fixture.expected[:1], missing.Status.AzureResources.EgressPublicIPs.PendingAzureResources)
	require.Equal(t, fixture.expected[1:], missing.Status.AzureResources.EgressPublicIPs.AzureResources)
	require.Nil(t, missing.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName])
	fixture.refresh(t)
	require.NoError(t, fixture.sync())
	require.Equal(t, 19, fixture.transport.requests)
	require.Equal(t, missing, fixture.stored(t))
	fixture.transport.set(fixture.expected, 0)
	require.NoError(t, fixture.sync())
	require.Equal(t, 20, fixture.transport.requests)
	require.Len(t, fixture.stored(t).Status.AzureResources.EgressPublicIPs.AzureResources, 9)
	require.True(t, fixture.stored(t).Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName].After(deadline.Time))
	require.Zero(t, fixture.database.reads)
}

func TestEgressPublicIPsListerLagAndFinalConflict(t *testing.T) {
	for _, phase := range []string{"failed GET", "partial", "confirmed"} {
		t.Run(phase, func(t *testing.T) {
			fixture := newEgressFixture(t, nil)
			initial := fixture.stored(t)
			switch phase {
			case "failed GET":
				fixture.transport.set(fixture.expected[:4], 503)
			case "partial":
				fixture.transport.set(fixture.expected[:4], 0)
			case "confirmed":
				fixture.transport.set(fixture.expected, 0)
			}
			err := fixture.sync()
			if phase == "failed GET" {
				require.Error(t, err)
				require.Equal(t, initial, fixture.stored(t))
			} else {
				require.NoError(t, err)
			}
			peer := fixture.stored(t)
			peer.Status.AzureResources.ManagedResourceGroup.AzureResource = testManagedResourceGroupID(t)
			if peer.Spec.EarliestRecheckTimesByController == nil {
				peer.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{}
			}
			peer.Spec.EarliestRecheckTimesByController["peer"] = ptr.To(metav1.NewTime(fixture.clock.Now().Add(time.Hour)))
			_, err = fixture.memory.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Replace(context.Background(), peer, nil)
			require.NoError(t, err)
			before := fixture.stored(t)
			fixture.transport.set(fixture.expected, 0)
			requests := fixture.transport.requests
			for retry := 1; retry <= 3; retry++ {
				require.True(t, cosmosstorageutils.IsPreconditionFailedError(fixture.sync()))
				require.Equal(t, requests+retry*9, fixture.transport.requests, "stale lister may repeat reads before final ETag conflict")
				require.Equal(t, before, fixture.stored(t), "conflict must preserve peer fields and committed classifications")
				fixture.assertHealth(t, true)
			}
			cached, err := corelisters.NewServiceProviderClusterLister(fixture.indexer).Get(context.Background(), testSubscriptionID, testResourceGroupName, testClusterName)
			require.NoError(t, err)
			require.Equal(t, initial, cached)
			fixture.refresh(t)
			require.NoError(t, fixture.sync())
			after := fixture.stored(t)
			require.Len(t, after.Status.AzureResources.EgressPublicIPs.AzureResources, 9)
			require.Equal(t, before.Status.AzureResources.ManagedResourceGroup, after.Status.AzureResources.ManagedResourceGroup)
			require.Equal(t, before.Spec.EarliestRecheckTimesByController["peer"], after.Spec.EarliestRecheckTimesByController["peer"])
			fixture.assertHealth(t, false)
			require.Zero(t, fixture.database.reads)
		})
	}
}

func TestEgressPublicIPsFinalWriteInterruption(t *testing.T) {
	for _, phase := range []string{"before", "conflict", "after"} {
		t.Run(phase, func(t *testing.T) {
			fixture := newEgressFixture(t, nil)
			fixture.transport.set(fixture.expected, 0)
			initial := fixture.stored(t)
			fixture.database.beforeReplace = func() {
				require.Equal(t, 9, fixture.transport.requests)
				if phase == "before" {
					goruntime.Goexit()
				}
				if phase == "conflict" {
					peer := fixture.stored(t)
					peer.Status.AzureResources.ManagedResourceGroup.AzureResource = testManagedResourceGroupID(t)
					_, err := fixture.memory.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Replace(context.Background(), peer, nil)
					require.NoError(t, err)
				}
			}
			fixture.database.afterReplace = func() {
				if phase == "after" {
					goruntime.Goexit()
				}
			}
			var syncErr error
			returned := false
			done := make(chan struct{})
			go func() { defer close(done); syncErr = fixture.sync(); returned = true }()
			<-done
			require.Equal(t, phase == "conflict", returned)
			if phase == "conflict" {
				require.True(t, cosmosstorageutils.IsPreconditionFailedError(syncErr))
			}
			stored := fixture.stored(t)
			if phase == "after" {
				require.Len(t, stored.Status.AzureResources.EgressPublicIPs.AzureResources, 9)
				require.NotNil(t, stored.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName])
			} else {
				require.Equal(t, initial.Status.AzureResources.EgressPublicIPs, stored.Status.AzureResources.EgressPublicIPs)
				require.Nil(t, stored.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName])
			}
			fixture.database.beforeReplace, fixture.database.afterReplace = nil, nil
			fixture.refresh(t)
			require.NoError(t, fixture.sync())
			require.Len(t, fixture.stored(t).Status.AzureResources.EgressPublicIPs.AzureResources, 9)
			if phase == "conflict" {
				require.NotNil(t, fixture.stored(t).Status.AzureResources.ManagedResourceGroup.AzureResource)
			}
		})
	}
}

func TestEgressPublicIPsNeedsWork(t *testing.T) {
	for _, scenario := range []string{"pending", "pending future", "confirmed no time", "confirmed future", "confirmed expired", "mismatch future", "duplicate future", "extra future", "missing ID", "empty ID", "missing MRG", "deleting", "UDR"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newEgressFixture(t, nil)
			sp := fixture.stored(t)
			sp.Status.AzureResources.EgressPublicIPs = coreapi.AzureMultiReference{AzureResources: fixture.expected}
			if strings.Contains(scenario, "pending") {
				sp.Status.AzureResources.EgressPublicIPs = coreapi.AzureMultiReference{PendingAzureResources: fixture.expected}
			}
			if strings.Contains(scenario, "future") {
				sp.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{EgressPublicIPsControllerName: ptr.To(metav1.NewTime(fixture.clock.Now().Add(time.Hour)))}
			}
			if scenario == "confirmed expired" {
				sp.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{EgressPublicIPsControllerName: ptr.To(metav1.NewTime(fixture.clock.Now()))}
			}
			switch scenario {
			case "mismatch future":
				sp.Status.AzureResources.EgressPublicIPs.AzureResources = fixture.expected[:8]
			case "duplicate future":
				sp.Status.AzureResources.EgressPublicIPs.AzureResources = slices.Repeat(fixture.expected[:1], 9)
			case "extra future":
				sp.Status.AzureResources.EgressPublicIPs.AzureResources = append(slices.Clone(fixture.expected), testManagedResourceGroupID(t))
			case "missing ID":
				fixture.cluster.ServiceProviderProperties.ClusterServiceID = nil
			case "empty ID":
				fixture.cluster.ServiceProviderProperties.ClusterServiceID = new(metadataapi.InternalID)
			case "missing MRG":
				fixture.cluster.CustomerProperties.Platform.ManagedResourceGroup = ""
			case "deleting":
				fixture.cluster.ServiceProviderProperties.DeletionTimestamp = ptr.To(metav1.NewTime(fixture.clock.Now()))
			case "UDR":
				fixture.cluster.CustomerProperties.Platform.OutboundType = metadataapi.OutboundType("UserDefinedRouting")
			}
			want := !slices.Contains([]string{"confirmed future", "missing ID", "empty ID", "missing MRG", "deleting", "UDR"}, scenario)
			require.Equal(t, want, (&egressPublicIPsSyncer{clock: fixture.clock}).needsWork(fixture.cluster, sp))
		})
	}
}

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
	testTenantID          = "test-tenant-id"
	testManagedRGName     = "test-managed-rg"
)

func newTestCluster(deleting bool) *coreapi.Cluster {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))

	cluster := &coreapi.Cluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: testClusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
	}
	cluster.CustomerProperties.Platform.ManagedResourceGroup = testManagedRGName
	if deleting {
		cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	}
	return cluster
}

// newTestServiceProviderCluster builds a ServiceProviderCluster addressable by the
// mock ResourcesDBClient with the given managed resource group reference.
func newTestServiceProviderCluster(reference coreapi.AzureReference) *coreapi.ServiceProviderCluster {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/" + coreapi.ServiceProviderClusterResourceTypeName +
			"/" + coreapi.ServiceProviderClusterResourceName,
	))

	serviceProviderCluster := &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
	}
	serviceProviderCluster.Status.AzureResources.ManagedResourceGroup = reference
	return serviceProviderCluster
}

// newTestSubscription builds a Subscription for the SliceSubscriptionLister with
// the given tenant ID.
func newTestSubscription(tenantID *string) *coreapi.Subscription {
	subscriptionResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID))
	return &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   subscriptionResourceID,
			PartitionKey: strings.ToLower(subscriptionResourceID.SubscriptionID),
		},
		Properties: &coreapi.SubscriptionProperties{
			TenantId: tenantID,
		},
	}
}

func testManagedResourceGroupID(t *testing.T) *azcorearm.ResourceID {
	t.Helper()
	return metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testManagedRGName))
}
