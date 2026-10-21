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
	"context"
	"fmt"
	"math"
	"net/http"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
)

func testVnetIntegrationCluster() *coreapi.Cluster {
	cluster := coreapitesting.MinimumValidClusterTestCase()
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + coreapitesting.TestSubscriptionID + "/resourceGroups/" + coreapitesting.TestResourceGroupName +
			"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-smi",
	))
	return cluster
}

func testVnetIntegrationSubscription() *coreapi.Subscription {
	return &coreapi.Subscription{
		Properties: &coreapi.SubscriptionProperties{
			TenantId: ptr.To(coreapitesting.TestTenantID),
		},
	}
}

func subnetResponse(addressPrefixes ...string) armnetwork.SubnetsClientGetResponse {
	subnet := armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{},
	}
	if len(addressPrefixes) == 1 {
		subnet.Properties.AddressPrefix = ptr.To(addressPrefixes[0])
	} else if len(addressPrefixes) > 1 {
		for _, p := range addressPrefixes {
			subnet.Properties.AddressPrefixes = append(subnet.Properties.AddressPrefixes, ptr.To(p))
		}
	}
	return armnetwork.SubnetsClientGetResponse{Subnet: subnet}
}

func TestAzureClusterVnetIntegrationSubnetSizeValidation_Name(t *testing.T) {
	t.Parallel()
	v := NewAzureClusterVnetIntegrationSubnetSizeValidation(nil)
	assert.Equal(t, "AzureClusterVnetIntegrationSubnetSizeValidation", v.Name())
}

