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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// AzureClusterKeyVaultAccessibilityValidation validates that the Azure Key Vault
// used for etcd KMS encryption exists and is network-accessible.
type AzureClusterKeyVaultAccessibilityValidation struct {
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder
}

func NewAzureClusterKeyVaultAccessibilityValidation(
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder,
) *AzureClusterKeyVaultAccessibilityValidation {
	return &AzureClusterKeyVaultAccessibilityValidation{
		azureFPAClientBuilder: azureFPAClientBuilder,
	}
}

func (a *AzureClusterKeyVaultAccessibilityValidation) Name() string {
	return "AzureClusterKeyVaultAccessibilityValidation"
}

func (a *AzureClusterKeyVaultAccessibilityValidation) Validate(
	ctx context.Context, clusterSubscription *arm.Subscription, cluster *api.HCPOpenShiftCluster,
) error {
	kmsProfile := customerManagedKmsProfile(cluster)
	if kmsProfile == nil {
		return nil
	}

	vaultsClient, err := a.azureFPAClientBuilder.KeyVaultVaultsClient(
		*clusterSubscription.Properties.TenantId,
		cluster.ID.SubscriptionID,
	)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get key vault client: %w", err))
	}

	resp, err := vaultsClient.Get(ctx, cluster.ID.ResourceGroupName, kmsProfile.ActiveKey.VaultName, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf(
			"key vault %q is not accessible in resource group %q: %w",
			kmsProfile.ActiveKey.VaultName, cluster.ID.ResourceGroupName, err))
	}

	if err := validateKeyVaultNetworkAccess(resp.Vault, kmsProfile); err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func validateKeyVaultNetworkAccess(vault armkeyvault.Vault, kmsProfile *api.KmsEncryptionProfile) error {
	if vault.Properties == nil {
		return fmt.Errorf("key vault %q has no properties", *vault.Name)
	}

	publicAccess := vault.Properties.PublicNetworkAccess
	isPublicDisabled := publicAccess != nil && strings.EqualFold(*publicAccess, "Disabled")

	if !isPublicDisabled {
		return nil
	}

	if kmsProfile.Visibility == api.KeyVaultVisibilityPublic {
		return fmt.Errorf(
			"key vault %q has public network access disabled, but the cluster is configured with key vault visibility %q. "+
				"Either enable public network access on the key vault or configure the cluster with private key vault visibility "+
				"and ensure a private endpoint and DNS are properly configured",
			kmsProfile.ActiveKey.VaultName, api.KeyVaultVisibilityPublic)
	}

	hasPrivateEndpoints := len(vault.Properties.PrivateEndpointConnections) > 0
	if !hasPrivateEndpoints {
		return fmt.Errorf(
			"key vault %q has public network access disabled but no private endpoint connections are configured. "+
				"Configure a private endpoint and DNS entries so the service can reach the key vault",
			kmsProfile.ActiveKey.VaultName)
	}

	return nil
}

func customerManagedKmsProfile(cluster *api.HCPOpenShiftCluster) *api.KmsEncryptionProfile {
	etcd := cluster.CustomerProperties.Etcd
	if etcd.DataEncryption.KeyManagementMode != api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged {
		return nil
	}
	if etcd.DataEncryption.CustomerManaged == nil {
		return nil
	}
	return etcd.DataEncryption.CustomerManaged.Kms
}
