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
	"slices"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/cachedreader"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// AzureNodePoolAvailabilityZoneValidation validates that a node pool requesting a specific
// availability zone uses a VM size that is available in that zone in the node pool's location.
// Node pools that do not request a specific availability zone are skipped.
type AzureNodePoolAvailabilityZoneValidation struct {
	resourceSKUsCachedReader cachedreader.VirtualMachineResourceSKUsCachedReader
}

func NewAzureNodePoolAvailabilityZoneValidation(resourceSKUsCachedReader cachedreader.VirtualMachineResourceSKUsCachedReader) *AzureNodePoolAvailabilityZoneValidation {
	return &AzureNodePoolAvailabilityZoneValidation{resourceSKUsCachedReader: resourceSKUsCachedReader}
}

var _ NodePoolValidation = (*AzureNodePoolAvailabilityZoneValidation)(nil)

func (v *AzureNodePoolAvailabilityZoneValidation) Name() string {
	return "AzureNodePoolAvailabilityZoneValidation"
}

func (v *AzureNodePoolAvailabilityZoneValidation) Validate(ctx context.Context, _ *coreapi.Cluster, nodePoolSubscription *coreapi.Subscription, nodePool *coreapi.NodePool) ValidationResult {
	nodePoolAvailabilityZone := nodePool.Properties.Platform.AvailabilityZone
	if len(nodePoolAvailabilityZone) == 0 {
		return SkippedValidation(
			"NotApplicable",
			"Node pool does not request a specific availability zone.",
			"Node pool does not request a specific availability zone; availability zone validation does not apply.",
		)
	}

	if nodePoolSubscription.Properties == nil || nodePoolSubscription.Properties.TenantId == nil || len(*nodePoolSubscription.Properties.TenantId) == 0 {
		return UnknownValidation(
			"InternalError",
			"Unable to verify VM size availability in the requested zone.",
			"subscription is missing tenant ID",
			ControllerReportingPolicyTypeError,
		)
	}
	tenantID := *nodePoolSubscription.Properties.TenantId

	vmSize := nodePool.Properties.Platform.VMSize
	sku, err := v.resourceSKUsCachedReader.GetVirtualMachineSKU(ctx, tenantID, nodePool.ID.SubscriptionID, nodePool.Location, vmSize)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify VM size availability in the requested zone.",
			fmt.Sprintf("failed to get resource SKU for VM size %q: %s", vmSize, err),
			ControllerReportingPolicyTypeError,
		)
	}

	zones, err := v.vmSizeAvailabilityZones(sku)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify VM size availability in the requested zone.",
			fmt.Sprintf("failed to determine availability zones for VM size %q: %s", vmSize, err),
			ControllerReportingPolicyTypeError,
		)
	}

	if len(zones) == 0 {
		internalAndUserMsg := fmt.Sprintf("VM size %q is available in location %q but does not support deployment to a specific availability zone", vmSize, nodePool.Location)
		return FailedValidation("AvailabilityZoneNotSupported", internalAndUserMsg, internalAndUserMsg)
	}

	if !slices.Contains(zones, nodePoolAvailabilityZone) {
		internalAndUserMsg := fmt.Sprintf("VM size %q does not support availability zone %q in location %q", vmSize, nodePoolAvailabilityZone, nodePool.Location)
		return FailedValidation("AvailabilityZoneNotSupported", internalAndUserMsg, internalAndUserMsg)
	}

	if restricted, restrictionReason := v.zoneIsRestricted(sku, nodePoolAvailabilityZone); restricted {
		internalAndUserMsg := fmt.Sprintf("VM size %q is restricted in availability zone %q in location %q: %s", vmSize, nodePoolAvailabilityZone, nodePool.Location, restrictionReason)
		return FailedValidation("AvailabilityZoneNotSupported", internalAndUserMsg, internalAndUserMsg)
	}

	internalAndUserMsg := fmt.Sprintf("VM size %q supports availability zone %q in location %q.", vmSize, nodePoolAvailabilityZone, nodePool.Location)
	return PassedValidation(coreapi.ControllerConditionReasonAsExpected, internalAndUserMsg, internalAndUserMsg)
}

// vmSizeAvailabilityZones returns the availability zones advertised by the SKU's location info.
// GetVirtualMachineSKU always returns a SKU from a Resource SKUs list filtered to a single
// location, so exactly one LocationInfo entry is expected.
func (v *AzureNodePoolAvailabilityZoneValidation) vmSizeAvailabilityZones(sku *armcompute.ResourceSKU) ([]string, error) {
	if len(sku.LocationInfo) == 0 || sku.LocationInfo[0] == nil {
		return nil, fmt.Errorf("resource SKU has no location info")
	}
	zones := make([]string, 0, len(sku.LocationInfo[0].Zones))
	for _, zone := range sku.LocationInfo[0].Zones {
		if zone != nil {
			zones = append(zones, *zone)
		}
	}
	return zones, nil
}

// zoneIsRestricted reports whether the given zone is subject to a zone-level restriction on
// this SKU (e.g. not available for the subscription, or gated behind a quota) and, if so, a
// human-readable reason. A SKU can be restricted in one zone of a location while remaining
// available in others, so restrictions are matched against the specific requested zone.
func (v *AzureNodePoolAvailabilityZoneValidation) zoneIsRestricted(sku *armcompute.ResourceSKU, zone string) (bool, string) {
	for _, restriction := range sku.Restrictions {
		if restriction == nil || restriction.Type == nil || *restriction.Type != armcompute.ResourceSKURestrictionsTypeZone || restriction.RestrictionInfo == nil {
			continue
		}
		for _, restrictedZone := range restriction.RestrictionInfo.Zones {
			if restrictedZone == nil || *restrictedZone != zone {
				continue
			}
			if restriction.ReasonCode != nil {
				return true, string(*restriction.ReasonCode)
			}
			return true, "unknown reason"
		}
	}
	return false, ""
}
