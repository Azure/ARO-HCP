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

package controllerutils

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
)

type capturingSubscriptionInformer struct {
	cache.SharedIndexInformer
	handler cache.ResourceEventHandler
	options cache.HandlerOptions
}

func (i *capturingSubscriptionInformer) AddEventHandlerWithOptions(handler cache.ResourceEventHandler, options cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error) {
	i.handler, i.options = handler, options
	return nil, nil
}

type discoveryTestInformers struct {
	coreinformers.BackendInformers
	informer *capturingSubscriptionInformer
	lister   corelisters.SubscriptionLister
}

func (i *discoveryTestInformers) Subscriptions() (cache.SharedIndexInformer, corelisters.SubscriptionLister) {
	return i.informer, i.lister
}

type discoveryTestSubscriptionLister struct {
	corelisters.SubscriptionLister
	subscription *coreapi.Subscription
	err          error
}

type discoveryTestMRGSyncer struct {
	keys chan ManagedResourceGroupKey
}

func (s *discoveryTestMRGSyncer) SyncOnce(ctx context.Context, key ManagedResourceGroupKey) error {
	select {
	case s.keys <- key:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *discoveryTestSubscriptionLister) Get(context.Context, string) (*coreapi.Subscription, error) {
	return l.subscription, l.err
}

func newDiscoveryTestController(t *testing.T) (*managedResourceGroupWatchingController, *capturingSubscriptionInformer, *discoveryTestSubscriptionLister, *azureclient.MockFirstPartyApplicationClientBuilder) {
	t.Helper()
	builder := azureclient.NewMockFirstPartyApplicationClientBuilder(gomock.NewController(t))
	subscription := &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID: metadataapi.Must(coreapihelpers.ToSubscriptionResourceID("sub1")),
		},
		Properties: &coreapi.SubscriptionProperties{TenantId: ptr.To("tenant1")},
	}
	lister := &discoveryTestSubscriptionLister{subscription: subscription}
	informer := &capturingSubscriptionInformer{}
	controller := NewManagedResourceGroupWatchingController("eastus", nil, builder, &discoveryTestInformers{
		informer: informer,
		lister:   lister,
	}, 24*time.Hour).(*managedResourceGroupWatchingController)
	t.Cleanup(controller.queue.ShutDown)
	t.Cleanup(controller.subscriptionQueue.ShutDown)
	return controller, informer, lister, builder
}

func discoveryTestPager(fetch func(context.Context, *armresources.ResourceGroupsClientListResponse) (armresources.ResourceGroupsClientListResponse, error)) *azruntime.Pager[armresources.ResourceGroupsClientListResponse] {
	return azruntime.NewPager(azruntime.PagingHandler[armresources.ResourceGroupsClientListResponse]{
		More: func(page armresources.ResourceGroupsClientListResponse) bool {
			return page.NextLink != nil
		},
		Fetcher: fetch,
	})
}

func TestManagedResourceGroupEventsOnlyEnqueueSubscriptions(t *testing.T) {
	c, informer, lister, _ := newDiscoveryTestController(t)
	require.Equal(t, 24*time.Hour, *informer.options.ResyncPeriod)

	// No Azure calls are expected while handling either adds or updates.
	informer.handler.OnAdd(lister.subscription, false)
	informer.handler.OnUpdate(lister.subscription, lister.subscription)
	require.Equal(t, 1, c.subscriptionQueue.Len(), "events for the same subscription should be deduplicated")
	require.Zero(t, c.queue.Len(), "discovery should not run in the informer handler")
}

