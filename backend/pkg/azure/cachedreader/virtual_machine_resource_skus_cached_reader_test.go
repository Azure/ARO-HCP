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

package cachedreader

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	utilsclock "k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
)

const (
	testTenantID       = "11111111-1111-1111-1111-111111111111"
	testSubscriptionID = "22222222-2222-2222-2222-222222222222"
	testVMSize         = "Standard_D4as_v4"
	testVMFamily       = "standardDASv4Family"
	testLocation       = "eastus"
)

// testListOptions is the ResourceSKUsClientListOptions every list call is expected to use,
// filtered to testLocation.
func testListOptions() *armcompute.ResourceSKUsClientListOptions {
	return &armcompute.ResourceSKUsClientListOptions{
		Filter: ptr.To(resourceSKUsListFilterForLocation(testLocation)),
	}
}

func makeTestVMResourceSKU(name, family string) *armcompute.ResourceSKU {
	return &armcompute.ResourceSKU{
		Name:         ptr.To(name),
		Family:       ptr.To(family),
		ResourceType: ptr.To(skuResourceTypeVirtualMachines),
	}
}

func makeTestDiskResourceSKU(name string) *armcompute.ResourceSKU {
	return &armcompute.ResourceSKU{
		Name:         ptr.To(name),
		ResourceType: ptr.To("disks"),
	}
}

func makeTestSKUListPager(skus []*armcompute.ResourceSKU, fetchErr error) *runtime.Pager[armcompute.ResourceSKUsClientListResponse] {
	pages := []armcompute.ResourceSKUsClientListResponse{{
		ResourceSKUsResult: armcompute.ResourceSKUsResult{Value: skus},
	}}
	idx := -1
	return runtime.NewPager(runtime.PagingHandler[armcompute.ResourceSKUsClientListResponse]{
		More: func(page armcompute.ResourceSKUsClientListResponse) bool {
			return idx+1 < len(pages)
		},
		Fetcher: func(ctx context.Context, page *armcompute.ResourceSKUsClientListResponse) (armcompute.ResourceSKUsClientListResponse, error) {
			if fetchErr != nil {
				return armcompute.ResourceSKUsClientListResponse{}, fetchErr
			}
			idx++
			return pages[idx], nil
		},
	})
}

