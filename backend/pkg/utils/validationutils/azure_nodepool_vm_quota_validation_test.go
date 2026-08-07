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

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/cachedreader"
	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testVMFamily          = "standardDASv4Family"
	testFamilyLocalized   = "Standard DASv4 Family vCPUs"
	testRegionalLocalized = "Total Regional vCPUs"
	testLocation          = "eastus"
)

func makeTestUsage(name, localized string, current int32, limit int64) *armcompute.Usage {
	return &armcompute.Usage{
		Name: &armcompute.UsageName{
			Value:          ptr.To(name),
			LocalizedValue: ptr.To(localized),
		},
		CurrentValue: ptr.To(current),
		Limit:        ptr.To(limit),
	}
}

func makeTestQuotaSKU(vcpus string) *armcompute.ResourceSKU {
	return &armcompute.ResourceSKU{
		Name:   ptr.To(testVMSize),
		Family: ptr.To(testVMFamily),
		Capabilities: []*armcompute.ResourceSKUCapabilities{
			{Name: ptr.To(computeResourceSKUCapabilityNameVCPUs), Value: ptr.To(vcpus)},
		},
	}
}

func newQuotaTestNodePool(t *testing.T, replicas int32, autoScaling *api.NodePoolAutoScaling) *api.HCPOpenShiftClusterNodePool {
	t.Helper()
	np := newTestNodePool(t, api.OsDiskTypeManaged, testVMSize)
	np.Location = testLocation
	np.Properties.Replicas = replicas
	np.Properties.AutoScaling = autoScaling
	return np
}

// newTestUsageListPager returns a one-page Usage list pager for tests.
// If fetchErr is non-nil, the first page fetch fails with that error.
func newTestUsageListPager(usages []*armcompute.Usage, fetchErr error) *runtime.Pager[armcompute.UsageClientListResponse] {
	pages := []armcompute.UsageClientListResponse{{
		ListUsagesResult: armcompute.ListUsagesResult{Value: usages},
	}}
	idx := -1
	return runtime.NewPager(runtime.PagingHandler[armcompute.UsageClientListResponse]{
		More: func(page armcompute.UsageClientListResponse) bool {
			return idx+1 < len(pages)
		},
		Fetcher: func(ctx context.Context, page *armcompute.UsageClientListResponse) (armcompute.UsageClientListResponse, error) {
			if fetchErr != nil {
				return armcompute.UsageClientListResponse{}, fetchErr
			}
			idx++
			return pages[idx], nil
		},
	})
}

