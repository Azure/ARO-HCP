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
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// diskEncryptionSetProvisioningStateSucceeded is the Microsoft.Compute provisioning state value
// indicating a disk encryption set has finished provisioning successfully.
const diskEncryptionSetProvisioningStateSucceeded = "Succeeded"

// AzureNodePoolDiskEncryptionSetValidation validates that a node pool's OS disk encryption set
// has finished provisioning successfully and is in the same Azure region as the node pool.
// Node pools that do not specify a disk encryption set are skipped.
type AzureNodePoolDiskEncryptionSetValidation struct {
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder
}

func NewAzureNodePoolDiskEncryptionSetValidation(smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder) *AzureNodePoolDiskEncryptionSetValidation {
	return &AzureNodePoolDiskEncryptionSetValidation{smiClientBuilder: smiClientBuilder}
}

var _ NodePoolValidation = (*AzureNodePoolDiskEncryptionSetValidation)(nil)

func (v *AzureNodePoolDiskEncryptionSetValidation) Name() string {
	return "AzureNodePoolDiskEncryptionSetValidation"
}

func (v *AzureNodePoolDiskEncryptionSetValidation) Validate(ctx context.Context, cluster *coreapi.Cluster, _ *coreapi.Subscription, nodePool *coreapi.NodePool) ValidationResult {
	encryptionSetID := nodePool.Properties.Platform.OSDisk.EncryptionSetID
	if encryptionSetID == nil {
		return SkippedValidation(
			"NotApplicable",
			"Node pool does not use a disk encryption set.",
			"Node pool does not specify an OS disk encryption set; disk encryption set validation does not apply.",
		)
	}

	smiResourceID := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
	clusterIdentityURL := cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL
	subscriptionID := cluster.ID.SubscriptionID

	diskEncryptionSetsClient, err := v.smiClientBuilder.DiskEncryptionSetsClient(ctx, clusterIdentityURL, smiResourceID, subscriptionID)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify disk encryption set.",
			fmt.Sprintf("failed to get disk encryption sets client as service managed identity: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}

	diskEncryptionSet, err := diskEncryptionSetsClient.Get(ctx, encryptionSetID.ResourceGroupName, encryptionSetID.Name, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
			userMsg := fmt.Sprintf("disk encryption set %q was not found", encryptionSetID.String())
			return FailedValidation("DiskEncryptionSetNotFound", userMsg, userMsg)
		}
		return UnknownValidation(
			"InternalError",
			"Unable to verify disk encryption set.",
			fmt.Sprintf("failed to get disk encryption set %q: %v", encryptionSetID.String(), err),
			ControllerReportingPolicyTypeError,
		)
	}

	if diskEncryptionSet.Properties == nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify disk encryption set.",
			fmt.Sprintf("disk encryption set %q has no properties", encryptionSetID.String()),
			ControllerReportingPolicyTypeError,
		)
	}
	if diskEncryptionSet.Location == nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify disk encryption set.",
			fmt.Sprintf("disk encryption set %q has no location", encryptionSetID.String()),
			ControllerReportingPolicyTypeError,
		)
	}

	// Both checks run independently of each other so a caller sees every reason the node
	// pool's disk encryption set can't be used, instead of only the first one found.
	var failureMessages []string
	if diskEncryptionSet.Properties.ProvisioningState == nil || *diskEncryptionSet.Properties.ProvisioningState != diskEncryptionSetProvisioningStateSucceeded {
		failureMessages = append(failureMessages, fmt.Sprintf("disk encryption set %q is not in a %q provisioning state", encryptionSetID.String(), diskEncryptionSetProvisioningStateSucceeded))
	}
	if !strings.EqualFold(*diskEncryptionSet.Location, nodePool.Location) {
		failureMessages = append(failureMessages, fmt.Sprintf("disk encryption set %q is in location %q but the node pool is in location %q", encryptionSetID.String(), *diskEncryptionSet.Location, nodePool.Location))
	}
	if len(failureMessages) > 0 {
		combined := strings.Join(failureMessages, "; ")
		return FailedValidation("DiskEncryptionSetNotValid", combined, combined)
	}

	internalAndUserMsg := fmt.Sprintf("Disk encryption set %q is provisioned and in the same region as the node pool.", encryptionSetID.String())
	return PassedValidation(coreapi.ControllerConditionReasonAsExpected, internalAndUserMsg, internalAndUserMsg)
}
