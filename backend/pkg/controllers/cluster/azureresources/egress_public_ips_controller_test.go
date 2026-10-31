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
	goruntime "runtime"
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
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	clusterstatus "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/status"
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
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
)

type egressTestInformers struct {
	coreinformers.BackendInformers
	database corecosmosstorage.ResourcesDBClient
}

type egressWriteFaultDB struct {
	corecosmosstorage.ResourcesDBClient
	beforeReplace func()
	afterReplace  func()
	afterGet      func()
	getErr        error
	replacements  int
	replaceErr    error
}

func (database *egressWriteFaultDB) ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName string) cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster] {
	return &egressWriteFaultCRUD{ResourceCRUD: database.ResourcesDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName), database: database}
}

type egressWriteFaultCRUD struct {
	cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster]
	database *egressWriteFaultDB
}

func (crud *egressWriteFaultCRUD) Get(ctx context.Context, name string) (*coreapi.ServiceProviderCluster, error) {
	if crud.database.getErr != nil {
		return nil, crud.database.getErr
	}
	value, err := crud.ResourceCRUD.Get(ctx, name)
	if err == nil && crud.database.afterGet != nil {
		crud.database.afterGet()
	}
	return value, err
}

func (crud *egressWriteFaultCRUD) Replace(ctx context.Context, value *coreapi.ServiceProviderCluster, options *azcosmos.ItemOptions) (*coreapi.ServiceProviderCluster, error) {
	crud.database.replacements++
	if crud.database.beforeReplace != nil {
		crud.database.beforeReplace()
	}
	stored, err := crud.ResourceCRUD.Replace(ctx, value, options)
	crud.database.replaceErr = err
	if err == nil && crud.database.afterReplace != nil {
		crud.database.afterReplace()
	}
	return stored, err
}

