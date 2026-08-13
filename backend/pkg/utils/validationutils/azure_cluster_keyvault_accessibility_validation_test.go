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
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	internalazure "github.com/Azure/ARO-HCP/internal/azure"
)

const (
	testKeyVaultName          = "test-kv"
	testKeyName               = "etcd-data-kms-encryption-key"
	testKeyVersion            = "abc123"
	testClusterIdentityURL    = "https://identity.example.com/cluster"
	testKmsIdentityResourceID = "/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroup + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/kms-identity"
)

func newTestCluster(t *testing.T, keyManagementMode metadataapi.EtcdDataEncryptionKeyManagementModeType, configureKmsIdentity bool) *coreapi.HCPOpenShiftCluster {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroup +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName))

	cluster := &coreapi.HCPOpenShiftCluster{
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: testClusterName,
			},
			Location: "eastus",
		},
	}
	cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL = testClusterIdentityURL

	if configureKmsIdentity {
		kmsResourceID := metadataapi.Must(azcorearm.ParseResourceID(testKmsIdentityResourceID))
		cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = map[string]*azcorearm.ResourceID{
			string(internalazure.ClusterOperatorIdentifierKMS): kmsResourceID,
		}
	}

	if keyManagementMode == metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged {
		cluster.CustomerProperties.Etcd = coreapi.EtcdProfile{
			DataEncryption: coreapi.EtcdDataEncryptionProfile{
				KeyManagementMode: metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged,
				CustomerManaged: &coreapi.CustomerManagedEncryptionProfile{
					EncryptionType: metadataapi.CustomerManagedEncryptionTypeKMS,
					Kms: &coreapi.KmsEncryptionProfile{
						Visibility: metadataapi.KeyVaultVisibilityPublic,
						ActiveKey: coreapi.KmsKey{
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

func TestAzureClusterKeyVaultAccessibilityValidation_Name(t *testing.T) {
	validation := NewAzureClusterKeyVaultAccessibilityValidation(nil)
	assert.Equal(t, "AzureClusterKeyVaultAccessibilityValidation", validation.Name())
}

func TestAzureClusterKeyVaultAccessibilityValidation_Validate(t *testing.T) {
	ctx := context.Background()
	subscription := newTestSubscription()

	tests := []struct {
		name        string
		cluster     *coreapi.HCPOpenShiftCluster
		setupMock   func(builder *azureclient.MockClusterOperatorIdentityClientBuilder, keysClient *azureclient.MockKeyVaultKeysClient)
		wantOutcome OutcomeType
		wantReason  string
	}{
		{
			name:        "skips validation when not using customer-managed encryption",
			cluster:     newTestCluster(t, "", false),
			wantOutcome: OutcomeTypeSkipped,
			wantReason:  "NotApplicable",
		},
		{
			name: "skips validation for non-KMS encryption types",
			cluster: func() *coreapi.HCPOpenShiftCluster {
				c := newTestCluster(t, metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged, true)
				c.CustomerProperties.Etcd.DataEncryption.CustomerManaged.EncryptionType = "FutureType"
				c.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms = nil
				return c
			}(),
			wantOutcome: OutcomeTypeSkipped,
			wantReason:  "NotApplicable",
		},
		{
			name:    "succeeds when the key vault is accessible to the KMS operator identity",
			cluster: newTestCluster(t, metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged, true),
			setupMock: func(builder *azureclient.MockClusterOperatorIdentityClientBuilder, keysClient *azureclient.MockKeyVaultKeysClient) {
				builder.EXPECT().KeyVaultKeysClient(gomock.Any(), testClusterIdentityURL, gomock.Any(), testKeyVaultName).Return(keysClient, nil)
				keysClient.EXPECT().GetKey(gomock.Any(), testKeyName, testKeyVersion, nil).Return(azkeys.GetKeyResponse{}, nil)
			},
			wantOutcome: OutcomeTypePassed,
		},
		{
			name:        "reports unknown when the cluster has no KMS operator identity configured (RP should prevent this)",
			cluster:     newTestCluster(t, metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged, false),
			wantOutcome: OutcomeTypeUnknown,
			wantReason:  "InternalError",
		},
		{
			name:    "reports unknown when the client builder returns an error",
			cluster: newTestCluster(t, metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged, true),
			setupMock: func(builder *azureclient.MockClusterOperatorIdentityClientBuilder, keysClient *azureclient.MockKeyVaultKeysClient) {
				builder.EXPECT().KeyVaultKeysClient(gomock.Any(), testClusterIdentityURL, gomock.Any(), testKeyVaultName).Return(nil, errors.New("credential error"))
			},
			wantOutcome: OutcomeTypeUnknown,
			wantReason:  "InternalError",
		},
		{
			name:    "fails with access denied when GetKey returns 403 Forbidden",
			cluster: newTestCluster(t, metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged, true),
			setupMock: func(builder *azureclient.MockClusterOperatorIdentityClientBuilder, keysClient *azureclient.MockKeyVaultKeysClient) {
				builder.EXPECT().KeyVaultKeysClient(gomock.Any(), testClusterIdentityURL, gomock.Any(), testKeyVaultName).Return(keysClient, nil)
				keysClient.EXPECT().GetKey(gomock.Any(), testKeyName, testKeyVersion, nil).Return(azkeys.GetKeyResponse{}, &azcore.ResponseError{StatusCode: http.StatusForbidden})
			},
			wantOutcome: OutcomeTypeFailed,
			wantReason:  "KeyVaultAccessDenied",
		},
		{
			name:    "fails with access denied when GetKey returns 401 Unauthorized",
			cluster: newTestCluster(t, metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged, true),
			setupMock: func(builder *azureclient.MockClusterOperatorIdentityClientBuilder, keysClient *azureclient.MockKeyVaultKeysClient) {
				builder.EXPECT().KeyVaultKeysClient(gomock.Any(), testClusterIdentityURL, gomock.Any(), testKeyVaultName).Return(keysClient, nil)
				keysClient.EXPECT().GetKey(gomock.Any(), testKeyName, testKeyVersion, nil).Return(azkeys.GetKeyResponse{}, &azcore.ResponseError{StatusCode: http.StatusUnauthorized})
			},
			wantOutcome: OutcomeTypeFailed,
			wantReason:  "KeyVaultAccessDenied",
		},
		{
			name:    "fails with key not found when GetKey returns 404 Not Found",
			cluster: newTestCluster(t, metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged, true),
			setupMock: func(builder *azureclient.MockClusterOperatorIdentityClientBuilder, keysClient *azureclient.MockKeyVaultKeysClient) {
				builder.EXPECT().KeyVaultKeysClient(gomock.Any(), testClusterIdentityURL, gomock.Any(), testKeyVaultName).Return(keysClient, nil)
				keysClient.EXPECT().GetKey(gomock.Any(), testKeyName, testKeyVersion, nil).Return(azkeys.GetKeyResponse{}, &azcore.ResponseError{StatusCode: http.StatusNotFound})
			},
			wantOutcome: OutcomeTypeFailed,
			wantReason:  "KmsKeyNotFound",
		},
		{
			name:    "returns unknown for other errors (network, 5xx, etc.)",
			cluster: newTestCluster(t, metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged, true),
			setupMock: func(builder *azureclient.MockClusterOperatorIdentityClientBuilder, keysClient *azureclient.MockKeyVaultKeysClient) {
				builder.EXPECT().KeyVaultKeysClient(gomock.Any(), testClusterIdentityURL, gomock.Any(), testKeyVaultName).Return(keysClient, nil)
				keysClient.EXPECT().GetKey(gomock.Any(), testKeyName, testKeyVersion, nil).Return(azkeys.GetKeyResponse{}, errors.New("connection timeout"))
			},
			wantOutcome: OutcomeTypeUnknown,
			wantReason:  "KeyVaultAccessibilityUnknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockBuilder := azureclient.NewMockClusterOperatorIdentityClientBuilder(ctrl)
			mockKeysClient := azureclient.NewMockKeyVaultKeysClient(ctrl)

			if tt.setupMock != nil {
				tt.setupMock(mockBuilder, mockKeysClient)
			}

			validation := NewAzureClusterKeyVaultAccessibilityValidation(mockBuilder)
			result := validation.Validate(ctx, subscription, tt.cluster)

			require.NoError(t, result.Validate(), "result should be well-formed")
			assert.Equal(t, tt.wantOutcome, result.Outcome.Type)

			if tt.wantReason != "" {
				assert.Equal(t, tt.wantReason, result.Reason())
			}
		})
	}
}
