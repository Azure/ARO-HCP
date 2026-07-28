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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	utilsclock "k8s.io/utils/clock"
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
		ResourceType: ptr.To(skuResourceTypeVirtualMachines),
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

func skuNames(skus []*armcompute.ResourceSKU) []string {
	var names []string
	for _, sku := range skus {
		names = append(names, *sku.Name)
	}
	return names
}

func TestResourceSKUsCachedReader_CacheHitMissAndConcurrency(t *testing.T) {
	vmSKU := makeVMResourceSKU(testVMSize, testVMFamily)
	diskSKU := makeDiskResourceSKU("Premium_LRS")

	tests := []struct {
		name string
		// concurrentCallers is how many goroutines call ListVirtualMachineSKUs
		// simultaneously. 1 means a simple serial call.
		concurrentCallers int
		// prepopulate controls whether SyncOnce is called before the
		// reader calls to seed the cache (cache hit path).
		prepopulate bool
		// blockClient when true makes the mock Azure client block until
		// all concurrent callers have started, proving they coalesce on
		// one in-flight request.
		blockClient bool
		// wantSKUNames is the VM SKU names every caller should receive.
		wantSKUNames []string
		// wantBuilderCalls is the total number of times the client
		// builder's ResourceSKUsClient method should have been invoked.
		wantBuilderCalls int32
	}{
		{
			name:              "cache hit returns data without calling live client again",
			prepopulate:       true,
			concurrentCallers: 1,
			wantSKUNames:      []string{testVMSize},
			// 1 call from the SyncOnce pre-population, 0 from the read
			wantBuilderCalls: 1,
		},
		{
			name:              "cache miss enqueues fetch and returns data after worker processes",
			prepopulate:       false,
			concurrentCallers: 1,
			wantSKUNames:      []string{testVMSize},
			wantBuilderCalls:  1,
		},
		{
			name:              "concurrent cache misses coalesce into single live client call",
			prepopulate:       false,
			concurrentCallers: 8,
			blockClient:       true,
			wantSKUNames:      []string{testVMSize},
			wantBuilderCalls:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			// listStarted / releaseList synchronize the blocking-client
			// variant so we can prove all callers are waiting before the
			// Azure response is delivered.
			var listStarted, releaseList sync.WaitGroup
			if tt.blockClient {
				listStarted.Add(1)
				releaseList.Add(1)
			}

			mockClient := azureclient.NewMockResourceSKUsClient(ctrl)

			pagerFactory := func(*armcompute.ResourceSKUsClientListOptions) *runtime.Pager[armcompute.ResourceSKUsClientListResponse] {
				if tt.blockClient {
					listStarted.Done()
					releaseList.Wait()
				}
				return skuListPager([]*armcompute.ResourceSKU{vmSKU, diskSKU}, nil)
			}
			// Expect exactly 1 list call regardless of caller count.
			mockClient.EXPECT().NewListPager(testListOptions()).DoAndReturn(pagerFactory).Times(1)

			builder := &fakeResourceSKUsClientBuilder{
				clients: map[string]azureclient.ResourceSKUsClient{testSubscriptionID: mockClient},
			}
			reader := newResourceSKUsCachedReader(builder, defaultResourceSKUsCacheMaxEntries, utilsclock.RealClock{}, testLocation)

			if tt.prepopulate {
				// Seed the cache directly via SyncOnce — this is the
				// "cache already warm" path.
				err := reader.SyncOnce(context.Background(), SKUKey{
					TenantID:       testTenantID,
					SubscriptionID: testSubscriptionID,
				})
				require.NoError(t, err)
			}

			// For cache-miss tests the controller must be running so
			// workers can drain the queue.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if !tt.prepopulate {
				go reader.Run(ctx, 1)
			}

			// Launch concurrent callers.
			type result struct {
				skus []*armcompute.ResourceSKU
				err  error
			}
			results := make(chan result, tt.concurrentCallers)
			var callersReady sync.WaitGroup
			callersReady.Add(tt.concurrentCallers)

			for i := 0; i < tt.concurrentCallers; i++ {
				go func() {
					callersReady.Done()
					skus, err := reader.ListVirtualMachineSKUs(ctx, testTenantID, testSubscriptionID)
					results <- result{skus: skus, err: err}
				}()
			}

			// For the blocking variant, wait until:
			//  1. all callers have entered ListVirtualMachineSKUs
			//  2. the mock client's NewListPager has been entered
			// then release the mock so the single fetch completes.
			if tt.blockClient {
				callersReady.Wait()
				listStarted.Wait()
				releaseList.Done()
			}

			for i := 0; i < tt.concurrentCallers; i++ {
				r := <-results
				require.NoError(t, r.err)
				assert.Equal(t, tt.wantSKUNames, skuNames(r.skus))
			}

			assert.Equal(t, tt.wantBuilderCalls, builder.calls.Load())
		})
	}
}

