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
	"errors"
	"fmt"
	"net/http"
	"time"

	"k8s.io/utils/clock"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azure"
)

const (
	// kmsKeyRBACPropagationGracePeriod is how long after a cluster is created an
	// authorization denial on the etcd KMS key is reported as Unknown rather than
	// Failed. Role assignments on a freshly created vault or identity propagate
	// with lag, and firing immediately would flag a healthy create as Degraded.
	kmsKeyRBACPropagationGracePeriod = 15 * time.Minute

	// Key Vault reports the reason a 403 was returned in the inner error code of
	// its error body; the outer code is the generic "Forbidden" for all of them.
	// The inner code is therefore the only thing that distinguishes a network
	// denial from an authorization denial, and the two have entirely different
	// remediations.
	//
	// TODO: PR #7394 (EtcdEncryptionKMSKeyAccessValidation) parses the same inner
	// codes out of HyperShift's ValidAzureKMSConfig condition message. Whichever
	// of the two merges second should lift these constants, and the keyVaultError
	// type below, into one shared file.
	keyVaultInnerErrorCodeForbiddenByConnection = "ForbiddenByConnection"
	keyVaultInnerErrorCodeForbiddenByRbac       = "ForbiddenByRbac"
)

// EtcdEncryptionKMSKeyReachableValidation validates that the customer-managed
// etcd KMS key is usable, by fetching it from the Key Vault data plane as the
// cluster's "kms" operator identity -- the same identity the KMS sidecar uses
// at runtime.
//
// This validation runs on the service cluster, while the real KMS interaction
// happens on the management cluster. The two do not share a network path, so
// this validation reports a verdict only where its answer generalises to the
// management cluster:
//
//   - Outcomes that do not depend on the network path at all -- the key or
//     version does not exist, or the identity is not authorized on the key --
//     are reported for every cluster.
//   - A public-endpoint network denial is reported only when the cluster
//     declares "visibility: Public", which the API defines as the Key Vault
//     being visible from the internet. The service cluster reaches it over that
//     same public endpoint, so a denial there is a true positive.
//   - For "visibility: Private" clusters, anything network-dependent is Skipped
//     rather than given a verdict: the service cluster has no route to the
//     customer's private endpoint, so neither success nor failure says anything
//     about the management cluster. Those clusters are covered by
//     EtcdEncryptionKMSKeyAccessValidation, which reads HyperShift's observation
//     from the management cluster itself.
type EtcdEncryptionKMSKeyReachableValidation struct {
	tokenRetrieverBuilder     azureclient.MIDataplaneBasedIdentityAccessTokenRetrieverBuilder
	keyVaultKeysClientBuilder azureclient.KeyVaultKeysClientBuilder
	clock                     clock.PassiveClock
}

func NewEtcdEncryptionKMSKeyReachableValidation(
	tokenRetrieverBuilder azureclient.MIDataplaneBasedIdentityAccessTokenRetrieverBuilder,
	keyVaultKeysClientBuilder azureclient.KeyVaultKeysClientBuilder,
	clock clock.PassiveClock,
) *EtcdEncryptionKMSKeyReachableValidation {
	return &EtcdEncryptionKMSKeyReachableValidation{
		tokenRetrieverBuilder:     tokenRetrieverBuilder,
		keyVaultKeysClientBuilder: keyVaultKeysClientBuilder,
		clock:                     clock,
	}
}

func (v *EtcdEncryptionKMSKeyReachableValidation) Name() string {
	return "EtcdEncryptionKMSKeyReachableValidation"
}

var _ ClusterValidation = (*EtcdEncryptionKMSKeyReachableValidation)(nil)

