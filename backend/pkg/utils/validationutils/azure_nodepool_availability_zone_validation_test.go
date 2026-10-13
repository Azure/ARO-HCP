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

func newAvailabilityZoneTestNodePool(t *testing.T, availabilityZone string) *coreapi.NodePool {
	t.Helper()
	nodePool := newTestNodePool(t, metadataapi.OsDiskTypeManaged, testVMSize)
	nodePool.Properties.Platform.AvailabilityZone = availabilityZone
	return nodePool
}

func makeTestZonalSKU(zones ...string) *armcompute.ResourceSKU {
	sku := makeTestVMResourceSKU(testVMSize)
	zonePtrs := make([]*string, 0, len(zones))
	for _, zone := range zones {
		zonePtrs = append(zonePtrs, ptr.To(zone))
	}
	sku.LocationInfo = []*armcompute.ResourceSKULocationInfo{
		{
			Location: ptr.To("eastus"),
			Zones:    zonePtrs,
		},
	}
	return sku
}

func makeTestZonalSKUWithRestriction(zones []string, restrictedZone string, reasonCode armcompute.ResourceSKURestrictionsReasonCode) *armcompute.ResourceSKU {
	sku := makeTestZonalSKU(zones...)
	sku.Restrictions = []*armcompute.ResourceSKURestrictions{
		{
			Type:       ptr.To(armcompute.ResourceSKURestrictionsTypeZone),
			ReasonCode: ptr.To(reasonCode),
			RestrictionInfo: &armcompute.ResourceSKURestrictionInfo{
				Zones: []*string{ptr.To(restrictedZone)},
			},
		},
	}
	return sku
}

func TestAzureNodePoolAvailabilityZoneValidation_Validate(t *testing.T) {
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
			name:        "no availability zone requested is skipped",
			nodePool:    newAvailabilityZoneTestNodePool(t, ""),
			wantOutcome: OutcomeTypeSkipped,
		},
		{
			name:     "requested zone is supported",
			nodePool: newAvailabilityZoneTestNodePool(t, "2"),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(makeTestZonalSKU("1", "2", "3"), nil)
			},
			wantOutcome: OutcomeTypePassed,
		},
		{
			name:     "requested zone is not supported",
			nodePool: newAvailabilityZoneTestNodePool(t, "4"),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(makeTestZonalSKU("1", "2", "3"), nil)
			},
			wantOutcome:         OutcomeTypeFailed,
			wantInternalMessage: `VM size "Standard_D8ds_v5" does not support availability zone "4" in location "eastus"`,
		},
		{
			name:     "requested zone is restricted for the subscription",
			nodePool: newAvailabilityZoneTestNodePool(t, "2"),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(makeTestZonalSKUWithRestriction([]string{"1", "2", "3"}, "2", armcompute.ResourceSKURestrictionsReasonCodeNotAvailableForSubscription), nil)
			},
			wantOutcome:         OutcomeTypeFailed,
			wantInternalMessage: `VM size "Standard_D8ds_v5" is restricted in availability zone "2" in location "eastus": NotAvailableForSubscription`,
		},
		{
			name:     "restriction on a different zone does not affect the requested zone",
			nodePool: newAvailabilityZoneTestNodePool(t, "1"),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(makeTestZonalSKUWithRestriction([]string{"1", "2", "3"}, "2", armcompute.ResourceSKURestrictionsReasonCodeNotAvailableForSubscription), nil)
			},
			wantOutcome: OutcomeTypePassed,
		},
		{
			name:     "VM size does not support any availability zone",
			nodePool: newAvailabilityZoneTestNodePool(t, "1"),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(makeTestZonalSKU(), nil)
			},
			wantOutcome:         OutcomeTypeFailed,
			wantInternalMessage: `VM size "Standard_D8ds_v5" is available in location "eastus" but does not support deployment to a specific availability zone`,
		},
		{
			name:     "unknown when resource SKU has no location info",
			nodePool: newAvailabilityZoneTestNodePool(t, "1"),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(makeTestVMResourceSKU(testVMSize), nil)
			},
			wantOutcome:         OutcomeTypeUnknown,
			wantInternalMessage: "resource SKU has no location info",
		},
		{
			name:     "unknown when SKU lookup fails",
			nodePool: newAvailabilityZoneTestNodePool(t, "1"),
			setupMockVMSKUCachedReader: func(skuReader *cachedreader.MockVirtualMachineResourceSKUsCachedReader) {
				skuReader.EXPECT().
					GetVirtualMachineSKU(gomock.Any(), testTenantID, testSubscriptionID, "eastus", testVMSize).
					Return(nil, errors.New("VM size not found"))
			},
			wantOutcome:         OutcomeTypeUnknown,
			wantInternalMessage: `failed to get resource SKU for VM size "Standard_D8ds_v5": VM size not found`,
		},
		{
			name: "unknown when subscription is missing tenant ID",
			subscription: &coreapi.Subscription{
				Properties: &coreapi.SubscriptionProperties{},
			},
			nodePool:            newAvailabilityZoneTestNodePool(t, "1"),
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
			validation := NewAzureNodePoolAvailabilityZoneValidation(skuReader)
			result := validation.Validate(ctx, cluster, sub, tt.nodePool)
			require.NoError(t, result.Validate())
			assert.Equal(t, tt.wantOutcome, result.Outcome.Type)
			if tt.wantInternalMessage != "" {
				assert.Contains(t, result.InternalMessage(), tt.wantInternalMessage)
			}
		})
	}
}