func TestAzureNodePoolVMQuotaValidation_Validate(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	cluster := &api.HCPOpenShiftCluster{}
	subscription := newTestSubscription()

	tests := []struct {
		name                       string
		subscription               *arm.Subscription
		nodePool                   *api.HCPOpenShiftClusterNodePool
		setupMockVMSKUCachedReader func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader)
		setupMockFPAUsageClient    func(ctrl *gomock.Controller, fpaBuilder *azureclient.MockFirstPartyApplicationClientBuilder)
		wantErrs                   []string
	}{
		{
			name:     "zero replicas succeeds without quota checks",
			nodePool: newQuotaTestNodePool(t, 0, nil),
		},
		{
			name: "fails when subscription is missing tenant ID",
			subscription: &arm.Subscription{
				Properties: &arm.SubscriptionProperties{},
			},
			nodePool: newQuotaTestNodePool(t, 2, nil),
			wantErrs: []string{"subscription is missing tenant ID"},
		},
		{
			name:     "fixed replicas succeeds when family and regional quota are sufficient",
			nodePool: newQuotaTestNodePool(t, 3, nil),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, testLocation, testVMSize).
					Return(makeTestQuotaSKU("4"), nil)
			},
			setupMockFPAUsageClient: func(ctrl *gomock.Controller, fpaBuilder *azureclient.MockFirstPartyApplicationClientBuilder) {
				usageClient := azureclient.NewMockUsageClient(ctrl)
				usageClient.EXPECT().
					NewListPager(testLocation, nil).
					Return(newTestUsageListPager([]*armcompute.Usage{
						makeTestUsage(testVMFamily, testFamilyLocalized, 10, 100),
						makeTestUsage(computeUsageNameTotalRegionalVCPUs, testRegionalLocalized, 50, 200),
					}, nil))
				fpaBuilder.EXPECT().
					UsageClient(testTenantID, testSubscriptionID).
					Return(usageClient, nil)
			},
		},
		{
			name: "autoscaling uses max for required instances",
			nodePool: newQuotaTestNodePool(t, 0, &api.NodePoolAutoScaling{
				Min: 1,
				Max: 5,
			}),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, testLocation, testVMSize).
					Return(makeTestQuotaSKU("4"), nil)
			},
			setupMockFPAUsageClient: func(ctrl *gomock.Controller, fpaBuilder *azureclient.MockFirstPartyApplicationClientBuilder) {
				usageClient := azureclient.NewMockUsageClient(ctrl)
				usageClient.EXPECT().
					NewListPager(testLocation, nil).
					Return(newTestUsageListPager([]*armcompute.Usage{
						// 5 * 4 = 20 required; remaining 15 -> fail family
						makeTestUsage(testVMFamily, testFamilyLocalized, 85, 100),
						makeTestUsage(computeUsageNameTotalRegionalVCPUs, testRegionalLocalized, 50, 200),
					}, nil))
				fpaBuilder.EXPECT().
					UsageClient(testTenantID, testSubscriptionID).
					Return(usageClient, nil)
			},
			wantErrs: []string{
				`insufficient quota for VM size "Standard_D8ds_v5" family "standardDASv4Family": need 20 vCPUs (autoscaling max 5 × 4 vCPUs per instance), have 15 remaining for "Standard DASv4 Family vCPUs" (current 85, limit 100)`,
			},
		},
		{
			name:     "fails when family quota is insufficient",
			nodePool: newQuotaTestNodePool(t, 4, nil),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, testLocation, testVMSize).
					Return(makeTestQuotaSKU("4"), nil)
			},
			setupMockFPAUsageClient: func(ctrl *gomock.Controller, fpaBuilder *azureclient.MockFirstPartyApplicationClientBuilder) {
				usageClient := azureclient.NewMockUsageClient(ctrl)
				usageClient.EXPECT().
					NewListPager(testLocation, nil).
					Return(newTestUsageListPager([]*armcompute.Usage{
						makeTestUsage(testVMFamily, testFamilyLocalized, 90, 100),
						makeTestUsage(computeUsageNameTotalRegionalVCPUs, testRegionalLocalized, 10, 200),
					}, nil))
				fpaBuilder.EXPECT().
					UsageClient(testTenantID, testSubscriptionID).
					Return(usageClient, nil)
			},
			wantErrs: []string{
				`insufficient quota for VM size "Standard_D8ds_v5" family "standardDASv4Family": need 16 vCPUs (4 replicas × 4 vCPUs per instance), have 10 remaining for "Standard DASv4 Family vCPUs" (current 90, limit 100)`,
			},
		},
		{
			name:     "fails when total regional quota is insufficient",
			nodePool: newQuotaTestNodePool(t, 4, nil),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, testLocation, testVMSize).
					Return(makeTestQuotaSKU("4"), nil)
			},
			setupMockFPAUsageClient: func(ctrl *gomock.Controller, fpaBuilder *azureclient.MockFirstPartyApplicationClientBuilder) {
				usageClient := azureclient.NewMockUsageClient(ctrl)
				usageClient.EXPECT().
					NewListPager(testLocation, nil).
					Return(newTestUsageListPager([]*armcompute.Usage{
						makeTestUsage(testVMFamily, testFamilyLocalized, 10, 100),
						makeTestUsage(computeUsageNameTotalRegionalVCPUs, testRegionalLocalized, 195, 200),
					}, nil))
				fpaBuilder.EXPECT().
					UsageClient(testTenantID, testSubscriptionID).
					Return(usageClient, nil)
			},
			wantErrs: []string{
				`insufficient total regional vCPU quota for VM size "Standard_D8ds_v5": need 16 vCPUs (4 replicas × 4 vCPUs per instance), have 5 remaining for "Total Regional vCPUs" (current 195, limit 200)`,
			},
		},
		{
			name:     "fails when both family and regional quotas are insufficient",
			nodePool: newQuotaTestNodePool(t, 4, nil),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, testLocation, testVMSize).
					Return(makeTestQuotaSKU("4"), nil)
			},
			setupMockFPAUsageClient: func(ctrl *gomock.Controller, fpaBuilder *azureclient.MockFirstPartyApplicationClientBuilder) {
				usageClient := azureclient.NewMockUsageClient(ctrl)
				usageClient.EXPECT().
					NewListPager(testLocation, nil).
					Return(newTestUsageListPager([]*armcompute.Usage{
						makeTestUsage(testVMFamily, testFamilyLocalized, 90, 100),
						makeTestUsage(computeUsageNameTotalRegionalVCPUs, testRegionalLocalized, 195, 200),
					}, nil))
				fpaBuilder.EXPECT().
					UsageClient(testTenantID, testSubscriptionID).
					Return(usageClient, nil)
			},
			wantErrs: []string{
				`insufficient quota for VM size "Standard_D8ds_v5" family "standardDASv4Family": need 16 vCPUs (4 replicas × 4 vCPUs per instance), have 10 remaining for "Standard DASv4 Family vCPUs" (current 90, limit 100)`,
				`insufficient total regional vCPU quota for VM size "Standard_D8ds_v5": need 16 vCPUs (4 replicas × 4 vCPUs per instance), have 5 remaining for "Total Regional vCPUs" (current 195, limit 200)`,
			},
		},
		{
			name:     "fails when SKU lookup fails",
			nodePool: newQuotaTestNodePool(t, 2, nil),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, testLocation, testVMSize).
					Return(nil, errors.New("VM size not found"))
			},
			wantErrs: []string{
				`failed to get resource SKU for VM size "Standard_D8ds_v5": VM size not found`,
			},
		},
		{
			name:     "fails when SKU is missing vCPUs capability",
			nodePool: newQuotaTestNodePool(t, 2, nil),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, testLocation, testVMSize).
					Return(&armcompute.ResourceSKU{
						Name:   ptr.To(testVMSize),
						Family: ptr.To(testVMFamily),
					}, nil)
			},
			wantErrs: []string{
				`resource SKU for VM size "Standard_D8ds_v5" is missing vCPUs capability`,
			},
		},
		{
			name:     "fails when SKU vCPUs capability is zero",
			nodePool: newQuotaTestNodePool(t, 2, nil),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, testLocation, testVMSize).
					Return(makeTestQuotaSKU("0"), nil)
			},
			wantErrs: []string{
				`resource SKU for VM size "Standard_D8ds_v5" has unexpected vCPUs capability value 0`,
			},
		},
		{
			name:     "fails when family usage is missing",
			nodePool: newQuotaTestNodePool(t, 2, nil),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, testLocation, testVMSize).
					Return(makeTestQuotaSKU("4"), nil)
			},
			setupMockFPAUsageClient: func(ctrl *gomock.Controller, fpaBuilder *azureclient.MockFirstPartyApplicationClientBuilder) {
				usageClient := azureclient.NewMockUsageClient(ctrl)
				usageClient.EXPECT().
					NewListPager(testLocation, nil).
					Return(newTestUsageListPager([]*armcompute.Usage{
						makeTestUsage(computeUsageNameTotalRegionalVCPUs, testRegionalLocalized, 10, 200),
					}, nil))
				fpaBuilder.EXPECT().
					UsageClient(testTenantID, testSubscriptionID).
					Return(usageClient, nil)
			},
			wantErrs: []string{
				`compute usage for VM family "standardDASv4Family" was not found in location "eastus"`,
			},
		},
		{
			name:     "fails when usage list fails",
			nodePool: newQuotaTestNodePool(t, 2, nil),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, testLocation, testVMSize).
					Return(makeTestQuotaSKU("4"), nil)
			},
			setupMockFPAUsageClient: func(ctrl *gomock.Controller, fpaBuilder *azureclient.MockFirstPartyApplicationClientBuilder) {
				usageClient := azureclient.NewMockUsageClient(ctrl)
				usageClient.EXPECT().
					NewListPager(testLocation, nil).
					Return(newTestUsageListPager(nil, errors.New("service unavailable")))
				fpaBuilder.EXPECT().
					UsageClient(testTenantID, testSubscriptionID).
					Return(usageClient, nil)
			},
			wantErrs: []string{
				`failed to list compute usages for location "eastus": service unavailable`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			skuReader := cachedreader.NewMockVirtualMachineResourceSKUsCachedReader(ctrl)
			fpaBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
			if tt.setupMockVMSKUCachedReader != nil {
				tt.setupMockVMSKUCachedReader(skuReader)
			}
			if tt.setupMockFPAUsageClient != nil {
				tt.setupMockFPAUsageClient(ctrl, fpaBuilder)
			}

			sub := tt.subscription
			if sub == nil {
				sub = subscription
			}
			validation := NewAzureNodePoolVMQuotaValidation(skuReader, fpaBuilder)
			err := validation.Validate(ctx, cluster, sub, tt.nodePool)

			if len(tt.wantErrs) == 0 {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				for _, wantErr := range tt.wantErrs {
					assert.ErrorContains(t, err, wantErr)
				}
			}
		})
	}
}