func TestManagedResourceGroupDiscoveryPagesAndFilters(t *testing.T) {
	c, _, lister, builder := newDiscoveryTestController(t)
	rgClient := azureclient.NewMockResourceGroupsClient(gomock.NewController(t))
	builder.EXPECT().ResourceGroupsClient("tenant2", "sub1").Return(rgClient, nil)
	// The worker must see current lister data, even if an older event was queued.
	c.enqueueSubscription(lister.subscription)
	lister.subscription.Properties.TenantId = ptr.To("tenant2")
	managedBy := "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1"
	pages := 0
	rgClient.EXPECT().NewListPager(gomock.Any()).DoAndReturn(func(options *armresources.ResourceGroupsClientListOptions) *azruntime.Pager[armresources.ResourceGroupsClientListResponse] {
		require.EqualValues(t, 100, *options.Top)
		return discoveryTestPager(func(ctx context.Context, _ *armresources.ResourceGroupsClientListResponse) (armresources.ResourceGroupsClientListResponse, error) {
			deadline, ok := ctx.Deadline()
			require.True(t, ok, "Azure paging should have a deadline")
			require.LessOrEqual(t, time.Until(deadline), managedResourceGroupDiscoveryTimeout)
			pages++
			page := armresources.ResourceGroupsClientListResponse{}
			if pages == 1 {
				page.NextLink = ptr.To("next")
				page.Value = []*armresources.ResourceGroup{
					{Name: ptr.To("valid-1"), Location: ptr.To("EASTUS"), ManagedBy: &managedBy},
					{Name: ptr.To("other-region"), Location: ptr.To("westus"), ManagedBy: &managedBy},
					{Name: ptr.To("unmanaged"), Location: ptr.To("eastus")},
					{Name: ptr.To("invalid-id"), Location: ptr.To("eastus"), ManagedBy: ptr.To("invalid")},
					{Name: ptr.To("other-type"), Location: ptr.To("eastus"), ManagedBy: ptr.To("/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachines/vm1")},
				}
			} else {
				page.Value = []*armresources.ResourceGroup{{Name: ptr.To("valid-2"), Location: ptr.To("eastus"), ManagedBy: &managedBy}}
			}
			return page, nil
		})
	})
	require.True(t, c.processNextSubscription(t.Context()))
	require.Equal(t, 2, pages)
	require.Equal(t, 2, c.queue.Len())
	for _, name := range []string{"valid-1", "valid-2"} {
		key, shutdown := c.queue.Get()
		require.False(t, shutdown)
		require.Equal(t, name, key.ResourceGroupName)
		require.Equal(t, managedBy, key.ManagedBy)
		c.queue.Done(key)
	}
}

func TestManagedResourceGroupDiscoveryRetry(t *testing.T) {
	for _, failure := range []string{"lister", "client", "page", "timeout"} {
		t.Run(failure, func(t *testing.T) {
			c, _, lister, builder := newDiscoveryTestController(t)
			rgClient := azureclient.NewMockResourceGroupsClient(gomock.NewController(t))
			transientErr := errors.New("transient failure")
			switch failure {
			case "lister":
				lister.err = transientErr
			case "client":
				builder.EXPECT().ResourceGroupsClient("tenant1", "sub1").Return(nil, transientErr)
			case "page", "timeout":
				builder.EXPECT().ResourceGroupsClient("tenant1", "sub1").Return(rgClient, nil)
				if failure == "timeout" {
					c.discoveryTimeout = 20 * time.Millisecond
				}
				rgClient.EXPECT().NewListPager(gomock.Any()).Return(discoveryTestPager(func(ctx context.Context, _ *armresources.ResourceGroupsClientListResponse) (armresources.ResourceGroupsClientListResponse, error) {
					if failure == "timeout" {
						<-ctx.Done()
						return armresources.ResourceGroupsClientListResponse{}, ctx.Err()
					}
					return armresources.ResourceGroupsClientListResponse{}, transientErr
				}))
			}
			key := SubscriptionKey{SubscriptionID: "sub1"}
			c.enqueueSubscription(lister.subscription)
			require.True(t, c.processNextSubscription(t.Context()))
			require.Equal(t, 1, c.subscriptionQueue.NumRequeues(key), "discovery errors must trigger rate-limited retry")

			lister.err = nil
			c.discoveryTimeout = managedResourceGroupDiscoveryTimeout
			builder.EXPECT().ResourceGroupsClient("tenant1", "sub1").Return(rgClient, nil)
			rgClient.EXPECT().NewListPager(gomock.Any()).Return(discoveryTestPager(func(context.Context, *armresources.ResourceGroupsClientListResponse) (armresources.ResourceGroupsClientListResponse, error) {
				return armresources.ResourceGroupsClientListResponse{}, nil
			}))
			require.Eventually(t, func() bool { return c.subscriptionQueue.Len() == 1 }, time.Second, time.Millisecond)
			require.True(t, c.processNextSubscription(t.Context()))
			require.Zero(t, c.subscriptionQueue.NumRequeues(key), "successful discovery must reset backoff")
		})
	}
}

func TestManagedResourceGroupDiscoverySkipsMissingSubscriptionOrTenant(t *testing.T) {
	for _, missing := range []string{"subscription", "properties", "tenant"} {
		t.Run(missing, func(t *testing.T) {
			c, _, lister, _ := newDiscoveryTestController(t)
			switch missing {
			case "subscription":
				lister.err = &azcore.ResponseError{StatusCode: http.StatusNotFound}
			case "properties":
				lister.subscription.Properties = nil
			case "tenant":
				lister.subscription.Properties.TenantId = nil
			}
			require.NoError(t, c.discoverAndEnqueueManagedResourceGroups(t.Context(), SubscriptionKey{SubscriptionID: "sub1"}))
		})
	}
}

