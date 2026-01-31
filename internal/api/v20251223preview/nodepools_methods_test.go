// Copyright 2025 Microsoft Corporation
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

package v20251223preview

import (
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/v20251223preview/generated"
)

func TestSizeGiBRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		original *api.NodePool
	}{
		{
			name: "SizeGiB with explicit value should round-trip",
			original: &api.NodePool{
				TrackedResource: arm.TrackedResource{
					Resource: arm.Resource{
						ID:   api.Must(azcorearm.ParseResourceID(strings.ToLower("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/myRg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/myCluster/nodePools/myNodePool"))),
						Name: "myNodePool",
						Type: "Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools",
					},
					Location: "eastus",
				},
				Properties: api.NodePoolProperties{
					Version: api.NodePoolVersionProfile{
						ID:           "4.15.1",
						ChannelGroup: "stable",
					},
					Platform: api.NodePoolPlatformProfile{
						SubnetID: api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/myRg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet")),
						VMSize:   "Standard_D2s_v3",
						OSDisk: api.OSDiskProfile{
							SizeGiB:                ptr.To(int32(128)),
							DiskStorageAccountType: api.DiskStorageAccountTypePremium_LRS,
						},
					},
					Replicas:   3,
					AutoRepair: true,
					Labels:     map[string]string{},
					Taints:     []api.Taint{},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roundTripInternalNodePool(t, tt.original)
		})
	}
}

