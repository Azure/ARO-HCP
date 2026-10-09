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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
)

const (
	testKMSKeyVersion     = "0123456789abcdef0123456789abcdef"
	previousKMSKeyVersion = "fedcba9876543210fedcba9876543210"
)

// keyVaultConditionMessage renders a ValidAzureKMSConfig message the way HyperShift reports a
// failed Key Vault encrypt call: the azcore.ResponseError string with the Key Vault JSON body.
func keyVaultConditionMessage(statusCode int, statusText, code, message, innerCode string) string {
	innerError := ""
	if innerCode != "" {
		innerError = fmt.Sprintf(",\n    \"innererror\": {\n      \"code\": %q\n    }", innerCode)
	}
	return fmt.Sprintf("failed to encrypt data using KMS (key: etcd-kms-key/%[6]s): "+
		"POST https://example-kv.vault.azure.net/keys/etcd-kms-key/%[6]s/encrypt\n"+
		"--------------------------------------------------------------------------------\n"+
		"RESPONSE %[1]d: %[1]d %[2]s\n"+
		"ERROR CODE: %[3]s\n"+
		"--------------------------------------------------------------------------------\n"+
		"{\n  \"error\": {\n    \"code\": %[3]q,\n    \"message\": %[4]q%[5]s\n  }\n}\n"+
		"--------------------------------------------------------------------------------\n",
		statusCode, statusText, code, message, innerError, testKMSKeyVersion)
}

func etcdKMSTestCluster(keyManagementMode metadataapi.EtcdDataEncryptionKeyManagementModeType, visibility metadataapi.KeyVaultVisibility) *coreapi.Cluster {
	cluster := &coreapi.Cluster{
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID: mustParseResourceID(fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s",
					operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName, operationtesting.TestClusterName)),
			},
		},
	}
	cluster.CustomerProperties.Etcd.DataEncryption.KeyManagementMode = keyManagementMode
	if keyManagementMode == metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged {
		cluster.CustomerProperties.Etcd.DataEncryption.CustomerManaged = &coreapi.CustomerManagedEncryptionProfile{
			EncryptionType: metadataapi.CustomerManagedEncryptionTypeKMS,
			Kms: &coreapi.KmsEncryptionProfile{
				Visibility: visibility,
				ActiveKey: coreapi.KmsKey{
					Name:      "etcd-kms-key",
					VaultName: "example-kv",
					Version:   testKMSKeyVersion,
				},
			},
		}
	}
	return cluster
}

func hostedClusterReadDesireWithKMSCondition(t *testing.T, condition *metav1.Condition, readDesireConditions ...metav1.Condition) *kubeapplierapi.ReadDesire {
	t.Helper()
	hostedCluster := &v1beta1.HostedCluster{}
	if condition != nil {
		hostedCluster.Status.Conditions = []metav1.Condition{*condition}
	}
	return operationtesting.NewHostedClusterReadDesire(t, hostedCluster, readDesireConditions...)
}

func kmsCondition(status metav1.ConditionStatus, reason, message string) *metav1.Condition {
	return &metav1.Condition{
		Type:    string(v1beta1.ValidAzureKMSConfig),
		Status:  status,
		Reason:  reason,
		Message: message,
	}
}

