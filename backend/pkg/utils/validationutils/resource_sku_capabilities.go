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
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
)

const (
	// computeResourceSKUCapabilityNameEphemeralOSDiskSupported is the
	// Microsoft.Compute Resource SKU capability that indicates whether a VM
	// size supports ephemeral OS disks.
	computeResourceSKUCapabilityNameEphemeralOSDiskSupported = "EphemeralOSDiskSupported"
	// computeResourceSKUCapabilityNameVCPUs is the Microsoft.Compute Resource
	// SKU capability that advertises the number of vCPUs for a VM size.
	computeResourceSKUCapabilityNameVCPUs = "vCPUs"

	// computeUsageNameTotalRegionalVCPUs is the Microsoft.Compute Usage API
	// Name.Value for the subscription's total regional vCPU quota
	// (localized as "Total Regional vCPUs").
	computeUsageNameTotalRegionalVCPUs = "cores"
)

// resourceSKUCapability returns the raw string value of the named Resource SKU
// capability, or nil if the SKU is nil, the capability is missing, or Name/Value
// are unset.
func resourceSKUCapability(sku *armcompute.ResourceSKU, capabilityName string) *string {
	if sku == nil {
		return nil
	}
	for _, capability := range sku.Capabilities {
		if capability == nil || capability.Name == nil || capability.Value == nil {
			continue
		}
		if strings.EqualFold(*capability.Name, capabilityName) {
			return capability.Value
		}
	}
	return nil
}

// isCapabilityEphemeralOSDiskSupported reports whether the Resource SKU
// advertises EphemeralOSDiskSupported.
//
// Returns:
//   - (true, true) when the capability is present and its value is "True" (case-insensitive)
//   - (false, true) when the capability is present but not "True"
//   - (false, false) when the SKU is nil or the capability is missing
func isCapabilityEphemeralOSDiskSupported(sku *armcompute.ResourceSKU) (bool, bool) {
	value := resourceSKUCapability(sku, computeResourceSKUCapabilityNameEphemeralOSDiskSupported)
	if value == nil {
		return false, false
	}
	return strings.EqualFold(*value, "True"), true
}

// lookupCapabilityVCPUs returns the Resource SKU vCPUs capability parsed as an int.
//
// The bool is true when the capability is present and its value parses as an
// integer (after TrimSpace). When the bool is false, the int is always 0
// (capability missing, empty, or not an integer). A successfully parsed value
// of 0 is returned as (0, true).
func lookupCapabilityVCPUs(sku *armcompute.ResourceSKU) (int, bool) {
	value := resourceSKUCapability(sku, computeResourceSKUCapabilityNameVCPUs)
	if value == nil {
		return 0, false
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(*value))
	if err != nil {
		return 0, false
	}
	return parsed, true
}