func TestSetDefaultValuesNodePool(t *testing.T) {
	tests := []struct {
		name     string
		input    *NodePool
		expected *NodePool
	}{
		{
			name: "nil SizeGiB should default to 64",
			input: &NodePool{
				NodePool: generated.NodePool{
					Properties: &generated.NodePoolProperties{
						Platform: &generated.NodePoolPlatformProfile{
							OSDisk: &generated.OsDiskProfile{
								SizeGiB: nil,
							},
						},
					},
				},
			},
			expected: &NodePool{
				NodePool: generated.NodePool{
					Properties: &generated.NodePoolProperties{
						Version: &generated.NodePoolVersionProfile{
							ChannelGroup: ptr.To("stable"),
						},
						Platform: &generated.NodePoolPlatformProfile{
							OSDisk: &generated.OsDiskProfile{
								SizeGiB:                ptr.To(int32(64)),
								DiskStorageAccountType: ptr.To(generated.DiskStorageAccountTypePremiumLRS),
							},
						},
						AutoRepair: ptr.To(true),
					},
				},
			},
		},
		{
			name: "explicit SizeGiB should be preserved",
			input: &NodePool{
				NodePool: generated.NodePool{
					Properties: &generated.NodePoolProperties{
						Platform: &generated.NodePoolPlatformProfile{
							OSDisk: &generated.OsDiskProfile{
								SizeGiB: ptr.To(int32(128)),
							},
						},
					},
				},
			},
			expected: &NodePool{
				NodePool: generated.NodePool{
					Properties: &generated.NodePoolProperties{
						Version: &generated.NodePoolVersionProfile{
							ChannelGroup: ptr.To("stable"),
						},
						Platform: &generated.NodePoolPlatformProfile{
							OSDisk: &generated.OsDiskProfile{
								SizeGiB:                ptr.To(int32(128)),
								DiskStorageAccountType: ptr.To(generated.DiskStorageAccountTypePremiumLRS),
							},
						},
						AutoRepair: ptr.To(true),
					},
				},
			},
		},
		{
			name: "nil OSDisk should be initialized with defaults",
			input: &NodePool{
				NodePool: generated.NodePool{
					Properties: &generated.NodePoolProperties{
						Platform: &generated.NodePoolPlatformProfile{
							OSDisk: nil,
						},
					},
				},
			},
			expected: &NodePool{
				NodePool: generated.NodePool{
					Properties: &generated.NodePoolProperties{
						Version: &generated.NodePoolVersionProfile{
							ChannelGroup: ptr.To("stable"),
						},
						Platform: &generated.NodePoolPlatformProfile{
							OSDisk: &generated.OsDiskProfile{
								SizeGiB:                ptr.To(int32(64)),
								DiskStorageAccountType: ptr.To(generated.DiskStorageAccountTypePremiumLRS),
							},
						},
						AutoRepair: ptr.To(true),
					},
				},
			},
		},
		{
			name:  "completely nil properties should be initialized",
			input: &NodePool{},
			expected: &NodePool{
				NodePool: generated.NodePool{
					Properties: &generated.NodePoolProperties{
						Version: &generated.NodePoolVersionProfile{
							ChannelGroup: ptr.To("stable"),
						},
						Platform: &generated.NodePoolPlatformProfile{
							OSDisk: &generated.OsDiskProfile{
								SizeGiB:                ptr.To(int32(64)),
								DiskStorageAccountType: ptr.To(generated.DiskStorageAccountTypePremiumLRS),
							},
						},
						AutoRepair: ptr.To(true),
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetDefaultValuesNodePool(tt.input)
			if !reflect.DeepEqual(tt.input, tt.expected) {
				t.Errorf("SetDefaultValuesNodePool() mismatch:\n%s", cmp.Diff(tt.expected, tt.input))
			}
		})
	}
}

func TestNormalizeOSDiskProfile(t *testing.T) {
	tests := []struct {
		name     string
		input    *generated.OsDiskProfile
		existing *api.OSDiskProfile
		expected *api.OSDiskProfile
	}{
		{
			name: "nil SizeGiB should not overwrite existing value",
			input: &generated.OsDiskProfile{
				SizeGiB:                nil,
				DiskStorageAccountType: ptr.To(generated.DiskStorageAccountTypeStandardSSDLRS),
			},
			existing: &api.OSDiskProfile{
				SizeGiB:                ptr.To(int32(128)),
				DiskStorageAccountType: api.DiskStorageAccountTypePremium_LRS,
			},
			expected: &api.OSDiskProfile{
				SizeGiB:                ptr.To(int32(128)),
				DiskStorageAccountType: api.DiskStorageAccountTypeStandardSSD_LRS,
			},
		},
		{
			name: "explicit SizeGiB should overwrite existing value",
			input: &generated.OsDiskProfile{
				SizeGiB:                ptr.To(int32(128)),
				DiskStorageAccountType: ptr.To(generated.DiskStorageAccountTypeStandardSSDLRS),
			},
			existing: &api.OSDiskProfile{
				SizeGiB:                ptr.To(int32(64)),
				DiskStorageAccountType: api.DiskStorageAccountTypePremium_LRS,
			},
			expected: &api.OSDiskProfile{
				SizeGiB:                ptr.To(int32(128)),
				DiskStorageAccountType: api.DiskStorageAccountTypeStandardSSD_LRS,
			},
		},
		{
			name: "explicit SizeGiB of 0 should be preserved for validation to reject",
			input: &generated.OsDiskProfile{
				SizeGiB:                ptr.To(int32(0)),
				DiskStorageAccountType: ptr.To(generated.DiskStorageAccountTypePremiumLRS),
			},
			existing: &api.OSDiskProfile{
				SizeGiB:                ptr.To(int32(64)),
				DiskStorageAccountType: api.DiskStorageAccountTypePremium_LRS,
			},
			expected: &api.OSDiskProfile{
				SizeGiB:                ptr.To(int32(0)),
				DiskStorageAccountType: api.DiskStorageAccountTypePremium_LRS,
			},
		},
		{
			name: "all nil input should preserve existing values",
			input: &generated.OsDiskProfile{
				SizeGiB:                nil,
				DiskStorageAccountType: nil,
				EncryptionSetID:        nil,
			},
			existing: &api.OSDiskProfile{
				SizeGiB:                ptr.To(int32(100)),
				DiskStorageAccountType: api.DiskStorageAccountTypePremium_LRS,
				EncryptionSetID:        api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Compute/diskEncryptionSets/test-encryption")),
			},
			expected: &api.OSDiskProfile{
				SizeGiB:                ptr.To(int32(100)),
				DiskStorageAccountType: api.DiskStorageAccountTypePremium_LRS,
				EncryptionSetID:        api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Compute/diskEncryptionSets/test-encryption")),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := *tt.existing
			require.Len(t, normalizeOSDiskProfile(field.NewPath("t"), tt.input, &result), 0)
			if !reflect.DeepEqual(&result, tt.expected) {
				t.Errorf("normalizeOSDiskProfile() mismatch:\n%s", cmp.Diff(tt.expected, &result))
			}
		})
	}
}

func TestNewOSDiskProfile(t *testing.T) {
	tests := []struct {
		name     string
		input    *api.OSDiskProfile
		expected generated.OsDiskProfile
	}{
		{
			name: "nil SizeGiB should remain nil in output",
			input: &api.OSDiskProfile{
				SizeGiB:                nil,
				DiskStorageAccountType: api.DiskStorageAccountTypePremium_LRS,
				EncryptionSetID:        api.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Compute/diskEncryptionSets/test-encryption")),
			},
			expected: generated.OsDiskProfile{
				SizeGiB:                nil,
				DiskStorageAccountType: ptr.To(generated.DiskStorageAccountTypePremiumLRS),
				EncryptionSetID:        ptr.To("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.Compute/diskEncryptionSets/test-encryption"),
			},
		},
		{
			name: "explicit SizeGiB should be preserved",
			input: &api.OSDiskProfile{
				SizeGiB:                ptr.To(int32(128)),
				DiskStorageAccountType: api.DiskStorageAccountTypeStandardSSD_LRS,
				EncryptionSetID:        nil,
			},
			expected: generated.OsDiskProfile{
				SizeGiB:                ptr.To(int32(128)),
				DiskStorageAccountType: ptr.To(generated.DiskStorageAccountTypeStandardSSDLRS),
				EncryptionSetID:        nil,
			},
		},
		{
			name:  "nil input should return empty struct",
			input: nil,
			expected: generated.OsDiskProfile{
				SizeGiB:                nil,
				DiskStorageAccountType: nil,
				EncryptionSetID:        nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := newOSDiskProfile(tt.input)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("newOSDiskProfile() mismatch:\n%s", cmp.Diff(tt.expected, result))
			}
		})
	}
}

func roundTripInternalNodePool(t *testing.T, original *api.NodePool) {
	v := version{}
	roundTrippedObj, err := v.NewHCPOpenShiftClusterNodePool(original).ConvertToInternal()
	require.NoError(t, err)

	// we compare using DeepEqual here because many of these types have private fields that cannot be introspected
	if !reflect.DeepEqual(original, roundTrippedObj) {
		t.Errorf("Round trip failed:\n%s", cmp.Diff(original, roundTrippedObj))
	}
}