func (v *EtcdEncryptionKMSKeyReachableValidation) Validate(ctx context.Context, clusterSubscription *coreapi.Subscription, cluster *coreapi.Cluster) ValidationResult {
	dataEncryption := cluster.CustomerProperties.Etcd.DataEncryption
	if dataEncryption.KeyManagementMode != metadataapi.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged {
		return SkippedValidation(
			"NotApplicable",
			"The cluster does not use a customer-managed etcd encryption key.",
			fmt.Sprintf("etcd data encryption key management mode is %q, not customer-managed", dataEncryption.KeyManagementMode),
		)
	}

	customerManaged := dataEncryption.CustomerManaged
	if customerManaged == nil || customerManaged.EncryptionType != metadataapi.CustomerManagedEncryptionTypeKMS || customerManaged.Kms == nil {
		return SkippedValidation(
			"NotApplicable",
			"The cluster does not use a customer-managed etcd KMS encryption key.",
			"customer-managed etcd encryption is not configured with a KMS key",
		)
	}

	kms := customerManaged.Kms

	// Managed HSM vaults live under a different DNS suffix and are still being
	// rolled out, so they are out of scope until the key vault type is handled
	// end to end.
	if kms.KeyVaultType == coreapi.KmsKeyVaultTypeManagedHSM {
		return SkippedValidation(
			"NotApplicable",
			"Etcd KMS keys stored in a Managed HSM are not validated.",
			"key vault type is Managed HSM",
		)
	}

	isPublicVault := kms.Visibility == metadataapi.KeyVaultVisibilityPublic

	// #6969 makes the frontend reject a create that omits the kms identity while
	// customer-managed KMS encryption is enabled, so a nil here is our bug and
	// not the customer's. Report it as Unknown so it never blames the user, and
	// keep the guard so a nil cannot panic in the credential path below.
	kmsIdentity := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators[string(azure.ClusterOperatorIdentifierKMS)]
	if kmsIdentity == nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify the etcd encryption key.",
			"cluster has customer-managed KMS encryption but no kms control plane operator identity",
			ControllerReportingPolicyTypeError,
		)
	}

	tokenRetriever, err := v.tokenRetrieverBuilder.Build(cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL, kmsIdentity)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify the etcd encryption key.",
			fmt.Sprintf("failed to build access token retriever for the kms identity: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}

	keysClient, err := v.keyVaultKeysClientBuilder.Build(kms.ActiveKey.VaultName, tokenRetriever)
	if err != nil {
		return UnknownValidation(
			"InternalError",
			"Unable to verify the etcd encryption key.",
			fmt.Sprintf("failed to build key vault keys client: %v", err),
			ControllerReportingPolicyTypeError,
		)
	}

	_, getKeyErr := keysClient.GetKey(ctx, kms.ActiveKey.Name, kms.ActiveKey.Version, nil)
	return v.classifyGetKeyResult(getKeyErr, kms, isPublicVault, v.clusterPastRBACGracePeriod(cluster))
}

// classifyGetKeyResult maps the outcome of the Key Vault GetKey call onto a
// validation result. pastGracePeriod gates authorization denials only; see
// kmsKeyRBACPropagationGracePeriod.
func (v *EtcdEncryptionKMSKeyReachableValidation) classifyGetKeyResult(
	getKeyErr error, kms *coreapi.KmsEncryptionProfile, isPublicVault bool, pastGracePeriod bool,
) ValidationResult {
	keyDescription := fmt.Sprintf("etcd encryption key %q in Key Vault %q", kms.ActiveKey.Name, kms.ActiveKey.VaultName)

	// A success proves the key is usable over the public endpoint. For a private
	// vault the management cluster takes an entirely different route, so this
	// tells us nothing about the path that actually matters.
	if getKeyErr == nil {
		if !isPublicVault {
			return SkippedValidation(
				"NotApplicable",
				"The etcd encryption key is not validated from the service cluster because its Key Vault is private.",
				fmt.Sprintf("reached %s from the service cluster, but visibility is Private so the result is not representative", keyDescription),
			)
		}
		message := fmt.Sprintf("The %s is reachable and usable.", keyDescription)
		return PassedValidation(coreapi.ControllerConditionReasonAsExpected, message, message)
	}

	var responseErr *azcore.ResponseError
	if !errors.As(getKeyErr, &responseErr) {
		// No HTTP response at all: DNS failure, dial timeout, context deadline.
		return v.inconclusive(isPublicVault, keyDescription, fmt.Sprintf("failed to reach the key vault: %v", getKeyErr))
	}

	switch responseErr.StatusCode {
	case http.StatusNotFound:
		// Network-independent: the vault answered, and the key or version is not
		// there. Reported identically for both visibilities.
		message := fmt.Sprintf(
			"The %s does not exist. Verify that the key and the key version %q are correct and that the key has not been deleted.",
			keyDescription, kms.ActiveKey.Version,
		)
		return FailedValidation("KmsKeyNotFound", message, message)

	case http.StatusForbidden:
		innerCode := keyVaultInnerErrorCode(responseErr)

		if innerCode == keyVaultInnerErrorCodeForbiddenByConnection {
			if !isPublicVault {
				return SkippedValidation(
					"NotApplicable",
					"The etcd encryption key is not validated from the service cluster because its Key Vault is private.",
					fmt.Sprintf("key vault denied the public network path to %s, which is expected for a Private vault", keyDescription),
				)
			}
			message := fmt.Sprintf(
				"The %s cannot be reached because the Key Vault denied network access. "+
					"The cluster was created with etcd.dataEncryption.customerManaged.kms.visibility %q, which requires the Key Vault to allow public network access. "+
					"Either enable public network access on the Key Vault, or delete and recreate the cluster with visibility %q together with an approved private endpoint for the Key Vault "+
					"and a privatelink.vaultcore.azure.net private DNS zone linked to the cluster's virtual network.",
				keyDescription, metadataapi.KeyVaultVisibilityPublic, metadataapi.KeyVaultVisibilityPrivate,
			)
			return FailedValidation("KeyVaultNetworkAccessDenied", message, message)
		}

		// For a 403, only an explicit ForbiddenByRbac code establishes that
		// authorization caused the denial. A missing or new inner code is
		// inconclusive; do not guess at a remediation or fail a private vault
		// based on a service-cluster network response.
		if innerCode != keyVaultInnerErrorCodeForbiddenByRbac {
			return v.inconclusive(isPublicVault, keyDescription, fmt.Sprintf("key vault returned HTTP 403 with unrecognised inner code %q", innerCode))
		}
		fallthrough

	case http.StatusUnauthorized:
		// Explicit RBAC denials and 401 responses can reflect role-propagation
		// lag right after a create, so hold them as Unknown until the grace
		// period has elapsed.
		if !pastGracePeriod {
			return UnknownValidation(
				"KeyVaultProbeInconclusive",
				"Verifying access to the etcd encryption key.",
				fmt.Sprintf("key vault returned HTTP %d for %s, within the role propagation grace period", responseErr.StatusCode, keyDescription),
				ControllerReportingPolicyTypeLogOnly,
			)
		}

		message := fmt.Sprintf(
			"The cluster's kms managed identity is not authorized to use the %s. "+
				"Grant that identity permission to get, wrap and unwrap the key, for example through the Key Vault Crypto User role.",
			keyDescription,
		)
		return FailedValidation("KeyVaultAccessDenied", message, message)

	default:
		return v.inconclusive(isPublicVault, keyDescription, fmt.Sprintf("key vault returned HTTP %d (%s)", responseErr.StatusCode, responseErr.ErrorCode))
	}
}

