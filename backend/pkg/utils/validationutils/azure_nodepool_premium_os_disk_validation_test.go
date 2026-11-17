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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/cachedreader"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

func newPremiumOSDiskTestNodePool(t *testing.T, diskStorageAccountType metadataapi.DiskStorageAccountType) *coreapi.NodePool {
	t.Helper()
	nodePool := newTestNodePool(t, metadataapi.OsDiskTypeManaged, testVMSize)
	nodePool.Properties.Platform.OSDisk.DiskStorageAccountType = diskStorageAccountType
	return nodePool
}

func TestAzureNodePoolPremiumOSDiskValidation_Validate(t *testing.T) {
	ctx := context.Background()
	cluster := &coreapi.Cluster{}
	subscription := newTestSubscription()

	tests := []struct {
		name                       string
		subscription               *coreapi.Subscription
		nodePool                   *coreapi.NodePool
		setupMockVMSKUCachedReader func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader)
		wantOutcome                OutcomeType
		wantInternalMessage        string
	}{
		{
			name:        "non-premium OS disk is skipped",
			nodePool:    newPremiumOSDiskTestNodePool(t, metadataapi.DiskStorageAccountTypeStandardSSD_LRS),
			wantOutcome: OutcomeTypeSkipped,
		},
		{
			name:     "premium OS disk succeeds when capability is True",
			nodePool: newPremiumOSDiskTestNodePool(t, metadataapi.DiskStorageAccountTypePremium_LRS),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(makeTestVMResourceSKU(testVMSize, &armcompute.ResourceSKUCapabilities{
						Name:  ptr.To(computeResourceSKUCapabilityNamePremiumIO),
						Value: ptr.To("True"),
					}), nil)
			},
			wantOutcome: OutcomeTypePassed,
		},
		{
			name:     "premium OS disk fails when capability is False",
			nodePool: newPremiumOSDiskTestNodePool(t, metadataapi.DiskStorageAccountTypePremium_LRS),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(makeTestVMResourceSKU(testVMSize, &armcompute.ResourceSKUCapabilities{
						Name:  ptr.To(computeResourceSKUCapabilityNamePremiumIO),
						Value: ptr.To("False"),
					}), nil)
			},
			wantOutcome:         OutcomeTypeFailed,
			wantInternalMessage: `vm size "Standard_D8ds_v5" does not support Premium_LRS OS disks`,
		},
		{
			name:     "premium OS disk fails when capability is missing",
			nodePool: newPremiumOSDiskTestNodePool(t, metadataapi.DiskStorageAccountTypePremium_LRS),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(makeTestVMResourceSKU(testVMSize), nil)
			},
			wantOutcome:         OutcomeTypeUnknown,
			wantInternalMessage: `resource SKU for VM size "Standard_D8ds_v5" is missing PremiumIO capability`,
		},
		{
			name:     "premium OS disk fails when SKU lookup fails",
			nodePool: newPremiumOSDiskTestNodePool(t, metadataapi.DiskStorageAccountTypePremium_LRS),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(nil, errors.New("VM size not found"))
			},
			wantOutcome:         OutcomeTypeUnknown,
			wantInternalMessage: `failed to get resource SKU for VM size "Standard_D8ds_v5": VM size not found`,
		},
		{
			name: "premium OS disk fails when subscription is missing tenant ID",
			subscription: &coreapi.Subscription{
				Properties: &coreapi.SubscriptionProperties{},
			},
			nodePool:            newPremiumOSDiskTestNodePool(t, metadataapi.DiskStorageAccountTypePremium_LRS),
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
			validation := NewAzureNodePoolPremiumOSDiskValidation(skuReader)
			result := validation.Validate(ctx, cluster, sub, tt.nodePool)
			require.NoError(t, result.Validate())
			assert.Equal(t, tt.wantOutcome, result.Outcome.Type)
			if tt.wantInternalMessage != "" {
				assert.Contains(t, result.InternalMessage(), tt.wantInternalMessage)
			}
		})
	}
}
