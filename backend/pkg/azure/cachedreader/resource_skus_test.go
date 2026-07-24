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
	"sync/atomic"
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

type fakeResourceSKUsClientBuilder struct {
	clients map[string]azureclient.ResourceSKUsClient
	err     error
	calls   atomic.Int32
}

func (f *fakeResourceSKUsClientBuilder) ResourceSKUsClient(tenantID, subscriptionID string) (azureclient.ResourceSKUsClient, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	client, ok := f.clients[subscriptionID]
	if !ok {
		return nil, errors.New("no client for subscription")
	}
	return client, nil
}

func makeVMResourceSKU(name, family string) *armcompute.ResourceSKU {
	return &armcompute.ResourceSKU{
		Name:         ptr.To(name),
		Family:       ptr.To(family),
		ResourceType: ptr.To(virtualMachinesResourceType),
	}
}

func makeDiskResourceSKU(name string) *armcompute.ResourceSKU {
	return &armcompute.ResourceSKU{
		Name:         ptr.To(name),
		ResourceType: ptr.To("disks"),
	}
}

func skuListPager(skus []*armcompute.ResourceSKU, fetchErr error) *runtime.Pager[armcompute.ResourceSKUsClientListResponse] {
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

func TestResourceSKUsCachedReader_ListVirtualMachineSKUs(t *testing.T) {
	ctx := context.Background()
	vmSKU := makeVMResourceSKU(testVMSize, testVMFamily)
	diskSKU := makeDiskResourceSKU("Premium_LRS")
	refreshedSKU := makeVMResourceSKU("Standard_D8as_v4", "standardDASv4Family")

	tests := []struct {
		name  string
		setup func(ctrl *gomock.Controller) (*fakeResourceSKUsClientBuilder, utilsclock.PassiveClock)
		calls []struct {
			advanceClockBy  time.Duration
			subscriptionID  string
			wantSKUNames    []string
			wantError       bool
			wantErrContains string
		}
		wantBuilderCalls int32
	}{
		{
			name: "caches successful list and filters to virtualMachines",
			setup: func(ctrl *gomock.Controller) (*fakeResourceSKUsClientBuilder, utilsclock.PassiveClock) {
				mockClient := azureclient.NewMockResourceSKUsClient(ctrl)
				mockClient.EXPECT().NewListPager(testListOptions()).Return(skuListPager([]*armcompute.ResourceSKU{vmSKU, diskSKU}, nil)).Times(1)
				return &fakeResourceSKUsClientBuilder{
					clients: map[string]azureclient.ResourceSKUsClient{testSubscriptionID: mockClient},
				}, utilsclock.RealClock{}
			},
			calls: []struct {
				advanceClockBy  time.Duration
				subscriptionID  string
				wantSKUNames    []string
				wantError       bool
				wantErrContains string
			}{
				{
					subscriptionID: testSubscriptionID,
					wantSKUNames:   []string{testVMSize},
				},
				{
					subscriptionID: testSubscriptionID,
					wantSKUNames:   []string{testVMSize},
				},
			},
			wantBuilderCalls: 1,
		},
		{
			name: "normalizes subscription ID case for cache key",
			setup: func(ctrl *gomock.Controller) (*fakeResourceSKUsClientBuilder, utilsclock.PassiveClock) {
				mockClient := azureclient.NewMockResourceSKUsClient(ctrl)
				mockClient.EXPECT().NewListPager(testListOptions()).Return(skuListPager([]*armcompute.ResourceSKU{vmSKU}, nil)).Times(1)
				return &fakeResourceSKUsClientBuilder{
					clients: map[string]azureclient.ResourceSKUsClient{
						"ABCDEF12-3456-7890-ABCD-EF1234567890": mockClient,
					},
				}, utilsclock.RealClock{}
			},
			calls: []struct {
				advanceClockBy  time.Duration
				subscriptionID  string
				wantSKUNames    []string
				wantError       bool
				wantErrContains string
			}{
				{
					subscriptionID: "ABCDEF12-3456-7890-ABCD-EF1234567890",
					wantSKUNames:   []string{testVMSize},
				},
				{
					subscriptionID: "abcdef12-3456-7890-abcd-ef1234567890",
					wantSKUNames:   []string{testVMSize},
				},
			},
			wantBuilderCalls: 1,
		},
		{
			name: "caches error within error freshness TTL",
			setup: func(ctrl *gomock.Controller) (*fakeResourceSKUsClientBuilder, utilsclock.PassiveClock) {
				mockClient := azureclient.NewMockResourceSKUsClient(ctrl)
				mockClient.EXPECT().NewListPager(testListOptions()).Return(skuListPager(nil, errors.New("service unavailable"))).Times(1)
				return &fakeResourceSKUsClientBuilder{
					clients: map[string]azureclient.ResourceSKUsClient{testSubscriptionID: mockClient},
				}, clocktesting.NewFakePassiveClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
			},
			calls: []struct {
				advanceClockBy  time.Duration
				subscriptionID  string
				wantSKUNames    []string
				wantError       bool
				wantErrContains string
			}{
				{
					subscriptionID:  testSubscriptionID,
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
			wantBuilderCalls: 1,
		},
		{
			name: "recovers after error freshness TTL and caches success for success freshness TTL",
			setup: func(ctrl *gomock.Controller) (*fakeResourceSKUsClientBuilder, utilsclock.PassiveClock) {
				mockClient := azureclient.NewMockResourceSKUsClient(ctrl)
				gomock.InOrder(
					mockClient.EXPECT().NewListPager(testListOptions()).Return(skuListPager(nil, errors.New("temporary"))),
					mockClient.EXPECT().NewListPager(testListOptions()).Return(skuListPager([]*armcompute.ResourceSKU{vmSKU}, nil)),
				)
				return &fakeResourceSKUsClientBuilder{
					clients: map[string]azureclient.ResourceSKUsClient{testSubscriptionID: mockClient},
				}, clocktesting.NewFakePassiveClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
			},
			calls: []struct {
				advanceClockBy  time.Duration
				subscriptionID  string
				wantSKUNames    []string
				wantError       bool
				wantErrContains string
			}{
				{
					subscriptionID:  testSubscriptionID,
					wantError:       true,
					wantErrContains: "temporary",
				},
				{
					advanceClockBy: resourceSKUsCacheErrorFreshnessTTL + time.Second,
					subscriptionID: testSubscriptionID,
					wantSKUNames:   []string{testVMSize},
				},
				{
					advanceClockBy: 10 * time.Minute,
					subscriptionID: testSubscriptionID,
					wantSKUNames:   []string{testVMSize},
				},
			},
			wantBuilderCalls: 2,
		},
		{
			name: "refreshes after success freshness TTL expiry",
			setup: func(ctrl *gomock.Controller) (*fakeResourceSKUsClientBuilder, utilsclock.PassiveClock) {
				mockClient := azureclient.NewMockResourceSKUsClient(ctrl)
				gomock.InOrder(
					mockClient.EXPECT().NewListPager(testListOptions()).Return(skuListPager([]*armcompute.ResourceSKU{vmSKU}, nil)),
					mockClient.EXPECT().NewListPager(testListOptions()).Return(skuListPager([]*armcompute.ResourceSKU{refreshedSKU}, nil)),
				)
				return &fakeResourceSKUsClientBuilder{
					clients: map[string]azureclient.ResourceSKUsClient{testSubscriptionID: mockClient},
				}, clocktesting.NewFakePassiveClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
			},
			calls: []struct {
				advanceClockBy  time.Duration
				subscriptionID  string
				wantSKUNames    []string
				wantError       bool
				wantErrContains string
			}{
				{
					subscriptionID: testSubscriptionID,
					wantSKUNames:   []string{testVMSize},
				},
				{
					advanceClockBy: 10 * time.Minute,
					subscriptionID: testSubscriptionID,
					wantSKUNames:   []string{testVMSize},
				},
				{
					advanceClockBy: resourceSKUsCacheSuccessFreshnessTTL - 10*time.Minute + time.Second,
					subscriptionID: testSubscriptionID,
					wantSKUNames:   []string{"Standard_D8as_v4"},
				},
			},
			wantBuilderCalls: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			builder, clock := tt.setup(ctrl)
			reader := newResourceSKUsCachedReader(builder, defaultResourceSKUsCacheMaxEntries, clock, testLocation)

			for _, call := range tt.calls {
				if call.advanceClockBy > 0 {
					fakeClock, ok := clock.(*clocktesting.FakePassiveClock)
					require.True(t, ok, "advanceClockBy requires a FakePassiveClock")
					fakeClock.SetTime(fakeClock.Now().Add(call.advanceClockBy))
				}

				got, err := reader.ListVirtualMachineSKUs(ctx, testTenantID, call.subscriptionID)
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
			assert.Equal(t, tt.wantBuilderCalls, builder.calls.Load())
		})
	}
}

func TestResourceSKUsCachedReader_LRUEviction(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	sub1 := "11111111-1111-1111-1111-111111111111"
	sub2 := "22222222-2222-2222-2222-222222222222"
	sub3 := "33333333-3333-3333-3333-333333333333"

	client1 := azureclient.NewMockResourceSKUsClient(ctrl)
	client2 := azureclient.NewMockResourceSKUsClient(ctrl)
	client3 := azureclient.NewMockResourceSKUsClient(ctrl)

	// sub1 is listed twice: once initially, once after eviction by sub3.
	client1.EXPECT().NewListPager(testListOptions()).Return(skuListPager([]*armcompute.ResourceSKU{
		makeVMResourceSKU("Standard_D2s_v3", "standardDSv3Family"),
	}, nil)).Times(2)
	client2.EXPECT().NewListPager(testListOptions()).Return(skuListPager([]*armcompute.ResourceSKU{
		makeVMResourceSKU("Standard_D4s_v3", "standardDSv3Family"),
	}, nil)).Times(1)
	client3.EXPECT().NewListPager(testListOptions()).Return(skuListPager([]*armcompute.ResourceSKU{
		makeVMResourceSKU("Standard_D8s_v3", "standardDSv3Family"),
	}, nil)).Times(1)

	builder := &fakeResourceSKUsClientBuilder{
		clients: map[string]azureclient.ResourceSKUsClient{
			sub1: client1,
			sub2: client2,
			sub3: client3,
		},
	}
	reader := newResourceSKUsCachedReader(builder, 2, utilsclock.RealClock{}, testLocation)

	_, err := reader.ListVirtualMachineSKUs(ctx, testTenantID, sub1)
	require.NoError(t, err)
	_, err = reader.ListVirtualMachineSKUs(ctx, testTenantID, sub2)
	require.NoError(t, err)
	// Evicts sub1 (least recently used).
	_, err = reader.ListVirtualMachineSKUs(ctx, testTenantID, sub3)
	require.NoError(t, err)
	// Misses cache and refreshes sub1.
	_, err = reader.ListVirtualMachineSKUs(ctx, testTenantID, sub1)
	require.NoError(t, err)

	assert.Equal(t, int32(4), builder.calls.Load())
}

func TestResourceSKUsCachedReader_Singleflight(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	var listStarted sync.WaitGroup
	listStarted.Add(1)
	var releaseList sync.WaitGroup
	releaseList.Add(1)

	mockClient := azureclient.NewMockResourceSKUsClient(ctrl)
	mockClient.EXPECT().NewListPager(testListOptions()).DoAndReturn(func(options *armcompute.ResourceSKUsClientListOptions) *runtime.Pager[armcompute.ResourceSKUsClientListResponse] {
		listStarted.Done()
		releaseList.Wait()
		return skuListPager([]*armcompute.ResourceSKU{makeVMResourceSKU(testVMSize, testVMFamily)}, nil)
	}).Times(1)

	builder := &fakeResourceSKUsClientBuilder{
		clients: map[string]azureclient.ResourceSKUsClient{testSubscriptionID: mockClient},
	}
	reader := NewResourceSKUsCachedReader(builder, testLocation)

	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, err := reader.ListVirtualMachineSKUs(ctx, testTenantID, testSubscriptionID)
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
	assert.Equal(t, int32(1), builder.calls.Load())
}

func TestResourceSKUsCachedReader_ReturnedSliceIsDeepCopy(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	vmSKU := makeVMResourceSKU(testVMSize, testVMFamily)
	mockClient := azureclient.NewMockResourceSKUsClient(ctrl)
	mockClient.EXPECT().NewListPager(testListOptions()).Return(skuListPager([]*armcompute.ResourceSKU{vmSKU}, nil)).Times(1)
	builder := &fakeResourceSKUsClientBuilder{
		clients: map[string]azureclient.ResourceSKUsClient{testSubscriptionID: mockClient},
	}
	reader := NewResourceSKUsCachedReader(builder, testLocation)

	first, err := reader.ListVirtualMachineSKUs(ctx, testTenantID, testSubscriptionID)
	require.NoError(t, err)
	require.Len(t, first, 1)

	// Mutate nested fields on the returned SKU; the cache must stay unchanged.
	*first[0].Name = "mutated"
	*first[0].Family = "mutatedFamily"
	first[0] = makeVMResourceSKU("replaced", "replacedFamily")

	second, err := reader.ListVirtualMachineSKUs(ctx, testTenantID, testSubscriptionID)
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.Equal(t, testVMSize, *second[0].Name)
	assert.Equal(t, testVMFamily, *second[0].Family)
}