func TestAzureClusterVnetIntegrationSubnetSizeValidation(t *testing.T) {
	t.Parallel()

	t.Run("nil VnetIntegrationSubnetID returns Skipped", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testVnetIntegrationCluster()
		cluster.CustomerProperties.Platform.VnetIntegrationSubnetID = nil

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		v := NewAzureClusterVnetIntegrationSubnetSizeValidation(smiBuilder)
		result := v.Validate(context.Background(), testVnetIntegrationSubscription(), cluster)
		require.Equal(t, OutcomeTypeSkipped, result.Outcome.Type)
		assert.Equal(t, "NotApplicable", result.Reason())
	})

	t.Run("SMI client builder error returns Unknown", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testVnetIntegrationCluster()

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiBuilder.EXPECT().SubnetsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, fmt.Errorf("credential error"))

		v := NewAzureClusterVnetIntegrationSubnetSizeValidation(smiBuilder)
		result := v.Validate(context.Background(), testVnetIntegrationSubscription(), cluster)
		require.Equal(t, OutcomeTypeUnknown, result.Outcome.Type)
		assert.Contains(t, result.InternalMessage(), "credential error")
	})

	t.Run("subnet Get error returns Unknown", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testVnetIntegrationCluster()
		subnets := azureclient.NewMockSubnetsClient(ctrl)
		subnets.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(armnetwork.SubnetsClientGetResponse{}, fmt.Errorf("network error"))

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiBuilder.EXPECT().SubnetsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(subnets, nil)

		v := NewAzureClusterVnetIntegrationSubnetSizeValidation(smiBuilder)
		result := v.Validate(context.Background(), testVnetIntegrationSubscription(), cluster)
		require.Equal(t, OutcomeTypeUnknown, result.Outcome.Type)
		assert.Contains(t, result.InternalMessage(), "network error")
	})

	t.Run("subnet NotFound returns Failed", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testVnetIntegrationCluster()
		subnets := azureclient.NewMockSubnetsClient(ctrl)
		subnets.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(armnetwork.SubnetsClientGetResponse{}, &azcore.ResponseError{StatusCode: http.StatusNotFound})

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiBuilder.EXPECT().SubnetsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(subnets, nil)

		v := NewAzureClusterVnetIntegrationSubnetSizeValidation(smiBuilder)
		result := v.Validate(context.Background(), testVnetIntegrationSubscription(), cluster)
		require.Equal(t, OutcomeTypeFailed, result.Outcome.Type)
		assert.Equal(t, "SubnetNotFound", result.Reason())
		assert.Contains(t, result.Outcome.Failed.UserMessage, "was not found")
	})

	t.Run("subnet with nil properties returns Unknown", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testVnetIntegrationCluster()
		subnets := azureclient.NewMockSubnetsClient(ctrl)
		subnets.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(armnetwork.SubnetsClientGetResponse{Subnet: armnetwork.Subnet{Properties: nil}}, nil)

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiBuilder.EXPECT().SubnetsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(subnets, nil)

		v := NewAzureClusterVnetIntegrationSubnetSizeValidation(smiBuilder)
		result := v.Validate(context.Background(), testVnetIntegrationSubscription(), cluster)
		require.Equal(t, OutcomeTypeUnknown, result.Outcome.Type)
		assert.Contains(t, result.InternalMessage(), "no properties")
	})

	t.Run("subnet with no address prefix returns Unknown", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testVnetIntegrationCluster()
		subnets := azureclient.NewMockSubnetsClient(ctrl)
		subnets.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(armnetwork.SubnetsClientGetResponse{
				Subnet: armnetwork.Subnet{Properties: &armnetwork.SubnetPropertiesFormat{}},
			}, nil)

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiBuilder.EXPECT().SubnetsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(subnets, nil)

		v := NewAzureClusterVnetIntegrationSubnetSizeValidation(smiBuilder)
		result := v.Validate(context.Background(), testVnetIntegrationSubscription(), cluster)
		require.Equal(t, OutcomeTypeUnknown, result.Outcome.Type)
		assert.Contains(t, result.InternalMessage(), "no address prefix")
	})

	t.Run("/29 subnet is too small (3 usable IPs) returns Failed", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testVnetIntegrationCluster()
		subnets := azureclient.NewMockSubnetsClient(ctrl)
		subnets.EXPECT().Get(gomock.Any(), coreapitesting.TestResourceGroupName, coreapitesting.TestVirtualNetworkName, coreapitesting.TestVnetIntegrationSubnetName, nil).
			Return(subnetResponse("10.0.1.0/29"), nil)

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiBuilder.EXPECT().SubnetsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(subnets, nil)

		v := NewAzureClusterVnetIntegrationSubnetSizeValidation(smiBuilder)
		result := v.Validate(context.Background(), testVnetIntegrationSubscription(), cluster)
		require.Equal(t, OutcomeTypeFailed, result.Outcome.Type)
		assert.Equal(t, "VnetIntegrationSubnetTooSmall", result.Reason())
		assert.Contains(t, result.Outcome.Failed.UserMessage, "3 usable IPs")
		assert.Contains(t, result.Outcome.Failed.UserMessage, "at least 6")
	})

	t.Run("/28 subnet is large enough (11 usable IPs) returns Passed", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testVnetIntegrationCluster()
		subnets := azureclient.NewMockSubnetsClient(ctrl)
		subnets.EXPECT().Get(gomock.Any(), coreapitesting.TestResourceGroupName, coreapitesting.TestVirtualNetworkName, coreapitesting.TestVnetIntegrationSubnetName, nil).
			Return(subnetResponse("10.0.1.0/28"), nil)

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiBuilder.EXPECT().SubnetsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(subnets, nil)

		v := NewAzureClusterVnetIntegrationSubnetSizeValidation(smiBuilder)
		result := v.Validate(context.Background(), testVnetIntegrationSubscription(), cluster)
		require.Equal(t, OutcomeTypePassed, result.Outcome.Type)
		assert.Contains(t, result.Outcome.Passed.UserMessage, "11 usable IPs")
	})

	t.Run("/24 subnet passes", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testVnetIntegrationCluster()
		subnets := azureclient.NewMockSubnetsClient(ctrl)
		subnets.EXPECT().Get(gomock.Any(), coreapitesting.TestResourceGroupName, coreapitesting.TestVirtualNetworkName, coreapitesting.TestVnetIntegrationSubnetName, nil).
			Return(subnetResponse("10.0.1.0/24"), nil)

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiBuilder.EXPECT().SubnetsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(subnets, nil)

		v := NewAzureClusterVnetIntegrationSubnetSizeValidation(smiBuilder)
		result := v.Validate(context.Background(), testVnetIntegrationSubscription(), cluster)
		require.Equal(t, OutcomeTypePassed, result.Outcome.Type)
		assert.Contains(t, result.Outcome.Passed.UserMessage, "251 usable IPs")
	})

	t.Run("dual-stack subnet sums usable IPs from both prefixes", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testVnetIntegrationCluster()
		subnets := azureclient.NewMockSubnetsClient(ctrl)
		subnets.EXPECT().Get(gomock.Any(), coreapitesting.TestResourceGroupName, coreapitesting.TestVirtualNetworkName, coreapitesting.TestVnetIntegrationSubnetName, nil).
			Return(subnetResponse("10.0.1.0/29", "fd00::/120"), nil)

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiBuilder.EXPECT().SubnetsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(subnets, nil)

		v := NewAzureClusterVnetIntegrationSubnetSizeValidation(smiBuilder)
		result := v.Validate(context.Background(), testVnetIntegrationSubscription(), cluster)
		// /29 -> 8-5=3 usable, /120 -> 256-5=251 usable, total=254
		require.Equal(t, OutcomeTypePassed, result.Outcome.Type)
		assert.Contains(t, result.Outcome.Passed.UserMessage, "254 usable IPs")
	})

	t.Run("/32 subnet yields 0 usable IPs returns Failed", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		cluster := testVnetIntegrationCluster()
		subnets := azureclient.NewMockSubnetsClient(ctrl)
		subnets.EXPECT().Get(gomock.Any(), coreapitesting.TestResourceGroupName, coreapitesting.TestVirtualNetworkName, coreapitesting.TestVnetIntegrationSubnetName, nil).
			Return(subnetResponse("10.0.1.1/32"), nil)

		smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
		smiBuilder.EXPECT().SubnetsClient(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(subnets, nil)

		v := NewAzureClusterVnetIntegrationSubnetSizeValidation(smiBuilder)
		result := v.Validate(context.Background(), testVnetIntegrationSubscription(), cluster)
		require.Equal(t, OutcomeTypeFailed, result.Outcome.Type)
		assert.Equal(t, "VnetIntegrationSubnetTooSmall", result.Reason())
	})
}