func TestFPAVirtualMachineResourceSKUsCachedReaderController_ListVirtualMachineSKUs(t *testing.T) {
	ctx := context.Background()
	vmSKU := makeTestVMResourceSKU(testVMSize, testVMFamily)
	diskSKU := makeTestDiskResourceSKU("Premium_LRS")

	tests := []struct {
		name  string
		setup func(ctrl *gomock.Controller) (*azureclient.MockFirstPartyApplicationClientBuilder, utilsclock.PassiveClock)
		calls []struct {
			advanceClockBy  time.Duration
			subscriptionID  string
			refresh         bool
			wantSKUNames    []string
			wantError       bool
			wantErrContains string
		}
	}{
		{
			name: "caches successful list and filters to virtualMachines",
			setup: func(ctrl *gomock.Controller) (*azureclient.MockFirstPartyApplicationClientBuilder, utilsclock.PassiveClock) {
				mockClient := azureclient.NewMockResourceSKUsClient(ctrl)
				mockClient.EXPECT().NewListPager(testListOptions()).Return(makeTestSKUListPager([]*armcompute.ResourceSKU{vmSKU, diskSKU}, nil)).Times(1)
				mockBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
				mockBuilder.EXPECT().ResourceSKUsClient(testTenantID, testSubscriptionID).Return(mockClient, nil).Times(1)
				return mockBuilder, utilsclock.RealClock{}
			},
			calls: []struct {
				advanceClockBy  time.Duration
				subscriptionID  string
				refresh         bool
				wantSKUNames    []string
				wantError       bool
				wantErrContains string
			}{
				{
					subscriptionID: testSubscriptionID,
					refresh:        true,
					wantSKUNames:   []string{testVMSize},
				},
				{
					subscriptionID: testSubscriptionID,
					wantSKUNames:   []string{testVMSize},
				},
			},
		},
		{
			name: "treats subscription ID casing as the same cache entry",
			setup: func(ctrl *gomock.Controller) (*azureclient.MockFirstPartyApplicationClientBuilder, utilsclock.PassiveClock) {
				const lowerSub = "abcdef12-3456-7890-abcd-ef1234567890"
				mockClient := azureclient.NewMockResourceSKUsClient(ctrl)
				mockClient.EXPECT().NewListPager(testListOptions()).Return(makeTestSKUListPager([]*armcompute.ResourceSKU{vmSKU}, nil)).Times(1)
				mockBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
				// Azure is called with the lowercased subscription ID from the normalized key.
				mockBuilder.EXPECT().ResourceSKUsClient(testTenantID, lowerSub).Return(mockClient, nil).Times(1)
				return mockBuilder, utilsclock.RealClock{}
			},
			calls: []struct {
				advanceClockBy  time.Duration
				subscriptionID  string
				refresh         bool
				wantSKUNames    []string
				wantError       bool
				wantErrContains string
			}{
				{
					subscriptionID: "ABCDEF12-3456-7890-ABCD-EF1234567890",
					refresh:        true,
					wantSKUNames:   []string{testVMSize},
				},
				{
					// Different casing must hit the same cache entry via List; no second SyncOnce/Azure list.
					subscriptionID: "abcdef12-3456-7890-abcd-ef1234567890",
					wantSKUNames:   []string{testVMSize},
				},
			},
		},
		{
			name: "caches error within error freshness TTL",
			setup: func(ctrl *gomock.Controller) (*azureclient.MockFirstPartyApplicationClientBuilder, utilsclock.PassiveClock) {
				mockClient := azureclient.NewMockResourceSKUsClient(ctrl)
				mockClient.EXPECT().NewListPager(testListOptions()).Return(makeTestSKUListPager(nil, errors.New("service unavailable"))).Times(1)
				mockBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
				mockBuilder.EXPECT().ResourceSKUsClient(testTenantID, testSubscriptionID).Return(mockClient, nil).Times(1)
				return mockBuilder, clocktesting.NewFakePassiveClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
			},
			calls: []struct {
				advanceClockBy  time.Duration
				subscriptionID  string
				refresh         bool
				wantSKUNames    []string
				wantError       bool
				wantErrContains string
			}{
				{
					subscriptionID:  testSubscriptionID,
					refresh:         true,
					wantError:       true,
					wantErrContains: "service unavailable",
				},
				{
					advanceClockBy:  2 * time.Minute,
					subscriptionID:  testSubscriptionID,
					wantError:       true,
					wantErrContains: "service unavailable",
				},
				{
					advanceClockBy:  resourceSKUsCacheErrorFreshnessTTL - 2*time.Minute,
					subscriptionID:  testSubscriptionID,
					wantError:       true,
					wantErrContains: "service unavailable",
				},
			},
		},
		{
			name: "recovers after error freshness TTL expiry and serves success until success TTL expires",
			setup: func(ctrl *gomock.Controller) (*azureclient.MockFirstPartyApplicationClientBuilder, utilsclock.PassiveClock) {
				mockClient := azureclient.NewMockResourceSKUsClient(ctrl)
				gomock.InOrder(
					mockClient.EXPECT().NewListPager(testListOptions()).Return(makeTestSKUListPager(nil, errors.New("temporary"))),
					mockClient.EXPECT().NewListPager(testListOptions()).Return(makeTestSKUListPager([]*armcompute.ResourceSKU{vmSKU}, nil)),
				)
				mockBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
				mockBuilder.EXPECT().ResourceSKUsClient(testTenantID, testSubscriptionID).Return(mockClient, nil).Times(2)
				return mockBuilder, clocktesting.NewFakePassiveClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
			},
			calls: []struct {
				advanceClockBy  time.Duration
				subscriptionID  string
				refresh         bool
				wantSKUNames    []string
				wantError       bool
				wantErrContains string
			}{
				{
					subscriptionID:  testSubscriptionID,
					refresh:         true,
					wantError:       true,
					wantErrContains: "temporary",
				},
				{
					// The error entry's TTL has elapsed, so this is a genuine cache miss.
					advanceClockBy: resourceSKUsCacheErrorFreshnessTTL + time.Second,
					subscriptionID: testSubscriptionID,
					refresh:        true,
					wantSKUNames:   []string{testVMSize},
				},
				{
					// Still within the success TTL: served straight from the cache, no new Azure call.
					advanceClockBy: 10 * time.Minute,
					subscriptionID: testSubscriptionID,
					wantSKUNames:   []string{testVMSize},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			builder, clock := tt.setup(ctrl)
			controller := newFPAVirtualMachineResourceSKUsCachedReaderController(builder, defaultResourceSKUsCacheMaxEntries, clock, testLocation)

			for _, call := range tt.calls {
				if call.advanceClockBy > 0 {
					fakeClock, ok := clock.(*clocktesting.FakePassiveClock)
					require.True(t, ok, "advanceClockBy requires a FakePassiveClock")
					fakeClock.SetTime(fakeClock.Now().Add(call.advanceClockBy))
				}

				if call.refresh {
					// Drive the refresh directly through SyncOnce, as the workqueue worker
					// would when processing a queued key.
					require.NoError(t, controller.SyncOnce(ctx, newVirtualMachineResourceSKUKey(testTenantID, call.subscriptionID)))
				}

				got, err := controller.ListVirtualMachineSKUs(ctx, testTenantID, call.subscriptionID, testLocation)
				if call.wantError {
					require.Error(t, err)
					if call.wantErrContains != "" {
						assert.ErrorContains(t, err, call.wantErrContains)
					}
					continue
				}
				require.NoError(t, err)
				var names []string
				for _, sku := range got {
					names = append(names, *sku.Name)
				}
				assert.Equal(t, call.wantSKUNames, names)
			}
		})
	}
}

// TestFPAVirtualMachineResourceSKUsCachedReaderController_SyncOnceEarlyReturnOnHit verifies that
// once a key has any cache entry (success or error), SyncOnce returns nil without calling Azure
// again. Retries happen only after the entry's TTL expires and ensureCached treats the key as a miss.
func TestFPAVirtualMachineResourceSKUsCachedReaderController_SyncOnceEarlyReturnOnHit(t *testing.T) {
	ctx := context.Background()
	vmSKU := makeTestVMResourceSKU(testVMSize, testVMFamily)

	t.Run("success entry", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := azureclient.NewMockResourceSKUsClient(ctrl)
		mockClient.EXPECT().NewListPager(testListOptions()).Return(makeTestSKUListPager([]*armcompute.ResourceSKU{vmSKU}, nil)).Times(1)
		mockBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
		mockBuilder.EXPECT().ResourceSKUsClient(testTenantID, testSubscriptionID).Return(mockClient, nil).Times(1)
		controller := NewFPAVirtualMachineResourceSKUsCachedReaderController(mockBuilder, testLocation)
		key := newVirtualMachineResourceSKUKey(testTenantID, testSubscriptionID)

		require.NoError(t, controller.SyncOnce(ctx, key))
		require.NoError(t, controller.SyncOnce(ctx, key), "second SyncOnce must early-return without calling Azure")

		skus, err := controller.ListVirtualMachineSKUs(ctx, testTenantID, testSubscriptionID, testLocation)
		require.NoError(t, err)
		require.Len(t, skus, 1)
		assert.Equal(t, testVMSize, *skus[0].Name)
	})

	t.Run("error entry", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := azureclient.NewMockResourceSKUsClient(ctrl)
		mockClient.EXPECT().NewListPager(testListOptions()).Return(makeTestSKUListPager(nil, errors.New("transient"))).Times(1)
		mockBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
		mockBuilder.EXPECT().ResourceSKUsClient(testTenantID, testSubscriptionID).Return(mockClient, nil).Times(1)
		controller := NewFPAVirtualMachineResourceSKUsCachedReaderController(mockBuilder, testLocation)
		key := newVirtualMachineResourceSKUKey(testTenantID, testSubscriptionID)

		require.NoError(t, controller.SyncOnce(ctx, key))
		require.NoError(t, controller.SyncOnce(ctx, key), "second SyncOnce must early-return even when the cached entry is an error")

		_, err := controller.ListVirtualMachineSKUs(ctx, testTenantID, testSubscriptionID, testLocation)
		require.Error(t, err)
		assert.ErrorContains(t, err, "transient")
	})
}