func TestResourceSKUsCachedReader_CacheMissWithError(t *testing.T) {
	ctrl := gomock.NewController(t)

	mockClient := azureclient.NewMockResourceSKUsClient(ctrl)
	mockClient.EXPECT().NewListPager(testListOptions()).Return(
		skuListPager(nil, errors.New("service unavailable")),
	).Times(1)

	builder := &fakeResourceSKUsClientBuilder{
		clients: map[string]azureclient.ResourceSKUsClient{testSubscriptionID: mockClient},
	}
	reader := newResourceSKUsCachedReader(builder, defaultResourceSKUsCacheMaxEntries, utilsclock.RealClock{}, testLocation)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go reader.Run(ctx, 1)

	_, err := reader.ListVirtualMachineSKUs(ctx, testTenantID, testSubscriptionID)
	require.Error(t, err)
	assert.ErrorContains(t, err, "service unavailable")
}

func TestResourceSKUsCachedReader_GetVirtualMachineSKU(t *testing.T) {
	vmSKU := makeVMResourceSKU(testVMSize, testVMFamily)
	diskSKU := makeDiskResourceSKU("Premium_LRS")

	tests := []struct {
		name            string
		vmSize          string
		wantName        string
		wantFamily      string
		wantError       bool
		wantErrContains string
	}{
		{
			name:       "returns the matching SKU",
			vmSize:     testVMSize,
			wantName:   testVMSize,
			wantFamily: testVMFamily,
		},
		{
			name:            "returns an error when the VM size is not found",
			vmSize:          "Standard_missing_v9",
			wantError:       true,
			wantErrContains: "Standard_missing_v9",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := azureclient.NewMockResourceSKUsClient(ctrl)
			mockClient.EXPECT().NewListPager(testListOptions()).Return(
				skuListPager([]*armcompute.ResourceSKU{vmSKU, diskSKU}, nil),
			).Times(1)

			builder := &fakeResourceSKUsClientBuilder{
				clients: map[string]azureclient.ResourceSKUsClient{testSubscriptionID: mockClient},
			}
			reader := newResourceSKUsCachedReader(builder, defaultResourceSKUsCacheMaxEntries, utilsclock.RealClock{}, testLocation)

			// Pre-populate cache.
			err := reader.SyncOnce(context.Background(), SKUKey{
				TenantID:       testTenantID,
				SubscriptionID: testSubscriptionID,
			})
			require.NoError(t, err)

			got, err := reader.GetVirtualMachineSKU(context.Background(), testTenantID, testSubscriptionID, tt.vmSize)
			if tt.wantError {
				require.Error(t, err)
				if tt.wantErrContains != "" {
					assert.ErrorContains(t, err, tt.wantErrContains)
				}
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tt.wantName, *got.Name)
			assert.Equal(t, tt.wantFamily, *got.Family)
		})
	}
}

func TestResourceSKUsCachedReader_ReturnedSliceIsDeepCopy(t *testing.T) {
	ctrl := gomock.NewController(t)
	vmSKU := makeVMResourceSKU(testVMSize, testVMFamily)
	mockClient := azureclient.NewMockResourceSKUsClient(ctrl)
	mockClient.EXPECT().NewListPager(testListOptions()).Return(
		skuListPager([]*armcompute.ResourceSKU{vmSKU}, nil),
	).Times(1)

	builder := &fakeResourceSKUsClientBuilder{
		clients: map[string]azureclient.ResourceSKUsClient{testSubscriptionID: mockClient},
	}
	reader := newResourceSKUsCachedReader(builder, defaultResourceSKUsCacheMaxEntries, utilsclock.RealClock{}, testLocation)

	err := reader.SyncOnce(context.Background(), SKUKey{
		TenantID:       testTenantID,
		SubscriptionID: testSubscriptionID,
	})
	require.NoError(t, err)

	first, err := reader.ListVirtualMachineSKUs(context.Background(), testTenantID, testSubscriptionID)
	require.NoError(t, err)
	require.Len(t, first, 1)

	// Mutate the returned slice; the cache must stay unchanged.
	*first[0].Name = "mutated"
	*first[0].Family = "mutatedFamily"
	first[0] = makeVMResourceSKU("replaced", "replacedFamily")

	second, err := reader.ListVirtualMachineSKUs(context.Background(), testTenantID, testSubscriptionID)
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.Equal(t, testVMSize, *second[0].Name)
	assert.Equal(t, testVMFamily, *second[0].Family)
}
