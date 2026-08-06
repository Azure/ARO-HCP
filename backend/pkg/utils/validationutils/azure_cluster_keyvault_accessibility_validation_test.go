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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

const (
	testKeyVaultName = "test-kv"
	testKeyName      = "etcd-data-kms-encryption-key"
	testKeyVersion   = "abc123"
)

func newTestCluster(t *testing.T, keyManagementMode api.EtcdDataEncryptionKeyManagementModeType, visibility api.KeyVaultVisibility) *api.HCPOpenShiftCluster {
	t.Helper()
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroup +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName))

	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: testClusterName,
			},
			Location: "eastus",
		},
	}

	if keyManagementMode == api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged {
		cluster.CustomerProperties.Etcd = api.EtcdProfile{
			DataEncryption: api.EtcdDataEncryptionProfile{
				KeyManagementMode: api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
				CustomerManaged: &api.CustomerManagedEncryptionProfile{
					EncryptionType: api.CustomerManagedEncryptionTypeKMS,
					Kms: &api.KmsEncryptionProfile{
						Visibility: visibility,
						ActiveKey: api.KmsKey{
							Name:      testKeyName,
							VaultName: testKeyVaultName,
							Version:   testKeyVersion,
						},
					},
				},
			},
		}
	}

	return cluster
}

func makeVaultResponse(publicNetworkAccess string, privateEndpointCount int) armkeyvault.VaultsClientGetResponse {
	var peConnections []*armkeyvault.PrivateEndpointConnectionItem
	for i := range privateEndpointCount {
		peConnections = append(peConnections, &armkeyvault.PrivateEndpointConnectionItem{
			ID: ptr.To("pe-" + string(rune('0'+i))),
		})
	}

	return armkeyvault.VaultsClientGetResponse{
		Vault: armkeyvault.Vault{
			Name: ptr.To(testKeyVaultName),
			Properties: &armkeyvault.VaultProperties{
				PublicNetworkAccess:        ptr.To(publicNetworkAccess),
				PrivateEndpointConnections: peConnections,
			},
		},
	}
}

func TestAzureClusterKeyVaultAccessibilityValidation_Name(t *testing.T) {
	validation := NewAzureClusterKeyVaultAccessibilityValidation(nil)
	assert.Equal(t, "AzureClusterKeyVaultAccessibilityValidation", validation.Name())
}

func TestAzureClusterKeyVaultAccessibilityValidation_Validate(t *testing.T) {
	ctx := context.Background()
	subscription := newTestSubscription()

	tests := []struct {
		name         string
		cluster      *api.HCPOpenShiftCluster
		setupMock    func(builder *azureclient.MockFirstPartyApplicationClientBuilder, vaultsClient *azureclient.MockKeyVaultVaultsClient)
		wantErr      string
		wantNoAzCall bool
	}{
		{
			name:         "skips validation when not using customer-managed encryption",
			cluster:      newTestCluster(t, "", api.KeyVaultVisibilityPublic),
			wantNoAzCall: true,
		},
		{
			name:    "succeeds when vault is publicly accessible",
			cluster: newTestCluster(t, api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged, api.KeyVaultVisibilityPublic),
			setupMock: func(builder *azureclient.MockFirstPartyApplicationClientBuilder, vaultsClient *azureclient.MockKeyVaultVaultsClient) {
				builder.EXPECT().KeyVaultVaultsClient(testTenantID, testSubscriptionID).Return(vaultsClient, nil)
				vaultsClient.EXPECT().Get(gomock.Any(), testResourceGroup, testKeyVaultName, nil).
					Return(makeVaultResponse("Enabled", 0), nil)
			},
		},
		{
			name:    "succeeds when vault is private with private endpoints",
			cluster: newTestCluster(t, api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged, api.KeyVaultVisibilityPrivate),
			setupMock: func(builder *azureclient.MockFirstPartyApplicationClientBuilder, vaultsClient *azureclient.MockKeyVaultVaultsClient) {
				builder.EXPECT().KeyVaultVaultsClient(testTenantID, testSubscriptionID).Return(vaultsClient, nil)
				vaultsClient.EXPECT().Get(gomock.Any(), testResourceGroup, testKeyVaultName, nil).
					Return(makeVaultResponse("Disabled", 1), nil)
			},
		},
		{
			name:    "fails when vault has public access disabled but cluster configured for public visibility",
			cluster: newTestCluster(t, api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged, api.KeyVaultVisibilityPublic),
			setupMock: func(builder *azureclient.MockFirstPartyApplicationClientBuilder, vaultsClient *azureclient.MockKeyVaultVaultsClient) {
				builder.EXPECT().KeyVaultVaultsClient(testTenantID, testSubscriptionID).Return(vaultsClient, nil)
				vaultsClient.EXPECT().Get(gomock.Any(), testResourceGroup, testKeyVaultName, nil).
					Return(makeVaultResponse("Disabled", 0), nil)
			},
			wantErr: `key vault "test-kv" has public network access disabled, but the cluster is configured with key vault visibility "Public"`,
		},
		{
			name:    "fails when vault is private with no private endpoints",
			cluster: newTestCluster(t, api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged, api.KeyVaultVisibilityPrivate),
			setupMock: func(builder *azureclient.MockFirstPartyApplicationClientBuilder, vaultsClient *azureclient.MockKeyVaultVaultsClient) {
				builder.EXPECT().KeyVaultVaultsClient(testTenantID, testSubscriptionID).Return(vaultsClient, nil)
				vaultsClient.EXPECT().Get(gomock.Any(), testResourceGroup, testKeyVaultName, nil).
					Return(makeVaultResponse("Disabled", 0), nil)
			},
			wantErr: `key vault "test-kv" has public network access disabled but no private endpoint connections are configured`,
		},
		{
			name:    "fails when vault does not exist",
			cluster: newTestCluster(t, api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged, api.KeyVaultVisibilityPublic),
			setupMock: func(builder *azureclient.MockFirstPartyApplicationClientBuilder, vaultsClient *azureclient.MockKeyVaultVaultsClient) {
				builder.EXPECT().KeyVaultVaultsClient(testTenantID, testSubscriptionID).Return(vaultsClient, nil)
				vaultsClient.EXPECT().Get(gomock.Any(), testResourceGroup, testKeyVaultName, nil).
					Return(armkeyvault.VaultsClientGetResponse{}, errors.New("vault not found"))
			},
			wantErr: `key vault "test-kv" is not accessible in resource group "test-rg"`,
		},
		{
			name:    "fails when client builder returns error",
			cluster: newTestCluster(t, api.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged, api.KeyVaultVisibilityPublic),
			setupMock: func(builder *azureclient.MockFirstPartyApplicationClientBuilder, vaultsClient *azureclient.MockKeyVaultVaultsClient) {
				builder.EXPECT().KeyVaultVaultsClient(testTenantID, testSubscriptionID).Return(nil, errors.New("credential error"))
			},
			wantErr: "failed to get key vault client",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockBuilder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
			mockVaultsClient := azureclient.NewMockKeyVaultVaultsClient(ctrl)

			if tt.setupMock != nil {
				tt.setupMock(mockBuilder, mockVaultsClient)
			}

			validation := NewAzureClusterKeyVaultAccessibilityValidation(mockBuilder)
			err := validation.Validate(ctx, subscription, tt.cluster)

			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.wantErr)
			}
		})
	}
}