// TestFPAVirtualMachineResourceSKUsCachedReaderController_SuccessTTLExpiryRefreshes verifies that
// after the success TTL expires the next SyncOnce pulls through from Azure again (no stale-while-
// revalidate): ensureCached only serves an entry while it remains in the LRUExpireCache.
func TestFPAVirtualMachineResourceSKUsCachedReaderController_SuccessTTLExpiryRefreshes(t *testing.T) {
	ctx := context.Background()
	vmSKU := makeTestVMResourceSKU(testVMSize, testVMFamily)
	refreshedSKU := makeTestVMResourceSKU("Standard_D8as_v4", "standardDASv4Family")

	ctrl := gomock.NewController(t)
	mockClient := azureclient.NewMockResourceSKUsClient(ctrl)
	gomock.InOrder(
		mockClient.EXPECT().NewListPager(testListOptions()).Return(makeTestSKUListPager([]*armcompute.ResourceSKU{vmSKU}, nil)),
		mockClient.EXPECT().NewListPager(testListOptions()).Return(makeTestSKUListPager([]*armcompute.ResourceSKU{refreshedSKU}, nil)),
	)
	mockBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
	mockBuilder.EXPECT().ResourceSKUsClient(testTenantID, testSubscriptionID).Return(mockClient, nil).Times(2)
	fakeClock := clocktesting.NewFakePassiveClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	controller := newFPAVirtualMachineResourceSKUsCachedReaderController(mockBuilder, defaultResourceSKUsCacheMaxEntries, fakeClock, testLocation)
	key := newVirtualMachineResourceSKUKey(testTenantID, testSubscriptionID)

	require.NoError(t, controller.SyncOnce(ctx, key))

	skus, err := controller.ListVirtualMachineSKUs(ctx, testTenantID, testSubscriptionID, testLocation)
	require.NoError(t, err)
	require.Len(t, skus, 1)
	assert.Equal(t, testVMSize, *skus[0].Name)
	assert.Equal(t, 0, controller.queue.Len(), "a cache hit must not queue a refresh")

	fakeClock.SetTime(fakeClock.Now().Add(resourceSKUsCacheSuccessFreshnessTTL + time.Second))

	// Entry expired: SyncOnce fills again; List observes the fresh value.
	require.NoError(t, controller.SyncOnce(ctx, key))
	skus, err = controller.ListVirtualMachineSKUs(ctx, testTenantID, testSubscriptionID, testLocation)
	require.NoError(t, err)
	require.Len(t, skus, 1)
	assert.Equal(t, "Standard_D8as_v4", *skus[0].Name)
	assert.Equal(t, 0, controller.queue.Len())
}