func TestAzureNodePoolVMQuotaValidation_requiredInstanceCount(t *testing.T) {
	ctrl := gomock.NewController(t)
	validation := NewAzureNodePoolVMQuotaValidation(
		cachedreader.NewMockVirtualMachineResourceSKUsCachedReader(ctrl),
		azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl),
	)

	tests := []struct {
		name        string
		replicas    int32
		autoScaling *api.NodePoolAutoScaling
		want        int32
	}{
		{
			name:     "fixed replicas",
			replicas: 3,
			want:     3,
		},
		{
			name:     "autoscaling uses max",
			replicas: 1,
			autoScaling: &api.NodePoolAutoScaling{
				Min: 1,
				Max: 7,
			},
			want: 7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, validation.requiredInstanceCount(newQuotaTestNodePool(t, tt.replicas, tt.autoScaling)))
		})
	}
}

func TestIsValidComputeUsage(t *testing.T) {
	tests := []struct {
		name  string
		usage *armcompute.Usage
		want  bool
	}{
		{
			name:  "nil usage",
			usage: nil,
			want:  false,
		},
		{
			name:  "nil name",
			usage: &armcompute.Usage{CurrentValue: ptr.To(int32(1)), Limit: ptr.To(int64(2))},
			want:  false,
		},
		{
			name: "nil name value",
			usage: &armcompute.Usage{
				Name:         &armcompute.UsageName{},
				CurrentValue: ptr.To(int32(1)),
				Limit:        ptr.To(int64(2)),
			},
			want: false,
		},
		{
			name: "empty name value",
			usage: &armcompute.Usage{
				Name:         &armcompute.UsageName{Value: ptr.To("")},
				CurrentValue: ptr.To(int32(1)),
				Limit:        ptr.To(int64(2)),
			},
			want: false,
		},
		{
			name: "nil current value",
			usage: &armcompute.Usage{
				Name:  &armcompute.UsageName{Value: ptr.To(testVMFamily)},
				Limit: ptr.To(int64(2)),
			},
			want: false,
		},
		{
			name: "nil limit",
			usage: &armcompute.Usage{
				Name:         &armcompute.UsageName{Value: ptr.To(testVMFamily)},
				CurrentValue: ptr.To(int32(1)),
			},
			want: false,
		},
		{
			name:  "valid usage",
			usage: makeTestUsage(testVMFamily, testFamilyLocalized, 1, 2),
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isValidComputeUsage(tt.usage))
		})
	}
}

