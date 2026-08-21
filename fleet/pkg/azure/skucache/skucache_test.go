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

package skucache

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	armcomputefake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6/fake"

	"github.com/Azure/ARO-HCP/internal/kuberesources"
)

const (
	testRegion         = "eastus"
	testSubscriptionID = "22222222-2222-2222-2222-222222222222"
)

func makeVMResourceSKU(name string, capabilities map[string]string) *armcompute.ResourceSKU {
	caps := make([]*armcompute.ResourceSKUCapabilities, 0, len(capabilities))
	for k, v := range capabilities {
		caps = append(caps, &armcompute.ResourceSKUCapabilities{Name: ptr.To(k), Value: ptr.To(v)})
	}
	return &armcompute.ResourceSKU{
		Name:         ptr.To(name),
		ResourceType: ptr.To(resourceTypeVirtualMachines),
		Capabilities: caps,
	}
}

// newTestCache builds a SKUCache whose Resource SKUs calls are served by
// the official Azure SDK fake transport, so tests exercise the real
// *armcompute.ResourceSKUsClient (paging, response parsing) rather than a
// hand-rolled pager.
func newTestCache(t *testing.T, skus []*armcompute.ResourceSKU, listErr error) *SKUCache {
	t.Helper()
	srv := armcomputefake.ResourceSKUsServer{
		NewListPager: func(options *armcompute.ResourceSKUsClientListOptions) (resp azfake.PagerResponder[armcompute.ResourceSKUsClientListResponse]) {
			if listErr != nil {
				resp.AddError(listErr)
				return
			}
			resp.AddPage(http.StatusOK, armcompute.ResourceSKUsClientListResponse{
				ResourceSKUsResult: armcompute.ResourceSKUsResult{Value: skus},
			}, nil)
			return
		},
	}
	transport := armcomputefake.NewResourceSKUsServerTransport(&srv)

	return NewSKUCache(testRegion, &azfake.TokenCredential{}, &policy.ClientOptions{Transport: transport}, nil)
}

func TestSKUCacheFetch(t *testing.T) {
	tests := []struct {
		name        string
		skus        []*armcompute.ResourceSKU
		listErr     error
		wantErr     bool
		wantEntries map[string]corev1.ResourceList
		wantMissing []string
	}{
		{
			name: "extracts cpu, memory, and secondary NICs",
			skus: []*armcompute.ResourceSKU{
				makeVMResourceSKU("Standard_D8ds_v5", map[string]string{
					"MemoryGB":             "32",
					"vCPUs":                "8",
					"MaxNetworkInterfaces": "4",
				}),
			},
			wantEntries: map[string]corev1.ResourceList{
				"Standard_D8ds_v5": {
					corev1.ResourceCPU:                 resource.MustParse("8"),
					corev1.ResourceMemory:              resource.MustParse("32Gi"),
					kuberesources.SwiftNICResourceName: resource.MustParse("3"),
				},
			},
		},
		{
			name: "skips non virtual machine resource types",
			skus: []*armcompute.ResourceSKU{
				{Name: ptr.To("Premium_LRS"), ResourceType: ptr.To("disks")},
			},
			wantMissing: []string{"Premium_LRS"},
		},
		{
			name: "skips SKUs missing capabilities",
			skus: []*armcompute.ResourceSKU{
				{Name: ptr.To("Standard_NoInfo"), ResourceType: ptr.To(resourceTypeVirtualMachines)},
			},
			wantMissing: []string{"Standard_NoInfo"},
		},
		{
			name: "MaxNetworkInterfaces of 1 produces no NIC entry",
			skus: []*armcompute.ResourceSKU{
				makeVMResourceSKU("Standard_B1s", map[string]string{
					"MemoryGB":             "1",
					"vCPUs":                "1",
					"MaxNetworkInterfaces": "1",
				}),
			},
			wantEntries: map[string]corev1.ResourceList{
				"Standard_B1s": {
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("1Gi"),
				},
			},
		},
		{
			name:    "wraps a pager error",
			skus:    nil,
			listErr: errors.New("boom"),
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cache := newTestCache(t, test.skus, test.listErr)

			skuResources, err := cache.fetchSKUResources(context.Background(), testSubscriptionID)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			for vmSize, wantRL := range test.wantEntries {
				actualRL, ok := skuResources[vmSize]
				require.True(t, ok, "expected %q in result", vmSize)
				for name, wantQuantity := range wantRL {
					actualQuantity, exists := actualRL[name]
					require.True(t, exists, "expected resource %q for %q", name, vmSize)
					assert.Zero(t, wantQuantity.Cmp(actualQuantity), "%s[%s]: expected %s, got %s", vmSize, name, wantQuantity.String(), actualQuantity.String())
				}
				assert.Equal(t, len(wantRL), len(actualRL), "unexpected extra resources for %q: %v", vmSize, actualRL)
			}
			for _, vmSize := range test.wantMissing {
				_, ok := skuResources[vmSize]
				assert.False(t, ok, "expected %q to be absent", vmSize)
			}
		})
	}
}