func TestTotalUsableIPs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		prefixes []netip.Prefix
		expected uint64
	}{
		{
			name:     "empty prefixes",
			prefixes: nil,
			expected: 0,
		},
		{
			name:     "/24 yields 251",
			prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")},
			expected: 251,
		},
		{
			name:     "/29 yields 3",
			prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/29")},
			expected: 3,
		},
		{
			name:     "/30 yields 0 (4 total, all reserved)",
			prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/30")},
			expected: 0,
		},
		{
			name:     "/32 yields 0",
			prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.1/32")},
			expected: 0,
		},
		{
			name:     "/16 IPv4 maximum Azure subnet yields 65531",
			prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/16")},
			expected: 65531,
		},
		{
			name: "Two Ipv4 prefixes sums both",
			prefixes: []netip.Prefix{
				netip.MustParsePrefix("10.0.0.0/29"),
				netip.MustParsePrefix("10.0.0.8/29"),
			},
			expected: 3 + 3, // /29 -> 8-5=3, /29 -> 8-5=3
		},
		{
			name: "dual-stack sums both",
			prefixes: []netip.Prefix{
				netip.MustParsePrefix("10.0.0.0/28"),
				netip.MustParsePrefix("fd00::/120"),
			},
			expected: 11 + 251, // /28 -> 16-5=11, /120 -> 256-5=251
		},
		{
			name:     "/64 IPv6 yields 2^64-5 (Azure reserves 5)",
			prefixes: []netip.Prefix{netip.MustParsePrefix("fd00::/64")},
			expected: math.MaxUint64 - 4, // 2^64 - 5 = 18446744073709551611
		},
		{
			name: "two large IPv6 prefixes saturate total to MaxUint64",
			prefixes: []netip.Prefix{
				netip.MustParsePrefix("fd00::/64"),
				netip.MustParsePrefix("fd01::/64"),
			},
			expected: math.MaxUint64, // sum overflows, capped
		},
		{
			name:     "/128 IPv6 yields 0 (hostBits=0)",
			prefixes: []netip.Prefix{netip.MustParsePrefix("::1/128")},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, totalUsableIPs(tt.prefixes))
		})
	}
}
