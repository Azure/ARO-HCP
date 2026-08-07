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
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/cachedreader"
	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// AzureNodePoolVMQuotaValidation validates that the customer's subscription has
// enough Compute vCPU quota in the node pool's location to deploy the requested
// VM size at the requested scale (replicas, or autoscaler max).
//
// Quota is checked against both the VM size family usage and the total regional
// vCPU usage (Compute Usage Name.Value "cores").
type AzureNodePoolVMQuotaValidation struct {
	resourceSKUsCachedReader cachedreader.VirtualMachineResourceSKUsCachedReader
	azureFPAClientBuilder    azureclient.FirstPartyApplicationClientBuilder
}

func NewAzureNodePoolVMQuotaValidation(resourceSKUsCachedReader cachedreader.VirtualMachineResourceSKUsCachedReader, azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder) *AzureNodePoolVMQuotaValidation {
	return &AzureNodePoolVMQuotaValidation{
		resourceSKUsCachedReader: resourceSKUsCachedReader,
		azureFPAClientBuilder:    azureFPAClientBuilder,
	}
}

var _ NodePoolValidation = (*AzureNodePoolVMQuotaValidation)(nil)

func (v *AzureNodePoolVMQuotaValidation) Name() string {
	return "AzureNodePoolVMQuotaValidation"
}

func (v *AzureNodePoolVMQuotaValidation) Validate(ctx context.Context, _ *api.HCPOpenShiftCluster, nodePoolSubscription *arm.Subscription, nodePool *api.HCPOpenShiftClusterNodePool) ValidationResult {
	instanceCount := v.requiredInstanceCount(nodePool)
	if instanceCount <= 0 {
		return SkippedValidation(
			"NotApplicable",
			"Node pool has no instances to validate quota for.",
			"Node pool has zero replicas and is not configured for autoscaling.",
		)
	}

	if nodePoolSubscription.Properties == nil || nodePoolSubscription.Properties.TenantId == nil || *nodePoolSubscription.Properties.TenantId == "" {
		return UnknownValidation(
			"InternalError",
			"Unable to verify VM quota.",
			"subscription is missing tenant ID",
			ControllerReportingPolicyTypeError,
		)
	}
	tenantID := *nodePoolSubscription.Properties.TenantId

	vmSize := nodePool.Properties.Platform.VMSize
	subscriptionID := nodePool.ID.SubscriptionID

	sku, err := v.resourceSKUsCachedReader.GetVirtualMachineSKU(ctx, tenantID, subscriptionID, nodePool.Location, vmSize)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify VM quota.",
			fmt.Sprintf("failed to get resource SKU for VM size %q: %s", vmSize, err),
			ControllerReportingPolicyTypeError,
		)
	}
	if sku.Family == nil || *sku.Family == "" {
		return UnknownValidation(
			"InternalError",
			"Unable to verify VM quota.",
			fmt.Sprintf("resource SKU for VM size %q is missing family", vmSize),
			ControllerReportingPolicyTypeError,
		)
	}
	family := *sku.Family

	vcpusPerInstance, ok := lookupCapabilityVCPUs(sku)
	if !ok {
		return UnknownValidation(
			"InternalError",
			"Unable to verify VM quota.",
			fmt.Sprintf("resource SKU for VM size %q is missing %s capability", vmSize, computeResourceSKUCapabilityNameVCPUs),
			ControllerReportingPolicyTypeError,
		)
	}
	if vcpusPerInstance <= 0 {
		return UnknownValidation(
			"InternalError",
			"Unable to verify VM quota.",
			fmt.Sprintf("resource SKU for VM size %q has unexpected %s capability value %d", vmSize, computeResourceSKUCapabilityNameVCPUs, vcpusPerInstance),
			ControllerReportingPolicyTypeError,
		)
	}

	requiredVCPUs := int64(instanceCount) * int64(vcpusPerInstance)

	usageClient, err := v.azureFPAClientBuilder.UsageClient(tenantID, subscriptionID)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify VM quota.",
			fmt.Sprintf("failed to create usage client: %s", err),
			ControllerReportingPolicyTypeError,
		)
	}

	familyUsage, regionalUsage, err := v.lookupFamilyAndRegionalVCPUUsages(ctx, usageClient, nodePool.Location, family)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify VM quota.",
			err.Error(),
			ControllerReportingPolicyTypeError,
		)
	}

	// Limit 0 means creation is not allowed, independently of CurrentValue
	// (remaining = Limit - CurrentValue).
	familyRemaining := *familyUsage.Limit - int64(*familyUsage.CurrentValue)
	regionalRemaining := *regionalUsage.Limit - int64(*regionalUsage.CurrentValue)

	var failureMessages []string
	if requiredVCPUs > familyRemaining {
		failureMessages = append(failureMessages, fmt.Sprintf("insufficient quota for VM size %q family %q: need %d vCPUs, have %d remaining for %q (current %d, limit %d)",
			vmSize, family, requiredVCPUs, familyRemaining, localizedNameFromComputeUsage(familyUsage), *familyUsage.CurrentValue, *familyUsage.Limit))
	}
	if requiredVCPUs > regionalRemaining {
		failureMessages = append(failureMessages, fmt.Sprintf("insufficient total regional vCPU quota for VM size %q: need %d vCPUs, have %d remaining for %q (current %d, limit %d)",
			vmSize, requiredVCPUs, regionalRemaining, localizedNameFromComputeUsage(regionalUsage), *regionalUsage.CurrentValue, *regionalUsage.Limit))
	}
	if len(failureMessages) > 0 {
		combined := strings.Join(failureMessages, "; ")
		return FailedValidation("InsufficientVMQuota", combined, combined)
	}

	internalMsg := fmt.Sprintf("Sufficient VM quota for VM size %q in location %q.", vmSize, nodePool.Location)
	return PassedValidation(api.ControllerConditionReasonAsExpected, internalMsg, internalMsg)
}

