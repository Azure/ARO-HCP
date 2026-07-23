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

	"github.com/Azure/ARO-HCP/backend/pkg/azure/cachedreader"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

// AzureVMSizeSupportsEphemeralOSDiskValidation validates that a node pool requesting
// an ephemeral OS disk uses a VM size that advertises EphemeralOSDiskSupported.
// Node pools without ephemeral OS disks are skipped.
type AzureVMSizeSupportsEphemeralOSDiskValidation struct {
	resourceSKUsCachedReader cachedreader.VirtualMachineResourceSKUsCachedReader
}

func NewAzureVMSizeSupportsEphemeralOSDiskValidation(resourceSKUsCachedReader cachedreader.VirtualMachineResourceSKUsCachedReader) *AzureVMSizeSupportsEphemeralOSDiskValidation {
	return &AzureVMSizeSupportsEphemeralOSDiskValidation{resourceSKUsCachedReader: resourceSKUsCachedReader}
}

var _ NodePoolValidation = (*AzureVMSizeSupportsEphemeralOSDiskValidation)(nil)

func (v *AzureVMSizeSupportsEphemeralOSDiskValidation) Name() string {
	return "AzureVMSizeSupportsEphemeralOSDiskValidation"
}

func (v *AzureVMSizeSupportsEphemeralOSDiskValidation) Validate(ctx context.Context, _ *coreapi.HCPOpenShiftCluster, nodePoolSubscription *coreapi.Subscription, nodePool *coreapi.HCPOpenShiftClusterNodePool) ValidationResult {
	if nodePool.Properties.Platform.OSDisk.DiskType != metadataapi.OsDiskTypeEphemeral {
		return SkippedValidation(
			"NotApplicable",
			"Node pool does not use an ephemeral OS disk.",
			"Node pool does not use an ephemeral OS disk; ephemeral OS disk validation does not apply.",
		)
	}

	if nodePoolSubscription.Properties == nil || nodePoolSubscription.Properties.TenantId == nil || *nodePoolSubscription.Properties.TenantId == "" {
		return UnknownValidation(
			"InternalError",
			"Unable to verify VM size support for ephemeral OS disks.",
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
			"Unable to verify VM size support for ephemeral OS disks.",
			fmt.Sprintf("failed to get resource SKU for VM size %q: %s", vmSize, err),
			ControllerReportingPolicyTypeError,
		)
	}

	supported, found := isCapabilityEphemeralOSDiskSupported(sku)
	if !found {
		return UnknownValidation(
			"InternalError",
			"Unable to verify VM size support for ephemeral OS disks.",
			fmt.Sprintf("resource SKU for VM size %q is missing %s capability", vmSize, computeResourceSKUCapabilityNameEphemeralOSDiskSupported),
			ControllerReportingPolicyTypeError,
		)
	}
	if !supported {
		userMsg := fmt.Sprintf("vm size %q does not support ephemeral OS disks", vmSize)
		return FailedValidation("EphemeralOSDiskNotSupported", userMsg, userMsg)
	}

	internalMsg := fmt.Sprintf("VM size %q supports ephemeral OS disks.", vmSize)
	return PassedValidation(coreapi.ControllerConditionReasonAsExpected, internalMsg, internalMsg)
}