func TestFPAVirtualMachineResourceSKUsCachedReaderController_LRUEviction(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	sub1 := "11111111-1111-1111-1111-111111111111"
	sub2 := "22222222-2222-2222-2222-222222222222"
	sub3 := "33333333-3333-3333-3333-333333333333"

	client1 := azureclient.NewMockResourceSKUsClient(ctrl)
	client2 := azureclient.NewMockResourceSKUsClient(ctrl)
	client3 := azureclient.NewMockResourceSKUsClient(ctrl)

	// sub1 is listed twice: once initially, once after eviction by sub3.
	client1.EXPECT().NewListPager(testListOptions()).Return(makeTestSKUListPager([]*armcompute.ResourceSKU{
		makeTestVMResourceSKU("Standard_D2s_v3", "standardDSv3Family"),
	}, nil)).Times(2)
	client2.EXPECT().NewListPager(testListOptions()).Return(makeTestSKUListPager([]*armcompute.ResourceSKU{
		makeTestVMResourceSKU("Standard_D4s_v3", "standardDSv3Family"),
	}, nil)).Times(1)
	client3.EXPECT().NewListPager(testListOptions()).Return(makeTestSKUListPager([]*armcompute.ResourceSKU{
		makeTestVMResourceSKU("Standard_D8s_v3", "standardDSv3Family"),
	}, nil)).Times(1)

	mockBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
	mockBuilder.EXPECT().ResourceSKUsClient(testTenantID, sub1).Return(client1, nil).Times(2)
	mockBuilder.EXPECT().ResourceSKUsClient(testTenantID, sub2).Return(client2, nil).Times(1)
	mockBuilder.EXPECT().ResourceSKUsClient(testTenantID, sub3).Return(client3, nil).Times(1)

	controller := newFPAVirtualMachineResourceSKUsCachedReaderController(mockBuilder, 2, utilsclock.RealClock{}, testLocation)

	require.NoError(t, controller.SyncOnce(ctx, newVirtualMachineResourceSKUKey(testTenantID, sub1)))
	require.NoError(t, controller.SyncOnce(ctx, newVirtualMachineResourceSKUKey(testTenantID, sub2)))
	// Evicts sub1 (least recently used).
	require.NoError(t, controller.SyncOnce(ctx, newVirtualMachineResourceSKUKey(testTenantID, sub3)))
	// Misses cache and refreshes sub1.
	require.NoError(t, controller.SyncOnce(ctx, newVirtualMachineResourceSKUKey(testTenantID, sub1)))
}