func TestEgressPublicIPsUnfinishedObservation(t *testing.T) {
	for _, initialState := range []string{"all found with error", "partial with error", "malformed", "missing"} {
		for _, faultPoint := range []string{"after invalidation", "before final write", "final conflict", "after final write", "after pending write", "invalidation conflict", "before invalidation"} {
			if (faultPoint == "invalidation conflict" || faultPoint == "before invalidation") && initialState != "missing" {
				continue
			}
			t.Run(initialState+"/"+faultPoint, func(t *testing.T) {
				ctx := context.Background()
				cluster := newTestCluster(false)
				clusterID := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123"))
				cluster.ServiceProviderProperties.ClusterServiceID = &clusterID
				expected, err := coreapihelpers.EgressPublicIPResourceIDs(cluster)
				require.NoError(t, err)
				memory, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, newTestServiceProviderCluster(coreapi.AzureReference{})})
				require.NoError(t, err)
				database := &egressWriteFaultDB{ResourcesDBClient: memory}
				crud := memory.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
				stored := func() *coreapi.ServiceProviderCluster {
					value, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
					require.NoError(t, err)
					return value
				}
				health := func() *metav1.Condition {
					value, err := memory.HCPClusters(testSubscriptionID, testResourceGroupName).Controllers(testClusterName).Get(ctx, EgressPublicIPsControllerName)
					require.NoError(t, err)
					return meta.FindStatusCondition(value.Status.Conditions, "Degraded")
				}
				values := egressListValues(expected)
				transport := &egressListTransport{test: t, pages: []egressListPage{{values: values}, {status: 503}}}
				switch initialState {
				case "partial with error":
					transport.pages[0].values = values[:4]
				case "malformed":
					transport.pages[1] = egressListPage{values: []*armnetwork.PublicIPAddress{nil}}
				case "missing":
					transport.pages = []egressListPage{{values: values[:8]}}
				}
				clock := clocktesting.NewFakePassiveClock(time.Now())
				enqueuer := &fakeAfterEnqueuer{}
				builder := azureclient.NewMockFirstPartyApplicationClientBuilder(gomock.NewController(t))
				client := newEgressListClient(t, transport)
				observing := false
				fault := ""
				builder.EXPECT().PublicIPAddressesClient(testTenantID, testSubscriptionID).DoAndReturn(func(_, _ string) (azureclient.PublicIPAddressesClient, error) {
					for _, observation := range stored().Status.AzureResources.EgressPublicIPs {
						require.NotEqual(t, coreapi.AzureResourceNotFound, observation, "missing state must be durably invalidated before Azure reads")
					}
					if fault == "after invalidation" {
						goruntime.Goexit()
					}
					observing = true
					return client, nil
				}).AnyTimes()
				informers := &egressTestInformers{database: database}
				_, clusterLister := informers.Clusters()
				newController := func() controllerutils.Controller {
					indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
					require.NoError(t, indexer.Add(stored()))
					serviceProviderLister := corelisters.NewServiceProviderClusterLister(indexer)
					return controllerutils.NewClusterWatchingController(EgressPublicIPsControllerName, database, informers, nil, time.Minute, &egressPublicIPsSyncer{
						clock: clock, resourcesDBClient: database, clusterLister: clusterLister, serviceProviderClusterLister: serviceProviderLister,
						subscriptionLister:    &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{newTestSubscription(ptr.To(testTenantID))}},
						azureFPAClientBuilder: builder, enqueueAfter: enqueuer,
					})
				}
				key := controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: testClusterName}
				run := func(interrupted bool) error {
					observing = false
					returned := false
					var syncErr error
					done := make(chan struct{})
					go func() {
						defer close(done)
						syncErr = newController().SyncOnce(ctx, key)
						returned = true
					}()
					<-done
					require.Equal(t, !interrupted, returned)
					return syncErr
				}
				require.Equal(t, initialState != "missing", run(false) != nil)
				originalHealth := health().DeepCopy()
				initial := stored()
				deadline := initial.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName].DeepCopy()
				clock.SetTime(deadline.Time)
				fault = faultPoint
				conflict := fault == "final conflict" || fault == "invalidation conflict"
				database.beforeReplace = func() {
					if (observing && fault == "before final write") || (!observing && fault == "before invalidation") {
						goruntime.Goexit()
					}
					if (observing && fault == "final conflict") || (!observing && fault == "invalidation conflict") {
						sibling := stored()
						sibling.Status.AzureResources.ManagedResourceGroup.AzureResource = testManagedResourceGroupID(t)
						if sibling.Spec.EarliestRecheckTimesByController == nil {
							sibling.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{}
						}
						sibling.Spec.EarliestRecheckTimesByController["other-controller"] = ptr.To(metav1.NewTime(clock.Now().Add(time.Hour)))
						_, err := crud.Replace(ctx, sibling, nil)
						require.NoError(t, err)
					}
				}
				database.afterReplace = func() {
					if observing && (fault == "after final write" || fault == "after pending write") {
						goruntime.Goexit()
					}
				}
				if fault == "after final write" {
					transport.pages = []egressListPage{{values: values}}
				}
				requestCount := transport.requests
				err = run(!conflict)
				if conflict {
					require.True(t, cosmosstorageutils.IsPreconditionFailedError(database.replaceErr))
					require.ErrorIs(t, err, database.replaceErr)
				} else {
					require.Equal(t, originalHealth, health(), "interruption must not report a completed observation")
				}
				if fault == "after invalidation" || fault == "before invalidation" || fault == "invalidation conflict" {
					require.Equal(t, requestCount, transport.requests)
				}
				fault = ""
				database.beforeReplace, database.afterReplace = nil, nil
				if faultPoint == "after pending write" {
					pending := stored()
					writes, reads := database.replacements, transport.requests
					require.NoError(t, run(false))
					require.Equal(t, pending, stored())
					require.Equal(t, writes, database.replacements)
					require.Equal(t, reads, transport.requests)
					require.Equal(t, originalHealth, health())
					deadline = pending.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName].DeepCopy()
					clock.SetTime(deadline.Time)
				}
				if faultPoint != "after final write" {
					assert.Equal(t, deadline, stored().Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName], "unfinished retry must retain its durable marker")
					transport.pages = []egressListPage{{values: values}, {status: 403}}
					requestCount = transport.requests
					require.Error(t, run(false), "restart must observe again, not report success from an old all-Found map")
					require.Equal(t, requestCount+2, transport.requests)
					require.Equal(t, metav1.ConditionTrue, health().Status)
					if originalHealth.Status == metav1.ConditionTrue {
						require.Equal(t, originalHealth.LastTransitionTime, health().LastTransitionTime)
					}
					for range 2 {
						before := stored()
						writes, reads, retries := database.replacements, transport.requests, len(enqueuer.enqueuedKeys)
						require.NoError(t, run(false))
						require.Equal(t, before, stored())
						require.Equal(t, writes, database.replacements)
						require.Equal(t, reads, transport.requests)
						require.Len(t, enqueuer.enqueuedKeys, retries+1)
						require.Equal(t, metav1.ConditionTrue, health().Status)
					}
					clock.SetTime(stored().Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName].Time)
					require.Error(t, run(false))
					clock.SetTime(stored().Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName].Time)
					transport.pages = []egressListPage{{values: values[:8]}}
					require.NoError(t, run(false))
					require.Equal(t, metav1.ConditionFalse, health().Status, "successful missing-only observation clears the error")
					require.Equal(t, coreapi.AzureResourceNotFound, stored().Status.AzureResources.EgressPublicIPs[expected[8].String()])
					clock.SetTime(stored().Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName].Time)
					transport.pages = []egressListPage{{values: values}}
					require.NoError(t, run(false))
				}
				requestCount, replacements := transport.requests, database.replacements
				require.NoError(t, run(false))
				require.Equal(t, requestCount, transport.requests, "persisted successful completion retains the all-Found fast path")
				require.Equal(t, replacements, database.replacements)
				require.Equal(t, metav1.ConditionFalse, health().Status)
				require.Nil(t, stored().Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName])
				if conflict {
					require.NotNil(t, stored().Status.AzureResources.ManagedResourceGroup.AzureResource)
					require.NotNil(t, stored().Spec.EarliestRecheckTimesByController["other-controller"])
				}
			})
		}
	}
}

