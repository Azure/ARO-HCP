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
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// AzureVMSizeSupportsEphemeralOSDiskValidation validates that a node pool requesting
// an ephemeral OS disk uses a VM size that advertises EphemeralOSDiskSupported.
// Node pools with managed OS disks are skipped.
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

func (v *AzureVMSizeSupportsEphemeralOSDiskValidation) Validate(ctx context.Context, _ *api.HCPOpenShiftCluster, nodePoolSubscription *arm.Subscription, nodePool *api.HCPOpenShiftClusterNodePool) error {
	if nodePool.Properties.Platform.OSDisk.DiskType != api.OsDiskTypeEphemeral {
		return nil
	}

	if nodePoolSubscription.Properties == nil || nodePoolSubscription.Properties.TenantId == nil || *nodePoolSubscription.Properties.TenantId == "" {
		return utils.TrackError(fmt.Errorf("subscription is missing tenant ID"))
	}
	tenantID := *nodePoolSubscription.Properties.TenantId

	vmSize := nodePool.Properties.Platform.VMSize
	sku, err := v.resourceSKUsCachedReader.GetVirtualMachineSKU(ctx, tenantID, nodePool.ID.SubscriptionID, nodePool.Location, vmSize)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get resource SKU for VM size %q: %w", vmSize, err))
	}

	supported, found := isCapabilityEphemeralOSDiskSupported(sku)
	if !found {
		return utils.TrackError(fmt.Errorf("resource SKU for VM size %q is missing %s capability", vmSize, computeResourceSKUCapabilityNameEphemeralOSDiskSupported))
	}
	if !supported {
		return utils.TrackError(fmt.Errorf("vm size %q does not support ephemeral OS disks", vmSize))
	}

	return nil
}