func TestManagedResourceGroupDiscoveryTimeoutCancelsPaging(t *testing.T) {
	c, _, _, builder := newDiscoveryTestController(t)
	c.discoveryTimeout = 20 * time.Millisecond
	rgClient := azureclient.NewMockResourceGroupsClient(gomock.NewController(t))
	builder.EXPECT().ResourceGroupsClient("tenant1", "sub1").Return(rgClient, nil)
	var firstDeadline time.Time
	pages := 0
	rgClient.EXPECT().NewListPager(gomock.Any()).Return(discoveryTestPager(func(ctx context.Context, _ *armresources.ResourceGroupsClientListResponse) (armresources.ResourceGroupsClientListResponse, error) {
		pages++
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		if pages == 1 {
			firstDeadline = deadline
			page := armresources.ResourceGroupsClientListResponse{}
			page.NextLink = ptr.To("next")
			return page, nil
		}
		require.Equal(t, firstDeadline, deadline, "all pages should share a single discovery deadline")
		<-ctx.Done()
		return armresources.ResourceGroupsClientListResponse{}, ctx.Err()
	}))
	require.ErrorIs(t, c.discoverAndEnqueueManagedResourceGroups(t.Context(), SubscriptionKey{SubscriptionID: "sub1"}), context.DeadlineExceeded)
	require.Equal(t, 2, pages)
}

func TestManagedResourceGroupShutdownCancelsDiscovery(t *testing.T) {
	c, _, lister, builder := newDiscoveryTestController(t)
	rgClient := azureclient.NewMockResourceGroupsClient(gomock.NewController(t))
	builder.EXPECT().ResourceGroupsClient("tenant1", "sub1").Return(rgClient, nil)
	started := make(chan struct{})
	pageErr := make(chan error, 1)
	rgClient.EXPECT().NewListPager(gomock.Any()).Return(discoveryTestPager(func(ctx context.Context, _ *armresources.ResourceGroupsClientListResponse) (armresources.ResourceGroupsClientListResponse, error) {
		close(started)
		<-ctx.Done()
		pageErr <- ctx.Err()
		return armresources.ResourceGroupsClientListResponse{}, ctx.Err()
	}))
	c.enqueueSubscription(lister.subscription)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(ctx, 1)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("discovery worker did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("controller did not stop after cancellation")
	}
	require.ErrorIs(t, <-pageErr, context.Canceled)
	require.True(t, c.subscriptionQueue.ShuttingDown())
	require.True(t, c.queue.ShuttingDown())
	require.Zero(t, c.subscriptionQueue.NumRequeues(SubscriptionKey{SubscriptionID: "sub1"}), "shutdown must not schedule discovery retries")
}

func TestManagedResourceGroupProcessingContinuesDuringDiscovery(t *testing.T) {
	c, _, lister, builder := newDiscoveryTestController(t)
	processed := make(chan ManagedResourceGroupKey, 1)
	c.syncer = &discoveryTestMRGSyncer{keys: processed}
	rgClient := azureclient.NewMockResourceGroupsClient(gomock.NewController(t))
	builder.EXPECT().ResourceGroupsClient("tenant1", "sub1").Return(rgClient, nil)
	scanning := make(chan struct{})
	rgClient.EXPECT().NewListPager(gomock.Any()).Return(discoveryTestPager(func(ctx context.Context, previous *armresources.ResourceGroupsClientListResponse) (armresources.ResourceGroupsClientListResponse, error) {
		if previous == nil {
			page := armresources.ResourceGroupsClientListResponse{}
			page.NextLink = ptr.To("next")
			page.Value = []*armresources.ResourceGroup{{
				Name:      ptr.To("managed-rg"),
				Location:  ptr.To("eastus"),
				ManagedBy: ptr.To("/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster1"),
			}}
			return page, nil
		}
		close(scanning)
		<-ctx.Done()
		return armresources.ResourceGroupsClientListResponse{}, ctx.Err()
	}))
	c.enqueueSubscription(lister.subscription)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(ctx, 1)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("controller did not stop")
		}
	}()
	select {
	case <-scanning:
	case <-time.After(time.Second):
		t.Fatal("discovery did not reach the second page")
	}
	select {
	case key := <-processed:
		require.Equal(t, "managed-rg", key.ResourceGroupName)
	case <-time.After(time.Second):
		t.Fatal("slow discovery blocked MRG processing")
	}
}

func TestManagedResourceGroupDiscoveryWaitsForCacheSync(t *testing.T) {
	c, _, lister, _ := newDiscoveryTestController(t)
	checked := make(chan struct{}, 1)
	c.AddCacheSyncs(func() bool {
		select {
		case checked <- struct{}{}:
		default:
		}
		return false
	})
	c.enqueueSubscription(lister.subscription)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(ctx, 1)
	}()
	select {
	case <-checked:
	case <-time.After(time.Second):
		t.Fatal("controller did not check cache synchronization")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("controller did not stop while waiting for cache synchronization")
	}
	require.True(t, c.subscriptionQueue.ShuttingDown())
	require.True(t, c.queue.ShuttingDown())
}