func TestEgressPublicIPsDelayedInformer(t *testing.T) {
	for _, initialState := range []string{"empty", "unknown", "found"} {
		for _, found := range []int{0, 9} {
			t.Run(fmt.Sprintf("%s/found%d", initialState, found), func(t *testing.T) {
				ctx := context.Background()
				cluster := newTestCluster(false)
				clusterID := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123"))
				cluster.ServiceProviderProperties.ClusterServiceID = &clusterID
				expected, err := coreapihelpers.EgressPublicIPResourceIDs(cluster)
				require.NoError(t, err)
				clock := clocktesting.NewFakePassiveClock(time.Now())
				serviceProvider := newTestServiceProviderCluster(coreapi.AzureReference{})
				if initialState != "empty" {
					serviceProvider.Status.AzureResources.EgressPublicIPs = map[string]coreapi.AzureResourceObservation{}
					for _, resourceID := range expected {
						serviceProvider.Status.AzureResources.EgressPublicIPs[resourceID.String()] = coreapi.AzureResourceUnknown
						if initialState == "found" {
							serviceProvider.Status.AzureResources.EgressPublicIPs[resourceID.String()] = coreapi.AzureResourceFound
						}
					}
					serviceProvider.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{EgressPublicIPsControllerName: ptr.To(metav1.NewTime(clock.Now().Add(-time.Second)))}
				}
				memory, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, serviceProvider})
				require.NoError(t, err)
				database := &egressWriteFaultDB{ResourcesDBClient: memory}
				crud := memory.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
				stored := func() *coreapi.ServiceProviderCluster {
					value, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
					require.NoError(t, err)
					return value
				}
				indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
				require.NoError(t, indexer.Add(stored()))
				transport := &egressListTransport{test: t, pages: []egressListPage{{status: 503}}}
				if found > 0 {
					transport.pages = append([]egressListPage{{values: egressListValues(expected)}}, transport.pages...)
				}
				builder := azureclient.NewMockFirstPartyApplicationClientBuilder(gomock.NewController(t))
				builder.EXPECT().PublicIPAddressesClient(testTenantID, testSubscriptionID).Return(newEgressListClient(t, transport), nil).AnyTimes()
				informers := &egressTestInformers{database: database}
				_, clusterLister := informers.Clusters()
				enqueuer := &fakeAfterEnqueuer{}
				newController := func() controllerutils.WatchingController {
					return controllerutils.NewClusterWatchingController(EgressPublicIPsControllerName, database, informers, nil, time.Minute, &egressPublicIPsSyncer{
						clock: clock, resourcesDBClient: database, clusterLister: clusterLister,
						serviceProviderClusterLister: corelisters.NewServiceProviderClusterLister(indexer),
						subscriptionLister:           &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{newTestSubscription(ptr.To(testTenantID))}},
						azureFPAClientBuilder:        builder, enqueueAfter: enqueuer,
					})
				}
				key := controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: testClusterName}
				controller := newController()
				require.Error(t, controller.SyncOnce(ctx, key))
				pending := stored()
				healthCRUD := memory.HCPClusters(testSubscriptionID, testResourceGroupName).Controllers(testClusterName)
				failed, err := healthCRUD.Get(ctx, EgressPublicIPsControllerName)
				require.NoError(t, err)
				reads, writes := transport.requests, database.replacements
				for retry := range 3 {
					clock.SetTime(clock.Now().Add(time.Second))
					if retry == 1 {
						controller = newController()
					}
					assert.NoError(t, controller.SyncOnce(ctx, key))
					assert.Equal(t, reads, transport.requests, "undelivered informer updates must not permit another enumeration")
					assert.Equal(t, writes, database.replacements, "cooldown must not attempt stale-ETag replacements")
					assert.Equal(t, pending, stored())
					health, err := healthCRUD.Get(ctx, EgressPublicIPsControllerName)
					require.NoError(t, err)
					assert.Equal(t, failed, health)
				}
				require.Len(t, enqueuer.enqueuedDurations, 4)
				for _, readErr := range []error{context.Canceled, cosmosstorageutils.NewNotFoundError(), errors.New("store unavailable")} {
					database.getErr = readErr
					require.ErrorIs(t, controller.SyncOnce(ctx, key), readErr)
					require.Equal(t, reads, transport.requests)
					require.Equal(t, writes, database.replacements)
					require.Equal(t, pending, stored())
					health, err := healthCRUD.Get(ctx, EgressPublicIPsControllerName)
					require.NoError(t, err)
					condition := meta.FindStatusCondition(health.Status.Conditions, "Degraded")
					require.Equal(t, metav1.ConditionTrue, condition.Status)
					require.Equal(t, meta.FindStatusCondition(failed.Status.Conditions, "Degraded").LastTransitionTime, condition.LastTransitionTime)
				}
				database.getErr = nil
				require.NoError(t, controller.SyncOnce(ctx, key))
				require.Equal(t, reads, transport.requests)
				clock.SetTime(pending.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName].Time)
				transport.pages = []egressListPage{{values: egressListValues(expected)}}
				require.NoError(t, controller.SyncOnce(ctx, key))
				require.Equal(t, reads+1, transport.requests)
				require.Nil(t, stored().Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName])
				require.NoError(t, indexer.Update(pending))
				require.NoError(t, controller.SyncOnce(ctx, key))
				require.Equal(t, reads+1, transport.requests, "late informer delivery must not restart completed observation")
				health, err := healthCRUD.Get(ctx, EgressPublicIPsControllerName)
				require.NoError(t, err)
				require.Equal(t, metav1.ConditionFalse, meta.FindStatusCondition(health.Status.Conditions, "Degraded").Status)
			})
		}
	}
}