func TestAzureNodePoolAvailabilityZoneValidation_zoneIsRestricted(t *testing.T) {
	tests := []struct {
		name           string
		sku            *armcompute.ResourceSKU
		zone           string
		wantRestricted bool
		wantReason     string
	}{
		{
			name: "no restrictions",
			sku:  makeTestZonalSKU("1", "2", "3"),
			zone: "2",
		},
		{
			name: "location-type restriction is ignored",
			sku: skuWithRestrictions(armcompute.ResourceSKURestrictions{
				Type: ptr.To(armcompute.ResourceSKURestrictionsTypeLocation),
				RestrictionInfo: &armcompute.ResourceSKURestrictionInfo{
					Locations: []*string{ptr.To("eastus")},
				},
			}),
			zone: "2",
		},
		{
			name: "zone restriction with nil restriction info is ignored",
			sku: skuWithRestrictions(armcompute.ResourceSKURestrictions{
				Type: ptr.To(armcompute.ResourceSKURestrictionsTypeZone),
			}),
			zone: "2",
		},
		{
			name: "zone restriction not matching the requested zone is ignored",
			sku: skuWithRestrictions(armcompute.ResourceSKURestrictions{
				Type:       ptr.To(armcompute.ResourceSKURestrictionsTypeZone),
				ReasonCode: ptr.To(armcompute.ResourceSKURestrictionsReasonCodeNotAvailableForSubscription),
				RestrictionInfo: &armcompute.ResourceSKURestrictionInfo{
					Zones: []*string{ptr.To("3")},
				},
			}),
			zone: "2",
		},
		{
			name: "zone restriction matching the requested zone reports the reason code",
			sku: skuWithRestrictions(armcompute.ResourceSKURestrictions{
				Type:       ptr.To(armcompute.ResourceSKURestrictionsTypeZone),
				ReasonCode: ptr.To(armcompute.ResourceSKURestrictionsReasonCodeNotAvailableForSubscription),
				RestrictionInfo: &armcompute.ResourceSKURestrictionInfo{
					Zones: []*string{ptr.To("2")},
				},
			}),
			zone:           "2",
			wantRestricted: true,
			wantReason:     "NotAvailableForSubscription",
		},
		{
			name: "zone restriction with a nil reason code reports an unknown reason",
			sku: skuWithRestrictions(armcompute.ResourceSKURestrictions{
				Type: ptr.To(armcompute.ResourceSKURestrictionsTypeZone),
				RestrictionInfo: &armcompute.ResourceSKURestrictionInfo{
					Zones: []*string{ptr.To("2")},
				},
			}),
			zone:           "2",
			wantRestricted: true,
			wantReason:     "unknown reason",
		},
		{
			name: "nil restriction entry and nil type are skipped",
			sku: skuWithRestrictions(nil, armcompute.ResourceSKURestrictions{
				RestrictionInfo: &armcompute.ResourceSKURestrictionInfo{
					Zones: []*string{ptr.To("2")},
				},
			}),
			zone: "2",
		},
		{
			name: "nil zone entries in restriction info are skipped",
			sku: skuWithRestrictions(armcompute.ResourceSKURestrictions{
				Type:       ptr.To(armcompute.ResourceSKURestrictionsTypeZone),
				ReasonCode: ptr.To(armcompute.ResourceSKURestrictionsReasonCodeQuotaID),
				RestrictionInfo: &armcompute.ResourceSKURestrictionInfo{
					Zones: []*string{nil, ptr.To("2")},
				},
			}),
			zone:           "2",
			wantRestricted: true,
			wantReason:     "QuotaId",
		},
		{
			name: "match found on a later restriction entry reports it correctly",
			sku: skuWithRestrictions(
				armcompute.ResourceSKURestrictions{
					Type:       ptr.To(armcompute.ResourceSKURestrictionsTypeZone),
					ReasonCode: ptr.To(armcompute.ResourceSKURestrictionsReasonCodeQuotaID),
					RestrictionInfo: &armcompute.ResourceSKURestrictionInfo{
						Zones: []*string{ptr.To("1")},
					},
				},
				armcompute.ResourceSKURestrictions{
					Type:       ptr.To(armcompute.ResourceSKURestrictionsTypeZone),
					ReasonCode: ptr.To(armcompute.ResourceSKURestrictionsReasonCodeNotAvailableForSubscription),
					RestrictionInfo: &armcompute.ResourceSKURestrictionInfo{
						Zones: []*string{ptr.To("2")},
					},
				},
			),
			zone:           "2",
			wantRestricted: true,
			wantReason:     "NotAvailableForSubscription",
		},
	}

	validation := NewAzureNodePoolAvailabilityZoneValidation(nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restricted, reason := validation.zoneIsRestricted(tt.sku, tt.zone)
			assert.Equal(t, tt.wantRestricted, restricted)
			assert.Equal(t, tt.wantReason, reason)
		})
	}
}

// skuWithRestrictions returns a zonal test SKU with the given restrictions attached.
// A nil entry in restrictions results in a nil *armcompute.ResourceSKURestrictions, exercising
// the nil-restriction-entry guard in zoneIsRestricted.
func skuWithRestrictions(restrictions ...any) *armcompute.ResourceSKU {
	sku := makeTestZonalSKU("1", "2", "3")
	sku.Restrictions = make([]*armcompute.ResourceSKURestrictions, 0, len(restrictions))
	for _, restriction := range restrictions {
		if restriction == nil {
			sku.Restrictions = append(sku.Restrictions, nil)
			continue
		}
		r := restriction.(armcompute.ResourceSKURestrictions)
		sku.Restrictions = append(sku.Restrictions, &r)
	}
	return sku
}