// TestFPAVirtualMachineResourceSKUsCachedReaderController_ConcurrentRefreshIsDeduplicated verifies
// that concurrent cache misses for the same subscription are de-duplicated by the workqueue (a
// key already queued or being processed is not re-fetched).
func TestFPAVirtualMachineResourceSKUsCachedReaderController_ConcurrentRefreshIsDeduplicated(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctrl := gomock.NewController(t)

	var listStarted sync.WaitGroup
	listStarted.Add(1)
	var releaseList sync.WaitGroup
	releaseList.Add(1)

	mockClient := azureclient.NewMockResourceSKUsClient(ctrl)
	mockClient.EXPECT().NewListPager(testListOptions()).DoAndReturn(func(options *armcompute.ResourceSKUsClientListOptions) *runtime.Pager[armcompute.ResourceSKUsClientListResponse] {
		listStarted.Done()
		releaseList.Wait()
		return makeTestSKUListPager([]*armcompute.ResourceSKU{makeTestVMResourceSKU(testVMSize, testVMFamily)}, nil)
	}).Times(1)

	mockBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
	mockBuilder.EXPECT().ResourceSKUsClient(testTenantID, testSubscriptionID).Return(mockClient, nil).Times(1)
	controller := NewFPAVirtualMachineResourceSKUsCachedReaderController(mockBuilder, testLocation)
	go controller.Run(ctx, 4)

	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, err := controller.ListVirtualMachineSKUs(ctx, testTenantID, testSubscriptionID, testLocation)
			errs <- err
		}()
	}

	listStarted.Wait()
	releaseList.Done()
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
}

// TestFPAVirtualMachineResourceSKUsCachedReaderController_ListTimesOutWhenNotRunning verifies that a
// cold List without Run (or SyncOnce) enqueues work and fails with the caller's canceled context
// instead of hanging forever or calling Azure.
func TestFPAVirtualMachineResourceSKUsCachedReaderController_ListContextCancelDuringPoll(t *testing.T) {
	ctrl := gomock.NewController(t)
	// No Azure expectations: workers never run, so Resource SKUs must not be called.
	mockBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
	controller := NewFPAVirtualMachineResourceSKUsCachedReaderController(mockBuilder, testLocation)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel the context — the goroutine should should not hang forever

	skus, err := controller.ListVirtualMachineSKUs(ctx, testTenantID, testSubscriptionID, testLocation)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, skus)
	assert.GreaterOrEqual(t, controller.queue.Len(), 1, "miss must enqueue a key for a worker that never came")
}