func TestEgressPublicIPsConcurrentKeys(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	database := corecosmosstoragetesting.NewMockResourcesDBClient()
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	transports := map[string]*egressListTransport{}
	keys := []controllerutils.HCPClusterKey{}
	for _, name := range []string{"failing", "successful"} {
		cluster := newTestCluster(false)
		cluster.ID = metadataapi.Must(azcorearm.ParseResourceID(strings.ReplaceAll(cluster.ID.String(), testClusterName, name)))
		cluster.ResourceID = cluster.ID
		cluster.Name = name
		cluster.CustomerProperties.Platform.ManagedResourceGroup = name
		clusterID := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/" + name))
		cluster.ServiceProviderProperties.ClusterServiceID = &clusterID
		expected, err := coreapihelpers.EgressPublicIPResourceIDs(cluster)
		require.NoError(t, err)
		_, err = database.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
		require.NoError(t, err)
		serviceProvider := newTestServiceProviderCluster(coreapi.AzureReference{})
		serviceProvider.ResourceID = metadataapi.Must(azcorearm.ParseResourceID(strings.ReplaceAll(serviceProvider.ResourceID.String(), testClusterName, name)))
		stored, err := database.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, name).Create(ctx, serviceProvider, nil)
		require.NoError(t, err)
		require.NoError(t, indexer.Add(stored))
		page := egressListPage{values: egressListValues(expected)}
		if name == "failing" {
			page = egressListPage{status: 503}
		}
		transports[name] = &egressListTransport{test: t, resourceGroup: name, pages: []egressListPage{page}}
		keys = append(keys, controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: name})
	}
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	mockController := gomock.NewController(t)
	client := azureclient.NewMockPublicIPAddressesClient(mockController)
	client.EXPECT().NewListPager(gomock.Any(), nil).DoAndReturn(func(group string, options *armnetwork.PublicIPAddressesClientListOptions) *runtime.Pager[armnetwork.PublicIPAddressesClientListResponse] {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			t.Error("concurrent keys did not both reach enumeration")
		}
		return newEgressListClient(t, transports[group]).NewListPager(group, options)
	}).Times(2)
	builder := azureclient.NewMockFirstPartyApplicationClientBuilder(mockController)
	builder.EXPECT().PublicIPAddressesClient(testTenantID, testSubscriptionID).Return(client, nil).Times(2)
	informers := &egressTestInformers{database: database}
	_, clusterLister := informers.Clusters()
	controller := controllerutils.NewClusterWatchingController(EgressPublicIPsControllerName, database, informers, nil, time.Minute, &egressPublicIPsSyncer{
		clock: clocktesting.NewFakePassiveClock(time.Now()), resourcesDBClient: database, clusterLister: clusterLister,
		serviceProviderClusterLister: corelisters.NewServiceProviderClusterLister(indexer),
		subscriptionLister:           &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{newTestSubscription(ptr.To(testTenantID))}},
		azureFPAClientBuilder:        builder, enqueueAfter: &fakeAfterEnqueuer{},
	})
	results := make(chan error, 2)
	for _, key := range keys {
		go func() {
			var syncErr error
			defer func() { results <- syncErr }()
			syncErr = controller.SyncOnce(ctx, key)
		}()
	}
	for range keys {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("workers did not concurrently enumerate distinct keys")
		}
	}
	close(release)
	errorsSeen := 0
	for range keys {
		if <-results != nil {
			errorsSeen++
		}
	}
	require.Equal(t, 1, errorsSeen)
	for _, key := range keys {
		crud := database.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
		before, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		require.NoError(t, controller.SyncOnce(ctx, key))
		after, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		require.Equal(t, before, after)
		require.Equal(t, 1, transports[key.HCPClusterName].requests)
		health, err := database.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Controllers(key.HCPClusterName).Get(ctx, EgressPublicIPsControllerName)
		require.NoError(t, err)
		want := metav1.ConditionFalse
		if key.HCPClusterName == "failing" {
			want = metav1.ConditionTrue
		}
		require.Equal(t, want, meta.FindStatusCondition(health.Status.Conditions, "Degraded").Status)
	}
}