func TestAzureNodePoolVMQuotaValidation_lookupFamilyAndRegionalVCPUUsages(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), logr.Discard())
	validation := &AzureNodePoolVMQuotaValidation{}

	tests := []struct {
		name                 string
		location             string
		family               string
		setupMockUsageClient func(usageClient *azureclient.MockUsageClient, location string)
		wantFamilyName       string
		wantRegionalName     string
		wantErr              string
	}{
		{
			name:     "returns family and regional usages",
			location: testLocation,
			family:   testVMFamily,
			setupMockUsageClient: func(usageClient *azureclient.MockUsageClient, location string) {
				usageClient.EXPECT().
					NewListPager(location, nil).
					Return(newTestUsageListPager([]*armcompute.Usage{
						makeTestUsage("otherFamily", "Other", 0, 10),
						makeTestUsage(testVMFamily, testFamilyLocalized, 10, 100),
						makeTestUsage(computeUsageNameTotalRegionalVCPUs, testRegionalLocalized, 50, 200),
					}, nil))
			},
			wantFamilyName:   testVMFamily,
			wantRegionalName: computeUsageNameTotalRegionalVCPUs,
		},
		{
			name:     "family match is case-insensitive",
			location: testLocation,
			family:   testVMFamily,
			setupMockUsageClient: func(usageClient *azureclient.MockUsageClient, location string) {
				usageClient.EXPECT().
					NewListPager(location, nil).
					Return(newTestUsageListPager([]*armcompute.Usage{
						makeTestUsage("STANDARDDASV4FAMILY", testFamilyLocalized, 10, 100),
						makeTestUsage("CORES", testRegionalLocalized, 50, 200),
					}, nil))
			},
			wantFamilyName:   "STANDARDDASV4FAMILY",
			wantRegionalName: "CORES",
		},
		{
			name:     "skips invalid entries and still finds valid matches",
			location: testLocation,
			family:   testVMFamily,
			setupMockUsageClient: func(usageClient *azureclient.MockUsageClient, location string) {
				usageClient.EXPECT().
					NewListPager(location, nil).
					Return(newTestUsageListPager([]*armcompute.Usage{
						nil,
						{Name: &armcompute.UsageName{Value: ptr.To(testVMFamily)}}, // missing current/limit
						makeTestUsage(testVMFamily, testFamilyLocalized, 10, 100),
						makeTestUsage(computeUsageNameTotalRegionalVCPUs, testRegionalLocalized, 50, 200),
					}, nil))
			},
			wantFamilyName:   testVMFamily,
			wantRegionalName: computeUsageNameTotalRegionalVCPUs,
		},
		{
			name:     "fails when family usage is missing",
			location: testLocation,
			family:   testVMFamily,
			setupMockUsageClient: func(usageClient *azureclient.MockUsageClient, location string) {
				usageClient.EXPECT().
					NewListPager(location, nil).
					Return(newTestUsageListPager([]*armcompute.Usage{
						makeTestUsage(computeUsageNameTotalRegionalVCPUs, testRegionalLocalized, 50, 200),
					}, nil))
			},
			wantErr: `compute usage for VM family "standardDASv4Family" was not found in location "eastus"`,
		},
		{
			name:     "fails when regional usage is missing",
			location: testLocation,
			family:   testVMFamily,
			setupMockUsageClient: func(usageClient *azureclient.MockUsageClient, location string) {
				usageClient.EXPECT().
					NewListPager(location, nil).
					Return(newTestUsageListPager([]*armcompute.Usage{
						makeTestUsage(testVMFamily, testFamilyLocalized, 10, 100),
					}, nil))
			},
			wantErr: `compute usage "cores" (total regional vCPUs) was not found in location "eastus"`,
		},
		{
			name:     "fails when usage list fails",
			location: testLocation,
			family:   testVMFamily,
			setupMockUsageClient: func(usageClient *azureclient.MockUsageClient, location string) {
				usageClient.EXPECT().
					NewListPager(location, nil).
					Return(newTestUsageListPager(nil, errors.New("service unavailable")))
			},
			wantErr: `failed to list compute usages for location "eastus": service unavailable`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			usageClient := azureclient.NewMockUsageClient(ctrl)
			tt.setupMockUsageClient(usageClient, tt.location)

			familyUsage, regionalUsage, err := validation.lookupFamilyAndRegionalVCPUUsages(ctx, usageClient, tt.location, tt.family)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, familyUsage)
			require.NotNil(t, regionalUsage)
			assert.Equal(t, tt.wantFamilyName, *familyUsage.Name.Value)
			assert.Equal(t, tt.wantRegionalName, *regionalUsage.Name.Value)
		})
	}
}