// TestFPAVirtualMachineResourceSKUsCachedReaderController_SuccessTTLExpiryRefreshesViaListAndRun
// verifies success TTL expiry is recovered through List + Run (ensureCached miss/poll + worker
// SyncOnce), without the test calling SyncOnce directly.
func TestFPAVirtualMachineResourceSKUsCachedReaderController_SuccessTTLExpiryRefreshesViaListAndRun(t *testing.T) {
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	vmSKU := makeTestVMResourceSKU(testVMSize, testVMFamily)
	refreshedSKU := makeTestVMResourceSKU("Standard_D8as_v4", "standardDASv4Family")

	ctrl := gomock.NewController(t)
	mockClient := azureclient.NewMockResourceSKUsClient(ctrl)
	gomock.InOrder(
		mockClient.EXPECT().NewListPager(testListOptions()).Return(makeTestSKUListPager([]*armcompute.ResourceSKU{vmSKU}, nil)),
		mockClient.EXPECT().NewListPager(testListOptions()).Return(makeTestSKUListPager([]*armcompute.ResourceSKU{refreshedSKU}, nil)),
	)
	mockBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
	mockBuilder.EXPECT().ResourceSKUsClient(testTenantID, testSubscriptionID).Return(mockClient, nil).Times(2)

	fakeClock := clocktesting.NewFakePassiveClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	controller := newFPAVirtualMachineResourceSKUsCachedReaderController(mockBuilder, defaultResourceSKUsCacheMaxEntries, fakeClock, testLocation)
	go controller.Run(runCtx, 1)

	skus, err := controller.ListVirtualMachineSKUs(runCtx, testTenantID, testSubscriptionID, testLocation)
	require.NoError(t, err)
	require.Len(t, skus, 1)
	assert.Equal(t, testVMSize, *skus[0].Name)
	assert.Equal(t, 0, controller.queue.Len(), "")

	fakeClock.SetTime(fakeClock.Now().Add(resourceSKUsCacheSuccessFreshnessTTL + time.Second))

	skus, err = controller.ListVirtualMachineSKUs(runCtx, testTenantID, testSubscriptionID, testLocation)
	require.NoError(t, err)
	require.Len(t, skus, 1)
	assert.Equal(t, "Standard_D8as_v4", *skus[0].Name)
}

// TestFPAVirtualMachineResourceSKUsCachedReaderController_ListReturnsAzureErrorViaRun verifies the
// production path when Azure Resource SKUs list fails: cold List → worker SyncOnce → error cached
// → List returns that error; a second List within the error TTL does not re-fetch; after error TTL
// expiry a third List recovers via Run.
func TestFPAVirtualMachineResourceSKUsCachedReaderController_ListReturnsAzureErrorViaRun(t *testing.T) {
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	vmSKU := makeTestVMResourceSKU(testVMSize, testVMFamily)

	ctrl := gomock.NewController(t)
	mockClient := azureclient.NewMockResourceSKUsClient(ctrl)
	gomock.InOrder(
		mockClient.EXPECT().NewListPager(testListOptions()).Return(makeTestSKUListPager(nil, errors.New("service unavailable"))),
		mockClient.EXPECT().NewListPager(testListOptions()).Return(makeTestSKUListPager([]*armcompute.ResourceSKU{vmSKU}, nil)),
	)
	mockBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
	mockBuilder.EXPECT().ResourceSKUsClient(testTenantID, testSubscriptionID).Return(mockClient, nil).Times(2)

	fakeClock := clocktesting.NewFakePassiveClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	controller := newFPAVirtualMachineResourceSKUsCachedReaderController(mockBuilder, defaultResourceSKUsCacheMaxEntries, fakeClock, testLocation)
	go controller.Run(runCtx, 1)

	skus, err := controller.ListVirtualMachineSKUs(runCtx, testTenantID, testSubscriptionID, testLocation)
	require.Error(t, err)
	assert.ErrorContains(t, err, "service unavailable")
	assert.Nil(t, skus)

	// Still within error TTL: served from cache, no second Azure call (Times(2) only after recovery).
	skus, err = controller.ListVirtualMachineSKUs(context.Background(), testTenantID, testSubscriptionID, testLocation)
	require.Error(t, err)
	assert.ErrorContains(t, err, "service unavailable")
	assert.Nil(t, skus)

	fakeClock.SetTime(fakeClock.Now().Add(resourceSKUsCacheErrorFreshnessTTL + time.Second))

	skus, err = controller.ListVirtualMachineSKUs(runCtx, testTenantID, testSubscriptionID, testLocation)
	require.NoError(t, err)
	require.Len(t, skus, 1)
	assert.Equal(t, testVMSize, *skus[0].Name)
}