func (informers *egressTestInformers) Clusters() (cache.SharedIndexInformer, corelisters.ClusterLister) {
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

func TestEgressPublicIPsDegradedCooldown(t *testing.T) {
	for _, status := range []int{403, 503} {
		for _, found := range []int{0, 9} {
			t.Run(fmt.Sprintf("status%d/found%d", status, found), func(t *testing.T) {
				ctx := context.Background()
				cluster := newTestCluster(false)
				clusterID := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123"))
				cluster.ServiceProviderProperties.ClusterServiceID = &clusterID
				expected, err := coreapihelpers.EgressPublicIPResourceIDs(cluster)
				require.NoError(t, err)
				database, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, newTestServiceProviderCluster(coreapi.AzureReference{})})
				require.NoError(t, err)
				informers := &egressTestInformers{database: database}
				_, clusterLister := informers.Clusters()
				_, serviceProviderLister := informers.ServiceProviderClusters()
				_, controllerLister := informers.Controllers()
				clock := clocktesting.NewFakePassiveClock(time.Now())
				enqueuer := &fakeAfterEnqueuer{}
				transport := &egressListTransport{test: t, pages: []egressListPage{{status: status}}}
				if found > 0 {
					transport.pages = append([]egressListPage{{values: egressListValues(expected)}}, transport.pages...)
				}
				builder := azureclient.NewMockFirstPartyApplicationClientBuilder(gomock.NewController(t))
				builder.EXPECT().PublicIPAddressesClient(testTenantID, testSubscriptionID).Return(newEgressListClient(t, transport), nil).AnyTimes()
				newController := func() controllerutils.WatchingController {
					return controllerutils.NewClusterWatchingController(EgressPublicIPsControllerName, database, informers, nil, time.Minute, &egressPublicIPsSyncer{
						clock: clock, resourcesDBClient: database, clusterLister: clusterLister, serviceProviderClusterLister: serviceProviderLister,
						subscriptionLister:    &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{newTestSubscription(ptr.To(testTenantID))}},
						azureFPAClientBuilder: builder, enqueueAfter: enqueuer,
					})
				}
				controller := newController()
				aggregator := clusterstatus.NewClusterDegradedAggregatorController(database, clusterLister, controllerLister, informers, unionkubeapplierinformers.NewUnionKubeApplierInformers(), clock)
				key := controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: testClusterName}
				storedController := func() *coreapi.Controller {
					value, err := database.HCPClusters(testSubscriptionID, testResourceGroupName).Controllers(testClusterName).Get(ctx, EgressPublicIPsControllerName)
					require.NoError(t, err)
					return value
				}
				storedSP := func() *coreapi.ServiceProviderCluster {
					value, err := serviceProviderLister.Get(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
					require.NoError(t, err)
					return value
				}
				assertAggregate := func(want metav1.ConditionStatus) {
					require.NoError(t, aggregator.SyncOnce(ctx, key))
					value, err := clusterLister.Get(ctx, testSubscriptionID, testResourceGroupName, testClusterName)
					require.NoError(t, err)
					require.Equal(t, want, meta.FindStatusCondition(value.Status.Conditions, "Degraded").Status)
				}
				require.Error(t, controller.SyncOnce(ctx, key))
				failed := storedController()
				condition := meta.FindStatusCondition(failed.Status.Conditions, "Degraded")
				require.Equal(t, metav1.ConditionTrue, condition.Status)
				observed := storedSP()
				assertAggregate(metav1.ConditionFalse)
				for _, elapsed := range []time.Duration{time.Second, 31 * time.Second, 40 * time.Second} {
					clock.SetTime(condition.LastTransitionTime.Add(elapsed))
					controller = newController()
					require.NoError(t, controller.SyncOnce(ctx, key))
					t.Logf("elapsed=%s controller Degraded=%s", elapsed, meta.FindStatusCondition(storedController().Status.Conditions, "Degraded").Status)
					assert.Equal(t, condition, meta.FindStatusCondition(storedController().Status.Conditions, "Degraded"), "cooldown events must preserve persisted error and transition time, including after restart")
					assert.Equal(t, failed.CosmosETag, storedController().CosmosETag)
					require.Equal(t, observed.CosmosETag, storedSP().CosmosETag)
					require.Equal(t, len(transport.pages), transport.requests)
					if elapsed > 30*time.Second {
						assertAggregate(metav1.ConditionTrue)
					}
				}
				require.Len(t, enqueuer.enqueuedDurations, 4)
				clock.SetTime(observed.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName].Time)
				require.Error(t, controller.SyncOnce(ctx, key))
				require.Equal(t, 2*len(transport.pages), transport.requests)
				require.Equal(t, condition.LastTransitionTime, meta.FindStatusCondition(storedController().Status.Conditions, "Degraded").LastTransitionTime)
				assertAggregate(metav1.ConditionTrue)
				clock.SetTime(storedSP().Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName].Time)
				transport.pages = []egressListPage{{}}
				require.NoError(t, controller.SyncOnce(ctx, key))
				require.Equal(t, metav1.ConditionFalse, meta.FindStatusCondition(storedController().Status.Conditions, "Degraded").Status)
				assertAggregate(metav1.ConditionFalse)
				for _, observation := range storedSP().Status.AzureResources.EgressPublicIPs {
					require.Equal(t, coreapi.AzureResourceNotFound, observation)
				}
				require.NoError(t, controller.SyncOnce(ctx, key))
				assertAggregate(metav1.ConditionFalse)
				clock.SetTime(storedSP().Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName].Time)
				transport.pages = []egressListPage{{values: egressListValues(expected)}}
				require.NoError(t, controller.SyncOnce(ctx, key))
				assertAggregate(metav1.ConditionFalse)
				require.Nil(t, storedSP().Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName])
			})
		}
	}
}