func TestEtcdEncryptionKMSKeyAccessValidation(t *testing.T) {
	customerManaged := metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged
	networkDenied := keyVaultConditionMessage(403, "Forbidden", "Forbidden",
		"Public network access is disabled and request is not from a trusted service nor via an approved private link.\r\n"+
			"Caller: appid=00000000-0000-0000-0000-000000000001;oid=00000000-0000-0000-0000-000000000002\r\nVault: example-kv;location=eastus2",
		"ForbiddenByConnection")

	tests := []struct {
		name                string
		cluster             *coreapi.Cluster
		readDesires         []*kubeapplierapi.ReadDesire
		expectedOutcome     OutcomeType
		expectedReason      string
		expectedUserMessage string
	}{
		{
			name:            "platform-managed keys are not applicable",
			cluster:         etcdKMSTestCluster(metadataapi.EtcdDataEncryptionKeyManagementModeTypePlatformManaged, ""),
			expectedOutcome: OutcomeTypeSkipped,
			expectedReason:  "NotApplicable",
		},
		{
			name:            "private Key Vaults are not evaluated even if the condition is False",
			cluster:         etcdKMSTestCluster(customerManaged, metadataapi.KeyVaultVisibilityPrivate),
			readDesires:     []*kubeapplierapi.ReadDesire{hostedClusterReadDesireWithKMSCondition(t, kmsCondition(metav1.ConditionFalse, v1beta1.AzureErrorReason, networkDenied))},
			expectedOutcome: OutcomeTypeSkipped,
			expectedReason:  "NotApplicable",
		},
		{
			name:            "hosted cluster not cached yet",
			cluster:         etcdKMSTestCluster(customerManaged, metadataapi.KeyVaultVisibilityPublic),
			expectedOutcome: OutcomeTypeSkipped,
			expectedReason:  "HostedClusterNotObserved",
		},
		{
			name:    "hosted cluster read not successful",
			cluster: etcdKMSTestCluster(customerManaged, metadataapi.KeyVaultVisibilityPublic),
			readDesires: []*kubeapplierapi.ReadDesire{hostedClusterReadDesireWithKMSCondition(t, kmsCondition(metav1.ConditionFalse, v1beta1.AzureErrorReason, networkDenied),
				metav1.Condition{Type: kubeapplierapi.ConditionTypeSuccessful, Status: metav1.ConditionFalse, Reason: "Failed"})},
			expectedOutcome: OutcomeTypeSkipped,
			expectedReason:  "HostedClusterNotObserved",
		},
		{
			name:            "condition not reported yet",
			cluster:         etcdKMSTestCluster(customerManaged, metadataapi.KeyVaultVisibilityPublic),
			readDesires:     []*kubeapplierapi.ReadDesire{hostedClusterReadDesireWithKMSCondition(t, nil)},
			expectedOutcome: OutcomeTypeSkipped,
			expectedReason:  "NoFailureReported",
		},
		{
			name:            "condition Unknown",
			cluster:         etcdKMSTestCluster(customerManaged, metadataapi.KeyVaultVisibilityPublic),
			readDesires:     []*kubeapplierapi.ReadDesire{hostedClusterReadDesireWithKMSCondition(t, kmsCondition(metav1.ConditionUnknown, "StatusUnknown", ""))},
			expectedOutcome: OutcomeTypeSkipped,
			expectedReason:  "NoFailureReported",
		},
		{
			name:            "condition True is not reported as passed because it does not identify the evaluated key",
			cluster:         etcdKMSTestCluster(customerManaged, metadataapi.KeyVaultVisibilityPublic),
			readDesires:     []*kubeapplierapi.ReadDesire{hostedClusterReadDesireWithKMSCondition(t, kmsCondition(metav1.ConditionTrue, "AsExpected", "All is well"))},
			expectedOutcome: OutcomeTypeSkipped,
			expectedReason:  "NoFailureReported",
		},
		{
			name:            "public network access disabled on the Key Vault",
			cluster:         etcdKMSTestCluster(customerManaged, metadataapi.KeyVaultVisibilityPublic),
			readDesires:     []*kubeapplierapi.ReadDesire{hostedClusterReadDesireWithKMSCondition(t, kmsCondition(metav1.ConditionFalse, v1beta1.AzureErrorReason, networkDenied))},
			expectedOutcome: OutcomeTypeFailed,
			expectedReason:  "KeyVaultNetworkAccessDenied",
			expectedUserMessage: `The control plane cannot reach etcd encryption key "etcd-kms-key" in Key Vault "example-kv": ` +
				`Key Vault responded with HTTP 403 (ForbiddenByConnection): Public network access is disabled and request is not from a trusted service nor via an approved private link. ` +
				`Clusters created with etcd.dataEncryption.customerManaged.kms.visibility "Public" reach the Key Vault over its public endpoint, so the Key Vault must allow public network access. ` +
				`To keep the Key Vault private, delete and recreate the cluster with kms.visibility "Private", an approved private endpoint for the Key Vault, ` +
				`and a privatelink.vaultcore.azure.net private DNS zone linked to the cluster's virtual network.`,
		},
		{
			name:    "failure reported for a previous key version is not attributed to the current key",
			cluster: etcdKMSTestCluster(customerManaged, metadataapi.KeyVaultVisibilityPublic),
			readDesires: []*kubeapplierapi.ReadDesire{hostedClusterReadDesireWithKMSCondition(t, kmsCondition(metav1.ConditionFalse, v1beta1.AzureErrorReason,
				strings.ReplaceAll(networkDenied, testKMSKeyVersion, previousKMSKeyVersion)))},
			expectedOutcome: OutcomeTypeSkipped,
			expectedReason:  "NotCurrentKey",
		},
		{
			name:    "authorization failures are not attributed to the customer because they can be transient",
			cluster: etcdKMSTestCluster(customerManaged, metadataapi.KeyVaultVisibilityPublic),
			readDesires: []*kubeapplierapi.ReadDesire{hostedClusterReadDesireWithKMSCondition(t, kmsCondition(metav1.ConditionFalse, v1beta1.AzureErrorReason,
				keyVaultConditionMessage(403, "Forbidden", "Forbidden",
					"Caller is not authorized to perform action on resource.\r\nIf role assignments, deny assignments or role definitions were changed recently, please observe propagation time.",
					"ForbiddenByRbac")))},
			expectedOutcome: OutcomeTypeSkipped,
			expectedReason:  "NotAttributable",
		},
		{
			name:    "key not found is not reported by this validation",
			cluster: etcdKMSTestCluster(customerManaged, metadataapi.KeyVaultVisibilityPublic),
			readDesires: []*kubeapplierapi.ReadDesire{hostedClusterReadDesireWithKMSCondition(t, kmsCondition(metav1.ConditionFalse, v1beta1.AzureErrorReason,
				keyVaultConditionMessage(404, "Not Found", "KeyNotFound", "A key with (name/id) etcd-kms-key was not found in this key vault.", "")))},
			expectedOutcome: OutcomeTypeSkipped,
			expectedReason:  "NotAttributable",
		},
		{
			name:    "Key Vault server errors are not attributed to the customer",
			cluster: etcdKMSTestCluster(customerManaged, metadataapi.KeyVaultVisibilityPublic),
			readDesires: []*kubeapplierapi.ReadDesire{hostedClusterReadDesireWithKMSCondition(t, kmsCondition(metav1.ConditionFalse, v1beta1.AzureErrorReason,
				keyVaultConditionMessage(503, "Service Unavailable", "ServiceUnavailable", "The service is unavailable.", "")))},
			expectedOutcome: OutcomeTypeSkipped,
			expectedReason:  "NotAttributable",
		},
		{
			name:    "platform credential failures are not attributed to the customer",
			cluster: etcdKMSTestCluster(customerManaged, metadataapi.KeyVaultVisibilityPublic),
			readDesires: []*kubeapplierapi.ReadDesire{hostedClusterReadDesireWithKMSCondition(t, kmsCondition(metav1.ConditionFalse, v1beta1.InvalidAzureCredentialsReason,
				networkDenied))},
			expectedOutcome: OutcomeTypeSkipped,
			expectedReason:  "NotAttributable",
		},
		{
			name:    "403 without a Key Vault error body is not attributed to the customer",
			cluster: etcdKMSTestCluster(customerManaged, metadataapi.KeyVaultVisibilityPublic),
			readDesires: []*kubeapplierapi.ReadDesire{hostedClusterReadDesireWithKMSCondition(t, kmsCondition(metav1.ConditionFalse, v1beta1.AzureErrorReason,
				networkDenied[:strings.Index(networkDenied, "{")]))},
			expectedOutcome: OutcomeTypeSkipped,
			expectedReason:  "NotAttributable",
		},
		{
			name:    "messages without a Key Vault response are not attributed to the customer",
			cluster: etcdKMSTestCluster(customerManaged, metadataapi.KeyVaultVisibilityPublic),
			readDesires: []*kubeapplierapi.ReadDesire{hostedClusterReadDesireWithKMSCondition(t, kmsCondition(metav1.ConditionFalse, v1beta1.AzureErrorReason,
				"failed to encrypt data using KMS (key: etcd-kms-key/"+testKMSKeyVersion+"): Post \"https://example-kv.vault.azure.net/keys\": context deadline exceeded"))},
			expectedOutcome: OutcomeTypeSkipped,
			expectedReason:  "NotAttributable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validation := NewEtcdEncryptionKMSKeyAccessValidation(&kubeapplierlistertesting.SliceReadDesireLister{Desires: tt.readDesires})

			result := validation.Validate(context.Background(), testSubscription(), tt.cluster)

			require.NoError(t, result.Validate(), "validation returned a malformed result")
			assert.Equal(t, tt.expectedOutcome, result.Outcome.Type, "unexpected outcome; internal message: %s", result.InternalMessage())
			assert.Equal(t, tt.expectedReason, result.Reason(), "unexpected reason")
			if tt.expectedUserMessage != "" {
				assert.Equal(t, tt.expectedUserMessage, result.ToCondition(validation.Name()).Message, "unexpected user message")
			}
			assert.NotContains(t, result.ToCondition(validation.Name()).Message, "Caller:", "user message must not include Key Vault caller details")
		})
	}
}