// inconclusive reports a probe that produced no trustworthy answer. For a
// private vault it is Skipped rather than Unknown, because the service cluster
// was never expected to reach the vault and an Unknown would permanently hold
// the cluster's RequirementsValid condition in a degraded state.
func (v *EtcdEncryptionKMSKeyReachableValidation) inconclusive(isPublicVault bool, keyDescription string, internalMessage string) ValidationResult {
	if !isPublicVault {
		return SkippedValidation(
			"NotApplicable",
			"The etcd encryption key is not validated from the service cluster because its Key Vault is private.",
			internalMessage,
		)
	}
	return UnknownValidation(
		"KeyVaultProbeInconclusive",
		"Verifying access to the etcd encryption key.",
		fmt.Sprintf("could not determine whether %s is usable: %s", keyDescription, internalMessage),
		ControllerReportingPolicyTypeLogOnly,
	)
}

// clusterPastRBACGracePeriod reports whether the cluster was created long enough
// ago that a role assignment on the etcd KMS key should have propagated. A
// missing CreatedAt is treated as "old enough" so a malformed document does not
// pin the validation in Unknown forever.
func (v *EtcdEncryptionKMSKeyReachableValidation) clusterPastRBACGracePeriod(cluster *coreapi.Cluster) bool {
	if cluster.SystemData == nil || cluster.SystemData.CreatedAt == nil {
		return true
	}
	return v.clock.Since(*cluster.SystemData.CreatedAt) > kmsKeyRBACPropagationGracePeriod
}

// keyVaultInnerErrorCode extracts the inner error code from a Key Vault error
// response body. It returns an empty string when the body is absent or does not
// carry one, which callers treat as "unattributed".
//
// A Key Vault error body looks like:
//
//	{"error":{"code":"Forbidden","message":"...","innererror":{"code":"ForbiddenByConnection"}}}
func keyVaultInnerErrorCode(responseErr *azcore.ResponseError) string {
	if responseErr.RawResponse == nil {
		return ""
	}
	// azcore caches the response body when it builds the ResponseError, so this
	// re-read is safe and does not consume the original stream.
	body, err := azruntime.Payload(responseErr.RawResponse)
	if err != nil || len(body) == 0 {
		return ""
	}

	var parsed struct {
		Error struct {
			InnerError struct {
				Code string `json:"code"`
			} `json:"innererror"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	return parsed.Error.InnerError.Code
}
