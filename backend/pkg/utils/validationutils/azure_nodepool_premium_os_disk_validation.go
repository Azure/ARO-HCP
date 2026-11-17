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

// AzureNodePoolPremiumOSDiskValidation validates that a node pool requesting a Premium_LRS OS
// disk uses a VM size that advertises PremiumIO support. Node pools using a different OS disk
// storage account type are skipped.
type AzureNodePoolPremiumOSDiskValidation struct {
	resourceSKUsCachedReader cachedreader.VirtualMachineResourceSKUsCachedReader
}

func NewAzureNodePoolPremiumOSDiskValidation(resourceSKUsCachedReader cachedreader.VirtualMachineResourceSKUsCachedReader) *AzureNodePoolPremiumOSDiskValidation {
	return &AzureNodePoolPremiumOSDiskValidation{resourceSKUsCachedReader: resourceSKUsCachedReader}
}

var _ NodePoolValidation = (*AzureNodePoolPremiumOSDiskValidation)(nil)

func (v *AzureNodePoolPremiumOSDiskValidation) Name() string {
	return "AzureNodePoolPremiumOSDiskValidation"
}

func (v *AzureNodePoolPremiumOSDiskValidation) Validate(ctx context.Context, _ *coreapi.Cluster, nodePoolSubscription *coreapi.Subscription, nodePool *coreapi.NodePool) ValidationResult {
	if nodePool.Properties.Platform.OSDisk.DiskStorageAccountType != metadataapi.DiskStorageAccountTypePremium_LRS {
		return SkippedValidation(
			"NotApplicable",
			"Node pool does not use a Premium_LRS OS disk.",
			"Node pool does not use a Premium_LRS OS disk; premium OS disk validation does not apply.",
		)
	}

	if nodePoolSubscription.Properties == nil || nodePoolSubscription.Properties.TenantId == nil || len(*nodePoolSubscription.Properties.TenantId) == 0 {
		return UnknownValidation(
			"InternalError",
			"Unable to verify VM size support for Premium_LRS OS disks.",
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
			"Unable to verify VM size support for Premium_LRS OS disks.",
			fmt.Sprintf("failed to get resource SKU for VM size %q: %s", vmSize, err),
			ControllerReportingPolicyTypeError,
		)
	}

	supported, found := isCapabilityPremiumIOSupported(sku)
	if !found {
		return UnknownValidation(
			"InternalError",
			"Unable to verify VM size support for Premium_LRS OS disks.",
			fmt.Sprintf("resource SKU for VM size %q is missing %s capability", vmSize, computeResourceSKUCapabilityNamePremiumIO),
			ControllerReportingPolicyTypeError,
		)
	}
	if !supported {
		userMsg := fmt.Sprintf("vm size %q does not support Premium_LRS OS disks", vmSize)
		return FailedValidation("PremiumOSDiskNotSupported", userMsg, userMsg)
	}

	internalMsg := fmt.Sprintf("VM size %q supports Premium_LRS OS disks.", vmSize)
	return PassedValidation(coreapi.ControllerConditionReasonAsExpected, internalMsg, internalMsg)
}
