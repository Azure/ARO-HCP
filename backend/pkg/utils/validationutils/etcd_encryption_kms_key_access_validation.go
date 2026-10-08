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
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
)

const keyVaultInnerErrorCodeForbiddenByConnection = "ForbiddenByConnection"

// keyVaultResponseStatusRegexp matches the status line azcore.ResponseError renders, e.g. "RESPONSE 403: 403 Forbidden".
var keyVaultResponseStatusRegexp = regexp.MustCompile(`RESPONSE (\d{3})`)

// EtcdEncryptionKMSKeyAccessValidation reports, with an actionable message, when the customer's Key
// Vault rejects the hosted control plane's network access to the customer-managed etcd encryption
// key. It reads HyperShift's ValidAzureKMSConfig condition from the cached HostedCluster instead of
// calling Key Vault itself, because only the management cluster uses the real network path.
//
// It only ever fails or skips. A True condition does not identify the key it evaluated, so it cannot
// be tied to the cluster's current key and is not reported as passed. Failures other than a Key Vault
// network denial for the current key are skipped, because a Failed validation fails the operation and
// those failures can be transient (e.g. role assignment propagation) or not attributable to the customer.
type EtcdEncryptionKMSKeyAccessValidation struct {
	readDesireLister kubeapplierlisters.ReadDesireLister
}

func NewEtcdEncryptionKMSKeyAccessValidation(readDesireLister kubeapplierlisters.ReadDesireLister) *EtcdEncryptionKMSKeyAccessValidation {
	return &EtcdEncryptionKMSKeyAccessValidation{
		readDesireLister: readDesireLister,
	}
}

func (v *EtcdEncryptionKMSKeyAccessValidation) Name() string {
	return "EtcdEncryptionKMSKeyAccessValidation"
}

func (v *EtcdEncryptionKMSKeyAccessValidation) Validate(ctx context.Context, _ *coreapi.Subscription, cluster *coreapi.Cluster) ValidationResult {
	dataEncryption := cluster.CustomerProperties.Etcd.DataEncryption
	if dataEncryption.KeyManagementMode != metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged ||
		dataEncryption.CustomerManaged == nil ||
		dataEncryption.CustomerManaged.EncryptionType != metadataapi.CustomerManagedEncryptionTypeKMS ||
		dataEncryption.CustomerManaged.Kms == nil {
		return SkippedValidation("NotApplicable", "Cluster does not use a customer-managed KMS key for etcd encryption.", "cluster does not use a customer-managed KMS key for etcd encryption")
	}
	kms := dataEncryption.CustomerManaged.Kms

	// HyperShift cannot reach private Key Vaults from the management cluster and does not evaluate
	// ValidAzureKMSConfig for them (OCPBUGS-123727), so the condition carries no signal here.
	if kms.Visibility == metadataapi.KeyVaultVisibilityPrivate {
		return SkippedValidation("NotApplicable", "Key Vault access is not verified for private Key Vaults.", "ValidAzureKMSConfig is not evaluated for private Key Vaults")
	}

	hostedCluster, observed, err := kubeapplierhelpers.GetCachedHostedClusterForCluster(ctx, v.readDesireLister, cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name)
	if err != nil {
		return SkippedValidation("HostedClusterNotObserved", "The hosted cluster has not been observed yet.", fmt.Sprintf("failed to read cached HostedCluster: %v", err))
	}
	if hostedCluster == nil || !observed {
		return SkippedValidation("HostedClusterNotObserved", "The hosted cluster has not been observed yet.", "cached HostedCluster is missing or its last read was not successful")
	}

	condition := meta.FindStatusCondition(hostedCluster.Status.Conditions, string(v1beta1.ValidAzureKMSConfig))
	if condition == nil || condition.Status != metav1.ConditionFalse {
		return SkippedValidation("NoFailureReported", "No etcd encryption key failure is reported.", "HostedCluster ValidAzureKMSConfig condition is missing or not False")
	}

	internalMessage := fmt.Sprintf("HostedCluster ValidAzureKMSConfig=False: %s: %s", condition.Reason, condition.Message)
	if !reportsActiveKey(condition.Message, kms.ActiveKey) {
		return SkippedValidation("NotCurrentKey", "No etcd encryption key failure is reported for the current key.", internalMessage)
	}
	keyVaultErr, ok := parseKeyVaultError(condition.Message)
	if condition.Reason != v1beta1.AzureErrorReason || !ok ||
		keyVaultErr.statusCode != http.StatusForbidden || keyVaultErr.innerCode != keyVaultInnerErrorCodeForbiddenByConnection {
		return SkippedValidation("NotAttributable", "Etcd encryption key access could not be verified.", internalMessage)
	}

	return FailedValidation(
		"KeyVaultNetworkAccessDenied",
		fmt.Sprintf(
			"The control plane cannot reach etcd encryption key %q in Key Vault %q: %s. "+
				"Clusters created with etcd.dataEncryption.customerManaged.kms.visibility %q reach the Key Vault over its public endpoint, so the Key Vault must allow public network access. "+
				"To keep the Key Vault private, delete and recreate the cluster with kms.visibility %q, an approved private endpoint for the Key Vault, "+
				"and a privatelink.vaultcore.azure.net private DNS zone linked to the cluster's virtual network.",
			kms.ActiveKey.Name, kms.ActiveKey.VaultName, keyVaultErr.describe(), metadataapi.KeyVaultVisibilityPublic, metadataapi.KeyVaultVisibilityPrivate),
		internalMessage,
	)
}

