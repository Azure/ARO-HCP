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

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	internalazure "github.com/Azure/ARO-HCP/internal/azure"
)

// AzureClusterKeyVaultAccessibilityValidation validates that the Azure Key Vault
// used for customer-managed etcd KMS encryption is reachable by the cluster's
// KMS operator managed identity -- the same identity, over the same data
// plane path, that etcd itself uses to perform the real encrypt/decrypt
// operations.
type AzureClusterKeyVaultAccessibilityValidation struct {
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder
}

func NewAzureClusterKeyVaultAccessibilityValidation(
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder,
) *AzureClusterKeyVaultAccessibilityValidation {
	return &AzureClusterKeyVaultAccessibilityValidation{
		smiClientBuilder: smiClientBuilder,
	}
}

func (a *AzureClusterKeyVaultAccessibilityValidation) Name() string {
	return "AzureClusterKeyVaultAccessibilityValidation"
}

func (a *AzureClusterKeyVaultAccessibilityValidation) Validate(ctx context.Context, _ *coreapi.Subscription, cluster *coreapi.HCPOpenShiftCluster) ValidationResult {
	if cluster.CustomerProperties.Etcd.DataEncryption.KeyManagementMode != metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged {
		return PassedValidation(coreapi.ControllerConditionReasonAsExpected, "As expected", "Cluster does not use customer-managed KMS encryption.")
	}

	// API validation (DiscriminatedUnion) guarantees CustomerManaged != nil
	// when KeyManagementMode == CustomerManaged. Guard against data corruption.
	cm := cluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged
	if cm == nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify Key Vault accessibility.",
			"customer-managed key management mode is set but customer-managed encryption profile is nil",
			ControllerReportingPolicyTypeError,
		)
	}

	if cm.EncryptionType != metadataapi.CustomerManagedEncryptionTypeKMS {
		return PassedValidation(coreapi.ControllerConditionReasonAsExpected, "As expected", "Cluster does not use KMS encryption.")
	}

	// API validation (DiscriminatedUnion) guarantees Kms != nil
	// when EncryptionType == KMS. Guard against data corruption.
	if cm.Kms == nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify Key Vault accessibility.",
			"KMS encryption type is set but KMS encryption profile is nil",
			ControllerReportingPolicyTypeError,
		)
	}

	kmsProfile := cm.Kms

	kmsIdentityResourceID := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators[string(internalazure.ClusterOperatorIdentifierKMS)]
	if kmsIdentityResourceID == nil {
		internalAndUserMsg := fmt.Sprintf("cluster has no %q operator identity configured", internalazure.ClusterOperatorIdentifierKMS)
		return FailedValidation("KmsIdentityNotConfigured", internalAndUserMsg, internalAndUserMsg)
	}

	clusterIdentityURL := cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL

	keysClient, err := a.smiClientBuilder.KeyVaultKeysClient(ctx, clusterIdentityURL, kmsIdentityResourceID, kmsProfile.ActiveKey.VaultName)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify Key Vault accessibility.",
			fmt.Sprintf("failed to get key vault client: %s", err),
			ControllerReportingPolicyTypeError,
		)
	}

	if _, err := keysClient.GetKey(ctx, kmsProfile.ActiveKey.Name, kmsProfile.ActiveKey.Version, nil); err != nil {
		internalAndUserMsg := fmt.Sprintf(
			"key vault %q is not accessible to the cluster's KMS operator identity: %s",
			kmsProfile.ActiveKey.VaultName, err)
		return FailedValidation("KeyVaultNotAccessible", internalAndUserMsg, internalAndUserMsg)
	}

	return PassedValidation(coreapi.ControllerConditionReasonAsExpected, "As expected", "Key Vault is accessible to the cluster's KMS operator identity.")
}