func TestFPAVirtualMachineResourceSKUsCachedReaderController_GetVirtualMachineSKU(t *testing.T) {
	ctx := context.Background()
	vmSKU := makeTestVMResourceSKU(testVMSize, testVMFamily)
	diskSKU := makeTestDiskResourceSKU("Premium_LRS")

	t.Run("returns the matching SKU", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := azureclient.NewMockResourceSKUsClient(ctrl)
		mockClient.EXPECT().NewListPager(testListOptions()).Return(makeTestSKUListPager([]*armcompute.ResourceSKU{vmSKU, diskSKU}, nil)).Times(1)
		mockBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
		mockBuilder.EXPECT().ResourceSKUsClient(testTenantID, testSubscriptionID).Return(mockClient, nil).Times(1)
		controller := NewFPAVirtualMachineResourceSKUsCachedReaderController(mockBuilder, testLocation)
		require.NoError(t, controller.SyncOnce(ctx, newVirtualMachineResourceSKUKey(testTenantID, testSubscriptionID)))

		got, err := controller.GetVirtualMachineSKU(ctx, testTenantID, testSubscriptionID, testLocation, testVMSize)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, testVMSize, *got.Name)
		assert.Equal(t, testVMFamily, *got.Family)
	})

	t.Run("returns an error when the VM size is not found", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := azureclient.NewMockResourceSKUsClient(ctrl)
		mockClient.EXPECT().NewListPager(testListOptions()).Return(makeTestSKUListPager([]*armcompute.ResourceSKU{vmSKU, diskSKU}, nil)).Times(1)
		mockBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
		mockBuilder.EXPECT().ResourceSKUsClient(testTenantID, testSubscriptionID).Return(mockClient, nil).Times(1)
		controller := NewFPAVirtualMachineResourceSKUsCachedReaderController(mockBuilder, testLocation)
		require.NoError(t, controller.SyncOnce(ctx, newVirtualMachineResourceSKUKey(testTenantID, testSubscriptionID)))

		const missingVMSize = "Standard_missing_v9"
		got, err := controller.GetVirtualMachineSKU(ctx, testTenantID, testSubscriptionID, testLocation, missingVMSize)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorContains(t, err, missingVMSize)
		assert.ErrorContains(t, err, testSubscriptionID)
		assert.ErrorContains(t, err, testLocation)
	})

	t.Run("returns azure list error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockClient := azureclient.NewMockResourceSKUsClient(ctrl)
		mockClient.EXPECT().NewListPager(testListOptions()).Return(makeTestSKUListPager(nil, errors.New("service unavailable"))).Times(1)
		mockBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
		mockBuilder.EXPECT().ResourceSKUsClient(testTenantID, testSubscriptionID).Return(mockClient, nil).Times(1)
		controller := NewFPAVirtualMachineResourceSKUsCachedReaderController(mockBuilder, testLocation)
		require.NoError(t, controller.SyncOnce(ctx, newVirtualMachineResourceSKUKey(testTenantID, testSubscriptionID)))

		got, err := controller.GetVirtualMachineSKU(ctx, testTenantID, testSubscriptionID, testLocation, testVMSize)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorContains(t, err, "service unavailable")
	})
}

// TestFPAVirtualMachineResourceSKUsCachedReaderController_LocationMismatch verifies the public
// API rejects a caller location that does not match the backend deployment location without
// consulting or refreshing the cache (see PR review discussion on requiring location on the
// interface methods while keeping cache internals scoped to the deployed region).
func TestFPAVirtualMachineResourceSKUsCachedReaderController_LocationMismatch(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	// No ResourceSKUsClient expectation: a location mismatch must not create a client or hit Azure.
	mockBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
	controller := NewFPAVirtualMachineResourceSKUsCachedReaderController(mockBuilder, testLocation)

	const otherLocation = "westus2"

	skus, err := controller.ListVirtualMachineSKUs(ctx, testTenantID, testSubscriptionID, otherLocation)
	require.Error(t, err)
	assert.Nil(t, skus)
	assert.ErrorContains(t, err, otherLocation)
	assert.ErrorContains(t, err, testLocation)

	got, err := controller.GetVirtualMachineSKU(ctx, testTenantID, testSubscriptionID, otherLocation, testVMSize)
	require.Error(t, err)
	assert.Nil(t, got)
	assert.ErrorContains(t, err, otherLocation)
	assert.ErrorContains(t, err, testLocation)
}
