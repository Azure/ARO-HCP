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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armfeatures"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/cachedreader"
	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

func newEncryptionAtHostTestNodePool(t *testing.T, enableEncryptionAtHost bool) *coreapi.NodePool {
	t.Helper()
	nodePool := newTestNodePool(t, metadataapi.OsDiskTypeManaged, testVMSize)
	nodePool.Properties.Platform.EnableEncryptionAtHost = enableEncryptionAtHost
	return nodePool
}

func makeTestFeatureResponse(state string) armfeatures.ClientGetResponse {
	return armfeatures.ClientGetResponse{
		FeatureResult: armfeatures.FeatureResult{
			Properties: &armfeatures.FeatureProperties{
				State: ptr.To(state),
			},
		},
	}
}

func TestAzureNodePoolEncryptionAtHostValidation_Validate(t *testing.T) {
	ctx := context.Background()
	cluster := &coreapi.Cluster{}
	subscription := newTestSubscription()

	tests := []struct {
		name                       string
		subscription               *coreapi.Subscription
		nodePool                   *coreapi.NodePool
		setupMockVMSKUCachedReader func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader)
		setupMockFPAFeaturesClient func(ctrl *gomock.Controller, fpaBuilder *azureclient.MockFirstPartyApplicationClientBuilder)
		wantOutcome                OutcomeType
		wantInternalMessage        string
	}{
		{
			name:        "encryption at host not requested is skipped",
			nodePool:    newEncryptionAtHostTestNodePool(t, false),
			wantOutcome: OutcomeTypeSkipped,
		},
		{
			name:     "succeeds when VM size supports it and feature is registered",
			nodePool: newEncryptionAtHostTestNodePool(t, true),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(makeTestVMResourceSKU(testVMSize, &armcompute.ResourceSKUCapabilities{
						Name:  ptr.To(computeResourceSKUCapabilityNameEncryptionAtHostSupported),
						Value: ptr.To("True"),
					}), nil)
			},
			setupMockFPAFeaturesClient: func(ctrl *gomock.Controller, fpaBuilder *azureclient.MockFirstPartyApplicationClientBuilder) {
				featuresClient := azureclient.NewMockFeaturesClient(ctrl)
				featuresClient.EXPECT().
					Get(gomock.Any(), encryptionAtHostFeatureResourceProviderNamespace, encryptionAtHostFeatureName, nil).
					Return(makeTestFeatureResponse(featureRegistrationStateRegistered), nil)
				fpaBuilder.EXPECT().
					FeaturesClient(testTenantID, testSubscriptionID).
					Return(featuresClient, nil)
			},
			wantOutcome: OutcomeTypePassed,
		},
		{
			name:     "succeeds when feature registration state casing differs from the expected constant",
			nodePool: newEncryptionAtHostTestNodePool(t, true),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(makeTestVMResourceSKU(testVMSize, &armcompute.ResourceSKUCapabilities{
						Name:  ptr.To(computeResourceSKUCapabilityNameEncryptionAtHostSupported),
						Value: ptr.To("True"),
					}), nil)
			},
			setupMockFPAFeaturesClient: func(ctrl *gomock.Controller, fpaBuilder *azureclient.MockFirstPartyApplicationClientBuilder) {
				featuresClient := azureclient.NewMockFeaturesClient(ctrl)
				featuresClient.EXPECT().
					Get(gomock.Any(), encryptionAtHostFeatureResourceProviderNamespace, encryptionAtHostFeatureName, nil).
					Return(makeTestFeatureResponse("registered"), nil)
				fpaBuilder.EXPECT().
					FeaturesClient(testTenantID, testSubscriptionID).
					Return(featuresClient, nil)
			},
			wantOutcome: OutcomeTypePassed,
		},
		{
			name:     "fails when VM size does not support encryption at host",
			nodePool: newEncryptionAtHostTestNodePool(t, true),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(makeTestVMResourceSKU(testVMSize, &armcompute.ResourceSKUCapabilities{
						Name:  ptr.To(computeResourceSKUCapabilityNameEncryptionAtHostSupported),
						Value: ptr.To("False"),
					}), nil)
			},
			setupMockFPAFeaturesClient: func(ctrl *gomock.Controller, fpaBuilder *azureclient.MockFirstPartyApplicationClientBuilder) {
				featuresClient := azureclient.NewMockFeaturesClient(ctrl)
				featuresClient.EXPECT().
					Get(gomock.Any(), encryptionAtHostFeatureResourceProviderNamespace, encryptionAtHostFeatureName, nil).
					Return(makeTestFeatureResponse(featureRegistrationStateRegistered), nil)
				fpaBuilder.EXPECT().
					FeaturesClient(testTenantID, testSubscriptionID).
					Return(featuresClient, nil)
			},
			wantOutcome:         OutcomeTypeFailed,
			wantInternalMessage: `vm size "Standard_D8ds_v5" does not support encryption at host`,
		},
		{
			name:     "fails with combined message when VM size unsupported and feature not registered",
			nodePool: newEncryptionAtHostTestNodePool(t, true),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(makeTestVMResourceSKU(testVMSize, &armcompute.ResourceSKUCapabilities{
						Name:  ptr.To(computeResourceSKUCapabilityNameEncryptionAtHostSupported),
						Value: ptr.To("False"),
					}), nil)
			},
			setupMockFPAFeaturesClient: func(ctrl *gomock.Controller, fpaBuilder *azureclient.MockFirstPartyApplicationClientBuilder) {
				featuresClient := azureclient.NewMockFeaturesClient(ctrl)
				featuresClient.EXPECT().
					Get(gomock.Any(), encryptionAtHostFeatureResourceProviderNamespace, encryptionAtHostFeatureName, nil).
					Return(makeTestFeatureResponse("NotRegistered"), nil)
				fpaBuilder.EXPECT().
					FeaturesClient(testTenantID, testSubscriptionID).
					Return(featuresClient, nil)
			},
			wantOutcome:         OutcomeTypeFailed,
			wantInternalMessage: `vm size "Standard_D8ds_v5" does not support encryption at host; feature "EncryptionAtHost" is not registered for subscription "22222222-2222-2222-2222-222222222222"`,
		},
		{
			name:     "unknown when encryption at host capability missing",
			nodePool: newEncryptionAtHostTestNodePool(t, true),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(makeTestVMResourceSKU(testVMSize), nil)
			},
			wantOutcome:         OutcomeTypeUnknown,
			wantInternalMessage: `resource SKU for VM size "Standard_D8ds_v5" is missing EncryptionAtHostSupported capability`,
		},
		{
			name:     "unknown when SKU lookup fails",
			nodePool: newEncryptionAtHostTestNodePool(t, true),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(nil, errors.New("VM size not found"))
			},
			wantOutcome:         OutcomeTypeUnknown,
			wantInternalMessage: `failed to get resource SKU for VM size "Standard_D8ds_v5": VM size not found`,
		},
		{
			name:     "fails when feature is not registered",
			nodePool: newEncryptionAtHostTestNodePool(t, true),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(makeTestVMResourceSKU(testVMSize, &armcompute.ResourceSKUCapabilities{
						Name:  ptr.To(computeResourceSKUCapabilityNameEncryptionAtHostSupported),
						Value: ptr.To("True"),
					}), nil)
			},
			setupMockFPAFeaturesClient: func(ctrl *gomock.Controller, fpaBuilder *azureclient.MockFirstPartyApplicationClientBuilder) {
				featuresClient := azureclient.NewMockFeaturesClient(ctrl)
				featuresClient.EXPECT().
					Get(gomock.Any(), encryptionAtHostFeatureResourceProviderNamespace, encryptionAtHostFeatureName, nil).
					Return(makeTestFeatureResponse("NotRegistered"), nil)
				fpaBuilder.EXPECT().
					FeaturesClient(testTenantID, testSubscriptionID).
					Return(featuresClient, nil)
			},
			wantOutcome:         OutcomeTypeFailed,
			wantInternalMessage: `feature "EncryptionAtHost" is not registered for subscription "22222222-2222-2222-2222-222222222222"`,
		},
		{
			name:     "unknown when features client lookup fails",
			nodePool: newEncryptionAtHostTestNodePool(t, true),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(makeTestVMResourceSKU(testVMSize, &armcompute.ResourceSKUCapabilities{
						Name:  ptr.To(computeResourceSKUCapabilityNameEncryptionAtHostSupported),
						Value: ptr.To("True"),
					}), nil)
			},
			setupMockFPAFeaturesClient: func(ctrl *gomock.Controller, fpaBuilder *azureclient.MockFirstPartyApplicationClientBuilder) {
				fpaBuilder.EXPECT().
					FeaturesClient(testTenantID, testSubscriptionID).
					Return(nil, errors.New("failed to build client"))
			},
			wantOutcome:         OutcomeTypeUnknown,
			wantInternalMessage: "failed to create features client: failed to build client",
		},
		{
			name: "unknown when subscription is missing tenant ID",
			subscription: &coreapi.Subscription{
				Properties: &coreapi.SubscriptionProperties{},
			},
			nodePool:            newEncryptionAtHostTestNodePool(t, true),
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
			fpaBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
			if tt.setupMockFPAFeaturesClient != nil {
				tt.setupMockFPAFeaturesClient(ctrl, fpaBuilder)
			}

			sub := tt.subscription
			if sub == nil {
				sub = subscription
			}
			validation := NewAzureNodePoolEncryptionAtHostValidation(skuReader, fpaBuilder)
			result := validation.Validate(ctx, cluster, sub, tt.nodePool)
			require.NoError(t, result.Validate())
			assert.Equal(t, tt.wantOutcome, result.Outcome.Type)
			if tt.wantInternalMessage != "" {
				assert.Contains(t, result.InternalMessage(), tt.wantInternalMessage)
			}
		})
	}
}
