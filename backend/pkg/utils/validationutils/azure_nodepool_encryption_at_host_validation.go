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

	"github.com/Azure/ARO-HCP/backend/pkg/azure/cachedreader"
	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

const (
	// encryptionAtHostFeatureResourceProviderNamespace is the Azure Resource Provider namespace
	// that owns the EncryptionAtHost feature.
	encryptionAtHostFeatureResourceProviderNamespace = "Microsoft.Compute"
	// encryptionAtHostFeatureName is the Microsoft.Features feature name that gates a
	// subscription's ability to use encryption at host.
	encryptionAtHostFeatureName = "EncryptionAtHost"
	// featureRegistrationStateRegistered is the Microsoft.Features registration state value
	// indicating a feature is registered for a subscription.
	featureRegistrationStateRegistered = "Registered"
)

// AzureNodePoolEncryptionAtHostValidation validates that a node pool requesting encryption at
// host uses a VM size that supports it and that the EncryptionAtHost feature is registered for
// the customer's subscription. Node pools that do not request encryption at host are skipped.
type AzureNodePoolEncryptionAtHostValidation struct {
	resourceSKUsCachedReader cachedreader.VirtualMachineResourceSKUsCachedReader
	azureFPAClientBuilder    azureclient.FirstPartyApplicationClientBuilder
}

func NewAzureNodePoolEncryptionAtHostValidation(resourceSKUsCachedReader cachedreader.VirtualMachineResourceSKUsCachedReader, azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder) *AzureNodePoolEncryptionAtHostValidation {
	return &AzureNodePoolEncryptionAtHostValidation{
		resourceSKUsCachedReader: resourceSKUsCachedReader,
		azureFPAClientBuilder:    azureFPAClientBuilder,
	}
}

var _ NodePoolValidation = (*AzureNodePoolEncryptionAtHostValidation)(nil)

func (v *AzureNodePoolEncryptionAtHostValidation) Name() string {
	return "AzureNodePoolEncryptionAtHostValidation"
}

func (v *AzureNodePoolEncryptionAtHostValidation) Validate(ctx context.Context, _ *coreapi.Cluster, nodePoolSubscription *coreapi.Subscription, nodePool *coreapi.NodePool) ValidationResult {
	if !nodePool.Properties.Platform.EnableEncryptionAtHost {
		return SkippedValidation(
			"NotApplicable",
			"Node pool does not request encryption at host.",
			"Node pool does not request encryption at host; encryption at host validation does not apply.",
		)
	}

	if nodePoolSubscription.Properties == nil || nodePoolSubscription.Properties.TenantId == nil || len(*nodePoolSubscription.Properties.TenantId) == 0 {
		return UnknownValidation(
			"InternalError",
			"Unable to verify encryption at host support.",
			"subscription is missing tenant ID",
			ControllerReportingPolicyTypeError,
		)
	}
	tenantID := *nodePoolSubscription.Properties.TenantId
	subscriptionID := nodePool.ID.SubscriptionID

	vmSize := nodePool.Properties.Platform.VMSize
	sku, err := v.resourceSKUsCachedReader.GetVirtualMachineSKU(ctx, tenantID, subscriptionID, nodePool.Location, vmSize)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify encryption at host support.",
			fmt.Sprintf("failed to get resource SKU for VM size %q: %s", vmSize, err),
			ControllerReportingPolicyTypeError,
		)
	}

	supported, found := isCapabilityEncryptionAtHostSupported(sku)
	if !found {
		return UnknownValidation(
			"InternalError",
			"Unable to verify encryption at host support.",
			fmt.Sprintf("resource SKU for VM size %q is missing %s capability", vmSize, computeResourceSKUCapabilityNameEncryptionAtHostSupported),
			ControllerReportingPolicyTypeError,
		)
	}
	featuresClient, err := v.azureFPAClientBuilder.FeaturesClient(tenantID, subscriptionID)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify encryption at host support.",
			fmt.Sprintf("failed to create features client: %s", err),
			ControllerReportingPolicyTypeError,
		)
	}

	feature, err := featuresClient.Get(ctx, encryptionAtHostFeatureResourceProviderNamespace, encryptionAtHostFeatureName, nil)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify encryption at host support.",
			fmt.Sprintf("failed to get feature %s/%s: %s", encryptionAtHostFeatureResourceProviderNamespace, encryptionAtHostFeatureName, err),
			ControllerReportingPolicyTypeError,
		)
	}

	if feature.Properties == nil || feature.Properties.State == nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify encryption at host support.",
			"feature properties or state is missing",
			ControllerReportingPolicyTypeError,
		)
	}

	var failureMessages []string
	if !supported {
		failureMessages = append(failureMessages, fmt.Sprintf("vm size %q does not support encryption at host", vmSize))
	}
	if !strings.EqualFold(*feature.Properties.State, featureRegistrationStateRegistered) {
		failureMessages = append(failureMessages, fmt.Sprintf("feature %q is not registered for subscription %q", encryptionAtHostFeatureName, subscriptionID))
	}
	if len(failureMessages) > 0 {
		combined := strings.Join(failureMessages, "; ")
		return FailedValidation("EncryptionAtHostNotSupported", combined, combined)
	}

	internalMsg := fmt.Sprintf("VM size %q supports encryption at host and the %s feature is registered for subscription %q.", vmSize, encryptionAtHostFeatureName, subscriptionID)
	return PassedValidation(coreapi.ControllerConditionReasonAsExpected, internalMsg, internalMsg)
}
