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

package validationutils

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
)

func TestResourceSKUCapability(t *testing.T) {
	tests := []struct {
		name           string
		sku            *armcompute.ResourceSKU
		capabilityName string
		want           *string
	}{
		{
			name:           "nil sku",
			sku:            nil,
			capabilityName: computeResourceSKUCapabilityNameVCPUs,
			want:           nil,
		},
		{
			name: "returns matching capability value",
			sku: makeTestVMResourceSKU(testVMSize, &armcompute.ResourceSKUCapabilities{
				Name:  ptr.To(computeResourceSKUCapabilityNameVCPUs),
				Value: ptr.To("8"),
			}),
			capabilityName: computeResourceSKUCapabilityNameVCPUs,
			want:           ptr.To("8"),
		},
		{
			name: "match is case-insensitive",
			sku: makeTestVMResourceSKU(testVMSize, &armcompute.ResourceSKUCapabilities{
				Name:  ptr.To("vcpus"),
				Value: ptr.To("4"),
			}),
			capabilityName: computeResourceSKUCapabilityNameVCPUs,
			want:           ptr.To("4"),
		},
		{
			name:           "missing capability",
			sku:            makeTestVMResourceSKU(testVMSize),
			capabilityName: computeResourceSKUCapabilityNameVCPUs,
			want:           nil,
		},
		{
			name: "skips capability entries with nil name or value",
			sku: makeTestVMResourceSKU(testVMSize,
				&armcompute.ResourceSKUCapabilities{Name: nil, Value: ptr.To("8")},
				&armcompute.ResourceSKUCapabilities{Name: ptr.To(computeResourceSKUCapabilityNameVCPUs), Value: nil},
				&armcompute.ResourceSKUCapabilities{Name: ptr.To(computeResourceSKUCapabilityNameVCPUs), Value: ptr.To("16")},
			),
			capabilityName: computeResourceSKUCapabilityNameVCPUs,
			want:           ptr.To("16"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resourceSKUCapability(tt.sku, tt.capabilityName)
			if tt.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, *tt.want, *got)
		})
	}
}

func TestIsCapabilityEphemeralOSDiskSupported(t *testing.T) {
	tests := []struct {
		name          string
		sku           *armcompute.ResourceSKU
		wantSupported bool
		wantFound     bool
	}{
		{
			name:          "nil sku",
			sku:           nil,
			wantSupported: false,
			wantFound:     false,
		},
		{
			name: "true value",
			sku: makeTestVMResourceSKU(testVMSize, &armcompute.ResourceSKUCapabilities{
				Name:  ptr.To(computeResourceSKUCapabilityNameEphemeralOSDiskSupported),
				Value: ptr.To("True"),
			}),
			wantSupported: true,
			wantFound:     true,
		},
		{
			name: "true value case-insensitive",
			sku: makeTestVMResourceSKU(testVMSize, &armcompute.ResourceSKUCapabilities{
				Name:  ptr.To("ephemeralosdisksupported"),
				Value: ptr.To("true"),
			}),
			wantSupported: true,
			wantFound:     true,
		},
		{
			name: "false value",
			sku: makeTestVMResourceSKU(testVMSize, &armcompute.ResourceSKUCapabilities{
				Name:  ptr.To(computeResourceSKUCapabilityNameEphemeralOSDiskSupported),
				Value: ptr.To("False"),
			}),
			wantSupported: false,
			wantFound:     true,
		},
		{
			name:          "missing capability",
			sku:           makeTestVMResourceSKU(testVMSize),
			wantSupported: false,
			wantFound:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			supported, found := isCapabilityEphemeralOSDiskSupported(tt.sku)
			assert.Equal(t, tt.wantSupported, supported)
			assert.Equal(t, tt.wantFound, found)
		})
	}
}

func TestLookupCapabilityVCPUs(t *testing.T) {
	tests := []struct {
		name      string
		sku       *armcompute.ResourceSKU
		want      int
		wantFound bool
	}{
		{
			name:      "nil sku",
			sku:       nil,
			want:      0,
			wantFound: false,
		},
		{
			name: "parses integer value",
			sku: makeTestVMResourceSKU(testVMSize, &armcompute.ResourceSKUCapabilities{
				Name:  ptr.To(computeResourceSKUCapabilityNameVCPUs),
				Value: ptr.To("8"),
			}),
			want:      8,
			wantFound: true,
		},
		{
			name: "trims whitespace",
			sku: makeTestVMResourceSKU(testVMSize, &armcompute.ResourceSKUCapabilities{
				Name:  ptr.To(computeResourceSKUCapabilityNameVCPUs),
				Value: ptr.To(" 16 "),
			}),
			want:      16,
			wantFound: true,
		},
		{
			name: "zero is a valid parsed value",
			sku: makeTestVMResourceSKU(testVMSize, &armcompute.ResourceSKUCapabilities{
				Name:  ptr.To(computeResourceSKUCapabilityNameVCPUs),
				Value: ptr.To("0"),
			}),
			want:      0,
			wantFound: true,
		},
		{
			name:      "missing capability",
			sku:       makeTestVMResourceSKU(testVMSize),
			want:      0,
			wantFound: false,
		},
		{
			name: "non-integer value",
			sku: makeTestVMResourceSKU(testVMSize, &armcompute.ResourceSKUCapabilities{
				Name:  ptr.To(computeResourceSKUCapabilityNameVCPUs),
				Value: ptr.To("eight"),
			}),
			want:      0,
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := lookupCapabilityVCPUs(tt.sku)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantFound, found)
		})
	}
}
