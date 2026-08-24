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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

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
	operatorIdentityClientBuilder azureclient.ClusterOperatorIdentityClientBuilder
}

func NewAzureClusterKeyVaultAccessibilityValidation(
	operatorIdentityClientBuilder azureclient.ClusterOperatorIdentityClientBuilder,
) *AzureClusterKeyVaultAccessibilityValidation {
	return &AzureClusterKeyVaultAccessibilityValidation{
		operatorIdentityClientBuilder: operatorIdentityClientBuilder,
	}
}

func (a *AzureClusterKeyVaultAccessibilityValidation) Name() string {
	return "AzureClusterKeyVaultAccessibilityValidation"
}

func (a *AzureClusterKeyVaultAccessibilityValidation) Validate(ctx context.Context, _ *coreapi.Subscription, cluster *coreapi.HCPOpenShiftCluster) ValidationResult {
	if cluster.CustomerProperties.Etcd.DataEncryption.KeyManagementMode != metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged {
		return SkippedValidation("NotApplicable", "Cluster does not use customer-managed KMS encryption.", "Cluster does not use customer-managed KMS encryption.")
	}

	// Frontend API validation (DiscriminatedUnion) guarantees CustomerManaged != nil
	// when KeyManagementMode == CustomerManaged, and Kms != nil when
	// EncryptionType == KMS. This validation runs on already-validated stored
	// cluster data, so we trust those invariants (consistent with the other
	// backend cluster validations).
	cm := cluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged
	if cm.EncryptionType != metadataapi.CustomerManagedEncryptionTypeKMS {
		return SkippedValidation("NotApplicable", "Cluster does not use KMS encryption.", "Cluster does not use KMS encryption.")
	}

	kmsProfile := cm.Kms

	// The frontend RP guarantees (via validateKmsIdentityRequirement) that the
	// "kms" operator identity is configured whenever customer-managed KMS
	// encryption is selected. If it is missing here it's an internal
	// inconsistency rather than a user error, so report Unknown, not Failed.
	kmsIdentityResourceID := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators[string(internalazure.ClusterOperatorIdentifierKMS)]
	if kmsIdentityResourceID == nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify Key Vault accessibility.",
			fmt.Sprintf("cluster has no %q operator identity configured, but the RP should guarantee it for customer-managed KMS encryption", internalazure.ClusterOperatorIdentifierKMS),
			ControllerReportingPolicyTypeError,
		)
	}

	clusterIdentityURL := cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL

	keysClient, err := a.operatorIdentityClientBuilder.KeyVaultKeysClient(ctx, clusterIdentityURL, kmsIdentityResourceID, kmsProfile.ActiveKey.VaultName)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify Key Vault accessibility.",
			fmt.Sprintf("failed to get key vault client: %s", err),
			ControllerReportingPolicyTypeError,
		)
	}

	if _, err := keysClient.GetKey(ctx, kmsProfile.ActiveKey.Name, kmsProfile.ActiveKey.Version, nil); err != nil {
		return a.classifyGetKeyError(err, kmsProfile.ActiveKey.VaultName, kmsProfile.ActiveKey.Name)
	}

	return PassedValidation(coreapi.ControllerConditionReasonAsExpected, "As expected", "KMS key is accessible to the cluster's KMS operator identity.")
}

// classifyGetKeyError examines the error from GetKey and returns an appropriate
// ValidationResult based on the error type:
//   - 401/403: Permission denied - the KMS identity lacks access to the Key Vault
//   - 404: Key not found - the specified key does not exist in the vault
//   - Other errors: Unknown - could be transient (network, service outage) or permanent
func (a *AzureClusterKeyVaultAccessibilityValidation) classifyGetKeyError(err error, vaultName, keyName string) ValidationResult {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		switch respErr.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			userMsg := fmt.Sprintf(
				"The KMS operator identity does not have access to Key Vault %q. "+
					"Ensure the identity has the 'Key Vault Crypto User' role on the vault.",
				vaultName)
			internalMsg := fmt.Sprintf(
				"KMS identity lacks permission to access key vault %q: %s",
				vaultName, err)
			return FailedValidation("KeyVaultAccessDenied", userMsg, internalMsg)

		case http.StatusNotFound:
			userMsg := fmt.Sprintf(
				"KMS key %q was not found in Key Vault %q. "+
					"Ensure the key exists and the name is correct.",
				keyName, vaultName)
			internalMsg := fmt.Sprintf(
				"KMS key %q not found in vault %q: %s",
				keyName, vaultName, err)
			return FailedValidation("KmsKeyNotFound", userMsg, internalMsg)
		}
	}

	// For all other errors (network issues, 5xx, timeouts, etc.), we cannot
	// determine if this is a permanent or transient issue.
	internalMsg := fmt.Sprintf(
		"failed to verify KMS key accessibility in vault %q: %s",
		vaultName, err)
	return UnknownValidation(
		"KeyVaultAccessibilityUnknown",
		"Unable to verify Key Vault accessibility. This may be a transient issue.",
		internalMsg,
		ControllerReportingPolicyTypeLogOnly,
	)
}