type egressListPage struct {
	values []*armnetwork.PublicIPAddress
	status int
	code   string
	err    error
	body   string
}

type egressListTransport struct {
	test          *testing.T
	pages         []egressListPage
	requests      int
	resourceGroup string
}

func (transport *egressListTransport) Do(request *http.Request) (*http.Response, error) {
	transport.test.Helper()
	require.Equal(transport.test, http.MethodGet, request.Method)
	group := transport.resourceGroup
	if group == "" {
		group = testManagedRGName
	}
	require.Equal(transport.test, "/subscriptions/"+testSubscriptionID+"/resourceGroups/"+group+"/providers/Microsoft.Network/publicIPAddresses", request.URL.Path, "only resource-group enumeration is allowed, never per-IP GETs")
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
			syncer.controller = controllerutils.NewClusterWatchingController(EgressPublicIPsControllerName, database, &egressTestInformers{database: database}, nil, time.Minute, syncer.egressPublicIPsSyncer)
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
			controllerCRUD := database.HCPClusters(testSubscriptionID, testResourceGroupName).Controllers(testClusterName)
			reported, err := controllerCRUD.Get(ctx, EgressPublicIPsControllerName)
			require.NoError(t, err)
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
				current, err := controllerCRUD.Get(ctx, EgressPublicIPsControllerName)
				require.NoError(t, err)
				require.Equal(t, reported, current, "queued status events must not clear or rewrite controller conditions")
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
		staleCache     bool
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
		{name: "stale informer ETag", found: 9, staleCache: true},
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
			if testCase.staleCache {
				serviceProvider.CosmosETag = "stale"
				syncer.serviceProviderClusterLister = &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{serviceProvider}}
			}
			key := controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: testClusterName}
			err = syncer.SyncOnce(ctx, key)
			if testCase.builderFailure || readFailure {
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
				if updated.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName] != nil {
					require.ErrorIs(t, retryErr, controllerutils.ErrSyncSkipped)
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
	require.ErrorIs(t, syncer.SyncOnce(ctx, key), controllerutils.ErrSyncSkipped)
	unchanged, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	require.Equal(t, initial.CosmosETag, unchanged.CosmosETag, "self-generated events must not recheck before the cooldown")
	clock.SetTime(recheck.Add(time.Second))
	faultDB := &egressWriteFaultDB{ResourcesDBClient: database, afterGet: func() {
		concurrent, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		concurrent.Status.AzureResources.ManagedResourceGroup.AzureResource = testManagedResourceGroupID(t)
		_, err = crud.Replace(ctx, concurrent, nil)
		require.NoError(t, err)
	}}
	syncer.resourcesDBClient = faultDB
	require.True(t, cosmosstorageutils.IsPreconditionFailedError(syncer.SyncOnce(ctx, key)), "failed invalidation must prevent all Azure calls")
	syncer.resourcesDBClient = database
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
	require.Equal(t, initial.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName], stored.Spec.EarliestRecheckTimesByController[EgressPublicIPsControllerName], "unfinished observations must retain their expired retry marker")
}
