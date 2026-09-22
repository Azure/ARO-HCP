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
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/cachedreader"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

const (
	testTenantID       = "11111111-1111-1111-1111-111111111111"
	testSubscriptionID = "22222222-2222-2222-2222-222222222222"
	testResourceGroup  = "test-rg"
	testClusterName    = "test-cluster"
	testNodePoolName   = "test-nodepool"
	testVMSize         = "Standard_D8ds_v5"
)

func newTestSubscription() *coreapi.Subscription {
	subResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID))
	return &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   subResourceID,
			PartitionKey: strings.ToLower(subResourceID.SubscriptionID),
		},
		State: coreapi.SubscriptionStateRegistered,
		Properties: &coreapi.SubscriptionProperties{
			TenantId: ptr.To(testTenantID),
		},
	}
}

func newTestNodePool(t *testing.T, diskType metadataapi.OsDiskType, vmSize string) *coreapi.HCPOpenShiftClusterNodePool {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroup +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/" + testNodePoolName))
	return &coreapi.HCPOpenShiftClusterNodePool{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: testNodePoolName,
				Type: coreapi.NodePoolResourceType.String(),
			},
			Location: "eastus",
		},
		Properties: coreapi.HCPOpenShiftClusterNodePoolProperties{
			Platform: coreapi.NodePoolPlatformProfile{
				VMSize: vmSize,
				OSDisk: coreapi.OSDiskProfile{
					DiskType: diskType,
				},
			},
		},
	}
}

func makeTestVMResourceSKU(name string, capabilities ...*armcompute.ResourceSKUCapabilities) *armcompute.ResourceSKU {
	return &armcompute.ResourceSKU{
		Name:         ptr.To(name),
		Capabilities: capabilities,
	}
}

func TestAzureVMSizeSupportsEphemeralOSDiskValidation_Validate(t *testing.T) {
	ctx := context.Background()
	cluster := &coreapi.HCPOpenShiftCluster{}
	subscription := newTestSubscription()

	tests := []struct {
		name                       string
		subscription               *coreapi.Subscription
		nodePool                   *coreapi.HCPOpenShiftClusterNodePool
		setupMockVMSKUCachedReader func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader)
		wantOutcome                OutcomeType
		wantInternalMessage        string
	}{
		{

			name:        "managed OS disk succeeds",
			nodePool:    newTestNodePool(t, metadataapi.OsDiskTypeManaged, testVMSize),
			wantOutcome: OutcomeTypeSkipped,
		},
		{
			name:        "ephemeral OS disk succeeds when capability is True",
			nodePool:    newTestNodePool(t, metadataapi.OsDiskTypeEphemeral, testVMSize),
			wantOutcome: OutcomeTypePassed,
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(makeTestVMResourceSKU(testVMSize, &armcompute.ResourceSKUCapabilities{
						Name:  ptr.To(computeResourceSKUCapabilityNameEphemeralOSDiskSupported),
						Value: ptr.To("True"),
					}), nil)
			},
		},
		{
			name:        "ephemeral OS disk succeeds when capability is true (case-insensitive)",
			nodePool:    newTestNodePool(t, metadataapi.OsDiskTypeEphemeral, testVMSize),
			wantOutcome: OutcomeTypePassed,
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(makeTestVMResourceSKU(testVMSize, &armcompute.ResourceSKUCapabilities{
						Name:  ptr.To("ephemeralosdisksupported"),
						Value: ptr.To("true"),
					}), nil)
			},
		},
		{
			name:     "ephemeral OS disk fails when capability is False",
			nodePool: newTestNodePool(t, metadataapi.OsDiskTypeEphemeral, testVMSize),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(makeTestVMResourceSKU(testVMSize, &armcompute.ResourceSKUCapabilities{
						Name:  ptr.To(computeResourceSKUCapabilityNameEphemeralOSDiskSupported),
						Value: ptr.To("False"),
					}), nil)
			},
			wantOutcome:         OutcomeTypeFailed,
			wantInternalMessage: `vm size "Standard_D8ds_v5" does not support ephemeral OS disks`,
		},
		{
			name:     "ephemeral OS disk fails when capability is missing",
			nodePool: newTestNodePool(t, metadataapi.OsDiskTypeEphemeral, testVMSize),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(makeTestVMResourceSKU(testVMSize), nil)
			},
			wantOutcome:         OutcomeTypeUnknown,
			wantInternalMessage: `resource SKU for VM size "Standard_D8ds_v5" is missing EphemeralOSDiskSupported capability`,
		},
		{
			name:     "ephemeral OS disk fails when SKU lookup fails",
			nodePool: newTestNodePool(t, metadataapi.OsDiskTypeEphemeral, testVMSize),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(nil, errors.New("VM size not found"))
			},
			wantOutcome:         OutcomeTypeUnknown,
			wantInternalMessage: `failed to get resource SKU for VM size "Standard_D8ds_v5": VM size not found`,
		},
		{
			name: "ephemeral OS disk fails when subscription is missing tenant ID",
			subscription: &coreapi.Subscription{
				Properties: &coreapi.SubscriptionProperties{},
			},
			nodePool:            newTestNodePool(t, metadataapi.OsDiskTypeEphemeral, testVMSize),
			wantOutcome:         OutcomeTypeUnknown,
			wantInternalMessage: "subscription is missing tenant ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			skuReader := cachedreader.NewMockVirtualMachineResourceSKUsCachedReader(ctrl)
			if tt.setupMockVMSKUCachedReader != nil {
				tt.setupMockVMSKUCachedReader(skuReader)
			}

			sub := tt.subscription
			if sub == nil {
				sub = subscription
			}
			validation := NewAzureVMSizeSupportsEphemeralOSDiskValidation(skuReader)
			result := validation.Validate(ctx, cluster, sub, tt.nodePool)
			require.NoError(t, result.Validate())
			assert.Equal(t, tt.wantOutcome, result.Outcome.Type)
			if tt.wantInternalMessage != "" {
				assert.Contains(t, result.InternalMessage(), tt.wantInternalMessage)
			}
		})
	}
}