// reportsActiveKey reports whether a ValidAzureKMSConfig failure message was produced for the given key.
// HyperShift rewrites the condition's ObservedGeneration when copying it to the HostedCluster, so the key
// named in the message ("(key: <name>/<version>)" and the vault URL) is the only reliable link between
// the result and the cluster's current key.
func reportsActiveKey(conditionMessage string, key coreapi.KmsKey) bool {
	message := strings.ToLower(conditionMessage)
	return strings.Contains(message, strings.ToLower(fmt.Sprintf("(key: %s/%s)", key.Name, key.Version))) &&
		strings.Contains(message, strings.ToLower(fmt.Sprintf("https://%s.", key.VaultName)))
}

// keyVaultError is the subset of a Key Vault error response needed to classify and describe it.
type keyVaultError struct {
	statusCode int
	innerCode  string
	message    string
}

// parseKeyVaultError extracts the Key Vault error from a ValidAzureKMSConfig message. HyperShift
// embeds the azcore.ResponseError string, which contains a "RESPONSE <status>" line followed by the
// JSON error body returned by Key Vault. Both are required; without the body the failure cannot be
// classified.
func parseKeyVaultError(conditionMessage string) (keyVaultError, bool) {
	match := keyVaultResponseStatusRegexp.FindStringSubmatch(conditionMessage)
	if match == nil {
		return keyVaultError{}, false
	}
	statusCode, err := strconv.Atoi(match[1])
	if err != nil {
		return keyVaultError{}, false
	}

	bodyStart := strings.Index(conditionMessage, "{")
	bodyEnd := strings.LastIndex(conditionMessage, "}")
	if bodyStart < 0 || bodyEnd < bodyStart {
		return keyVaultError{}, false
	}
	var body struct {
		Error struct {
			Code       string `json:"code"`
			Message    string `json:"message"`
			InnerError struct {
				Code string `json:"code"`
			} `json:"innererror"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(conditionMessage[bodyStart:bodyEnd+1]), &body); err != nil || body.Error.Code == "" {
		return keyVaultError{}, false
	}
	// Key Vault appends caller and vault details on following lines; only the first line explains the failure.
	firstLine, _, _ := strings.Cut(strings.ReplaceAll(body.Error.Message, "\r\n", "\n"), "\n")
	return keyVaultError{
		statusCode: statusCode,
		innerCode:  body.Error.InnerError.Code,
		message:    strings.TrimSpace(firstLine),
	}, true
}

// describe renders the error for customers, e.g.
// "Key Vault responded with HTTP 403 (ForbiddenByConnection): Public network access is disabled ...".
func (e keyVaultError) describe() string {
	description := fmt.Sprintf("Key Vault responded with HTTP %d (%s)", e.statusCode, e.innerCode)
	if message := strings.TrimRight(e.message, "."); message != "" {
		description += ": " + message
	}
	return description
}