// requiredInstanceCount returns the peak number of VMs the node pool may run.
// Autoscaled pools use AutoScaling.Max; fixed-size pools use Replicas.
func (v *AzureNodePoolVMQuotaValidation) requiredInstanceCount(nodePool *api.HCPOpenShiftClusterNodePool) int32 {
	if nodePool.Properties.AutoScaling != nil {
		return nodePool.Properties.AutoScaling.Max
	}
	return nodePool.Properties.Replicas
}

// lookupFamilyAndRegionalVCPUUsages pages Microsoft.Compute location usages and
// returns the usage entries for the given VM size family and for total regional
// vCPUs (Name.Value "cores").
//
// Parameters:
//   - usageClient: Compute Usage client for the customer subscription
//   - location: Azure region of the node pool (Usage API is per-location)
//   - family: Resource SKU family name (e.g. "standardDSv3Family"), matched
//     case-insensitively against Usage.Name.Value
//
// Returns the family usage and regional ("cores") usage, or an error if listing
// fails or either entry is missing from the Usage API response.
func (v *AzureNodePoolVMQuotaValidation) lookupFamilyAndRegionalVCPUUsages(ctx context.Context, usageClient azureclient.UsageClient, location string, family string) (*armcompute.Usage, *armcompute.Usage, error) {
	logger := utils.LoggerFromContext(ctx)
	var familyUsage *armcompute.Usage
	var regionalUsage *armcompute.Usage

	pager := usageClient.NewListPager(location, nil)
	for pager.More() {
		page, pageErr := pager.NextPage(ctx)
		if pageErr != nil {
			return nil, nil, utils.TrackError(fmt.Errorf("failed to list compute usages for location %q: %w", location, pageErr))
		}
		for _, usage := range page.Value {
			if !isValidComputeUsage(usage) {
				logger.Info("skipping unexpected compute usage entry missing required fields", "location", location, "usage", usage)
				continue
			}
			name := *usage.Name.Value
			if strings.EqualFold(name, family) {
				familyUsage = usage
			}
			if strings.EqualFold(name, computeUsageNameTotalRegionalVCPUs) {
				regionalUsage = usage
			}
			if familyUsage != nil && regionalUsage != nil {
				return familyUsage, regionalUsage, nil
			}
		}
	}

	if familyUsage == nil {
		return nil, nil, utils.TrackError(fmt.Errorf("compute usage for VM family %q was not found in location %q", family, location))
	}
	if regionalUsage == nil {
		return nil, nil, utils.TrackError(fmt.Errorf("compute usage %q (total regional vCPUs) was not found in location %q", computeUsageNameTotalRegionalVCPUs, location))
	}
	return familyUsage, regionalUsage, nil
}

// isValidComputeUsage reports whether a Microsoft.Compute Usage list entry has
// the fields this package needs to inspect it: non-empty Name.Value,
// CurrentValue, and Limit.
func isValidComputeUsage(usage *armcompute.Usage) bool {
	return usage != nil && usage.Name != nil && usage.Name.Value != nil && *usage.Name.Value != "" && usage.CurrentValue != nil && usage.Limit != nil
}

// localizedNameFromComputeUsage returns a human-readable usage name for error
// messages, preferring Name.LocalizedValue (e.g. "Standard DSv2 Family vCPUs") and
// falling back to Name.Value (e.g. "standardDSv2Family").
func localizedNameFromComputeUsage(usage *armcompute.Usage) string {
	if usage.Name.LocalizedValue != nil && *usage.Name.LocalizedValue != "" {
		return *usage.Name.LocalizedValue
	}
	return *usage.Name.Value
}