func TestSKUCacheCachesPerSubscription(t *testing.T) {
	calls := 0
	srv := armcomputefake.ResourceSKUsServer{
		NewListPager: func(options *armcompute.ResourceSKUsClientListOptions) (resp azfake.PagerResponder[armcompute.ResourceSKUsClientListResponse]) {
			calls++
			resp.AddPage(http.StatusOK, armcompute.ResourceSKUsClientListResponse{
				ResourceSKUsResult: armcompute.ResourceSKUsResult{Value: []*armcompute.ResourceSKU{
					makeVMResourceSKU("Standard_D8ds_v5", map[string]string{
						"MemoryGB": "32",
						"vCPUs":    "8",
					}),
				}},
			}, nil)
			return
		},
	}
	transport := armcomputefake.NewResourceSKUsServerTransport(&srv)
	cache := NewSKUCache(testRegion, &azfake.TokenCredential{}, &policy.ClientOptions{Transport: transport}, nil)

	first, err := cache.SKUResourcesByVMSize(context.Background(), testSubscriptionID)
	require.NoError(t, err)
	second, err := cache.SKUResourcesByVMSize(context.Background(), testSubscriptionID)
	require.NoError(t, err)

	assert.Equal(t, first, second)
	assert.Equal(t, 1, calls, "the SKU list should only be fetched once for a cached subscription")
}

func TestExtractSKUResources(t *testing.T) {
	tests := []struct {
		name    string
		sku     *armcompute.ResourceSKU
		wantRL  corev1.ResourceList
		wantLen int
	}{
		{
			name: "all capabilities present",
			sku: makeVMResourceSKU("Standard_D16ds_v5", map[string]string{
				"MemoryGB":             "64",
				"vCPUs":                "16",
				"MaxNetworkInterfaces": "8",
			}),
			wantRL: corev1.ResourceList{
				corev1.ResourceCPU:                 resource.MustParse("16"),
				corev1.ResourceMemory:              resource.MustParse("64Gi"),
				kuberesources.SwiftNICResourceName: resource.MustParse("7"),
			},
			wantLen: 3,
		},
		{
			name: "memory only",
			sku: makeVMResourceSKU("Standard_Partial", map[string]string{
				"MemoryGB": "16",
			}),
			wantRL: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			},
			wantLen: 1,
		},
		{
			name: "decimal memory parses correctly",
			sku: makeVMResourceSKU("Standard_Half", map[string]string{
				"MemoryGB": "3.5",
				"vCPUs":    "2",
			}),
			wantRL: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("3.5Gi"),
			},
			wantLen: 2,
		},
		{
			name: "skips nil capability entries",
			sku: &armcompute.ResourceSKU{
				Name:         ptr.To("Standard_WithNils"),
				ResourceType: ptr.To(resourceTypeVirtualMachines),
				Capabilities: []*armcompute.ResourceSKUCapabilities{
					nil,
					{Name: ptr.To("MemoryGB"), Value: ptr.To("8")},
				},
			},
			wantRL: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("8Gi"),
			},
			wantLen: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := extractSKUResources(test.sku)
			assert.Equal(t, test.wantLen, len(result), "unexpected resource count")
			for name, wantQuantity := range test.wantRL {
				actualQuantity, exists := result[name]
				require.True(t, exists, "expected resource %q", name)
				assert.Zero(t, wantQuantity.Cmp(actualQuantity), "%s: expected %s, got %s", name, wantQuantity.String(), actualQuantity.String())
			}
		})
	}
}
